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
	cases := []struct {
		name   string
		events []Event
		want   []string
	}{
		{"none", nil, nil},
		{"answered and owed", owes, []string{"tu_2", "tu_3"}},
		{"denied is never run", []Event{use("a", "deny", "", 1)}, nil},
		{"ask waits for its confirmation", []Event{use("a", "ask", "", 1)}, nil},
		{"ask confirmed allow is owed", []Event{use("a", "ask", "", 1), {Type: Confirmation, ToolUseID: "a", Verdict: "allow"}}, []string{"a"}},
		{"ask confirmed deny is not", []Event{use("a", "ask", "", 1), {Type: Confirmation, ToolUseID: "a", Verdict: "deny"}}, nil},
		{"an interrupt cuts off earlier calls", []Event{use("a", "allow", "", 1), {Type: Interrupt, At: at(2)}, use("b", "allow", "", 3)}, []string{"b"}},
		{"an interrupt with no time cuts off the calls before it", []Event{use("a", "allow", "", 1), {Type: Interrupt}, use("b", "allow", "", 3)}, []string{"b"}},
		{"a thread's interrupt cuts off nothing", []Event{use("a", "allow", "", 1), {Type: Interrupt, Thread: "th", At: at(2)}}, []string{"a"}},
		{"a terminated thread's calls are not owed", []Event{use("a", "allow", "th", 1), use("b", "allow", "", 1), {Type: ThreadTerminated, Thread: "th"}}, []string{"b"}},
		{"a terminated session owes nothing", append(slices.Clone(owes), Event{Type: SessionTerminated}), nil},
	}
	for _, c := range cases {
		if got := owed(c.events, time.Time{}); !slices.Equal(got, c.want) {
			t.Errorf("%s: owed %v, want %v", c.name, got, c.want)
		}
	}
	// A call made after the item stopped is the session's next worker's, and still live.
	next := []Event{use("a", "allow", "", 1), use("b", "allow", "", 3)}
	if got := owed(next, at(2)); !slices.Equal(got, []string{"a"}) {
		t.Errorf("owed until the stop %v, want [a]", got)
	}
}
