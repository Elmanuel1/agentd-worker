package dispatch

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

var cfg = Config{HandoffWait: 120 * time.Second, MaxSandboxes: 100, ReleaseInterval: 600 * time.Second}

// TestStatesOnlyStepForward holds the handoff order: each state's only step is the next state's.
func TestStatesOnlyStepForward(t *testing.T) {
	cases := []struct {
		state   reflect.Type
		methods []string
	}{
		{reflect.TypeFor[Polled](), []string{"Launch"}},
		{reflect.TypeFor[Launched](), []string{"Collect"}},
		{reflect.TypeFor[Collected](), []string{"Ack"}},
		{reflect.TypeFor[Acked](), []string{"Session"}},
	}
	for _, c := range cases {
		var got []string
		for m := range c.state.Methods() {
			got = append(got, m.Name)
		}
		if !slices.Equal(got, c.methods) {
			t.Errorf("%s has methods %v, want %v", c.state.Name(), got, c.methods)
		}
		if c.state.NumField() > 0 && c.state.Field(0).IsExported() {
			t.Errorf("%s exports its fields", c.state.Name())
		}
	}
}

func TestHandoff(t *testing.T) {
	cases := []struct {
		name         string
		runningFirst bool          // the sandbox already runs, with no nonce held here
		collectAfter time.Duration // 0: never collected
		redeliver    bool
		wantLaunches int
		wantStops    int
		wantAcked    bool
	}{
		{name: "collected then acked", collectAfter: 10 * time.Second, wantLaunches: 1, wantAcked: true},
		{name: "uncollected is dropped and stopped (SC-5)", wantLaunches: 1, wantStops: 1},
		{name: "running with no nonce is stopped (SC-6)", runningFirst: true, wantStops: 1},
		{name: "redelivery while waiting launches nothing (SC-7)", redeliver: true, collectAfter: 30 * time.Second, wantLaunches: 1, wantAcked: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t, cfg, nil)
				r.backend.running["sesn_1"] = c.runningFirst
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error)
				go func() { done <- r.ctrl.Run(ctx) }()
				r.queue.items <- item{"sesn_1", ids.NewSecret("s")}
				time.Sleep(time.Second)
				if c.redeliver {
					r.queue.items <- item{"sesn_1", ids.NewSecret("s")}
				}
				if c.collectAfter > 0 {
					time.Sleep(c.collectAfter)
					if code := r.collect("sesn_1"); code != 200 {
						t.Fatalf("collect: %d", code)
					}
				}
				time.Sleep(2 * cfg.HandoffWait)
				cancel()
				<-done
				launched, stopped, _ := r.backend.calls()
				_, acked := r.queue.state()
				if len(launched) != c.wantLaunches || len(stopped) != c.wantStops || (len(acked) == 1) != c.wantAcked {
					t.Errorf("launched %v, stopped %v, acked %v", launched, stopped, acked)
				}
			})
		})
	}
}

// TestMaxSandboxesPausesPolling holds SC-8: with every slot waiting on a handoff, no poll runs.
func TestMaxSandboxesPausesPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		one := cfg
		one.MaxSandboxes = 1
		r := newRig(t, one, nil)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go r.ctrl.Run(ctx)
		r.queue.items <- item{"sesn_1", ids.NewSecret("s")}
		synctest.Wait()
		before, _ := r.queue.state()
		time.Sleep(60 * time.Second)
		synctest.Wait()
		if after, _ := r.queue.state(); after != before {
			t.Errorf("polled %d times while at max_sandboxes, want %d", after, before)
		}
		r.collect("sesn_1")
		time.Sleep(5 * time.Second)
		if after, _ := r.queue.state(); after <= before {
			t.Error("polling did not resume after the handoff ended")
		}
	})
}

// TestShutdown holds SC-11: an uncollected item is left unacked and its sandbox kept; a collected one is acked.
func TestShutdown(t *testing.T) {
	for _, collected := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, cfg, nil)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error)
			go func() { done <- r.ctrl.Run(ctx) }()
			r.queue.items <- item{"sesn_1", ids.NewSecret("s")}
			time.Sleep(time.Second)
			if collected {
				r.collect("sesn_1")
			}
			cancel()
			<-done
			_, stopped, _ := r.backend.calls()
			_, acked := r.queue.state()
			if len(stopped) != 0 || (len(acked) == 1) != collected {
				t.Errorf("collected %v: stopped %v, acked %v", collected, stopped, acked)
			}
		})
	}
}

// TestReleaseGoneSessions holds SC-9: only a session agentd answers 404 for is released.
func TestReleaseGoneSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sessions := fakeSessions{"sesn_gone": errGone, "sesn_live": nil, "sesn_err": context.DeadlineExceeded}
		r := newRig(t, cfg, sessions)
		for s := range sessions {
			r.backend.running[s] = false
		}
		ctx, cancel := context.WithCancel(t.Context())
		go r.ctrl.Release(ctx)
		time.Sleep(cfg.ReleaseInterval - time.Second)
		synctest.Wait()
		if _, _, released := r.backend.calls(); len(released) != 0 {
			t.Fatalf("released %v before the interval", released)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		cancel()
		if _, _, released := r.backend.calls(); !slices.Equal(released, []ids.SessionID{"sesn_gone"}) {
			t.Errorf("released %v", released)
		}
	})
}
