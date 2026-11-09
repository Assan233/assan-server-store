package auth

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestInitializationAndMemberLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if needed, err := s.NeedsSetup(ctx); err != nil || !needed {
		t.Fatalf("fresh setup: %v %v", needed, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.Register(ctx, "admin", "Long-enough-password1!")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("initialization winners: %d", success)
	}
	if err := s.CreateMember(ctx, "member", "Long-enough-password1!"); err != nil {
		t.Fatal(err)
	}
	u, token, err := s.Login(ctx, "member", "Long-enough-password1!")
	if err != nil || u.Role != "member" {
		t.Fatalf("member login: %+v %v", u, err)
	}
	if err := s.ManageMember(ctx, u.ID, "disable", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserForToken(ctx, token); !errors.Is(err, ErrUnauth) {
		t.Fatal("disabled session valid")
	}
	if _, _, err := s.Login(ctx, "member", "Long-enough-password1!"); !errors.Is(err, ErrInvalid) {
		t.Fatal("disabled login accepted")
	}
	if err := s.ManageMember(ctx, u.ID, "enable", ""); err != nil {
		t.Fatal(err)
	}
	_, token, err = s.Login(ctx, "member", "Long-enough-password1!")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ManageMember(ctx, u.ID, "reset-password", "New-long-enough-password1!"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserForToken(ctx, token); !errors.Is(err, ErrUnauth) {
		t.Fatal("reset session valid")
	}
	if _, _, err := s.Login(ctx, "member", "Long-enough-password1!"); !errors.Is(err, ErrInvalid) {
		t.Fatal("old password valid")
	}
	if _, _, err := s.Login(ctx, "member", "New-long-enough-password1!"); err != nil {
		t.Fatal(err)
	}
	if err := s.ManageMember(ctx, u.ID, "delete", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login(ctx, "member", "New-long-enough-password1!"); !errors.Is(err, ErrInvalid) {
		t.Fatal("deleted login accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if needed, err := s.NeedsSetup(ctx); err != nil || needed {
		t.Fatalf("setup reopened after restart: %v %v", needed, err)
	}
}
