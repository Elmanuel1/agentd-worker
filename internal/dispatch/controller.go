package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch/handoff"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// Config holds the controller's settings.
type Config struct {
	// HandoffWait is how long a sandbox gets to collect its item; the queue's reclaim window is twice it.
	HandoffWait time.Duration
	// MaxSandboxes bounds the items handled at once.
	MaxSandboxes int
	// ReleaseInterval is how often sandboxes of ended sessions are released.
	ReleaseInterval time.Duration
	// Retention releases a session idle with no running sandbox for longer than it; zero keeps it.
	Retention time.Duration
}

type Controller struct {
	cfg      Config
	queue    Queue
	sessions Sessions
	launcher Launcher
	locator  Locator
	store    *handoff.Store
	log      *slog.Logger
	metrics  metrics

	mu       sync.Mutex
	inflight map[ids.SessionID]bool
	// unacked holds items a sandbox collected whose ack failed, until their redelivery.
	unacked map[ids.SessionID]Collected
}

func New(cfg Config, queue Queue, sessions Sessions, launcher Launcher, locator Locator, store *handoff.Store, meter metric.Meter, log *slog.Logger) (*Controller, error) {
	if cfg.HandoffWait <= 0 || cfg.MaxSandboxes <= 0 || cfg.ReleaseInterval <= 0 {
		return nil, errors.New("handoff wait, max sandboxes and release interval must be positive")
	}
	m, err := newMetrics(meter, queue)
	if err != nil {
		return nil, err
	}
	return &Controller{
		cfg: cfg, queue: queue, sessions: sessions, launcher: launcher, locator: locator,
		store: store, log: log, metrics: m, inflight: map[ids.SessionID]bool{}, unacked: map[ids.SessionID]Collected{},
	}, nil
}

// Run polls until ctx ends, then waits for the handoffs under way; an item not yet collected is
// left for agentd to hand out again.
func (c *Controller) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	slots := make(chan struct{}, c.cfg.MaxSandboxes)
	failures := 0
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		work, ok, err := c.queue.Poll(ctx, 2*c.cfg.HandoffWait)
		if ctx.Err() != nil {
			<-slots
			return nil
		}
		// As Anthropic's dispatcher paces it: 1 to 3 s after an empty poll, and a doubling pause
		// up to 60 s after failures in a row, each with jitter.
		var wait time.Duration
		switch {
		case err != nil:
			failures++
			c.log.Warn("poll failed", "error", err, "attempt", failures)
			base := min(time.Second<<min(failures, 6), time.Minute)
			wait = base/2 + rand.N(base/2)
		case !ok:
			failures = 0
			wait = time.Second + rand.N(2*time.Second)
		}
		if wait > 0 {
			<-slots
			pause := time.NewTimer(wait)
			select {
			case <-pause.C:
			case <-ctx.Done():
				pause.Stop()
			}
			continue
		}
		failures = 0
		wg.Go(func() {
			defer func() { <-slots }()
			c.handle(ctx, Polled{session: work.Session, work: work.ID, secret: work.Secret})
		})
	}
}

// handle runs one item's handoff from Polled to Acked.
func (c *Controller) handle(ctx context.Context, polled Polled) {
	session := polled.session
	if !c.begin(session) {
		c.log.Info("item redelivered while its session is being handled; waiting", "session_id", session)
		return
	}
	defer c.end(session)
	_, running, err := c.locator.Address(ctx, session)
	if err != nil {
		c.log.Warn("finding the session's sandbox failed; leaving the item for redelivery", "session_id", session, "error", err)
		return
	}
	// A sandbox that runs while this controller holds no nonce for it, as after a restart, is
	// stopped and the item left unacked, so its next delivery launches afresh.
	// An item collected here whose ack failed comes back while its sandbox runs: it is acked,
	// as Anthropic's dispatcher does, not taken for a sandbox this controller does not know.
	if collected, ok := c.takeUnacked(session, polled.work); running && ok {
		c.ack(ctx, collected)
		return
	}
	if running {
		err = c.launcher.Stop(ctx, session)
	}
	if running && err != nil {
		c.log.Warn("stopping a sandbox with no nonce failed", "session_id", session, "error", err)
		return
	}
	if running {
		c.log.Info("stopped a running sandbox this controller holds no nonce for", "session_id", session)
		return
	}
	launched, err := polled.Launch(ctx, c.launcher, c.store)
	if err != nil {
		c.log.Warn("launch failed; leaving the item for redelivery", "session_id", session, "error", err)
		return
	}
	c.metrics.launches.Add(ctx, 1)
	collected, err := launched.Collect(ctx, c.cfg.HandoffWait, c.store, c.launcher)
	if errors.Is(err, ErrHandoffTimeout) {
		c.metrics.timeouts.Add(context.WithoutCancel(ctx), 1)
		c.log.Warn("sandbox did not collect its item; stopping it", "session_id", session, "error", err)
		return
	}
	if err != nil {
		return
	}
	c.metrics.collections.Add(context.WithoutCancel(ctx), 1)
	c.ack(ctx, collected)
}

func (c *Controller) ack(ctx context.Context, collected Collected) {
	acked, err := collected.Ack(ctx, c.queue)
	if err != nil {
		c.mu.Lock()
		c.unacked[collected.session] = collected
		c.mu.Unlock()
		c.log.Warn("ack failed; agentd will hand the item out again", "session_id", collected.session, "error", err)
		return
	}
	c.log.Info("work item handed over", "session_id", acked.Session())
}

func (c *Controller) takeUnacked(session ids.SessionID, work ids.WorkID) (Collected, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	collected, ok := c.unacked[session]
	delete(c.unacked, session)
	return collected, ok && collected.work == work
}

func (c *Controller) begin(session ids.SessionID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight[session] {
		return false
	}
	c.inflight[session] = true
	return true
}

func (c *Controller) end(session ids.SessionID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, session)
}
