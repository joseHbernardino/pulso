// Package command runs one-off shell commands on the server Pulso manages.
//
// It is the counterpart of the shell runtime: the runtime supervises a long
// running command, while this package runs a command once, streams its output
// and reports the exit status. The CLI (`pulso exec`) and the dashboard
// console both use it, so a command behaves the same way from either one.
//
// Nothing here decides who is allowed to run a command. Authorization belongs
// to the caller — the web API restricts it to administrators and records every
// attempt in the audit log.
package command

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"pulso.local/pulso/internal/envfile"
	"pulso.local/pulso/internal/runtime"
)

const (
	// DefaultTimeout bounds a command that does not ask for a limit. A
	// one-off command is expected to finish; anything long-lived belongs in
	// the process table as a supervised application.
	DefaultTimeout = 60 * time.Second

	// MaxTimeout is the ceiling for an explicit timeout.
	MaxTimeout = 30 * time.Minute

	// DefaultMaxOutput is how much of each stream is kept in the result. The
	// tail is kept, because the end of a failing command is what explains it.
	DefaultMaxOutput = 256 * 1024

	// MaxCommandLength bounds the script a caller may submit.
	MaxCommandLength = 64 * 1024

	// readChunk is the size of a single read from the command's output.
	readChunk = 16 * 1024

	// StopGrace is how long a cancelled command has to end on its own after
	// being asked to, before it is forced down.
	StopGrace = 5 * time.Second
)

// envName matches a usable environment variable name.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Request describes one command execution.
type Request struct {
	// Command is a shell command line or a multi-line script. Required.
	Command string
	// Args become the script's positional parameters ("$1", "$2", "$@").
	Args []string
	// Cwd is the working directory. Empty means the directory Pulso runs in.
	Cwd string
	// Env overlays the inherited environment.
	Env map[string]string
	// EnvFile is an optional dotenv file loaded before Env, which wins.
	EnvFile string
	// Shell overrides the interpreter. Empty resolves bash, then sh.
	Shell string
	// Timeout bounds the run. Zero means DefaultTimeout; values above
	// MaxTimeout are clamped.
	Timeout time.Duration
	// MaxOutput is the number of bytes kept per stream in the Result. Zero
	// means DefaultMaxOutput. Streaming is never truncated.
	MaxOutput int
}

// Stream identifies which of the command's outputs an event came from.
type Stream string

const (
	// StreamStdout is the command's standard output.
	StreamStdout Stream = "stdout"
	// StreamStderr is the command's standard error.
	StreamStderr Stream = "stderr"
)

// Event is one chunk of output from a running command.
type Event struct {
	Stream Stream
	Text   string
}

// Result reports how a command ended.
type Result struct {
	// Command is the command line or script that was run.
	Command string `json:"command"`
	// Shell is the interpreter that ran it.
	Shell string `json:"shell"`
	// Cwd is the resolved working directory.
	Cwd string `json:"cwd"`
	// Stdout and Stderr hold the tail of each stream, up to MaxOutput bytes.
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	// ExitCode is the command's exit status, or -1 when it never ran or was
	// killed before reporting one.
	ExitCode int `json:"exit_code"`
	// Truncated reports that the streams held more than MaxOutput bytes.
	Truncated bool `json:"truncated"`
	// TimedOut reports that the command was killed for exceeding the timeout.
	TimedOut bool `json:"timed_out"`
	// Stopped reports that the command was cancelled before it finished.
	Stopped bool `json:"stopped,omitempty"`
	// StartedAt is when the command was launched.
	StartedAt time.Time `json:"started_at"`
	// Duration is how long it ran.
	Duration time.Duration `json:"duration"`
}

// Succeeded reports whether the command exited cleanly.
func (r Result) Succeeded() bool { return r.ExitCode == 0 && !r.TimedOut && !r.Stopped }

