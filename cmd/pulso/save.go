package main

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/daemon"
)

func newSaveCmd() *cobra.Command {
	return &cobra.Command{Use: "save", Short: "Save current process list for resurrection", RunE: func(cmd *cobra.Command, args []string) error {
		client, err := ensureDaemon()
		if err != nil {
			return err
		}
		defer client.Close()
		resp, err := client.Send(context.Background(), daemon.Request{Action: daemon.ActionSave})
		if err != nil {
			return fmt.Errorf("save recovery state: %w", err)
		}
		if !resp.Success {
			return fmt.Errorf("save recovery state: %s", resp.Error)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "[Pulso] Process state saved")
		return err
	}}
}
