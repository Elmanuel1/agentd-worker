package agentd

import (
	"context"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
	"github.com/Elmanuel1/agentd-worker/internal/reap"
)

// pageSize is the items per page of the reaper's list.
const pageSize = 100

// errorText is the content of the error result posted for an owed call.
const errorText = "The worker running this tool call stopped before it finished."

// List returns one page of the environment's items.
func (c *Client) List(ctx context.Context, cursor string) ([]reap.Item, string, error) {
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
		items = append(items, reap.Item{Session: session, State: string(w.State), Beat: beat, StoppedAt: parse(w.StoppedAt)})
	}
	return items, page.NextPage, nil
}

// Events is the session event routes, with the client's credential.
type Events struct {
	api anthropic.Client
}

func (c *Client) Events() Events { return Events{api: c.api} }

// List returns every event of the session, oldest first.
func (e Events) List(ctx context.Context, session ids.SessionID) ([]reap.Event, error) {
	pager := e.api.Beta.Sessions.Events.ListAutoPaging(ctx, string(session), anthropic.BetaSessionEventListParams{
		Limit: param.NewOpt(int64(1000)),
		Order: anthropic.BetaSessionEventListParamsOrderAsc,
	})
	var out []reap.Event
	for pager.Next() {
		ev := pager.Current()
		out = append(out, reap.Event{Type: ev.Type, ID: ev.ID, ToolUseID: ev.ToolUseID})
	}
	return out, wrap(pager.Err())
}

// Send posts one error user.tool_result per call, in one request.
func (e Events) Send(ctx context.Context, session ids.SessionID, toolUseIDs []string) error {
	events := make([]anthropic.BetaManagedAgentsEventParamsUnion, 0, len(toolUseIDs))
	for _, id := range toolUseIDs {
		events = append(events, anthropic.BetaManagedAgentsEventParamsUnion{OfUserToolResult: &anthropic.BetaManagedAgentsUserToolResultEventParams{
			ToolUseID: id,
			IsError:   param.NewOpt(true),
			Content: []anthropic.BetaManagedAgentsUserToolResultEventParamsContentUnion{
				{OfText: &anthropic.BetaManagedAgentsTextBlockParam{Text: errorText}},
			},
		}})
	}
	_, err := e.api.Beta.Sessions.Events.Send(ctx, string(session), anthropic.BetaSessionEventSendParams{Events: events})
	return wrap(err)
}

func parse(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}
