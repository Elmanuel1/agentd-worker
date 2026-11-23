package docker

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// TestNoWorkerStartsDuringStop holds DK-3 on the engine: a worker whose fetch exits 0 while a Stop
// of its session runs is not started.
func TestNoWorkerStartsDuringStop(t *testing.T) {
	cfg := config(t)
	l := newLauncher(t, cfg)
	session := ids.SessionID("sesn_gate")
	t.Cleanup(func() { l.Release(context.Background(), session) })
	byRole := map[role]string{}
	for _, spec := range l.specs(session, ids.NewNonce()) {
		// Fetch stands in as a shell from the sandbox image that exits 0, as fetch does with its item.
		if role(spec.Config.Labels[labelRole]) == roleFetch {
			spec.Config.Image, spec.Config.Cmd, spec.Config.User = cfg.SandboxImage, []string{"sh", "-c", "exit 0"}, ""
		}
		res, err := l.cli.ContainerCreate(t.Context(), spec)
		if err != nil {
			t.Fatal(err)
		}
		byRole[role(spec.Config.Labels[labelRole])] = res.ID
	}
	release, err := l.gate.stop(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := l.cli.ContainerStart(t.Context(), byRole[roleFetch], client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	l.startWorker(t.Context(), session, byRole)
	got, err := l.cli.ContainerInspect(t.Context(), byRole[roleWorker], client.ContainerInspectOptions{})
	if err != nil || got.Container.State.Status != container.StateCreated {
		t.Errorf("worker %v, %v; want it left created while the Stop runs", got.Container.State, err)
	}
}
