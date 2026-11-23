package main

import (
	"context"
	"log/slog"

	"github.com/kelseyhightower/envconfig"

	"github.com/Elmanuel1/agentd-worker/internal/agentd"
	"github.com/Elmanuel1/agentd-worker/internal/reap"
	"github.com/Elmanuel1/agentd-worker/internal/telemetry"
)

// ReaperSettings are the reaper's when it runs alone.
type ReaperSettings struct {
	AgentdSettings
	ReapSettings
}

func runReap(ctx context.Context, log *slog.Logger) error {
	var s ReaperSettings
	if err := envconfig.Process("", &s); err != nil {
		return err
	}
	meter, shutdown, err := telemetry.Setup(ctx, "agentd-worker-reap")
	if err != nil {
		return err
	}
	defer shutdown(context.WithoutCancel(ctx))
	client := agentd.New(s.BaseURL, s.EnvironmentID, s.Key.Reveal(), requestTimeout)
	reaper, err := reap.New(s.ReapSettings.config(), client, client.Events(), meter, log)
	if err != nil {
		return err
	}
	log.Info("reaper started", "environment_id", s.EnvironmentID)
	return reaper.Run(ctx)
}
