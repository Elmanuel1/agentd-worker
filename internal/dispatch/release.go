package dispatch

import (
	"context"
	"time"
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
			state, err := c.sessions.State(ctx, sb.Session)
			if err != nil {
				c.log.Warn("reading session failed; keeping its sandbox", "session_id", sb.Session, "error", err)
				continue
			}
			if !state.Over(sb.Running, time.Now(), c.cfg.Retention) {
				continue
			}
			if err := c.launcher.Release(ctx, sb.Session); err != nil {
				c.log.Warn("release failed", "session_id", sb.Session, "error", err)
				continue
			}
			c.metrics.releases.Add(ctx, 1)
			c.log.Info("released the sandbox of an ended session", "session_id", sb.Session, "gone", state.Gone, "terminated", state.Terminated)
		}
	}
}
