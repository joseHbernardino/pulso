package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"pulso.local/pulso/pkg/types"
)

func TestWebStartValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  types.ProcessConfig
	}{
		{"missing name", types.ProcessConfig{Entrypoint: "sleep"}},
		{"missing command", types.ProcessConfig{Name: "demo"}},
		{"path traversal", types.ProcessConfig{Name: "../outside", Entrypoint: "sleep"}},
		{"unknown language", types.ProcessConfig{Name: "demo", Entrypoint: "app", Runtime: "not-a-language"}},
		{"invalid folder", types.ProcessConfig{Name: "demo", Entrypoint: "sleep", Cwd: filepath.Join(t.TempDir(), "missing")}},
		{"invalid environment", types.ProcessConfig{Name: "demo", Entrypoint: "sleep", Env: map[string]string{"BAD=NAME": "value"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := setupTestServer(t)
			defer srv.supervisor.Shutdown()
			body, err := json.Marshal(tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			srv.handleStartProcess(w, httptest.NewRequest(http.MethodPost, "/api/v1/processes", bytes.NewReader(body)))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400: %s", w.Code, w.Body.String())
			}
			if len(srv.supervisor.List()) != 0 {
				t.Fatal("invalid request registered a process")
			}
		})
	}
}

// Exercise real HTTP completion: cancelling the request after a 201 must not
// terminate its application. A delayed file proves the child kept executing.
func TestWebApplicationLifecycle(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required for script lifecycle test")
	}
	dir := t.TempDir()
	script := `import os, pathlib, sys, time
time.sleep(0.15)
pathlib.Path("started.txt").write_text("|".join(sys.argv[1:]) + "|" + os.environ["WEB_TEST_VALUE"])
print("application ready", flush=True)
time.sleep(60)
`
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := setupTestServer(t)
	defer srv.supervisor.Shutdown()
	router := chi.NewRouter()
	router.Post("/api/v1/processes", srv.handleStartProcess)
	router.Get("/api/v1/processes", srv.handleListProcesses)
	router.Post("/api/v1/processes/{id}/restart", srv.handleRestartProcess)
	router.Post("/api/v1/processes/{id}/stop", srv.handleStopProcess)
	router.Delete("/api/v1/processes/{id}", srv.handleDeleteProcess)
	httpSrv := httptest.NewServer(router)
	defer httpSrv.Close()

	send := func(method, path, body string, wantStatus int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, httpSrv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpSrv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s %s: status=%d want=%d: %s", method, path, resp.StatusCode, wantStatus, data)
		}
		return data
	}

	body, err := json.Marshal(types.ProcessConfig{
		Name: "web-script", Runtime: "auto", Entrypoint: "app.py", Cwd: dir,
		Args: []string{"hello world", "x=y"}, Env: map[string]string{"WEB_TEST_VALUE": "first=second"}, RestartPolicy: types.RestartNever,
	})
	if err != nil {
		t.Fatal(err)
	}
	data := send(http.MethodPost, "/api/v1/processes", string(body), http.StatusCreated)
	var info types.ProcessInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Runtime != "python" {
		t.Fatalf("runtime=%q, want python", info.Runtime)
	}
	marker := filepath.Join(dir, "started.txt")
	waitForScript := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if data, err := os.ReadFile(marker); err == nil {
				if string(data) != "hello world|x=y|first=second" {
					t.Fatalf("arguments or environment changed: %q", data)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("application did not survive the HTTP response or execute its script")
	}
	waitForScript()
	send(http.MethodPost, "/api/v1/processes", string(body), http.StatusConflict)
	var listed []types.ProcessInfo
	if err := json.Unmarshal(send(http.MethodGet, "/api/v1/processes", "", http.StatusOK), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("application missing from list: %v, %d processes", err, len(listed))
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	send(http.MethodPost, "/api/v1/processes/"+info.ID+"/restart", "", http.StatusOK)
	waitForScript()
	send(http.MethodPost, "/api/v1/processes/"+info.ID+"/stop", "", http.StatusOK)
	proc, err := srv.supervisor.Get(info.ID)
	if err != nil || proc.Info().State != types.StateStopped {
		t.Fatalf("application did not stop: %v", err)
	}
	send(http.MethodDelete, "/api/v1/processes/"+info.ID, "", http.StatusOK)
	if len(srv.supervisor.List()) != 0 {
		t.Fatal("application still registered after deletion")
	}
}

func TestPrepareWebProcessKeepsExplicitExecutable(t *testing.T) {
	cfg, err := prepareWebProcess(types.ProcessConfig{Name: "demo", Runtime: "process", Entrypoint: "python3", Args: []string{"-m", "http.server"}})
	if err != nil || cfg.Entrypoint != "python3" || len(cfg.Args) != 2 {
		t.Fatalf("direct executable was unexpectedly wrapped: %+v, %v", cfg, err)
	}
}

func TestPrepareWebProcessResolvesJavaScript(t *testing.T) {
	cfg, err := prepareWebProcess(types.ProcessConfig{Name: "frontend", Runtime: "auto", Entrypoint: "server.js", Args: []string{"--port", "8080"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime != "node" || len(cfg.Args) != 3 || cfg.Args[0] != "server.js" || cfg.Args[1] != "--port" || cfg.Args[2] != "8080" {
		t.Fatalf("JavaScript adapter was not resolved correctly: %+v", cfg)
	}
}

func TestWebStartIgnoresRequestCancellation(t *testing.T) {
	srv := setupTestServer(t)
	defer srv.supervisor.Shutdown()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/processes", strings.NewReader(`{"name":"long-lived","entrypoint":"sleep","args":["60"],"runtime":"process","restart_policy":"never"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	srv.handleStartProcess(w, req)
	cancel()
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		proc, err := srv.supervisor.Get("long-lived")
		if err != nil || proc.Info().State != types.StateRunning {
			t.Fatalf("request cancellation stopped the process: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
