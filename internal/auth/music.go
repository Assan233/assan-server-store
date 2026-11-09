package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SaveMusicCredential encrypts a user's music password using the deployment key.
func (s *Store) SaveMusicCredential(ctx context.Context, userID int64, username, password string, key []byte) error {
	username = strings.TrimSpace(username)
	if len(key) != 32 || username == "" || len(username) > 128 || password == "" || len(password) > 512 {
		return ErrInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nonce, nonce, []byte(password), []byte(fmt.Sprintf("assan-music:%d", userID)))
	_, err = s.db.ExecContext(ctx, `INSERT INTO music_credentials(user_id,username,password_sealed) VALUES(?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET username=excluded.username,password_sealed=excluded.password_sealed`, userID, username, sealed)
	return err
}

// MusicCredential returns the current user's decrypted music credential.
func (s *Store) MusicCredential(ctx context.Context, userID int64, key []byte) (string, string, error) {
	var username string
	var sealed []byte
	if err := s.db.QueryRowContext(ctx, `SELECT username,password_sealed FROM music_credentials WHERE user_id=?`, userID).Scan(&username, &sealed); err != nil {
		return "", "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	if len(sealed) < aead.NonceSize() {
		return "", "", ErrInvalid
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(fmt.Sprintf("assan-music:%d", userID)))
	if err != nil {
		return "", "", err
	}
	return username, string(plain), nil
}

// HasMusicCredential reports whether the user has saved music login details.
func (s *Store) HasMusicCredential(ctx context.Context, userID int64) (bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM music_credentials WHERE user_id=?`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// DeleteMusicCredential forgets the user's saved music login details.
func (s *Store) DeleteMusicCredential(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM music_credentials WHERE user_id=?`, userID)
	return err
}
