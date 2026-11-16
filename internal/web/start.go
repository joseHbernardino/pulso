package web

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"pulso.local/pulso/internal/runtime"
	"pulso.local/pulso/pkg/types"
)

var webProcessName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var webEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// prepareWebProcess resolves a script's language adapter once, before handing
// its executable and argument vector to the supervisor. Arguments are never
// passed through a shell.
func prepareWebProcess(cfg types.ProcessConfig) (types.ProcessConfig, error) {
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Entrypoint = strings.TrimSpace(cfg.Entrypoint)
	if !webProcessName.MatchString(cfg.Name) {
		return cfg, fmt.Errorf("use a process name starting with a letter or number, followed by letters, numbers, dots, underscores, colons or hyphens")
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	for key, value := range cfg.Env {
		if !webEnvName.MatchString(key) || strings.ContainsRune(value, 0) {
			return cfg, fmt.Errorf("invalid environment variable %q", key)
		}
	}

	var err error
	if cfg.Cwd == "" {
		cfg.Cwd, err = os.Getwd()
	} else {
		cfg.Cwd, err = filepath.Abs(cfg.Cwd)
	}
	if err != nil {
		return cfg, fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(cfg.Cwd)
	if err != nil {
		return cfg, fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return cfg, fmt.Errorf("working directory must be a folder on the server")
	}

	if cfg.Runtime == "auto" {
		switch strings.ToLower(filepath.Ext(cfg.Entrypoint)) {
		case ".py":
			cfg.Runtime = "python"
		case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx":
			cfg.Runtime = "node"
		case ".go":
			cfg.Runtime = "go"
		case ".rb":
			cfg.Runtime = "ruby"
		case ".php":
			cfg.Runtime = "php"
		default:
			cfg.Runtime = "process"
		}
	}
	// Preserve direct executable requests from existing API clients.
	if cfg.Runtime == "" || cfg.Runtime == "unknown" || cfg.Runtime == "process" {
		return cfg, nil
	}

	adapter, err := runtime.NewDetector().Get(cfg.Runtime)
	if err != nil {
		return cfg, err
	}
	cmd, err := adapter.StartCmd(runtime.StartOptions{
		Entrypoint:  cfg.Entrypoint,
		Args:        cfg.Args,
		Cwd:         cfg.Cwd,
		Env:         cfg.Env,
		Interpreter: cfg.Interpreter,
		UseBundle:   cfg.UseBundle,
	})
	if err != nil {
		return cfg, err
	}
	cfg.Entrypoint = cmd.Path
	cfg.Args = cmd.Args[1:]
	cfg.Interpreter = ""
	cfg.UseBundle = false
	return cfg, nil
}
