package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch/handoff"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// pollBackoff is the pause after a failed poll, so a down agentd is not hammered.
const pollBackoff = time.Second

// Config holds the controller's settings.
type Config struct {
	// HandoffWait is how long a sandbox gets to collect its item; the queue's reclaim window is twice it.
	HandoffWait time.Duration
	// MaxSandboxes bounds the items handled at once.
	MaxSandboxes int
	// ReleaseInterval is how often sandboxes of gone sessions are released.
	ReleaseInterval time.Duration
}

// Controller is the poll loop and its handoffs.
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
}

// New wires a controller. meter receives its metrics.
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
		store: store, log: log, metrics: m, inflight: map[ids.SessionID]bool{},
	}, nil
}

// Run polls until ctx ends, then waits for the handoffs under way; an item not yet collected is
// left for agentd to hand out again.
func (c *Controller) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	slots := make(chan struct{}, c.cfg.MaxSandboxes)
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		session, secret, ok, err := c.queue.Poll(ctx, 2*c.cfg.HandoffWait)
		if !ok || err != nil {
			<-slots
			if err != nil && ctx.Err() == nil {
				c.log.Warn("poll failed", "error", err)
				pause(ctx, pollBackoff)
			}
			continue
		}
		wg.Go(func() {
			defer func() { <-slots }()
			c.handle(ctx, Polled{session: session, secret: secret})
		})
	}
}

// handle runs one item's handoff from Polled to Acked.
func (c *Controller) handle(ctx context.Context, polled Polled) {
	session := polled.session
	if !c.begin(session) {
		c.log.Info("item redelivered while its sandbox is collecting it; waiting", "session_id", session)
		return
	}
	defer c.end(session)
	if _, running, err := c.locator.Address(ctx, session); err != nil || running {
		c.recover(ctx, session, err)
		return
	}
	launched, err := polled.Launch(ctx, c.launcher, c.store)
	if err != nil {
		c.log.Warn("launch failed; leaving the item for redelivery", "session_id", session, "error", err)
		return
	}
	c.metrics.launches.Add(ctx, 1)
	collected, err := launched.Collect(ctx, c.cfg.HandoffWait, c.store, c.launcher)
	if err != nil {
		if errors.Is(err, ErrHandoffTimeout) {
			c.metrics.timeouts.Add(context.WithoutCancel(ctx), 1)
			c.log.Warn("sandbox did not collect its item; stopped it", "session_id", session, "error", err)
		}
		return
	}
	c.metrics.collections.Add(context.WithoutCancel(ctx), 1)
	acked, err := collected.Ack(ctx, c.queue)
	if err != nil {
		c.log.Warn("ack failed; agentd will hand the item out again", "session_id", session, "error", err)
		return
	}
	c.log.Info("work item handed over", "session_id", acked.Session())
}

// recover handles an item for a session whose sandbox runs with no nonce held here, as after a
// restart: the sandbox is stopped and the item left unacked, so its next delivery launches afresh.
func (c *Controller) recover(ctx context.Context, session ids.SessionID, lookup error) {
	if lookup != nil {
		c.log.Warn("finding the session's sandbox failed; leaving the item for redelivery", "session_id", session, "error", lookup)
		return
	}
	if err := c.launcher.Stop(ctx, session); err != nil {
		c.log.Warn("stopping a sandbox with no nonce failed", "session_id", session, "error", err)
		return
	}
	c.log.Info("stopped a running sandbox this controller holds no nonce for", "session_id", session)
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

func pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
