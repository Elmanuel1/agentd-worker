package docker

import (
	"context"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// startWorker starts the worker once fetch exits 0, then watches it; a fetch that fails removes
// the sandbox at once.
func (l *Launcher) startWorker(ctx context.Context, session ids.SessionID, byRole map[role]string) {
	code, err := l.waitExit(ctx, byRole[roleFetch])
	if ctx.Err() != nil {
		return
	}
	if err != nil || code != 0 {
		l.log.Info("fetch exited without the work item; removing the sandbox", "session_id", session, "exit_code", code, "error", err)
		l.remove(ctx, session, byRole)
		return
	}
	// A Stop under way removes the sandbox itself.
	started, ok := l.gate.start(session)
	if !ok {
		return
	}
	_, err = l.cli.ContainerStart(ctx, byRole[roleWorker], client.ContainerStartOptions{})
	started()
	if err != nil {
		l.log.Warn("starting the worker failed", "session_id", session, "error", err)
		l.remove(ctx, session, byRole)
		return
	}
	l.watchWorker(ctx, session, byRole)
}

// watchWorker stops the sidecar when the worker exits, so it stops the item, then removes the
// sandbox's containers.
func (l *Launcher) watchWorker(ctx context.Context, session ids.SessionID, byRole map[role]string) {
	code, err := l.waitExit(ctx, byRole[roleWorker])
	if ctx.Err() != nil || err != nil {
		return
	}
	l.log.Info("worker exited", "session_id", session, "exit_code", code)
	timeout := roleSidecar.grace()
	if _, err := l.cli.ContainerStop(ctx, byRole[roleSidecar], client.ContainerStopOptions{Timeout: &timeout}); err != nil && !cerrdefs.IsNotFound(err) {
		l.log.Warn("stopping the sidecar failed", "session_id", session, "error", err)
	}
	l.remove(ctx, session, byRole)
}

// waitExit waits for the container to exit. An engine error waits again, after up to 30 s, so a
// lost connection does not end the watch; a container gone, or ctx ending, does.
func (l *Launcher) waitExit(ctx context.Context, id string) (int64, error) {
	delay := time.Second
	for {
		code, err := l.wait(ctx, id, container.WaitConditionNotRunning)
		if err == nil || ctx.Err() != nil || cerrdefs.IsNotFound(err) {
			return code, err
		}
		l.log.Warn("waiting on a sandbox container failed; waiting again", "container", id, "error", err)
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(2*delay, 30*time.Second)
	}
}

func (l *Launcher) wait(ctx context.Context, id string, until container.WaitCondition) (int64, error) {
	wait := l.cli.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: until})
	select {
	case res := <-wait.Result:
		return res.StatusCode, nil
	case err := <-wait.Error:
		return -1, err
	}
}

// remove removes this launch's containers only: a later Launch may already have replaced them.
// The work-item volume stays until the session's next Launch or Stop.
func (l *Launcher) remove(ctx context.Context, session ids.SessionID, byRole map[role]string) {
	for _, id := range byRole {
		_, err := l.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
		if err != nil && !cerrdefs.IsNotFound(err) {
			l.log.Warn("removing a sandbox container failed", "session_id", session, "error", err)
		}
	}
}
