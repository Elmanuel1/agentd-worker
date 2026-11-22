// Package dispatch is the controller: it polls the queue, launches a sandbox per item through a
// Launcher, hands the item over through the handoff store, acks it once collected, and releases
// the sandboxes of sessions that are gone.
package dispatch

import (
	"context"
	"net/netip"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// Queue is the environment's work queue.
type Queue interface {
	// Poll returns ok false when no item arrived within the server's block.
	Poll(ctx context.Context, reclaimAfter time.Duration) (session ids.SessionID, secret ids.Secret, ok bool, err error)
	Ack(ctx context.Context, session ids.SessionID) error
	Depth(ctx context.Context) (int64, error)
}

// Sessions tells a live session from one agentd no longer has.
type Sessions interface {
	// Exists is false only on agentd's own 404 for the session.
	Exists(ctx context.Context, session ids.SessionID) (bool, error)
}

// Launcher runs sandboxes on one backend. Every call is safe to repeat.
type Launcher interface {
	Launch(ctx context.Context, session ids.SessionID, nonce ids.Nonce) error
	// List returns each session the backend holds a sandbox or volume for.
	List(ctx context.Context) ([]Sandbox, error)
	Stop(ctx context.Context, session ids.SessionID) error
	// Release stops the sandbox and deletes what the backend keeps for the session.
	Release(ctx context.Context, session ids.SessionID) error
}

// Locator finds a session's running sandbox.
type Locator interface {
	Address(ctx context.Context, session ids.SessionID) (addr netip.Addr, running bool, err error)
}

// Sandbox is what a backend holds for one session.
type Sandbox struct {
	Session ids.SessionID
	Running bool
}
