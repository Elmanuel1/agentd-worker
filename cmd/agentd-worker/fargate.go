package main

import (
	"context"
	"log/slog"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/kelseyhightower/envconfig"

	"github.com/Elmanuel1/agentd-worker/internal/launcher"
	"github.com/Elmanuel1/agentd-worker/internal/launcher/fargate"
)

// FargateSettings are read only when AGENTD_WORKER_LAUNCHER is fargate.
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

func openFargate(ctx context.Context, sc launcher.Sandbox, _ *slog.Logger) (backend, error) {
	var s FargateSettings
	if err := envconfig.Process("", &s); err != nil {
		return nil, err
	}
	aws, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return fargate.New(ecs.NewFromConfig(aws), efs.NewFromConfig(aws), fargate.Config{
		Cluster: s.Cluster, Subnets: s.Subnets, SecurityGroups: s.SecurityGroups,
		FileSystemID: s.FileSystemID, RootMount: s.RootMount, WorkerUID: workerUID, WorkerGID: workerGID,
		ExecutionRoleARN: s.ExecutionRoleARN, TaskRoleARN: s.TaskRoleARN,
		CPU: s.CPU, Memory: s.Memory,
		LogGroup: s.LogGroup, Region: s.Region,
		Sandbox: sc,
	})
}
