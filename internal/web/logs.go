package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"pulso.local/pulso/internal/httputil"
	"pulso.local/pulso/internal/logutil"
)

// handleGetLogs includes stderr by default and lifecycle information even for silent exits.
func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	proc, err := s.supervisor.Get(chi.URLParam(r, "id"))
	if err != nil {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	stream := r.URL.Query().Get("stream")
	if stream == "" {
		stream = "all"
	}
	if stream != "all" && stream != "stdout" && stream != "stderr" {
		httputil.WriteJSON(w, 400, map[string]string{"error": "use all, stdout ou stderr"})
		return
	}
	lines := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 {
		lines = min(n, 5000)
	}
	warnings := []string{}
	if err := proc.FlushLogs(); err != nil {
		warnings = append(warnings, "Não foi possível descarregar todo o buffer de logs: "+err.Error())
	}
	stdoutPath, stderrPath := s.supervisor.LogPath(proc.ID), s.supervisor.LogPathStderr(proc.ID)
	read := func(path, label string) (string, error) {
		if path == "" {
			return "", nil
		}
		data, err := logutil.ReadFileBounded(path, 1<<20)
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			warnings = append(warnings, "Não foi possível ler "+label+": "+err.Error())
			return "", err
		}
		return logutil.TailLines(string(data), lines), nil
	}
	stdout, outErr := read(stdoutPath, "stdout")
	stderr, errErr := read(stderrPath, "stderr")
	content := stdout
	switch stream {
	case "stderr":
		content = stderr
	case "all":
		if stderr != "" && stdout != "" {
			content = "=== Erros (stderr) ===\n" + stderr + "\n=== Saída (stdout) ===\n" + stdout
		} else if stderr != "" {
			content = stderr
		}
	}
	info := proc.Info()
	result := map[string]any{
		"logs": content, "stdout": stdout, "stderr": stderr, "stream": stream, "lines": strconv.Itoa(lines),
		"path": filepath.Base(stdoutPath), "stderr_path": filepath.Base(stderrPath), "warnings": warnings,
		"process": map[string]any{"id": info.ID, "name": info.Name, "state": info.State, "pid": info.PID,
			"exit_code": info.ExitCode, "restarts": info.Restarts, "last_event": info.LastEvent, "last_reason": info.LastReason,
			"last_failure": info.LastFailure, "started_at": info.StartedAt, "finished_at": info.FinishedAt, "uptime": info.Uptime},
	}
	status := http.StatusOK
	if (stream == "stdout" && outErr != nil) || (stream == "stderr" && errErr != nil) || (outErr != nil && errErr != nil) {
		status = http.StatusInternalServerError
		result["error"] = "Não foi possível ler os logs solicitados. " + strings.Join(warnings, " ")
	}
	httputil.WriteJSON(w, status, result)
}
