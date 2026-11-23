package docker

import (
	"testing"
	"testing/synctest"
)

// TestGate holds DK-3 against a worker starting during Stop: Stop waits out a start under way, and
// no worker starts while a Stop runs, nor is one kept from starting after.
func TestGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGate()
		if !g.start("sesn_1") {
			t.Fatal("a start with no Stop was refused")
		}
		stopped := make(chan func())
		go func() { stopped <- g.stop("sesn_1") }()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("Stop went ahead of a start under way")
		default:
		}
		g.started("sesn_1")
		release := <-stopped
		if g.start("sesn_1") {
			t.Error("a worker started while a Stop ran")
		}
		if !g.start("sesn_2") {
			t.Error("a Stop of one session held off another's worker")
		}
		g.started("sesn_2")
		release()
		if !g.start("sesn_1") {
			t.Error("a worker was kept from starting after the Stop")
		}
	})
}
