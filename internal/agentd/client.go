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

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// blockMs is how long one poll blocks server-side.
const blockMs = 999

// APIError is agentd's answer to a failed call.
type APIError struct {
	status int
	err    error
}

func (e *APIError) Error() string   { return e.err.Error() }
func (e *APIError) Unwrap() error   { return e.err }
func (e *APIError) HTTPStatus() int { return e.status }

// Client calls one environment's routes with one credential: an environment key, the runtime
// key, or a session's work token.
type Client struct {
	api anthropic.Client
	env ids.EnvironmentID
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

// Poll claims one item, or returns ok false when none arrived within the block.
func (c *Client) Poll(ctx context.Context, reclaimAfter time.Duration) (ids.SessionID, ids.Secret, bool, error) {
	work, err := c.api.Beta.Environments.Work.Poll(ctx, string(c.env), anthropic.BetaEnvironmentWorkPollParams{
		BlockMs:            param.NewOpt(int64(blockMs)),
		ReclaimOlderThanMs: param.NewOpt(reclaimAfter.Milliseconds()),
	})
	if err != nil {
		return "", ids.Secret{}, false, wrap(err)
	}
	if work == nil || work.ID == "" {
		return "", ids.Secret{}, false, nil
	}
	session, err := ids.ParseSessionID(work.Data.ID)
	if err != nil {
		return "", ids.Secret{}, false, err
	}
	return session, ids.NewSecret(work.Secret), true, nil
}

// Ack confirms the session's item.
func (c *Client) Ack(ctx context.Context, session ids.SessionID) error {
	_, err := c.api.Beta.Environments.Work.Ack(ctx, string(session), anthropic.BetaEnvironmentWorkAckParams{EnvironmentID: string(c.env)})
	return wrap(err)
}

// Depth is the queue's depth from work stats.
func (c *Client) Depth(ctx context.Context) (int64, error) {
	stats, err := c.api.Beta.Environments.Work.Stats(ctx, string(c.env), anthropic.BetaEnvironmentWorkStatsParams{})
	if err != nil {
		return 0, wrap(err)
	}
	return stats.Depth, nil
}

// Stop force-stops the session's item, as ant does on exit.
func (c *Client) Stop(ctx context.Context, session ids.SessionID) error {
	_, err := c.api.Beta.Environments.Work.Stop(ctx, string(session), anthropic.BetaEnvironmentWorkStopParams{
		EnvironmentID:                 string(c.env),
		BetaSelfHostedWorkStopRequest: anthropic.BetaSelfHostedWorkStopRequestParam{Force: param.NewOpt(true)},
	})
	return wrap(err)
}

// Exists is false only on agentd's own 404 naming the session, as Anthropic's dispatcher reads it:
// a proxy's 404 or any other failure keeps the session's sandbox.
func (c *Client) Exists(ctx context.Context, session ids.SessionID) (bool, error) {
	_, err := c.api.Beta.Sessions.Get(ctx, string(session), anthropic.BetaSessionGetParams{})
	if err == nil {
		return true, nil
	}
	if gone(err, session) {
		return false, nil
	}
	return true, wrap(err)
}

func gone(err error, session ids.SessionID) bool {
	apiErr, ok := errors.AsType[*anthropic.Error](err)
	if !ok || apiErr.StatusCode != http.StatusNotFound || apiErr.Type() != anthropic.ErrorTypeNotFoundError {
		return false
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	return json.Unmarshal([]byte(apiErr.RawJSON()), &body) == nil && body.Error.Message == "Session not found: "+string(session)
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := errors.AsType[*anthropic.Error](err); ok {
		return &APIError{status: apiErr.StatusCode, err: fmt.Errorf("agentd answered %d: %s", apiErr.StatusCode, apiErr.Type())}
	}
	return err
}
