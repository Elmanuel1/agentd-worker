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
	StateStopping = "stopping"
	StateStopped  = "stopped"
)

// Event types the reaper reads.
const (
	ToolUse           = "agent.tool_use"
	ToolResult        = "user.tool_result"
	Confirmation      = "user.tool_confirmation"
	Interrupt         = "user.interrupt"
	ThreadTerminated  = "session.thread_status_terminated"
	SessionTerminated = "session.status_terminated"
	SessionDeleted    = "session.deleted"
)

// EventTypes are the event types owed reads, all a session's log needs to give it.
func EventTypes() []string {
	return []string{ToolUse, ToolResult, Confirmation, Interrupt, ThreadTerminated, SessionTerminated, SessionDeleted}
}

// Item is one work item of the environment.
type Item struct {
	Work    ids.WorkID
	Session ids.SessionID
	State   string
	// Beat is the latest heartbeat, or the ack before any heartbeat; zero when there is neither.
	Beat      time.Time
	StoppedAt time.Time
}

// StaleAt reports whether, at now, a worker holds the item and has not beaten for longer than after.
// A stopping item waits for its worker's next heartbeat, so a dead worker leaves it stopping.
func (i Item) StaleAt(now time.Time, after time.Duration) bool {
	live := i.State == StateActive || i.State == StateStarting || i.State == StateStopping
	return live && !i.Beat.IsZero() && now.Sub(i.Beat) > after
}

// Event is one session event, with the fields the SDK's tool runner reads to decide what it owes.
type Event struct {
	Type, ID, ToolUseID string
	// Permission is a tool use's evaluated_permission: allow, ask or deny.
	Permission string
	Thread     string
	At         time.Time
	// Verdict is a tool confirmation's result: allow or deny.
	Verdict string
}

// Items is the environment's work items.
type Items interface {
	// List returns one page; next is empty on the last.
	List(ctx context.Context, cursor string) (items []Item, next string, err error)
	Stop(ctx context.Context, work ids.WorkID) error
}

// Events is the sessions' event logs.
type Events interface {
	// List returns the session's events, oldest first.
	List(ctx context.Context, session ids.SessionID) ([]Event, error)
	// Send posts an error user.tool_result for each call.
	Send(ctx context.Context, session ids.SessionID, toolUseIDs []string) error
}
