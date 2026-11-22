// Package reap answers for workers that died without their own shutdown: it stops a stale item,
// then posts an error result for each tool call its session still owes, as ant does on SIGTERM.
package reap

import (
	"context"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// Work item states the reaper reads.
const (
	StateStarting = "starting"
	StateActive   = "active"
	StateStopped  = "stopped"
)

// Event types the reaper reads.
const (
	ToolUse           = "agent.tool_use"
	ToolResult        = "user.tool_result"
	SessionTerminated = "session.status_terminated"
	SessionDeleted    = "session.deleted"
)

// Item is one work item of the environment. Its id is its session's.
type Item struct {
	Session ids.SessionID
	State   string
	// Beat is the latest heartbeat, or the ack before any heartbeat; zero when there is neither.
	Beat      time.Time
	StoppedAt time.Time
}

// Event is one session event.
type Event struct {
	Type      string
	ID        string
	ToolUseID string
}

// Items is the environment's work items.
type Items interface {
	// List returns one page; next is empty on the last.
	List(ctx context.Context, cursor string) (items []Item, next string, err error)
	Stop(ctx context.Context, session ids.SessionID) error
}

// Events is the sessions' event logs.
type Events interface {
	// List returns the session's events, oldest first.
	List(ctx context.Context, session ids.SessionID) ([]Event, error)
	// Send posts an error user.tool_result for each call.
	Send(ctx context.Context, session ids.SessionID, toolUseIDs []string) error
}
