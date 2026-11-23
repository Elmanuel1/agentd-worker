package fargate

import (
	"context"
	"fmt"
	"slices"

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
	return l.tasksOf(ctx, in)
}

// tasksOf lists the tasks in matches and describes them.
func (l *Launcher) tasksOf(ctx context.Context, in *ecs.ListTasksInput) ([]ecstypes.Task, error) {
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
