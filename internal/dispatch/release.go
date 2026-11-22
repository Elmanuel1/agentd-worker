package dispatch

import (
	"context"
	"time"
)

// Release releases the sandboxes of sessions agentd no longer has, every ReleaseInterval, until ctx ends.
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
			exists, err := c.sessions.Exists(ctx, sb.Session)
			if err != nil {
				c.log.Warn("reading session failed; keeping its sandbox", "session_id", sb.Session, "error", err)
				continue
			}
			if exists {
				continue
			}
			if err := c.launcher.Release(ctx, sb.Session); err != nil {
				c.log.Warn("release failed", "session_id", sb.Session, "error", err)
				continue
			}
			c.metrics.releases.Add(ctx, 1)
			c.log.Info("released the sandbox of a gone session", "session_id", sb.Session)
		}
	}
}
