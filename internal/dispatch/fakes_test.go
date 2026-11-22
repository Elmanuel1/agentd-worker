package dispatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch/handoff"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// sandboxAddr is httptest.NewRequest's RemoteAddr.
var sandboxAddr = netip.MustParseAddr("192.0.2.1")

type item struct {
	session ids.SessionID
	secret  ids.Secret
}

type fakeQueue struct {
	items chan item
	mu    sync.Mutex
	polls int
	acked []ids.SessionID
}

func (q *fakeQueue) Poll(ctx context.Context, _ time.Duration) (ids.SessionID, ids.Secret, bool, error) {
	q.mu.Lock()
	q.polls++
	q.mu.Unlock()
	select {
	case it := <-q.items:
		return it.session, it.secret, true, nil
	case <-ctx.Done():
		return "", ids.Secret{}, false, ctx.Err()
	case <-time.After(999 * time.Millisecond):
		return "", ids.Secret{}, false, nil
	}
}

func (q *fakeQueue) Ack(_ context.Context, s ids.SessionID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.acked = append(q.acked, s)
	return nil
}

func (q *fakeQueue) Depth(context.Context) (int64, error) { return int64(len(q.items)), nil }

func (q *fakeQueue) state() (int, []ids.SessionID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.polls, append([]ids.SessionID(nil), q.acked...)
}

// fakeBackend is a Launcher and a Locator over an in-memory set of sandboxes.
type fakeBackend struct {
	mu       sync.Mutex
	running  map[ids.SessionID]bool
	nonces   map[ids.SessionID]ids.Nonce
	launched []ids.SessionID
	stopped  []ids.SessionID
	released []ids.SessionID
}

func newBackend() *fakeBackend {
	return &fakeBackend{running: map[ids.SessionID]bool{}, nonces: map[ids.SessionID]ids.Nonce{}}
}

func (b *fakeBackend) Launch(_ context.Context, s ids.SessionID, n ids.Nonce) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running[s], b.nonces[s] = true, n
	b.launched = append(b.launched, s)
	return nil
}

func (b *fakeBackend) List(context.Context) ([]Sandbox, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Sandbox
	for s, r := range b.running {
		out = append(out, Sandbox{Session: s, Running: r})
	}
	return out, nil
}

func (b *fakeBackend) Stop(_ context.Context, s ids.SessionID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running[s] = false
	b.stopped = append(b.stopped, s)
	return nil
}

func (b *fakeBackend) Release(_ context.Context, s ids.SessionID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.running, s)
	b.released = append(b.released, s)
	return nil
}

func (b *fakeBackend) Address(_ context.Context, s ids.SessionID) (netip.Addr, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return sandboxAddr, b.running[s], nil
}

func (b *fakeBackend) nonce(s ids.SessionID) ids.Nonce {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nonces[s]
}

func (b *fakeBackend) calls() (launched, stopped, released []ids.SessionID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ids.SessionID(nil), b.launched...), append([]ids.SessionID(nil), b.stopped...), append([]ids.SessionID(nil), b.released...)
}

type fakeSessions map[ids.SessionID]error

func (f fakeSessions) Exists(_ context.Context, s ids.SessionID) (bool, error) {
	err, ok := f[s]
	if errors.Is(err, errGone) {
		return false, nil
	}
	return ok || err == nil, err
}

var errGone = errors.New("gone")

type rig struct {
	queue   *fakeQueue
	backend *fakeBackend
	store   *handoff.Store
	server  http.Handler
	ctrl    *Controller
}

func newRig(t *testing.T, cfg Config, sessions fakeSessions) *rig {
	t.Helper()
	return newMeteredRig(t, cfg, sessions, noop.Meter{})
}

func newMeteredRig(t *testing.T, cfg Config, sessions fakeSessions, meter metric.Meter) *rig {
	t.Helper()
	r := &rig{queue: &fakeQueue{items: make(chan item, 10)}, backend: newBackend()}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r.store = handoff.NewStore(r.backend)
	r.server = handoff.NewServer(r.store, "env_1", "https://agentd.test", log).Handler()
	ctrl, err := New(cfg, r.queue, sessions, r.backend, r.backend, r.store, meter, log)
	if err != nil {
		t.Fatal(err)
	}
	r.ctrl = ctrl
	return r
}

// collect plays the sandbox's fetch container and returns the status it got.
func (r *rig) collect(s ids.SessionID) int {
	req := httptest.NewRequest(http.MethodGet, "/work-item", nil)
	req.Header.Set(handoff.NonceHeader, string(r.backend.nonce(s)))
	rec := httptest.NewRecorder()
	r.server.ServeHTTP(rec, req)
	return rec.Code
}
