package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrMusicQueueFull means the user's pending music commands reached the limit.
var ErrMusicQueueFull = errors.New("music command queue is full")

// EnqueueMusicCommand records one command for the authenticated user's player.
func (s *Store) EnqueueMusicCommand(ctx context.Context, userID int64, action string) error {
	if action != "previous" && action != "next" && action != "toggle" {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO music_commands(user_id,action)
		SELECT ?,? WHERE (SELECT COUNT(*) FROM music_commands WHERE user_id=?) < 256`, userID, action, userID)
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

// ConsumeMusicCommand atomically removes and returns the oldest command for a user.
// A command is considered consumed as soon as it is returned, without acknowledgement.
func (s *Store) ConsumeMusicCommand(ctx context.Context, userID int64) (string, error) {
	var action string
	err := s.db.QueryRowContext(ctx, `DELETE FROM music_commands WHERE id =
		(SELECT id FROM music_commands WHERE user_id=? ORDER BY id LIMIT 1)
		RETURNING action`, userID).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("consume music command: %w", err)
	}
	return action, nil
}
