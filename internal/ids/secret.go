package ids

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const redacted = "[redacted]"

// ErrNoToken is returned when a work secret carries no sessions token.
var ErrNoToken = errors.New("work secret carries no sessions token")

// Secret is a work item's secret payload, verbatim. It prints as [redacted].
type Secret struct{ v string }

func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal returns the value, for the one place that must send it.
func (s Secret) Reveal() string { return s.v }

func (Secret) String() string               { return redacted }
func (Secret) GoString() string             { return redacted }
func (Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (Secret) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

// WorkToken is the session's work token, from the secret's sessions_token. It prints as [redacted].
type WorkToken struct{ v string }

// Reveal returns the value, for the Authorization header.
func (t WorkToken) Reveal() string { return t.v }

func (WorkToken) String() string               { return redacted }
func (WorkToken) GoString() string             { return redacted }
func (WorkToken) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (WorkToken) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

// WorkToken decodes the secret's URL-safe base64 JSON, with or without padding, as the SDK's worker does.
func (s Secret) WorkToken() (WorkToken, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s.v, "="))
	if err != nil {
		return WorkToken{}, fmt.Errorf("decoding work secret: %w", ErrNoToken)
	}
	var payload struct {
		SessionsToken string `json:"sessions_token"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.SessionsToken == "" {
		return WorkToken{}, ErrNoToken
	}
	return WorkToken{v: payload.SessionsToken}, nil
}
