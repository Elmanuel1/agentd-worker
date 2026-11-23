package fargate

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ecs"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/launcher"
	"github.com/Elmanuel1/agentd-worker/internal/launcher/launchertest"
)

var cfg = Config{
	Cluster: "agentd", Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}, FileSystemID: "fs-1",
	RootMount: "/mnt/efs", ExecutionRoleARN: "arn:aws:iam::1:role/exec", TaskRoleARN: "arn:aws:iam::1:role/none",
	CPU: "512", Memory: "1024",
	LogGroup: "/agentd/sandboxes", Region: "us-east-1", WorkerUID: 1001, WorkerGID: 1000,
	Sandbox: launcher.Sandbox{SandboxImage: "sandbox@sha256:1", ToolImage: "agentd-worker@sha256:2", HandoffURL: "https://controller:8443", HandoffCA: "PEM", HandoffWait: 90 * time.Second},
}

// open builds a launcher on a file system root of its own, closed when the test ends.
func open(t *testing.T, ecsClient ECS, efsClient EFS, cfg Config) *Launcher {
	t.Helper()
	cfg.RootMount = t.TempDir()
	l, err := New(ecsClient, efsClient, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l
}

// TestNewRefusesAWaitFargateCannotKeep holds SC-1's limit on Fargate: the handoff wait ends inside
// the start timeout.
func TestNewRefusesAWaitFargateCannotKeep(t *testing.T) {
	over := cfg
	over.HandoffWait, over.RootMount = MaxHandoffWait+time.Second, t.TempDir()
	if _, err := New(newFake(), newFake(), over); err == nil {
		t.Errorf("a handoff wait of %s was taken", over.HandoffWait)
	}
}

// TestSuite holds FG-1 to FG-4 against the shared contract.
func TestSuite(t *testing.T) {
	launchertest.Run(t, func(t *testing.T) (launchertest.Subject, launchertest.Count) {
		f := newFake()
		return open(t, f, f, cfg), f.running
	})
}

// TestLaunchAndRelease holds FG-1 and FG-4's specifics.
func TestLaunchAndRelease(t *testing.T) {
	f := newFake()
	l := open(t, f, f, cfg)
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
	if err := open(t, f, f, changed).Launch(ctx, "sesn_1", "n3"); err != nil {
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
	if aws.ToString(run.StartedBy) != "sesn_1" || !strings.HasPrefix(aws.ToString(run.TaskDefinition), "arn:aws:ecs:r:1:task-definition/agentd-sandbox-sesn_1:") || aws.ToString(run.ClientToken) != "n3" || aws.ToString(env.Name) != "fetch" || aws.ToString(env.Environment[0].Value) != "n3" {
		t.Errorf("RunTask %+v", run)
	}
	dir := filepath.Join(l.cfg.RootMount, "sessions", "sesn_1")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(ctx, "sesn_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); len(f.points) != 0 || len(f.activeDefs()) != 0 || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after release: points %v, defs %v, directory %v", f.points, f.activeDefs(), err)
	}
}

// TestRelaunchUnderOneNonce holds FG-1's repeated Launch: it keeps a task ECS does not return yet,
// as Stop does, and fails when the nonce's task stopped, or ECS dropped it past its retention, or
// ECS's replay of it answers, since ECS would only replay it.
func TestRelaunchUnderOneNonce(t *testing.T) {
	hide := func(f *fakeAWS) ECS { return &hidden{unlisted{&lagging{fakeAWS: f, seen: map[string]bool{}}}, -1} }
	cases := []struct {
		name    string
		ecs     func(*fakeAWS) ECS
		stopped bool
		later   time.Duration
		// restart relaunches from a launcher with no record of the first, so ECS's replay answers.
		restart bool
		failed  bool
	}{
		{"a task ECS does not return yet is this launch", hide, false, 0, false, false},
		{"a task ECS dropped past its retention cannot run again", hide, true, 2 * taskRetention, false, true},
		{"a stopped task cannot run again", func(f *fakeAWS) ECS { return f }, true, 0, false, true},
		{"a replay of a stopped task is no launch", func(f *fakeAWS) ECS { return f }, true, 0, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFake()
				l := open(t, c.ecs(f), f, cfg)
				if err := l.Launch(t.Context(), "sesn_1", "n1"); err != nil {
					t.Fatal(err)
				}
				if c.stopped {
					f.StopTask(t.Context(), &ecs.StopTaskInput{Task: aws.String(l.tasks["sesn_1"].arn)})
				}
				time.Sleep(c.later)
				if c.restart {
					l = open(t, c.ecs(f), f, cfg)
				}
				if err := l.Launch(t.Context(), "sesn_1", "n1"); (err != nil) != c.failed {
					t.Errorf("repeated launch: %v", err)
				}
			})
		})
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
	l := open(t, client, nil, cfg)
	if _, err := client.RegisterTaskDefinition(t.Context(), l.taskDefinition("sesn_1", "fsap-1")); err != nil {
		t.Fatal(err)
	}
	var def struct {
		PidMode         *string
		RuntimePlatform struct{ CpuArchitecture string }
		Containers      []struct {
			Name         string
			Essential    bool
			Secrets      []any
			Command      []string
			StartTimeout int
			DependsOn    []struct{ ContainerName, Condition string }
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
	// ECS applies a start timeout to the container waited on, so fetch's bounds the handoff.
	if fetch, worker := def.Containers[0], def.Containers[1]; fetch.StartTimeout != 120 || worker.StartTimeout != 0 {
		t.Errorf("start timeouts: fetch %d, worker %d; want 120 on fetch only", fetch.StartTimeout, worker.StartTimeout)
	}
	if cmd := def.Containers[1].Command; !slices.Equal(cmd, []string{"cma-sandbox-worker"}) {
		t.Errorf("worker command %q, want Anthropic's worker script, as the chart runs it", cmd)
	}
	// The worker starts once fetch succeeds and after the sidecar, so ECS stops the sidecar after it (SD-1).
	want := []struct{ ContainerName, Condition string }{{"fetch", "SUCCESS"}, {"sidecar", "START"}}
	if dep := def.Containers[1].DependsOn; !slices.Equal(dep, want) {
		t.Errorf("worker depends on %+v, want %+v", dep, want)
	}
}
