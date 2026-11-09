package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/Assan233/assan-server-store/internal/auth"
	"github.com/Assan233/assan-server-store/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "password reset failed:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: reset-password USERNAME")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("run this command from an interactive terminal")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	store, err := auth.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	fmt.Fprint(os.Stderr, "New password: ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("read new password: %w", err)
	}
	fmt.Fprint(os.Stderr, "Confirm password: ")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("read password confirmation: %w", err)
	}
	if string(first) != string(second) {
		return errors.New("passwords do not match")
	}
	if err := store.ResetPassword(context.Background(), strings.TrimSpace(os.Args[1]), string(first)); err != nil {
		if errors.Is(err, auth.ErrUnauth) {
			return errors.New("user does not exist")
		}
		if errors.Is(err, auth.ErrInvalid) {
			return errors.New(auth.PasswordRequirement)
		}
		return err
	}
	fmt.Fprintln(os.Stderr, "Password updated; all existing sessions were revoked.")
	return nil
}
