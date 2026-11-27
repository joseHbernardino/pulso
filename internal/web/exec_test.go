package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/pkg/types"
)

// consoleHarness is a panel with accounts, which the command console requires.
type consoleHarness struct {
	server  *Server
	handler http.Handler
	admin   string
	viewer  string
}

func setupConsole(t *testing.T) *consoleHarness {
	t.Helper()
	dir := t.TempDir()
	store, err := auth.OpenUserStore(filepath.Join(dir, "auth.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	token, err := os.ReadFile(filepath.Join(dir, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Bootstrap(strings.TrimSpace(string(token)), "admin", "AdminPassword123!"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser("viewer", "ViewerPassword123!", auth.RoleView); err != nil {
		t.Fatal(err)
	}

	base := setupTestServer(t)
	t.Cleanup(func() {
		if err := base.supervisor.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	server := NewServerWithUsers(base.supervisor, "127.0.0.1:0", store)
	t.Cleanup(server.sessions.Stop)
	handler, err := server.handler()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := server.sessions.CreateSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := server.sessions.CreateSession("viewer")
	if err != nil {
		t.Fatal(err)
	}
	return &consoleHarness{server: server, handler: handler, admin: admin.Token, viewer: viewer.Token}
}

func (h *consoleHarness) request(t *testing.T, method, path, body, session string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Real-IP", "127.0.0.1")
	if session != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	}
	return request
}

// events reads an NDJSON response into decoded events.
func events(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExecStreamsOutputForAnAdministrator(t *testing.T) {
	harness := setupConsole(t)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, http.MethodPost, "/api/v1/exec",
		`{"command":"echo visivel; echo problema >&2","timeout_seconds":30}`, harness.admin))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/x-ndjson") {
		t.Errorf("Content-Type = %q", contentType)
	}

	decoded := events(t, recorder.Body.String())
	if len(decoded) < 3 {
		t.Fatalf("expected a start, output and end event, got %v", decoded)
	}
	if decoded[0]["type"] != "start" {
		t.Errorf("first event = %v, want start", decoded[0])
	}
	if decoded[0]["id"] == "" || decoded[0]["id"] == nil {
		t.Error("the start event must carry the identifier used to stop the command")
	}

	var stdout, stderr strings.Builder
	last := decoded[len(decoded)-1]
	for _, event := range decoded {
		if event["type"] != "output" {
			continue
		}
		if event["stream"] == "stderr" {
			stderr.WriteString(event["text"].(string))
		} else {
			stdout.WriteString(event["text"].(string))
		}
	}
	if !strings.Contains(stdout.String(), "visivel") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "problema") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if last["type"] != "end" {
		t.Fatalf("last event = %v, want end", last)
	}
	if code, ok := last["exit_code"].(float64); !ok || code != 0 {
		t.Errorf("exit_code = %v, want 0", last["exit_code"])
	}
}

func TestExecReportsTheCommandExitCode(t *testing.T) {
	harness := setupConsole(t)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, http.MethodPost, "/api/v1/exec",
		`{"command":"exit 42"}`, harness.admin))

	decoded := events(t, recorder.Body.String())
	last := decoded[len(decoded)-1]
	if code, ok := last["exit_code"].(float64); !ok || code != 42 {
		t.Errorf("exit_code = %v, want 42", last["exit_code"])
	}
}

func TestExecRefusesWithoutAdministrator(t *testing.T) {
	harness := setupConsole(t)
	for name, session := range map[string]string{"viewer": harness.viewer, "anonymous": ""} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			harness.handler.ServeHTTP(recorder, harness.request(t, http.MethodPost, "/api/v1/exec",
				`{"command":"echo nao deveria rodar"}`, session))
			if recorder.Code == http.StatusOK {
				t.Fatalf("a %s was allowed to run a command", name)
			}
			if strings.Contains(recorder.Body.String(), "nao deveria rodar") {
				t.Error("the command was executed despite the refusal")
			}
		})
	}
}

