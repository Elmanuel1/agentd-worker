package reap

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// TestLogsAndMetrics holds RP-4 and RP-6: one INFO line per answered item, and both counters.
func TestLogsAndMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
	var logged bytes.Buffer
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	beat := now.Add(-7 * time.Minute)
	f := &fakeAgentd{items: []Item{{Session: "s", State: StateActive, Beat: beat}}, events: map[ids.SessionID][]Event{"s": owes}}
	r, err := New(Config{Interval: time.Minute, StaleAfter: 360 * time.Second}, f, events{f}, meter, slog.New(slog.NewJSONHandler(&logged, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r.passAt(t.Context(), now)

	var line struct {
		Level         string
		LastHeartbeat time.Time `json:"last_heartbeat"`
		Calls         int       `json:"calls_answered"`
	}
	if err := json.Unmarshal(logged.Bytes(), &line); err != nil {
		t.Fatalf("log %q: %v", logged.String(), err)
	}
	var raw map[string]any
	json.Unmarshal(logged.Bytes(), &raw)
	if line.Level != "INFO" || raw["item_id"] != "s" || !line.LastHeartbeat.Equal(beat) || line.Calls != 2 {
		t.Errorf("log line %s", logged.String())
	}

	got := sums(t, reader)
	if got["reaped"] != 1 || got["error_results"] != 2 {
		t.Errorf("metrics %v", got)
	}
}

func sums(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if s, ok := m.Data.(metricdata.Sum[int64]); ok && len(s.DataPoints) > 0 {
				out[m.Name] = s.DataPoints[0].Value
			}
		}
	}
	return out
}
