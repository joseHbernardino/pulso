package main

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/daemon"
)

func newResurrectCmd() *cobra.Command {
	return &cobra.Command{Use: "resurrect", Short: "Restore previously saved processes", RunE: func(cmd *cobra.Command, args []string) error {
		client, err := ensureDaemon()
		if err != nil {
			return err
		}
		defer client.Close()
		resp, err := client.Send(context.Background(), daemon.Request{Action: daemon.ActionResurrect})
		if err != nil {
			return fmt.Errorf("restore services: %w", err)
		}
		if !resp.Success {
			return fmt.Errorf("restore services: %s", resp.Error)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "[Pulso] Saved services restored")
		return err
	}}
}
