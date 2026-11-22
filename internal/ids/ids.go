// Package ids holds the typed ids and secrets every other package passes around.
package ids

import (
	"crypto/rand"
	"errors"
	"fmt"
)

// ErrInvalid is returned for an id that is empty or holds a character no backend accepts.
var ErrInvalid = errors.New("invalid id")

// SessionID is a session's id, which is also its work item's id.
type SessionID string

// ParseSessionID checks s before it becomes a label, a tag or a family name.
func ParseSessionID(s string) (SessionID, error) {
	if !safe(s) {
		return "", fmt.Errorf("session %q: %w", s, ErrInvalid)
	}
	return SessionID(s), nil
}

// EnvironmentID is the environment or queue the controller serves.
type EnvironmentID string

// Nonce is the one-time value a sandbox's fetch container presents to collect its item.
type Nonce string

// NewNonce returns 26 base32 characters, 130 bits from crypto/rand.
func NewNonce() Nonce {
	return Nonce(rand.Text())
}

// safe reports whether s fits ECS startedBy, an EFS client token and a task-definition family name.
func safe(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		ok := c == '_' || c == '-' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}
