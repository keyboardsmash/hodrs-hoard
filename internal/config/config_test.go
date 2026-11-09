package config

import (
	"log/slog"
	"testing"
)

// env turns a map into a getenv-style lookup function for tests.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"HOARD_ADDR":      " 127.0.0.1:9000 ",
		"HOARD_LOG_LEVEL": "DEBUG",
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
			if _, err := Load(env(tc.env)); err == nil {
				t.Errorf("Load succeeded, want error")
			}
		})
	}
}
