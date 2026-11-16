package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/daemon"
	"pulso.local/pulso/internal/watcher"
)

func newWatchCmd() *cobra.Command {
	var (
		paths    []string
		ignore   []string
		debounce string
	)

	cmd := &cobra.Command{
		Use:   "watch <id|name>",
		Short: "Enable file watching for a process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			debounceDur, _ := time.ParseDuration(debounce)

			// Try daemon IPC.
			if daemonIsRunning() {
				resp, err := sendIPC(daemon.ActionReload, daemon.StopPayload{
					Target: target,
				})
				if err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Daemon IPC failed, using direct mode: %v\n", err)
				} else if resp.Success {
					_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Watching process %q (via daemon)\n", target)
					return nil
				}
			}

			// Direct mode: get the process and attach a watcher.
			sup, err := getSupervisor()
			if err != nil {
				return err
			}

			proc, err := sup.Get(target)
			if err != nil {
				return err
			}

			info := proc.Info()
			watchPaths := paths
			if len(watchPaths) == 0 {
				watchPaths = []string{info.Config.Cwd}
			}

			watchIgnore := ignore
			if len(watchIgnore) == 0 {
				watchIgnore = []string{
					".git", "node_modules", "__pycache__", "*.pyc",
					".DS_Store", "vendor", "dist", "build", "bin",
				}
			}

			if debounceDur == 0 {
				debounceDur = 100 * time.Millisecond
			}

			w, err := watcher.New(watchPaths, watchIgnore, debounceDur)
			if err != nil {
				return fmt.Errorf("failed to create watcher: %w", err)
			}

			// Handler restarts the process on file changes.
			handler := func(changedPaths []string) {
				for _, p := range changedPaths {
					_, _ = fmt.Fprintf(os.Stderr, "[Pulso] File changed: %s\n", p)
				}
				ctx := cmd.Context()
				if err := sup.RestartProcess(ctx, info.ID); err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Restart failed: %v\n", err)
				} else {
					_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Process %q restarted\n", info.Name)
				}
			}

			if err := w.Start(handler); err != nil {
				return fmt.Errorf("failed to start watcher: %w", err)
			}

			_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Watching %v for process %q (Ctrl+C to stop)\n", watchPaths, info.Name)

			// Block until interrupted.
			select {}
		},
	}

	cmd.Flags().StringArrayVar(&paths, "paths", nil, "paths to watch")
	cmd.Flags().StringArrayVar(&ignore, "ignore", nil, "patterns to ignore")
	cmd.Flags().StringVar(&debounce, "debounce", "100ms", "debounce duration")

	return cmd
}
