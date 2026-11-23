package main

import (
	"context"
	"log/slog"

	"github.com/kelseyhightower/envconfig"

	"github.com/Elmanuel1/agentd-worker/internal/resolver"
)

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
