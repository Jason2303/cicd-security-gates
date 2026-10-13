// Package server is the pulse HTTP service: config, handlers, middleware and
// the run loop with graceful shutdown.
package server

import (
	"fmt"
	"strconv"
	"time"
)

// Config is everything the service reads from its environment.
type Config struct {
	// Port the HTTP server listens on.
	Port string
	// Version is the git SHA the binary was built from (set via -ldflags).
	Version string
	// Color is "blue" or "green", injected from the pod label via the
	// downward API. Empty when running outside Kubernetes.
	Color string
	// Environment is "staging", "production" or "local".
	Environment string
	// FailHealth makes /healthz return 503, to exercise automated rollback.
	FailHealth bool
	// FailHealthAfter delays FailHealth so a bad release passes rollout and
	// smoke tests, then fails after the traffic switch, which is what the
	// post-deploy health check must catch.
	FailHealthAfter time.Duration
	// ShutdownDelay is how long the pod keeps serving after SIGTERM while
	// reporting not-ready, so Kubernetes removes it from Service endpoints
	// before connections are closed.
	ShutdownDelay time.Duration
}

// LoadConfig builds a Config from a lookup function (os.LookupEnv in main,
// a map in tests).
func LoadConfig(version string, lookup func(string) (string, bool)) (Config, error) {
	get := func(key, def string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}
		return def
	}

	cfg := Config{
		Port:        get("PORT", "8080"),
		Version:     version,
		Color:       get("COLOR", ""),
		Environment: get("ENVIRONMENT", "local"),
	}

	var err error
	if cfg.FailHealth, err = strconv.ParseBool(get("FAIL_HEALTH", "false")); err != nil {
		return Config{}, fmt.Errorf("FAIL_HEALTH: %w", err)
	}
	if cfg.FailHealthAfter, err = time.ParseDuration(get("FAIL_HEALTH_AFTER", "0s")); err != nil {
		return Config{}, fmt.Errorf("FAIL_HEALTH_AFTER: %w", err)
	}
	if cfg.ShutdownDelay, err = time.ParseDuration(get("SHUTDOWN_DELAY", "5s")); err != nil {
		return Config{}, fmt.Errorf("SHUTDOWN_DELAY: %w", err)
	}
	if cfg.FailHealthAfter < 0 || cfg.ShutdownDelay < 0 {
		return Config{}, fmt.Errorf("durations must not be negative")
	}
	return cfg, nil
}
