package server

import (
	"testing"
	"time"
)

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig("abc123", lookupFrom(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{
		Port:          "8080",
		Version:       "abc123",
		Environment:   "local",
		ShutdownDelay: 5 * time.Second,
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	cfg, err := LoadConfig("abc123", lookupFrom(map[string]string{
		"PORT":              "9090",
		"COLOR":             "green",
		"ENVIRONMENT":       "staging",
		"FAIL_HEALTH":       "true",
		"FAIL_HEALTH_AFTER": "90s",
		"SHUTDOWN_DELAY":    "0s",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "9090" || cfg.Color != "green" || cfg.Environment != "staging" {
		t.Errorf("string fields wrong: %+v", cfg)
	}
	if !cfg.FailHealth || cfg.FailHealthAfter != 90*time.Second || cfg.ShutdownDelay != 0 {
		t.Errorf("health/shutdown fields wrong: %+v", cfg)
	}
}

func TestLoadConfigEmptyValueUsesDefault(t *testing.T) {
	cfg, err := LoadConfig("v", lookupFrom(map[string]string{"PORT": ""}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want default 8080", cfg.Port)
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	cases := map[string]map[string]string{
		"bad bool":            {"FAIL_HEALTH": "maybe"},
		"bad fail duration":   {"FAIL_HEALTH_AFTER": "soon"},
		"bad shutdown":        {"SHUTDOWN_DELAY": "later"},
		"negative duration":   {"SHUTDOWN_DELAY": "-1s"},
		"negative fail after": {"FAIL_HEALTH_AFTER": "-5s"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig("v", lookupFrom(env)); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
