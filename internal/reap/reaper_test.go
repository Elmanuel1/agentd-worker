package reap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

type fakeAgentd struct {
	mu      sync.Mutex
	items   []Item
	events  map[ids.SessionID][]Event
	stopErr map[ids.WorkID]error
	// stoppedAt is the stop agentd records for a stopped item, zero for none.
	stoppedAt time.Time
	sendErr   error
	pages     int
	calls     []string // "stop sesn" and "send sesn ids", in order
}

func (f *fakeAgentd) List(_ context.Context, cursor string) ([]Item, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages++
	start, _ := strconv.Atoi(cursor)
	end := min(start+100, len(f.items))
	next := ""
	if end < len(f.items) {
		next = strconv.Itoa(end)
	}
	return f.items[start:end], next, nil
}

func (f *fakeAgentd) Stop(_ context.Context, s ids.WorkID) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "stop "+string(s))
	return f.stoppedAt, f.stopErr[s]
}

type events struct{ f *fakeAgentd }

func (e events) List(_ context.Context, s ids.SessionID) ([]Event, error) { return e.f.events[s], nil }

func (e events) Send(_ context.Context, s ids.SessionID, calls []string) error {
	e.f.mu.Lock()
	defer e.f.mu.Unlock()
	e.f.calls = append(e.f.calls, fmt.Sprintf("send %s %v", s, calls))
	return e.f.sendErr
}

func (f *fakeAgentd) read() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pages
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

// TestPass holds RP-1, RP-2 and RP-5; a stale item owes no replacement worker's calls.
func TestPass(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		item Item
		stop error
		// log is the session's events, owes when nil.
		log  []Event
		want []string
		// stoppedAt is the stop agentd records, in its own clock; zero for none.
		stoppedAt time.Time
	}{
		{"stale is stopped before it is answered (RP-1)", Item{Work: "w", Session: "s", State: StateActive, Beat: now.Add(-7 * time.Minute)}, nil, nil, []string{"stop w", "send s [tu_2 tu_3]"}, time.Time{}},
		{"stale starting item by its ack", Item{Work: "w", Session: "s", State: StateStarting, Beat: now.Add(-7 * time.Minute)}, nil, nil, []string{"stop w", "send s [tu_2 tu_3]"}, time.Time{}},
		{"beating item is left alone", Item{Work: "w", Session: "s", State: StateActive, Beat: now.Add(-time.Minute)}, nil, nil, nil, time.Time{}},
		{"unacked item is left to reclaim", Item{Work: "w", Session: "s", State: StateStarting}, nil, nil, nil, time.Time{}},
		{"recently stopped is answered (RP-2)", Item{Work: "w", Session: "s", State: StateStopped, StoppedAt: now.Add(-30 * time.Second)}, nil, nil, []string{"send s [tu_2 tu_3]"}, time.Time{}},
		{"stopped within an interval before the last pass, by a lagging clock, is answered", Item{Work: "w", Session: "s", State: StateStopped, StoppedAt: now.Add(-90 * time.Second)}, nil, nil, []string{"send s [tu_2 tu_3]"}, time.Time{}},
		{"stopped an interval before the last pass is left", Item{Work: "w", Session: "s", State: StateStopped, StoppedAt: now.Add(-2 * time.Minute)}, nil, nil, nil, time.Time{}},
		{"a stopping item whose worker died is stopped", Item{Work: "w", Session: "s", State: StateStopping, Beat: now.Add(-7 * time.Minute)}, nil, nil, []string{"stop w", "send s [tu_2 tu_3]"}, time.Time{}},
		{"a stale item agentd records no stop for owes only calls made by this pass", Item{Work: "w", Session: "s", State: StateActive, Beat: now.Add(-7 * time.Minute)}, nil,
			[]Event{{Type: ToolUse, ID: "dead", At: now.Add(-time.Minute)}, {Type: ToolUse, ID: "live", At: now.Add(time.Second)}}, []string{"stop w", "send s [dead]"}, time.Time{}},
		{"a failed stop answers nothing", Item{Work: "w", Session: "s", State: StateActive, Beat: now.Add(-7 * time.Minute)}, errors.New("503"), nil, []string{"stop w"}, time.Time{}},
		{"a stale item owes calls made by agentd's stop, though its clock lags this one", Item{Work: "w", Session: "s", State: StateActive, Beat: now.Add(-7 * time.Minute)}, nil,
			[]Event{{Type: ToolUse, ID: "dead", At: now.Add(-time.Minute)}, {Type: ToolUse, ID: "live", At: now.Add(-10 * time.Second)}}, []string{"stop w", "send s [dead]"}, now.Add(-30 * time.Second)},
		{"a stale item owes calls made by agentd's stop, though its clock runs ahead", Item{Work: "w", Session: "s", State: StateActive, Beat: now.Add(-7 * time.Minute)}, nil,
			[]Event{{Type: ToolUse, ID: "dead", At: now.Add(2 * time.Second)}}, []string{"stop w", "send s [dead]"}, now.Add(5 * time.Second)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := c.log
			if log == nil {
				log = owes
			}
			f := &fakeAgentd{items: []Item{c.item}, events: map[ids.SessionID][]Event{"s": log}, stopErr: map[ids.WorkID]error{"w": c.stop}, stoppedAt: c.stoppedAt}
			r := newReaper(t, f)
			r.lastPass = now.Add(-time.Minute)
			r.passAt(t.Context(), now)
			if !slices.Equal(f.calls, c.want) {
				t.Errorf("calls %v, want %v", f.calls, c.want)
			}
		})
	}
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
		if f.read() != 3 {
			t.Fatalf("first pass read %d pages, want 3", f.read())
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		cancel()
		if pages := f.read(); pages != 6 {
			t.Errorf("read %d pages after one interval, want 6", pages)
		}
	})
}

// TestStatesOnlyStepForward holds the reaper's order (CV-3): no state converts to another, and
// each state's only step is the next one's.
func TestStatesOnlyStepForward(t *testing.T) {
	cases := []struct {
		state   reflect.Type
		methods []string
	}{
		{reflect.TypeFor[Stale](), []string{"Stop"}},
		{reflect.TypeFor[Stopped](), []string{"Answer"}},
		{reflect.TypeFor[Answered](), []string{"Calls", "Reaped"}},
	}
	for _, c := range cases {
		var got []string
		for m := range c.state.Methods() {
			got = append(got, m.Name)
		}
		if !slices.Equal(got, c.methods) {
			t.Errorf("%s has methods %v, want %v", c.state.Name(), got, c.methods)
		}
		for _, other := range cases {
			if other.state != c.state && c.state.ConvertibleTo(other.state) {
				t.Errorf("%s converts to %s", c.state.Name(), other.state.Name())
			}
		}
	}
}
