package docker

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

// TestGate holds DK-3 against workers starting during Stop: Stop waits out every start under way,
// gives up when its context ends, and no worker starts while a Stop runs, nor is one kept from
// starting after.
func TestGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGate()
		first, ok1 := g.start("sesn_1")
		second, ok2 := g.start("sesn_1")
		if !ok1 || !ok2 {
			t.Fatal("a start with no Stop was refused")
		}
		stopped := make(chan func())
		go func() {
			release, _ := g.stop(t.Context(), "sesn_1")
			stopped <- release
		}()
		first()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("Stop went ahead of a start under way")
		default:
		}
		second()
		release := <-stopped
		if _, ok := g.start("sesn_1"); ok {
			t.Error("a worker started while a Stop ran")
		}
		other, ok := g.start("sesn_2")
		if !ok {
			t.Error("a Stop of one session held off another's worker")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := g.stop(ctx, "sesn_2"); !errors.Is(err, context.Canceled) {
			t.Errorf("a Stop whose context ended waited on: %v", err)
		}
		other()
		release()
		if _, ok := g.start("sesn_1"); !ok {
			t.Error("a worker was kept from starting after the Stop")
		}
		if _, ok := g.start("sesn_2"); !ok {
			t.Error("a Stop that gave up kept holding starts off")
		}
	})
}

// TestStopWaitsOutAWorkerStart holds that Stop goes through the gate before it reads the session.
func TestStopWaitsOutAWorkerStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &Launcher{gate: newGate()}
		started, _ := l.gate.start("sesn_1")
		defer started()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)
		go func() { done <- l.Stop(ctx, "sesn_1") }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("Stop went ahead of a worker start under way")
		default:
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Stop: %v, want its context's end", err)
		}
	})
}
