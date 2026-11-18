package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/pkg/types"
)

func TestPersistentWebRoles(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.OpenUserStore(filepath.Join(dir, "auth.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	token, err := os.ReadFile(filepath.Join(dir, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Bootstrap(strings.TrimSpace(string(token)), "admin", "AdminPassword123!"); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateUser("viewer", "ViewerPassword123!", auth.RoleView)
	if err != nil {
		t.Fatal(err)
	}
	base := setupTestServer(t)
	defer func() {
		if err := base.supervisor.Shutdown(); err != nil {
			t.Error(err)
		}
	}()
	process, err := base.supervisor.AddProcess(context.Background(), types.ProcessConfig{Name: "rbac-app", Entrypoint: "sleep", Args: []string{"60"}, Env: map[string]string{"PRIVATE_KEY": "must-not-reach-viewer"}, RestartPolicy: types.RestartNever})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithUsers(base.supervisor, "127.0.0.1:0", store)
	defer server.sessions.Stop()
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	adminSession, err := server.sessions.CreateSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	viewSession, err := server.sessions.CreateSession(viewer.Username)
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, path, body, session, origin string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Real-IP", "127.0.0.1")
		if session != "" {
			request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, recorder.Code, want, recorder.Body.String())
		}
		return recorder.Body.String()
	}
	send("GET", "/api/v1/processes", "", "", "", 401)
	send("GET", "/ws", "", "", "", 401)
	send("GET", "/api/auth/not-public", "", "", "", 401)
	send("GET", "/assets/brand.css", "", "", "", 200)
	send("GET", "/login", "", "", "", 200)
	for _, path := range []string{"/api/v1/processes", "/api/v1/processes/" + process.ID} {
		body := send("GET", path, "", viewSession.Token, "", 200)
		if strings.Contains(body, "must-not-reach-viewer") || strings.Contains(body, "PRIVATE_KEY") {
			t.Fatal("viewer received process secrets")
		}
	}
	status := send("GET", "/api/auth/status", "", viewSession.Token, "", 200)
	if !strings.Contains(status, `"role":"view"`) {
		t.Fatal("viewer role missing")
	}
	versionBody := send("GET", "/api/v1/system/version", "", viewSession.Token, "", 200)
	if !strings.Contains(versionBody, `"version"`) {
		t.Fatal("installed version missing for viewer")
	}
	send("GET", "/api/v1/system/updates", "", viewSession.Token, "", 403)
	send("POST", "/api/v1/system/updates/check", `{}`, viewSession.Token, "", 403)
	send("POST", "/api/v1/system/updates", `{"version":"v1.0.0"}`, viewSession.Token, "", 403)
	send("POST", "/api/v1/system/updates", `{"version":"v1.0.0"}`, adminSession.Token, "https://attacker.example", 403)
	send("GET", "/api/v1/system/updates", "", adminSession.Token, "", 200)
	send("GET", "/api/v1/users", "", viewSession.Token, "", 403)
	send("GET", "/users", "", viewSession.Token, "", 403)
	send("GET", "/api/v1/processes/"+process.ID+"/settings", "", viewSession.Token, "", 403)
	send("GET", "/api/v1/processes/"+process.ID+"/settings", "", "", "", 401)
	send("POST", "/api/v1/env-files/preview", `{}`, viewSession.Token, "", 403)
	send("POST", "/api/v1/filesystem/browse", `{}`, viewSession.Token, "", 403)
	send("POST", "/api/v1/filesystem/browse", `{}`, "", "", 401)
	send("POST", "/api/v1/projects/inspect", `{}`, viewSession.Token, "", 403)
	send("POST", "/api/v1/projects/inspect", `{}`, "", "", 401)
	send("POST", "/api/v1/projects/inspect", `{}`, adminSession.Token, "https://attacker.example", 403)
	send("POST", "/api/v1/container-applications", `{}`, viewSession.Token, "", 403)
	send("PUT", "/api/v1/container-applications/0123456789abcdef01234567", `{}`, viewSession.Token, "", 403)
	send("GET", "/api/v1/container-applications/0123456789abcdef01234567", "", viewSession.Token, "", 403)
	send("GET", "/api/v1/container-applications/0123456789abcdef01234567/logs", "", viewSession.Token, "", 403)
	send("POST", "/api/v1/container-applications", `{}`, "", "", 401)
	send("POST", "/api/v1/filesystem/browse", `{}`, adminSession.Token, "https://attacker.example", 403)
	send("PATCH", "/api/v1/processes/"+process.ID+"/settings", `{}`, viewSession.Token, "", 403)
	send("POST", "/api/v1/env-files/preview", `{}`, adminSession.Token, "https://attacker.example", 403)
	for _, action := range []struct{ method, path string }{
		{"POST", "/api/v1/processes"}, {"POST", "/api/v1/processes/" + process.ID + "/stop"},
		{"POST", "/api/v1/processes/" + process.ID + "/restart"}, {"POST", "/api/v1/processes/" + process.ID + "/reload"},
		{"DELETE", "/api/v1/processes/" + process.ID}, {"POST", "/api/v1/users"}, {"PATCH", "/api/v1/users/1"}, {"DELETE", "/api/v1/users/1"},
	} {
		send(action.method, action.path, `{}`, viewSession.Token, "", 403)
	}
	send("GET", "/api/v1/users", "", adminSession.Token, "", 200)
	send("GET", "/users", "", adminSession.Token, "", 200)
	send("POST", "/api/v1/users", `{"username":"reader","password":"ReaderPassword123!","role":"view"}`, adminSession.Token, "", 201)
	send("POST", "/api/v1/users", `{"username":"reader","password":"ReaderPassword123!","role":"view"}`, adminSession.Token, "", 409)
	send("POST", "/api/v1/users", `{}`, adminSession.Token, "https://attacker.example", 403)
	body := send("POST", "/api/auth/login", `{"username":"viewer","password":"ViewerPassword123!","role":"admin"}`, "", "", 200)
	if !strings.Contains(body, `"role":"view"`) {
		t.Fatal("login accepted client-supplied role")
	}
	send("POST", "/api/auth/logout", "", viewSession.Token, "", 200)
	send("GET", "/api/v1/processes", "", viewSession.Token, "", 401)

}
