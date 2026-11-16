package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulso.local/pulso/internal/envfile"
	"pulso.local/pulso/pkg/types"
)

func TestWebApplicationEnvSettings(t *testing.T) {
	server := setupTestServer(t)
	defer func() { _ = server.supervisor.Shutdown() }()
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, path string, body any, want int) []byte {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(data)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("secret response can be cached")
		}
		return rec.Body.Bytes()
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	var preview envfile.Document
	if err := json.Unmarshal(send("POST", "/api/v1/env-files/preview", map[string]string{"path": ".env", "cwd": dir}, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Exists || preview.Path != path {
		t.Fatal("missing preview wrong")
	}
	content := "# preserved\nWEB_TOKEN=first\n"
	var info types.ProcessInfo
	body := applicationSettingsRequest{ProcessConfig: types.ProcessConfig{Name: "web-env", Runtime: "process", Entrypoint: "sleep", Args: []string{"60"}, Cwd: dir, EnvFile: ".env", RestartPolicy: types.RestartNever}, EnvContent: &content, EnvRevision: preview.Revision}
	if err = json.Unmarshal(send("POST", "/api/v1/processes", body, 201), &info); err != nil {
		t.Fatal(err)
	}
	if info.Config.EnvFile != path || info.Config.Launch == nil {
		t.Fatal("missing env path or original launch")
	}
	settingsURL := "/api/v1/processes/" + info.ID + "/settings"
	var settings struct {
		Config   types.ProcessConfig `json:"config"`
		Revision string              `json:"revision"`
		EnvFile  envfile.Document    `json:"env_file"`
		Pending  bool                `json:"pending_changes"`
	}
	if err = json.Unmarshal(send("GET", settingsURL, nil, 200), &settings); err != nil {
		t.Fatal(err)
	}
	invalid := "WEB_TOKEN=\"unclosed-secret"
	body.ProcessConfig = settings.Config
	body.Revision = settings.Revision
	body.EnvRevision = settings.EnvFile.Revision
	body.EnvContent = &invalid
	send("PATCH", settingsURL, body, 400)
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Fatal("invalid save modified file")
	}
	next := "# preserved\nWEB_TOKEN=second\n"
	body.EnvContent = &next
	body.Args = []string{"120"}
	saved := send("PATCH", settingsURL, body, 200)
	if err = json.Unmarshal(saved, &settings); err != nil {
		t.Fatal(err)
	}
	proc, _ := server.supervisor.Get(info.ID)
	if !settings.Pending || proc.Info().PID != info.PID || proc.Config.Args[0] != "60" {
		t.Fatal("save restarted or changed active app")
	}
	send("PATCH", settingsURL, body, 409)
	if err = os.WriteFile(path, []byte("WEB_TOKEN=external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body.Revision = settings.Revision
	body.EnvRevision = settings.EnvFile.Revision
	send("PATCH", settingsURL, body, 409)
	got, _ = os.ReadFile(path)
	if string(got) != "WEB_TOKEN=external\n" {
		t.Fatal("concurrent edit overwritten")
	}
	send("POST", "/api/v1/processes/"+info.ID+"/restart", nil, 200)
	proc, _ = server.supervisor.Get(info.ID)
	if proc.Config.Args[0] != "120" || proc.Info().PendingChanges {
		t.Fatal("restart did not apply desired config")
	}
	send("POST", "/api/v1/env-files/preview", map[string]string{"path": "/etc/passwd"}, 400)
}

func TestWebSettingsPreserveResolvedRuntime(t *testing.T) {
	cfg, err := prepareWebProcess(types.ProcessConfig{Name: "python-app", Runtime: "python", Entrypoint: "app.py", Cwd: t.TempDir(), Args: []string{"--port", "8080"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Launch == nil || cfg.Launch.Entrypoint != "app.py" {
		t.Fatal("original launch lost")
	}
	original := cfg.Launch
	cfg.Runtime, cfg.Entrypoint, cfg.Args, cfg.Interpreter, cfg.UseBundle = original.Runtime, original.Entrypoint, original.Args, original.Interpreter, original.UseBundle
	again, err := prepareWebProcess(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Args) != 3 || again.Args[0] != "app.py" {
		t.Fatal("runtime was wrapped twice")
	}
	// Legacy applications with no launch metadata keep their already resolved command.
	legacy := again
	legacy.Launch = nil
	legacy.Runtime = "process"
	normalized, err := prepareWebProcess(legacy)
	if err != nil || normalized.Entrypoint != again.Entrypoint || len(normalized.Args) != len(again.Args) {
		t.Fatalf("legacy command changed: %v", err)
	}
}

func TestWebSettingsMissingApplication(t *testing.T) {
	server := setupTestServer(t)
	defer func() { _ = server.supervisor.Shutdown() }()
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/processes/missing/settings", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}
