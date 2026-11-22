package fargate

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

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
			id, err := New(f, f, f, cfg).accessPoint(t.Context(), "sesn_1")
			if (err != nil) != tc.wantErr || (err == nil && id != "fsap-sesn_1") || time.Since(start) != tc.took {
				t.Errorf("creating %d reads: got %q, %v after %s", tc.left, id, err, time.Since(start))
			}
		})
	}
}
