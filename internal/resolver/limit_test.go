package resolver

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestLimitDropsWhenFull holds the relay's bound: a query past the slots is dropped and warned of
// once, and a released slot is handed out again.
func TestLimitDropsWhenFull(t *testing.T) {
	var logs bytes.Buffer
	l := newLimit(1, slog.New(slog.NewJSONHandler(&logs, nil)))
	if !l.take() {
		t.Fatal("the first query found no slot")
	}
	if second, third := l.take(), l.take(); second || third {
		t.Error("a query past the bound took a slot")
	}
	if n := strings.Count(logs.String(), "dropping queries"); n != 1 {
		t.Errorf("warned %d times for two drops in a minute, want once", n)
	}
	l.release()
	if !l.take() {
		t.Error("a released slot was not handed out again")
	}
}
