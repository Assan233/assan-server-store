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
	"strings"
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
		mp3, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,channels", "-of", "default=nw=1", filepath.Join(dir, got)).Output()
		if err != nil || !bytes.Contains(mp3, []byte("codec_name=mp3\nchannels=2")) {
			t.Fatalf("expected stereo MP3, got %q, %v", mp3, err)
		}
		if _, err := s.Convert(ctx, source, "music"); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("expected conversion collision, got %v", err)
		}
	}
	t.Run("write titles", func(t *testing.T) {
		if err := exec.Command("python3", "-I", "-c", "import mutagen").Run(); err != nil {
			t.Skip("title integration test requires Python 3 with Mutagen")
		}
		const title = "晴天.Live"
		for _, ext := range []string{".MP3", ".flac", ".m4a", ".ogg", ".opus", ".wma"} {
			source := "music/" + title + ext
			args := []string{"-metadata", "artist=Example"}
			if ext != ".flac" {
				args = append(args, "-metadata", "title=Old title")
			}
			generate(source, args...)
			filename := filepath.Join(dir, source)
			audioHash := func() string {
				t.Helper()
				data, err := exec.Command("ffmpeg", "-v", "error", "-i", filename, "-map", "0:a:0", "-c", "copy", "-f", "hash", "-").CombinedOutput()
				if err != nil {
					t.Fatalf("hash %s: %v: %s", source, err, data)
				}
				return string(data)
			}
			before := audioHash()
			if got, err := s.SetTitle(ctx, source); err != nil || got != (TitleResult{Path: source, Title: title, Changed: true}) {
				t.Fatalf("%s set title = %+v, %v", source, got, err)
			}
			data, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags=title,artist:stream_tags=title,artist", "-of", "default=nw=1", filename).Output()
			text := strings.ToLower(string(data))
			if err != nil || !strings.Contains(text, "tag:title="+strings.ToLower(title)+"\n") || !strings.Contains(text, "tag:artist=example\n") || strings.Contains(text, "old title") || audioHash() != before {
				t.Fatalf("%s title, artist or audio mismatch: %q, %v", source, data, err)
			}
			if got, err := s.SetTitle(ctx, source); err != nil || got != (TitleResult{Path: source, Title: title}) {
				t.Fatalf("%s repeat = %+v, %v", source, got, err)
			}
		}
		const bad = "music/bad.flac"
		filename := filepath.Join(dir, bad)
		original := []byte("corrupt audio")
		if err := os.WriteFile(filename, original, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetTitle(ctx, bad); !errors.Is(err, ErrMedia) {
			t.Fatalf("bad audio: %v", err)
		}
		if data, err := os.ReadFile(filename); err != nil || !bytes.Equal(data, original) {
			t.Fatalf("original altered: %q, %v", data, err)
		}
		for _, source := range []string{"../outside.mp3", "music/ .flac", "music/raw.aac"} {
			if _, err := s.SetTitle(ctx, source); !errors.Is(err, ErrInput) {
				t.Fatalf("%s: %v", source, err)
			}
		}
	})
}
