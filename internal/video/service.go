// Package video resolves video links and prepares temporary MP4 downloads.
package video

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"
)

var (
	ErrInput       = errors.New("invalid video URL")
	ErrUnavailable = errors.New("downloader is not configured")
	ErrMissing     = errors.New("episode or download expired")
	ErrBusy        = errors.New("downloader is busy")
	ErrUpstream    = errors.New("unable to obtain video from upstream")
	ErrRestricted  = errors.New("Youku restricted playback")
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
	URL       *url.URL
	Extension string
}

type resolvedEpisode struct {
	user    int64
	expires time.Time
	source  source
}

type download struct {
	user int64
	name string
	file *os.File
}

// Service owns short-lived episode references and a bounded set of MP4 files.
type Service struct {
	dir          string
	bin          string
	ytDLP        string
	client       *http.Client
	resolveSlots chan struct{}
	slots        chan struct{}
	mu           sync.Mutex
	episodes     []resolvedEpisode
	files        map[string]download
}

// New configures yt-dlp and FFmpeg when the video tool is enabled.
func New(provider, dir string) (*Service, error) {
	switch provider {
	case "", "disabled":
		return nil, nil
	case "yt-dlp":
	default:
		return nil, errors.New("VIDEO_PROVIDER must be yt-dlp or disabled")
	}
	ytDLP, err := exec.LookPath("yt-dlp")
	if err != nil {
		return nil, errors.New("video downloader requires yt-dlp on PATH")
	}
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, errors.New("video downloader requires ffmpeg on PATH")
	}
	return &Service{dir: dir, bin: bin, ytDLP: ytDLP,
		client: publicClient(), resolveSlots: make(chan struct{}, 4), slots: make(chan struct{}, 2), files: make(map[string]download)}, nil
}

func webURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

// Resolve creates an account-bound reference for a direct media URL or a Youku video page.
func (s *Service) Resolve(ctx context.Context, user int64, raw string) ([]Episode, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	raw = strings.TrimSpace(raw)
	media, err := url.Parse(raw)
	if err != nil || !webURL(media) || len(raw) > 4096 {
		return nil, ErrInput
	}
	media.Fragment = ""
	ext := path.Ext(media.Path)
	switch strings.ToLower(ext) {
	case ".mp4", ".m3u8":
		return s.remember(user, media, strings.ToLower(ext), strings.TrimSuffix(path.Base(media.Path), ext)), nil
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
	item, err := s.resolveYTDLP(ctx, videoURL)
	if err != nil {
		return nil, err
	}
	media, err = url.Parse(item.URL)
	if err != nil || !webURL(media) || len(item.URL) > 8192 {
		return nil, ErrUpstream
	}
	return s.remember(user, media, ".m3u8", item.Title), nil
}

func (s *Service) remember(user int64, media *url.URL, extension, title string) []Episode {
	episode := Episode{rand.Text(), safeName(title)}
	s.mu.Lock()
	now := time.Now()
	for len(s.episodes) > 0 && (len(s.episodes) >= 32 || !s.episodes[0].expires.After(now)) {
		s.episodes[0] = resolvedEpisode{}
		s.episodes = s.episodes[1:]
	}
	s.episodes = append(s.episodes, resolvedEpisode{user, now.Add(lifetime), source{episode, media, extension}})
	s.mu.Unlock()
	return []Episode{episode}
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
	now := time.Now()
	for _, episode := range s.episodes {
		if episode.user == user && episode.source.ID == id && episode.expires.After(now) {
			return &episode.source
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
	file, err := s.merge(ctx, item.URL, item.Extension)
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
