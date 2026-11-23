package main

import (
	"bytes"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Elmanuel1/agentd-worker/internal/fetch"
)

// TestFetchLogsHandledEndings holds that fetch ending without its item, which the controller
// handles, logs at INFO for a refusal and WARN for the handoff wait, not ERROR.
func TestFetchLogsHandledEndings(t *testing.T) {
	cases := []struct {
		name   string
		status int
		level  string
	}{
		{"a refusal", http.StatusForbidden, "INFO"},
		{"the handoff wait", http.StatusServiceUnavailable, "WARN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(c.status) }))
			t.Cleanup(srv.Close)
			for _, v := range fetch.Env(srv.URL, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})), 1) {
				t.Setenv(v.Name, v.Value)
			}
			t.Setenv(fetch.EnvNonce, "n1")
			t.Setenv("CMA_SHARED_DIR", t.TempDir())
			var out bytes.Buffer
			err := runFetch(t.Context(), slog.New(slog.NewTextHandler(&out, nil)))
			if !errors.Is(err, errLogged) || !strings.Contains(out.String(), "level="+c.level) {
				t.Errorf("err %v, log %q; want it logged at %s", err, out.String(), c.level)
			}
		})
	}
}
