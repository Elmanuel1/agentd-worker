package reap

import "time"

// owed returns the tool calls a worker still owes a result, in order, by the rules of the SDK's
// tool runner: a call with no result, allowed by its permission or by a later confirmation, not
// cut off by a later interrupt of the whole session, and not of a terminated thread. A session
// that ended owes nothing, nor a call made after until.
func owed(events []Event, until time.Time) []string {
	type call struct {
		id, permission, thread string
		at                     time.Time
	}
	var calls []call
	answered, verdicts, terminated := map[string]bool{}, map[string]string{}, map[string]bool{}
	// cut counts the calls before the session's last interrupt in the log, which may carry no time.
	cut := 0
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
			if e.Thread == "" {
				cut = len(calls)
			}
		case ThreadTerminated:
			terminated[e.Thread] = true
		case SessionTerminated, SessionDeleted:
			return nil
		}
	}
	var ids []string
	for _, c := range calls[cut:] {
		allowed := c.permission == "" || c.permission == "allow" || (c.permission == "ask" && verdicts[c.id] == "allow")
		later := c.at.After(until)
		if answered[c.id] || terminated[c.thread] || !allowed || later {
			continue
		}
		ids = append(ids, c.id)
	}
	return ids
}
