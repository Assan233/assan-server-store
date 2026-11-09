// Package auth owns local accounts and server-side browser sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

const sessionLifetime = 7 * 24 * time.Hour

var (
	ErrInvalid = errors.New("invalid credentials or input")
	ErrExists  = errors.New("username already exists")
	ErrUnauth  = errors.New("authentication required")
	usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
)

// User is the public account representation.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// Store persists users and revocable sessions in SQLite.
type Store struct{ db *sql.DB }

// Open initializes the SQLite store and applies the schema migration.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open auth database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect auth database: %w", err)
	}
	const schema = `CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY, username TEXT NOT NULL COLLATE NOCASE UNIQUE,
		password_hash BLOB NOT NULL, password_salt BLOB NOT NULL, created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS sessions (
		token_hash BLOB PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires_at);`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate auth database: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Register creates an account and its first browser session.
func (s *Store) Register(ctx context.Context, username, password string) (User, string, error) {
	username = strings.TrimSpace(username)
	if !usernameRE.MatchString(username) || len(password) < 12 || len(password) > 128 {
		return User{}, "", ErrInvalid
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return User{}, "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, "", fmt.Errorf("begin registration: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,password_salt,created_at) VALUES(?,?,?,?)`, username, hash, salt, time.Now().Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, "", ErrExists
		}
		return User{}, "", fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, "", err
	}
	token, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	if err := insertSession(ctx, tx, id, token); err != nil {
		return User{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return User{}, "", fmt.Errorf("commit registration: %w", err)
	}
	return User{ID: id, Username: username}, token, nil
}

// Login verifies credentials and creates a fresh session.
func (s *Store) Login(ctx context.Context, username, password string) (User, string, error) {
	var u User
	var hash, salt []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,username,password_hash,password_salt FROM users WHERE username=? COLLATE NOCASE`, strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &hash, &salt)
	if errors.Is(err, sql.ErrNoRows) {
		// Keep the expensive password check on the missing-account path too.
		_ = argon2.IDKey([]byte(password), []byte("assan-store-dummy-salt"), 2, 64*1024, 1, 32)
		return User{}, "", ErrInvalid
	}
	if err != nil {
		return User{}, "", fmt.Errorf("find login user: %w", err)
	}
	if len(password) > 128 {
		return User{}, "", ErrInvalid
	}
	actual := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	if !equal(actual, hash) {
		return User{}, "", ErrInvalid
	}
	token, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`, digest(token), u.ID, time.Now().Add(sessionLifetime).Unix(), time.Now().Unix()); err != nil {
		return User{}, "", fmt.Errorf("create session: %w", err)
	}
	return u, token, nil
}

// UserForToken returns the user associated with an unexpired session token.
func (s *Store) UserForToken(ctx context.Context, token string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT users.id,users.username FROM sessions JOIN users ON users.id=sessions.user_id WHERE sessions.token_hash=? AND sessions.expires_at>?`, digest(token), time.Now().Unix()).Scan(&u.ID, &u.Username)
	if err != nil {
		return User{}, ErrUnauth
	}
	return u, nil
}

// Logout revokes a browser session.
func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, digest(token))
	return err
}

// ChangePassword verifies the current password and revokes all account sessions.
func (s *Store) ChangePassword(ctx context.Context, userID int64, oldPassword, newPassword string) error {
	if len(newPassword) < 12 || len(newPassword) > 128 {
		return ErrInvalid
	}
	var hash, salt []byte
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash,password_salt FROM users WHERE id=?`, userID).Scan(&hash, &salt); err != nil {
		return ErrUnauth
	}
	if !equal(argon2.IDKey([]byte(oldPassword), salt, 2, 64*1024, 1, 32), hash) {
		return ErrInvalid
	}
	newSalt := make([]byte, 16)
	if _, err := rand.Read(newSalt); err != nil {
		return err
	}
	newHash := argon2.IDKey([]byte(newPassword), newSalt, 2, 64*1024, 1, 32)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE users SET password_hash=?,password_salt=? WHERE id=?`, newHash, newSalt, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetPassword changes an account password from a trusted administrative command.
func (s *Store) ResetPassword(ctx context.Context, username, newPassword string) error {
	if len(newPassword) < 12 || len(newPassword) > 128 {
		return ErrInvalid
	}
	var userID int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username=? COLLATE NOCASE`, strings.TrimSpace(username)).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnauth
		}
		return fmt.Errorf("find user for password reset: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(newPassword), salt, 2, 64*1024, 1, 32)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=?,password_salt=? WHERE id=?`, hash, salt, userID); err != nil {
		return fmt.Errorf("update reset password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("revoke reset sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

func insertSession(ctx context.Context, tx *sql.Tx, userID int64, token string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`, digest(token), userID, time.Now().Add(sessionLifetime).Unix(), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func digest(token string) []byte { sum := sha256.Sum256([]byte(token)); return sum[:] }
func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
