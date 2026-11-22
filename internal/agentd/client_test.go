package agentd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/reap"
)

// fake answers each "METHOD path" with its body and status, and records request bodies.
type fake struct {
	routes map[string]answer
	bodies map[string]string
	query  map[string]string
}

func serve(t *testing.T, f *fake) *Client {
	t.Helper()
	f.bodies, f.query = map[string]string{}, map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		f.bodies[key], f.query[key] = string(b), r.URL.RawQuery
		route, ok := f.routes[key]
		if !ok {
			route.status, route.body = 500, `{"type":"error","error":{"type":"api_error","message":"no route"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(route.status)
		w.Write([]byte(route.body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "env_1", "key", 5*time.Second)
}

type answer struct {
	status int
	body   string
}

func route(status int, body string) answer { return answer{status, body} }

func notFound(msg string) string {
	return `{"type":"error","error":{"type":"not_found_error","message":"` + msg + `"}}`
}

// TestExists holds SC-9's reading: only agentd's own 404 naming the session is gone.
func TestExists(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		exists bool
		err    bool
	}{
		{"live", 200, `{"id":"sesn_1","status":"idle"}`, true, false},
		{"agentd's 404", 404, notFound("Session not found: sesn_1"), false, false},
		{"another 404 message", 404, notFound("session not found"), true, true},
		{"a proxy's 404", 404, `not found`, true, true},
		{"5xx", 503, `{"type":"error","error":{"type":"overloaded_error","message":"x"}}`, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fake{routes: map[string]answer{"GET /v1/sessions/sesn_1": route(c.status, c.body)}}
			exists, err := serve(t, f).Exists(t.Context(), "sesn_1")
			if exists != c.exists || (err != nil) != c.err {
				t.Errorf("exists %v, err %v", exists, err)
			}
		})
	}
}

// TestPoll holds SC-1: block_ms 999 and reclaim_older_than_ms as given.
func TestPoll(t *testing.T) {
	f := &fake{routes: map[string]answer{"GET /v1/environments/env_1/work/poll": route(200, `{"id":"sesn_1","type":"work","data":{"id":"sesn_1","type":"session"},"secret":"s3"}`)}}
	c := serve(t, f)
	session, secret, ok, err := c.Poll(t.Context(), 240*time.Second)
	if err != nil || !ok || session != "sesn_1" || secret.Reveal() != "s3" {
		t.Fatalf("poll %v %v %v", session, ok, err)
	}
	q := f.query["GET /v1/environments/env_1/work/poll"]
	if !strings.Contains(q, "block_ms=999") || !strings.Contains(q, "reclaim_older_than_ms=240000") {
		t.Errorf("query %q", q)
	}
}

// TestStopCarriesStatus holds SD-1's reading of 401 and 409.
func TestStopCarriesStatus(t *testing.T) {
	f := &fake{routes: map[string]answer{"POST /v1/environments/env_1/work/sesn_1/stop": route(409, `{"type":"error","error":{"type":"invalid_request_error","message":"stopped"}}`)}}
	err := serve(t, f).Stop(t.Context(), "sesn_1")
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok || apiErr.HTTPStatus() != 409 || strings.Contains(err.Error(), "key") {
		t.Errorf("stop error %v", err)
	}
	if body := f.bodies["POST /v1/environments/env_1/work/sesn_1/stop"]; body != `{"force":true}` {
		t.Errorf("stop body %q", body)
	}
}

// TestListAndSend hold the reaper's reads and the error results it posts (RP-1, RP-3).
func TestListAndSend(t *testing.T) {
	f := &fake{routes: map[string]answer{
		"GET /v1/environments/env_1/work": route(200, `{"data":[
			{"id":"sesn_1","data":{"id":"sesn_1"},"state":"active","latest_heartbeat_at":"2026-11-22T10:00:00Z"},
			{"id":"sesn_2","data":{"id":"sesn_2"},"state":"starting","acknowledged_at":"2026-11-22T09:00:00Z"}],"next_page":"p2"}`),
		"POST /v1/sessions/sesn_1/events": route(200, `{"data":[]}`),
	}}
	c := serve(t, f)
	items, next, err := c.List(t.Context(), "p1")
	if err != nil || next != "p2" || len(items) != 2 {
		t.Fatalf("list %v %q %v", items, next, err)
	}
	want := reap.Item{Session: "sesn_2", State: "starting", Beat: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)}
	if items[1] != want {
		t.Errorf("item %+v, want %+v", items[1], want)
	}
	if q := f.query["GET /v1/environments/env_1/work"]; !strings.Contains(q, "page=p1") || !strings.Contains(q, "limit=100") {
		t.Errorf("list query %q", q)
	}
	if err := c.Events().Send(t.Context(), "sesn_1", []string{"tu_1", "tu_2"}); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Events []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			IsError   bool   `json:"is_error"`
		} `json:"events"`
	}
	json.Unmarshal([]byte(f.bodies["POST /v1/sessions/sesn_1/events"]), &sent)
	if len(sent.Events) != 2 || sent.Events[1].ToolUseID != "tu_2" || !sent.Events[0].IsError || sent.Events[0].Type != "user.tool_result" {
		t.Errorf("sent %+v", sent)
	}
}
