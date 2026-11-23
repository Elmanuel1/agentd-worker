// Package fetch collects a sandbox's work item from the controller and leaves it in the shared
// directory for the worker container, which starts only once fetch exits 0.
package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch/handoff"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// The fetch container's environment, which its launcher sets.
const (
	EnvURL   = "AGENTD_HANDOFF_URL"
	EnvNonce = "AGENTD_HANDOFF_NONCE"
	EnvCA    = "AGENTD_HANDOFF_CA"
	EnvWait  = "AGENTD_HANDOFF_WAIT"
)

// Var is one environment variable.
type Var struct{ Name, Value string }

// Env is every variable a launcher gives the fetch container but EnvNonce, which each launch adds.
func Env(url, ca string, wait time.Duration) []Var {
	return []Var{{EnvURL, url}, {EnvCA, ca}, {EnvWait, wait.String()}}
}

var (
	// ErrRefused means the controller answered 403: this sandbox will never get the item.
	ErrRefused = errors.New("the controller refused the work item")
	// ErrTimeout means the handoff wait passed with no item.
	ErrTimeout = errors.New("no work item within the handoff wait")
)

// Doer sends the GET /work-item request.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Config holds fetch's settings.
type Config struct {
	// URL is the controller's base URL.
	URL   string
	Nonce ids.Nonce
	// Dir is the shared directory, /run/cma.
	Dir string
	// UID and GID own the written files: the worker container's user.
	UID, GID int
	Every    time.Duration
	Wait     time.Duration
}

// Run asks for the item every cfg.Every until it gets it, then writes it.
func Run(ctx context.Context, client Doer, cfg Config) error {
	if cfg.Every <= 0 || cfg.Wait <= 0 {
		return errors.New("fetch's retry interval and handoff wait must be positive")
	}
	deadline := time.Now().Add(cfg.Wait)
	for {
		it, err := get(ctx, client, cfg)
		if err == nil {
			return write(cfg, it)
		}
		if errors.Is(err, ErrRefused) {
			return err
		}
		if !time.Now().Before(deadline) {
			return errors.Join(ErrTimeout, err)
		}
		t := time.NewTimer(cfg.Every)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func get(ctx context.Context, client Doer, cfg Config) (handoff.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/work-item", nil)
	if err != nil {
		return handoff.Item{}, err
	}
	req.Header.Set(handoff.NonceHeader, string(cfg.Nonce))
	resp, err := client.Do(req)
	if err != nil {
		return handoff.Item{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return handoff.Item{}, ErrRefused
	default:
		return handoff.Item{}, fmt.Errorf("controller answered %d", resp.StatusCode)
	}
	var it handoff.Item
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&it); err != nil {
		return handoff.Item{}, fmt.Errorf("decoding work item: %w", err)
	}
	if _, err := ids.ParseSessionID(string(it.SessionID)); err != nil || it.WorkID == "" || it.EnvironmentID == "" || it.BaseURL == "" || it.Secret == "" {
		return handoff.Item{}, errors.Join(ErrRefused, errors.New("work item is missing a field"))
	}
	return it, nil
}

// write leaves one value per file, mode 0600, owned by the worker's user, the secret last: its
// presence tells the worker the rest is there. The work-item directory stays fetch's, so the
// worker can read it and not change it, as Anthropic's script checks; the shared directory is
// the worker's group's, so the script can ask for its run to be recorded.
func write(cfg Config, it handoff.Item) error {
	dir := filepath.Join(cfg.Dir, "work-item")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chown(cfg.Dir, os.Getuid(), cfg.GID); err != nil {
		return err
	}
	// Sticky, so the worker's group can add files beside work-item but not rename or replace it.
	if err := os.Chmod(cfg.Dir, os.ModeSticky|0o775); err != nil {
		return err
	}
	fields := [][2]string{
		{"session_id", string(it.SessionID)}, {"work_id", string(it.WorkID)}, {"environment_id", string(it.EnvironmentID)},
		{"base_url", it.BaseURL}, {"secret", it.Secret},
	}
	for _, f := range fields {
		path := filepath.Join(dir, f[0])
		err := os.WriteFile(path+".tmp", []byte(f[1]), 0o600)
		if err == nil {
			err = os.Chown(path+".tmp", cfg.UID, cfg.GID)
		}
		if err == nil {
			err = os.Rename(path+".tmp", path)
		}
		if err != nil {
			return fmt.Errorf("writing %s: %w", f[0], err)
		}
	}
	return nil
}
