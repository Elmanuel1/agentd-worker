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

	"github.com/Elmanuel1/agentd-worker/internal/fetch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

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
