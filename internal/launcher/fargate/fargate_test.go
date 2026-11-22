package fargate

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ecs"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/launcher/launchertest"
)

var cfg = Config{
	Cluster: "agentd", Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}, FileSystemID: "fs-1",
	RootMount: "/mnt/efs", ExecutionRoleARN: "arn:aws:iam::1:role/exec", TaskRoleARN: "arn:aws:iam::1:role/none",
	SandboxImage: "sandbox@sha256:1", ToolImage: "agentd-worker@sha256:2", CPU: "512", Memory: "1024",
	LogGroup: "/agentd/sandboxes", Region: "us-east-1", HandoffURL: "https://controller:8443", HandoffCA: "PEM", HandoffWait: 2 * time.Minute,
}

// TestSuite holds FG-1 to FG-4 against the shared contract.
func TestSuite(t *testing.T) {
	launchertest.Run(t, func(*testing.T) (launchertest.Subject, launchertest.Count) {
		f := newFake()
		return New(f, f, f, cfg), f.running
	})
}

// TestLaunchAndRelease holds FG-1 and FG-4's specifics.
func TestLaunchAndRelease(t *testing.T) {
	f := newFake()
	l := New(f, f, f, cfg)
	ctx := t.Context()
	for _, n := range []ids.Nonce{"n1", "n2"} {
		if err := l.Launch(ctx, "sesn_1", n); err != nil {
			t.Fatal(err)
		}
	}
	if defs := f.activeDefs(); len(defs) != 1 {
		t.Errorf("registered %v, want one family revision", defs)
	}
	changed := cfg
	changed.SandboxImage = "sandbox@sha256:3"
	if err := New(f, f, f, changed).Launch(ctx, "sesn_1", "n3"); err != nil {
		t.Fatal(err)
	}
	if defs := f.activeDefs(); len(defs) != 2 {
		t.Errorf("a new image registered %v, want a second revision", defs)
	}
	ap := f.points["fsap-sesn_1"]
	if aws.ToString(ap.Tags[0].Value) != "sesn_1" || aws.ToString(ap.RootDirectory.Path) != "/sessions/sesn_1" {
		t.Errorf("access point %+v", ap)
	}
	run := f.runs[2]
	env := run.Overrides.ContainerOverrides[0]
	if aws.ToString(run.StartedBy) != "sesn_1" || !strings.HasPrefix(aws.ToString(run.TaskDefinition), "arn:aws:ecs:r:1:task-definition/agentd-sesn_1:") || aws.ToString(run.ClientToken) != "n3" || aws.ToString(env.Name) != "fetch" || aws.ToString(env.Environment[0].Value) != "n3" {
		t.Errorf("RunTask %+v", run)
	}
	if err := l.Release(ctx, "sesn_1"); err != nil {
		t.Fatal(err)
	}
	if len(f.points) != 0 || len(f.activeDefs()) != 0 || !slices.Equal(f.removed, []string{"/mnt/efs/sessions/sesn_1"}) {
		t.Errorf("after release: points %v, defs %v, removed %v", f.points, f.activeDefs(), f.removed)
	}
}

// TestTaskDefinition holds FG-5 and FG-8. ECS publishes no JSON schema, so the definition goes
// through the SDK's own validation and serializer, and the wire JSON is checked.
func TestTaskDefinition(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	client := ecs.New(ecs.Options{Region: "us-east-1", BaseEndpoint: aws.String(srv.URL), Credentials: credentials.NewStaticCredentialsProvider("a", "b", "")})
	l := New(client, nil, nil, cfg)
	if _, err := client.RegisterTaskDefinition(t.Context(), l.taskDefinition("sesn_1", "fsap-1")); err != nil {
		t.Fatal(err)
	}
	var def struct {
		PidMode         *string
		RuntimePlatform struct{ CpuArchitecture string }
		Containers      []struct {
			Name      string
			Essential bool
			Secrets   []any
			DependsOn []struct{ ContainerName, Condition string }
		} `json:"containerDefinitions"`
	}
	if err := json.Unmarshal(body, &def); err != nil {
		t.Fatal(err)
	}
	if def.PidMode != nil || def.RuntimePlatform.CpuArchitecture != "ARM64" || len(def.Containers) != 3 {
		t.Fatalf("definition %s", body)
	}
	for _, c := range def.Containers {
		if c.Secrets != nil {
			t.Errorf("%s has secrets", c.Name)
		}
		if c.Essential != (c.Name == containerWorker) {
			t.Errorf("%s essential %v", c.Name, c.Essential)
		}
	}
	if dep := def.Containers[1].DependsOn; len(dep) != 1 || dep[0].ContainerName != "fetch" || dep[0].Condition != "SUCCESS" {
		t.Errorf("worker depends on %+v", dep)
	}
}
