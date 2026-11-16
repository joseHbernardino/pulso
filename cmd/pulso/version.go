package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/version"
)

func newVersionCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print Pulso version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pulso %s\n", version.Version)
			if verbose {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Build:    %s\n", version.BuildTime)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Go:       %s\n", runtime.Version())
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  OS/Arch:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&verbose, "verbose", false, "show build details")

	return cmd
}
