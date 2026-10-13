package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, cfg Config) (*Server, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return New(cfg, log), &buf
}

func get(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: invalid JSON %q: %v", path, rec.Body.String(), err)
		}
	}
	return rec, body
}

func TestRootAndVersion(t *testing.T) {
	s, _ := testServer(t, Config{Version: "deadbeef", Color: "blue", Environment: "staging"})
	h := s.Handler()

	rec, body := get(t, h, "/")
	if rec.Code != http.StatusOK || body["version"] != "deadbeef" || body["color"] != "blue" {
		t.Errorf("/ = %d %v", rec.Code, body)
	}

	rec, body = get(t, h, "/version")
	if rec.Code != http.StatusOK {
		t.Fatalf("/version status = %d", rec.Code)
	}
	if body["version"] != "deadbeef" || body["color"] != "blue" || body["environment"] != "staging" {
		t.Errorf("/version body = %v", body)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	s, _ := testServer(t, Config{})
	rec, _ := get(t, s.Handler(), "/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestLivezAlwaysOK(t *testing.T) {
	s, _ := testServer(t, Config{FailHealth: true})
	rec, _ := get(t, s.Handler(), "/livez")
	if rec.Code != http.StatusOK {
		t.Errorf("/livez = %d, want 200 even with FAIL_HEALTH", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		cfg     Config
		elapsed time.Duration
		want    int
	}{
		{"healthy by default", Config{}, 0, http.StatusOK},
		{"fail immediately", Config{FailHealth: true}, 0, http.StatusServiceUnavailable},
		{"fail after delay, before", Config{FailHealth: true, FailHealthAfter: time.Minute}, 30 * time.Second, http.StatusOK},
		{"fail after delay, after", Config{FailHealth: true, FailHealthAfter: time.Minute}, 61 * time.Second, http.StatusServiceUnavailable},
		{"delay without flag", Config{FailHealthAfter: time.Second}, time.Hour, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServer(t, tc.cfg)
			s.started = t0
			s.now = func() time.Time { return t0.Add(tc.elapsed) }
			rec, body := get(t, s.Handler(), "/healthz")
			if rec.Code != tc.want {
				t.Errorf("/healthz = %d %v, want %d", rec.Code, body, tc.want)
			}
		})
	}
}

func TestReadyz(t *testing.T) {
	s, _ := testServer(t, Config{})
	h := s.Handler()

	if rec, _ := get(t, h, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before ready: %d, want 503", rec.Code)
	}
	s.ready.Store(true)
	if rec, _ := get(t, h, "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("after ready: %d, want 200", rec.Code)
	}
}

func TestRequestLogging(t *testing.T) {
	s, buf := testServer(t, Config{Color: "green"})
	get(t, s.Handler(), "/version")

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("log line is not JSON: %q", buf.String())
	}
	if entry["path"] != "/version" || entry["status"] != float64(200) || entry["color"] != "green" || entry["level"] != "INFO" {
		t.Errorf("unexpected log entry: %v", entry)
	}

	buf.Reset()
	get(t, s.Handler(), "/livez")
	if !strings.Contains(buf.String(), `"level":"DEBUG"`) {
		t.Errorf("probe requests should log at debug, got %q", buf.String())
	}
}

func TestServeAndGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := testServer(t, Config{Version: "v1", ShutdownDelay: 200 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	url := "http://" + ln.Addr().String()
	waitFor(t, func() bool { return status(url+"/readyz") == http.StatusOK })

	cancel()
	// During the drain window the pod reports not-ready but still serves.
	waitFor(t, func() bool { return status(url+"/readyz") == http.StatusServiceUnavailable })
	if got := status(url + "/version"); got != http.StatusOK {
		t.Errorf("/version during drain = %d, want 200", got)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestRunFailsOnBadPort(t *testing.T) {
	s, _ := testServer(t, Config{Port: "not-a-port"})
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestServeReturnsListenerError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Serve will fail immediately on a closed listener.
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	s, _ := testServer(t, Config{})
	if err := s.Serve(context.Background(), ln); err == nil {
		t.Fatal("expected error from closed listener")
	}
}

func status(url string) int {
	resp, err := http.Get(url) //nolint:gosec // test helper, local URL
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
