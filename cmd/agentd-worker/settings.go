package main

import (
	"strconv"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/reap"
)

// seconds is a setting given in whole seconds.
type seconds time.Duration

func (s *seconds) UnmarshalText(text []byte) error {
	n, err := strconv.Atoi(string(text))
	*s = seconds(time.Duration(n) * time.Second)
	return err
}

// AgentdSettings are how the controller and the reaper reach agentd's work queue.
type AgentdSettings struct {
	BaseURL       string            `envconfig:"ANTHROPIC_BASE_URL" required:"true"`
	EnvironmentID ids.EnvironmentID `envconfig:"ANTHROPIC_ENVIRONMENT_ID" required:"true"`
	Key           ids.Secret        `envconfig:"ANTHROPIC_ENVIRONMENT_KEY" required:"true"`
}

// ReapSettings are the reaper's, in the controller or alone.
type ReapSettings struct {
	Interval   seconds `envconfig:"AGENTD_REAP_INTERVAL_SECONDS" default:"60"`
	StaleAfter seconds `envconfig:"AGENTD_STALE_AFTER_SECONDS" default:"360"`
}

func (s ReapSettings) config() reap.Config {
	return reap.Config{Interval: time.Duration(s.Interval), StaleAfter: time.Duration(s.StaleAfter)}
}
