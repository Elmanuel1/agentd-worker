package main

import (
	"testing"
	"time"
)

// TestHandoffWait holds SC-1's default handoff wait: 120 s, 110 s on Fargate, and a set one as set.
func TestHandoffWait(t *testing.T) {
	cases := []struct {
		launcher, set string
		want          time.Duration
	}{
		{"docker", "", 120 * time.Second},
		{"fargate", "", 110 * time.Second},
		{"fargate", "90", 90 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.launcher+"/"+c.set, func(t *testing.T) {
			t.Setenv("AGENTD_HANDOFF_WAIT_SECONDS", c.set)
			var e env
			if got := dispatchConfig(&e, c.launcher).HandoffWait; got != c.want || e.err() != nil {
				t.Errorf("handoff wait %s, %v; want %s", got, e.err(), c.want)
			}
		})
	}
}
