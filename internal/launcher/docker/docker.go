// Package docker is the Launcher adapter for a Docker engine: each session's sandbox is a fetch,
// a worker and a sidecar container on one network, its /workspace on a volume of its own.
package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// ErrNoRuntime means the engine lacks the runtime sandboxes must run under.
var ErrNoRuntime = errors.New("the Docker engine lacks the sandbox runtime")

// Config holds the adapter's settings.
type Config struct {
	// Network is the Docker network sandboxes join; agentd and the controller are reachable on it.
	Network string
	// Runtime is the OCI runtime sandboxes run under, runsc by default.
	Runtime string
	// SandboxImage runs the worker; ToolImage, agentd-worker's own image, runs fetch and the sidecar.
	SandboxImage, ToolImage string
	// HandoffURL is the controller's GET /work-item base as sandboxes reach it, HandoffCA its CA in PEM.
	HandoffURL, HandoffCA string
	HandoffWait           time.Duration
}

// Launcher runs sandboxes on one engine. Close waits for the launches it started.
type Launcher struct {
	cli  *client.Client
	cfg  Config
	log  *slog.Logger
	life context.Context
	wg   sync.WaitGroup
}

// New checks the engine has cfg.Runtime. life bounds the goroutines that start each worker after
// its fetch container exits 0.
func New(life context.Context, cli *client.Client, cfg Config, log *slog.Logger) (*Launcher, error) {
	info, err := cli.Info(life, client.InfoOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading the engine's runtimes: %w", err)
	}
	if _, ok := info.Info.Runtimes[cfg.Runtime]; !ok {
		return nil, fmt.Errorf("%w %q; it has %v", ErrNoRuntime, cfg.Runtime, slices.Sorted(maps.Keys(info.Info.Runtimes)))
	}
	return &Launcher{cli: cli, cfg: cfg, log: log, life: life}, nil
}

// Close waits for the worker starts under way.
func (l *Launcher) Close() { l.wg.Wait() }

// Launch replaces whatever containers the session had with a fresh sandbox on its kept volume.
func (l *Launcher) Launch(ctx context.Context, session ids.SessionID, nonce ids.Nonce) error {
	if err := l.Stop(ctx, session); err != nil {
		return err
	}
	for _, v := range []struct{ name, role string }{{workspaceVolume(session), roleWorkspace}, {runVolume(session), roleRun}} {
		if _, err := l.cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: v.name, Labels: labels(session, v.role)}); err != nil {
			return fmt.Errorf("creating volume %s: %w", v.name, err)
		}
	}
	created := map[string]string{}
	for _, spec := range l.specs(session, nonce) {
		res, err := l.cli.ContainerCreate(ctx, spec)
		if err != nil {
			return errors.Join(fmt.Errorf("creating %s: %w", spec.Name, err), l.Stop(context.WithoutCancel(ctx), session))
		}
		created[spec.Config.Labels[labelRole]] = res.ID
	}
	for _, role := range []string{roleSidecar, roleFetch} {
		if _, err := l.cli.ContainerStart(ctx, created[role], client.ContainerStartOptions{}); err != nil {
			return errors.Join(fmt.Errorf("starting %s: %w", role, err), l.Stop(context.WithoutCancel(ctx), session))
		}
	}
	l.wg.Go(func() { l.startWorker(session, created) })
	return nil
}

// startWorker starts the worker once fetch exits 0. When the worker exits it stops the sidecar,
// which stops the item, then removes the containers; a fetch that fails removes them at once.
func (l *Launcher) startWorker(session ids.SessionID, byRole map[string]string) {
	if code, err := l.wait(byRole[roleFetch]); err != nil || code != 0 {
		l.log.Info("fetch exited without the work item; removing the sandbox", "session_id", session, "exit_code", code, "error", err)
		l.remove(session, byRole)
		return
	}
	if _, err := l.cli.ContainerStart(l.life, byRole[roleWorker], client.ContainerStartOptions{}); err != nil {
		l.log.Warn("starting the worker failed", "session_id", session, "error", err)
		l.remove(session, byRole)
		return
	}
	code, err := l.wait(byRole[roleWorker])
	if err != nil {
		return
	}
	l.log.Info("worker exited", "session_id", session, "exit_code", code)
	timeout := sidecarGrace
	if _, err := l.cli.ContainerStop(l.life, byRole[roleSidecar], client.ContainerStopOptions{Timeout: &timeout}); err != nil && !cerrdefs.IsNotFound(err) {
		l.log.Warn("stopping the sidecar failed", "session_id", session, "error", err)
	}
	l.remove(session, byRole)
}

// sidecarGrace is the seconds the sidecar gets to stop the item.
const sidecarGrace = 15

func (l *Launcher) wait(id string) (int64, error) {
	wait := l.cli.ContainerWait(l.life, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case res := <-wait.Result:
		return res.StatusCode, nil
	case err := <-wait.Error:
		return -1, err
	}
}

// remove removes this launch's containers only: a later Launch may already have replaced them.
// The work-item volume stays until the session's next Launch or Stop.
func (l *Launcher) remove(session ids.SessionID, byRole map[string]string) {
	for _, id := range byRole {
		_, err := l.cli.ContainerRemove(l.life, id, client.ContainerRemoveOptions{Force: true})
		if err != nil && !cerrdefs.IsNotFound(err) {
			l.log.Warn("removing a sandbox container failed", "session_id", session, "error", err)
		}
	}
}
