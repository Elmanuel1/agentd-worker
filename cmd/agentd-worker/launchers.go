package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/launcher"
	"github.com/Elmanuel1/agentd-worker/internal/launcher/fargate"
)

// Anthropic's sandbox image runs the worker as uid 1001 in group 1000.
const workerUID, workerGID = 1001, 1000

// backend is a Launcher and its Locator, closed on shutdown.
type backend interface {
	dispatch.Launcher
	dispatch.Locator
	Close()
}

// backendKind is one backend the controller runs sandboxes on: the handoff wait it defaults to, and
// how to open it, which reads the backend's own settings.
type backendKind struct {
	handoffWait time.Duration
	open        func(ctx context.Context, sc launcher.Sandbox, log *slog.Logger) (backend, error)
}

// launcherNamed is the backend AGENTD_WORKER_LAUNCHER names.
func launcherNamed(name string) (backendKind, error) {
	switch name {
	case "docker":
		return backendKind{handoffWait: 120 * time.Second, open: openDocker}, nil
	case "fargate":
		return backendKind{handoffWait: fargate.MaxHandoffWait, open: openFargate}, nil
	default:
		return backendKind{}, fmt.Errorf("AGENTD_WORKER_LAUNCHER %q: want docker or fargate", name)
	}
}
