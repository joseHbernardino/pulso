package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"pulso.local/pulso/internal/envfile"
	"pulso.local/pulso/internal/httputil"
	"pulso.local/pulso/internal/supervisor"
	"pulso.local/pulso/pkg/types"
)

type applicationSettingsRequest struct {
	types.ProcessConfig
	Revision    string  `json:"revision"`
	EnvContent  *string `json:"env_content"`
	EnvRevision string  `json:"env_revision"`
}

func (s *Server) settingsError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, envfile.ErrConflict) || errors.Is(err, supervisor.ErrConfigConflict) {
		status = http.StatusConflict
	}
	httputil.WriteJSON(w, status, s.paths.issue(err))
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg, revision, err := s.supervisor.ProcessSettings(chi.URLParam(r, "id"))
	if err != nil {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	result := map[string]any{"config": cfg, "revision": revision}
	if cfg.EnvFile != "" {
		doc, readErr := envfile.Read(cfg.EnvFile)
		if readErr != nil {
			result["env_error"] = s.paths.issue(readErr).Error
			result["env_issue"] = s.paths.issue(readErr)
		} else {
			result["env_file"] = doc
			s.supervisor.ObserveEnvRevision(cfg.EnvFile, doc.Revision)
		}
	}
	proc, err := s.supervisor.Get(chi.URLParam(r, "id"))
	if err != nil {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	result["pending_changes"] = proc.Info().PendingChanges
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebBody)
	var req applicationSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.settingsError(w, errors.New("configuração inválida"))
		return
	}
	cfg, err := s.prepareProcess(req.ProcessConfig)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	if err = s.supervisor.UpdateSettings(chi.URLParam(r, "id"), cfg, req.Revision, req.EnvContent, req.EnvRevision); err != nil {
		s.settingsError(w, err)
		return
	}
	s.handleGetSettings(w, r)
}

func (s *Server) handlePreviewEnv(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebBody)
	var req struct {
		Path string `json:"path"`
		Cwd  string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.settingsError(w, errors.New("caminho inválido"))
		return
	}
	cwd, err := s.paths.absolute(req.Cwd, "")
	if err != nil {
		s.settingsError(w, err)
		return
	}
	input, err := s.paths.translate(req.Path)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	path, err := envfile.Resolve(input, cwd)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	if path == "" {
		s.settingsError(w, errors.New("informe o caminho do .env"))
		return
	}
	doc, err := envfile.Read(path)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, struct {
		envfile.Document
		Cwd    string `json:"cwd"`
		Mapped bool   `json:"mapped"`
	}{Document: doc, Cwd: cwd, Mapped: req.Path != path || req.Cwd != cwd})
}
