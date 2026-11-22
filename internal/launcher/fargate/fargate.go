// Package fargate is the Launcher adapter for ECS on Fargate: each session's sandbox is one task
// of a task-definition family named after the session, its /workspace a directory on EFS reached
// through the session's own access point.
package fargate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"

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

// Dirs removes a session's directory through the file system's root, mounted in the controller.
type Dirs interface {
	RemoveAll(path string) error
}

// Config holds the adapter's settings.
type Config struct {
	Cluster                 string
	Subnets, SecurityGroups []string
	FileSystemID            string
	// RootMount is where the controller mounts the file system's root.
	RootMount string
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
	ecs  ECS
	efs  EFS
	dirs Dirs
	cfg  Config
}

func New(ecsClient ECS, efsClient EFS, dirs Dirs, cfg Config) *Launcher {
	return &Launcher{ecs: ecsClient, efs: efsClient, dirs: dirs, cfg: cfg}
}

func family(s ids.SessionID) string { return "agentd-" + string(s) }

func sessionDir(s ids.SessionID) string { return "/sessions/" + string(s) }

// Launch stops the session's earlier tasks, creates its access point when it has none, registers
// a family revision when the family has none or its config changed, then runs its task with the
// nonce in the fetch container's environment.
func (l *Launcher) Launch(ctx context.Context, session ids.SessionID, nonce ids.Nonce) error {
	if err := l.Stop(ctx, session); err != nil {
		return err
	}
	ap, err := l.efs.CreateAccessPoint(ctx, &efs.CreateAccessPointInput{
		ClientToken:  aws.String(string(session)),
		FileSystemId: aws.String(l.cfg.FileSystemID),
		PosixUser:    &efstypes.PosixUser{Uid: aws.Int64(1001), Gid: aws.Int64(1000)},
		RootDirectory: &efstypes.RootDirectory{
			Path:         aws.String(sessionDir(session)),
			CreationInfo: &efstypes.CreationInfo{OwnerUid: aws.Int64(1001), OwnerGid: aws.Int64(1000), Permissions: aws.String("0750")},
		},
		Tags: []efstypes.Tag{{Key: aws.String(tagSession), Value: aws.String(string(session))}},
	})
	if err != nil {
		return fmt.Errorf("creating the access point: %w", err)
	}
	if err := l.ensureFamily(ctx, session, aws.ToString(ap.AccessPointId)); err != nil {
		return err
	}
	out, err := l.ecs.RunTask(ctx, &ecs.RunTaskInput{
		Cluster:        aws.String(l.cfg.Cluster),
		TaskDefinition: aws.String(family(session)),
		LaunchType:     ecstypes.LaunchTypeFargate,
		StartedBy:      aws.String(string(session)),
		NetworkConfiguration: &ecstypes.NetworkConfiguration{AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
			Subnets: l.cfg.Subnets, SecurityGroups: l.cfg.SecurityGroups, AssignPublicIp: ecstypes.AssignPublicIpDisabled,
		}},
		Overrides: &ecstypes.TaskOverride{ContainerOverrides: []ecstypes.ContainerOverride{{
			Name:        aws.String(containerFetch),
			Environment: []ecstypes.KeyValuePair{{Name: aws.String("AGENTD_HANDOFF_NONCE"), Value: aws.String(string(nonce))}},
		}}},
	})
	if err != nil {
		return fmt.Errorf("running the task: %w", err)
	}
	if len(out.Failures) > 0 {
		return fmt.Errorf("running the task: %s", aws.ToString(out.Failures[0].Reason))
	}
	return nil
}

// ensureFamily registers a revision when the family's latest one was made from another config: a
// new image, size, handoff URL or CA then reaches the session's next launch.
func (l *Launcher) ensureFamily(ctx context.Context, session ids.SessionID, accessPoint string) error {
	def := l.taskDefinition(session, accessPoint)
	raw, err := json.Marshal(def)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	config := hex.EncodeToString(sum[:16])
	out, err := l.ecs.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(family(session)),
		Include:        []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
	})
	ce, isClient := errors.AsType[*ecstypes.ClientException](err)
	missing := isClient && strings.Contains(strings.ToLower(ce.ErrorMessage()), "unable to describe task definition")
	if err != nil && !missing {
		return fmt.Errorf("reading the task definition: %w", err)
	}
	if err == nil && slices.ContainsFunc(out.Tags, func(t ecstypes.Tag) bool {
		return aws.ToString(t.Key) == tagConfig && aws.ToString(t.Value) == config
	}) {
		return nil
	}
	def.Tags = []ecstypes.Tag{{Key: aws.String(tagConfig), Value: aws.String(config)}}
	if _, err := l.ecs.RegisterTaskDefinition(ctx, def); err != nil {
		return fmt.Errorf("registering the task definition: %w", err)
	}
	return nil
}
