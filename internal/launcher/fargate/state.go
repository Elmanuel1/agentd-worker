package fargate

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// List returns each session with a task that should run or an access point tagged with it.
func (l *Launcher) List(ctx context.Context) ([]dispatch.Sandbox, error) {
	tasks, err := l.listTasks(ctx, "")
	if err != nil {
		return nil, err
	}
	points, err := l.accessPoints(ctx)
	if err != nil {
		return nil, err
	}
	running := map[ids.SessionID]bool{}
	for s := range points {
		running[s] = false
	}
	for _, t := range tasks {
		if s, err := ids.ParseSessionID(aws.ToString(t.StartedBy)); err == nil {
			running[s] = true
		}
	}
	out := make([]dispatch.Sandbox, 0, len(running))
	for s, r := range running {
		out = append(out, dispatch.Sandbox{Session: s, Running: r})
	}
	return out, nil
}

// Address is the task's private IP once it, or its fetch container, is RUNNING. A task still
// starting runs without an address. A known task costs one DescribeTasks call and no ListTasks.
func (l *Launcher) Address(ctx context.Context, session ids.SessionID) (netip.Addr, bool, error) {
	l.mu.Lock()
	arn := l.tasks[session]
	l.mu.Unlock()
	if arn == "" {
		return addressOf(l.listTasks(ctx, session))
	}
	return addressOf(l.describeTasks(ctx, []string{arn}))
}

// addressOf reads the address from the tasks that should run; a stopped task, which DescribeTasks
// still returns for a known ARN, is not running.
func addressOf(tasks []ecstypes.Task, err error) (netip.Addr, bool, error) {
	running := false
	for _, t := range tasks {
		if aws.ToString(t.DesiredStatus) != "RUNNING" {
			continue
		}
		running = true
		fetchRuns := slices.ContainsFunc(t.Containers, func(c ecstypes.Container) bool {
			return aws.ToString(c.Name) == containerFetch && aws.ToString(c.LastStatus) == "RUNNING"
		})
		if aws.ToString(t.LastStatus) != "RUNNING" && !fetchRuns {
			continue
		}
		for _, a := range t.Attachments {
			for _, d := range a.Details {
				if aws.ToString(d.Name) == "privateIPv4Address" {
					addr, err := netip.ParseAddr(aws.ToString(d.Value))
					return addr, true, err
				}
			}
		}
	}
	return netip.Addr{}, running, err
}

// Stop stops the session's tasks and returns once they have, so no later task shares the
// workspace with one still shutting down. The task a Launch ran stays known until then, so a
// retry after an interrupted wait waits for it too.
func (l *Launcher) Stop(ctx context.Context, session ids.SessionID) error {
	l.mu.Lock()
	known := l.tasks[session]
	l.mu.Unlock()
	tasks, err := l.listTasks(ctx, session)
	if err != nil {
		return err
	}
	var arns []string
	if known != "" {
		arns = append(arns, known)
	}
	var errs []error
	for _, t := range tasks {
		_, err := l.ecs.StopTask(ctx, &ecs.StopTaskInput{Cluster: aws.String(l.cfg.Cluster), Task: t.TaskArn, Reason: aws.String("agentd-worker stopped the sandbox")})
		errs = append(errs, err)
		if arn := aws.ToString(t.TaskArn); arn != known {
			arns = append(arns, arn)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if err := l.waitStopped(ctx, arns); err != nil {
		return err
	}
	l.mu.Lock()
	if l.tasks[session] == known {
		delete(l.tasks, session)
	}
	l.mu.Unlock()
	return nil
}

// Release stops the session's tasks and deletes its directory, its family and its access point.
// The access point goes last: it is what List finds the session by, so a failure before it
// leaves the session listed for the next pass to finish.
func (l *Launcher) Release(ctx context.Context, session ids.SessionID) error {
	if err := l.Stop(ctx, session); err != nil {
		return err
	}
	if err := l.dirs.RemoveAll(path.Join(l.cfg.RootMount, sessionDir(session))); err != nil {
		return fmt.Errorf("removing the session's directory: %w", err)
	}
	if err := l.deleteFamily(ctx, session); err != nil {
		return err
	}
	points, err := l.accessPoints(ctx)
	if err != nil {
		return err
	}
	for _, id := range points[session] {
		if _, err := l.efs.DeleteAccessPoint(ctx, &efs.DeleteAccessPointInput{AccessPointId: aws.String(id)}); err != nil {
			return fmt.Errorf("deleting access point %s: %w", id, err)
		}
	}
	return nil
}
