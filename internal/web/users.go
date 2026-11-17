package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/containerupdate"
)

type sessionContextKey struct{}

func requestSession(r *http.Request) *auth.Session {
	value, _ := r.Context().Value(sessionContextKey{}).(*auth.Session)
	return value
}

// authorizeWeb enforces roles on the server as well as in the interface.
// Unauthenticated requests have already been rejected by WebAuthMiddleware.
func (s *Server) authorizeWeb(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/login" {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Add("Vary", "Cookie")
		}
		mutation := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if mutation {
			origin := r.Header.Get("Origin")
			if origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(parsed.Host, r.Host) || (parsed.Scheme != "http" && parsed.Scheme != "https") {
					writeJSON(w, http.StatusForbidden, loginResponse{Error: "request origin not allowed"})
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeJSON(w, http.StatusForbidden, loginResponse{Error: "request origin not allowed"})
				return
			}
		}
		if s.users != nil && !isPublicPath(r.URL.Path) {
			session := requestSession(r)
			if session == nil {
				writeJSON(w, http.StatusUnauthorized, loginResponse{Error: "authentication required"})
				return
			}
			if session.Role != auth.RoleAdmin && session.Role != auth.RoleView {
				writeJSON(w, http.StatusForbidden, loginResponse{Error: "permission denied"})
				return
			}
			if (mutation || strings.HasSuffix(r.URL.Path, "/settings") || r.URL.Path == "/users" || strings.HasPrefix(r.URL.Path, "/api/v1/users")) && session.Role != auth.RoleAdmin {
				writeJSON(w, http.StatusForbidden, loginResponse{Error: "administrator permission required"})
				return
			}
		}
		if mutation && s.updateDirectory != "" && !isPublicPath(r.URL.Path) && !strings.HasPrefix(r.URL.Path, "/api/v1/system/updates") {
			status, err := containerupdate.LatestStatus(s.updateDirectory)
			if err != nil || (status != nil && status.Active()) {
				writeJSON(w, 503, loginResponse{Error: "O Pulso está sendo atualizado. Aguarde o retorno antes de alterar aplicações ou usuários."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleUsersPage(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		http.NotFound(w, r)
		return
	}
	page, err := frontendFS.ReadFile("frontend/users.html")
	if err != nil {
		http.Error(w, "unable to render users", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (s *Server) handleSetupAPI(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		writeJSON(w, http.StatusNotFound, loginResponse{Error: "user accounts are unavailable"})
		return
	}
	if !s.allowLoginAttempt(r.RemoteAddr) {
		writeJSON(w, http.StatusTooManyRequests, loginResponse{Error: "too many failed attempts"})
		return
	}
	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResponse{Error: "invalid request body"})
		return
	}
	user, err := s.users.Bootstrap(req.Token, req.Username, req.Password)
	if err != nil {
		s.recordFailedLogin(r.RemoteAddr)
		status := http.StatusBadRequest
		if errors.Is(err, auth.ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		if errors.Is(err, auth.ErrAlreadyConfigured) {
			status = http.StatusConflict
		}
		if !errors.Is(err, auth.ErrUnauthorized) && !errors.Is(err, auth.ErrAlreadyConfigured) && !errors.Is(err, auth.ErrInvalidUser) {
			log.Error().Err(err).Msg("account setup failed")
			writeJSON(w, http.StatusInternalServerError, loginResponse{Error: "unable to configure accounts"})
			return
		}
		writeJSON(w, status, loginResponse{Error: err.Error()})
		return
	}
	s.resetFailedLogins(r.RemoteAddr)
	session, err := s.sessions.CreateSession(user.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, loginResponse{Error: "administrator created; sign in to continue"})
		return
	}
	cookie := auth.SessionCookie(session.Token, time.Until(session.ExpiresAt))
	cookie.Secure = isSecureRequest(r)
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusCreated, loginResponse{Success: true, Username: user.Username, Role: user.Role})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		writeJSON(w, http.StatusNotFound, loginResponse{Error: "user accounts are unavailable"})
		return
	}
	users, err := s.users.ListUsers()
	if err != nil {
		log.Error().Err(err).Msg("list users failed")
		writeJSON(w, http.StatusInternalServerError, loginResponse{Error: "unable to list users"})
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		writeJSON(w, http.StatusNotFound, loginResponse{Error: "user accounts are unavailable"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResponse{Error: "invalid request body"})
		return
	}
	if req.Role == "" {
		req.Role = auth.RoleView
	}
	user, err := s.users.CreateUser(req.Username, req.Password, req.Role)
	if err != nil {
		s.writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		writeJSON(w, http.StatusNotFound, loginResponse{Error: "user accounts are unavailable"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, loginResponse{Error: "invalid user ID"})
		return
	}
	var change auth.UserUpdate
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if err := json.NewDecoder(r.Body).Decode(&change); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResponse{Error: "invalid request body"})
		return
	}
	if err := s.users.UpdateUser(id, change); err != nil {
		s.writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		writeJSON(w, http.StatusNotFound, loginResponse{Error: "user accounts are unavailable"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, loginResponse{Error: "invalid user ID"})
		return
	}
	if err := s.users.DeleteUser(id); err != nil {
		s.writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) writeUserError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, auth.ErrLastAdmin), errors.Is(err, auth.ErrUserExists):
		status = http.StatusConflict
	case errors.Is(err, auth.ErrUserNotFound):
		status = http.StatusNotFound
	case errors.Is(err, auth.ErrInvalidUser):
	default:
		log.Error().Err(err).Msg("account operation failed")
		writeJSON(w, http.StatusInternalServerError, loginResponse{Error: "account storage unavailable"})
		return
	}
	writeJSON(w, status, loginResponse{Error: err.Error()})
}
