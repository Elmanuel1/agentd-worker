package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/kelseyhightower/envconfig"

	"github.com/Elmanuel1/agentd-worker/internal/agentd"
	"github.com/Elmanuel1/agentd-worker/internal/fetch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/reap"
	"github.com/Elmanuel1/agentd-worker/internal/resolver"
	"github.com/Elmanuel1/agentd-worker/internal/sidecar"
	"github.com/Elmanuel1/agentd-worker/internal/telemetry"
)

// Anthropic's image runs the worker as uid 1001 in group 1000.
const workerUID, workerGID = 1001, 1000

// FetchSettings are a fetch container's, from its launcher.
type FetchSettings struct {
	URL   string        `envconfig:"AGENTD_HANDOFF_URL" required:"true"`
	Nonce ids.Nonce     `envconfig:"AGENTD_HANDOFF_NONCE" required:"true"`
	CA    string        `envconfig:"AGENTD_HANDOFF_CA" required:"true"`
	Wait  time.Duration `envconfig:"AGENTD_HANDOFF_WAIT" required:"true"`
	Dir   string        `envconfig:"CMA_SHARED_DIR" default:"/run/cma"`
}

func runFetch(ctx context.Context, log *slog.Logger) error {
	var s FetchSettings
	if err := envconfig.Process("", &s); err != nil {
		return err
	}
	cfg := fetch.Config{URL: s.URL, Nonce: s.Nonce, Dir: s.Dir, UID: workerUID, GID: workerGID, Every: 2 * time.Second, Wait: s.Wait}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(s.CA)) {
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

// ResolverSettings are the DNS relay's.
type ResolverSettings struct {
	Addr     string `envconfig:"AGENTD_RESOLVER_ADDR" default:":53"`
	Upstream string `envconfig:"AGENTD_RESOLVER_UPSTREAM" default:"127.0.0.11:53"`
}

func runResolver(ctx context.Context, log *slog.Logger) error {
	var s ResolverSettings
	if err := envconfig.Process("", &s); err != nil {
		return err
	}
	log.Info("resolver started", "addr", s.Addr, "upstream", s.Upstream)
	return resolver.Serve(ctx, s.Addr, s.Upstream, log)
}
