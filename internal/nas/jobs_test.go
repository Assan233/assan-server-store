package nas

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestConversionJobsOutliveRequestAndPersistAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	filename := filepath.Join(t.TempDir(), "jobs.db")
	started, release := make(chan string, 3), make(chan struct{})
	jobs, err := openJobs(ctx, filename, func(ctx context.Context, source, _ string) (string, error) {
		started <- source
		if source == "first.mp4" {
			select {
			case <-release:
				return "Music/first.mp3", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		<-ctx.Done()
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if jobs != nil {
			jobs.Close()
		}
	}()
	request, disconnect := context.WithCancel(ctx)
	defer disconnect()
	first, err := jobs.Submit(request, "first.mp4", "Music")
	if err != nil || first.Status != "queued" {
		t.Fatalf("submit: %+v, %v", first, err)
	}
	disconnect()
	for _, source := range []string{"second.mkv", "third.mp4"} {
		if _, err := jobs.Submit(ctx, source, "Music"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := jobs.Submit(ctx, "../outside.mp4", "Music"); !errors.Is(err, ErrInput) {
		t.Fatalf("invalid path: %v", err)
	}
	waitStarted := func(want string) {
		t.Helper()
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("started %q, want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	waitStarted("first.mp4")
	rows, err := jobs.List(ctx)
	if err != nil || len(rows) != 3 || rows[0].Status != "queued" || rows[1].Status != "queued" || rows[2].Status != "running" {
		t.Fatalf("while converting: %+v, %v", rows, err)
	}
	close(release)
	waitStarted("second.mkv")
	if err := jobs.Close(); err != nil {
		t.Fatal(err)
	}
	jobs, err = openJobs(ctx, filename, func(_ context.Context, source, _ string) (string, error) {
		started <- source
		return "", fs.ErrExist
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStarted("third.mp4")
	for {
		rows, err = jobs.List(ctx)
		if err != nil || len(rows) != 3 {
			t.Fatalf("after restart: %+v, %v", rows, err)
		}
		if rows[0].Status == "failed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if rows[0].Error != "目标文件已存在，未覆盖原文件。" || rows[1].Status != "failed" || rows[1].Error == "" || rows[2].ID != first.ID || rows[2].Status != "completed" || rows[2].Path != "Music/first.mp3" {
		t.Fatalf("persisted conversion records: %+v", rows)
	}
}
