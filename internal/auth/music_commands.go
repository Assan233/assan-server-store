package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrMusicQueueFull means the pending music commands reached the limit.
var ErrMusicQueueFull = errors.New("music command queue is full")

// EnqueueMusicCommand records one command for the shared music player.
func (s *Store) EnqueueMusicCommand(ctx context.Context, action string) error {
	if action != "previous" && action != "next" && action != "toggle" {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO public_music_commands(action)
		SELECT ? WHERE (SELECT COUNT(*) FROM public_music_commands) < 256`, action)
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
	return nil
}

// ConsumeMusicCommand atomically removes and returns the oldest shared command.
// A command is considered consumed as soon as it is returned, without acknowledgement.
func (s *Store) ConsumeMusicCommand(ctx context.Context) (string, error) {
	var action string
	err := s.db.QueryRowContext(ctx, `DELETE FROM public_music_commands WHERE id =
		(SELECT id FROM public_music_commands ORDER BY id LIMIT 1)
		RETURNING action`).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("consume music command: %w", err)
	}
	return action, nil
}
