// Package nas manages media in a dedicated NAS share.
package nas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrInput reports an invalid share path or unsupported format.
	ErrInput = errors.New("invalid NAS input")
	// ErrBusy reports that another media operation is active.
	ErrBusy = errors.New("NAS media service is busy")
	// ErrMedia reports a read, probe, or encode failure in the media engine.
	ErrMedia = errors.New("media processing failed")
	// ErrNoTitle reports an audio file without a usable title tag.
	ErrNoTitle = errors.New("audio has no usable title")
	// ErrChanged reports that the source was replaced during processing.
	ErrChanged = errors.New("source changed during processing")
)

const (
	// MaxDuration limits a single conversion.
	MaxDuration = 15 * time.Minute
	maxMP3Bytes = 1 << 30
)

var formats = map[string]map[string]string{
	"video": {".mkv": "matroska", ".mp4": "mov"},
	"audio": {".mp3": "mp3", ".flac": "flac", ".m4a": "mov", ".aac": "aac", ".wav": "wav",
		".ogg": "ogg", ".oga": "ogg", ".opus": "ogg", ".wma": "asf", ".aif": "aiff", ".aiff": "aiff", ".ape": "ape"},
}

// Service confines NAS operations to a single configured filesystem root.
type Service struct {
	root            *os.Root
	ffmpeg, ffprobe string
	slot            chan struct{}
}

// New opens the media root. An empty directory setting disables NAS access.
func New(dir string) (*Service, error) {
	if dir == "" {
		return nil, nil
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("locate ffmpeg for NAS media: %w", err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("locate ffprobe for NAS media: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open NAS media directory: %w", err)
	}
	return &Service{root: root, ffmpeg: ffmpeg, ffprobe: ffprobe, slot: make(chan struct{}, 1)}, nil
}

// Close closes the media root.
func (s *Service) Close() error { return s.root.Close() }

// Entry describes a selectable file or a directory in the share.
type Entry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
}

func valid(name string) bool {
	if name == "." {
		return true
	}
	if len(name) > 4096 || !fs.ValidPath(name) || strings.ContainsRune(name, '\\') || strings.ContainsFunc(name, unicode.IsControl) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

// List returns folders and files in the requested media category. The folder
// category returns only directories for the output picker.
func (s *Service) List(dir, kind string) ([]Entry, error) {
	allowed, ok := formats[kind]
	if !valid(dir) || (!ok && kind != "folder") {
		return nil, ErrInput
	}
	f, err := s.root.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries := make([]Entry, 0)
	for {
		batch, err := f.ReadDir(256)
		for _, entry := range batch {
			name := entry.Name()
			if valid(name) && (entry.IsDir() || (entry.Type().IsRegular() && allowed[strings.ToLower(path.Ext(name))] != "")) {
				entries = append(entries, Entry{Name: name, Directory: entry.IsDir()})
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Directory != entries[j].Directory {
			return entries[i].Directory
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func openMedia(root *os.Root, name string) (*os.File, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrInput
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !os.SameFile(info, opened) {
		f.Close()
		return nil, ErrChanged
	}
	return f, nil
}

func (s *Service) acquire() bool {
	select {
	case s.slot <- struct{}{}:
		return true
	default:
		return false
	}
}

func mediaCommand(ctx context.Context, bin string, file *os.File, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	// Pass only the opened, seekable descriptor. The media process cannot
	// open arbitrary local paths or make network requests through FFmpeg IO.
	cmd.ExtraFiles = []*os.File{file}
	cmd.Env = []string{"LC_ALL=C"}
	return cmd
}

// Convert writes the first audio track as MP3 in the chosen existing folder.
func (s *Service) Convert(ctx context.Context, source, dir string) (string, error) {
	demuxer := formats["video"][strings.ToLower(path.Ext(source))]
	if !valid(source) || !valid(dir) || demuxer == "" {
		return "", ErrInput
	}
	if !s.acquire() {
		return "", ErrBusy
	}
	defer func() { <-s.slot }()
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	input, err := openMedia(s.root, source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	out, err := s.root.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer out.Close()
	name := strings.TrimSuffix(path.Base(source), path.Ext(source)) + ".mp3"
	if _, err := out.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			return "", fs.ErrExist
		}
		return "", err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	tmp := ".assan-" + hex.EncodeToString(id[:]) + ".tmp"
	f, err := out.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer out.Remove(tmp)
	cmd := mediaCommand(ctx, s.ffmpeg, input, "-nostdin", "-v", "error", "-xerror",
		"-protocol_whitelist", "fd", "-f", demuxer, "-fd", "3", "-i", "fd:",
		"-map", "0:a:0", "-ac", "2", "-c:a", "libmp3lame", "-q:a", "2", "-fs", fmt.Sprint(maxMP3Bytes), "-f", "mp3", "pipe:1")
	cmd.Stdout = f
	runErr := cmd.Run()
	info, statErr := f.Stat()
	closeErr := f.Close()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if runErr != nil || statErr != nil || closeErr != nil || info.Size() == 0 || info.Size() >= maxMP3Bytes {
		return "", ErrMedia
	}
	if err := moveNoReplace(out, tmp, name); err != nil {
		return "", err
	}
	return path.Join(dir, name), nil
}

// Rename reads the embedded song title and renames the file in its own folder.
// It preserves the original extension and never overwrites an existing entry.
func (s *Service) Rename(ctx context.Context, source string) (string, error) {
	ext := path.Ext(source)
	demuxer := formats["audio"][strings.ToLower(ext)]
	if !valid(source) || demuxer == "" {
		return "", ErrInput
	}
	if !s.acquire() {
		return "", ErrBusy
	}
	defer func() { <-s.slot }()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	dir, base := path.Dir(source), path.Base(source)
	parent, err := s.root.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	input, err := openMedia(parent, base)
	if err != nil {
		return "", err
	}
	defer input.Close()
	cmd := mediaCommand(ctx, s.ffprobe, input, "-v", "error", "-protocol_whitelist", "fd", "-f", demuxer,
		"-fd", "3", "-select_streams", "a:0", "-show_entries", "format_tags=title:stream=index:stream_tags=title", "-of", "json", "-i", "fd:")
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var result struct {
		Format  struct{ Tags map[string]string }
		Streams []struct{ Tags map[string]string }
	}
	if err != nil || json.Unmarshal(data, &result) != nil || len(result.Streams) == 0 {
		return "", ErrMedia
	}
	name := cleanTitle(titleTag(result.Format.Tags))
	if name == "" {
		name = cleanTitle(titleTag(result.Streams[0].Tags))
	}
	if name == "" {
		return "", ErrNoTitle
	}
	name += ext
	if name == base {
		return source, nil
	}
	info, err := input.Stat()
	if err != nil {
		return "", err
	}
	current, err := parent.Lstat(base)
	if err != nil || !os.SameFile(info, current) {
		return "", ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := moveNoReplace(parent, base, name); err != nil {
		return "", err
	}
	return path.Join(dir, name), nil
}

func titleTag(tags map[string]string) string {
	for key, value := range tags {
		if strings.EqualFold(key, "title") {
			return value
		}
	}
	return ""
}

func cleanTitle(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Trim(strings.Join(strings.Fields(value), " "), ". ")
	if len(value) > 240 {
		end := 240
		for !utf8.RuneStart(value[end]) {
			end--
		}
		value = strings.TrimRight(value[:end], ". ")
	}
	stem, _, _ := strings.Cut(strings.ToUpper(value), ".")
	switch strings.TrimRight(stem, " ") {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		value = "_" + value
	}
	return value
}
