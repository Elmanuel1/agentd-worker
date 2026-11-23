package docker

import (
	"context"
	"sync"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// gate keeps a worker's start apart from Stop of its session: a worker either started before
// Stop read the session's containers, and gets its graceful stop, or never starts.
type gate struct {
	mu       sync.Mutex
	stopping map[ids.SessionID]int
	// starting holds a channel per start under way, closed once that start has run.
	starting map[ids.SessionID]map[chan struct{}]bool
}

func newGate() *gate {
	return &gate{stopping: map[ids.SessionID]int{}, starting: map[ids.SessionID]map[chan struct{}]bool{}}
}

// start reports whether the session's worker may start and, when it may, the func to call once
// the start has run.
func (g *gate) start(session ids.SessionID) (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping[session] > 0 {
		return nil, false
	}
	ch := make(chan struct{})
	if g.starting[session] == nil {
		g.starting[session] = map[chan struct{}]bool{}
	}
	g.starting[session][ch] = true
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		delete(g.starting[session], ch)
		if len(g.starting[session]) == 0 {
			delete(g.starting, session)
		}
		close(ch)
	}, true
}

// stop waits out the session's starts under way, until ctx ends, and holds starts off until the
// func it returns runs.
func (g *gate) stop(ctx context.Context, session ids.SessionID) (func(), error) {
	g.mu.Lock()
	g.stopping[session]++
	release := func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.stopping[session]--
		if g.stopping[session] == 0 {
			delete(g.stopping, session)
		}
	}
	for len(g.starting[session]) > 0 {
		var ch chan struct{}
		for ch = range g.starting[session] {
			break
		}
		g.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
		g.mu.Lock()
	}
	g.mu.Unlock()
	return release, nil
}
