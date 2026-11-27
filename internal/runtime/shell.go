package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

// DefaultShell is used when neither an explicit interpreter nor PULSO_SHELL
// names one and bash is unavailable. Every supported platform has /bin/sh.
const DefaultShell = "sh"

// shellScriptExtensions are the file extensions that identify a script file
// rather than a command line.
var shellScriptExtensions = map[string]bool{
	".sh":   true,
	".bash": true,
	".ksh":  true,
	".zsh":  true,
}

// ShellRuntime runs a command line or a shell script through a POSIX shell.
//
// It is the escape hatch for everything the language adapters do not cover:
// a command with a pipe, a conditional or a redirection, a `.sh` file on the
// server, or an inline script written in the dashboard. Unlike the other
// adapters, it deliberately hands the string to a shell, so the caller is
// trusting whoever wrote the command — which is why the web API restricts it
// to administrators.
type ShellRuntime struct{}

// Name returns the runtime identifier.
func (s *ShellRuntime) Name() string { return "shell" }

// Detect never claims a project. The shell runtime is always requested
// explicitly, so auto-detection must not fall back to it: a project with a
// stray `start.sh` should still be detected as Node, Python or Go.
func (s *ShellRuntime) Detect(_ string) bool { return false }

// StartCmd builds the shell invocation for opts.
func (s *ShellRuntime) StartCmd(opts StartOptions) (*exec.Cmd, error) {
	shell, args, err := ShellCommandLine(opts)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(shell, args...)
	cmd.Dir = opts.Cwd
	cmd.Env = buildEnv(opts.Env)

	log.Debug().
		Str("runtime", "shell").
		Str("shell", shell).
		Strs("args", args).
		Str("cwd", opts.Cwd).
		Msg("built start command")

	return cmd, nil
}

// ShellCommandLine resolves the shell binary and its argument vector for a
// command line, a script file or an inline script. It builds no command, so
// callers that need a context-bound process can use exec.CommandContext.
//
// Three shapes are supported, in this order:
//
//  1. opts.Script is set — the script is passed with `-c`, and opts.Args
//     become its positional parameters ("$1", "$2", "$@").
//  2. opts.Entrypoint names a script file (an existing file, or one with a
//     shell extension) — the file is run as `<shell> <file> <args...>`.
//  3. otherwise opts.Entrypoint is a command line — the arguments are
//     shell-quoted and appended to it, then the whole line is passed with
//     `-c`.
func ShellCommandLine(opts StartOptions) (string, []string, error) {
	shell := ShellBinary(opts.Interpreter)

	script := strings.TrimSpace(opts.Script)
	if script != "" {
		// `sh -c <script> <name> <args...>` sets $0 to <name> and exposes the
		// remaining words as "$@", which is what a script expects.
		args := append([]string{"-c", opts.Script, shellArgv0}, opts.Args...)
		return shell, args, nil
	}

	entrypoint := strings.TrimSpace(opts.Entrypoint)
	if entrypoint == "" {
		return "", nil, fmt.Errorf("shell: a command or a script is required")
	}

	if isShellScriptFile(entrypoint, opts.Cwd) {
		return shell, append([]string{entrypoint}, opts.Args...), nil
	}

	line := entrypoint
	for _, arg := range opts.Args {
		line += " " + ShellQuote(arg)
	}
	return shell, []string{"-c", line, shellArgv0}, nil
}

// shellArgv0 is the $0 reported to the script. It identifies Pulso in a
// script's own error messages and in `ps` output.
const shellArgv0 = "pulso"

// ShellBinary resolves which shell to use. An explicit interpreter wins, then
// PULSO_SHELL, then bash when it is installed, and finally sh.
func ShellBinary(interpreter string) string {
	if shell := strings.TrimSpace(interpreter); shell != "" {
		return shell
	}
	if shell := strings.TrimSpace(os.Getenv("PULSO_SHELL")); shell != "" {
		return shell
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	return DefaultShell
}

// IsShellScriptFile reports whether entrypoint names a script file rather
// than a command line. Callers that must decide whether to treat the value as
// a path — translating it, or checking that it exists — use this.
func IsShellScriptFile(entrypoint, cwd string) bool {
	return isShellScriptFile(strings.TrimSpace(entrypoint), cwd)
}

// isShellScriptFile reports whether entrypoint should be run as a script file
// instead of being interpreted as a command line. A path with a shell
// extension counts even when it does not exist yet, so that a typo is
// reported by the shell as a missing file rather than silently becoming a
// command line.
func isShellScriptFile(entrypoint string, cwd string) bool {
	if strings.ContainsAny(entrypoint, "|&;<>()$`\n*?") {
		return false
	}
	if shellScriptExtensions[strings.ToLower(filepath.Ext(entrypoint))] {
		return true
	}
	if strings.ContainsRune(entrypoint, ' ') {
		return false
	}
	// A bare word such as "uptime" is a command, not a file. Only a path
	// (absolute, or relative to the working directory) is treated as a file.
	if !strings.ContainsRune(entrypoint, os.PathSeparator) {
		return false
	}
	candidate := entrypoint
	if !filepath.IsAbs(candidate) && cwd != "" {
		candidate = filepath.Join(cwd, candidate)
	}
	info, err := os.Stat(candidate)
	return err == nil && !info.IsDir()
}

// ShellQuote wraps a value in single quotes so a shell reads it as one word,
// whatever it contains. An embedded single quote is closed, escaped and
// reopened — the standard POSIX idiom.
func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\r\"'$&|;<>()[]{}*?!#~`\\") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
