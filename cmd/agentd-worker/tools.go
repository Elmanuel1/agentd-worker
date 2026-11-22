package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/agentd"
	"github.com/Elmanuel1/agentd-worker/internal/fetch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/reap"
	"github.com/Elmanuel1/agentd-worker/internal/sidecar"
	"github.com/Elmanuel1/agentd-worker/internal/telemetry"
)

// Anthropic's image runs the worker as uid 1001 in group 1000.
const workerUID, workerGID = 1001, 1000

func runFetch(ctx context.Context, log *slog.Logger) error {
	var e env
	cfg := fetch.Config{
		URL: e.required("AGENTD_HANDOFF_URL"), Nonce: ids.Nonce(e.required("AGENTD_HANDOFF_NONCE")),
		Dir: e.str("CMA_SHARED_DIR", "/run/cma"), UID: workerUID, GID: workerGID,
		Every: 2 * time.Second, Wait: e.duration("AGENTD_HANDOFF_WAIT", 120*time.Second),
	}
	ca := e.required("AGENTD_HANDOFF_CA")
	if err := e.err(); err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(ca)) {
		return errors.New("AGENTD_HANDOFF_CA holds no certificate")
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
	}
	if err := fetch.Run(ctx, client, cfg); err != nil {
		return err
	}
	log.Info("work item collected")
	return nil
}

func runSidecar(ctx context.Context, log *slog.Logger) error {
	var e env
	cfg := sidecar.Config{Dir: e.str("CMA_SHARED_DIR", "/run/cma"), Every: time.Second}
	connect := func(baseURL string, env ids.EnvironmentID, token ids.WorkToken) sidecar.Items {
		return agentd.New(baseURL, env, token.Reveal(), requestTimeout)
	}
	if err := sidecar.Run(ctx, cfg, connect); err != nil {
		return err
	}
	log.Info("work item stopped after the worker exited")
	return nil
}

func runReap(ctx context.Context, log *slog.Logger) error {
	var e env
	baseURL := e.required("ANTHROPIC_BASE_URL")
	envID := ids.EnvironmentID(e.required("ANTHROPIC_ENVIRONMENT_ID"))
	key := ids.NewSecret(e.required("ANTHROPIC_ENVIRONMENT_KEY"))
	cfg := reapConfig(&e)
	if err := e.err(); err != nil {
		return err
	}
	meter, shutdown, err := telemetry.Setup(ctx, "agentd-worker-reap")
	if err != nil {
		return err
	}
	defer shutdown(context.WithoutCancel(ctx))
	client := agentd.New(baseURL, envID, key.Reveal(), requestTimeout)
	reaper, err := reap.New(cfg, client, client.Events(), meter, log)
	if err != nil {
		return err
	}
	log.Info("reaper started", "environment_id", envID)
	return reaper.Run(ctx)
}
