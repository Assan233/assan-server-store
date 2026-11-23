package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var youkuVideoPath = regexp.MustCompile(`^/v_show/id_([A-Za-z0-9]{1,128})(?:={0,2})\.html$`)

func youkuURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || !webURL(u) || u.Port() != "" || len(raw) > 4096 || !strings.EqualFold(u.Hostname(), "v.youku.com") {
		return "", ErrInput
	}
	match := youkuVideoPath.FindStringSubmatch(u.Path)
	if match == nil {
		return "", ErrInput
	}
	// Rebuild a single-video URL; do not pass user-controlled queries to the extractor.
	return "https://v.youku.com/v_show/id_" + match[1] + ".html", nil
}

type extractedMedia struct{ Title, URL string }

func (s *Service) resolveYTDLP(ctx context.Context, videoURL string) (extractedMedia, error) {
	cmd := exec.CommandContext(ctx, s.ytDLP,
		"--ignore-config", "--no-plugin-dirs", "--no-cache-dir", "--no-playlist",
		"--use-extractors", "youku", "--socket-timeout", "8", "--retries", "0", "--extractor-retries", "0",
		"--simulate", "--format", "best[protocol^=m3u8][ext=mp4]",
		"--print", `{"title":%(title)j,"url":%(url)j}`, "--", videoURL)
	// Keep application credentials and optional CLI configuration out of the subprocess.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "PYTHONUTF8=1"}
	cmd.WaitDelay = time.Second
	var stdout, stderr extractorOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if bytes.Contains(stderr.buf.Bytes(), []byte("Youku server reported error -6004:")) ||
			bytes.Contains(stderr.buf.Bytes(), []byte("Youku said: Sorry, this video is")) {
			return extractedMedia{}, ErrRestricted
		}
		return extractedMedia{}, ErrUpstream
	}
	var result extractedMedia
	if json.Unmarshal(stdout.buf.Bytes(), &result) != nil {
		return extractedMedia{}, ErrUpstream
	}
	return result, nil
}

type extractorOutput struct{ buf bytes.Buffer }

func (b *extractorOutput) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > 64<<10 {
		return 0, ErrUpstream
	}
	return b.buf.Write(p)
}
