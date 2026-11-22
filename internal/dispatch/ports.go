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
	// Poll returns ok false when no session's item arrived within the server's block.
	Poll(ctx context.Context, reclaimAfter time.Duration) (work Work, ok bool, err error)
	Ack(ctx context.Context, work ids.WorkID) error
	Depth(ctx context.Context) (int64, error)
}

// Sessions reads what agentd says of a session.
type Sessions interface {
	State(ctx context.Context, session ids.SessionID) (Session, error)
}

// Session is a session's state as far as its sandbox's storage is concerned.
type Session struct {
	// Gone is agentd's own 404 for the session.
	Gone bool
	// Terminated holds for an archived session, which never runs again.
	Terminated bool
	// Idle holds while the session waits for a message; Since is when it last changed.
	Idle  bool
	Since time.Time
}

// Over reports whether the session's storage can go: it is gone or terminated, or idle with no
// running sandbox for longer than retention, when retention is set.
func (s Session) Over(running bool, now time.Time, retention time.Duration) bool {
	idleTooLong := retention > 0 && s.Idle && !running && now.Sub(s.Since) > retention
	return s.Gone || s.Terminated || idleTooLong
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

// Work is one item the queue handed out.
type Work struct {
	ID      ids.WorkID
	Session ids.SessionID
	Secret  ids.Secret
}

// Sandbox is what a backend holds for one session.
type Sandbox struct {
	Session ids.SessionID
	Running bool
}
