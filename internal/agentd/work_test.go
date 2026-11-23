package agentd

import (
	"testing"
	"time"
)

// TestWorkStop holds RP-1's stop time: the one agentd records, from the stop's answer, or from the
// item when another stopped it first and the stop answers 409.
func TestWorkStop(t *testing.T) {
	item := `{"id":"work_1","data":{"id":"sesn_1"},"state":"stopped","stopped_at":"2026-11-22T10:00:00Z"}`
	conflict := `{"type":"error","error":{"type":"invalid_request_error","message":"already stopped"}}`
	at := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		routes map[string]answer
		want   time.Time
		failed bool
	}{
		{"the stop's answer", map[string]answer{"POST /v1/environments/env_1/work/work_1/stop": route(200, item)}, at, false},
		{"a 409 reads the item", map[string]answer{
			"POST /v1/environments/env_1/work/work_1/stop": route(409, conflict),
			"GET /v1/environments/env_1/work/work_1":       route(200, item),
		}, at, false},
		{"a failed stop is an error", map[string]answer{}, time.Time{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := serve(t, &fake{routes: c.routes}).Work().Stop(t.Context(), "work_1")
			if (err != nil) != c.failed || !got.Equal(c.want) {
				t.Errorf("stopped at %s, %v; want %s", got, err, c.want)
			}
		})
	}
}
