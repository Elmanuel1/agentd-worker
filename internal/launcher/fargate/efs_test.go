package fargate

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
)

// creating answers the first left reads of an access point as creating; -1 never stops.
type creating struct {
	*fakeAWS
	left int
}

func (c *creating) DescribeAccessPoints(ctx context.Context, in *efs.DescribeAccessPointsInput, opts ...func(*efs.Options)) (*efs.DescribeAccessPointsOutput, error) {
	out, err := c.fakeAWS.DescribeAccessPoints(ctx, in, opts...)
	if c.left == 0 {
		return out, err
	}
	c.left--
	for i := range out.AccessPoints {
		out.AccessPoints[i].LifeCycleState = efstypes.LifeCycleStateCreating
	}
	return out, err
}

// TestAccessPointWaitsUntilAvailable holds that a new access point is used once available, and
// not after 30 s of creating.
func TestAccessPointWaitsUntilAvailable(t *testing.T) {
	for _, tc := range []struct {
		left    int
		wantErr bool
		took    time.Duration
	}{{3, false, 3 * time.Second}, {-1, true, 31 * time.Second}} {
		synctest.Test(t, func(t *testing.T) {
			f := &creating{fakeAWS: newFake(), left: tc.left}
			start := time.Now()
			id, err := open(t, f, f, cfg).accessPoint(t.Context(), "sesn_1")
			if (err != nil) != tc.wantErr || (err == nil && id != "fsap-sesn_1") || time.Since(start) != tc.took {
				t.Errorf("creating %d reads: got %q, %v after %s", tc.left, id, err, time.Since(start))
			}
		})
	}
}

// TestAddressOfAStoppedTask holds that a launched task which stopped on its own, which
// DescribeTasks still returns, does not count as running.
func TestAddressOfAStoppedTask(t *testing.T) {
	f := newFake()
	l := open(t, f, f, cfg)
	if err := l.Launch(t.Context(), "sesn_1", "n1"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	for _, task := range f.tasks {
		task.DesiredStatus, task.LastStatus = aws.String(string(ecstypes.DesiredStatusStopped)), aws.String(lastStopped)
	}
	f.mu.Unlock()
	if _, running, err := l.Address(t.Context(), "sesn_1"); running || err != nil {
		t.Errorf("a stopped task: running %v, %v", running, err)
	}
}
