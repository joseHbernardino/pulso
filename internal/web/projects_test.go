package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulso.local/pulso/internal/projects"
)

func TestInspectProjectWithHostPaths(t *testing.T) {
	s := setupTestServer(t)
	defer func() { _ = s.supervisor.Shutdown() }()
	root := t.TempDir()
	dir := filepath.Join(root, "srv", "my-api")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{"app.py": "print('must not run')", ".env": "TOKEN=do-not-disclose"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.paths = newPathResolver(true, "", "").withHostRoot(root)
	handler, err := s.handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/projects/inspect", strings.NewReader(`{"path":"/srv/my-api"}`)))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), "do-not-disclose") {
		t.Fatalf("unsafe inspection: %d %s", rec.Code, rec.Body.String())
	}
	var report projects.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Path != dir || len(report.Suggestions) != 1 || len(report.EnvFiles) != 1 {
		t.Fatalf("host discovery: %+v", report)
	}
	cfg, err := s.prepareProcess(report.Suggestions[0].Config)
	if err != nil || cfg.Cwd != dir || cfg.Launch.Entrypoint != filepath.Join(dir, "app.py") {
		t.Fatalf("suggestion cannot be prepared: %+v %v", cfg, err)
	}
	if len(s.supervisor.List()) != 0 {
		t.Fatal("inspection started a process")
	}
}
