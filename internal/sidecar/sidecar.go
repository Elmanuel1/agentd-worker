// Package sidecar runs beside a Docker or Fargate sandbox's worker. It records the worker's run
// when Anthropic's script asks, as Anthropic's fetch container does in a pod, and when the sandbox
// stops it stops the work item with the work token, so a worker that ends without ant's own stop
// leaves no active item.
package sidecar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// stopTimeout bounds the stop, which runs after a SIGTERM has cancelled ctx.
const stopTimeout = 10 * time.Second

// Items stops the sandbox's own work item.
type Items interface {
	Stop(ctx context.Context, work ids.WorkID) error
}

// Connect builds Items for the work item's agentd and token.
type Connect func(baseURL string, env ids.EnvironmentID, token ids.WorkToken) Items

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
	work := ids.WorkID(read(dir, "work_id"))
	if work == "" {
		return errors.New("the work item has no work_id")
	}
	token, err := ids.NewSecret(string(secret)).WorkToken()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	items := connect(read(dir, "base_url"), ids.EnvironmentID(read(dir, "environment_id")), token)
	if err := items.Stop(ctx, work); err != nil {
		return fmt.Errorf("stopping item %s: %w", work, err)
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
