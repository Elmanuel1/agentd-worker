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
// engine's embedded resolver answers it. A relay already running is reused.
func (l *Launcher) startResolver(ctx context.Context) error {
	if l.cfg.Runtime == runc {
		return nil
	}
	name := "agentd-resolver-" + l.cfg.Network
	_, err := l.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: l.cfg.ToolImage, Cmd: []string{roleResolver}, User: root,
			Labels: map[string]string{labelRole: roleResolver},
		},
		HostConfig: &container.HostConfig{
			Runtime:       runc,
			NetworkMode:   container.NetworkMode(l.cfg.Network),
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			CapDrop:       []string{"ALL"},
			CapAdd:        []string{"NET_BIND_SERVICE"},
			SecurityOpt:   []string{"no-new-privileges"},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{l.cfg.Network: {}}},
	})
	if err != nil && !cerrdefs.IsConflict(err) {
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

func (l *Launcher) resolvConf() string {
	return filepath.Join(l.cfg.StateDir, "resolv.conf")
}
