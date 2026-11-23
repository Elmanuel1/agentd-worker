package fargate

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// listTasks returns the cluster's tasks that should run, or the session's alone when session is set.
func (l *Launcher) listTasks(ctx context.Context, session ids.SessionID) ([]ecstypes.Task, error) {
	// ListTasks takes startedBy only as its sole filter; the desired status defaults to RUNNING.
	in := &ecs.ListTasksInput{Cluster: aws.String(l.cfg.Cluster), DesiredStatus: ecstypes.DesiredStatusRunning}
	if session != "" {
		in = &ecs.ListTasksInput{Cluster: aws.String(l.cfg.Cluster), StartedBy: aws.String(string(session))}
	}
	var arns []string
	for p := ecs.NewListTasksPaginator(l.ecs, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing tasks: %w", err)
		}
		arns = append(arns, page.TaskArns...)
	}
	return l.describeTasks(ctx, arns)
}

func (l *Launcher) describeTasks(ctx context.Context, arns []string) ([]ecstypes.Task, error) {
	var tasks []ecstypes.Task
	for chunk := range slices.Chunk(arns, 100) {
		out, err := l.ecs.DescribeTasks(ctx, &ecs.DescribeTasksInput{Cluster: aws.String(l.cfg.Cluster), Tasks: chunk})
		if err != nil {
			return nil, fmt.Errorf("describing tasks: %w", err)
		}
		tasks = append(tasks, out.Tasks...)
	}
	return tasks, nil
}

// stopWait bounds the wait for stopped tasks: the worker's 60 s after SIGTERM, the sidecar's 30 s
// after it, and room for ECS to deprovision.
const stopWait = 3 * time.Minute

// waitStopped reads the tasks every 5 s until each is STOPPED or gone. The SDK's waiter would
// wait out its timeout on a task ECS no longer returns.
func (l *Launcher) waitStopped(ctx context.Context, arns []string) error {
	ctx, cancel := context.WithTimeout(ctx, stopWait)
	defer cancel()
	for len(arns) > 0 {
		tasks, err := l.describeTasks(ctx, arns)
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
	return nil
}