// ShellFor reports which interpreter would run the request. The console shows
// it before the command starts, so the operator knows whether bash or sh is
// interpreting what they typed.
func ShellFor(req Request) string { return runtime.ShellBinary(req.Shell) }

// Runner executes commands. It holds no state, so one instance is safe for
// concurrent use.
type Runner struct{}

// NewRunner returns a Runner.
func NewRunner() *Runner { return &Runner{} }

// Validate checks a request without running it, and returns the request with
// its working directory and timeout resolved.
func (r *Runner) Validate(req Request) (Request, error) {
	req.Command = strings.TrimRight(req.Command, " \t\r\n")
	if strings.TrimSpace(req.Command) == "" {
		return req, errors.New("a command is required")
	}
	if len(req.Command) > MaxCommandLength {
		return req, fmt.Errorf("command is longer than %d bytes", MaxCommandLength)
	}
	if strings.ContainsRune(req.Command, 0) {
		return req, errors.New("command contains a null byte")
	}
	for name, value := range req.Env {
		if !envName.MatchString(name) {
			return req, fmt.Errorf("invalid environment variable %q", name)
		}
		if strings.ContainsRune(value, 0) {
			return req, fmt.Errorf("environment variable %q contains a null byte", name)
		}
	}
	for _, arg := range req.Args {
		if strings.ContainsRune(arg, 0) {
			return req, errors.New("argument contains a null byte")
		}
	}

	cwd, err := resolveCwd(req.Cwd)
	if err != nil {
		return req, err
	}
	req.Cwd = cwd

	if req.EnvFile != "" {
		resolved, err := envfile.Resolve(req.EnvFile, req.Cwd)
		if err != nil {
			return req, err
		}
		req.EnvFile = resolved
	}

	switch {
	case req.Timeout <= 0:
		req.Timeout = DefaultTimeout
	case req.Timeout > MaxTimeout:
		req.Timeout = MaxTimeout
	}
	if req.MaxOutput <= 0 {
		req.MaxOutput = DefaultMaxOutput
	}
	return req, nil
}

// Run executes the command and returns its result. Output is forwarded to
// sink as it arrives, which may be nil when only the result matters. Sink is
// called from a single goroutine at a time, so it needs no locking.
//
// A non-zero exit status is not an error: it is reported in the result, the
// way a shell reports it. An error is returned only when the command could
// not be run or its request is invalid.
func (r *Runner) Run(ctx context.Context, req Request, sink func(Event)) (Result, error) {
	req, err := r.Validate(req)
	if err != nil {
		return Result{}, err
	}

	shell, args, err := runtime.ShellCommandLine(runtime.StartOptions{
		Script:      req.Command,
		Args:        req.Args,
		Cwd:         req.Cwd,
		Interpreter: req.Shell,
	})
	if err != nil {
		return Result{}, err
	}

	environment, err := buildEnvironment(req)
	if err != nil {
		return Result{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, shell, args...)
	cmd.Dir = req.Cwd
	cmd.Env = environment
	setProcessGroup(cmd)
	// A cancelled run — a timeout, or an operator pressing stop — asks the
	// whole process group to end, then forces it down if it is still there
	// after the grace period. The reaped channel keeps the hard kill from
	// ever signalling a process id that has already been waited for.
	reaped := make(chan struct{})
	cmd.Cancel = func() error {
		if err := terminateProcessGroup(cmd.Process); err != nil {
			return err
		}
		go func() {
			select {
			case <-reaped:
			case <-time.After(StopGrace):
				_ = killProcessGroup(cmd.Process)
			}
		}()
		return nil
	}
	cmd.WaitDelay = StopGrace + 2*time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("capture stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("capture stderr: %w", err)
	}

	result := Result{
		Command:   req.Command,
		Shell:     shell,
		Cwd:       req.Cwd,
		ExitCode:  -1,
		StartedAt: time.Now(),
	}

	if err := cmd.Start(); err != nil {
		return result, fmt.Errorf("run command: %w", err)
	}

	var (
		mu       sync.Mutex
		outTail  = newTailBuffer(req.MaxOutput)
		errTail  = newTailBuffer(req.MaxOutput)
		streamWG sync.WaitGroup
	)
	emit := func(stream Stream, tail *tailBuffer, text string) {
		mu.Lock()
		tail.WriteString(text)
		if sink != nil {
			sink(Event{Stream: stream, Text: text})
		}
		mu.Unlock()
	}

	streamWG.Add(2)
	go func() {
		defer streamWG.Done()
		forward(stdout, func(text string) { emit(StreamStdout, outTail, text) })
	}()
	go func() {
		defer streamWG.Done()
		forward(stderr, func(text string) { emit(StreamStderr, errTail, text) })
	}()
	streamWG.Wait()

	waitErr := cmd.Wait()
	close(reaped)
	result.Duration = time.Since(result.StartedAt)
	result.Stdout = outTail.String()
	result.Stderr = errTail.String()
	result.Truncated = outTail.Truncated() || errTail.Truncated()

	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		result.Stopped = true
	}

	logEvent := log.Debug().
		Str("shell", shell).
		Str("cwd", req.Cwd).
		Int("exit_code", result.ExitCode).
		Dur("duration", result.Duration).
		Bool("timed_out", result.TimedOut)

	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) && !result.TimedOut && !result.Stopped {
		logEvent.Err(waitErr).Msg("command ended abnormally")
		return result, fmt.Errorf("run command: %w", waitErr)
	}
	logEvent.Msg("command finished")
	return result, nil
}

