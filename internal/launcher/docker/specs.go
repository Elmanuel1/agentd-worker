package docker

import (
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

const (
	labelSession  = "agentd.session"
	labelRole     = "agentd.role"
	roleFetch     = "fetch"
	roleWorker    = "worker"
	roleSidecar   = "sidecar"
	roleWorkspace = "workspace"
	roleRun       = "run"
	// root runs fetch, which gives the worker the item's files, and the sidecar, which reads them.
	root = "0:0"
)

func workspaceVolume(s ids.SessionID) string { return "agentd-ws-" + string(s) }
func runVolume(s ids.SessionID) string       { return "agentd-run-" + string(s) }

func labels(s ids.SessionID, role string) map[string]string {
	return map[string]string{labelSession: string(s), labelRole: role}
}

// specs are the session's three containers. None gets the Docker socket, a host or shared
// namespace, or an added capability, nor any host path but the read-only resolv.conf under runsc;
// the worker, which runs the session's code, drops every capability.
func (l *Launcher) specs(session ids.SessionID, nonce ids.Nonce) []client.ContainerCreateOptions {
	run := mount.Mount{Type: mount.TypeVolume, Source: runVolume(session), Target: "/run/cma"}
	workspace := mount.Mount{Type: mount.TypeVolume, Source: workspaceVolume(session), Target: "/workspace"}
	fetch := l.spec(session, roleFetch, &container.Config{
		Image: l.cfg.ToolImage,
		Cmd:   []string{"fetch"},
		User:  root,
		Env: []string{
			"AGENTD_HANDOFF_URL=" + l.cfg.HandoffURL,
			"AGENTD_HANDOFF_NONCE=" + string(nonce),
			"AGENTD_HANDOFF_CA=" + l.cfg.HandoffCA,
			"AGENTD_HANDOFF_WAIT=" + l.cfg.HandoffWait.String(),
		},
	}, []mount.Mount{run}, nil)
	worker := l.spec(session, roleWorker, &container.Config{Image: l.cfg.SandboxImage}, []mount.Mount{workspace, run}, []string{"ALL"})
	sidecar := l.spec(session, roleSidecar, &container.Config{Image: l.cfg.ToolImage, Cmd: []string{"sidecar"}, User: root}, []mount.Mount{run}, nil)
	return []client.ContainerCreateOptions{fetch, worker, sidecar}
}

func (l *Launcher) spec(session ids.SessionID, role string, cfg *container.Config, mounts []mount.Mount, drop []string) client.ContainerCreateOptions {
	cfg.Labels = labels(session, role)
	// Under runsc the engine's embedded resolver is out of reach, so the sandbox's one host path
	// is the read-only resolv.conf naming the relay startResolver runs.
	if l.cfg.Runtime != runc {
		mounts = append(slices.Clone(mounts), mount.Mount{Type: mount.TypeBind, Source: l.resolvConf(), Target: "/etc/resolv.conf", ReadOnly: true})
	}
	return client.ContainerCreateOptions{
		Name:   "agentd-" + role + "-" + string(session),
		Config: cfg,
		HostConfig: &container.HostConfig{
			Runtime:     l.cfg.Runtime,
			NetworkMode: container.NetworkMode(l.cfg.Network),
			Mounts:      mounts,
			CapDrop:     drop,
			IpcMode:     container.IPCModePrivate,
			SecurityOpt: []string{"no-new-privileges"},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{l.cfg.Network: {}}},
	}
}
