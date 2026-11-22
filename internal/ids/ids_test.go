package ids

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretsPrintRedacted(t *testing.T) {
	secret := NewSecret(encode(t, map[string]any{"sessions_token": "sk-ant-oat01-live"}))
	token, err := secret.WorkToken()
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{"secret": secret, "token": token} {
		t.Run(name, func(t *testing.T) {
			var logged bytes.Buffer
			slog.New(slog.NewJSONHandler(&logged, nil)).Info("x", "v", v)
			jsonOut, _ := json.Marshal(map[string]any{"v": v})
			outs := []string{fmt.Sprint(v), fmt.Sprintf("%v %+v %#v %s", v, v, v, v), logged.String(), string(jsonOut)}
			for _, out := range outs {
				if strings.Contains(out, "sk-ant") || strings.Contains(out, secret.Reveal()) || !strings.Contains(out, "[redacted]") {
					t.Errorf("printed %q", out)
				}
			}
		})
	}
}

func TestWorkToken(t *testing.T) {
	cases := []struct {
		name, secret, want string
		err                error
	}{
		{"unpadded", encode(t, map[string]any{"sessions_token": "tok"}), "tok", nil},
		{"padded", base64.URLEncoding.EncodeToString([]byte(`{"sessions_token":"tok"}`)), "tok", nil},
		{"no token", encode(t, map[string]any{"version": 1}), "", ErrNoToken},
		{"not base64", "%%%", "", ErrNoToken},
		{"not json", base64.RawURLEncoding.EncodeToString([]byte("[1]")), "", ErrNoToken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewSecret(c.secret).WorkToken()
			if !errors.Is(err, c.err) || got.Reveal() != c.want {
				t.Errorf("got %q, %v; want %q, %v", got.Reveal(), err, c.want, c.err)
			}
		})
	}
}

func TestParseSessionID(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"sesn_011CZkZaBF1tNoB5wlCeusgy", true},
		{"a-b_C9", true},
		{"", false},
		{"sesn/1", false},
		{"sesn 1", false},
		{strings.Repeat("a", 65), false},
	}
	for _, c := range cases {
		_, err := ParseSessionID(c.in)
		if (err == nil) != c.ok {
			t.Errorf("ParseSessionID(%q) = %v", c.in, err)
		}
	}
}

func TestNewNonceIsUnique(t *testing.T) {
	if a, b := NewNonce(), NewNonce(); a == b || len(a) != 26 {
		t.Errorf("nonces %q and %q", a, b)
	}
}

func encode(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
