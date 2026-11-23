// Package video resolves Youku episodes and prepares temporary MP4 downloads.
package video

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	ErrInput       = errors.New("invalid Youku URL")
	ErrUnavailable = errors.New("downloader is not configured")
	ErrMissing     = errors.New("episode or download expired")
	ErrBusy        = errors.New("downloader is busy")
	ErrUpstream    = errors.New("unable to obtain video from upstream")
)

const (
	MaxDuration   = 4 * time.Minute
	lifetime      = 10 * time.Minute
	maxVideoBytes = 1 << 30
)

// Episode is a short-lived, user-bound reference to a resolved video.
type Episode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type source struct {
	Episode
	URL *url.URL
}

type episodeList struct {
	user    int64
	expires time.Time
	sources []source
}

type download struct {
	user int64
	name string
	file *os.File
}

// Service owns short-lived episode references and a bounded set of MP4 files.
type Service struct {
	resolver     *url.URL
	origin       string
	dir          string
	bin          string
	client       *http.Client
	resolveSlots chan struct{}
	slots        chan struct{}
	mu           sync.Mutex
	lists        []episodeList
	files        map[string]download
}

// New configures a youku_spi compatible resolver. An empty endpoint disables the tool.
func New(endpoint, origin, dir string) (*Service, error) {
	if endpoint == "" {
		return nil, nil
	}
	resolver, err := url.Parse(endpoint)
	if err != nil || !webURL(resolver) || resolver.Scheme != "https" {
		return nil, errors.New("VIDEO_RESOLVER_URL must be a valid HTTPS URL")
	}
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, errors.New("video downloader requires ffmpeg on PATH")
	}
	return &Service{resolver: resolver, origin: strings.TrimRight(origin, "/"), dir: dir, bin: bin,
		client: publicClient(), resolveSlots: make(chan struct{}, 4), slots: make(chan struct{}, 2), files: make(map[string]download)}, nil
}

func webURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func youkuURL(raw string) (string, error) {
	value, _, _ := strings.Cut(strings.TrimSpace(raw), "&")
	u, err := url.Parse(value)
	if err != nil || !webURL(u) || u.Path == "" || u.Path == "/" || u.Port() != "" || len(value) > 4096 {
		return "", ErrInput
	}
	host := strings.ToLower(u.Hostname())
	if host != "youku.com" && !strings.HasSuffix(host, ".youku.com") {
		return "", ErrInput
	}
	u.Fragment = ""
	return u.String(), nil
}

// Resolve submits the Youku link using the query and response contract of youku_spi.
func (s *Service) Resolve(ctx context.Context, user int64, raw string) ([]Episode, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	videoURL, err := youkuURL(raw)
	if err != nil {
		return nil, err
	}
	select {
	case s.resolveSlots <- struct{}{}:
		defer func() { <-s.resolveSlots }()
	default:
		return nil, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u := *s.resolver
	q := u.Query()
	q.Set("jx", videoURL)
	q.Set("s1ig", "11399")
	q.Set("g", "")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrUpstream
	}
	req.Header.Set("Accept", "application/json, */*")
	if s.origin != "" {
		req.Header.Set("Origin", s.origin)
		req.Header.Set("Referer", s.origin+"/")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, ErrUpstream
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			Source struct {
				Episodes []struct{ URL, Name string } `json:"eps"`
			} `json:"source"`
		} `json:"data"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result) != nil || len(result.Data) == 0 {
		return nil, ErrUpstream
	}
	var sources []source
	var episodes []Episode
	for _, item := range result.Data[0].Source.Episodes {
		media, err := url.Parse(item.URL)
		if err != nil || !webURL(media) || len(item.URL) > 8192 {
			continue
		}
		episode := Episode{rand.Text(), safeName(item.Name)}
		episodes = append(episodes, episode)
		sources = append(sources, source{episode, media})
		if len(episodes) == 300 {
			break
		}
	}
	if len(episodes) == 0 {
		return nil, ErrUpstream
	}
	s.mu.Lock()
	now := time.Now()
	for len(s.lists) > 0 && (len(s.lists) >= 32 || !s.lists[0].expires.After(now)) {
		s.lists[0] = episodeList{}
		s.lists = s.lists[1:]
	}
	s.lists = append(s.lists, episodeList{user, now.Add(lifetime), sources})
	s.mu.Unlock()
	return episodes, nil
}

func safeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	runes := []rune(strings.Trim(name, " ."))
	if len(runes) == 0 {
		return "视频"
	}
	return string(runes[:min(len(runes), 60)])
}

func (s *Service) lookup(user int64, id string) *source {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, list := range s.lists {
		if list.user == user && list.expires.After(time.Now()) {
			for _, item := range list.sources {
				if item.ID == id {
					return &item
				}
			}
		}
	}
	return nil
}

// Prepare downloads and merges one episode, returning a short-lived download ID.
func (s *Service) Prepare(ctx context.Context, user int64, id string) (string, error) {
	if s == nil {
		return "", ErrUnavailable
	}
	item := s.lookup(user, id)
	if item == nil {
		return "", ErrMissing
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return "", ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	file, err := s.merge(ctx, item.URL)
	if err != nil || ctx.Err() != nil {
		if file != nil {
			_ = file.Close()
		}
		<-s.slots
		return "", ErrUpstream
	}
	token := rand.Text()
	s.mu.Lock()
	s.files[token] = download{user, item.Name + ".mp4", file}
	s.mu.Unlock()
	time.AfterFunc(5*time.Minute, func() {
		if expired, _, closeFile := s.Take(user, token); expired != nil {
			closeFile()
		}
	})
	return token, nil
}

// Take claims an MP4 exactly once. The caller must call the returned release function.
func (s *Service) Take(user int64, token string) (*os.File, string, func()) {
	if s == nil {
		return nil, "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.files[token]
	if !ok || item.user != user {
		return nil, "", nil
	}
	delete(s.files, token)
	return item.file, item.name, func() { _ = item.file.Close(); <-s.slots }
}
