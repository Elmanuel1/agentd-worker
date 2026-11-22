package docker

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// List returns each session with a container or a workspace volume; it runs while its fetch or worker does.
func (l *Launcher) List(ctx context.Context) ([]dispatch.Sandbox, error) {
	containers, err := l.containers(ctx, "")
	if err != nil {
		return nil, err
	}
	vols, err := l.cli.VolumeList(ctx, client.VolumeListOptions{Filters: make(client.Filters).Add("label", labelRole+"="+roleWorkspace)})
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}
	running := map[ids.SessionID]bool{}
	for _, v := range vols.Items {
		if s, err := ids.ParseSessionID(v.Labels[labelSession]); err == nil && !running[s] {
			running[s] = false
		}
	}
	for _, c := range containers {
		if s, err := ids.ParseSessionID(c.Labels[labelSession]); err == nil {
			running[s] = running[s] || runs(c)
		}
	}
	out := make([]dispatch.Sandbox, 0, len(running))
	for s, r := range running {
		out = append(out, dispatch.Sandbox{Session: s, Running: r})
	}
	return out, nil
}

// Address is the fetch container's address while it runs, else the worker's.
func (l *Launcher) Address(ctx context.Context, session ids.SessionID) (netip.Addr, bool, error) {
	containers, err := l.containers(ctx, session)
	if err != nil {
		return netip.Addr{}, false, err
	}
	for _, role := range []string{roleFetch, roleWorker} {
		for _, c := range containers {
			if c.Labels[labelRole] != role || !runs(c) || c.NetworkSettings == nil {
				continue
			}
			if ep := c.NetworkSettings.Networks[l.cfg.Network]; ep != nil && ep.IPAddress.IsValid() {
				return ep.IPAddress, true, nil
			}
		}
	}
	return netip.Addr{}, false, nil
}

// Stop removes the session's containers and the volume that held its work item, keeping /workspace.
func (l *Launcher) Stop(ctx context.Context, session ids.SessionID) error {
	containers, err := l.containers(ctx, session)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range containers {
		if _, err := l.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("removing container %s: %w", c.ID, err))
		}
	}
	errs = append(errs, l.removeVolume(ctx, runVolume(session)))
	return errors.Join(errs...)
}

// Release stops the session's sandbox and removes its workspace volume.
func (l *Launcher) Release(ctx context.Context, session ids.SessionID) error {
	if err := l.Stop(ctx, session); err != nil {
		return err
	}
	return l.removeVolume(ctx, workspaceVolume(session))
}

func (l *Launcher) removeVolume(ctx context.Context, name string) error {
	_, err := l.cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: true})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("removing volume %s: %w", name, err)
	}
	return nil
}

// containers lists the session's containers, or every sandbox container when session is empty.
func (l *Launcher) containers(ctx context.Context, session ids.SessionID) ([]container.Summary, error) {
	label := labelSession
	if session != "" {
		label += "=" + string(session)
	}
	res, err := l.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("label", label)})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	return res.Items, nil
}

func runs(c container.Summary) bool {
	return c.State == container.StateRunning && (c.Labels[labelRole] == roleFetch || c.Labels[labelRole] == roleWorker)
}
