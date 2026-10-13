package server

import (
	"net/http"
)

    type statusBody struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type versionBody struct {
	Version     string `json:"version"`
	Color       string `json:"color"`
	Environment string `json:"environment"`
}

// handleRoot is the "business" endpoint; it includes version and colour so a
// curl loop during a deploy shows the cutover.
func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service": "pulse",
		"version": s.cfg.Version,
		"color":   s.cfg.Color,
	})
}

// handleLivez answers "is the process alive?". Kubernetes' liveness probe uses
// it. It never reflects FAIL_HEALTH, so a bad release is not hidden by
// restarts: the pipeline sees the failure instead of kubelet masking it.
func (s *Server) handleLivez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// handleHealthz is application health, the endpoint the smoke test and the
// post-deploy health check poll.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	if s.unhealthy() {
		writeJSON(w, http.StatusServiceUnavailable, statusBody{Status: "fail", Reason: "FAIL_HEALTH is set"})
		return
	}
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// handleReadyz answers "should this pod receive traffic?". It is false before
// the listener is up and during graceful shutdown.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, statusBody{Status: "not ready"})
		return
	}
	writeJSON(w, http.StatusOK, statusBody{Status: "ready"})
}

// handleVersion proves which build and which colour is serving.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, versionBody{
		Version:     s.cfg.Version,
		Color:       s.cfg.Color,
		Environment: s.cfg.Environment,
	})
}

func (s *Server) unhealthy() bool {
	if !s.cfg.FailHealth {
		return false
	}
	return s.now().Sub(s.started) >= s.cfg.FailHealthAfter
}
