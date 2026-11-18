package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/containerupdate"
	"pulso.local/pulso/internal/supervisor"
)

func TestUpdateRequiresPersistentAdministrator(t *testing.T) {
	srv := setupTestServer(t)
	defer func() { _ = srv.supervisor.Shutdown() }()
	handler, err := srv.handler()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/system/updates", strings.NewReader(`{"version":"v1.0.0"}`)))
	if w.Code != 403 {
		t.Fatalf("unauthenticated update allowed: %d", w.Code)
	}
}
func TestUpdateSavesDesiredStateBeforeRequestingWorker(t *testing.T) {
	dir := t.TempDir()
	updates := shortTempDir(t)
	store, err := auth.OpenUserStore(filepath.Join(dir, "auth.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	token, err := os.ReadFile(filepath.Join(dir, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Bootstrap(strings.TrimSpace(string(token)), "admin", "DisposableAdminPassword123!"); err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(supervisor.Options{LogDir: dir})
	defer func() { _ = sup.Shutdown() }()
	server := NewServerWithUsers(sup, "127.0.0.1:0", store)
	defer server.sessions.Stop()
	server.updateDirectory = updates
	server.updateClient = containerupdate.NewClient(updates)
	saved := make(chan bool, 1)
	listener, err := net.Listen("unix", filepath.Join(updates, "updater.sock"))
	if err != nil {
		t.Fatal(err)
	}
	helper := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/update" {
			t.Errorf("unexpected helper request: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var request struct{ Version string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		data, err := os.ReadFile(filepath.Join(dir, "dump.json"))
		saved <- err == nil && string(data) == "[]" && request.Version == "v1.0.0"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"id":"job","state":"running"}`))
	})}
	go func() { _ = helper.Serve(listener) }()
	defer func() { _ = helper.Shutdown(context.Background()) }()
	session, err := server.sessions.CreateSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/system/updates", strings.NewReader(`{"version":"v1.0.0"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.Token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 202 || !<-saved {
		t.Fatalf("worker requested before saving desired state: %d %s", w.Code, w.Body.String())
	}
	// New server instances must also refuse changes while a persisted job is active.
	if err = os.WriteFile(filepath.Join(updates, "job.json"), []byte(`{"id":"job","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("POST", "/api/v1/users", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.Token})
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("mutation allowed during update: %d", w.Code)
	}
}
