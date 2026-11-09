// Package config loads and validates process configuration.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Config holds validated runtime settings.
type Config struct {
	Port            int
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

// Load reads environment variables, applying defaults only to absent variables.
func Load() (Config, error) {
	cfg := Config{Port: 8080, LogLevel: slog.LevelInfo, ShutdownTimeout: 10 * time.Second}
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
	return cfg, nil
}
