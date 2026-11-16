package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/config"
	"pulso.local/pulso/internal/daemon"
	"pulso.local/pulso/pkg/types"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon <subcommand>",
		Short: "Manage the Pulso daemon",
	}

	// daemon start
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the Pulso daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if daemonIsRunning() {
				_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Daemon is already running")
				return nil
			}
			if cfgFile != "" {
				_ = os.Setenv("PULSO_CONFIG", cfgFile)
			}
			if err := daemon.StartDaemon(); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Failed to start daemon: %v\n", err)
				return err
			}
			_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Daemon started")
			return nil
		},
	})

	// daemon stop
	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the Pulso daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := daemonClient()
			if !client.IsAlive() {
				_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Daemon is not running")
				return nil
			}

			pidFile := daemon.NewPIDFile(daemon.DefaultDataDir())
			pid, _ := pidFile.Read()
			if pid > 0 {
				proc, err := os.FindProcess(pid)
				if err == nil {
					if err := proc.Signal(syscall.SIGTERM); err != nil {
						_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Failed to stop daemon: %v\n", err)
						return err
					}
				}
			}

			// Wait for daemon to stop.
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if !client.IsAlive() {
					_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Daemon stopped")
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Timed out waiting for daemon to stop\n")
			return fmt.Errorf("timed out waiting for daemon to stop")
		},
	})

	// daemon status
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Check daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := daemonClient()
			if client.IsAlive() {
				pidFile := daemon.NewPIDFile(daemon.DefaultDataDir())
				pid, _ := pidFile.Read()
				socket := daemon.DefaultSocketPath()
				_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Daemon is running (pid: %d, socket: %s)\n", pid, socket)
			} else {
				_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Daemon is not running")
			}
			return nil
		},
	})

	// daemon restart
	cmd.AddCommand(&cobra.Command{
		Use:   "restart",
		Short: "Restart the Pulso daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := daemonClient()
			if client.IsAlive() {
				pidFile := daemon.NewPIDFile(daemon.DefaultDataDir())
				pid, _ := pidFile.Read()
				if pid > 0 {
					proc, err := os.FindProcess(pid)
					if err == nil {
						_ = proc.Signal(syscall.SIGTERM)
					}
				}
				// Wait for stop.
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					if !client.IsAlive() {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
			}

			if cfgFile != "" {
				_ = os.Setenv("PULSO_CONFIG", cfgFile)
			}
			if err := daemon.StartDaemon(); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Failed to start daemon: %v\n", err)
				return err
			}
			_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Daemon restarted")
			return nil
		},
	})

	// daemon reload
	cmd.AddCommand(&cobra.Command{
		Use:   "reload",
		Short: "Reload daemon config without stopping processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !daemonIsRunning() {
				_, _ = fmt.Fprintln(os.Stderr, "[Pulso] Daemon is not running")
				return fmt.Errorf("daemon not running")
			}

			// Resolve config path to pass to daemon.
			configPath := cfgFile
			if configPath == "" {
				if env := os.Getenv("PULSO_CONFIG"); env != "" {
					configPath = env
				}
			}
			resp, err := sendIPC(daemon.ActionConfigReload, daemon.ConfigReloadPayload{
				ConfigPath: configPath,
			})
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Failed to reload config: %v\n", err)
				return err
			}
			if !resp.Success {
				_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Failed to reload config: %s\n", resp.Error)
				return fmt.Errorf("config reload failed: %s", resp.Error)
			}
			_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Config reloaded")
			return nil
		},
	})

	// daemon run (internal - runs daemon in foreground, used when forking)
	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground (internal)",
		RunE: func(cmd *cobra.Command, args []string) error {
			socketPath := daemon.DefaultSocketPath()
			dataDir := daemon.DefaultDataDir()

			// Load config if available (PULSO_CONFIG env from CLI, or search CWD).
			var cfg *types.PulsoConfig
			configPath := os.Getenv("PULSO_CONFIG")
			cfg, err := config.Load(configPath)
			if err != nil {
				cfg = &types.PulsoConfig{}
				config.ApplyDefaults(cfg)
			}

			return daemon.RunDaemon(socketPath, dataDir, cfg, configPath)
		},
	})

	_ = config.Load
	return cmd
}
