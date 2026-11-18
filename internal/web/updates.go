package web

import (
	"encoding/json"
	"net/http"
	"os"

	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/containerupdate"
	"pulso.local/pulso/internal/version"
)

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"build": version.Current(), "container": os.Getenv("PULSO_CONTAINER") == "1", "updates_configured": s.updateClient != nil})
}
func (s *Server) requireUpdateAdmin(w http.ResponseWriter, r *http.Request) bool {
	session := requestSession(r)
	if s.users == nil || session == nil || session.Role != auth.RoleAdmin {
		writeJSON(w, 403, map[string]string{"error": "Entre com uma conta de administrador para gerenciar atualizações."})
		return false
	}
	return true
}
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireUpdateAdmin(w, r) {
		return
	}
	if s.updateClient == nil {
		writeJSON(w, 200, containerupdate.UpdateInfo{Reason: "Ative o serviço de atualização com compose.update.yaml para atualizar a imagem pelo painel."})
		return
	}
	info, err := s.updateClient.Status(r.Context(), r.Method == http.MethodPost)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, info)
}
func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireUpdateAdmin(w, r) {
		return
	}
	if s.updateClient == nil {
		writeJSON(w, 409, map[string]string{"error": "O serviço de atualização não foi configurado nesta instalação."})
		return
	}
	var request struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Informe a versão exibida na verificação."})
		return
	}
	if err := s.supervisor.Save(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Não foi possível salvar o estado das aplicações: " + err.Error()})
		return
	}
	job, err := s.updateClient.Begin(r.Context(), request.Version)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	// A notificação sai no aceite, não na conclusão: a atualização reinicia o
	// próprio painel, então este processo não existe mais para avisar depois.
	actor := ""
	if session := requestSession(r); session != nil {
		actor = session.Username
	}
	version := job.ToVersion
	if version == "" {
		version = request.Version
	}
	s.notifySystemUpdate(version, actor)
	writeJSON(w, 202, job)
}
