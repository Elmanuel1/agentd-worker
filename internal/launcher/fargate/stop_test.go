package fargate

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// stuckDir fails its first directory removal.
type stuckDir struct {
	*fakeAWS
	failed bool
}

func (f *stuckDir) RemoveAll(p string) error {
	if !f.failed {
		f.failed = true
		return errors.New("device or resource busy")
	}
	return f.fakeAWS.RemoveAll(p)
}

// TestReleaseFinishesOnRetry holds FG-4 when a release fails part way: the session keeps its access
// point, so List still finds it and the next release finishes.
func TestReleaseFinishesOnRetry(t *testing.T) {
	f := &stuckDir{fakeAWS: newFake()}
	l := New(f, f, f, cfg)
	if err := l.Launch(t.Context(), "sesn_1", "n1"); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(t.Context(), "sesn_1"); err == nil {
		t.Fatal("a failed directory removal released the session")
	}
	if listed, err := l.List(t.Context()); err != nil || len(listed) != 1 {
		t.Fatalf("after a failed release List gave %v, %v; want the session", listed, err)
	}
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
		if arn := aws.ToString(t.TaskArn); aws.ToString(t.LastStatus) == "STOPPED" && !f.seen[arn] {
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
		l := New(f, f, f, cfg)
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
		if err := New(f, f, f, cfg).Stop(t.Context(), "sesn_1"); err != nil || time.Since(start) != 5*time.Second {
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

// refusing fails every StopTask.
type refusing struct{ unlisted }

func (refusing) StopTask(context.Context, *ecs.StopTaskInput, ...func(*ecs.Options)) (*ecs.StopTaskOutput, error) {
	return nil, errors.New("throttled")
}

// TestStopsATaskNotYetListed holds FG-3 while ListTasks lags: the task the last Launch ran is
// stopped by its ARN and waited for, and Stop forgets it only once it has stopped.
func TestStopsATaskNotYetListed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := unlisted{&lagging{fakeAWS: newFake(), seen: map[string]bool{}}}
		l := New(f, f, f, cfg)
		if err := l.Launch(t.Context(), "sesn_1", "n1"); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		err := l.Stop(t.Context(), "sesn_1")
		if n, _ := f.running(t.Context(), "sesn_1"); err != nil || n != 0 || l.tasks["sesn_1"] != "" || time.Since(start) != 5*time.Second {
			t.Errorf("Stop: %v after %s, %d running, known %q; want it stopped and waited for 5 s", err, time.Since(start), n, l.tasks["sesn_1"])
		}
		r := refusing{unlisted{&lagging{fakeAWS: newFake(), seen: map[string]bool{}}}}
		l = New(r, r, r, cfg)
		if err := l.Launch(t.Context(), "sesn_1", "n1"); err != nil {
			t.Fatal(err)
		}
		if err := l.Stop(t.Context(), "sesn_1"); err == nil || l.tasks["sesn_1"] == "" {
			t.Errorf("a failed stop: %v, known %q; want an error and the task kept for the retry", err, l.tasks["sesn_1"])
		}
	})
}
