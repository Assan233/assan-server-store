// Package config loads and validates process configuration.
package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds validated runtime settings.
type Config struct {
	Port            int
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	DatabasePath    string
	SecureCookies   bool
	TrustProxy      bool
	MusicKey        []byte
}

// Load reads environment variables, applying defaults only to absent variables.
func Load() (Config, error) {
	cfg := Config{Port: 8080, LogLevel: slog.LevelInfo, ShutdownTimeout: 10 * time.Second, DatabasePath: "data/assan.db"}
	if value, ok := os.LookupEnv("HTTP_PORT"); ok {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("HTTP_PORT must be an integer between 1 and 65535")
		}
		cfg.Port = port
	}
	if value, ok := os.LookupEnv("LOG_LEVEL"); ok {
		if err := cfg.LogLevel.UnmarshalText([]byte(value)); err != nil {
			return Config{}, fmt.Errorf("LOG_LEVEL: %w", err)
		}
	}
	if value, ok := os.LookupEnv("SHUTDOWN_TIMEOUT"); ok {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
		}
		cfg.ShutdownTimeout = duration
	}
	if value, ok := os.LookupEnv("DATABASE_PATH"); ok {
		if value == "" {
			return Config{}, fmt.Errorf("DATABASE_PATH must not be empty")
		}
		cfg.DatabasePath = value
	}
	if value, ok := os.LookupEnv("SECURE_COOKIES"); ok {
		secure, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("SECURE_COOKIES must be a boolean")
		}
		cfg.SecureCookies = secure
	}
	if value, ok := os.LookupEnv("TRUST_PROXY"); ok {
		trusted, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("TRUST_PROXY must be a boolean")
		}
		cfg.TrustProxy = trusted
	}
	value, set := os.LookupEnv("MUSIC_CREDENTIAL_KEY")
	if !set {
		contents, err := os.ReadFile(".env.local")
		if err != nil && !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("read .env.local: %w", err)
		}
		for _, line := range strings.Split(string(contents), "\n") {
			if candidate, ok := strings.CutPrefix(strings.TrimSpace(line), "MUSIC_CREDENTIAL_KEY="); ok {
				value = strings.TrimSpace(candidate)
			}
		}
	}
	if value != "" {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(key) != 32 {
			return Config{}, fmt.Errorf("MUSIC_CREDENTIAL_KEY must be base64 encoding of 32 random bytes")
		}
		cfg.MusicKey = key
	}
	return cfg, nil
}
