package chatly

// RotateExternalSecret's optional activateAt, asserted on what the SDK SENT.
//
// The route's body schema is `.strict().default({})`: no body is the hard
// cut every existing caller gets, `activateAt` opens an overlap window, and
// any other key — `activate_at` included — is a 400. So the three things
// that can go wrong here are all on the wire: the key's spelling, a body
// appearing where none was sent before, and an instant serialised in the
// caller's zone instead of UTC. A fake that echoes the request agrees with
// all three, so these read the raw bytes.
//
// The no-options case is covered, unchanged, by the path-only sweep in
// visitors_test.go: zero bytes, no Content-Type, an Idempotency-Key.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type rotateSeen struct {
	method      string
	path        string
	query       string
	body        []byte
	idem        string
	contentType string
	requests    int
}

func rotateClient(t *testing.T, response string) (*Client, *rotateSeen) {
	t.Helper()
	seen := &rotateSeen{}
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.requests++
		seen.method = r.Method
		seen.path = r.URL.Path
		seen.query = r.URL.RawQuery
		seen.idem = r.Header.Get("Idempotency-Key")
		seen.contentType = r.Header.Get("Content-Type")
		seen.body, _ = readAll(r)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(response))
	}))
	return c, seen
}

// decodeExactlyActivateAt asserts the body is a JSON object with ONE key,
// spelled `activateAt`, and returns its value.
func decodeExactlyActivateAt(t *testing.T, body []byte) string {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body %q is not a JSON object: %v", body, err)
	}
	if _, snake := got["activate_at"]; snake {
		t.Errorf("body %s spells the key activate_at; the strict schema 400s on it", body)
	}
	if len(got) != 1 {
		t.Errorf("body %s has %d keys, want exactly one (activateAt)", body, len(got))
	}
	v, ok := got["activateAt"]
	if !ok {
		t.Fatalf("body %s carries no activateAt", body)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("activateAt = %#v, want a string", v)
	}
	return s
}

const rotateResponse = `{"signingSecret":"whsec_x","activatesAt":"2026-10-16T09:00:00.000Z"}`

func TestRotateExternalSecretSendsActivateAtRawVerbatim(t *testing.T) {
	// An offset form on purpose: verbatim means verbatim, not normalised.
	const at = "2026-10-16T11:00:00.000+02:00"
	c, seen := rotateClient(t, rotateResponse)

	if _, err := c.Bots.RotateExternalSecret(context.Background(), "bt_1",
		RotateExternalSecretOptions{ActivateAtRaw: at}); err != nil {
		t.Fatal(err)
	}

	if seen.requests != 1 {
		t.Fatalf("issued %d requests, want 1", seen.requests)
	}
	if seen.method != http.MethodPost {
		t.Errorf("method = %q, want POST", seen.method)
	}
	if seen.path != "/v1/bots/bt_1/external/rotate-secret" {
		t.Errorf("path = %q", seen.path)
	}
	if seen.query != "" {
		t.Errorf("sent query %q; activateAt belongs in the body", seen.query)
	}
	if len(seen.idem) != 36 {
		t.Errorf("Idempotency-Key = %q, want a 36-char uuid", seen.idem)
	}
	if got := decodeExactlyActivateAt(t, seen.body); got != at {
		t.Errorf("activateAt = %q, want %q verbatim", got, at)
	}
	// The raw bytes too, so a key-order or extra-whitespace change is a
	// decision rather than an accident.
	if want := `{"activateAt":"` + at + `"}`; !bytes.Equal(seen.body, []byte(want)) {
		t.Errorf("body = %s, want %s", seen.body, want)
	}
}

func TestRotateExternalSecretSerialisesATimeAsUTC(t *testing.T) {
	plus2 := time.FixedZone("+02:00", 2*60*60)
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"utc", time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC), "2026-10-16T09:00:00Z"},
		// 11:00 at +02:00 is 09:00Z. Sent unconverted it would read
		// "2026-10-16T11:00:00+02:00" — the same instant to a parser, but the
		// contract is the Z form, and a Format without .UTC() is also the
		// bug that ships local wall-clock times into other fields.
		{"plus two", time.Date(2026, 10, 16, 11, 0, 0, 0, plus2), "2026-10-16T09:00:00Z"},
		{"sub-second", time.Date(2026, 10, 16, 11, 0, 0, 500_000_000, plus2), "2026-10-16T09:00:00.5Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := rotateClient(t, rotateResponse)
			if _, err := c.Bots.RotateExternalSecret(context.Background(), "bt_1",
				RotateExternalSecretOptions{ActivateAt: tc.at}); err != nil {
				t.Fatal(err)
			}
			got := decodeExactlyActivateAt(t, seen.body)
			if got != tc.want {
				t.Errorf("activateAt = %q, want %q", got, tc.want)
			}
			parsed, err := time.Parse(time.RFC3339Nano, got)
			if err != nil {
				t.Fatalf("activateAt %q does not parse as RFC3339: %v", got, err)
			}
			if !parsed.Equal(tc.at) {
				t.Errorf("activateAt %q is %v, want the instant %v", got, parsed, tc.at)
			}
		})
	}
}

// A zero-valued options struct is the same request as no options: the zero
// time means absent, so no body appears.
func TestRotateExternalSecretEmptyOptionsSendNoBody(t *testing.T) {
	c, seen := rotateClient(t, `{"signingSecret":"whsec_x","activatesAt":null}`)
	if _, err := c.Bots.RotateExternalSecret(context.Background(), "bt_1", RotateExternalSecretOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(seen.body) != 0 {
		t.Errorf("sent a %d-byte body %q for empty options; want none", len(seen.body), seen.body)
	}
	if seen.contentType != "" {
		t.Errorf("sent Content-Type %q with no body", seen.contentType)
	}
}

func TestRotateExternalSecretRefusesAmbiguousOptionsBeforeSending(t *testing.T) {
	c, seen := rotateClient(t, rotateResponse)
	at := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

	if _, err := c.Bots.RotateExternalSecret(context.Background(), "bt_1",
		RotateExternalSecretOptions{ActivateAt: at, ActivateAtRaw: "2026-10-16T09:00:00Z"}); err == nil {
		t.Error("both ActivateAt and ActivateAtRaw: want an error")
	}
	if _, err := c.Bots.RotateExternalSecret(context.Background(), "bt_1",
		RotateExternalSecretOptions{ActivateAt: at}, RotateExternalSecretOptions{}); err == nil {
		t.Error("two options values: want an error")
	}
	if seen.requests != 0 {
		t.Errorf("issued %d requests; a refused call must send nothing", seen.requests)
	}
}

func TestRotateExternalSecretExposesActivatesAt(t *testing.T) {
	c, _ := rotateClient(t, rotateResponse)
	got, err := c.Bots.RotateExternalSecret(context.Background(), "bt_1",
		RotateExternalSecretOptions{ActivateAt: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if got["signingSecret"] != "whsec_x" {
		t.Errorf("signingSecret = %#v", got["signingSecret"])
	}
	if got["activatesAt"] != "2026-10-16T09:00:00.000Z" {
		t.Errorf("activatesAt = %#v", got["activatesAt"])
	}

	c, _ = rotateClient(t, `{"signingSecret":"whsec_x","activatesAt":null}`)
	got, err = c.Bots.RotateExternalSecret(context.Background(), "bt_1")
	if err != nil {
		t.Fatal(err)
	}
	v, present := got["activatesAt"]
	if !present || v != nil {
		t.Errorf("activatesAt = %#v (present %v), want a present null", v, present)
	}
}
