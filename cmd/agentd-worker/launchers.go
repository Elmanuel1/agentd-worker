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
	"github.com/moby/moby/client"

	"github.com/Elmanuel1/agentd-worker/internal/launcher/docker"
	"github.com/Elmanuel1/agentd-worker/internal/launcher/fargate"
)

// sandboxConfig is what every adapter gives the sandboxes it launches.
type sandboxConfig struct {
	handoffURL, handoffCA   string
	handoffWait             time.Duration
	sandboxImage, toolImage string
}

func newBackend(ctx context.Context, name string, sc sandboxConfig, log *slog.Logger) (backend, error) {
	switch name {
	case "docker":
		return newDocker(ctx, sc, log)
	case "fargate":
		return newFargate(ctx, sc)
	default:
		return nil, fmt.Errorf("AGENTD_WORKER_LAUNCHER %q: want docker or fargate", name)
	}
}

func newDocker(ctx context.Context, sc sandboxConfig, log *slog.Logger) (backend, error) {
	var e env
	cfg := docker.Config{
		Network: e.required("AGENTD_DOCKER_NETWORK"), Runtime: e.str("AGENTD_DOCKER_RUNTIME", "runsc"),
		StateDir:     e.str("AGENTD_DOCKER_STATE_DIR", "/var/lib/agentd-worker"),
		SandboxImage: sc.sandboxImage, ToolImage: sc.toolImage,
		HandoffURL: sc.handoffURL, HandoffCA: sc.handoffCA, HandoffWait: sc.handoffWait,
	}
	if err := e.err(); err != nil {
		return nil, err
	}
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	l, err := docker.New(ctx, cli, cfg, log)
	if err != nil {
		cli.Close()
		return nil, err
	}
	return l, nil
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

// defaultHandoffWait is 120 s, or on Fargate the most its start timeout leaves room for.
func defaultHandoffWait(launcher string) int {
	if launcher == "fargate" {
		return int(fargateMaxWait / time.Second)
	}
	return 120
}

func newFargate(ctx context.Context, sc sandboxConfig) (backend, error) {
	if sc.handoffWait > fargateMaxWait {
		return nil, fmt.Errorf("AGENTD_HANDOFF_WAIT_SECONDS is %s; on Fargate it must be at most %s", sc.handoffWait, fargateMaxWait)
	}
	var e env
	cfg := fargate.Config{
		Cluster: e.required("AGENTD_ECS_CLUSTER"), Subnets: e.list("AGENTD_SUBNETS"), SecurityGroups: e.list("AGENTD_SECURITY_GROUPS"),
		FileSystemID: e.required("AGENTD_EFS_ID"), RootMount: e.str("AGENTD_EFS_ROOT", "/mnt/efs"),
		ExecutionRoleARN: e.required("AGENTD_EXECUTION_ROLE_ARN"), TaskRoleARN: e.required("AGENTD_TASK_ROLE_ARN"),
		SandboxImage: sc.sandboxImage, ToolImage: sc.toolImage,
		CPU: e.str("AGENTD_TASK_CPU", "512"), Memory: e.str("AGENTD_TASK_MEMORY", "1024"),
		LogGroup: e.required("AGENTD_LOG_GROUP"), Region: e.required("AWS_REGION"),
		HandoffURL: sc.handoffURL, HandoffCA: sc.handoffCA, HandoffWait: sc.handoffWait,
	}
	if err := e.err(); err != nil {
		return nil, err
	}
	aws, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return fargateBackend{fargate.New(ecs.NewFromConfig(aws), efs.NewFromConfig(aws), rootDirs{}, cfg)}, nil
}
