package fetch

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch/handoff"
)

const body = `{"session_id":"sesn_1","work_id":"sesn_1","environment_id":"env_1","base_url":"https://agentd.test","secret":"s3cret"}`

// script answers each request with the next status, the last one repeating; 0 is a dial error.
type script struct {
	codes []int
	calls int
	nonce string
}

func (s *script) Do(r *http.Request) (*http.Response, error) {
	s.nonce = r.Header.Get(handoff.NonceHeader)
	code := s.codes[min(s.calls, len(s.codes)-1)]
	s.calls++
	if code == 0 {
		return nil, errors.New("connection refused")
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// TestRun holds FE-1 and FE-2.
func TestRun(t *testing.T) {
	cases := []struct {
		name  string
		codes []int
		err   error
		took  time.Duration
	}{
		{"collected first time", []int{200}, nil, 0},
		{"retries every 2 s until collected", []int{0, 503, 200}, nil, 4 * time.Second},
		{"a refusal exits at once", []int{0, 403}, ErrRefused, 2 * time.Second},
		{"the handoff wait ends it", []int{0}, ErrTimeout, 120 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				cfg := Config{URL: "https://ctl", Nonce: "n1", Dir: dir, UID: os.Getuid(), GID: os.Getgid(), Every: 2 * time.Second, Wait: 120 * time.Second}
				s := &script{codes: c.codes}
				start := time.Now()
				err := Run(t.Context(), s, cfg)
				if !errors.Is(err, c.err) || time.Since(start) != c.took || s.nonce != "n1" {
					t.Fatalf("err %v after %v, want %v after %v", err, time.Since(start), c.err, c.took)
				}
				if c.err != nil {
					if _, err := os.Stat(filepath.Join(dir, "work-item", "secret")); err == nil {
						t.Error("a failed fetch left a secret")
					}
					return
				}
				checkFiles(t, dir)
			})
		})
	}
}

// TestRunNeedsPositiveWaits holds that fetch refuses a config it would spin or give up at once on.
func TestRunNeedsPositiveWaits(t *testing.T) {
	for _, cfg := range []Config{{Every: 0, Wait: time.Second}, {Every: time.Second, Wait: 0}} {
		s := &script{codes: []int{200}}
		if err := Run(t.Context(), s, cfg); err == nil || s.nonce != "" {
			t.Errorf("every %s, wait %s: %v after a request, want a refusal before one", cfg.Every, cfg.Wait, err)
		}
	}
}

func checkFiles(t *testing.T, dir string) {
	t.Helper()
	shared, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if shared.Mode()&os.ModeSticky == 0 || shared.Mode().Perm() != 0o775 {
		t.Errorf("shared directory %v: want sticky 0775, so the worker cannot replace work-item", shared.Mode())
	}
	want := map[string]string{"session_id": "sesn_1", "work_id": "sesn_1", "environment_id": "env_1", "base_url": "https://agentd.test", "secret": "s3cret"}
	for name, value := range want {
		path := filepath.Join(dir, "work-item", name)
		got, err := os.ReadFile(path)
		if err != nil || string(got) != value {
			t.Errorf("%s = %q, %v", name, got, err)
			continue
		}
		info, _ := os.Stat(path)
		st := info.Sys().(*syscall.Stat_t)
		if info.Mode().Perm() != 0o600 || int(st.Uid) != os.Getuid() {
			t.Errorf("%s mode %v uid %d", name, info.Mode().Perm(), st.Uid)
		}
	}
}
