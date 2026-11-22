package reap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

type fakeAgentd struct {
	items   []Item
	events  map[ids.SessionID][]Event
	stopErr map[ids.SessionID]error
	sendErr error
	pages   int
	calls   []string // "stop sesn" and "send sesn ids", in order
}

func (f *fakeAgentd) List(_ context.Context, cursor string) ([]Item, string, error) {
	f.pages++
	start, _ := strconv.Atoi(cursor)
	end := min(start+100, len(f.items))
	next := ""
	if end < len(f.items) {
		next = strconv.Itoa(end)
	}
	return f.items[start:end], next, nil
}

func (f *fakeAgentd) Stop(_ context.Context, s ids.SessionID) error {
	f.calls = append(f.calls, "stop "+string(s))
	return f.stopErr[s]
}

type events struct{ f *fakeAgentd }

func (e events) List(_ context.Context, s ids.SessionID) ([]Event, error) { return e.f.events[s], nil }

func (e events) Send(_ context.Context, s ids.SessionID, calls []string) error {
	e.f.calls = append(e.f.calls, fmt.Sprintf("send %s %v", s, calls))
	return e.f.sendErr
}

var owes = []Event{
	{Type: ToolUse, ID: "tu_1"}, {Type: ToolResult, ToolUseID: "tu_1"},
	{Type: ToolUse, ID: "tu_2"}, {Type: ToolUse, ID: "tu_3"},
}

func newReaper(t *testing.T, f *fakeAgentd) *Reaper {
	t.Helper()
	r, err := New(Config{Interval: time.Minute, StaleAfter: 360 * time.Second}, f, events{f}, noop.Meter{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestPass holds RP-1, RP-2 and RP-5.
func TestPass(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		item Item
		stop error
		want []string
	}{
		{"stale is stopped before it is answered (RP-1)", Item{Session: "s", State: StateActive, Beat: now.Add(-7 * time.Minute)}, nil, []string{"stop s", "send s [tu_2 tu_3]"}},
		{"stale starting item by its ack", Item{Session: "s", State: StateStarting, Beat: now.Add(-7 * time.Minute)}, nil, []string{"stop s", "send s [tu_2 tu_3]"}},
		{"beating item is left alone", Item{Session: "s", State: StateActive, Beat: now.Add(-time.Minute)}, nil, nil},
		{"unacked item is left to reclaim", Item{Session: "s", State: StateStarting}, nil, nil},
		{"recently stopped is answered (RP-2)", Item{Session: "s", State: StateStopped, StoppedAt: now.Add(-30 * time.Second)}, nil, []string{"send s [tu_2 tu_3]"}},
		{"stopped before the last pass is left", Item{Session: "s", State: StateStopped, StoppedAt: now.Add(-2 * time.Minute)}, nil, nil},
		{"a failed stop answers nothing", Item{Session: "s", State: StateActive, Beat: now.Add(-7 * time.Minute)}, errors.New("503"), []string{"stop s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeAgentd{items: []Item{c.item}, events: map[ids.SessionID][]Event{"s": owes}, stopErr: map[ids.SessionID]error{"s": c.stop}}
			r := newReaper(t, f)
			r.lastPass = now.Add(-time.Minute)
			r.passAt(t.Context(), now)
			if !slices.Equal(f.calls, c.want) {
				t.Errorf("calls %v, want %v", f.calls, c.want)
			}
		})
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

// TestRunReadsEveryPage holds RP-3: a pass every interval, each over every page of 250 items.
func TestRunReadsEveryPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeAgentd{}
		for i := range 250 {
			f.items = append(f.items, Item{Session: ids.SessionID(fmt.Sprint(i)), State: StateActive, Beat: time.Now()})
		}
		r := newReaper(t, f)
		ctx, cancel := context.WithCancel(t.Context())
		go r.Run(ctx)
		synctest.Wait()
		if f.pages != 3 {
			t.Fatalf("first pass read %d pages, want 3", f.pages)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		cancel()
		if f.pages != 6 {
			t.Errorf("read %d pages after one interval, want 6", f.pages)
		}
	})
}

func TestOwed(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		want   []string
	}{
		{"none", nil, nil},
		{"answered and owed", owes, []string{"tu_2", "tu_3"}},
		{"terminated owes nothing", append(slices.Clone(owes), Event{Type: SessionTerminated}), nil},
	}
	for _, c := range cases {
		if got := owed(c.events); !slices.Equal(got, c.want) {
			t.Errorf("%s: owed %v, want %v", c.name, got, c.want)
		}
	}
}
