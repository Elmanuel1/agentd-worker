package fargate

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// TestReleaseFinishesOnRetry holds FG-4 when a release fails part way: the session keeps its access
// point, so List still finds it and the next release finishes.
func TestReleaseFinishesOnRetry(t *testing.T) {
	f := newFake()
	l := open(t, f, f, cfg)
	if err := l.Launch(t.Context(), "sesn_1", "n1"); err != nil {
		t.Fatal(err)
	}
	// A directory the controller cannot write keeps the session's own from being removed.
	sessions := filepath.Join(l.cfg.RootMount, "sessions")
	if err := os.MkdirAll(filepath.Join(sessions, "sesn_1"), 0o750); err != nil {
		t.Fatal(err)
	}
	os.Chmod(sessions, 0o500)
	t.Cleanup(func() { os.Chmod(sessions, 0o700) })
	if err := l.Release(t.Context(), "sesn_1"); err == nil {
		t.Fatal("a failed directory removal released the session")
	}
	if listed, err := l.List(t.Context()); err != nil || len(listed) != 1 {
		t.Fatalf("after a failed release List gave %v, %v; want the session", listed, err)
	}
	os.Chmod(sessions, 0o700)
	if err := l.Release(t.Context(), "sesn_1"); err != nil || len(f.points) != 0 || len(f.activeDefs()) != 0 {
		t.Errorf("retried release: %v, points %v, defs %v", err, f.points, f.activeDefs())
	}
}

// lagging reports each stopped task as still deactivating on its first read, as ECS does while
// the task's containers shut down.
type lagging struct {
	*fakeAWS
	seen map[string]bool
}

func (f *lagging) DescribeTasks(ctx context.Context, in *ecs.DescribeTasksInput, _ ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	out, err := f.fakeAWS.DescribeTasks(ctx, in)
	for i, t := range out.Tasks {
		if arn := aws.ToString(t.TaskArn); aws.ToString(t.LastStatus) == lastStopped && !f.seen[arn] {
			f.seen[arn] = true
			out.Tasks[i].LastStatus = aws.String("DEACTIVATING")
		}
	}
	return out, err
}

// TestStopWaitsUntilStopped holds that Stop returns once the session's tasks have stopped, not when
// ECS takes the stop, so a next Launch never shares the workspace with a worker still shutting
// down; after a controller restart too, for a task told to stop before it.
func TestStopWaitsUntilStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &lagging{fakeAWS: newFake(), seen: map[string]bool{}}
		l := open(t, f, f, cfg)
		for _, n := range []ids.Nonce{"n1", "n2"} {
			if err := l.Launch(t.Context(), "sesn_1", n); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now()
		if err := l.Stop(t.Context(), "sesn_1"); err != nil || time.Since(start) != 5*time.Second {
			t.Errorf("Stop: %v after %s, want a deactivating read and a stopped one 5 s later", err, time.Since(start))
		}
		if err := l.Launch(t.Context(), "sesn_1", "n3"); err != nil {
			t.Fatal(err)
		}
		for _, arn := range slices.Collect(maps.Keys(f.tasks)) {
			f.StopTask(t.Context(), &ecs.StopTaskInput{Task: aws.String(arn)})
		}
		start = time.Now()
		if err := open(t, f, f, cfg).Stop(t.Context(), "sesn_1"); err != nil || time.Since(start) != 5*time.Second {
			t.Errorf("Stop after a restart: %v after %s, want it to wait for the task told to stop", err, time.Since(start))
		}
	})
}

// unlisted is ECS before ListTasks catches up: it lists no task, and a stopped task still reads
// as deactivating once.
type unlisted struct{ *lagging }

func (unlisted) ListTasks(context.Context, *ecs.ListTasksInput, ...func(*ecs.Options)) (*ecs.ListTasksOutput, error) {
	return &ecs.ListTasksOutput{}, nil
}

// hidden leaves every task out of its first reads, as DescribeTasks does just after RunTask; with
// reads at -1 it never returns one, as after ECS drops a stopped task.
type hidden struct {
	unlisted
	reads int
}

func (h *hidden) DescribeTasks(ctx context.Context, in *ecs.DescribeTasksInput, opts ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	out, err := h.unlisted.DescribeTasks(ctx, in, opts...)
	if h.reads != 0 {
		h.reads = max(h.reads-1, -1)
		out.Tasks = nil
	}
	return out, err
}

// refusing fails every StopTask.
type refusing struct{ unlisted }

func (refusing) StopTask(context.Context, *ecs.StopTaskInput, ...func(*ecs.Options)) (*ecs.StopTaskOutput, error) {
	return nil, errors.New("throttled")
}

// TestStopsATaskNotYetListed holds FG-3 while ECS lags: the task the last Launch ran is stopped by
// its ARN and waited for, one DescribeTasks does not return yet is waited for, and Stop forgets
// it only once it has stopped or ECS has dropped it.
func TestStopsATaskNotYetListed(t *testing.T) {
	cases := []struct {
		name    string
		ecs     func(*lagging) ECS
		failed  bool
		running int
		known   bool
		took    time.Duration
		// age is how long before the stop the task was launched.
		age time.Duration
	}{
		{"a task the list lags behind is stopped and waited for", func(f *lagging) ECS { return unlisted{f} }, false, 0, false, 5 * time.Second, 0},
		{"a refused stop keeps the task for the retry", func(f *lagging) ECS { return refusing{unlisted{f}} }, true, 1, true, 0, 0},
		{"a task ECS does not return yet is stopped once it does", func(f *lagging) ECS { return &hidden{unlisted{f}, 1} }, false, 0, false, 10 * time.Second, 0},
		{"a task ECS does not return just after its launch is waited for", func(f *lagging) ECS { return &hidden{unlisted{f}, -1} }, false, 1, false, missingGrace, 0},
		{"a task ECS dropped long after its launch is gone at once", func(f *lagging) ECS { return &hidden{unlisted{f}, -1} }, false, 1, false, 0, time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &lagging{fakeAWS: newFake(), seen: map[string]bool{}}
				l := open(t, c.ecs(f), f, cfg)
				if err := l.Launch(t.Context(), "sesn_1", "n1"); err != nil {
					t.Fatal(err)
				}
				l.tasks["sesn_1"] = launched{l.tasks["sesn_1"].arn, time.Now().Add(-c.age)}
				start := time.Now()
				err := l.Stop(t.Context(), "sesn_1")
				n, _ := f.running(t.Context(), "sesn_1")
				if (err != nil) != c.failed || n != c.running || (l.tasks["sesn_1"].arn != "") != c.known || time.Since(start) != c.took {
					t.Errorf("Stop: %v after %s, %d running, known %q", err, time.Since(start), n, l.tasks["sesn_1"].arn)
				}
			})
		})
	}
}
