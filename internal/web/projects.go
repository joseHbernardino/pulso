package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"pulso.local/pulso/internal/httputil"
	"pulso.local/pulso/internal/projects"
)

func (s *Server) handleInspectProject(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebBody)
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		s.settingsError(w, errors.New("selecione a pasta do projeto"))
		return
	}
	path, err := s.paths.absolute(req.Path, "")
	if err != nil {
		s.settingsError(w, err)
		return
	}
	report, err := projects.Inspect(path, nil)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	report.Path = s.paths.userPath(report.Path)
	for i := range report.EnvFiles {
		report.EnvFiles[i] = s.paths.userPath(report.EnvFiles[i])
	}
	for i := range report.Suggestions {
		cfg := &report.Suggestions[i].Config
		cfg.Cwd = s.paths.userPath(cfg.Cwd)
		if filepath.IsAbs(cfg.Entrypoint) {
			cfg.Entrypoint = s.paths.userPath(cfg.Entrypoint)
		}
		if filepath.IsAbs(cfg.Interpreter) {
			cfg.Interpreter = s.paths.userPath(cfg.Interpreter)
		}
		// Script arguments retain their translated path inside the container.
	}
	httputil.WriteJSON(w, http.StatusOK, report)
}
