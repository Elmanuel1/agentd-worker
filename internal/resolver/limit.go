package resolver

import (
	"log/slog"
	"sync"
	"time"
)

// inFlight bounds the queries relayed at once, so one sandbox flooding the relay costs the others
// only dropped queries, which their resolvers retry, and never the relay's memory.
const inFlight = 256

// limit hands out slots for queries and counts the ones it drops, warning at most once a minute.
type limit struct {
	slots   chan struct{}
	log     *slog.Logger
	mu      sync.Mutex
	dropped int
	warned  time.Time
}

func newLimit(n int, log *slog.Logger) *limit {
	return &limit{slots: make(chan struct{}, n), log: log}
}

// take reserves a slot, or drops the query when every slot is taken.
func (l *limit) take() bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropped++
	if time.Since(l.warned) < time.Minute {
		return false
	}
	l.log.Warn("dns relay full; dropping queries", "dropped", l.dropped, "in_flight", cap(l.slots))
	l.dropped, l.warned = 0, time.Now()
	return false
}

func (l *limit) release() { <-l.slots }
