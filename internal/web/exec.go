package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"pulso.local/pulso/internal/command"
	"pulso.local/pulso/internal/httputil"
)

// The command console runs a shell command on the server that hosts the
// panel. It is the most privileged thing the dashboard can do, so three rules
// hold:
//
//  1. Only an authenticated administrator may call it. With no accounts
//     configured there is no administrator, and the endpoint refuses.
//  2. Every attempt is recorded in the audit log with the command itself,
//     including the ones that were refused.
//  3. Concurrency and output are bounded, so the console cannot be used to
//     exhaust the panel.
const (
	// maxConcurrentExec is how many console commands may run at once.
	maxConcurrentExec = 4

	// maxStreamedOutput is how much output is forwarded to the browser. The
	// command keeps running after the limit; only the stream stops growing.
	maxStreamedOutput = 8 << 20

	// execAuditCommandLimit is how much of the command is kept in the audit
	// entry. A long script is identified by its first line.
	execAuditCommandLimit = 400
)

// execRequest is the body of POST /api/v1/exec.
type execRequest struct {
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	EnvFile        string            `json:"env_file,omitempty"`
	Shell          string            `json:"shell,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	// App borrows a managed application's working directory, environment and
	// .env file, so a maintenance command runs in the same context.
	App string `json:"app,omitempty"`
}

// execEvent is one line of the NDJSON stream the browser reads.
type execEvent struct {
	Type       string `json:"type"`
	ID         string `json:"id,omitempty"`
	Stream     string `json:"stream,omitempty"`
	Text       string `json:"text,omitempty"`
	Shell      string `json:"shell,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Error      string `json:"error,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Stopped    bool   `json:"stopped,omitempty"`
	Limited    bool   `json:"output_limited,omitempty"`
}

// handleExecCommand runs a command and streams its output as NDJSON.
func (s *Server) handleExecCommand(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, "O console de comandos exige uma conta de administrador.") {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxWebBody)
	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "comando inválido"})
		return
	}

	auditRecord(r, req.App, auditCommandDetail(req.Command))

	request, err := s.buildExecRequest(req)
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, s.paths.issue(err))
		return
	}

	runner := command.NewRunner()
	if request, err = runner.Validate(request); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, s.paths.issue(err))
		return
	}

	select {
	case s.execSlots <- struct{}{}:
		defer func() { <-s.execSlots }()
	default:
		httputil.WriteJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": fmt.Sprintf("já há %d comandos em execução. Aguarde um deles terminar.", maxConcurrentExec),
		})
		return
	}

	// The command outlives the browser's patience: a page that is closed or
	// a connection that drops must not kill a migration halfway through. The
	// timeout the caller chose, and the stop endpoint, are what end it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	id := s.registerExec(cancel)
	defer s.releaseExec(id)

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	encoder := json.NewEncoder(w)
	controller := http.NewResponseController(w)
	send := func(event execEvent) {
		if err := encoder.Encode(event); err != nil {
			return
		}
		_ = controller.Flush()
	}

	send(execEvent{Type: "start", ID: id, Shell: command.ShellFor(request), Cwd: request.Cwd})

	var streamed int
	limited := false
	sink := func(event command.Event) {
		if streamed >= maxStreamedOutput {
			limited = true
			return
		}
		text := event.Text
		if streamed+len(text) > maxStreamedOutput {
			text = text[:maxStreamedOutput-streamed]
			limited = true
		}
		streamed += len(text)
		send(execEvent{Type: "output", Stream: string(event.Stream), Text: text})
	}

	result, runErr := runner.Run(ctx, request, sink)
	if runErr != nil {
		send(execEvent{Type: "error", Error: runErr.Error()})
		return
	}

	exitCode := result.ExitCode
	send(execEvent{
		Type:       "end",
		ExitCode:   &exitCode,
		DurationMS: result.Duration.Milliseconds(),
		TimedOut:   result.TimedOut,
		Stopped:    result.Stopped,
		Limited:    limited,
	})
}

