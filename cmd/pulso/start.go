package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/daemon"
	"pulso.local/pulso/internal/metrics"
	"pulso.local/pulso/internal/runtime"
	"pulso.local/pulso/internal/supervisor"
	"pulso.local/pulso/pkg/types"
)

func newStartCmd() *cobra.Command {
	var (
		onlyFlag    string
		name        string
		runtimeName string
		workingDir  string
		envVars     []string
		watch       bool
		watchIgnore []string
		restartPol  string
		maxRestarts int
		instances   int
		namespace   string
		useBundle   bool
		script      string
		shellBinary string
	)

	cmd := &cobra.Command{
		Use:   "start [entrypoint]",
		Short: "Start a process",
		Long: `Start a managed process. Pulso will auto-detect the runtime if not specified.

With no arguments, starts all processes defined in pulso.yaml (with autostart: true).
Use --only to start specific processes by name (comma-separated).

Supports Go binaries/scripts, Python scripts, Node.js/TypeScript apps, Bun apps, Deno apps, Ruby apps, and PHP apps.

With --runtime shell the entrypoint is a full shell command line, so pipes,
conditionals and redirections work:

  pulso start --runtime shell "node server.js | tee -a /var/log/app.log"

With --script the process is an inline shell script, and no entrypoint is
needed:

  pulso start --name backup --script 'pg_dump app | gzip > /bak/app.sql.gz'

A .sh file is run as a script by the shell runtime:

  pulso start --runtime shell /srv/minha-api/start.sh`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runtimeName = runtime.NormalizeName(runtimeName)
			shellRequested := runtimeName == "shell" || script != ""

			// No args and no inline script: start everything in the config
			// file. An inline script is itself the program, so it never means
			// "start the config file".
			if len(args) == 0 && script == "" {
				if shellRequested {
					return fmt.Errorf("--runtime shell needs a command line, a .sh file, or --script")
				}
				configPath := resolveConfigPath(cmd)
				return startAllFromConfig(onlyFlag, configPath)
			}

			entrypoint := ""
			var extraArgs []string
			if len(args) > 0 {
				entrypoint = args[0]
				extraArgs = args[1:]
			}
			if script != "" && entrypoint != "" {
				// The script is the program; a positional argument alongside
				// it would silently be ignored.
				extraArgs = args
				entrypoint = ""
			}

			// When the entrypoint is a quoted command string like "bun dev",
			// split it into binary + args (pm2-style). A shell command line is
			// left whole: splitting it would lose the pipes and operators that
			// are the reason for asking for a shell.
			if !shellRequested && len(extraArgs) == 0 && strings.Contains(entrypoint, " ") {
				parts, err := splitShellArgs(entrypoint)
				if err != nil {
					return fmt.Errorf("failed to parse command: %w", err)
				}
				if len(parts) > 1 {
					entrypoint = parts[0]
					extraArgs = parts[1:]
				}
			}

			if name == "" {
				if shellRequested {
					name = shellProcessName(script, entrypoint)
				} else {
					name = filepath.Base(entrypoint)
				}
			}
			cwd := workingDir
			if cwd == "" {
				cwd, _ = os.Getwd()
			}

			// Resolve runtime.
			resolvedRuntime := runtimeName
			detector := runtime.NewDetector()
			if shellRequested {
				resolvedRuntime = "shell"
			}
			if resolvedRuntime == "" || resolvedRuntime == "auto" {
				// First, check if the entrypoint itself is a known runtime binary.
				// This handles cases like "bun dev", "node server.js", "python app.py".
				entryBin := filepath.Base(entrypoint)
				if _, err := detector.Get(entryBin); err == nil {
					resolvedRuntime = entryBin
					log.Debug().Str("runtime", resolvedRuntime).Msg("detected runtime from entrypoint")
				} else if _, err := exec.LookPath(entrypoint); err == nil {
					// Entrypoint is an executable on PATH (e.g. "sleep", "echo")
					// — don't inherit runtime from project files.
					resolvedRuntime = "process"
					log.Debug().Str("runtime", resolvedRuntime).Msg("detected generic process from entrypoint")
				} else {
					rt, err := detector.Detect(cwd)
					if err == nil {
						resolvedRuntime = rt.Name()
					} else {
						resolvedRuntime = "unknown"
					}
					log.Debug().Str("runtime", resolvedRuntime).Msg("auto-detected runtime from project files")
				}
			}

			// Resolve actual command via runtime adapter.
			finalEntrypoint := entrypoint
			finalArgs := extraArgs
			if rt, err := detector.Get(resolvedRuntime); err == nil {
				resolvedCmd, cmdErr := rt.StartCmd(runtime.StartOptions{
					Entrypoint:  entrypoint,
					Args:        extraArgs,
					Cwd:         cwd,
					Env:         parseEnvs(envVars),
					UseBundle:   useBundle,
					Script:      script,
					Interpreter: shellBinary,
				})
				if cmdErr != nil && shellRequested {
					return cmdErr
				}
				if cmdErr == nil && resolvedCmd != nil {
					finalEntrypoint = resolvedCmd.Path
					if len(resolvedCmd.Args) > 1 {
						finalArgs = resolvedCmd.Args[1:]
					}
				}
			}

			if dryRun {
				_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Dry run: would start %q\n", name)
				_, _ = fmt.Fprintf(os.Stdout, "  runtime:    %s\n", resolvedRuntime)
				_, _ = fmt.Fprintf(os.Stdout, "  entrypoint: %s\n", finalEntrypoint)
				_, _ = fmt.Fprintf(os.Stdout, "  args:       %v\n", finalArgs)
				_, _ = fmt.Fprintf(os.Stdout, "  cwd:        %s\n", cwd)
				_, _ = fmt.Fprintf(os.Stdout, "  instances:  %d\n", instances)
				if script != "" {
					_, _ = fmt.Fprintf(os.Stdout, "  script:\n%s\n", script)
				}
				return nil
			}

			cfg := types.ProcessConfig{
				Name:          name,
				Runtime:       resolvedRuntime,
				Entrypoint:    finalEntrypoint,
				Args:          finalArgs,
				Script:        script,
				Cwd:           cwd,
				Env:           parseEnvs(envVars),
				RestartPolicy: types.RestartPolicy(restartPol),
				MaxRestarts:   maxRestarts,
				Instances:     instances,
				Namespace:     namespace,
			}

			if shellRequested {
				cfg.Launch = &types.LaunchConfig{
					Runtime:     "shell",
					Entrypoint:  entrypoint,
					Args:        extraArgs,
					Interpreter: shellBinary,
					Script:      script,
				}
			}

			if watch {
				cfg.Watch = &types.WatchConfig{
					Enabled: true,
					Paths:   []string{cwd},
					Ignore:  watchIgnore,
				}
			}

			if cfg.Instances < 1 {
				cfg.Instances = 1
			}

			for i := 0; i < cfg.Instances; i++ {
				instanceCfg := cfg
				instanceCfg.InstanceIndex = i
				if cfg.Instances > 1 {
					instanceCfg.Name = fmt.Sprintf("%s:%d", cfg.Name, i)
				}

				// Always try daemon IPC first (auto-starts daemon if needed).
				payload := daemon.StartPayload{
					Name:          instanceCfg.Name,
					Runtime:       instanceCfg.Runtime,
					Entrypoint:    instanceCfg.Entrypoint,
					Args:          instanceCfg.Args,
					Cwd:           instanceCfg.Cwd,
					Env:           instanceCfg.Env,
					RestartPolicy: string(instanceCfg.RestartPolicy),
					MaxRestarts:   instanceCfg.MaxRestarts,
				}
				resp, err := sendIPC(daemon.ActionStart, payload)
				if err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Daemon IPC failed, using direct mode: %v\n", err)
				} else if !resp.Success {
					return fmt.Errorf("daemon error: %s", resp.Error)
				} else {
					var info types.ProcessInfo
					if err := json.Unmarshal(resp.Data, &info); err == nil {
						_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Process %q started (id: %d, pid: %d)\n", info.Name, info.NumericID, info.PID)
					}
					continue
				}

				sup, err := getSupervisor()
				if err != nil {
					return err
				}

				// Direct mode fallback.
				proc, err := sup.AddProcess(context.Background(), instanceCfg)
				if err != nil {
					return fmt.Errorf("failed to start process: %w", err)
				}

				info := proc.Info()
				_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Process %q started (id: %d, pid: %d)\n", info.Name, info.NumericID, info.PID)
			}

			showSpeedList()
			return nil
		},
	}

	cmd.Flags().StringVar(&onlyFlag, "only", "", "start only specific processes by name (comma-separated, requires config file)")
	cmd.Flags().StringVarP(&name, "name", "n", "", "process name (default: entrypoint filename)")
	cmd.Flags().StringVarP(&runtimeName, "runtime", "r", "", "runtime: go, python, node, bun, deno, ruby, php, shell, auto (default: auto-detect)")
	cmd.Flags().StringVarP(&workingDir, "cwd", "d", "", "working directory (default: current)")
	cmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "environment variables (KEY=VAL, repeatable)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "watch for file changes and auto-restart")
	cmd.Flags().StringArrayVar(&watchIgnore, "watch-ignore", nil, "patterns to ignore when watching")
	cmd.Flags().StringVar(&restartPol, "restart-policy", "", "restart policy: always, on-failure, never")
	cmd.Flags().IntVar(&maxRestarts, "max-restarts", 0, "max restarts within window (0 = use default)")
	cmd.Flags().IntVar(&maxRestarts, "max-retry", 0, "alias for --max-restarts")
	cmd.Flags().IntVar(&instances, "instances", 1, "number of instances to start")
	cmd.Flags().StringVar(&namespace, "namespace", "", "process namespace")
	cmd.Flags().BoolVar(&useBundle, "use-bundle", false, "wrap entrypoint with bundle exec (Ruby)")
	cmd.Flags().StringVar(&script, "script", "", "inline shell script to supervise (implies --runtime shell)")
	cmd.Flags().StringVar(&shellBinary, "shell", "", "interpreter for --runtime shell (default: bash when installed, else sh)")

	return cmd
}

