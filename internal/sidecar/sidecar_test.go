package sidecar

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

type apiError int

func (e apiError) Error() string   { return "agentd error" }
func (e apiError) HTTPStatus() int { return int(e) }

type items struct {
	err     error
	stopped []ids.SessionID
	token   string
}

// TestStop holds SD-1: the item is stopped when the sandbox stops, 401 and 409 counting as stopped.
func TestStop(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"stopped", nil, true},
		{"401 is already stopped", apiError(401), true},
		{"409 is already stopped", apiError(409), true},
		{"500 is an error", apiError(500), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				writeItem(t, dir)
				it := &items{err: c.err}
				connect := func(_ string, _ ids.EnvironmentID, tok ids.WorkToken) Items {
					it.token = tok.Reveal()
					return it
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error)
				go func() { done <- Run(ctx, Config{Dir: dir, Every: time.Second}, connect) }()
				time.Sleep(5 * time.Second)
				if len(it.stopped) != 0 {
					t.Fatal("stopped before the sandbox stopped")
				}
				cancel()
				err := <-done
				if (err == nil) != c.want || len(it.stopped) != 1 || it.token != "tok" {
					t.Errorf("err %v, stopped %v, token %q", err, it.stopped, it.token)
				}
			})
		})
	}
}

func (i *items) Stop(_ context.Context, s ids.SessionID) error {
	i.stopped = append(i.stopped, s)
	return i.err
}

// TestRecordsTheRun answers Anthropic's script, which waits for worker-ran before it runs ant.
func TestRecordsTheRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		writeItem(t, dir)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go Run(ctx, Config{Dir: dir, Every: time.Second}, func(string, ids.EnvironmentID, ids.WorkToken) Items { return &items{} })
		ran := filepath.Join(dir, "work-item", "worker-ran")
		time.Sleep(3 * time.Second)
		if exists(ran) {
			t.Fatal("recorded a run nobody asked for")
		}
		os.WriteFile(filepath.Join(dir, "worker-starting"), nil, 0o600)
		time.Sleep(2 * time.Second)
		if !exists(ran) {
			t.Error("did not record the run")
		}
	})
}

func TestNoItemStopsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := Run(ctx, Config{Dir: t.TempDir(), Every: time.Second}, func(string, ids.EnvironmentID, ids.WorkToken) Items {
		t.Fatal("connected with no work item")
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}

func writeItem(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"session_id": "sesn_1", "environment_id": "env_1", "base_url": "https://agentd.test",
		"secret": base64.RawURLEncoding.EncodeToString([]byte(`{"sessions_token":"tok"}`)),
	}
	if err := os.MkdirAll(filepath.Join(dir, "work-item"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, v := range files {
		if err := os.WriteFile(filepath.Join(dir, "work-item", name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