// forward reads a stream to completion, handing each chunk to emit.
func forward(reader io.Reader, emit func(string)) {
	buffered := bufio.NewReaderSize(reader, readChunk)
	chunk := make([]byte, readChunk)
	for {
		n, err := buffered.Read(chunk)
		if n > 0 {
			emit(string(chunk[:n]))
		}
		if err != nil {
			return
		}
	}
}

// resolveCwd turns a requested working directory into an absolute path and
// verifies it is a folder that exists.
func resolveCwd(requested string) (string, error) {
	cwd := strings.TrimSpace(requested)
	var err error
	if cwd == "" {
		cwd, err = os.Getwd()
	} else {
		cwd, err = filepath.Abs(cwd)
	}
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory %q is not a folder", cwd)
	}
	return cwd, nil
}

// buildEnvironment merges the inherited environment, the dotenv file and the
// request's own variables, in that order of precedence.
func buildEnvironment(req Request) ([]string, error) {
	overlay := map[string]string{}
	if req.EnvFile != "" {
		merged, _, err := envfile.Environment(req.EnvFile, req.Cwd, req.Env)
		if err != nil {
			return nil, err
		}
		overlay = merged
	} else {
		for name, value := range req.Env {
			overlay[name] = value
		}
	}
	overlay["PULSO"] = "true"
	overlay["PULSO_EXEC"] = "true"

	base := os.Environ()
	out := make([]string, 0, len(base)+len(overlay))
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := overlay[name]; replaced {
				continue
			}
		}
		out = append(out, entry)
	}
	for name, value := range overlay {
		out = append(out, name+"="+value)
	}
	return out, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max     int
	buf     []byte
	dropped bool
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) WriteString(text string) {
	if t.max <= 0 {
		return
	}
	if len(text) >= t.max {
		t.buf = append(t.buf[:0], text[len(text)-t.max:]...)
		t.dropped = true
		return
	}
	if excess := len(t.buf) + len(text) - t.max; excess > 0 {
		t.buf = append(t.buf[:0], t.buf[excess:]...)
		t.dropped = true
	}
	t.buf = append(t.buf, text...)
}

func (t *tailBuffer) String() string { return string(t.buf) }

// Truncated reports whether anything was dropped from the front.
func (t *tailBuffer) Truncated() bool { return t.dropped }