func TestExecRejectsAnEmptyCommand(t *testing.T) {
	harness := setupConsole(t)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, http.MethodPost, "/api/v1/exec",
		`{"command":"   "}`, harness.admin))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestExecBorrowsAnApplicationContext(t *testing.T) {
	harness := setupConsole(t)
	dir := t.TempDir()
	if _, err := harness.server.supervisor.AddProcess(context.Background(), types.ProcessConfig{
		Name:          "contexto",
		Entrypoint:    "sleep",
		Args:          []string{"60"},
		Cwd:           dir,
		Env:           map[string]string{"APP_TOKEN": "do-app"},
		RestartPolicy: types.RestartNever,
	}); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, http.MethodPost, "/api/v1/exec",
		`{"command":"printf '%s' \"$APP_TOKEN\"","app":"contexto"}`, harness.admin))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}

	decoded := events(t, recorder.Body.String())
	var stdout strings.Builder
	for _, event := range decoded {
		if event["type"] == "output" {
			stdout.WriteString(event["text"].(string))
		}
	}
	if stdout.String() != "do-app" {
		t.Errorf("output = %q, want do-app: the application's environment was not used", stdout.String())
	}
	if cwd, _ := decoded[0]["cwd"].(string); cwd == "" {
		t.Error("the start event must report the working directory")
	}
}

func TestExecRejectsAnUnknownApplication(t *testing.T) {
	harness := setupConsole(t)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, http.MethodPost, "/api/v1/exec",
		`{"command":"true","app":"nao-existe"}`, harness.admin))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestExecStopEndsARunningCommand(t *testing.T) {
	harness := setupConsole(t)

	type outcome struct {
		code int
		body string
	}
	done := make(chan outcome, 1)
	go func() {
		recorder := httptest.NewRecorder()
		harness.handler.ServeHTTP(recorder, harness.request(t, http.MethodPost, "/api/v1/exec",
			`{"command":"echo iniciado; sleep 60","timeout_seconds":120}`, harness.admin))
		done <- outcome{recorder.Code, recorder.Body.String()}
	}()

	// Wait for the command to register, then stop it by its identifier.
	var id string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && id == "" {
		harness.server.execMu.Lock()
		for key := range harness.server.execRunning {
			id = key
		}
		harness.server.execMu.Unlock()
		if id == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if id == "" {
		t.Fatal("the running command was never registered")
	}

	stop := httptest.NewRecorder()
	harness.handler.ServeHTTP(stop, harness.request(t, http.MethodPost, "/api/v1/exec/"+id+"/stop", "", harness.admin))
	if stop.Code != http.StatusOK {
		t.Fatalf("stop status = %d, body %s", stop.Code, stop.Body.String())
	}

	select {
	case result := <-done:
		if result.code != http.StatusOK {
			t.Fatalf("exec status = %d", result.code)
		}
		decoded := events(t, result.body)
		last := decoded[len(decoded)-1]
		if last["type"] != "end" {
			t.Fatalf("last event = %v, want end", last)
		}
		if stopped, _ := last["stopped"].(bool); !stopped {
			t.Errorf("end event = %v, want stopped reported", last)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the command did not end after being stopped")
	}
}

func TestExecStopReportsAFinishedCommand(t *testing.T) {
	harness := setupConsole(t)
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, harness.request(t, http.MethodPost, "/api/v1/exec/desconhecido/stop", "", harness.admin))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestAuditCommandDetailShortensScripts(t *testing.T) {
	if got := auditCommandDetail(" docker ps \n"); got != "docker ps" {
		t.Errorf("auditCommandDetail = %q", got)
	}
	if got := auditCommandDetail("linha1\nlinha2\nlinha3"); got != "linha1 (+2 linhas)" {
		t.Errorf("auditCommandDetail = %q", got)
	}
	// A command line is one of the few places someone may type a password
	// inline, and the audit log never stores a secret.
	if got := auditCommandDetail("PGPASSWORD=muito-secreto pg_dump app"); got != "PGPASSWORD=*** pg_dump app" {
		t.Errorf("auditCommandDetail = %q, want the assigned value replaced", got)
	}
	if strings.Contains(auditCommandDetail("TOKEN=abc123 curl https://x\nmais"), "abc123") {
		t.Error("an assigned value reached the audit detail")
	}
	long := auditCommandDetail(strings.Repeat("x", execAuditCommandLimit+50))
	if len([]rune(long)) != execAuditCommandLimit+1 {
		t.Errorf("auditCommandDetail kept %d runes, want %d plus the ellipsis", len([]rune(long)), execAuditCommandLimit)
	}
	if auditCommandDetail("  ") != "" {
		t.Error("a blank command has no detail")
	}
}

func TestConsolePageIsServed(t *testing.T) {
	harness := setupConsole(t)
	request := harness.request(t, http.MethodGet, "/console", "", harness.admin)
	request.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "console.js") {
		t.Error("the console page did not render")
	}
}
