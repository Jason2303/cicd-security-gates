// Command pulse is a small HTTP service used to exercise the CI/CD pipeline.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Jason2303/cicd-security-gates/internal/server"
)

// version is overwritten at build time:
//
//	go build -ldflags "-X main.version=$GIT_SHA" ./cmd/pulse
var version = "dev"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := server.LoadConfig(version, os.LookupEnv)
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	if err := server.New(cfg, log).Run(ctx); err != nil {
		log.Error("server failed", "error", err)
		os.Exit(1)
	}
}
