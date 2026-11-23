package main

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/fetch"
)

// TestFetchLogsEndings holds the level fetch ending without its item logs at: INFO for a refusal
// and a stop and WARN for the handoff wait, which the controller handles, and ERROR, from main,
// for a malformed item a person must look at.
func TestFetchLogsEndings(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		stopped bool
		// level is the line fetch logs; empty when main logs the error, at ERROR.
		level string
	}{
		{"a refusal", http.StatusForbidden, "", false, "INFO"},
		{"a stop", http.StatusServiceUnavailable, "", true, "INFO"},
		{"the handoff wait", http.StatusServiceUnavailable, "", false, "WARN"},
		{"a malformed item", http.StatusOK, `{"session_id":"sesn_1"}`, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				w.Write([]byte(c.body))
			}))
			t.Cleanup(srv.Close)
			wait := time.Duration(1)
			if c.stopped {
				wait = time.Hour
			}
			for _, v := range fetch.Env(srv.URL, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})), wait) {
				t.Setenv(v.Name, v.Value)
			}
			t.Setenv(fetch.EnvNonce, "n1")
			t.Setenv("CMA_SHARED_DIR", t.TempDir())
			ctx, cancel := context.WithCancel(t.Context())
			if c.stopped {
				cancel()
			}
			defer cancel()
			var out bytes.Buffer
			err := runFetch(ctx, slog.New(slog.NewTextHandler(&out, nil)))
			logged := c.level != ""
			if err == nil || errors.Is(err, errLogged) != logged || (logged && !strings.Contains(out.String(), "level="+c.level)) {
				t.Errorf("err %v, log %q; want it logged at %q", err, out.String(), c.level)
			}
		})
	}
}
