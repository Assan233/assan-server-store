//go:build unix

package video

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveAndPrepareEpisode(t *testing.T) {
	bin := os.Getenv("TEST_FFMPEG")
	if bin == "" {
		var err error
		bin, err = exec.LookPath("ffmpeg")
		if err != nil {
			t.Skip("ffmpeg is not installed")
		}
	}
	dir := t.TempDir()
	output, err := exec.Command(bin, "-nostdin", "-f", "lavfi", "-i", "color=size=64x64:rate=24", "-t", "1", "-c:v", "libx264", "-f", "hls", filepath.Join(dir, "stream.m3u8")).CombinedOutput()
	if err != nil {
		t.Fatalf("create HLS fixture: %v: %s", err, output)
	}
	mux := http.NewServeMux()
	mux.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.Dir(dir))))
	upstream := httptest.NewServer(mux)
	defer upstream.Close()
	extractor := filepath.Join(dir, "yt-dlp")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"title\":\"第 1 集\",\"url\":\"" + upstream.URL + "/media/stream.m3u8\"}'\n"
	if err := os.WriteFile(extractor, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s := &Service{ytDLP: extractor, dir: dir, bin: bin,
		client: upstream.Client(), resolveSlots: make(chan struct{}, 1), slots: make(chan struct{}, 1), files: make(map[string]download)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	items, err := s.Resolve(ctx, 7, "https://v.youku.com/v_show/id_fixture.html?foo=bar&other=value")
	if err != nil || len(items) != 1 || items[0].Name != "第 1 集" || items[0].ID == "" {
		t.Fatalf("unexpected episodes: %+v, %v", items, err)
	}
	if _, err := s.Prepare(ctx, 8, items[0].ID); !errors.Is(err, ErrMissing) {
		t.Fatalf("another account could prepare the episode: %v", err)
	}
	token, err := s.Prepare(ctx, 7, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if file, _, _ := s.Take(8, token); file != nil {
		t.Fatal("another account could take the download")
	}
	file, name, release := s.Take(7, token)
	if file == nil || name != "第 1 集.mp4" {
		t.Fatalf("unexpected download %v, %q", file, name)
	}
	defer release()
	data, err := io.ReadAll(file)
	if err != nil || len(data) < 8 || string(data[4:8]) != "ftyp" || !bytes.Contains(data, []byte("mdat")) {
		t.Fatalf("expected an MP4 with media, got %d bytes: %v", len(data), err)
	}
	if file, _, _ := s.Take(7, token); file != nil {
		t.Fatal("download ID was reusable")
	}
}
