package agentd

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/reap"
)

// pageSize is the Managed Agents list routes' largest page, as agentd and live cap it.
const pageSize = 100

// errorText is the content of the error result posted for an owed call.
const errorText = "The worker running this tool call stopped before it finished."

// Work is the environment's work items as the reaper reads and stops them, with the client's credential.
type Work struct{ c *Client }

func (c *Client) Work() Work { return Work{c: c} }

// List returns one page of the environment's items.
func (w Work) List(ctx context.Context, cursor string) ([]reap.Item, string, error) {
	c := w.c
	params := anthropic.BetaEnvironmentWorkListParams{Limit: param.NewOpt(int64(pageSize))}
	if cursor != "" {
		params.Page = param.NewOpt(cursor)
	}
	page, err := c.api.Beta.Environments.Work.List(ctx, string(c.env), params)
	if err != nil {
		return nil, "", wrap(err)
	}
	items := make([]reap.Item, 0, len(page.Data))
	for _, w := range page.Data {
		session, err := ids.ParseSessionID(w.Data.ID)
		if err != nil {
			continue
		}
		beat := parse(w.LatestHeartbeatAt)
		if beat.IsZero() {
			beat = parse(w.AcknowledgedAt)
		}
		items = append(items, reap.Item{
			Work: ids.WorkID(w.ID), Session: session, State: string(w.State), Beat: beat, StoppedAt: parse(w.StoppedAt),
		})
	}
	return items, page.NextPage, nil
}

// Stop force-stops the item and returns the stop agentd records, in the clock that stamps the
// session's events. agentd answers an item already stopped with its first stop; live may answer
// 409, and then the item is read for it.
func (w Work) Stop(ctx context.Context, work ids.WorkID) (time.Time, error) {
	got, err := w.c.stop(ctx, work)
	if apiErr, ok := errors.AsType[*anthropic.Error](err); ok && apiErr.StatusCode == http.StatusConflict {
		got, err = w.c.api.Beta.Environments.Work.Get(ctx, string(work), anthropic.BetaEnvironmentWorkGetParams{EnvironmentID: string(w.c.env)})
	}
	if err != nil {
		return time.Time{}, wrap(err)
	}
	return parse(got.StoppedAt), nil
}

// Events is the session event routes, with the client's credential.
type Events struct {
	api anthropic.Client
}

func (c *Client) Events() Events { return Events{api: c.api} }

// List returns the session's tool-call events, oldest first.
func (e Events) List(ctx context.Context, session ids.SessionID) ([]reap.Event, error) {
	var types []anthropic.BetaManagedAgentsSessionEventType
	for _, t := range reap.EventTypes() {
		types = append(types, anthropic.BetaManagedAgentsSessionEventType(t))
	}
	pager := e.api.Beta.Sessions.Events.ListAutoPaging(ctx, string(session), anthropic.BetaSessionEventListParams{
		Limit: param.NewOpt(int64(pageSize)),
		Order: anthropic.BetaSessionEventListParamsOrderAsc,
		// Only the events the reaper reads, so a long session's history costs few pages.
		Types: types,
	})
	var out []reap.Event
	for pager.Next() {
		ev := pager.Current()
		out = append(out, reap.Event{
			Type: ev.Type, ID: ev.ID, ToolUseID: ev.ToolUseID, Permission: string(ev.EvaluatedPermission),
			Thread: ev.SessionThreadID, At: ev.ProcessedAt, Verdict: string(ev.AsUserToolConfirmation().Result),
		})
	}
	return out, wrap(pager.Err())
}

// Send posts one error user.tool_result per call, each in its own request, so one the server
// refuses does not keep the others unanswered.
func (e Events) Send(ctx context.Context, session ids.SessionID, toolUseIDs []string) error {
	var errs []error
	for _, id := range toolUseIDs {
		result := anthropic.BetaManagedAgentsUserToolResultEventParams{
			ToolUseID: id,
			IsError:   param.NewOpt(true),
			Content: []anthropic.BetaManagedAgentsUserToolResultEventParamsContentUnion{
				{OfText: &anthropic.BetaManagedAgentsTextBlockParam{Text: errorText}},
			},
		}
		_, err := e.api.Beta.Sessions.Events.Send(ctx, string(session), anthropic.BetaSessionEventSendParams{
			Events: []anthropic.BetaManagedAgentsEventParamsUnion{{OfUserToolResult: &result}},
		})
		errs = append(errs, wrap(err))
	}
	return errors.Join(errs...)
}

func parse(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}
