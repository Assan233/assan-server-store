//go:build unix

package video

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWithYTDLP(t *testing.T) {
	t.Setenv("MUSIC_CREDENTIAL_KEY", "not-for-subprocesses")
	extractor := filepath.Join(t.TempDir(), "yt-dlp")
	script := `#!/bin/sh
test -z "${MUSIC_CREDENTIAL_KEY+x}" || exit 1
for arg do url="$arg"; done
case "$url" in
  https://v.youku.com/v_show/id_XPublic.html)
    printf '%s\n' '{"title":"测试视频","url":"https://media.example.com/index.m3u8"}' ;;
  https://v.youku.com/v_show/id_XRestricted.html)
    printf '%s\n' 'ERROR: [youku] Youku server reported error -6004: restricted' >&2
    exit 1 ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(extractor, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s := &Service{ytDLP: extractor, resolveSlots: make(chan struct{}, 1)}
	episodes, err := s.Resolve(context.Background(), 5, "https://v.youku.com/v_show/id_XPublic==.html?spm=test&foo=bar")
	if err != nil || len(episodes) != 1 || episodes[0].ID == "" || episodes[0].Name != "测试视频" {
		t.Fatalf("Resolve = %v, %v", episodes, err)
	}
	if got := s.lookup(5, episodes[0].ID).URL.String(); got != "https://media.example.com/index.m3u8" {
		t.Fatalf("stored media URL = %s", got)
	}
	for raw, want := range map[string]error{
		"https://v.youku.com/v_show/id_XRestricted.html":       ErrRestricted,
		"https://list.youku.com/show/id_XPublic.html":          ErrInput,
		"https://v.youku.com.evil.test/v_show/id_XPublic.html": ErrInput,
	} {
		if _, err := s.Resolve(context.Background(), 5, raw); !errors.Is(err, want) {
			t.Errorf("Resolve(%q) error = %v, want %v", raw, err, want)
		}
	}
}
