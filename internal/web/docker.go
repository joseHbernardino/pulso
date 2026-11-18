package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"pulso.local/pulso/internal/containerupdate"
	"pulso.local/pulso/internal/httputil"
)

// The Docker inventory lets the dashboard manage the whole Engine, including
// the containers, images, volumes and networks Pulso did not create.
//
// Two rules hold throughout:
//
//  1. Reading a list is allowed for the view role; the Docker list endpoints
//     do not return environments, so a list cannot leak a credential.
//     Inspecting a container does return its environment, so that is
//     redacted for anyone who is not an administrator.
//  2. Every mutation is administrator-only, and the agent refuses to touch
//     the panel's own containers regardless of what the panel asks.

func (s *Server) handleDockerContainers(w http.ResponseWriter, r *http.Request) {
	if !s.containerReader(w, r) {
		return
	}
	containers, err := s.updateClient.DockerContainers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true, "containers": containers})
}

func (s *Server) handleDockerContainer(w http.ResponseWriter, r *http.Request) {
	if !s.containerReader(w, r) {
		return
	}
	details, err := s.updateClient.DockerContainer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	// O ambiente de um contêiner costuma guardar credenciais. Para quem não
	// é administrador, as chaves são preservadas e os valores removidos, de
	// modo que a tela continue útil sem revelar segredo.
	if !s.sessionIsAdmin(r) {
		details.Env = redactEnvironment(details.Env)
		for i := range details.Mounts {
			// O caminho de origem revela a disposição do host.
			details.Mounts[i].Source = ""
		}
	}
	httputil.WriteJSON(w, http.StatusOK, details)
}

func (s *Server) handleDockerContainerLogs(w http.ResponseWriter, r *http.Request) {
	// Logs de aplicação podem conter segredos impressos pelo programa, então
	// a leitura é restrita ao administrador.
	if !s.requireAdmin(w, r, "Entre como administrador para ver os logs dos contêineres.") {
		return
	}
	if s.updateClient == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "gestão de contêineres indisponível"})
		return
	}
	tail := 200
	if value := r.URL.Query().Get("tail"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			tail = parsed
		}
	}
	logs, err := s.updateClient.DockerContainerLogs(r.Context(), chi.URLParam(r, "id"), tail)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func (s *Server) handleDockerContainerAction(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	auditRecord(r, id, "contêiner: "+action)
	if err := s.updateClient.DockerContainerAction(r.Context(), id, action); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDockerImages(w http.ResponseWriter, r *http.Request) {
	if !s.containerReader(w, r) {
		return
	}
	images, err := s.updateClient.DockerImages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true, "images": images})
}

func (s *Server) handleDockerRemoveImage(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	auditRecord(r, id, "imagem removida")
	if err := s.updateClient.DockerRemoveImage(r.Context(), id); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDockerVolumes(w http.ResponseWriter, r *http.Request) {
	if !s.containerReader(w, r) {
		return
	}
	volumes, err := s.updateClient.DockerVolumes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true, "volumes": volumes})
}

func (s *Server) handleDockerRemoveVolume(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	auditRecord(r, name, "volume removido")
	if err := s.updateClient.DockerRemoveVolume(r.Context(), name); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDockerNetworks(w http.ResponseWriter, r *http.Request) {
	if !s.containerReader(w, r) {
		return
	}
	networks, err := s.updateClient.DockerNetworks(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true, "networks": networks})
}

func (s *Server) handleDockerRemoveNetwork(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	auditRecord(r, id, "rede removida")
	if err := s.updateClient.DockerRemoveNetwork(r.Context(), id); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDockerDisk(w http.ResponseWriter, r *http.Request) {
	if s.updateClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	if !s.containerReader(w, r) {
		return
	}
	usage, err := s.updateClient.DockerDisk(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true, "usage": usage})
}

// handleDockerPrune simulates or performs cleanup.
//
// A real run requires confirm:true in the body, in addition to the
// administrator role. Cleanup deletes data, and a stray click should not be
// enough; the dashboard is expected to show the simulation first.
func (s *Server) handleDockerPrune(w http.ResponseWriter, r *http.Request) {
	if !s.containerAdmin(w, r) {
		return
	}
	var request struct {
		Resources []string `json:"resources"`
		DryRun    bool     `json:"dry_run"`
		Confirm   bool     `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWebBody)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pedido de limpeza inválido"})
		return
	}
	if len(request.Resources) == 0 {
		request.Resources = containerupdate.PruneResources()
	}
	if !request.DryRun && !request.Confirm {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "a limpeza apaga dados; envie dry_run para simular, ou confirm para executar",
		})
		return
	}

	result, err := s.updateClient.DockerPrune(r.Context(), request.Resources, request.DryRun)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if !request.DryRun {
		auditRecord(r, strings.Join(request.Resources, ","),
			"limpeza executada: "+strconv.Itoa(len(result.Removed))+" recursos, "+
				strconv.FormatInt(result.Reclaimed, 10)+" bytes")
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

// sessionIsAdmin reports whether the request carries an administrator session.
func (s *Server) sessionIsAdmin(r *http.Request) bool {
	session := requestSession(r)
	return s.users != nil && session != nil && session.Role == "admin"
}

// redactEnvironment keeps the variable names and drops every value. The
// names alone are useful for diagnosis; the values are credentials.
func redactEnvironment(env []string) []string {
	if len(env) == 0 {
		return nil
	}
	result := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if !found || name == "" {
			continue
		}
		result = append(result, name+"=***")
	}
	return result
}
