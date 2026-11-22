// Package launchertest is the contract every Launcher adapter passes: launches, lists, stops and
// releases that are safe to repeat, and an address only while the sandbox runs.
package launchertest

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// Subject is an adapter under test.
type Subject interface {
	dispatch.Launcher
	dispatch.Locator
}

// Count reports how many sandboxes the backend holds for the session, read from the backend
// itself, so a second sandbox the adapter's List would fold into one still shows.
type Count func(ctx context.Context, s ids.SessionID) (int, error)

// Run runs the suite against the adapters newSubject builds, one per case.
func Run(t *testing.T, newSubject func(t *testing.T) (Subject, Count)) {
	cases := []struct {
		name string
		run  func(t *testing.T, ctx context.Context, l Subject, count Count, s ids.SessionID)
	}{
		{"launch runs the sandbox", launchRuns},
		{"launch twice keeps one sandbox", launchTwice},
		{"stop keeps the session's volume", stopKeeps},
		{"release forgets the session", releaseForgets},
		{"calls on an unknown session are safe", unknownSafe},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			l, count := newSubject(t)
			s := ids.SessionID(fmt.Sprintf("sesn_lt%d_%d", time.Now().UnixNano()%1e9, i))
			t.Cleanup(func() { l.Release(context.WithoutCancel(ctx), s) })
			c.run(t, ctx, l, count, s)
		})
	}
}

func launchRuns(t *testing.T, ctx context.Context, l Subject, _ Count, s ids.SessionID) {
	must(t, l.Launch(ctx, s, ids.NewNonce()))
	addr := waitRunning(t, ctx, l, s, true)
	if !addr.IsValid() {
		t.Error("a running sandbox has no address")
	}
	if got := listed(t, ctx, l, s); got == nil || !got.Running {
		t.Errorf("List shows %+v for a running sandbox", got)
	}
}

func launchTwice(t *testing.T, ctx context.Context, l Subject, count Count, s ids.SessionID) {
	must(t, l.Launch(ctx, s, ids.NewNonce()))
	must(t, l.Launch(ctx, s, ids.NewNonce()))
	waitRunning(t, ctx, l, s, true)
	n, err := count(ctx, s)
	must(t, err)
	if n != 1 {
		t.Errorf("the backend holds %d sandboxes for the session, want 1", n)
	}
	all, err := l.List(ctx)
	must(t, err)
	if n := len(slices.DeleteFunc(all, func(sb dispatch.Sandbox) bool { return sb.Session != s })); n != 1 {
		t.Errorf("List shows the session %d times", n)
	}
}

func stopKeeps(t *testing.T, ctx context.Context, l Subject, _ Count, s ids.SessionID) {
	must(t, l.Launch(ctx, s, ids.NewNonce()))
	waitRunning(t, ctx, l, s, true)
	must(t, l.Stop(ctx, s))
	must(t, l.Stop(ctx, s))
	waitRunning(t, ctx, l, s, false)
	if got := listed(t, ctx, l, s); got == nil || got.Running {
		t.Errorf("List shows %+v for a stopped sandbox, want it kept and not running", got)
	}
}

func releaseForgets(t *testing.T, ctx context.Context, l Subject, _ Count, s ids.SessionID) {
	must(t, l.Launch(ctx, s, ids.NewNonce()))
	waitRunning(t, ctx, l, s, true)
	must(t, l.Release(ctx, s))
	must(t, l.Release(ctx, s))
	if got := listed(t, ctx, l, s); got != nil {
		t.Errorf("List shows %+v after release", got)
	}
}

func unknownSafe(t *testing.T, ctx context.Context, l Subject, _ Count, s ids.SessionID) {
	must(t, l.Stop(ctx, s))
	must(t, l.Release(ctx, s))
	if _, running, err := l.Address(ctx, s); err != nil || running {
		t.Errorf("Address of an unknown session: running %v, %v", running, err)
	}
}
