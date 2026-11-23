package docker

import (
	"sync"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// gate keeps a worker's start apart from Stop of its session: a worker either started before
// Stop read the session's containers, and gets its graceful stop, or never starts.
type gate struct {
	mu       sync.Mutex
	stopping map[ids.SessionID]int
	// starting closes when the session's worker has started, or failed to.
	starting map[ids.SessionID]chan struct{}
}

func newGate() *gate {
	return &gate{stopping: map[ids.SessionID]int{}, starting: map[ids.SessionID]chan struct{}{}}
}

// start reports whether the session's worker may start; when it may, the caller calls started.
func (g *gate) start(session ids.SessionID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping[session] > 0 {
		return false
	}
	g.starting[session] = make(chan struct{})
	return true
}

func (g *gate) started(session ids.SessionID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.starting[session])
	delete(g.starting, session)
}

// stop waits out a start under way and holds starts off until the func it returns runs.
func (g *gate) stop(session ids.SessionID) func() {
	g.mu.Lock()
	g.stopping[session]++
	for ch := g.starting[session]; ch != nil; ch = g.starting[session] {
		g.mu.Unlock()
		<-ch
		g.mu.Lock()
	}
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.stopping[session]--
		if g.stopping[session] == 0 {
			delete(g.stopping, session)
		}
	}
}
