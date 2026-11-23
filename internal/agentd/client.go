// Package agentd is the adapter for agentd's work, session and event routes, on the official SDK.
// agentd matches the Managed Agents API, so the same adapter runs against live.
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"

	"github.com/Elmanuel1/agentd-worker/internal/dispatch"
	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// blockMs is how long one poll blocks server-side.
const blockMs = 999

// Client calls one environment's routes with one credential: an environment key, the runtime
// key, or a session's work token.
type Client struct {
	api    anthropic.Client
	env    ids.EnvironmentID
	worker string
	// itemToken is set when the credential is the item's own work token, which ends with the item.
	itemToken bool
}

// New builds a client that only ever dials baseURL.
func New(baseURL string, env ids.EnvironmentID, credential string, timeout time.Duration) *Client {
	return &Client{
		api: anthropic.NewClient(
			option.WithoutEnvironmentDefaults(),
			option.WithBaseURL(baseURL),
			option.WithAuthToken(credential),
			option.WithMaxRetries(2),
			option.WithRequestTimeout(timeout),
		),
		env: env,
	}
}

// ForItem builds a client on a work item's own token, as its sidecar holds.
func ForItem(baseURL string, env ids.EnvironmentID, token ids.WorkToken, timeout time.Duration) *Client {
	c := New(baseURL, env, token.Reveal(), timeout)
	c.itemToken = true
	return c
}

// WithWorker names the poller in each poll, so the queue's stats count it in workers_polling.
func (c *Client) WithWorker(id string) *Client {
	c.worker = id
	return c
}

// Poll claims one item, or returns ok false when no session's item arrived within the block. The
// queue also sends healthcheck items, which the SDK's worker acks and stops; so does this.
func (c *Client) Poll(ctx context.Context, reclaimAfter time.Duration) (dispatch.Work, bool, error) {
	params := anthropic.BetaEnvironmentWorkPollParams{
		BlockMs:            param.NewOpt(int64(blockMs)),
		ReclaimOlderThanMs: param.NewOpt(reclaimAfter.Milliseconds()),
	}
	if c.worker != "" {
		params.AnthropicWorkerID = param.NewOpt(c.worker)
	}
	work, err := c.api.Beta.Environments.Work.Poll(ctx, string(c.env), params)
	if err != nil {
		return dispatch.Work{}, false, wrap(err)
	}
	if work == nil || work.ID == "" {
		return dispatch.Work{}, false, nil
	}
	id := ids.WorkID(work.ID)
	if work.Data.Type != "session" {
		return dispatch.Work{}, false, errors.Join(c.Ack(ctx, id), c.Stop(ctx, id))
	}
	session, err := ids.ParseSessionID(work.Data.ID)
	if err != nil {
		return dispatch.Work{}, false, err
	}
	return dispatch.Work{ID: id, Session: session, Secret: ids.NewSecret(work.Secret)}, true, nil
}

func (c *Client) Ack(ctx context.Context, work ids.WorkID) error {
	_, err := c.api.Beta.Environments.Work.Ack(ctx, string(work), anthropic.BetaEnvironmentWorkAckParams{EnvironmentID: string(c.env)})
	return wrap(err)
}

func (c *Client) Depth(ctx context.Context) (int64, error) {
	stats, err := c.api.Beta.Environments.Work.Stats(ctx, string(c.env), anthropic.BetaEnvironmentWorkStatsParams{})
	if err != nil {
		return 0, wrap(err)
	}
	return stats.Depth, nil
}

// Stop force-stops the item, as ant does on exit. A 409 means another stopped it first, as the SDK's
// worker reads it, and so does a 401 to the item's own token, which ends with the item.
func (c *Client) Stop(ctx context.Context, work ids.WorkID) error {
	_, err := c.stop(ctx, work)
	apiErr, _ := errors.AsType[*anthropic.Error](err)
	if apiErr != nil && (apiErr.StatusCode == http.StatusConflict || (c.itemToken && apiErr.StatusCode == http.StatusUnauthorized)) {
		return nil
	}
	return wrap(err)
}

func (c *Client) stop(ctx context.Context, work ids.WorkID) (*anthropic.BetaSelfHostedWork, error) {
	return c.api.Beta.Environments.Work.Stop(ctx, string(work), anthropic.BetaEnvironmentWorkStopParams{
		EnvironmentID:                 string(c.env),
		BetaSelfHostedWorkStopRequest: anthropic.BetaSelfHostedWorkStopRequestParam{Force: param.NewOpt(true)},
	})
}

// State counts a session gone only on agentd's own 404 naming it, as Anthropic's dispatcher reads
// it: a proxy's 404 or any other failure is an error, and the caller keeps the sandbox.
func (c *Client) State(ctx context.Context, session ids.SessionID) (dispatch.Session, error) {
	got, err := c.api.Beta.Sessions.Get(ctx, string(session), anthropic.BetaSessionGetParams{})
	if err == nil {
		return dispatch.Session{
			Terminated: got.Status == anthropic.BetaManagedAgentsSessionStatusTerminated,
			Idle:       got.Status == anthropic.BetaManagedAgentsSessionStatusIdle,
			Since:      got.UpdatedAt,
		}, nil
	}
	apiErr, ok := errors.AsType[*anthropic.Error](err)
	if !ok || apiErr.StatusCode != http.StatusNotFound || apiErr.Type() != anthropic.ErrorTypeNotFoundError {
		return dispatch.Session{}, wrap(err)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(apiErr.RawJSON()), &body) == nil && body.Error.Message == "Session not found: "+string(session) {
		return dispatch.Session{Gone: true}, nil
	}
	return dispatch.Session{}, wrap(err)
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	// The SDK's own message quotes the request; this one names only the answer.
	if apiErr, ok := errors.AsType[*anthropic.Error](err); ok {
		return fmt.Errorf("agentd answered %d: %s", apiErr.StatusCode, apiErr.Type())
	}
	return err
}
