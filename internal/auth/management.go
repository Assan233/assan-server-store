package auth

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// NeedsSetup reports whether this installation has never been initialized.
func (s *Store) NeedsSetup(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM installation`).Scan(&count)
	return count == 0, err
}

// Users lists public account information for the administrative boundary.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,username,role,disabled FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CreateMember adds a subordinate account without creating a session.
func (s *Store) CreateMember(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if !usernameRE.MatchString(username) || !validPassword(password) {
		return ErrInvalid
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(username,password_hash,password_salt,created_at) VALUES(?,?,?,?)`, username, hash, salt, time.Now().Unix())
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrExists
	}
	return err
}

// ManageMember changes a subordinate account and revokes its existing sessions.
// Administrator accounts cannot be modified through this operation.
func (s *Store) ManageMember(ctx context.Context, id int64, action, password string) error {
	var salt, hash []byte
	switch action {
	case "reset-password":
		if !validPassword(password) {
			return ErrInvalid
		}
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return err
		}
		hash = argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	case "disable", "enable", "delete":
	default:
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, id).Scan(&role); err != nil {
		return ErrInvalid
	}
	if role != "member" {
		return ErrForbidden
	}
	switch action {
	case "delete":
		_, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	case "disable", "enable":
		_, err = tx.ExecContext(ctx, `UPDATE users SET disabled=? WHERE id=?`, action == "disable", id)
	case "reset-password":
		_, err = tx.ExecContext(ctx, `UPDATE users SET password_hash=?,password_salt=? WHERE id=?`, hash, salt, id)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
