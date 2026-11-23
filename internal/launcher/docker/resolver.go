package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	roleResolver = "resolver"
	// runc is the engine's own runtime, under which the embedded resolver answers.
	runc = "runc"
)

// startResolver runs the DNS relay on the sandbox network under runc and writes a resolv.conf
// naming it, which each sandbox mounts read-only. Under runc a sandbox needs neither: the
// engine's embedded resolver answers it. A relay already running from the same image is reused.
func (l *Launcher) startResolver(ctx context.Context) error {
	if l.cfg.Runtime == runc {
		return nil
	}
	name := "agentd-resolver-" + l.cfg.Network
	err := l.createResolver(ctx, name)
	if cerrdefs.IsConflict(err) {
		err = l.replaceStaleResolver(ctx, name)
	}
	if err != nil {
		return fmt.Errorf("creating the resolver: %w", err)
	}
	if _, err := l.cli.ContainerStart(ctx, name, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting the resolver: %w", err)
	}
	got, err := l.cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("reading the resolver's address: %w", err)
	}
	ep := got.Container.NetworkSettings.Networks[l.cfg.Network]
	if ep == nil || !ep.IPAddress.IsValid() {
		return fmt.Errorf("the resolver has no address on network %s", l.cfg.Network)
	}
	if err := os.MkdirAll(l.cfg.StateDir, 0o755); err != nil {
		return err
	}
	conf := fmt.Sprintf("nameserver %s\noptions ndots:0\n", ep.IPAddress)
	return os.WriteFile(l.resolvConf(), []byte(conf), 0o644)
}

// createResolver makes the relay. Every sandbox reaches it, so it runs as a user with no
// capability, binding port 53 through the sysctl, with bounded memory and processes.
func (l *Launcher) createResolver(ctx context.Context, name string) error {
	memory, pids := int64(64<<20), int64(64)
	_, err := l.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: l.cfg.ToolImage, Cmd: []string{roleResolver}, User: "65532:65532",
			// The Go runtime does not read the cgroup's limit; this keeps the heap under it.
			Env:    []string{"GOMEMLIMIT=48MiB"},
			Labels: map[string]string{labelRole: roleResolver},
		},
		HostConfig: &container.HostConfig{
			Runtime:        runc,
			NetworkMode:    container.NetworkMode(l.cfg.Network),
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			ReadonlyRootfs: true,
			Sysctls:        map[string]string{"net.ipv4.ip_unprivileged_port_start": "53"},
			Resources:      container.Resources{Memory: memory, PidsLimit: &pids},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{l.cfg.Network: {}}},
	})
	return err
}

// replaceStaleResolver keeps an existing relay made from the image the tool image names now, and
// replaces one made from another, so an image rebuilt under the same tag reaches it.
func (l *Launcher) replaceStaleResolver(ctx context.Context, name string) error {
	got, err := l.cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	image, err := l.cli.ImageInspect(ctx, l.cfg.ToolImage)
	if err != nil {
		return fmt.Errorf("reading the tool image: %w", err)
	}
	if got.Container.Image == image.ID {
		return nil
	}
	if _, err := l.cli.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true}); err != nil {
		return err
	}
	return l.createResolver(ctx, name)
}

func (l *Launcher) resolvConf() string {
	return filepath.Join(l.cfg.StateDir, "resolv.conf")
}
