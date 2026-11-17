package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"pulso.local/pulso/pkg/types"
)

func TestLogsIncludeStderrAndSilentFailure(t *testing.T) {
	for _, tc := range []struct {
		name, script, stderr string
		code                 int
	}{
		{"stderr-only", "printf 'python3.11: command not found' >&2; exit 127", "python3.11: command not found", 127},
		{"silent", "exit 42", "", 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := setupTestServer(t)
			t.Cleanup(func() { _ = srv.supervisor.Shutdown() })
			p, err := srv.supervisor.AddProcess(context.Background(), types.ProcessConfig{Name: tc.name, Entrypoint: "sh", Args: []string{"-c", tc.script}, RestartPolicy: types.RestartNever, Env: map[string]string{"SECRET": "do-not-return-this-secret"}})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for p.Info().LastFailure == nil && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			router := chi.NewRouter()
			router.Get("/logs/{id}", srv.handleGetLogs)
			for _, stream := range []string{"", "all", "stderr", "stdout"} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest("GET", "/logs/"+p.ID+"?stream="+stream, nil))
				if w.Code != 200 {
					t.Fatalf("logs: %d %s", w.Code, w.Body.String())
				}
				var result struct {
					Logs, Stderr string
					Process      struct {
						ExitCode    int                   `json:"exit_code"`
						LastFailure *types.ProcessFailure `json:"last_failure"`
					}
				}
				if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Process.LastFailure == nil || result.Process.ExitCode != tc.code {
					t.Fatalf("missing silent failure: %s", w.Body.String())
				}
				if !strings.Contains(result.Stderr, tc.stderr) || (stream != "stdout" && !strings.Contains(result.Logs, tc.stderr)) {
					t.Fatalf("stderr missing: %s", w.Body.String())
				}
				if stream == "stdout" && result.Logs != "" {
					t.Fatal("stdout contains stderr")
				}
				if strings.Contains(w.Body.String(), "do-not-return-this-secret") {
					t.Fatal("logs metadata exposes environment")
				}
			}
			if err = os.WriteFile(srv.supervisor.LogPath(p.ID), []byte("normal output\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/logs/"+p.ID+"?lines=999999", nil))
			if !strings.Contains(w.Body.String(), `"lines":"5000"`) || !strings.Contains(w.Body.String(), "normal output") {
				t.Fatalf("combined logs or cap missing: %s", w.Body.String())
			}
			w = httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/logs/"+p.ID+"?stream=invalid", nil))
			if w.Code != 400 {
				t.Fatalf("invalid stream: %d", w.Code)
			}
		})
	}
}
