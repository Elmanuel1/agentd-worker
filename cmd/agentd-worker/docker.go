package main

import (
	"context"
	"log/slog"

	"github.com/kelseyhightower/envconfig"
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/launcher"
	"github.com/Elmanuel1/agentd-worker/internal/launcher/docker"
)

// DockerSettings are read only when AGENTD_WORKER_LAUNCHER is docker.
type DockerSettings struct {
	Network  string `envconfig:"AGENTD_DOCKER_NETWORK" required:"true"`
	Runtime  string `envconfig:"AGENTD_DOCKER_RUNTIME" default:"runsc"`
	StateDir string `envconfig:"AGENTD_DOCKER_STATE_DIR" default:"/var/lib/agentd-worker"`
}

func openDocker(ctx context.Context, sc launcher.Sandbox, log *slog.Logger) (backend, error) {
	var s DockerSettings
	if err := envconfig.Process("", &s); err != nil {
		return nil, err
	}
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	l, err := docker.New(ctx, cli, docker.Config{
		Network: s.Network, Runtime: s.Runtime, StateDir: s.StateDir, Sandbox: sc,
	}, log)
	if err != nil {
		cli.Close()
		return nil, err
	}
	return l, nil
}
