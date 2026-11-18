package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"pulso.local/pulso/internal/audit"
	"pulso.local/pulso/internal/auth"
	"pulso.local/pulso/internal/mcp"
	"pulso.local/pulso/internal/metrics"
	"pulso.local/pulso/internal/notify"
	"pulso.local/pulso/internal/supervisor"
	"pulso.local/pulso/pkg/types"
)

// DefaultSocketPath returns the default Unix socket path.
func DefaultSocketPath() string {
	return filepath.Join(DefaultDataDir(), "tmp", "pulso.sock")
}

// DefaultDataDir returns the default data directory.
// Always uses $HOME/.pulso/ (creates it on first access if needed).
func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".pulso")
}

// ResolveDataDir returns cfgDir if non-empty, otherwise DefaultDataDir().
func ResolveDataDir(cfgDir string) string {
	if cfgDir != "" {
		if cfgDir == "~" {
			home, _ := os.UserHomeDir()
			return home
		}
		if strings.HasPrefix(cfgDir, "~/") {
			home, _ := os.UserHomeDir()
			cfgDir = filepath.Join(home, strings.TrimPrefix(cfgDir, "~/"))
		}
		return cfgDir
	}
	return DefaultDataDir()
}

// ResolveSocketPath returns cfgSocket if non-empty, otherwise <dataDir>/tmp/pulso.sock.
func ResolveSocketPath(dataDir, cfgSocket string) string {
	if cfgSocket != "" {
		return ResolveDataDir(cfgSocket)
	}
	return filepath.Join(dataDir, "tmp", "pulso.sock")
}

