package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"pulso.local/pulso/internal/daemon"
	"pulso.local/pulso/pkg/types"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"pulso.local/pulso/internal/containerupdate"
)

func newContainerUpdaterCmd() *cobra.Command {
	dir := os.Getenv("PULSO_UPDATE_DIR")
	if dir == "" {
		dir = containerupdate.DefaultDirectory
	}
	socket := os.Getenv("PULSO_DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	var job string
	cmd := &cobra.Command{Use: "container-updater", Short: "Companion service for Docker image updates"}
	cmd.PersistentFlags().StringVar(&dir, "directory", dir, "shared update socket and state directory")
	cmd.PersistentFlags().StringVar(&socket, "docker-socket", socket, "local Docker Engine Unix socket")
	cmd.AddCommand(&cobra.Command{Use: "serve", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		agent, err := containerupdate.NewAgent(ctx, containerupdate.Config{Directory: dir, Socket: socket, Repository: os.Getenv("PULSO_UPDATE_IMAGE")})
		if err != nil {
			return err
		}
		return agent.Serve(ctx)
	}})
	worker := &cobra.Command{Use: "worker", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Minute)
		defer cancel()
		return containerupdate.Worker(ctx, containerupdate.NewEngine(socket), dir, job)
	}}
	worker.Flags().StringVar(&job, "job", "", "accepted update job ID")
	_ = worker.MarkFlagRequired("job")
	cmd.AddCommand(worker)
	cmd.AddCommand(&cobra.Command{Use: "health", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
		defer cancel()
		return containerupdate.NewClient(dir).Health(ctx)
	}})
	cmd.AddCommand(&cobra.Command{Use: "probe", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		response, err := daemonClient().Send(cmd.Context(), daemon.Request{Action: daemon.ActionList})
		if err != nil {
			return err
		}
		if !response.Success {
			return fmt.Errorf("consulta IPC recusada")
		}
		var procs []types.ProcessInfo
		if err = json.Unmarshal(response.Data, &procs); err != nil {
			return err
		}
		result := make([]containerupdate.Application, 0, len(procs))
		for _, proc := range procs {
			result = append(result, containerupdate.Application{Name: proc.Name, State: string(proc.State), Ready: proc.Ready, KeepStopped: proc.State == types.StateStopped && proc.LastEvent != "restart_skipped" && proc.LastEvent != "exited", RestartPolicy: string(proc.Config.RestartPolicy)})
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}})
	return cmd
}
