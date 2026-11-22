package reap

import (
	"slices"
	"time"
)

// owed returns the tool calls a worker still owes a result, in order, by the rules of the SDK's
// tool runner: a call with no result, allowed by its permission or by a later confirmation, not
// cut off by an interrupt of the whole session, and not of a terminated thread. A session that
// ended owes nothing.
func owed(events []Event) []string {
	type call struct {
		id, permission, thread string
		at                     time.Time
	}
	var calls []call
	answered, verdicts, terminated := map[string]bool{}, map[string]string{}, map[string]bool{}
	var cutoff time.Time
	for _, e := range events {
		switch e.Type {
		case ToolUse:
			calls = append(calls, call{e.ID, e.Permission, e.Thread, e.At})
		case ToolResult:
			answered[e.ToolUseID] = true
		case Confirmation:
			verdicts[e.ToolUseID] = e.Verdict
		case Interrupt:
			// An interrupt aimed at one thread does not cut off the session's calls.
			if e.Thread == "" && e.At.After(cutoff) {
				cutoff = e.At
			}
		case ThreadTerminated:
			terminated[e.Thread] = true
		case SessionTerminated, SessionDeleted:
			return nil
		}
	}
	calls = slices.DeleteFunc(calls, func(c call) bool {
		interrupted := !cutoff.IsZero() && !c.at.After(cutoff)
		allowed := c.permission == "" || c.permission == "allow" || (c.permission == "ask" && verdicts[c.id] == "allow")
		return answered[c.id] || interrupted || terminated[c.thread] || !allowed
	})
	ids := make([]string, len(calls))
	for i, c := range calls {
		ids[i] = c.id
	}
	return ids
}