// startAllFromConfig starts all processes defined in pulso.yaml via daemon IPC.
func startAllFromConfig(onlyFlag string, configPath string) error {
	resp, err := sendIPC(daemon.ActionStartAll, daemon.StartAllPayload{
		Only:   onlyFlag,
		Config: configPath,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}

	var result struct {
		Started []types.ProcessInfo `json:"started"`
		Errors  []string            `json:"errors"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	for _, info := range result.Started {
		_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Process %q started (id: %d, pid: %d)\n", info.Name, info.NumericID, info.PID)
	}
	for _, e := range result.Errors {
		_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Error: %s\n", e)
	}

	if len(result.Started) == 0 && len(result.Errors) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "[Pulso] No processes to start (check pulso.yaml)")
	}

	showSpeedList()
	return nil
}

func parseEnvs(envVars []string) map[string]string {
	env := make(map[string]string)
	for _, e := range envVars {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

// cliCollector is the shared metrics collector for CLI direct mode.
// Initialized once by getSupervisor and stopped by the finalizer.
var (
	cliCollector     *metrics.Collector
	cliCollectorOnce sync.Once
)

func getSupervisor() (*supervisor.Supervisor, error) {
	// Initialize the shared collector exactly once.
	cliCollectorOnce.Do(func() {
		cliCollector = metrics.NewCollector()
		cliCollector.Start(5 * time.Second)
	})

	opts := supervisor.Options{
		LogDir: dataDir(),
		Defaults: types.DefaultsConfig{
			RestartPolicy: types.RestartOnFailure,
			MaxRestarts:   10,
		},
		MetricsCollector: cliCollector,
		MetricsInterval:  5 * time.Second,
	}

	if err := os.MkdirAll(opts.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	log.Debug().Str("log_dir", opts.LogDir).Msg("creating supervisor")
	return supervisor.New(opts), nil
}

// shellNoiseWords are the leading words that say nothing about what a script
// does, so they never become its name.
var shellNoiseWords = map[string]bool{
	"set": true, "export": true, "cd": true, "source": true, ".": true,
	"umask": true, "shopt": true, "trap": true, "ulimit": true, "unset": true,
	"readonly": true, "local": true, "declare": true, "eval": true,
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"while": true, "until": true, "for": true, "do": true, "done": true,
	"case": true, "esac": true, "function": true, "exec": true, "echo": true,
}

// shellProcessName derives a process name from an inline script or a command
// line, so that `--script` does not also require `--name`. It takes the first
// word that actually names a program, skipping assignments, redirections and
// shell keywords, the way a person reading the script would.
func shellProcessName(script, entrypoint string) string {
	source := script
	if strings.TrimSpace(source) == "" {
		source = entrypoint
	}
	fallback := ""
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, field := range strings.Fields(line) {
			// Skip a leading variable assignment such as `ENV=production cmd`.
			if strings.Contains(field, "=") || strings.HasPrefix(field, "-") {
				continue
			}
			name := sanitizeProcessName(filepath.Base(field))
			if name == "" {
				break
			}
			if shellNoiseWords[strings.ToLower(name)] {
				// Remember it: a script that only echoes is still better
				// named "echo" than "shell".
				if fallback == "" {
					fallback = name
				}
				break
			}
			return name
		}
	}
	if fallback != "" {
		return fallback
	}
	return "shell"
}

// sanitizeProcessName keeps only the characters a process name accepts, and
// returns "" when nothing usable is left.
func sanitizeProcessName(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '.' || r == '_' || r == '-' || r == ':':
			if out.Len() > 0 {
				out.WriteRune(r)
			}
		}
	}
	return strings.Trim(out.String(), "._-:")
}
