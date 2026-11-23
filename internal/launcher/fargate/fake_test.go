package fargate

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// fakeAWS is ECS and EFS in memory: a task runs as soon as it starts.
type fakeAWS struct {
	mu     sync.Mutex
	n      int
	tasks  map[string]*ecstypes.Task
	defs   map[string]string // ARN -> ACTIVE or INACTIVE
	tags   map[string][]ecstypes.Tag
	points map[string]efstypes.AccessPointDescription
	runs   []*ecs.RunTaskInput
}

func newFake() *fakeAWS {
	return &fakeAWS{tasks: map[string]*ecstypes.Task{}, defs: map[string]string{}, tags: map[string][]ecstypes.Tag{}, points: map[string]efstypes.AccessPointDescription{}}
}

func (f *fakeAWS) RunTask(_ context.Context, in *ecs.RunTaskInput, _ ...func(*ecs.Options)) (*ecs.RunTaskOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	arn := fmt.Sprintf("arn:aws:ecs:r:1:task/c/%d", f.n)
	f.tasks[arn] = &ecstypes.Task{TaskArn: aws.String(arn), TaskDefinitionArn: in.TaskDefinition, StartedBy: in.StartedBy, LastStatus: aws.String("RUNNING"), DesiredStatus: aws.String("RUNNING"),
		Attachments: []ecstypes.Attachment{{Details: []ecstypes.KeyValuePair{{Name: aws.String("privateIPv4Address"), Value: aws.String(fmt.Sprintf("10.0.0.%d", f.n))}}}}}
	f.runs = append(f.runs, in)
	return &ecs.RunTaskOutput{Tasks: []ecstypes.Task{*f.tasks[arn]}}, nil
}

func (f *fakeAWS) StopTask(_ context.Context, in *ecs.StopTaskInput, _ ...func(*ecs.Options)) (*ecs.StopTaskOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[aws.ToString(in.Task)]; ok {
		t.DesiredStatus, t.LastStatus = aws.String("STOPPED"), aws.String("STOPPED")
	}
	return &ecs.StopTaskOutput{}, nil
}

func (f *fakeAWS) ListTasks(_ context.Context, in *ecs.ListTasksInput, _ ...func(*ecs.Options)) (*ecs.ListTasksOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.StartedBy != nil && in.DesiredStatus != "" {
		return nil, &ecstypes.InvalidParameterException{Message: aws.String("startedBy must be the only filter")}
	}
	if in.DesiredStatus == "" {
		in.DesiredStatus = ecstypes.DesiredStatusRunning
	}
	var arns []string
	for arn, t := range f.tasks {
		family := in.Family == nil || strings.Contains(aws.ToString(t.TaskDefinitionArn), "/"+aws.ToString(in.Family)+":")
		if aws.ToString(t.DesiredStatus) == string(in.DesiredStatus) && family && (in.StartedBy == nil || aws.ToString(in.StartedBy) == aws.ToString(t.StartedBy)) {
			arns = append(arns, arn)
		}
	}
	return &ecs.ListTasksOutput{TaskArns: arns}, nil
}

func (f *fakeAWS) DescribeTasks(_ context.Context, in *ecs.DescribeTasksInput, _ ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := &ecs.DescribeTasksOutput{}
	// ECS reports a task it no longer holds as a failure, not a task.
	for _, arn := range in.Tasks {
		if t, ok := f.tasks[arn]; ok {
			out.Tasks = append(out.Tasks, *t)
		}
	}
	return out, nil
}

func (f *fakeAWS) RegisterTaskDefinition(_ context.Context, in *ecs.RegisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	arn := fmt.Sprintf("arn:aws:ecs:r:1:task-definition/%s:%d", aws.ToString(in.Family), f.n)
	f.defs[arn] = "ACTIVE"
	f.tags[arn] = in.Tags
	return &ecs.RegisterTaskDefinitionOutput{TaskDefinition: &ecstypes.TaskDefinition{TaskDefinitionArn: aws.String(arn)}}, nil
}

func (f *fakeAWS) DescribeTaskDefinition(_ context.Context, in *ecs.DescribeTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.defs[aws.ToString(in.TaskDefinition)]; ok {
		return &ecs.DescribeTaskDefinitionOutput{Tags: f.tags[aws.ToString(in.TaskDefinition)]}, nil
	}
	return nil, &ecstypes.ClientException{Message: aws.String("Unable to describe task definition.")}
}

func (f *fakeAWS) ListTaskDefinitions(_ context.Context, in *ecs.ListTaskDefinitionsInput, _ ...func(*ecs.Options)) (*ecs.ListTaskDefinitionsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := in.Status
	if want == "" {
		want = ecstypes.TaskDefinitionStatusActive
	}
	var arns []string
	for arn, status := range f.defs {
		if status == string(want) && strings.HasPrefix(path.Base(arn), aws.ToString(in.FamilyPrefix)) {
			arns = append(arns, arn)
		}
	}
	slices.Sort(arns)
	return &ecs.ListTaskDefinitionsOutput{TaskDefinitionArns: arns}, nil
}

func (f *fakeAWS) DeregisterTaskDefinition(_ context.Context, in *ecs.DeregisterTaskDefinitionInput, _ ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defs[aws.ToString(in.TaskDefinition)] = "INACTIVE"
	return &ecs.DeregisterTaskDefinitionOutput{}, nil
}

func (f *fakeAWS) DeleteTaskDefinitions(_ context.Context, in *ecs.DeleteTaskDefinitionsInput, _ ...func(*ecs.Options)) (*ecs.DeleteTaskDefinitionsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, arn := range in.TaskDefinitions {
		if f.defs[arn] != "INACTIVE" {
			return nil, &ecstypes.ClientException{Message: aws.String("only INACTIVE revisions can be deleted")}
		}
		delete(f.defs, arn)
	}
	return &ecs.DeleteTaskDefinitionsOutput{}, nil
}

func (f *fakeAWS) activeDefs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for arn := range f.defs {
		out = append(out, arn)
	}
	slices.Sort(out)
	return out
}

func (f *fakeAWS) running(_ context.Context, s ids.SessionID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.tasks {
		if aws.ToString(t.StartedBy) == string(s) && aws.ToString(t.DesiredStatus) == "RUNNING" {
			n++
		}
	}
	return n, nil
}
