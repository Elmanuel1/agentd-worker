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

// hidden is a secret value that prints, logs and marshals as [redacted].
type hidden struct{ v string }

// Reveal returns the value, for the one place that must send it.
func (h hidden) Reveal() string { return h.v }

func (hidden) String() string               { return redacted }
func (hidden) GoString() string             { return redacted }
func (hidden) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (hidden) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

// Secret is a work item's secret payload, verbatim.
type Secret struct{ hidden }

func NewSecret(v string) Secret { return Secret{hidden{v}} }

// WorkToken is the session's work token, from the secret's sessions_token.
type WorkToken struct{ hidden }

// Key is a key read from configuration: an environment or runtime key, or a TLS private key.
type Key struct{ hidden }

func (k *Key) UnmarshalText(text []byte) error {
	k.v = string(text)
	return nil
}

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
	return WorkToken{hidden{payload.SessionsToken}}, nil
}
