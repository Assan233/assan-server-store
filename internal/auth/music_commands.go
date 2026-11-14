package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const musicCommandLifetime = 20 * time.Second

// ErrMusicQueueFull means the pending music commands reached the limit.
var ErrMusicQueueFull = errors.New("music command queue is full")

// EnqueueMusicCommand records one command for the shared music player.
func (s *Store) EnqueueMusicCommand(ctx context.Context, action string) error {
	if action != "previous" && action != "next" && action != "toggle" {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enqueue music command: %w", err)
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `DELETE FROM public_music_commands WHERE expires_at<=?`, now.UnixMilli()); err != nil {
		return fmt.Errorf("clear expired music commands: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO public_music_commands(action,expires_at)
		SELECT ?,? WHERE (SELECT COUNT(*) FROM public_music_commands) < 256`, action, now.Add(musicCommandLifetime).UnixMilli())
	if err != nil {
		return fmt.Errorf("enqueue music command: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMusicQueueFull
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit music command: %w", err)
	}
	return nil
}

// ConsumeMusicCommand clears expired commands and atomically returns the oldest live command.
// A command is considered consumed as soon as it is returned, without acknowledgement.
func (s *Store) ConsumeMusicCommand(ctx context.Context) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin consume music command: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM public_music_commands WHERE expires_at<=?`, time.Now().UnixMilli()); err != nil {
		return "", fmt.Errorf("clear expired music commands: %w", err)
	}
	var action string
	err = tx.QueryRowContext(ctx, `DELETE FROM public_music_commands WHERE id =
		(SELECT id FROM public_music_commands ORDER BY id LIMIT 1)
		RETURNING action`).Scan(&action)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("consume music command: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit consumed music command: %w", err)
	}
	return action, nil
}
