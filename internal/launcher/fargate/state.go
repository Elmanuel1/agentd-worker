package fargate

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// A task's and a container's lastStatus, which the SDK types only as a string.
const (
	lastRunning = "RUNNING"
	lastStopped = "STOPPED"
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
	arn := l.tasks[session].arn
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
		if aws.ToString(t.DesiredStatus) != string(ecstypes.DesiredStatusRunning) {
			continue
		}
		running = true
		fetchRuns := slices.ContainsFunc(t.Containers, func(c ecstypes.Container) bool {
			return aws.ToString(c.Name) == containerFetch && aws.ToString(c.LastStatus) == lastRunning
		})
		if aws.ToString(t.LastStatus) != lastRunning && !fetchRuns {
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

// taskRetention is how long ECS keeps returning a task after it stops; it always returns one that
// runs. So a known task it does not return stopped at least this long ago once its launch is this
// old, and before that is not yet consistent, not gone, and Stop waits on it.
const taskRetention = time.Hour

// Stop stops the session's tasks and returns once each has stopped, so no later task shares the
// workspace with one still shutting down. ListTasks is eventually consistent, so the task the last
// Launch ran is read by its ARN as well, and a task told to stop before a controller restart is
// found by its family. The SDK's waiter is not used: it waits out its timeout on a task ECS no
// longer returns.
func (l *Launcher) Stop(ctx context.Context, session ids.SessionID) error {
	l.mu.Lock()
	known := l.tasks[session]
	l.mu.Unlock()
	listed, err := l.listTasks(ctx, session)
	if err != nil {
		return err
	}
	var arns []string
	if known.arn != "" {
		arns = append(arns, known.arn)
	}
	for _, t := range listed {
		if arn := aws.ToString(t.TaskArn); !slices.Contains(arns, arn) {
			arns = append(arns, arn)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, stopWait)
	defer cancel()
	// startedBy must be ListTasks' only filter, so tasks told to stop are found by their family.
	stopping := &ecs.ListTasksInput{Cluster: aws.String(l.cfg.Cluster), Family: aws.String(family(session)), DesiredStatus: ecstypes.DesiredStatusStopped}
	asked := map[string]bool{}
	for {
		tasks, err := l.describeTasks(ctx, arns)
		if err != nil {
			return err
		}
		// A task can first appear in any read, so each stops what still runs, once, and reads
		// again at once what the stop changed.
		var errs []error
		for _, t := range tasks {
			if aws.ToString(t.DesiredStatus) != string(ecstypes.DesiredStatusRunning) || asked[aws.ToString(t.TaskArn)] {
				continue
			}
			asked[aws.ToString(t.TaskArn)] = true
			_, err := l.ecs.StopTask(ctx, &ecs.StopTaskInput{Cluster: aws.String(l.cfg.Cluster), Task: t.TaskArn, Reason: aws.String("agentd-worker stopped the sandbox")})
			errs = append(errs, err)
		}
		if err := errors.Join(errs...); err != nil {
			return err
		}
		if len(errs) > 0 {
			continue
		}
		told, err := l.tasksOf(ctx, stopping)
		if err != nil {
			return err
		}
		missing := known.arn != "" && time.Since(known.at) < taskRetention &&
			!slices.ContainsFunc(tasks, func(t ecstypes.Task) bool { return aws.ToString(t.TaskArn) == known.arn })
		if !missing && !slices.ContainsFunc(append(tasks, told...), func(t ecstypes.Task) bool { return aws.ToString(t.LastStatus) != lastStopped }) {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the session's tasks to stop: %w", ctx.Err())
		case <-time.After(5 * time.Second):
		}
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
	if err := l.root.RemoveAll(sessionDir(session)); err != nil {
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
