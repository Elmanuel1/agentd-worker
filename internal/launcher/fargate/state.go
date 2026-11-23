package fargate

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"time"

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

// stopWait bounds the wait for stopped tasks: the worker's 60 s after SIGTERM, the sidecar's 30 s
// after it, and room for ECS to deprovision.
const stopWait = 3 * time.Minute

// Stop stops the session's tasks and returns once every task of its family has stopped, so no
// later task shares the workspace with one still shutting down, one told to stop before a
// controller restart included. The SDK's waiter is not used: it waits out its timeout on a task ECS
// no longer returns.
func (l *Launcher) Stop(ctx context.Context, session ids.SessionID) error {
	l.mu.Lock()
	delete(l.tasks, session)
	l.mu.Unlock()
	tasks, err := l.listTasks(ctx, session)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		_, err := l.ecs.StopTask(ctx, &ecs.StopTaskInput{Cluster: aws.String(l.cfg.Cluster), Task: t.TaskArn, Reason: aws.String("agentd-worker stopped the sandbox")})
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, stopWait)
	defer cancel()
	// startedBy must be ListTasks' only filter, so tasks told to stop are found by their family.
	stopping := &ecs.ListTasksInput{Cluster: aws.String(l.cfg.Cluster), Family: aws.String(family(session)), DesiredStatus: ecstypes.DesiredStatusStopped}
	for {
		tasks, err := l.tasksOf(ctx, stopping)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(tasks, func(t ecstypes.Task) bool { return aws.ToString(t.LastStatus) != "STOPPED" }) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the session's tasks to stop: %w", ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
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
