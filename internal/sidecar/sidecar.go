// Package sidecar runs beside a Docker or Fargate sandbox's worker. It records the worker's run
// when Anthropic's script asks, as Anthropic's fetch container does in a pod, and when the sandbox
// stops it stops the work item with the work token, so a worker that ends without ant's own stop
// leaves no active item.
package sidecar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// stopTimeout bounds the stop, which runs after a SIGTERM has cancelled ctx.
const stopTimeout = 10 * time.Second

// Items stops the session's own work item.
type Items interface {
	Stop(ctx context.Context, session ids.SessionID) error
}

// Connect builds Items for the work item's agentd and token.
type Connect func(baseURL string, env ids.EnvironmentID, token ids.WorkToken) Items

// statusError is an agentd answer that carries its HTTP status.
type statusError interface {
	error
	HTTPStatus() int
}

// Config holds the sidecar's settings.
type Config struct {
	// Dir is the shared directory, /run/cma, with the item in work-item/.
	Dir   string
	Every time.Duration
}

// Run records the worker's run on request until ctx ends, when the backend stops the sandbox
// after its worker exits, then stops the item.
func Run(ctx context.Context, cfg Config, connect Connect) error {
	for {
		if err := record(cfg.Dir); err != nil {
			return err
		}
		t := time.NewTimer(cfg.Every)
		select {
		case <-ctx.Done():
			t.Stop()
			return stop(context.WithoutCancel(ctx), cfg, connect)
		case <-t.C:
		}
	}
}

// record writes work-item/worker-ran once the worker asks with worker-starting. Anthropic's script
// runs no session code before it sees the record, in a directory the worker cannot write.
func record(dir string) error {
	ran := filepath.Join(dir, "work-item", "worker-ran")
	if !exists(filepath.Join(dir, "worker-starting")) || exists(ran) {
		return nil
	}
	return os.WriteFile(ran, nil, 0o644)
}

func stop(ctx context.Context, cfg Config, connect Connect) error {
	dir := filepath.Join(cfg.Dir, "work-item")
	secret, err := os.ReadFile(filepath.Join(dir, "secret"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	session, err := ids.ParseSessionID(read(dir, "session_id"))
	if err != nil {
		return err
	}
	token, err := ids.NewSecret(string(secret)).WorkToken()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	items := connect(read(dir, "base_url"), ids.EnvironmentID(read(dir, "environment_id")), token)
	err = items.Stop(ctx, session)
	if se, ok := errors.AsType[statusError](err); ok && (se.HTTPStatus() == http.StatusUnauthorized || se.HTTPStatus() == http.StatusConflict) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stopping item %s: %w", session, err)
	}
	return nil
}

func read(dir, name string) string {
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return strings.TrimSpace(string(b))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
