package fargate

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// tasks returns the cluster's tasks that should run, the session's alone when session is set.
func (l *Launcher) tasks(ctx context.Context, session ids.SessionID) ([]ecstypes.Task, error) {
	in := &ecs.ListTasksInput{Cluster: aws.String(l.cfg.Cluster), DesiredStatus: ecstypes.DesiredStatusRunning}
	if session != "" {
		in.StartedBy = aws.String(string(session))
	}
	var arns []string
	for p := ecs.NewListTasksPaginator(l.ecs, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing tasks: %w", err)
		}
		arns = append(arns, page.TaskArns...)
	}
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

// accessPoints returns the file system's access points by the session each is tagged with.
func (l *Launcher) accessPoints(ctx context.Context) (map[ids.SessionID][]string, error) {
	out := map[ids.SessionID][]string{}
	in := &efs.DescribeAccessPointsInput{FileSystemId: aws.String(l.cfg.FileSystemID)}
	for p := efs.NewDescribeAccessPointsPaginator(l.efs, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing access points: %w", err)
		}
		for _, ap := range page.AccessPoints {
			for _, tag := range ap.Tags {
				if aws.ToString(tag.Key) != tagSession {
					continue
				}
				if s, err := ids.ParseSessionID(aws.ToString(tag.Value)); err == nil {
					out[s] = append(out[s], aws.ToString(ap.AccessPointId))
				}
			}
		}
	}
	return out, nil
}
