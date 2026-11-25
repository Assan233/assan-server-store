package nas

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

var titleFormats = map[string]string{
	".mp3": "mp3", ".flac": "flac", ".m4a": "mp4", ".ogg": "ogg", ".oga": "ogg",
	".opus": "opus", ".wma": "asf", ".ape": "ape",
}

//go:embed set_title.py
var titleScript string

// TitleResult describes the title stored at the unchanged relative path.
type TitleResult struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Changed bool   `json:"changed"`
}

// SetTitle writes the filename stem to the song title without re-encoding audio.
func (s *Service) SetTitle(ctx context.Context, source string) (TitleResult, error) {
	var result TitleResult
	base, ext := path.Base(source), path.Ext(source)
	title := strings.TrimSuffix(base, ext)
	format := titleFormats[strings.ToLower(ext)]
	if !valid(source) || format == "" || strings.TrimSpace(title) == "" {
		return result, ErrInput
	}
	if !s.acquire() {
		return result, ErrBusy
	}
	defer func() { <-s.slot }()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	parent, err := s.root.OpenRoot(path.Dir(source))
	if err != nil {
		return result, err
	}
	defer parent.Close()
	input, err := openMedia(parent, base)
	if err != nil {
		return result, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return result, err
	}
	const maxTitleBytes = 1 << 30
	if info.Size() > maxTitleBytes {
		return result, ErrMedia
	}

	// Edit a private copy in the same directory and publish only on success.
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return result, err
	}
	tmp := ".assan-" + hex.EncodeToString(id[:]) + ".tmp"
	f, err := parent.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return result, err
	}
	defer parent.Remove(tmp)
	defer f.Close()
	if n, err := io.Copy(f, io.LimitReader(contextReader{ctx, input}, maxTitleBytes+1)); err != nil {
		return result, err
	} else if n != info.Size() {
		return result, ErrChanged
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	cmd := exec.CommandContext(ctx, "python3", "-I", "-c", titleScript, format, title)
	cmd.ExtraFiles = []*os.File{f}
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 2 {
			return result, ErrMedia
		}
		if errors.As(err, &exit) && exit.ExitCode() == 3 {
			return result, ErrTagStorage
		}
		return result, fmt.Errorf("run audio tag editor: %w", err)
	}
	current, err := parent.Lstat(base)
	if err != nil || !os.SameFile(info, current) || info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
		return result, ErrChanged
	}
	result = TitleResult{Path: source, Title: title}
	if string(output) == "unchanged\n" {
		return result, nil
	}
	if string(output) != "changed\n" {
		return TitleResult{}, ErrMedia
	}
	if err := f.Chmod(info.Mode().Perm()); err != nil {
		return TitleResult{}, err
	}
	if err := f.Sync(); err != nil {
		return TitleResult{}, err
	}
	if err := f.Close(); err != nil {
		return TitleResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return TitleResult{}, err
	}
	if err := parent.Rename(tmp, base); err != nil {
		return TitleResult{}, err
	}
	result.Changed = true
	return result, nil
}

// ErrTagStorage reports that the tag editor could not write its copy.
var ErrTagStorage = errors.New("write audio tag copy failed")

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
