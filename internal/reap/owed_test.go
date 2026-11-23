package reap

import (
	"slices"
	"testing"
	"time"
)

// TestOwed holds the SDK runner's rules for what a worker owes (RP-1).
func TestOwed(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 22, 12, m, 0, 0, time.UTC) }
	use := func(id, permission, thread string, m int) Event {
		return Event{Type: ToolUse, ID: id, Permission: permission, Thread: thread, At: at(m)}
	}
	// until is the minute the item stopped, 0 for a stale item, whose stop is this pass's.
	cases := []struct {
		name   string
		events []Event
		until  int
		want   []string
	}{
		{"none", nil, 0, nil},
		{"answered and owed", owes, 0, []string{"tu_2", "tu_3"}},
		{"denied is never run", []Event{use("a", "deny", "", 1)}, 0, nil},
		{"ask waits for its confirmation", []Event{use("a", "ask", "", 1)}, 0, nil},
		{"ask confirmed allow is owed", []Event{use("a", "ask", "", 1), {Type: Confirmation, ToolUseID: "a", Verdict: "allow"}}, 0, []string{"a"}},
		{"ask confirmed deny is not", []Event{use("a", "ask", "", 1), {Type: Confirmation, ToolUseID: "a", Verdict: "deny"}}, 0, nil},
		{"an interrupt cuts off earlier calls", []Event{use("a", "allow", "", 1), {Type: Interrupt, At: at(2)}, use("b", "allow", "", 3)}, 0, []string{"b"}},
		{"an interrupt with no time cuts off the calls before it", []Event{use("a", "allow", "", 1), {Type: Interrupt}, use("b", "allow", "", 3)}, 0, []string{"b"}},
		{"a thread's interrupt cuts off nothing", []Event{use("a", "allow", "", 1), {Type: Interrupt, Thread: "th", At: at(2)}}, 0, []string{"a"}},
		{"a terminated thread's calls are not owed", []Event{use("a", "allow", "th", 1), use("b", "allow", "", 1), {Type: ThreadTerminated, Thread: "th"}}, 0, []string{"b"}},
		{"a call after the stop is the next worker's, and still live", []Event{use("a", "allow", "", 1), use("b", "allow", "", 3)}, 2, []string{"a"}},
		{"a terminated session owes nothing", append(slices.Clone(owes), Event{Type: SessionTerminated}), 0, nil},
	}
	for _, c := range cases {
		var until time.Time
		if c.until > 0 {
			until = at(c.until)
		}
		if got := owed(c.events, until); !slices.Equal(got, c.want) {
			t.Errorf("%s: owed %v, want %v", c.name, got, c.want)
		}
	}
}
