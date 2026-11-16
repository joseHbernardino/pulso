package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"pulso.local/pulso/internal/config"
	"pulso.local/pulso/internal/daemon"
	"pulso.local/pulso/pkg/types"
)

func newConfigReloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config reload",
		Short: "Reload configuration and apply changes",
		Long:  `Reload the pulso configuration file and apply changes without restarting healthy processes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Read current config.
			var oldCfg types.PulsoConfig
			if err := viper.Unmarshal(&oldCfg); err != nil {
				return fmt.Errorf("failed to read current config: %w", err)
			}

			// Re-read config file.
			if err := viper.ReadInConfig(); err != nil {
				return fmt.Errorf("failed to read config file: %w", err)
			}

			var newCfg types.PulsoConfig
			if err := viper.Unmarshal(&newCfg); err != nil {
				return fmt.Errorf("failed to parse new config: %w", err)
			}

			// Validate new config.
			if err := newCfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			// Diff and display changes.
			diff := config.DiffConfigs(&oldCfg, &newCfg)

			if diff.String() == "no changes" {
				_, _ = fmt.Fprintln(os.Stdout, "[Pulso] No configuration changes detected")
				return nil
			}

			_, _ = fmt.Fprintf(os.Stdout, "[Pulso] Configuration changes:\n%s", diff.String())

			// Apply changes via daemon.
			if daemonIsRunning() {
				resp, err := sendIPC(daemon.ActionConfigReload, nil)
				if err != nil {
					return fmt.Errorf("failed to send config reload to daemon: %w", err)
				}
				if !resp.Success {
					return fmt.Errorf("daemon config reload failed: %s", resp.Error)
				}
				_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Configuration reloaded via daemon")
				return nil
			}

			_, _ = fmt.Fprintln(os.Stdout, "[Pulso] Config reloaded (daemon not running, changes apply on next start)")
			return nil
		},
	}

	return cmd
}
