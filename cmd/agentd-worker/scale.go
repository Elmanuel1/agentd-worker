package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	"golang.org/x/sync/errgroup"

	"github.com/Elmanuel1/agentd-worker/internal/agentd"
	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/dispatch/handoff"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/reap"
	"github.com/Elmanuel1/agentd-worker/internal/telemetry"
)

// requestTimeout bounds one agentd request; a poll blocks at most 999 ms of it.
const requestTimeout = 30 * time.Second

// ScaleSettings are the controller's. HandoffWait left unset takes the backend's default.
type ScaleSettings struct {
	AgentdSettings
	ReapSettings
	Launcher        string     `envconfig:"AGENTD_WORKER_LAUNCHER" default:"docker"`
	HandoffWait     seconds    `envconfig:"AGENTD_HANDOFF_WAIT_SECONDS"`
	MaxSandboxes    int        `envconfig:"AGENTD_MAX_SANDBOXES" default:"100"`
	ReleaseInterval seconds    `envconfig:"AGENTD_RELEASE_INTERVAL_SECONDS" default:"600"`
	RetentionDays   uint       `envconfig:"AGENTD_RETENTION_DAYS" default:"0"`
	HandoffAddr     string     `envconfig:"AGENTD_HANDOFF_ADDR" default:":8443"`
	HandoffCert     string     `envconfig:"AGENTD_HANDOFF_CERT" required:"true"`
	HandoffKey      ids.Secret `envconfig:"AGENTD_HANDOFF_KEY" required:"true"`
	HandoffURL      string     `envconfig:"AGENTD_HANDOFF_URL" required:"true"`
	HandoffCA       string     `envconfig:"AGENTD_HANDOFF_CA" required:"true"`
	SandboxBaseURL  string     `envconfig:"AGENTD_SANDBOX_BASE_URL"`
	SandboxImage    string     `envconfig:"AGENTD_SANDBOX_IMAGE" required:"true"`
	ToolImage       string     `envconfig:"AGENTD_TOOL_IMAGE" required:"true"`
}

func runScale(ctx context.Context, log *slog.Logger) error {
	var s ScaleSettings
	if err := envconfig.Process("", &s); err != nil {
		return err
	}
	l, err := launcherNamed(s.Launcher)
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair([]byte(s.HandoffCert), []byte(s.HandoffKey.Reveal()))
	if err != nil {
		return fmt.Errorf("AGENTD_HANDOFF_CERT and AGENTD_HANDOFF_KEY: %w", err)
	}
	cfg := dispatchConfig(s, l)
	sandboxes := sandboxConfig{
		handoffURL: s.HandoffURL, handoffCA: s.HandoffCA, handoffWait: cfg.HandoffWait,
		sandboxImage: s.SandboxImage, toolImage: s.ToolImage,
	}
	sandboxBaseURL := cmp.Or(s.SandboxBaseURL, s.BaseURL)

	meter, shutdown, err := telemetry.Setup(ctx, "agentd-worker-scale")
	if err != nil {
		return err
	}
	defer shutdown(context.WithoutCancel(ctx))
	be, err := l.open(ctx, sandboxes, log)
	if err != nil {
		return err
	}
	defer be.Close()

	host, err := os.Hostname()
	if err != nil {
		return err
	}
	client := agentd.New(s.BaseURL, s.EnvironmentID, s.Key.Reveal(), requestTimeout).WithWorker(host)
	store := handoff.NewStore()
	ctrl, err := dispatch.New(cfg, client, client, be, be, store, meter, log)
	if err != nil {
		return err
	}
	reaper, err := reap.New(s.ReapSettings.config(), client, client.Events(), meter, log)
	if err != nil {
		return err
	}
	server := handoff.NewServer(store, be, s.EnvironmentID, sandboxBaseURL, log)
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return handoff.Serve(ctx, s.HandoffAddr, cert, server.Handler()) })
	g.Go(func() error { return ctrl.Run(ctx) })
	g.Go(func() error { return ctrl.Release(ctx) })
	g.Go(func() error { return reaper.Run(ctx) })
	log.Info("controller started", "environment_id", s.EnvironmentID, "launcher", s.Launcher)
	return g.Wait()
}

// dispatchConfig gives the backend's handoff wait to a controller that sets none.
func dispatchConfig(s ScaleSettings, l launcher) dispatch.Config {
	return dispatch.Config{
		HandoffWait:     cmp.Or(time.Duration(s.HandoffWait), l.handoffWait),
		MaxSandboxes:    s.MaxSandboxes,
		ReleaseInterval: time.Duration(s.ReleaseInterval),
		Retention:       time.Duration(s.RetentionDays) * 24 * time.Hour,
	}
}
