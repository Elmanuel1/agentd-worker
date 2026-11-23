// Package fargate is the Launcher adapter for ECS on Fargate: each session's sandbox is one task
// of a task-definition family named after the session, its /workspace a directory on EFS reached
// through the session's own access point.
package fargate

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"

	"github.com/Elmanuel1/agentd-worker/internal/fetch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

const (
	// tagSession tags each access point with its session.
	tagSession = "agentd:session"
	// tagConfig tags each family revision with a digest of the config it was made from.
	tagConfig = "agentd:config"
)

// ECS holds the ECS calls the adapter makes.
type ECS interface {
	RunTask(context.Context, *ecs.RunTaskInput, ...func(*ecs.Options)) (*ecs.RunTaskOutput, error)
	StopTask(context.Context, *ecs.StopTaskInput, ...func(*ecs.Options)) (*ecs.StopTaskOutput, error)
	ListTasks(context.Context, *ecs.ListTasksInput, ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
	DescribeTasks(context.Context, *ecs.DescribeTasksInput, ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
	RegisterTaskDefinition(context.Context, *ecs.RegisterTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
	DescribeTaskDefinition(context.Context, *ecs.DescribeTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error)
	ListTaskDefinitions(context.Context, *ecs.ListTaskDefinitionsInput, ...func(*ecs.Options)) (*ecs.ListTaskDefinitionsOutput, error)
	DeregisterTaskDefinition(context.Context, *ecs.DeregisterTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error)
	DeleteTaskDefinitions(context.Context, *ecs.DeleteTaskDefinitionsInput, ...func(*ecs.Options)) (*ecs.DeleteTaskDefinitionsOutput, error)
}

// EFS holds the EFS calls the adapter makes.
type EFS interface {
	CreateAccessPoint(context.Context, *efs.CreateAccessPointInput, ...func(*efs.Options)) (*efs.CreateAccessPointOutput, error)
	DescribeAccessPoints(context.Context, *efs.DescribeAccessPointsInput, ...func(*efs.Options)) (*efs.DescribeAccessPointsOutput, error)
	DeleteAccessPoint(context.Context, *efs.DeleteAccessPointInput, ...func(*efs.Options)) (*efs.DeleteAccessPointOutput, error)
}

// Config holds the adapter's settings.
type Config struct {
	Cluster                 string
	Subnets, SecurityGroups []string
	FileSystemID            string
	// RootMount is where the controller mounts the file system's root.
	RootMount string
	// WorkerUID and WorkerGID own each session's directory: the sandbox image's worker user.
	WorkerUID, WorkerGID int64
	// TaskRoleARN names a role with no permission; ExecutionRoleARN pulls images and writes logs.
	ExecutionRoleARN, TaskRoleARN string
	SandboxImage, ToolImage       string
	CPU, Memory                   string
	LogGroup, Region              string
	HandoffURL, HandoffCA         string
	HandoffWait                   time.Duration
}

// Launcher runs sandboxes as Fargate tasks.
type Launcher struct {
	ecs ECS
	efs EFS
	// root is the file system's root, through which Release removes a session's directory; an
	// os.Root keeps the removal inside it.
	root *os.Root
	cfg  Config
	mu   sync.Mutex
	// tasks holds the task each Launch ran, so Address reads it with one DescribeTasks call;
	// ECS allows 20 such reads a second, and a ListTasks per handoff would spend two.
	tasks map[ids.SessionID]string
}

// MaxHandoffWait keeps the handoff wait inside the start timeout a Fargate container waits for its
// dependency, with room for the image pull.
const MaxHandoffWait = startTimeout - 10*time.Second

// New refuses a handoff wait Fargate cannot keep, and opens the file system's root.
func New(ecsClient ECS, efsClient EFS, cfg Config) (*Launcher, error) {
	if cfg.HandoffWait <= 0 || cfg.HandoffWait > MaxHandoffWait {
		return nil, fmt.Errorf("the handoff wait is %s; on Fargate it must be above 0 and at most %s", cfg.HandoffWait, MaxHandoffWait)
	}
	root, err := os.OpenRoot(cfg.RootMount)
	if err != nil {
		return nil, fmt.Errorf("opening the file system's root: %w", err)
	}
	return &Launcher{ecs: ecsClient, efs: efsClient, root: root, cfg: cfg, tasks: map[ids.SessionID]string{}}, nil
}

func (l *Launcher) Close() { l.root.Close() }

func family(s ids.SessionID) string { return "agentd-sandbox-" + string(s) }

// sessionDir is the session's directory under the file system's root.
func sessionDir(s ids.SessionID) string { return "sessions/" + string(s) }

// Launch stops the session's earlier tasks, makes its access point and family revision when it has
// none or its config changed, then runs that revision with the nonce in the fetch container's
// environment. The nonce is the call's idempotency token, so an SDK retry runs no second task.
func (l *Launcher) Launch(ctx context.Context, session ids.SessionID, nonce ids.Nonce) error {
	if err := l.Stop(ctx, session); err != nil {
		return err
	}
	accessPoint, err := l.accessPoint(ctx, session)
	if err != nil {
		return err
	}
	revision, err := l.ensureFamily(ctx, session, accessPoint)
	if err != nil {
		return err
	}
	out, err := l.ecs.RunTask(ctx, &ecs.RunTaskInput{
		Cluster:        aws.String(l.cfg.Cluster),
		TaskDefinition: aws.String(revision),
		ClientToken:    aws.String(string(nonce)),
		LaunchType:     ecstypes.LaunchTypeFargate,
		StartedBy:      aws.String(string(session)),
		NetworkConfiguration: &ecstypes.NetworkConfiguration{AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
			Subnets: l.cfg.Subnets, SecurityGroups: l.cfg.SecurityGroups, AssignPublicIp: ecstypes.AssignPublicIpDisabled,
		}},
		Overrides: &ecstypes.TaskOverride{ContainerOverrides: []ecstypes.ContainerOverride{{
			Name:        aws.String(containerFetch),
			Environment: []ecstypes.KeyValuePair{{Name: aws.String(fetch.EnvNonce), Value: aws.String(string(nonce))}},
		}}},
	})
	if err != nil {
		return fmt.Errorf("running the task: %w", err)
	}
	if len(out.Failures) > 0 || len(out.Tasks) == 0 {
		return fmt.Errorf("running the task: %v", out.Failures)
	}
	l.mu.Lock()
	l.tasks[session] = aws.ToString(out.Tasks[0].TaskArn)
	l.mu.Unlock()
	return nil
}
