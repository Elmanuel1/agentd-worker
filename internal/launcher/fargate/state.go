package fargate

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// List returns each session with a task that should run or an access point tagged with it.
func (l *Launcher) List(ctx context.Context) ([]dispatch.Sandbox, error) {
	tasks, err := l.tasks(ctx, "")
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
// starting runs without an address.
func (l *Launcher) Address(ctx context.Context, session ids.SessionID) (netip.Addr, bool, error) {
	tasks, err := l.tasks(ctx, session)
	if err != nil || len(tasks) == 0 {
		return netip.Addr{}, false, err
	}
	for _, t := range tasks {
		if !fetchRuns(t) {
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
	return netip.Addr{}, true, nil
}

func fetchRuns(t ecstypes.Task) bool {
	if aws.ToString(t.LastStatus) == "RUNNING" {
		return true
	}
	for _, c := range t.Containers {
		if aws.ToString(c.Name) == containerFetch && aws.ToString(c.LastStatus) == "RUNNING" {
			return true
		}
	}
	return false
}

// Stop stops the session's tasks.
func (l *Launcher) Stop(ctx context.Context, session ids.SessionID) error {
	tasks, err := l.tasks(ctx, session)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		_, err := l.ecs.StopTask(ctx, &ecs.StopTaskInput{Cluster: aws.String(l.cfg.Cluster), Task: t.TaskArn, Reason: aws.String("agentd-worker stopped the sandbox")})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Release stops the session's tasks and deletes its access point, its directory and its family.
func (l *Launcher) Release(ctx context.Context, session ids.SessionID) error {
	if err := l.Stop(ctx, session); err != nil {
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
	if err := l.dirs.RemoveAll(path.Join(l.cfg.RootMount, sessionDir(session))); err != nil {
		return fmt.Errorf("removing the session's directory: %w", err)
	}
	return l.deleteFamily(ctx, session)
}

func (l *Launcher) deleteFamily(ctx context.Context, session ids.SessionID) error {
	var arns []string
	in := &ecs.ListTaskDefinitionsInput{FamilyPrefix: aws.String(family(session))}
	for p := ecs.NewListTaskDefinitionsPaginator(l.ecs, in); p.HasMorePages(); {
		page, err := p.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("listing the family's revisions: %w", err)
		}
		for _, arn := range page.TaskDefinitionArns {
			// The prefix also matches longer session ids; the ARN ends in family:revision.
			if strings.HasPrefix(path.Base(arn), family(session)+":") {
				arns = append(arns, arn)
			}
		}
	}
	for _, arn := range arns {
		if _, err := l.ecs.DeregisterTaskDefinition(ctx, &ecs.DeregisterTaskDefinitionInput{TaskDefinition: aws.String(arn)}); err != nil {
			return fmt.Errorf("deregistering %s: %w", arn, err)
		}
	}
	for chunk := range slices.Chunk(arns, 10) {
		if _, err := l.ecs.DeleteTaskDefinitions(ctx, &ecs.DeleteTaskDefinitionsInput{TaskDefinitions: chunk}); err != nil {
			return fmt.Errorf("deleting the family's revisions: %w", err)
		}
	}
	return nil
}
