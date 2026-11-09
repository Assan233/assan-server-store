package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRegistrationLoginAndPasswordRevocation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	user, firstToken, err := store.Register(ctx, "tool_user", "a-strong-password-12")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.UserForToken(ctx, firstToken); err != nil || got != user {
		t.Fatalf("UserForToken() = (%+v, %v), want (%+v, nil)", got, err, user)
	}
	if _, _, err := store.Login(ctx, user.Username, "wrong-password"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Login(wrong password) error = %v, want ErrInvalid", err)
	}
	if _, _, err := store.Login(ctx, "unknown_user", "a-strong-password-12"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Login(unknown user) error = %v, want ErrInvalid", err)
	}
	if _, _, err := store.Login(ctx, "TOOL_USER", "a-strong-password-12"); err != nil {
		t.Fatalf("Login(case-insensitive username) error = %v", err)
	}
	_, secondToken, err := store.Login(ctx, user.Username, "a-strong-password-12")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ChangePassword(ctx, user.ID, "a-strong-password-12", "an-even-stronger-password"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{firstToken, secondToken} {
		if _, err := store.UserForToken(ctx, token); !errors.Is(err, ErrUnauth) {
			t.Fatalf("UserForToken(revoked token) error = %v, want ErrUnauth", err)
		}
	}
	if _, _, err := store.Login(ctx, user.Username, "a-strong-password-12"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Login(old password) error = %v, want ErrInvalid", err)
	}
	if _, _, err := store.Login(ctx, user.Username, "an-even-stronger-password"); err != nil {
		t.Fatalf("Login(new password) error = %v", err)
	}
}

func TestRegisterRejectsInvalidInput(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, tc := range []struct{ username, password string }{
		{"ab", "a-strong-password-12"},
		{"bad name", "a-strong-password-12"},
		{"valid_user", "short"},
	} {
		if _, _, err := store.Register(context.Background(), tc.username, tc.password); !errors.Is(err, ErrInvalid) {
			t.Errorf("Register(%q, %q) error = %v, want ErrInvalid", tc.username, tc.password, err)
		}
	}
}
