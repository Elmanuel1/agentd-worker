package reap

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// TestFirstPassLooksBack holds RP-2 across a restart: the first pass after it answers an item
// stopped in the day before it, whose answer may have failed, and reads nothing older.
func TestFirstPassLooksBack(t *testing.T) {
	items := []Item{
		{Work: "w", Session: "s", State: StateStopped, StoppedAt: time.Now().Add(-time.Hour)},
		{Work: "old", Session: "old", State: StateStopped, StoppedAt: time.Now().Add(-25 * time.Hour)},
	}
	f := &fakeAgentd{items: items, events: map[ids.SessionID][]Event{"s": owes, "old": owes}}
	newReaper(t, f).Pass(t.Context())
	if want := []string{"send s [tu_2 tu_3]"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls %v, want %v", f.calls, want)
	}
}

// TestFailedCallIsRetried holds RP-5: a pass carries on past a failure and the next pass retries it.
func TestFailedCallIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		f := &fakeAgentd{
			items:   []Item{{Session: "a", State: StateStopped, StoppedAt: now}, {Session: "b", State: StateStopped, StoppedAt: now}},
			events:  map[ids.SessionID][]Event{"a": owes, "b": owes},
			sendErr: errors.New("503"),
		}
		r := newReaper(t, f)
		r.lastPass = now.Add(-time.Second)
		r.Pass(t.Context())
		if len(f.calls) != 2 {
			t.Fatalf("first pass stopped after a failure: %v", f.calls)
		}
		f.sendErr = nil
		f.calls = nil
		time.Sleep(time.Minute)
		r.Pass(t.Context())
		if len(f.calls) != 2 {
			t.Errorf("retry pass calls %v", f.calls)
		}
	})
}

// TestUntimedStopIsAnsweredByTheListedTime holds RP-1 when agentd answers a stop with no time: the
// pass answers nothing and retries, the next answers by the stop time agentd lists, and an item
// it lists with no time is retried again; no cutoff is ever this clock's.
func TestUntimedStopIsAnsweredByTheListedTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		listed time.Time
		want   []string
	}{
		{"the listed time", now.Add(-30 * time.Second), []string{"send s [dead]"}},
		{"no listed time", time.Time{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := []Event{{Type: ToolUse, ID: "dead", At: now.Add(-time.Minute)}, {Type: ToolUse, ID: "live", At: now.Add(-10 * time.Second)}}
			f := &fakeAgentd{items: []Item{{Work: "w", Session: "s", State: StateActive, Beat: now.Add(-7 * time.Minute)}}, events: map[ids.SessionID][]Event{"s": log}, untimed: true}
			r := newReaper(t, f)
			r.passAt(t.Context(), now)
			if want := []string{"stop w"}; !slices.Equal(f.calls, want) {
				t.Fatalf("first pass %v, want %v", f.calls, want)
			}
			f.items, f.calls = []Item{{Work: "w", Session: "s", State: StateStopped, StoppedAt: c.listed}}, nil
			r.passAt(t.Context(), now.Add(time.Minute))
			if !slices.Equal(f.calls, c.want) || r.retry["s"] != (c.want == nil) {
				t.Errorf("next pass %v, retry %v; want %v", f.calls, r.retry["s"], c.want)
			}
		})
	}
}
