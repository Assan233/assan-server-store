package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMusicCommandsExpireBeforeDelivery(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := time.Now()
	if err := store.EnqueueMusicCommand(ctx, "previous"); err != nil {
		t.Fatal(err)
	}
	var deadline int64
	if err := store.db.QueryRowContext(ctx, `SELECT expires_at FROM public_music_commands WHERE action='previous'`).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if deadline < start.Add(20*time.Second).UnixMilli() || deadline > time.Now().Add(20*time.Second).UnixMilli() {
		t.Fatalf("unexpected 20-second deadline: %d", deadline)
	}
	if err := store.EnqueueMusicCommand(ctx, "next"); err != nil {
		t.Fatal(err)
	}
	// Move only the first command past its deadline: the next one must still be delivered.
	if _, err := store.db.ExecContext(ctx, `UPDATE public_music_commands SET expires_at=? WHERE action='previous'`, time.Now().Add(-time.Millisecond).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	action, err := store.ConsumeMusicCommand(ctx)
	if err != nil || action != "next" {
		t.Fatalf("consume unexpired command: action=%q err=%v", action, err)
	}
	var remaining int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM public_music_commands`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("expired command was not cleared: count=%d err=%v", remaining, err)
	}
}

func TestExpiredMusicCommandsDoNotFillQueue(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 256; i++ {
		if err := store.EnqueueMusicCommand(ctx, "next"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.EnqueueMusicCommand(ctx, "toggle"); !errors.Is(err, ErrMusicQueueFull) {
		t.Fatalf("full queue: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE public_music_commands SET expires_at=?`, time.Now().Add(-time.Millisecond).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueMusicCommand(ctx, "previous"); err != nil {
		t.Fatalf("expired commands should free capacity: %v", err)
	}
	action, err := store.ConsumeMusicCommand(ctx)
	if err != nil || action != "previous" {
		t.Fatalf("consume after cleanup: action=%q err=%v", action, err)
	}
}

func TestExistingMusicCommandsExpireOnMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE public_music_commands (
		id INTEGER PRIMARY KEY, action TEXT NOT NULL CHECK(action IN ('previous','next','toggle'))
	);
	INSERT INTO public_music_commands(action) VALUES('next');
	PRAGMA user_version=1`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	action, err := store.ConsumeMusicCommand(ctx)
	if err != nil || action != "" {
		t.Fatalf("legacy undated command must expire: action=%q err=%v", action, err)
	}
	if err := store.EnqueueMusicCommand(ctx, "toggle"); err != nil {
		t.Fatal(err)
	}
	action, err = store.ConsumeMusicCommand(ctx)
	if err != nil || action != "toggle" {
		t.Fatalf("new command after migration: action=%q err=%v", action, err)
	}
}
