package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/kelseyhightower/envconfig"

	"github.com/Elmanuel1/agentd-worker/internal/agentd"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/sidecar"
)

// SidecarSettings are a sidecar container's.
type SidecarSettings struct {
	Dir string `envconfig:"CMA_SHARED_DIR" default:"/run/cma"`
}

func runSidecar(ctx context.Context, log *slog.Logger) error {
	var s SidecarSettings
	if err := envconfig.Process("", &s); err != nil {
		return err
	}
	cfg := sidecar.Config{Dir: s.Dir, Every: time.Second}
	connect := func(baseURL string, env ids.EnvironmentID, token ids.WorkToken) sidecar.Items {
		return agentd.ForItem(baseURL, env, token, requestTimeout)
	}
	if err := sidecar.Run(ctx, cfg, connect); err != nil {
		return err
	}
	log.Info("work item stopped after the worker exited")
	return nil
}
