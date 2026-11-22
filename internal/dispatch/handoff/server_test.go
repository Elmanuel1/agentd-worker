package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

type locator struct {
	addr    netip.Addr
	running bool
	err     error
}

func (l locator) Address(context.Context, ids.SessionID) (netip.Addr, bool, error) {
	return l.addr, l.running, l.err
}

// TestWorkItem holds SC-3 and SC-4: only the held nonce, from the running sandbox's own address, collects.
func TestWorkItem(t *testing.T) {
	own := netip.MustParseAddr("192.0.2.1")
	cases := []struct {
		name   string
		nonce  ids.Nonce
		loc    locator
		method string
		want   int
		spent  bool
	}{
		{"collected", "n1", locator{own, true, nil}, http.MethodGet, http.StatusOK, true},
		{"stray nonce", "n2", locator{own, true, nil}, http.MethodGet, http.StatusForbidden, false},
		{"empty nonce", "", locator{own, true, nil}, http.MethodGet, http.StatusForbidden, false},
		{"wrong address", "n1", locator{netip.MustParseAddr("192.0.2.9"), true, nil}, http.MethodGet, http.StatusForbidden, false},
		{"stopped sandbox", "n1", locator{own, false, nil}, http.MethodGet, http.StatusForbidden, false},
		{"lookup failed", "n1", locator{own, true, errors.New("engine down")}, http.MethodGet, http.StatusForbidden, false},
		{"head spends nothing", "n1", locator{own, true, nil}, http.MethodHead, http.StatusMethodNotAllowed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := NewStore(c.loc)
			collected := store.Open("sesn_1", "work_1", "n1", ids.NewSecret("the-secret"))
			srv := NewServer(store, "env_1", "https://agentd.test", slog.New(slog.NewJSONHandler(io.Discard, nil)))
			req := httptest.NewRequest(c.method, "/work-item", nil)
			req.Header.Set(NonceHeader, string(c.nonce))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status %d, want %d", rec.Code, c.want)
			}
			if !c.spent {
				if !store.Drop("sesn_1") {
					t.Error("a refused request spent the nonce")
				}
				return
			}
			<-collected
			again := httptest.NewRecorder()
			srv.Handler().ServeHTTP(again, req)
			if again.Code != http.StatusForbidden {
				t.Errorf("a spent nonce got %d", again.Code)
			}
			var got Item
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			want := Item{SessionID: "sesn_1", WorkID: "work_1", EnvironmentID: "env_1", BaseURL: "https://agentd.test", Secret: "the-secret"}
			if got != want {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestDropAfterCollectionReportsCollected(t *testing.T) {
	store := NewStore(locator{netip.MustParseAddr("192.0.2.1"), true, nil})
	store.Open("sesn_1", "work_1", "n1", ids.NewSecret("s"))
	if !store.Drop("sesn_1") {
		t.Fatal("an uncollected item was not dropped")
	}
	if store.Drop("sesn_1") {
		t.Error("a dropped item is still held")
	}
}