// registerExec records a running command so it can be stopped, and returns
// the identifier the browser uses to ask for that.
func (s *Server) registerExec(cancel context.CancelFunc) string {
	id := uuid.NewString()
	s.execMu.Lock()
	defer s.execMu.Unlock()
	if s.execRunning == nil {
		s.execRunning = make(map[string]context.CancelFunc)
	}
	s.execRunning[id] = cancel
	return id
}

// releaseExec forgets a command that has finished.
func (s *Server) releaseExec(id string) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	delete(s.execRunning, id)
}

// handleStopExecCommand ends a command the console is still streaming.
func (s *Server) handleStopExecCommand(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r, "O console de comandos exige uma conta de administrador.") {
		return
	}
	id := chi.URLParam(r, "id")
	s.execMu.Lock()
	cancel, running := s.execRunning[id]
	s.execMu.Unlock()
	if !running {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "esse comando já terminou"})
		return
	}
	auditRecord(r, id, "")
	cancel()
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

// buildExecRequest turns the browser's request into a runner request, applying
// the application context and the panel's path translation.
func (s *Server) buildExecRequest(req execRequest) (command.Request, error) {
	out := command.Request{
		Command: req.Command,
		Args:    req.Args,
		Env:     req.Env,
		EnvFile: req.EnvFile,
		Shell:   req.Shell,
	}

	cwd := strings.TrimSpace(req.Cwd)
	if req.App != "" {
		config, _, err := s.supervisor.ProcessSettings(req.App)
		if err != nil {
			return out, fmt.Errorf("aplicação %q: %w", req.App, err)
		}
		if cwd == "" {
			cwd = config.Cwd
		}
		if out.EnvFile == "" {
			out.EnvFile = config.EnvFile
		}
		if len(config.Env) > 0 {
			merged := make(map[string]string, len(config.Env)+len(req.Env))
			for name, value := range config.Env {
				merged[name] = value
			}
			// What the console typed wins over the application's own values.
			for name, value := range req.Env {
				merged[name] = value
			}
			out.Env = merged
		}
	}

	if cwd != "" {
		resolved, err := s.paths.absolute(cwd, "")
		if err != nil {
			return out, err
		}
		out.Cwd = resolved
	}
	if out.EnvFile != "" {
		resolved, err := s.paths.translate(out.EnvFile)
		if err != nil {
			return out, err
		}
		out.EnvFile = resolved
	}
	if req.Shell != "" {
		resolved, err := s.paths.commandPath(req.Shell)
		if err != nil {
			return out, err
		}
		out.Shell = resolved
	}

	if req.TimeoutSeconds > 0 {
		out.Timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	return out, nil
}

// assignedValue matches a NAME=value word, so the value can be dropped before
// the command reaches the audit log.
var assignedValue = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)=[^\s]+`)

// auditCommandDetail shortens a command for the audit entry without changing
// what it says: the first line identifies a script, and the length is capped.
//
// The value of every NAME=value word is replaced, because a command line is
// one of the few places where someone may type a password inline
// (`PGPASSWORD=… pg_dump`). The audit log never stores a secret, so the name
// is kept and the value is not.
func auditCommandDetail(commandText string) string {
	trimmed := strings.TrimSpace(commandText)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	detail := assignedValue.ReplaceAllString(strings.TrimSpace(lines[0]), "$1=***")
	if len(lines) > 1 {
		detail += fmt.Sprintf(" (+%d linhas)", len(lines)-1)
	}
	if len(detail) > execAuditCommandLimit {
		detail = detail[:execAuditCommandLimit] + "…"
	}
	return detail
}

// handleConsolePage serves the command console.
func (s *Server) handleConsolePage(w http.ResponseWriter, r *http.Request) {
	page, err := frontendFS.ReadFile("frontend/console.html")
	if err != nil {
		http.Error(w, "unable to render console", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}
