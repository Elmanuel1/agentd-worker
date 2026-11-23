package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/kelseyhightower/envconfig"

	"github.com/Elmanuel1/agentd-worker/internal/launcher/fargate"
)

// FargateSettings are the Fargate backend's own.
type FargateSettings struct {
	Cluster          string   `envconfig:"AGENTD_ECS_CLUSTER" required:"true"`
	Subnets          []string `envconfig:"AGENTD_SUBNETS" required:"true"`
	SecurityGroups   []string `envconfig:"AGENTD_SECURITY_GROUPS" required:"true"`
	FileSystemID     string   `envconfig:"AGENTD_EFS_ID" required:"true"`
	RootMount        string   `envconfig:"AGENTD_EFS_ROOT" default:"/mnt/efs"`
	ExecutionRoleARN string   `envconfig:"AGENTD_EXECUTION_ROLE_ARN" required:"true"`
	TaskRoleARN      string   `envconfig:"AGENTD_TASK_ROLE_ARN" required:"true"`
	CPU              string   `envconfig:"AGENTD_TASK_CPU" default:"512"`
	Memory           string   `envconfig:"AGENTD_TASK_MEMORY" default:"1024"`
	LogGroup         string   `envconfig:"AGENTD_LOG_GROUP" required:"true"`
	Region           string   `envconfig:"AWS_REGION" required:"true"`
}

// fargateBackend adds the no-op Close the Fargate adapter needs to be a backend.
type fargateBackend struct{ *fargate.Launcher }

func (fargateBackend) Close() {}

// rootDirs removes directories under the EFS root the controller mounts.
type rootDirs struct{}

func (rootDirs) RemoveAll(path string) error { return os.RemoveAll(path) }

// fargateMaxWait keeps the handoff wait inside the 120 s a Fargate container may wait for its
// dependency, with room for the image pull.
const fargateMaxWait = 110 * time.Second

func openFargate(ctx context.Context, sc sandboxConfig, _ *slog.Logger) (backend, error) {
	if sc.handoffWait > fargateMaxWait {
		return nil, fmt.Errorf("AGENTD_HANDOFF_WAIT_SECONDS is %s; on Fargate it must be at most %s", sc.handoffWait, fargateMaxWait)
	}
	var s FargateSettings
	if err := envconfig.Process("", &s); err != nil {
		return nil, err
	}
	aws, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return fargateBackend{fargate.New(ecs.NewFromConfig(aws), efs.NewFromConfig(aws), rootDirs{}, fargate.Config{
		Cluster: s.Cluster, Subnets: s.Subnets, SecurityGroups: s.SecurityGroups,
		FileSystemID: s.FileSystemID, RootMount: s.RootMount,
		ExecutionRoleARN: s.ExecutionRoleARN, TaskRoleARN: s.TaskRoleARN,
		SandboxImage: sc.sandboxImage, ToolImage: sc.toolImage, CPU: s.CPU, Memory: s.Memory,
		LogGroup: s.LogGroup, Region: s.Region,
		HandoffURL: sc.handoffURL, HandoffCA: sc.handoffCA, HandoffWait: sc.handoffWait,
	})}, nil
}
