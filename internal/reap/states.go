package reap

import (
	"context"
	"fmt"
	"slices"
)

// Stale is an active or starting item whose worker stopped beating.
type Stale struct {
	item Item
}

// Stop stops the item, so the session's next tool call queues it again.
func (s Stale) Stop(ctx context.Context, items Items) (Stopped, error) {
	if err := items.Stop(ctx, s.item.Session); err != nil {
		return Stopped{}, fmt.Errorf("stopping item %s: %w", s.item.Session, err)
	}
	return Stopped{item: s.item, reaped: true}, nil
}

// Stopped is an item no worker holds, whose owed calls may still be unanswered. reaped is true
// when this pass stopped it.
type Stopped struct {
	item   Item
	reaped bool
}

// Answer posts an error result for each tool call the session still owes.
func (s Stopped) Answer(ctx context.Context, events Events) (Answered, error) {
	log, err := events.List(ctx, s.item.Session)
	if err != nil {
		return Answered{}, fmt.Errorf("listing events of %s: %w", s.item.Session, err)
	}
	calls := owed(log)
	if len(calls) == 0 {
		return Answered{of: s}, nil
	}
	if err := events.Send(ctx, s.item.Session, calls); err != nil {
		return Answered{}, fmt.Errorf("answering %d calls of %s: %w", len(calls), s.item.Session, err)
	}
	return Answered{of: s, calls: len(calls)}, nil
}

// Answered is a stopped item whose session owes nothing.
type Answered struct {
	of    Stopped
	calls int
}

func (a Answered) Calls() int { return a.calls }

func (a Answered) Reaped() bool { return a.of.reaped }

// owed returns the tool calls with no result after them, in order. A session that ended owes nothing.
func owed(events []Event) []string {
	var calls []string
	for _, e := range events {
		switch e.Type {
		case ToolUse:
			calls = append(calls, e.ID)
		case ToolResult:
			calls = slices.DeleteFunc(calls, func(id string) bool { return id == e.ToolUseID })
		case SessionTerminated, SessionDeleted:
			return nil
		}
	}
	return calls
}
