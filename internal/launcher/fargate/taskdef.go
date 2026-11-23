package fargate

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

const (
	containerFetch   = "fetch"
	containerWorker  = "worker"
	containerSidecar = "sidecar"
	volumeWorkspace  = "workspace"
	volumeRun        = "run"
)

// taskDefinition is the session's family: fetch, then the worker once fetch succeeds, and a
// non-essential sidecar, on ARM64. The workspace mounts through the session's access point with
// the task's IAM identity, which the file system's policy names. It sets no pidMode, so no container reads another's
// processes, and no secrets: the work secret reaches the task only through the handoff.
func (l *Launcher) taskDefinition(session ids.SessionID, accessPoint string) *ecs.RegisterTaskDefinitionInput {
	run := types.MountPoint{SourceVolume: aws.String(volumeRun), ContainerPath: aws.String("/run/cma")}
	workspace := types.MountPoint{SourceVolume: aws.String(volumeWorkspace), ContainerPath: aws.String("/workspace")}
	return &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family(session)),
		RequiresCompatibilities: []types.Compatibility{types.CompatibilityFargate},
		NetworkMode:             types.NetworkModeAwsvpc,
		Cpu:                     aws.String(l.cfg.CPU),
		Memory:                  aws.String(l.cfg.Memory),
		RuntimePlatform:         &types.RuntimePlatform{CpuArchitecture: types.CPUArchitectureArm64, OperatingSystemFamily: types.OSFamilyLinux},
		ExecutionRoleArn:        aws.String(l.cfg.ExecutionRoleARN),
		TaskRoleArn:             aws.String(l.cfg.TaskRoleARN),
		Volumes: []types.Volume{
			{Name: aws.String(volumeWorkspace), EfsVolumeConfiguration: &types.EFSVolumeConfiguration{
				FileSystemId:        aws.String(l.cfg.FileSystemID),
				TransitEncryption:   types.EFSTransitEncryptionEnabled,
				AuthorizationConfig: &types.EFSAuthorizationConfig{AccessPointId: aws.String(accessPoint), Iam: types.EFSAuthorizationConfigIAMEnabled},
			}},
			{Name: aws.String(volumeRun)},
		},
		ContainerDefinitions: []types.ContainerDefinition{
			{
				Name: aws.String(containerFetch), Image: aws.String(l.cfg.ToolImage), Command: []string{"fetch"}, User: aws.String("0"),
				Essential: aws.Bool(false),
				Environment: []types.KeyValuePair{
					{Name: aws.String("AGENTD_HANDOFF_URL"), Value: aws.String(l.cfg.HandoffURL)},
					{Name: aws.String("AGENTD_HANDOFF_CA"), Value: aws.String(l.cfg.HandoffCA)},
					{Name: aws.String("AGENTD_HANDOFF_WAIT"), Value: aws.String(l.cfg.HandoffWait.String())},
				},
				MountPoints:      []types.MountPoint{run},
				LogConfiguration: l.logs(containerFetch),
			},
			{
				Name: aws.String(containerWorker), Image: aws.String(l.cfg.SandboxImage), Essential: aws.Bool(true),
				// ECS stops containers in reverse of their dependencies, so the sidecar, started
				// before the worker, stops after it and never stops the item under a live worker.
				DependsOn: []types.ContainerDependency{
					{ContainerName: aws.String(containerFetch), Condition: types.ContainerConditionSuccess},
					{ContainerName: aws.String(containerSidecar), Condition: types.ContainerConditionStart},
				},
				// Fargate's limits for both: the handoff wait must end inside the 120 s start
				// timeout, and ant gets 60 s after SIGTERM to post its result and stop its item.
				StartTimeout:     aws.Int32(120),
				StopTimeout:      aws.Int32(60),
				MountPoints:      []types.MountPoint{workspace, run},
				LogConfiguration: l.logs(containerWorker),
			},
			{
				Name: aws.String(containerSidecar), Image: aws.String(l.cfg.ToolImage), Command: []string{"sidecar"}, User: aws.String("0"),
				Essential:        aws.Bool(false),
				StopTimeout:      aws.Int32(30),
				MountPoints:      []types.MountPoint{run},
				LogConfiguration: l.logs(containerSidecar),
			},
		},
	}
}

func (l *Launcher) logs(container string) *types.LogConfiguration {
	return &types.LogConfiguration{LogDriver: types.LogDriverAwslogs, Options: map[string]string{
		"awslogs-group": l.cfg.LogGroup, "awslogs-region": l.cfg.Region, "awslogs-stream-prefix": container,
	}}
}
