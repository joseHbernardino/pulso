package web

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"
	"pulso.local/pulso/internal/audit"
	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/httputil"
)

// auditActions maps a routed request to a stable action name. The key is the
// method and the chi route pattern, so renaming a handler never changes what
// is recorded. Routes absent from the map fall back to "method pattern",
// which keeps new endpoints audited by default.
var auditActions = map[string]string{
	"POST /api/auth/login":                              "auth.login",
	"POST /api/auth/logout":                             "auth.logout",
	"POST /api/auth/setup":                              "auth.setup",
	"POST /api/v1/processes":                            "process.start",
	"POST /api/v1/exec":                                 "command.exec",
	"POST /api/v1/exec/{id}/stop":                       "command.exec.stop",
	"POST /api/v1/processes/{id}/stop":                  "process.stop",
	"POST /api/v1/processes/{id}/restart":               "process.restart",
	"POST /api/v1/processes/{id}/reload":                "process.reload",
	"DELETE /api/v1/processes/{id}":                     "process.delete",
	"PATCH /api/v1/processes/{id}/settings":             "process.settings",
	"POST /api/v1/container-applications":               "application.deploy",
	"PUT /api/v1/container-applications/{id}":           "application.redeploy",
	"POST /api/v1/container-applications/{id}/{action}": "application.action",
	"POST /api/v1/users":                                "user.create",
	"PATCH /api/v1/users/{id}":                          "user.update",
	"DELETE /api/v1/users/{id}":                         "user.delete",
	"POST /api/v1/system/updates":                       "system.update",
	"POST /api/v1/system/updates/check":                 "system.update.check",
	"POST /api/v1/notifications/channels":               "notification.create",
	"PUT /api/v1/notifications/channels/{id}":           "notification.update",
	"DELETE /api/v1/notifications/channels/{id}":        "notification.delete",
	"POST /api/v1/notifications/channels/{id}/test":     "notification.test",
	"POST /api/v1/docker/containers/{id}/{action}":      "docker.container",
	"DELETE /api/v1/docker/images/{id}":                 "docker.image.delete",
	"DELETE /api/v1/docker/volumes/{name}":              "docker.volume.delete",
	"DELETE /api/v1/docker/networks/{id}":               "docker.network.delete",
	"POST /api/v1/docker/prune":                         "docker.prune",
}

// auditDetails lets a handler add a target and a short, already-safe note to
// the entry the middleware will write. It never carries secret values.
type auditDetails struct {
	target string
	detail string
}

type auditContextKey struct{}

// auditRecord enriches the pending audit entry for this request. It is a
// no-op when auditing is off, so handlers can call it unconditionally.
func auditRecord(r *http.Request, target, detail string) {
	if details, ok := r.Context().Value(auditContextKey{}).(*auditDetails); ok && details != nil {
		if target != "" {
			details.target = target
		}
		if detail != "" {
			details.detail = detail
		}
	}
}

// auditMiddleware records every state-changing request, including the ones
// that were refused. Read requests are not recorded: they would bury the
// entries that matter without adding accountability.
//
// Request bodies are never read here. A body may carry a password or an
// environment variable, so only the method, the route pattern, the status and
// what a handler explicitly reports are stored.
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.audit == nil || !auditableMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		details := &auditDetails{}
		request := r.WithContext(context.WithValue(r.Context(), auditContextKey{}, details))
		recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		started := time.Now()

		next.ServeHTTP(recorder, request)

		status := recorder.Status()
		if status == 0 {
			status = http.StatusOK
		}

		// O padrão da rota só está disponível depois do roteamento.
		pattern := r.URL.Path
		if routeContext := chi.RouteContext(request.Context()); routeContext != nil {
			if routed := routeContext.RoutePattern(); routed != "" {
				pattern = routed
			}
		}
		key := r.Method + " " + pattern
		action, known := auditActions[key]
		if !known {
			action = key
		}

		target := details.target
		if target == "" {
			if id := chi.URLParamFromCtx(request.Context(), "id"); id != "" {
				target = id
			}
		}
		if verb := chi.URLParamFromCtx(request.Context(), "action"); verb != "" {
			switch action {
			case "application.action":
				action = "application." + verb
			case "docker.container":
				action = "docker.container." + verb
			}
		}

		actor, role := "anônimo", ""
		if session := s.auditSession(request); session != nil {
			actor, role = session.Username, session.Role
		}

		entry := audit.Entry{
			Actor:     actor,
			Role:      role,
			Action:    action,
			Target:    target,
			Outcome:   outcomeForStatus(status),
			Status:    status,
			Detail:    details.detail,
			IP:        clientIP(r),
			UserAgent: r.UserAgent(),
			CreatedAt: started,
		}
		if err := s.audit.Record(entry); err != nil {
			log.Error().Err(err).Str("action", action).Msg("record audit entry")
		}
	})
}

// auditSession identifica o autor da requisição. A sessão é resolvida aqui,
// e não lida do contexto, porque este middleware roda por fora da
// autenticação para também registrar as recusas — e o contexto preenchido
// por um middleware interno não é visível aqui.
func (s *Server) auditSession(r *http.Request) *auth.Session {
	if session := requestSession(r); session != nil {
		return session
	}
	if s.sessions == nil {
		return nil
	}
	if session, ok := s.sessions.ValidateSession(auth.SessionFromRequest(r)); ok {
		return session
	}
	return nil
}

func auditableMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func outcomeForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return audit.OutcomeDenied
	case status >= 400:
		return audit.OutcomeError
	default:
		return audit.OutcomeOK
	}
}

// clientIP reports the peer address. Proxy headers are deliberately ignored:
// they are attacker-controlled, and an audit record must not be forgeable.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, "Entre como administrador para ver o registro de auditoria.") {
		return
	}
	if s.audit == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "entries": []any{}})
		return
	}
	filter := audit.Filter{
		Actor:  strings.TrimSpace(r.URL.Query().Get("actor")),
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
	}
	if limit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		filter.Limit = limit
	}
	if hours, err := strconv.Atoi(r.URL.Query().Get("hours")); err == nil && hours > 0 {
		filter.Since = time.Now().Add(-time.Duration(hours) * time.Hour)
	}
	entries, err := s.audit.List(filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":         true,
		"entries":         entries,
		"retention_hours": int(s.audit.Retention().Hours()),
	})
}
