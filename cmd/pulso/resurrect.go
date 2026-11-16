package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/daemon"
)

func newResurrectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resurrect",
		Short: "Restore previously saved processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Try daemon IPC.
			if daemonIsRunning() {
				resp, err := sendIPC(daemon.ActionResurrect, nil)
				if err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "[Pulso] Daemon IPC failed, using direct mode: %v\n", err)
				} else if resp.Success {
					_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Processes resurrected")
					return nil
				}
			}

			// Direct mode.
			sup, err := getSupervisor()
			if err != nil {
				return err
			}

			if err := sup.Resurrect(); err != nil {
				return fmt.Errorf("failed to resurrect processes: %w", err)
			}

			procs := sup.List()
			_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Resurrected %d process(es)\n", len(procs))
			return nil
		},
	}

	return cmd
}
