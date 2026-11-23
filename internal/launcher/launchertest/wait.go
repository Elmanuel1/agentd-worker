package launchertest

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// waitRunning polls Address until the sandbox's running state is want, as a backend's start and
// stop take a moment to show.
func waitRunning(t *testing.T, ctx context.Context, l Subject, s ids.SessionID, want bool) netip.Addr {
	t.Helper()
	for {
		addr, running, err := l.Address(ctx, s)
		must(t, err)
		if running == want {
			return addr
		}
		select {
		case <-ctx.Done():
			t.Fatalf("sandbox running stayed %v", running)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func listed(t *testing.T, ctx context.Context, l Subject, s ids.SessionID) *dispatch.Sandbox {
	t.Helper()
	all, err := l.List(ctx)
	must(t, err)
	for _, sb := range all {
		if sb.Session == s {
			return &sb
		}
	}
	return nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
