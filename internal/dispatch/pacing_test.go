package dispatch

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// TestFailedPollsBackOff holds the dispatcher's pacing: after each failure in a row the next poll
// waits between half and all of 2 s, 4 s, then 8 s.
func TestFailedPollsBackOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, cfg, nil)
		r.queue.failPolls = 3
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go r.ctrl.Run(ctx)
		// Poll n+1 comes after the sum of the waits before it: in [1 s, 2 s), [3 s, 6 s), [7 s, 14 s).
		start := time.Now()
		for _, want := range []struct {
			at    time.Duration
			polls int
		}{{900 * time.Millisecond, 1}, {2900 * time.Millisecond, 2}, {6900 * time.Millisecond, 3}} {
			time.Sleep(want.at - time.Since(start))
			synctest.Wait()
			if polls, _ := r.queue.state(); polls != want.polls {
				t.Errorf("after %s: %d polls, want %d", want.at, polls, want.polls)
			}
		}
		time.Sleep(14100*time.Millisecond - time.Since(start))
		synctest.Wait()
		if polls, _ := r.queue.state(); polls < 4 {
			t.Errorf("after 14.1s: %d polls, want the fourth", polls)
		}
	})
}
