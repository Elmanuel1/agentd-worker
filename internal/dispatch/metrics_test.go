package dispatch

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// TestMetrics holds SC-12: one collected item, one timed out, one release, and the queue's depth.
func TestMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader := sdkmetric.NewManualReader()
		meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
		r := newMeteredRig(t, cfg, fakeSessions{"sesn_gone": Session{Gone: true}}, meter)
		r.backend.running["sesn_gone"] = false
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { r.ctrl.Run(ctx); close(done) }()
		go r.ctrl.Release(ctx)
		r.queue.items <- item{"sesn_a", ids.NewSecret("s")}
		r.queue.items <- item{"sesn_b", ids.NewSecret("s")}
		time.Sleep(time.Second)
		r.collect("sesn_a")
		time.Sleep(cfg.ReleaseInterval)
		synctest.Wait()
		got := collect(t, reader)
		cancel()
		<-done
		want := map[string]int64{"launches": 2, "collections": 1, "handoff_timeouts": 1, "releases": 1}
		for name, v := range want {
			if got[name] != v {
				t.Errorf("%s = %d, want %d", name, got[name], v)
			}
		}
	})
}

// TestQueueDepth reads the queue's depth while no controller consumes it.
func TestQueueDepth(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
	r := newMeteredRig(t, cfg, nil, meter)
	r.queue.items <- item{"sesn_a", ids.NewSecret("s")}
	r.queue.items <- item{"sesn_b", ids.NewSecret("s")}
	if got := collect(t, reader)["queue_depth"]; got != 2 {
		t.Errorf("queue_depth = %d, want 2", got)
	}
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				out[m.Name] = d.DataPoints[0].Value
			case metricdata.Gauge[int64]:
				out[m.Name] = d.DataPoints[0].Value
			}
		}
	}
	return out
}
