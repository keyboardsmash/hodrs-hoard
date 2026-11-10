// Package config loads runtime configuration from environment variables.
//
// Only settings needed by the current milestone live here. New settings are
// added as later milestones need them.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
)

// Config holds all runtime settings for the hoard binary.
type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// LogLevel controls log verbosity (debug, info, warn, error).
	LogLevel slog.Level
	// DataDir holds the SQLite database and, later, covers.
	DataDir string
	// DatabasePath is the SQLite file inside DataDir. Not configurable on
	// its own: keeping everything in one folder keeps backups simple.
	DatabasePath string
}

// Defaults used when an environment variable is unset or empty.
const (
	DefaultAddr     = ":8080"
	DefaultLogLevel = "info"
	// AppDirName is the folder name inside the user's data folder (ASCII slug).
	AppDirName = "hodrs-hoard"
	// DatabaseFile is the SQLite file name inside the data folder.
	DatabaseFile = "hoard.db"
)

// Env is what Load needs from the environment. In production main passes
// the real environment; tests pass fakes.
type Env struct {
	// Getenv looks up one variable, like os.Getenv.
	Getenv func(string) string
	// UserConfigDir returns the per-user config folder, like os.UserConfigDir.
	// Only used on macOS; Linux follows XDG (see defaultDataDir).
	UserConfigDir func() (string, error)
	// UserHomeDir returns the home folder, like os.UserHomeDir.
	UserHomeDir func() (string, error)
	// GOOS is the operating system, normally runtime.GOOS.
	GOOS string
}

// Load reads configuration from env.
func Load(env Env) (Config, error) {
	getenv := env.Getenv
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

	cfg.DataDir = strings.TrimSpace(getenv("HOARD_DATA_DIR"))
	if cfg.DataDir == "" {
		dir, err := defaultDataDir(env)
		if err != nil {
			return Config{}, fmt.Errorf("finding default data folder (set HOARD_DATA_DIR): %w", err)
		}
		cfg.DataDir = dir
	}
	// Abs makes a relative HOARD_DATA_DIR independent of the working directory.
	abs, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("HOARD_DATA_DIR %q: %w", cfg.DataDir, err)
	}
	cfg.DataDir = abs

	cfg.DatabasePath = filepath.Join(cfg.DataDir, DatabaseFile)

	return cfg, nil
}

// defaultDataDir picks the per-user data folder.
//
//   - Linux and other Unix: $XDG_DATA_HOME/hodrs-hoard, falling back to
//     ~/.local/share/hodrs-hoard (the XDG standard place for app data).
//   - macOS: the user config folder (~/Library/Application Support), which is
//     where apps keep their data there.
//
// Windows is not supported (see cmd/hoard/unsupported_windows.go).
func defaultDataDir(env Env) (string, error) {
	switch env.GOOS {
	case "darwin":
		dir, err := env.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, AppDirName), nil
	}

	// XDG says relative paths in XDG_DATA_HOME are invalid and must be ignored.
	if dir := env.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, AppDirName), nil
	}
	home, err := env.UserHomeDir()
	if err != nil {
		return "", err
	}
	if home == "" {
		return "", errors.New("home folder is unknown")
	}
	return filepath.Join(home, ".local", "share", AppDirName), nil
}

// valueOr returns v with surrounding whitespace removed, or def if v is empty.
func valueOr(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}
