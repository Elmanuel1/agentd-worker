package reap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// Config holds the reaper's settings.
type Config struct {
	// Interval is the time between passes.
	Interval time.Duration
	// StaleAfter is how long an item may go without a heartbeat: the 300 s lease plus 60 s.
	StaleAfter time.Duration
}

// lookback is how far before a start its first pass answers stopped items. A start keeps no record
// of earlier passes, so a failed answer is retried after a restart within this, and the first pass
// reads no event log of an item stopped longer ago.
const lookback = 24 * time.Hour

// Reaper runs passes over the environment's items. It is not safe for concurrent passes.
type Reaper struct {
	cfg      Config
	items    Items
	events   Events
	log      *slog.Logger
	reaped   metric.Int64Counter
	results  metric.Int64Counter
	lastPass time.Time
	// retry holds sessions whose answer failed, so a later pass answers them though they stopped earlier.
	retry map[ids.SessionID]bool
}

func New(cfg Config, items Items, events Events, meter metric.Meter, log *slog.Logger) (*Reaper, error) {
	if cfg.Interval <= 0 || cfg.StaleAfter <= 0 {
		return nil, errors.New("reap interval and stale-after must be positive")
	}
	reaped, err1 := meter.Int64Counter("reaped", metric.WithDescription("stale items stopped"))
	results, err2 := meter.Int64Counter("error_results", metric.WithDescription("error results posted for owed calls"))
	if err := errors.Join(err1, err2); err != nil {
		return nil, err
	}
	return &Reaper{
		cfg: cfg, items: items, events: events, log: log, reaped: reaped, results: results,
		lastPass: time.Now().Add(-lookback), retry: map[ids.SessionID]bool{},
	}, nil
}

// Run passes at once and then every Interval until ctx ends.
func (r *Reaper) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		r.Pass(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Pass reads every page of items and reaps each. A failed page leaves the pass for the next one.
func (r *Reaper) Pass(ctx context.Context) {
	r.passAt(ctx, time.Now())
}

func (r *Reaper) passAt(ctx context.Context, now time.Time) {
	cursor := ""
	for {
		items, next, err := r.items.List(ctx, cursor)
		if err != nil {
			r.log.Warn("listing work items failed", "error", err)
			return
		}
		for _, it := range items {
			r.reap(ctx, now, it)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	r.lastPass = now
}

// reap runs one item from Stale or Stopped to Answered.
func (r *Reaper) reap(ctx context.Context, now time.Time, it Item) {
	var stopped Stopped
	switch {
	case it.StaleAt(now, r.cfg.StaleAfter):
		s, err := Stale{item: it}.Stop(ctx, r.items)
		if err != nil {
			r.fail(it, err)
			return
		}
		r.reaped.Add(ctx, 1)
		stopped = s
	// agentd's clock stamps StoppedAt; an interval of overlap absorbs its lag behind this one.
	case it.State == StateStopped && (it.StoppedAt.After(r.lastPass.Add(-r.cfg.Interval)) || r.retry[it.Session]):
		stopped = Stopped{item: it}
	default:
		return
	}
	answered, err := stopped.Answer(ctx, r.events)
	if err != nil {
		r.fail(it, err)
		return
	}
	delete(r.retry, it.Session)
	if answered.Calls() > 0 || answered.Reaped() {
		r.results.Add(ctx, int64(answered.Calls()))
		r.log.Info("answered a dead worker's item", "item_id", it.Session, "last_heartbeat", it.Beat, "calls_answered", answered.Calls())
	}
}

func (r *Reaper) fail(it Item, err error) {
	r.retry[it.Session] = true
	r.log.Warn("reaping an item failed; retrying next pass", "item_id", it.Session, "error", err)
}
