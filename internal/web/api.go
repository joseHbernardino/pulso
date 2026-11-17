package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"pulso.local/pulso/internal/envfile"
	"pulso.local/pulso/internal/httputil"
	"pulso.local/pulso/internal/metrics"
	"pulso.local/pulso/pkg/types"
)

const maxWebBody = 1 << 20 // 1 MiB

// handleListProcesses returns all managed processes.
func (s *Server) handleListProcesses(w http.ResponseWriter, r *http.Request) {
	procs := s.supervisor.List()
	if session := requestSession(r); session != nil && session.Role == "view" {
		for i := range procs {
			procs[i].Config = types.ProcessConfig{Name: procs[i].Name, Runtime: procs[i].Runtime}
		}
	}
	httputil.WriteJSON(w, http.StatusOK, procs)
}

// handleGetProcess returns a single process by ID.
func (s *Server) handleGetProcess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	proc, err := s.supervisor.Get(id)
	if err != nil {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	info := proc.Info()
	if session := requestSession(r); session != nil && session.Role == "view" {
		info.Config = types.ProcessConfig{Name: info.Name, Runtime: info.Runtime}
	}
	httputil.WriteJSON(w, http.StatusOK, info)
}

// handleStartProcess creates and starts a new process.
func (s *Server) handleStartProcess(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebBody)
	var req applicationSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	cfg, err := s.prepareProcess(req.ProcessConfig)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	for _, existing := range s.supervisor.List() {
		if existing.Name == cfg.Name {
			httputil.WriteJSON(w, http.StatusConflict, map[string]string{"error": "an application with this name already exists"})
			return
		}
	}

	if req.EnvContent != nil {
		if cfg.EnvFile == "" {
			s.settingsError(w, fmt.Errorf("informe o caminho do .env"))
			return
		}
		doc, err := envfile.Write(cfg.EnvFile, *req.EnvContent, req.EnvRevision)
		if err != nil {
			s.settingsError(w, err)
			return
		}
		s.supervisor.ObserveEnvRevision(cfg.EnvFile, doc.Revision)
	}
	// Managed applications outlive the HTTP request and its timeout. The
	// supervisor still owns cancellation when the process or daemon is stopped.
	proc, err := s.supervisor.AddProcess(context.WithoutCancel(r.Context()), cfg)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		httputil.WriteJSON(w, status, s.paths.issue(err))
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, proc.Info())
}

// handleStopProcess stops a running process.
func (s *Server) handleStopProcess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	force := r.URL.Query().Get("force") == "true"
	timeout := 5 * time.Second
	if t := r.URL.Query().Get("timeout"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}

	if err := s.supervisor.StopProcess(id, force, timeout); err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// handleRestartProcess restarts a process.
func (s *Server) handleRestartProcess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.supervisor.RestartProcess(context.WithoutCancel(r.Context()), id); err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, s.paths.issue(err))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

// handleReloadProcess reloads a process.
func (s *Server) handleReloadProcess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.supervisor.ReloadProcess(context.WithoutCancel(r.Context()), id); err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, s.paths.issue(err))
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

// handleDeleteProcess removes a process.
func (s *Server) handleDeleteProcess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.supervisor.RemoveProcess(id); err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleSystemMetrics returns system-wide resource usage.
func (s *Server) handleSystemMetrics(w http.ResponseWriter, r *http.Request) {
	sm := metrics.GetSystemMetrics()
	httputil.WriteJSON(w, http.StatusOK, sm)
}
