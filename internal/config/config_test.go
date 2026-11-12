package config

import (
	"errors"
	"log/slog"
	"testing"
)

// fakeEnv builds an Env from a map, with a fixed home and config folder.
func fakeEnv(goos string, m map[string]string) Env {
	return Env{
		Getenv:        func(k string) string { return m[k] },
		UserConfigDir: func() (string, error) { return "/cfg", nil },
		UserHomeDir:   func() (string, error) { return "/home/u", nil },
		GOOS:          goos,
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(fakeEnv("linux", nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if want := "/home/u/.local/share/hodrs-hoard"; cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if want := "/home/u/.local/share/hodrs-hoard/hoard.db"; cfg.DatabasePath != want {
		t.Errorf("DatabasePath = %q, want %q", cfg.DatabasePath, want)
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := Load(fakeEnv("linux", map[string]string{
		"HOARD_ADDR":      " 127.0.0.1:9000 ",
		"HOARD_LOG_LEVEL": "DEBUG",
		"HOARD_DATA_DIR":  "/data",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Errorf("Addr = %q, want 127.0.0.1:9000", cfg.Addr)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.DataDir != "/data" {
		t.Errorf("DataDir = %q, want /data", cfg.DataDir)
	}
	if cfg.CoversDir != "/data/covers" {
		t.Errorf("CoversDir = %q", cfg.CoversDir)
	}
	if cfg.DatabasePath != "/data/hoard.db" {
		t.Errorf("DatabasePath = %q, want /data/hoard.db", cfg.DatabasePath)
	}
}

func TestDefaultDataDir(t *testing.T) {
	cases := []struct {
		name, goos string
		env        map[string]string
		want       string
	}{
		{"linux home", "linux", nil, "/home/u/.local/share/hodrs-hoard"},
		{"linux xdg", "linux", map[string]string{"XDG_DATA_HOME": "/xdg"}, "/xdg/hodrs-hoard"},
		{"linux relative xdg ignored", "linux", map[string]string{"XDG_DATA_HOME": "rel"}, "/home/u/.local/share/hodrs-hoard"},
		{"macos", "darwin", nil, "/cfg/hodrs-hoard"},
		{"datadir wins", "linux", map[string]string{"HOARD_DATA_DIR": "/srv/hoard"}, "/srv/hoard"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(fakeEnv(tc.goos, tc.env))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.DataDir != tc.want {
				t.Errorf("DataDir = %q, want %q", cfg.DataDir, tc.want)
			}
		})
	}
}

func TestNoHomeFolder(t *testing.T) {
	env := fakeEnv("linux", nil)
	env.UserHomeDir = func() (string, error) { return "", errors.New("no home") }
	if _, err := Load(env); err == nil {
		t.Error("Load succeeded without a home folder, want error")
	}
	// Setting HOARD_DATA_DIR makes the home folder unnecessary.
	env.Getenv = func(k string) string {
		if k == "HOARD_DATA_DIR" {
			return "/data"
		}
		return ""
	}
	if _, err := Load(env); err != nil {
		t.Errorf("Load with HOARD_DATA_DIR: %v", err)
	}
}

// Table-driven tests are the usual Go style: one slice of cases, one loop.
func TestLoadInvalid(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"addr without port", map[string]string{"HOARD_ADDR": "localhost"}},
		{"addr with empty port", map[string]string{"HOARD_ADDR": "localhost:"}},
		{"unknown log level", map[string]string{"HOARD_LOG_LEVEL": "loud"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(fakeEnv("linux", tc.env)); err == nil {
				t.Errorf("Load succeeded, want error")
			}
		})
	}
}
