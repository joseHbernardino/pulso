package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"pulso.local/pulso/internal/command"
	"pulso.local/pulso/internal/daemon"
	"pulso.local/pulso/internal/runtime"
	"pulso.local/pulso/pkg/types"
)

// execCommandSource describes where the command text came from, for the
// error message when nothing was provided.
const execCommandSource = "give a command as an argument, with --file, or on standard input"

func newExecCmd() *cobra.Command {
	var (
		cwd        string
		envVars    []string
		envFile    string
		shell      string
		scriptFile string
		timeout    time.Duration
		appTarget  string
		quiet      bool
		format     string
	)

	cmd := &cobra.Command{
		Use:   "exec [command] [args...]",
		Short: "Run a one-off command or script on this server",
		Long: `Run a command through a shell, stream its output, and exit with its status.

The command is a full shell line, so pipes, conditionals, redirections and
variables all work. A multi-line script is accepted the same way — from
--file, from standard input, or as a quoted argument.

Arguments after the command become the script's positional parameters, which
it reads as "$1", "$2" and "$@".

Use --app to borrow a managed application's working directory, environment and
.env file, which is the quickest way to run a migration or a maintenance task
in the same context the application runs in.

Pulso exits with the command's own exit code, so it composes with shell
conditionals and CI steps.`,
		Example: `  # A command line, with a pipe
  pulso exec "docker ps --format '{{.Names}}' | sort"

  # A script file on the server
  pulso exec --file /srv/minha-api/deploy.sh

  # A script on standard input
  cat deploy.sh | pulso exec --file -

  # Positional parameters
  pulso exec 'echo "ambiente: $1"' production

  # In an application's own directory and environment
  pulso exec --app api "rails db:migrate"

  # Machine-readable result
  pulso exec --output json "uptime"`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			script, scriptArgs, err := resolveExecScript(cobraCmd, scriptFile, args)
			if err != nil {
				return err
			}

			request := command.Request{
				Command: script,
				Args:    scriptArgs,
				Cwd:     cwd,
				Env:     parseEnvs(envVars),
				EnvFile: envFile,
				Shell:   shell,
				Timeout: timeout,
			}

			if appTarget != "" {
				if err := applyAppContext(&request, appTarget); err != nil {
					return err
				}
			}

			runner := command.NewRunner()
			if dryRun {
				resolved, err := runner.Validate(request)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(os.Stdout, "[Pulso] would run in %s:\n%s\n", resolved.Cwd, resolved.Command)
				return nil
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			sink := func(event command.Event) {
				target := os.Stdout
				if event.Stream == command.StreamStderr {
					target = os.Stderr
				}
				_, _ = io.WriteString(target, event.Text)
			}
			if quiet || format == "json" {
				sink = nil
			}

			result, err := runner.Run(ctx, request, sink)
			if err != nil {
				return err
			}

			if format == "json" {
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(result); err != nil {
					return err
				}
			} else if result.TimedOut {
				_, _ = fmt.Fprintf(os.Stderr, "[Pulso] command killed after %s\n", request.Timeout)
			}

			if result.Succeeded() {
				return nil
			}
			// The command ran and failed. Its status is the answer, so report
			// it as our own exit code instead of a Pulso error message.
			code := result.ExitCode
			if code <= 0 {
				code = 1
			}
			os.Exit(code)
			return nil
		},
	}

	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (default: the current directory)")
	cmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "environment variable as NAME=value (repeatable)")
	cmd.Flags().StringVar(&envFile, "env-file", "", "dotenv file to load before --env")
	cmd.Flags().StringVar(&shell, "shell", "", "interpreter to use (default: bash when installed, else sh)")
	cmd.Flags().StringVarP(&scriptFile, "file", "f", "", "read the script from a file, or from - for standard input")
	cmd.Flags().DurationVar(&timeout, "timeout", command.DefaultTimeout, "kill the command after this long")
	cmd.Flags().StringVar(&appTarget, "app", "", "run with this application's directory, environment and .env")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "do not stream the command's output")
	cmd.Flags().StringVarP(&format, "output", "o", "text", "output format: text or json")

	return cmd
}

// resolveExecScript decides what to run: a file, standard input, or the
// positional arguments. The first argument is the script; the rest are its
// positional parameters.
func resolveExecScript(cobraCmd *cobra.Command, scriptFile string, args []string) (string, []string, error) {
	if scriptFile == "" {
		if len(args) == 0 {
			return "", nil, errors.New(execCommandSource)
		}
		return args[0], args[1:], nil
	}

	if scriptFile == "-" {
		content, err := io.ReadAll(cobraCmd.InOrStdin())
		if err != nil {
			return "", nil, fmt.Errorf("read script from standard input: %w", err)
		}
		if strings.TrimSpace(string(content)) == "" {
			return "", nil, errors.New("the script on standard input is empty")
		}
		return string(content), args, nil
	}

	content, err := os.ReadFile(scriptFile)
	if err != nil {
		return "", nil, fmt.Errorf("read script: %w", err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return "", nil, fmt.Errorf("script %q is empty", scriptFile)
	}
	return string(content), args, nil
}

// applyAppContext fills the request's working directory, environment and
// dotenv path from a managed application, without overriding what the caller
// asked for explicitly.
func applyAppContext(request *command.Request, target string) error {
	config, err := lookupProcessConfig(target)
	if err != nil {
		return err
	}

	if request.Cwd == "" {
		request.Cwd = config.Cwd
	}
	if request.EnvFile == "" {
		request.EnvFile = config.EnvFile
	}
	if len(config.Env) > 0 {
		merged := make(map[string]string, len(config.Env)+len(request.Env))
		for name, value := range config.Env {
			merged[name] = value
		}
		// Variables given on the command line win over the application's.
		for name, value := range request.Env {
			merged[name] = value
		}
		request.Env = merged
	}
	return nil
}

// lookupProcessConfig reads a managed process's configuration, through the
// daemon when it is running and directly otherwise.
func lookupProcessConfig(target string) (types.ProcessConfig, error) {
	if daemonIsRunning() {
		resp, err := sendIPC(daemon.ActionStatus, daemon.StopPayload{Target: target})
		if err == nil && resp.Success {
			var info types.ProcessInfo
			if err := json.Unmarshal(resp.Data, &info); err != nil {
				return types.ProcessConfig{}, fmt.Errorf("read application %q: %w", target, err)
			}
			return info.Config, nil
		}
		if err != nil {
			return types.ProcessConfig{}, fmt.Errorf("read application %q: %w", target, err)
		}
		return types.ProcessConfig{}, fmt.Errorf("application %q not found: %s", target, resp.Error)
	}

	sup, err := getSupervisor()
	if err != nil {
		return types.ProcessConfig{}, err
	}
	proc, err := sup.Get(target)
	if err != nil {
		return types.ProcessConfig{}, err
	}
	return proc.Info().Config, nil
}

// shellSourceLine reports what a shell application was asked to run, rather
// than the resolved `sh -c` invocation. It is the command line, or the first
// real line of an inline script, and empty for every other runtime.
func shellSourceLine(cfg types.ProcessConfig) string {
	source := cfg.Script
	if source == "" && cfg.Launch != nil {
		source = cfg.Launch.Script
		if source == "" && runtime.NormalizeName(cfg.Launch.Runtime) == "shell" {
			source = cfg.Launch.Entrypoint
		}
	}
	if source == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(source), "\n")
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 160 {
			line = line[:160] + "…"
		}
		if remaining := len(lines) - index - 1; remaining > 0 {
			return fmt.Sprintf("%s (+%d more lines)", line, remaining)
		}
		return line
	}
	return ""
}
