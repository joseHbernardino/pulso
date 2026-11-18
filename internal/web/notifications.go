package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"pulso.local/pulso/internal/httputil"
	"pulso.local/pulso/internal/notify"
)

// SetNotifications enables outbound notifications. The caller keeps ownership
// of both the store and the dispatcher.
func (s *Server) SetNotifications(store *notify.Store, dispatcher *notify.Dispatcher) {
	s.notifyStore = store
	s.notifier = dispatcher
}

// notifyAdmin authorizes channel management. Channel settings hold
// credentials, so this is administrator-only, both for reading and writing.
func (s *Server) notifyAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !s.requireAdmin(w, r, "Entre como administrador para gerenciar as notificações.") {
		return false
	}
	if s.notifyStore == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "channels": []any{}})
		return false
	}
	return true
}

func (s *Server) handleListNotificationChannels(w http.ResponseWriter, r *http.Request) {
	if !s.notifyAdmin(w, r) {
		return
	}
	// PublicList redige toda credencial; o valor guardado nunca sai daqui.
	channels, err := s.notifyStore.PublicList()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":  true,
		"channels": channels,
		"kinds":    notify.KnownKinds(),
		"events":   notify.KnownEvents(),
	})
}

func (s *Server) handleSaveNotificationChannel(w http.ResponseWriter, r *http.Request) {
	if !s.notifyAdmin(w, r) {
		return
	}
	var channel notify.Channel
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWebBody)).Decode(&channel); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "configuração de canal inválida"})
		return
	}
	channel.ID = chi.URLParam(r, "id")
	saved, err := s.notifyStore.Put(channel)
	if err != nil {
		s.settingsError(w, err)
		return
	}
	auditRecord(r, saved.Name, "canal de notificação "+saved.Kind)
	httputil.WriteJSON(w, http.StatusOK, saved.Public())
}

func (s *Server) handleDeleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	if !s.notifyAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.notifyStore.Delete(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	auditRecord(r, id, "canal de notificação removido")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTestNotificationChannel sends a test message and reports the real
// delivery error, so a misconfiguration is visible in the dashboard instead
// of only in the server log.
func (s *Server) handleTestNotificationChannel(w http.ResponseWriter, r *http.Request) {
	if !s.notifyAdmin(w, r) {
		return
	}
	if s.notifier == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "notificações indisponíveis"})
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.notifier.TestChannel(id); err != nil {
		auditRecord(r, id, "teste de canal falhou")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	auditRecord(r, id, "teste de canal entregue")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Deployment watch settings. The panel requests a deployment and returns
// immediately; the outcome is produced by the agent, so it is observed here.
const (
	deployWatchInterval = 2 * time.Second
	deployWatchTimeout  = 15 * time.Minute
)

// watchDeployment follows one deployment to its conclusion and notifies the
// result. It is bounded in time so a stuck deployment cannot leak a
// goroutine, and every exit path is accounted for.
func (s *Server) watchDeployment(id, name, actor string) {
	if s.notifier == nil || s.updateClient == nil || id == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), deployWatchTimeout)
		defer cancel()
		ticker := time.NewTicker(deployWatchInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				s.notifier.Dispatch(notify.Event{
					Kind:        notify.EventDeployFailed,
					Severity:    notify.SeverityWarning,
					Title:       "Implantação sem conclusão",
					Message:     "A implantação não concluiu dentro do tempo acompanhado. Confira os logs no painel.",
					Application: name,
					Actor:       actor,
				})
				return
			case <-ticker.C:
			}

			app, err := s.updateClient.Application(ctx, id)
			if err != nil {
				// O agente pode estar reiniciando; segue tentando até o prazo.
				continue
			}
			switch app.Phase {
			case "ready":
				s.notifier.Dispatch(notify.Event{
					Kind:        notify.EventDeploySucceeded,
					Severity:    notify.SeverityInfo,
					Title:       "Implantação concluída",
					Message:     "A aplicação está em execução com a imagem " + app.Config.Image + ".",
					Application: name,
					Actor:       actor,
				})
				return
			case "failed":
				message := app.Error
				if message == "" {
					message = "A implantação falhou. Confira os logs no painel."
				}
				s.notifier.Dispatch(notify.Event{
					Kind:        notify.EventDeployFailed,
					Severity:    notify.SeverityError,
					Title:       "Falha na implantação",
					Message:     message,
					Application: name,
					Actor:       actor,
				})
				return
			}
		}
	}()
}

// notifySystemUpdate reports an update applied to the panel itself.
func (s *Server) notifySystemUpdate(version, actor string) {
	if s.notifier == nil {
		return
	}
	s.notifier.Dispatch(notify.Event{
		Kind:     notify.EventSystemUpdate,
		Severity: notify.SeverityInfo,
		Title:    "Atualização do Pulso solicitada",
		Message:  "Versão " + version + ".",
		Actor:    actor,
	})
	log.Debug().Str("version", version).Msg("system update notification queued")
}
