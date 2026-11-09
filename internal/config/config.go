// Package config loads runtime configuration from environment variables.
//
// Only settings needed by the current milestone live here. New settings
// (database URL, data directory, ...) are added as later milestones need them.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
)

// Config holds all runtime settings for the hoard binary.
type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// LogLevel controls log verbosity (debug, info, warn, error).
	LogLevel slog.Level
}

// Defaults used when an environment variable is unset or empty.
const (
	DefaultAddr     = ":8080"
	DefaultLogLevel = "info"
)

// Load reads configuration using the given lookup function.
//
// Passing the lookup function in (instead of calling os.Getenv directly)
// keeps Load easy to test: tests pass a map, main passes os.Getenv.
// `func(string) string` is a function type, much like a callable in Python.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr: valueOr(getenv("HOARD_ADDR"), DefaultAddr),
	}

	// net.SplitHostPort accepts ":8080", "127.0.0.1:8080" and "[::1]:8080".
	// The host part may be empty (= all interfaces), the port may not.
	if _, port, err := net.SplitHostPort(cfg.Addr); err != nil || port == "" {
		return Config{}, fmt.Errorf("HOARD_ADDR %q: expected host:port, e.g. :8080 or 127.0.0.1:8080", cfg.Addr)
	}

	level := valueOr(getenv("HOARD_LOG_LEVEL"), DefaultLogLevel)
	// UnmarshalText parses "debug", "info", "warn", "error" (case-insensitive).
	// `&cfg.LogLevel` passes a pointer so the method can set the field.
	if err := cfg.LogLevel.UnmarshalText([]byte(level)); err != nil {
		return Config{}, fmt.Errorf("HOARD_LOG_LEVEL %q: expected debug, info, warn or error", level)
	}

	return cfg, nil
}

// valueOr returns v with surrounding whitespace removed, or def if v is empty.
func valueOr(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}
