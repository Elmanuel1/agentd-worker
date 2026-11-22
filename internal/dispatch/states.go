package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch/handoff"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// ackTimeout bounds the ack, which outlives a cancelled context: the sandbox already holds the item.
const ackTimeout = 10 * time.Second

// ErrHandoffTimeout means the sandbox did not collect its item within the handoff wait.
var ErrHandoffTimeout = errors.New("the sandbox did not collect its work item in time")

// Polled is an item the queue handed out. Its secret lives only here and in the handoff store.
type Polled struct {
	session ids.SessionID
	secret  ids.Secret
}

// Launch opens the item for collection under a fresh nonce and launches the session's sandbox.
func (p Polled) Launch(ctx context.Context, launcher Launcher, store *handoff.Store) (Launched, error) {
	nonce := ids.NewNonce()
	collected := store.Open(p.session, nonce, p.secret)
	if err := launcher.Launch(ctx, p.session, nonce); err != nil {
		store.Drop(p.session)
		return Launched{}, fmt.Errorf("launching session %s: %w", p.session, err)
	}
	return Launched{session: p.session, collected: collected}, nil
}

// Launched is a sandbox started for its item, which it has not collected yet.
type Launched struct {
	session   ids.SessionID
	collected <-chan struct{}
}

// Collect waits for the sandbox to collect its item. When the wait ends first, the item is
// dropped unacked and the sandbox stopped; when ctx ends first, the item is left for agentd.
func (l Launched) Collect(ctx context.Context, wait time.Duration, store *handoff.Store, launcher Launcher) (Collected, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-l.collected:
		return Collected{session: l.session}, nil
	case <-timer.C:
		if !store.Drop(l.session) {
			return Collected{session: l.session}, nil
		}
		if err := launcher.Stop(context.WithoutCancel(ctx), l.session); err != nil {
			return Collected{}, errors.Join(ErrHandoffTimeout, err)
		}
		return Collected{}, ErrHandoffTimeout
	case <-ctx.Done():
		if !store.Drop(l.session) {
			return Collected{session: l.session}, nil
		}
		return Collected{}, ctx.Err()
	}
}

// Collected is an item the sandbox holds and agentd has not been told of.
type Collected struct {
	session ids.SessionID
}

func (c Collected) Ack(ctx context.Context, queue Queue) (Acked, error) {
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ackTimeout)
	defer cancel()
	if err := queue.Ack(ackCtx, c.session); err != nil {
		return Acked{}, fmt.Errorf("acking session %s: %w", c.session, err)
	}
	return Acked{of: c}, nil
}

// Acked is an item handed over and confirmed. Nothing follows it.
type Acked struct {
	of Collected
}

func (a Acked) Session() ids.SessionID { return a.of.session }
