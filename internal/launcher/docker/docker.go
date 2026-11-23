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
	// StateDir holds the resolv.conf sandboxes under runsc mount; it is a path on the engine's host.
	StateDir string
}

// Launcher runs sandboxes on one engine. Each sandbox has a watcher that starts its worker after
// fetch and stops its sidecar after the worker; Close ends the watchers and waits for them, and
// the next New adopts every sandbox it finds.
type Launcher struct {
	cli *client.Client
	cfg Config
	log *slog.Logger
	// watch is the watchers' context, cancelled by Close.
	watch  context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New checks the engine has cfg.Runtime and adopts the sandboxes of earlier launches.
func New(ctx context.Context, cli *client.Client, cfg Config, log *slog.Logger) (*Launcher, error) {
	info, err := cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading the engine's runtimes: %w", err)
	}
	if _, ok := info.Info.Runtimes[cfg.Runtime]; !ok {
		return nil, fmt.Errorf("%w %q; it has %v", ErrNoRuntime, cfg.Runtime, slices.Sorted(maps.Keys(info.Info.Runtimes)))
	}
	watch, cancel := context.WithCancel(context.WithoutCancel(ctx))
	l := &Launcher{cli: cli, cfg: cfg, log: log, watch: watch, cancel: cancel}
	if err := l.startResolver(ctx); err != nil {
		cancel()
		return nil, err
	}
	containers, err := l.containers(ctx, "")
	if err != nil {
		cancel()
		return nil, err
	}
	byRole := map[ids.SessionID]map[string]string{}
	created, unfetched := map[ids.SessionID]bool{}, map[ids.SessionID]bool{}
	for _, c := range containers {
		s := ids.SessionID(c.Labels[labelSession])
		if byRole[s] == nil {
			byRole[s] = map[string]string{}
		}
		role := c.Labels[labelRole]
		byRole[s][role] = c.ID
		created[s] = created[s] || (role == roleWorker && c.State == container.StateCreated)
		unfetched[s] = unfetched[s] || (role == roleFetch && c.State == container.StateCreated)
	}
	// A sandbox whose fetch never started lost its nonce with the earlier launch, so it is
	// removed. A worker not started yet waits on its fetch, as at launch; any other is watched
	// until it exits, at once when it already has.
	for s, roles := range byRole {
		if unfetched[s] {
			l.wg.Go(func() { l.remove(watch, s, roles) })
			continue
		}
		if created[s] {
			l.wg.Go(func() { l.startWorker(watch, s, roles) })
			continue
		}
		l.wg.Go(func() { l.watchWorker(watch, s, roles) })
	}
	return l, nil
}

func (l *Launcher) Close() {
	l.cancel()
	l.wg.Wait()
}

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
	l.wg.Go(func() { l.startWorker(l.watch, session, created) })
	return nil
}
