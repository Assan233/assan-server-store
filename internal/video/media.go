package video

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var specialIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

func publicIPv4(ip netip.Addr) bool {
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, prefix := range specialIPv4 {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func publicClient() *http.Client {
	transport := &http.Transport{
		IdleConnTimeout: 30 * time.Second, MaxIdleConns: 16, TLSHandshakeTimeout: 8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
			if err != nil {
				return nil, err
			}
			dialer := net.Dialer{Timeout: 5 * time.Second}
			for _, ip := range addresses {
				if publicIPv4(ip.Unmap()) {
					if conn, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port)); err == nil {
						return conn, nil
					}
				}
			}
			return nil, errors.New("no reachable public IPv4 address")
		},
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !webURL(req.URL) || (via[0].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return http.ErrUseLastResponse
		}
		if req.URL.Host != via[0].URL.Host {
			req.Header.Del("Origin")
			req.Header.Del("Referer")
		}
		return nil
	}}
}

type mediaRelay struct {
	client    *http.Client
	host      string
	mu        sync.Mutex
	urls      map[string]*url.URL
	paths     map[string]string
	remaining atomic.Int64
	failed    atomic.Bool
}

var mediaExtension = regexp.MustCompile(`^\.[A-Za-z0-9]{1,8}$`)
var uriAttribute = regexp.MustCompile(`([:,])URI="([^"]+)"`)

func (m *mediaRelay) register(u *url.URL, extension string) (string, error) {
	if !webURL(u) || len(u.String()) > 8192 {
		return "", ErrUpstream
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := u.String()
	if existing := m.paths[key]; existing != "" {
		return existing, nil
	}
	if len(m.urls) >= 12000 {
		return "", ErrUpstream
	}
	if extension == "" {
		extension = path.Ext(u.Path)
		if !mediaExtension.MatchString(extension) {
			extension = ""
		}
	}
	local := fmt.Sprintf("/media/%d%s", len(m.urls), extension)
	m.paths[key] = local
	m.urls[local] = u
	return local, nil
}

func (m *mediaRelay) playlist(base *url.URL, data []byte) ([]byte, error) {
	var out bytes.Buffer
	scan := bufio.NewScanner(bytes.NewReader(data))
	rewrite := func(value string) (string, error) {
		u, err := base.Parse(value)
		if err != nil {
			return "", err
		}
		return m.register(u, "")
	}
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			var err error
			line, err = rewrite(line)
			if err != nil {
				return nil, err
			}
		} else if strings.HasPrefix(line, "#EXT") {
			var invalid bool
			line = uriAttribute.ReplaceAllStringFunc(line, func(attr string) string {
				match := uriAttribute.FindStringSubmatch(attr)
				local, err := rewrite(match[2])
				invalid = invalid || err != nil
				return match[1] + `URI="` + local + `"`
			})
			if invalid {
				return nil, ErrUpstream
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes(), scan.Err()
}

func (m *mediaRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fail := func() { m.failed.Store(true); w.WriteHeader(http.StatusBadGateway) }
	// Also acts as FFmpeg's explicit HTTP proxy. Never forward arbitrary targets.
	if r.Method != http.MethodGet || r.Host != m.host || r.URL.RawQuery != "" {
		fail()
		return
	}
	m.mu.Lock()
	upstream := m.urls[r.URL.Path]
	m.mu.Unlock()
	if upstream == nil {
		fail()
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream.String(), nil)
	if err != nil {
		fail()
		return
	}
	if value := r.Header.Get("Range"); value != "" {
		req.Header.Set("Range", value)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		fail()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		fail()
		return
	}
	reader := bufio.NewReader(relayReader{m, resp.Body})
	if prefix, _ := reader.Peek(len("#EXTM3U")); string(prefix) == "#EXTM3U" {
		const maxPlaylist = 2 << 20
		data, err := io.ReadAll(io.LimitReader(reader, maxPlaylist+1))
		if err != nil || len(data) > maxPlaylist {
			fail()
			return
		}
		data, err = m.playlist(resp.Request.URL, data)
		if err != nil {
			fail()
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		_, _ = w.Write(data)
		return
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, reader)
}

type relayReader struct {
	relay *mediaRelay
	body  io.Reader
}

func (r relayReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if r.relay.remaining.Add(-int64(n)) < 0 || (err != nil && err != io.EOF) {
		r.relay.failed.Store(true)
		return 0, ErrUpstream
	}
	return n, err
}

func (s *Service) merge(ctx context.Context, u *url.URL, extension string) (*os.File, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	relay := &mediaRelay{client: s.client, host: listener.Addr().String(), urls: make(map[string]*url.URL), paths: make(map[string]string)}
	relay.remaining.Store(maxVideoBytes)
	entry, err := relay.register(u, extension)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: relay, ReadHeaderTimeout: 3 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	file, err := os.CreateTemp(s.dir, "video-*.mp4")
	if err != nil {
		return nil, err
	}
	// Unlink immediately: the descriptor stays readable until download or expiry.
	if err := os.Remove(file.Name()); err != nil {
		_ = file.Close()
		return nil, err
	}
	local := "http://" + relay.host
	demuxer := "hls"
	if extension == ".mp4" {
		demuxer = "mov"
	}
	cmd := exec.CommandContext(ctx, s.bin, "-nostdin", "-loglevel", "error", "-xerror",
		"-protocol_whitelist", "http,httpproxy,tcp,crypto", "-http_proxy", local, "-f", demuxer, "-i", local+entry,
		"-map", "0:v:0", "-map", "0:a:0?", "-c", "copy", "-movflags", "+frag_keyframe+empty_moov",
		"-fs", fmt.Sprint(maxVideoBytes), "-f", "mp4", "pipe:1")
	// Force the proxy even for nested HLS requests and don't expose API secrets.
	cmd.Env = []string{"http_proxy=" + local}
	cmd.Stdout = file
	if err := cmd.Run(); err != nil || relay.failed.Load() {
		_ = file.Close()
		return nil, ErrUpstream
	}
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil || size == 0 || size >= maxVideoBytes {
		_ = file.Close()
		return nil, ErrUpstream
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
