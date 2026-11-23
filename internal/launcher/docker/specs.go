package docker

import (
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/fetch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

const (
	labelSession = "agentd.session"
	labelRole    = "agentd.role"
	// root runs fetch, which gives the worker the item's files, and the sidecar, which reads them.
	root = "0:0"
)

// role is a sandbox container's or volume's part, labelled on it.
type role string

const (
	roleFetch     role = "fetch"
	roleWorker    role = "worker"
	roleSidecar   role = "sidecar"
	roleWorkspace role = "workspace"
	roleRun       role = "run"
)

func roleOf(c container.Summary) role { return role(c.Labels[labelRole]) }

// grace is the seconds a container of the role gets after SIGTERM. The worker gets 60, as
// Anthropic's operations page advises (--stop-timeout 60), so ant posts its error result and
// stops its item; the sidecar 30 to stop the item itself, as on Fargate.
func (r role) grace() int {
	if r == roleWorker {
		return 60
	}
	return 30
}

// addressable is whether the sandbox's address is a running container of the role's: fetch's while
// it collects the item, then the worker's.
func (r role) addressable() bool { return r == roleFetch || r == roleWorker }

func workspaceVolume(s ids.SessionID) string { return "agentd-ws-" + string(s) }
func runVolume(s ids.SessionID) string       { return "agentd-run-" + string(s) }

func labels(s ids.SessionID, r role) map[string]string {
	return map[string]string{labelSession: string(s), labelRole: string(r)}
}

// specs are the session's three containers. None gets the Docker socket, a host or shared
// namespace, or an added capability, nor any host path but the read-only resolv.conf under runsc;
// the worker, which runs the session's code, drops every capability.
func (l *Launcher) specs(session ids.SessionID, nonce ids.Nonce) []client.ContainerCreateOptions {
	run := mount.Mount{Type: mount.TypeVolume, Source: runVolume(session), Target: "/run/cma"}
	workspace := mount.Mount{Type: mount.TypeVolume, Source: workspaceVolume(session), Target: "/workspace"}
	fetchSpec := l.spec(session, roleFetch, &container.Config{
		Image: l.cfg.ToolImage,
		Cmd:   []string{"fetch"},
		User:  root,
		Env:   []string{fetch.EnvNonce + "=" + string(nonce)},
	}, []mount.Mount{run}, nil)
	for _, v := range fetch.Env(l.cfg.HandoffURL, l.cfg.HandoffCA, l.cfg.HandoffWait) {
		fetchSpec.Config.Env = append(fetchSpec.Config.Env, v.Name+"="+v.Value)
	}
	worker := l.spec(session, roleWorker, &container.Config{Image: l.cfg.SandboxImage}, []mount.Mount{workspace, run}, []string{"ALL"})
	sidecar := l.spec(session, roleSidecar, &container.Config{Image: l.cfg.ToolImage, Cmd: []string{"sidecar"}, User: root}, []mount.Mount{run}, nil)
	return []client.ContainerCreateOptions{fetchSpec, worker, sidecar}
}

func (l *Launcher) spec(session ids.SessionID, r role, cfg *container.Config, mounts []mount.Mount, drop []string) client.ContainerCreateOptions {
	cfg.Labels = labels(session, r)
	// Under runsc the engine's embedded resolver is out of reach, so the sandbox's one host path
	// is the read-only resolv.conf naming the relay startResolver runs.
	if l.cfg.Runtime != runc {
		mounts = append(slices.Clone(mounts), mount.Mount{Type: mount.TypeBind, Source: l.resolvConf(), Target: "/etc/resolv.conf", ReadOnly: true})
	}
	return client.ContainerCreateOptions{
		Name:   "agentd-" + string(r) + "-" + string(session),
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
