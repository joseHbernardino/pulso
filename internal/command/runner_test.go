package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunCapturesBothStreamsAndExitCode(t *testing.T) {
	result, err := NewRunner().Run(context.Background(), Request{
		Command: "echo saida; echo falha >&2; exit 3",
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", result.ExitCode)
	}
	if result.Succeeded() {
		t.Error("a command that exits 3 must not be reported as successful")
	}
	if !strings.Contains(result.Stdout, "saida") {
		t.Errorf("Stdout = %q", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "falha") {
		t.Errorf("Stderr = %q", result.Stderr)
	}
}

func TestRunStreamsOutputAsItArrives(t *testing.T) {
	var (
		mu     sync.Mutex
		events []Event
	)
	result, err := NewRunner().Run(context.Background(), Request{Command: "echo um; echo dois >&2"}, func(event Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Succeeded() {
		t.Fatalf("command failed: exit %d, stderr %q", result.ExitCode, result.Stderr)
	}

	var stdout, stderr strings.Builder
	mu.Lock()
	for _, event := range events {
		switch event.Stream {
		case StreamStdout:
			stdout.WriteString(event.Text)
		case StreamStderr:
			stderr.WriteString(event.Text)
		default:
			t.Errorf("unexpected stream %q", event.Stream)
		}
	}
	mu.Unlock()
	if !strings.Contains(stdout.String(), "um") || !strings.Contains(stderr.String(), "dois") {
		t.Errorf("streamed stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunUsesWorkingDirectoryEnvironmentAndArguments(t *testing.T) {
	dir := t.TempDir()
	result, err := NewRunner().Run(context.Background(), Request{
		Command: `printf '%s|%s|%s' "$PWD" "$GREETING" "$1"`,
		Args:    []string{"primeiro"},
		Cwd:     dir,
		Env:     map[string]string{"GREETING": "ola"},
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// macOS resolves the temp directory through a symlink, so compare the
	// resolved paths rather than the strings.
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(result.Stdout, "|")
	if len(parts) != 3 {
		t.Fatalf("Stdout = %q", result.Stdout)
	}
	gotDir, err := filepath.EvalSymlinks(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != wantDir {
		t.Errorf("working directory = %q, want %q", gotDir, wantDir)
	}
	if parts[1] != "ola" {
		t.Errorf("environment = %q, want ola", parts[1])
	}
	if parts[2] != "primeiro" {
		t.Errorf("positional parameter = %q, want primeiro", parts[2])
	}
}

func TestRunLoadsDotenvBeforeRequestEnvironment(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("FROM_FILE=file\nOVERRIDDEN=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := NewRunner().Run(context.Background(), Request{
		Command: `printf '%s|%s' "$FROM_FILE" "$OVERRIDDEN"`,
		Cwd:     dir,
		EnvFile: envPath,
		Env:     map[string]string{"OVERRIDDEN": "request"},
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "file|request" {
		t.Errorf("Stdout = %q, want file|request", result.Stdout)
	}
}

func TestRunMarksPulsoInTheEnvironment(t *testing.T) {
	result, err := NewRunner().Run(context.Background(), Request{Command: `printf '%s' "$PULSO_EXEC"`}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "true" {
		t.Errorf("PULSO_EXEC = %q, want true", result.Stdout)
	}
}

func TestRunTimesOutAndKillsTheProcessGroup(t *testing.T) {
	start := time.Now()
	result, err := NewRunner().Run(context.Background(), Request{
		Command: "sleep 30 & sleep 30; wait",
		Timeout: 300 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.TimedOut {
		t.Error("TimedOut was not reported")
	}
	if result.Succeeded() {
		t.Error("a command killed by its timeout must not be reported as successful")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the run took %s: the process group was not ended", elapsed)
	}
}

func TestRunReportsCancellationAsStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	result, err := NewRunner().Run(ctx, Request{Command: "sleep 30", Timeout: 30 * time.Second}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Stopped {
		t.Error("Stopped was not reported for a cancelled command")
	}
	if result.TimedOut {
		t.Error("a cancelled command is not a timeout")
	}
}

func TestRunKeepsOnlyTheTailOfLongOutput(t *testing.T) {
	result, err := NewRunner().Run(context.Background(), Request{
		Command:   "for i in 1 2 3 4 5 6 7 8 9; do printf '%s' 0123456789; done; printf 'FIM'",
		MaxOutput: 16,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Stdout) > 16 {
		t.Errorf("Stdout kept %d bytes, want at most 16", len(result.Stdout))
	}
	if !strings.HasSuffix(result.Stdout, "FIM") {
		t.Errorf("Stdout = %q: the end of the output must be what is kept", result.Stdout)
	}
	if !result.Truncated {
		t.Error("Truncated was not reported")
	}
}

func TestValidateRejectsBadRequests(t *testing.T) {
	runner := NewRunner()
	tests := []struct {
		name    string
		request Request
	}{
		{"empty command", Request{Command: "   \n"}},
		{"null byte in command", Request{Command: "echo \x00"}},
		{"oversized command", Request{Command: strings.Repeat("a", MaxCommandLength+1)}},
		{"invalid environment name", Request{Command: "true", Env: map[string]string{"not a name": "x"}}},
		{"null byte in environment", Request{Command: "true", Env: map[string]string{"NAME": "a\x00b"}}},
		{"null byte in argument", Request{Command: "true", Args: []string{"a\x00b"}}},
		{"missing working directory", Request{Command: "true", Cwd: "/definitely/not/here"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := runner.Validate(test.request); err == nil {
				t.Error("expected the request to be rejected")
			}
		})
	}
}

func TestValidateResolvesDefaults(t *testing.T) {
	runner := NewRunner()
	resolved, err := runner.Validate(Request{Command: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %s, want %s", resolved.Timeout, DefaultTimeout)
	}
	if resolved.MaxOutput != DefaultMaxOutput {
		t.Errorf("MaxOutput = %d, want %d", resolved.MaxOutput, DefaultMaxOutput)
	}
	if !filepath.IsAbs(resolved.Cwd) {
		t.Errorf("Cwd = %q, want an absolute path", resolved.Cwd)
	}

	clamped, err := runner.Validate(Request{Command: "true", Timeout: 10 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if clamped.Timeout != MaxTimeout {
		t.Errorf("Timeout = %s, want it clamped to %s", clamped.Timeout, MaxTimeout)
	}
}

func TestRunMultiLineScript(t *testing.T) {
	result, err := NewRunner().Run(context.Background(), Request{Command: `
set -e
primeiro=um
echo "$primeiro"
echo dois
`}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "um\ndois\n" {
		t.Errorf("Stdout = %q, want \"um\\ndois\\n\"", result.Stdout)
	}
}

func TestShellForReportsTheInterpreter(t *testing.T) {
	if got := ShellFor(Request{Shell: "/bin/dash"}); got != "/bin/dash" {
		t.Errorf("ShellFor = %q, want /bin/dash", got)
	}
	if got := ShellFor(Request{}); got == "" {
		t.Error("ShellFor must name a shell even when none was requested")
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	tail := newTailBuffer(5)
	tail.WriteString("abc")
	tail.WriteString("de")
	if got := tail.String(); got != "abcde" {
		t.Fatalf("String() = %q, want abcde", got)
	}
	if tail.Truncated() {
		t.Error("nothing was dropped yet")
	}
	tail.WriteString("fg")
	if got := tail.String(); got != "cdefg" {
		t.Errorf("String() = %q, want cdefg", got)
	}
	if !tail.Truncated() {
		t.Error("Truncated was not reported after dropping bytes")
	}
	tail.WriteString("0123456789")
	if got := tail.String(); got != "56789" {
		t.Errorf("String() = %q, want 56789", got)
	}
}
