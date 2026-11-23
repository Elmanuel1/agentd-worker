package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"time"

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

// backend is a Launcher and its Locator, closed on shutdown.
type backend interface {
	dispatch.Launcher
	dispatch.Locator
	Close()
}

func runScale(ctx context.Context, log *slog.Logger) error {
	var e env
	baseURL := e.required("ANTHROPIC_BASE_URL")
	envID := ids.EnvironmentID(e.required("ANTHROPIC_ENVIRONMENT_ID"))
	key := ids.NewSecret(e.required("ANTHROPIC_ENVIRONMENT_KEY"))
	launcher := e.str("AGENTD_WORKER_LAUNCHER", "docker")
	cfg := dispatchConfig(&e, launcher)
	reapCfg := reapConfig(&e)
	addr := e.str("AGENTD_HANDOFF_ADDR", ":8443")
	cert, certErr := tls.X509KeyPair([]byte(e.required("AGENTD_HANDOFF_CERT")), []byte(e.required("AGENTD_HANDOFF_KEY")))
	sandboxBaseURL := e.str("AGENTD_SANDBOX_BASE_URL", baseURL)
	sandboxes := sandboxConfig{
		handoffURL: e.required("AGENTD_HANDOFF_URL"), handoffCA: e.required("AGENTD_HANDOFF_CA"), handoffWait: cfg.HandoffWait,
		sandboxImage: e.required("AGENTD_SANDBOX_IMAGE"), toolImage: e.required("AGENTD_TOOL_IMAGE"),
	}
	if err := e.err(); err != nil {
		return err
	}
	if certErr != nil {
		return fmt.Errorf("AGENTD_HANDOFF_CERT and AGENTD_HANDOFF_KEY: %w", certErr)
	}

	meter, shutdown, err := telemetry.Setup(ctx, "agentd-worker-scale")
	if err != nil {
		return err
	}
	defer shutdown(context.WithoutCancel(ctx))
	be, err := newBackend(ctx, launcher, sandboxes, log)
	if err != nil {
		return err
	}
	defer be.Close()

	host, err := os.Hostname()
	if err != nil {
		return err
	}
	client := agentd.New(baseURL, envID, key.Reveal(), requestTimeout).WithWorker(host)
	store := handoff.NewStore(be)
	ctrl, err := dispatch.New(cfg, client, client, be, be, store, meter, log)
	if err != nil {
		return err
	}
	reaper, err := reap.New(reapCfg, client, client.Events(), meter, log)
	if err != nil {
		return err
	}
	server := handoff.NewServer(store, envID, sandboxBaseURL, log)
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return handoff.Serve(ctx, addr, cert, server.Handler()) })
	g.Go(func() error { return ctrl.Run(ctx) })
	g.Go(func() error { return ctrl.Release(ctx) })
	g.Go(func() error { return reaper.Run(ctx) })
	log.Info("controller started", "environment_id", envID, "launcher", launcher)
	return g.Wait()
}

// dispatchConfig reads the controller's settings. The handoff wait defaults to 120 s, and on
// Fargate to the most its start timeout leaves room for.
func dispatchConfig(e *env, launcher string) dispatch.Config {
	wait := 120
	if launcher == "fargate" {
		wait = int(fargateMaxWait / time.Second)
	}
	return dispatch.Config{
		HandoffWait:     e.seconds("AGENTD_HANDOFF_WAIT_SECONDS", wait),
		MaxSandboxes:    e.number("AGENTD_MAX_SANDBOXES", 100, 1),
		ReleaseInterval: e.seconds("AGENTD_RELEASE_INTERVAL_SECONDS", 600),
		Retention:       time.Duration(e.number("AGENTD_RETENTION_DAYS", 0, 0)) * 24 * time.Hour,
	}
}

func reapConfig(e *env) reap.Config {
	return reap.Config{
		Interval:   e.seconds("AGENTD_REAP_INTERVAL_SECONDS", 60),
		StaleAfter: e.seconds("AGENTD_STALE_AFTER_SECONDS", 360),
	}
}
