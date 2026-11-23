package reap

import (
	"context"
	"errors"
	"fmt"
)

// Stale is an active or starting item whose worker stopped beating.
type Stale struct {
	item Item
}

// Stop stops the item, so the session's next tool call queues it again. Its calls are those made
// by the stop agentd records: a replacement worker's come after the requeue that follows it. A
// stop agentd answers with no time is a failure, so the next pass answers it as a stopped item
// with the time agentd lists, never by this clock.
func (s Stale) Stop(ctx context.Context, items Items) (Stopped, error) {
	at, err := items.Stop(ctx, s.item.Work)
	if err == nil && at.IsZero() {
		err = errors.New("agentd recorded no stop time")
	}
	if err != nil {
		return Stopped{}, fmt.Errorf("stopping item %s: %w", s.item.Session, err)
	}
	stopped := s.item
	stopped.StoppedAt = at
	return Stopped{item: stopped, reaped: true}, nil
}

// Stopped is an item no worker holds, whose owed calls may still be unanswered. reaped is true
// when this pass stopped it.
type Stopped struct {
	item   Item
	reaped bool
}

// Answer posts an error result for each tool call the session still owes. Of a stopped item,
// only calls made by its stop count: a later one is the session's next worker's.
func (s Stopped) Answer(ctx context.Context, events Events) (Answered, error) {
	log, err := events.List(ctx, s.item.Session)
	if err != nil {
		return Answered{}, fmt.Errorf("listing events of %s: %w", s.item.Session, err)
	}
	calls := owed(log, s.item.StoppedAt)
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
