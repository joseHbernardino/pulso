package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"pulso.local/pulso/internal/audit"
	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/containerupdate"
	"pulso.local/pulso/internal/envfile"
	"pulso.local/pulso/internal/projects"
)

func (s *Server) handleContainerPage(w http.ResponseWriter, r *http.Request) {
	data, err := frontendFS.ReadFile("frontend/containers.html")
	if err != nil {
		http.Error(w, "página indisponível", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
func (s *Server) handleProcessPage(w http.ResponseWriter, r *http.Request) {
	data, err := frontendFS.ReadFile("frontend/index.html")
	if err != nil {
		http.Error(w, "página indisponível", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
func (s *Server) handleHomePage(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("PULSO_APPLICATIONS_ENABLED") == "1" {
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}
	s.handleProcessPage(w, r)
}
func (s *Server) containerAdmin(w http.ResponseWriter, r *http.Request) bool {
	if session := requestSession(r); s.users == nil || session == nil || session.Role != auth.RoleAdmin {
		writeJSON(w, 403, map[string]string{"error": "Entre como administrador para gerenciar os projetos Docker."})
		return false
	}
	if s.updateClient == nil {
		writeJSON(w, 409, map[string]string{"error": "Ative os projetos Docker com bash scripts/pulso-docker.sh start."})
		return false
	}
	return true
}
func (s *Server) handleContainerApplications(w http.ResponseWriter, r *http.Request) {
	if s.updateClient == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "applications": []any{}, "reason": "Execute bash scripts/pulso-docker.sh start na pasta da instalação para ativar os projetos Docker."})
		return
	}
	apps, err := s.updateClient.Applications(r.Context())
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	if session := requestSession(r); session == nil || session.Role != auth.RoleAdmin {
		for i := range apps {
			cfg := apps[i].Config
			apps[i].Config = containerupdate.ApplicationConfig{Name: cfg.Name, Image: cfg.Image, MemoryMB: cfg.MemoryMB, CPUs: cfg.CPUs}
			if cfg.Source != nil {
				apps[i].Config.Source = &containerupdate.ApplicationSource{Kind: cfg.Source.Kind, Version: cfg.Source.Version}
			}
			apps[i].BuildLog = ""
			apps[i].Error = ""
		}
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "applications": apps})
}
func (s *Server) handleDeployContainer(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	if !s.deployMu.TryLock() {
		writeJSON(w, 409, map[string]string{"error": "outra implantação está sendo preparada; aguarde"})
		return
	}
	defer s.deployMu.Unlock()
	current, err := s.updateClient.Applications(r.Context())
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	for _, app := range current {
		if app.Phase == "deploying" {
			writeJSON(w, 409, map[string]string{"error": "há uma implantação em andamento; aguarde sua conclusão"})
			return
		}
	}
	var req struct {
		containerupdate.ApplicationConfig
		EnvText string `json:"env_text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWebBody)).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "configuração inválida"})
		return
	}
	if strings.TrimSpace(req.EnvText) != "" {
		values, err := envfile.Parse(req.EnvText)
		if err != nil {
			s.settingsError(w, err)
			return
		}
		req.Env = values
	}
	if err := containerupdate.ValidateApplication(&req.ApplicationConfig); err != nil {
		s.settingsError(w, err)
		return
	}
	// A auditoria recebe o nome, a imagem e apenas as CHAVES das variáveis.
	// Valores de ambiente podem conter credenciais e nunca são registrados.
	detail := "imagem " + req.Image
	if keys := audit.EnvKeys(req.Env); keys != "" {
		detail += "; variáveis: " + keys
	}
	auditRecord(r, req.Name, detail)
	id := chi.URLParam(r, "id")
	var previous *containerupdate.ApplicationConfig
	for i := range current {
		if current[i].ID == id {
			previous = &current[i].Config
			break
		}
	}
	if id != "" && previous == nil {
		writeJSON(w, 404, map[string]string{"error": "projeto não encontrado"})
		return
	}
	if req.Source != nil {
		path, err := s.paths.absolute(req.Source.Path, "")
		if err != nil {
			s.settingsError(w, err)
			return
		}
		if strings.TrimSpace(req.Source.Path) == "" || path == s.paths.hostRoot || filepath.Clean(path) == "/" {
			s.settingsError(w, fmt.Errorf("selecione a pasta específica da aplicação"))
			return
		}
		req.Source.Path = s.paths.userPath(path)
		rebuild := previous == nil || previous.Source == nil || req.Source.Rebuild || req.Image == "" || previous.Image != req.Image
		if previous != nil && previous.Source != nil {
			rebuild = rebuild || previous.Source.Kind != req.Source.Kind || previous.Source.Path != req.Source.Path || previous.Source.Version != req.Source.Version
		}
		req.Source.Archive = ""
		req.Source.Rebuild = rebuild
		if rebuild {
			archive, err := projects.Archive(r.Context(), path, req.Source.Kind, req.Source.Version, req.Command...)
			if err != nil {
				s.settingsError(w, err)
				return
			}
			req.Source.Archive = archive
			req.Image = ""
		}
	}
	app, err := s.updateClient.DeployApplication(r.Context(), chi.URLParam(r, "id"), req.ApplicationConfig)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	// A implantação é assíncrona: o agente produz o resultado. O painel
	// acompanha até a conclusão para notificar sucesso ou falha.
	actor := ""
	if session := requestSession(r); session != nil {
		actor = session.Username
	}
	s.watchDeployment(app.ID, req.Name, actor)
	writeJSON(w, 202, app)
}
func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	if err := s.updateClient.ApplicationAction(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "action")); err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	logs, err := s.updateClient.ApplicationLogs(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"logs": logs})
}

// containerReader authorizes read-only container access. Métricas não revelam
// variáveis nem configuração, então o papel de visualização também as recebe;
// escrever continua restrito a containerAdmin.
func (s *Server) containerReader(w http.ResponseWriter, r *http.Request) bool {
	if session := requestSession(r); s.users == nil || session == nil {
		writeJSON(w, 403, map[string]string{"error": "Entre para ver os projetos Docker."})
		return false
	}
	if s.updateClient == nil {
		writeJSON(w, 409, map[string]string{"error": "Ative os projetos Docker com bash scripts/pulso-docker.sh start."})
		return false
	}
	return true
}

func (s *Server) handleContainerStats(w http.ResponseWriter, r *http.Request) {
	if !s.containerReader(w, r) {
		return
	}
	stats, err := s.updateClient.ApplicationStats(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, stats)
}

func (s *Server) handleAllContainerStats(w http.ResponseWriter, r *http.Request) {
	if s.updateClient == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "stats": []any{}})
		return
	}
	if !s.containerReader(w, r) {
		return
	}
	stats, err := s.updateClient.AllApplicationStats(r.Context())
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "stats": stats})
}

func (s *Server) handleContainerSettings(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	app, err := s.updateClient.Application(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, app)
}