// StartDaemon forks the daemon process and waits for it to become ready.
func StartDaemon() error { return StartDaemonAt(DefaultDataDir(), DefaultSocketPath()) }
func StartDaemonAt(dataDir, socketPath string) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	cmd := exec.Command(bin, "--data-dir", dataDir, "daemon", "run", "--socket-path", socketPath)
	cmd.Stdout = nil // daemon manages its own logging
	cmd.Stderr = nil // daemon manages its own logging
	setProcessGroup(cmd)

	// Pass the caller's config file path to the daemon so it loads the same config.
	if cfgPath := os.Getenv("PULSO_CONFIG"); cfgPath != "" {
		cmd.Env = append(os.Environ(), "PULSO_CONFIG="+cfgPath)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Release the process handle immediately — the daemon outlives the caller.
	_ = cmd.Process.Release()

	log.Debug().Int("pid", cmd.Process.Pid).Msg("daemon process started")

	// Wait for the socket to become available.
	client := NewClient(socketPath)
	defer client.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if client.IsAlive() {
			log.Debug().Msg("daemon is ready")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for daemon to become ready")
}

// RunDaemon runs the daemon in the current process (blocks).
func RunDaemon(socketPath string, dataDir string, cfg *types.PulsoConfig, configPath string) error {
	// Create data directory structure.
	if err := os.MkdirAll(filepath.Join(dataDir, "apps"), 0o755); err != nil {
		return fmt.Errorf("failed to create apps directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "state"), 0o755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "tmp"), 0o755); err != nil {
		return fmt.Errorf("failed to create tmp directory: %w", err)
	}

	probe := NewClient(socketPath)
	alive := probe.IsAlive()
	probe.Close()
	if alive {
		return fmt.Errorf("another daemon is already serving this socket")
	}

	lock, err := lockDaemon(filepath.Join(dataDir, "tmp", "daemon.lock"))
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	// Create metrics collector — always running so all processes are tracked.
	metricsInterval := cfg.Metrics.MetricsInterval()
	col := metrics.NewCollector()
	col.Start(metricsInterval)
	defer col.Stop()

	// Create supervisor with options from config.
	sup := supervisor.New(supervisor.Options{
		AutoSave:         true,
		LogDir:           dataDir,
		Defaults:         cfg.Defaults,
		MetricsCollector: col,
		MetricsInterval:  metricsInterval,
	})

	defer func() {
		if err := sup.Shutdown(); err != nil {
			log.Error().Err(err).Msg("close supervisor")
		}
	}()
	// Restore before admitting new commands or autostarting config entries.
	if err := sup.Resurrect(); err != nil {
		return fmt.Errorf("restore saved services: %w", err)
	}
	if len(sup.List()) > 0 {
		if err := sup.Save(); err != nil {
			return fmt.Errorf("commit recovered services: %w", err)
		}
	}

	// PID file in tmp directory.
	pidDir := filepath.Join(dataDir, "tmp")

	// Create authenticator from security config.
	authenticator, err := auth.NewAuthenticator(cfg.Security.Auth)
	if err != nil {
		return fmt.Errorf("failed to create authenticator: %w", err)
	}

	// Create server.
	srv := NewServer(sup, socketPath, pidDir, authenticator, cfg, configPath)
	users, err := auth.OpenUserStore(filepath.Join(dataDir, "security", "auth.sqlite"))
	if err != nil {
		col.Stop()
		return fmt.Errorf("open web accounts: %w", err)
	}
	defer func() {
		if err := users.Close(); err != nil {
			log.Error().Err(err).Msg("close web accounts")
		}
	}()
	srv.webUsers = users

	// Registro de auditoria em arquivo próprio: um problema no banco de contas
	// não derruba o registro, e a retenção é independente das contas.
	auditLog, err := audit.Open(filepath.Join(dataDir, "security", "audit.sqlite"), audit.DefaultRetention)
	if err != nil {
		col.Stop()
		return fmt.Errorf("open audit log: %w", err)
	}
	defer func() {
		if err := auditLog.Close(); err != nil {
			log.Error().Err(err).Msg("close audit log")
		}
	}()
	if removed, err := auditLog.Prune(time.Now()); err != nil {
		log.Error().Err(err).Msg("prune audit log")
	} else if removed > 0 {
		log.Info().Int64("entries", removed).Msg("audit entries past retention removed")
	}
	srv.auditLog = auditLog

	// Notificações de saída: funcionam em rede fechada, pois só fazem
	// tráfego de saída. A configuração fica em um arquivo 0600.
	notifyStore, err := notify.OpenStore(filepath.Join(dataDir, "security", "notifications.json"))
	if err != nil {
		col.Stop()
		return fmt.Errorf("open notification channels: %w", err)
	}
	notifier := notify.NewDispatcher(notifyStore)
	defer notifier.Close()
	srv.notifyStore = notifyStore
	srv.notifier = notifier

	log.Info().
		Str("socket", socketPath).
		Str("data_dir", dataDir).
		Msg("starting daemon")

	// Start MCP HTTP server if enabled.
	var mcpCancel context.CancelFunc
	if cfg.MCP.Enabled && cfg.MCP.Transport == "http" {
		mcpSrv := mcp.NewMCPServer(sup, col)
		mcpSrv.SetAuth(authenticator)
		mcpCfg := cfg.MCP
		if mcpCfg.Listen == "" {
			mcpCfg.Listen = "localhost:8090"
		}
		mcpCtx, cancel := context.WithCancel(context.Background())
		mcpCancel = cancel
		go func() {
			if err := mcpSrv.Start(mcpCtx, mcpCfg); err != nil {
				log.Error().Err(err).Msg("MCP HTTP server error")
			}
		}()
		log.Info().Str("addr", mcpCfg.Listen).Msg("MCP HTTP server started alongside daemon")
	}

	// Auto-start processes from config (autostart: true, respecting depends_on/priority).
	startConfigProcesses(sup, cfg)

	// Blocks until shutdown.
	err = srv.Start(context.Background())

	// Clean up MCP and metrics on shutdown.
	if mcpCancel != nil {
		mcpCancel()
	}
	col.Stop()

	return err
}

// startConfigProcesses starts all autostart processes from the config file.
// Called once during daemon startup, after the server is ready.
func startConfigProcesses(sup *supervisor.Supervisor, cfg *types.PulsoConfig) {
	if cfg == nil || len(cfg.Processes) == 0 {
		return
	}

	existing := make(map[string]bool)
	for _, proc := range sup.List() {
		existing[proc.Name] = true
	}
	var toStart []types.ProcessConfig
	for _, p := range cfg.Processes {
		if p.Autostart {
			if existing[p.Name] {
				continue
			}
			toStart = append(toStart, p)
		}
	}

	if len(toStart) == 0 {
		return
	}

	// Sort by priority then topological order (same as handleStartAll).
	SortStartOrder(toStart)
	toStart = ExpandProcessInstances(toStart)

	for _, p := range toStart {
		if _, err := sup.AddProcess(context.Background(), p); err != nil {
			log.Error().Err(err).Str("process", p.Name).Msg("failed to auto-start process")
		} else {
			log.Info().Str("process", p.Name).Msg("auto-started process")
		}
	}
}
