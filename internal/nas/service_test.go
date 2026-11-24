package nas

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListMediaShare(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"测试.MP4", "track.flac", ".private.mp4", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	s := &Service{root: root}
	for kind, want := range map[string][]Entry{
		"video":  {{"out", true}, {"测试.MP4", false}},
		"audio":  {{"out", true}, {"track.flac", false}},
		"folder": {{"out", true}},
	} {
		got, err := s.List(".", kind)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("list %s = %v, %v; want %v", kind, got, err, want)
		}
	}
	for _, path := range []string{"../", "../elsewhere", "/tmp", "outside"} {
		if _, err := s.List(path, "folder"); err == nil {
			t.Fatalf("listed outside share: %q", path)
		}
	}
}

func TestMediaWithFFmpeg(t *testing.T) {
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip("integration test requires FFmpeg and FFprobe in PATH")
		}
	}
	dir := t.TempDir()
	for _, share := range []string{"videos", "music"} {
		if err := os.Mkdir(filepath.Join(dir, share), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	generate := func(name string, args ...string) []byte {
		t.Helper()
		file := filepath.Join(dir, name)
		args = append([]string{"-v", "error", "-f", "lavfi", "-i", "sine=duration=0.2"}, args...)
		if data, err := exec.Command(s.ffmpeg, append(args, file)...).CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v: %s", name, err, data)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, video := range []string{"示例.mp4", "示例2.mkv"} {
		source := "videos/" + video
		generate(source, "-f", "lavfi", "-i", "color=size=64x64:duration=0.2", "-c:v", "mpeg4", "-c:a", "aac")
		got, err := s.Convert(ctx, source, "music")
		if err != nil || got != "music/"+video[:len(video)-4]+".mp3" {
			t.Fatalf("convert = %q, %v", got, err)
		}
		mp3, err := exec.Command(s.ffprobe, "-v", "error", "-show_entries", "stream=codec_name,channels", "-of", "default=nw=1", filepath.Join(dir, got)).Output()
		if err != nil || !bytes.Contains(mp3, []byte("codec_name=mp3\nchannels=2")) {
			t.Fatalf("expected stereo MP3, got %q, %v", mp3, err)
		}
		if _, err := s.Convert(ctx, source, "music"); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("expected conversion collision, got %v", err)
		}
	}
	for _, ext := range []string{".MP3", ".flac", ".m4a", ".wav", ".opus"} {
		source := "music/original" + ext
		original := generate(source, "-metadata", "title=春天/旅行")
		name := "music/春天 旅行" + ext
		if got, err := s.Rename(ctx, source); got != name || err != nil {
			t.Fatalf("rename = %q, %v", got, err)
		}
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !bytes.Equal(data, original) {
			t.Fatalf("renamed content changed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, source)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("original filename still exists: %v", err)
		}
	}
	generate("music/collision.flac", "-metadata", "title=春天/旅行")
	if _, err := os.Stat(filepath.Join(dir, "music/春天 旅行.flac")); err != nil {
		t.Fatalf("collision target missing before rename: %v", err)
	}
	if got, err := s.Rename(ctx, "music/collision.flac"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("expected rename collision, got %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "music/collision.flac")); err != nil {
		t.Fatal(err)
	}
	generate("no-title.flac")
	if _, err := s.Rename(ctx, "no-title.flac"); !errors.Is(err, ErrNoTitle) {
		t.Fatalf("expected missing title, got %v", err)
	}
}
