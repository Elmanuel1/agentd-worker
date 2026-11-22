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

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// NonceHeader carries the nonce, under the name Anthropic's dispatcher reads.
const NonceHeader = "X-CMA-Assignment-Nonce"

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

type item struct {
	SessionID     string `json:"session_id"`
	WorkID        string `json:"work_id"`
	EnvironmentID string `json:"environment_id"`
	BaseURL       string `json:"base_url"`
	Secret        string `json:"secret"`
}

// Run asks for the item every cfg.Every until it gets it, then writes it.
func Run(ctx context.Context, client Doer, cfg Config) error {
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

func get(ctx context.Context, client Doer, cfg Config) (item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/work-item", nil)
	if err != nil {
		return item{}, err
	}
	req.Header.Set(NonceHeader, string(cfg.Nonce))
	resp, err := client.Do(req)
	if err != nil {
		return item{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return item{}, ErrRefused
	default:
		return item{}, fmt.Errorf("controller answered %d", resp.StatusCode)
	}
	var it item
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&it); err != nil {
		return item{}, fmt.Errorf("decoding work item: %w", err)
	}
	if _, err := ids.ParseSessionID(it.SessionID); err != nil || it.WorkID == "" || it.EnvironmentID == "" || it.BaseURL == "" || it.Secret == "" {
		return item{}, errors.Join(ErrRefused, errors.New("work item is missing a field"))
	}
	return it, nil
}

// write leaves one value per file, mode 0600, owned by the worker's user, the secret last: its
// presence tells the worker the rest is there. The work-item directory stays fetch's, so the
// worker can read it and not change it, as Anthropic's script checks; the shared directory is
// the worker's group's, so the script can ask for its run to be recorded.
func write(cfg Config, it item) error {
	dir := filepath.Join(cfg.Dir, "work-item")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chown(cfg.Dir, os.Getuid(), cfg.GID); err != nil {
		return err
	}
	if err := os.Chmod(cfg.Dir, 0o775); err != nil {
		return err
	}
	fields := [][2]string{
		{"session_id", it.SessionID}, {"work_id", it.WorkID}, {"environment_id", it.EnvironmentID},
		{"base_url", it.BaseURL}, {"secret", it.Secret},
	}
	for _, f := range fields {
		if err := writeFile(filepath.Join(dir, f[0]), f[1], cfg.UID, cfg.GID); err != nil {
			return fmt.Errorf("writing %s: %w", f[0], err)
		}
	}
	return nil
}

func writeFile(path, value string, uid, gid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o600); err != nil {
		return err
	}
	if err := os.Chown(tmp, uid, gid); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
