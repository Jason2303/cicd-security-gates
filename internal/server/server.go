package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Server holds the state shared by the handlers.
type Server struct {
	cfg     Config
	log     *slog.Logger
	started time.Time
	now     func() time.Time
	ready   atomic.Bool
}

// New returns a Server that is not ready yet; Run marks it ready once the
// listener is open.
func New(cfg Config, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, log: log, now: time.Now}
	s.started = s.now()
	return s
}

// Handler returns the routed, logged HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /livez", s.handleLivez)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /version", s.handleVersion)
	return s.logRequests(mux)
}

// Run serves on cfg.Port until ctx is cancelled, then drains gracefully.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", ":"+s.cfg.Port)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve is Run with a caller-supplied listener (used by tests).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	s.ready.Store(true)
	s.log.Info("server started",
		"addr", ln.Addr().String(), "version", s.cfg.Version,
		"color", s.cfg.Color, "environment", s.cfg.Environment)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	// Stop advertising readiness first so the endpoint controller pulls this
	// pod out of the Service, then keep serving in-flight and late-routed
	// requests for ShutdownDelay before closing connections.
	s.ready.Store(false)
	s.log.Info("shutdown signal received, draining", "delay", s.cfg.ShutdownDelay.String())
	time.Sleep(s.cfg.ShutdownDelay)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	s.log.Info("server stopped")
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
