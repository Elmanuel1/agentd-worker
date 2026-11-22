package dispatch

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// TestReleaseEndedSessions holds SC-9: a deleted or archived session is released, and with a
// retention set, one idle past it with no running sandbox; any other, or one agentd cannot
// answer for, keeps its storage.
func TestReleaseEndedSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old := time.Now().Add(-48 * time.Hour)
		sessions := fakeSessions{
			"sesn_deleted":      Session{Gone: true},
			"sesn_archived":     Session{Terminated: true},
			"sesn_idle_old":     Session{Idle: true, Since: old},
			"sesn_idle_running": Session{Idle: true, Since: old},
			"sesn_idle_new":     Session{Idle: true, Since: time.Now()},
			"sesn_live":         Session{},
			"sesn_err":          context.DeadlineExceeded,
		}
		withRetention := cfg
		withRetention.Retention = 24 * time.Hour
		r := newRig(t, withRetention, sessions)
		for s := range sessions {
			r.backend.running[s] = s == "sesn_idle_running"
		}
		ctx, cancel := context.WithCancel(t.Context())
		go r.ctrl.Release(ctx)
		time.Sleep(cfg.ReleaseInterval - time.Second)
		synctest.Wait()
		if _, _, released := r.backend.calls(); len(released) != 0 {
			t.Fatalf("released %v before the interval", released)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		cancel()
		_, _, released := r.backend.calls()
		slices.Sort(released)
		if want := []ids.SessionID{"sesn_archived", "sesn_deleted", "sesn_idle_old"}; !slices.Equal(released, want) {
			t.Errorf("released %v, want %v", released, want)
		}
	})
}
