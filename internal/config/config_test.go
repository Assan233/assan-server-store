package config

import (
	"encoding/base64"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	for _, key := range []string{"HTTP_PORT", "LOG_LEVEL", "SHUTDOWN_TIMEOUT"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load()
	if err != nil || cfg.Port != 8080 || cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	cfg, err = Load()
	if err != nil || cfg.Port != 9090 || cfg.LogLevel != slog.LevelDebug || cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("overrides: %+v, %v", cfg, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"HTTP_PORT", ""}, {"HTTP_PORT", "0"}, {"HTTP_PORT", "65536"}, {"HTTP_PORT", "abc"},
		{"LOG_LEVEL", "verbose"}, {"LOG_LEVEL", ""},
		{"SHUTDOWN_TIMEOUT", "0s"}, {"SHUTDOWN_TIMEOUT", "-1s"}, {"SHUTDOWN_TIMEOUT", "bad"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLocalMusicKeyAndEnvironmentOverride(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MUSIC_CREDENTIAL_KEY", "")
	if err := os.Unsetenv("MUSIC_CREDENTIAL_KEY"); err != nil {
		t.Fatal(err)
	}
	local := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := os.WriteFile(".env.local", []byte("# Local secret\nMUSIC_CREDENTIAL_KEY="+local+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || string(cfg.MusicKey) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("local key: %v", err)
	}
	t.Setenv("MUSIC_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210")))
	cfg, err = Load()
	if err != nil || string(cfg.MusicKey) != "fedcba9876543210fedcba9876543210" {
		t.Fatalf("environment override: %v", err)
	}
}
