package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/config"
	"pulso.local/pulso/internal/mcp"
	"pulso.local/pulso/internal/metrics"
	"pulso.local/pulso/internal/supervisor"
	"pulso.local/pulso/pkg/types"
)

func newMCPCmd() *cobra.Command {
	var (
		transport string
		listen    string
	)

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP (Model Context Protocol) server",
		Long: `Start the Pulso MCP server for AI agent integrations.

Supports stdio transport (default) for CLI/agent use and HTTP transport
for remote integrations. The MCP server exposes Pulso process management
operations as tools and structured data as resources.

Examples:
  pulso mcp                        # stdio transport (for AI agents)
  pulso mcp --transport http       # HTTP transport on :8090
  pulso mcp --transport http --listen :3000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPServer(transport, listen)
		},
	}

	cmd.Flags().StringVar(&transport, "transport", "", "Transport type: stdio (default) or http")
	cmd.Flags().StringVar(&listen, "listen", "", "Listen address for HTTP transport (default: localhost:8090)")

	return cmd
}

func runMCPServer(transportOverride, listenOverride string) error {
	// Load config.
	cfg, err := config.Load("")
	if err != nil {
		cfg = &types.PulsoConfig{}
		config.ApplyDefaults(cfg)
	}

	// Apply CLI overrides.
	mcpCfg := cfg.MCP
	if transportOverride != "" {
		mcpCfg.Transport = transportOverride
	}
	if listenOverride != "" {
		mcpCfg.Listen = listenOverride
	}
	if mcpCfg.Transport == "" {
		mcpCfg.Transport = "stdio"
	}

	// Create log directory.
	dd := dataDir()
	if err := os.MkdirAll(dd, 0o755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create metrics collector.
	col := metrics.NewCollector()
	col.Start(5e9) // 5 second interval
	defer col.Stop()

	// Create supervisor.
	sup := supervisor.New(supervisor.Options{
		LogDir:           dd,
		Defaults:         cfg.Defaults,
		MetricsCollector: col,
		MetricsInterval:  5 * time.Second,
	})

	// Create MCP server.
	srv := mcp.NewMCPServer(sup, col)

	// Set up context with signal handling.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Info().Str("signal", sig.String()).Msg("received shutdown signal")
		cancel()
	}()

	log.Info().
		Str("transport", mcpCfg.Transport).
		Msg("starting MCP server")

	if err := srv.Start(ctx, mcpCfg); err != nil {
		return fmt.Errorf("MCP server error: %w", err)
	}

	_ = sup.Shutdown()
	return nil
}
