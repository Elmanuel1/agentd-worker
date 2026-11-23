package dispatch

import (
	"context"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// Release releases, every ReleaseInterval until ctx ends, the sandboxes and storage of sessions that
// are deleted or archived, and of those idle past Retention, as Anthropic's dispatcher does.
func (c *Controller) Release(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.ReleaseInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		sandboxes, err := c.launcher.List(ctx)
		if err != nil {
			c.log.Warn("listing sandboxes failed", "error", err)
			continue
		}
		for _, sb := range sandboxes {
			c.release(ctx, sb)
		}
	}
}

// release releases one session's sandbox and storage once the session is over. It holds the
// session's guard from the check to the release, so no launch for the session runs between them.
func (c *Controller) release(ctx context.Context, sb Sandbox) {
	if !c.begin(sb.Session) {
		return
	}
	defer c.end(sb.Session)
	state, err := c.sessions.State(ctx, sb.Session)
	if err != nil {
		c.log.Warn("reading session failed; keeping its sandbox", "session_id", sb.Session, "error", err)
		return
	}
	now := time.Now()
	if !state.Over(sb.Running, now, c.cfg.Retention) {
		return
	}
	// An idle session keeps its storage while its sandbox runs, and one may have launched since
	// the list, so its sandbox is looked up again.
	if !state.Over(true, now, c.cfg.Retention) && c.running(ctx, sb.Session) {
		return
	}
	if err := c.launcher.Release(ctx, sb.Session); err != nil {
		c.log.Warn("release failed", "session_id", sb.Session, "error", err)
		return
	}
	c.metrics.releases.Add(ctx, 1)
	c.log.Info("released the sandbox of an ended session", "session_id", sb.Session, "gone", state.Gone, "terminated", state.Terminated)
}

// running reports whether the session's sandbox runs, and true when that cannot be read.
func (c *Controller) running(ctx context.Context, session ids.SessionID) bool {
	_, running, err := c.locator.Address(ctx, session)
	if err != nil {
		c.log.Warn("finding the session's sandbox failed; keeping it", "session_id", session, "error", err)
		return true
	}
	return running
}
