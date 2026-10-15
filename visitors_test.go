package chatly

// What these methods SEND, asserted on the request rather than on the answer.
//
// The surface table (surfacecases_test.go) proves each method issues the verb
// and path the manifest describes, and the lock records it. It does not look
// at the request BODY or at the headers — and a fake transport that answers
// every route with whatever the SDK asked for agrees with any bug in either.
// Two sibling SDKs shipped a paginator over a bare-array route with green
// suites exactly that way.
//
// So the seven writes whose only input is a path parameter are swept here for
// the two things the lock cannot see: that no body goes up, and that the
// Idempotency-Key the contract requires does.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// pathOnlyWrites is every POST on this surface whose entire input is a path
// parameter. None of them may send a body: the API validates with zod, and a
// body it did not ask for is at best ignored and at worst a 400 — but more to
// the point, a method that sends `{}` has invented a request shape, and the
// one place that shows up is a customer's integration.
func pathOnlyWrites() []struct {
	name string
	path string
	call func(context.Context, *Client) (any, error)
} {
	return []struct {
		name string
		path string
		call func(context.Context, *Client) (any, error)
	}{
		{"ai.test_provider", "/v1/ai-providers/openai/test", func(ctx context.Context, c *Client) (any, error) {
			return c.AI.TestProvider(ctx, AIProviderOpenAI)
		}},
		{"bots.rotate_external_secret", "/v1/bots/bt_1/external/rotate-secret", func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.RotateExternalSecret(ctx, "bt_1")
		}},
		{"channels.rotate_secret", "/v1/channels/ch_1/secret/rotate", func(ctx context.Context, c *Client) (any, error) {
			return c.Channels.RotateSecret(ctx, "ch_1")
		}},
		{"customer_cards.rotate_secret", "/v1/customer-cards/cc_1/rotate-secret", func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCards.RotateSecret(ctx, "cc_1")
		}},
		{"docs_sources.webhook", "/v1/docs-sources/ds_1/webhook", func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.Webhook(ctx, "ds_1")
		}},
		{"knowledge_gaps.draft", "/v1/knowledge-gaps/kg_1/draft", func(ctx context.Context, c *Client) (any, error) {
			return c.KnowledgeGaps.Draft(ctx, "kg_1")
		}},
		// The id is interpolated, not appended: a method that built
		// "/v1/visitors/start-conversation" and passed the contact in a body
		// would satisfy a test that only looked at the verb.
		{"visitors.start_conversation", "/v1/visitors/ct_1/start-conversation", func(ctx context.Context, c *Client) (any, error) {
			return c.Visitors.StartConversation(ctx, "ct_1")
		}},
	}
}

func TestPathOnlyWritesSendNoBodyAndAnIdempotencyKey(t *testing.T) {
	// A floor, so this sweep cannot quietly shrink to nothing while staying
	// green. It is the shape of audit this repo has had go blind before.
	if got := len(pathOnlyWrites()); got < 7 {
		t.Fatalf("the path-only write sweep covers %d routes, expected at least 7", got)
	}

	for _, tc := range pathOnlyWrites() {
		t.Run(tc.name, func(t *testing.T) {
			var seen struct {
				method      string
				path        string
				query       string
				body        []byte
				idem        string
				contentType string
				requests    int
			}
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen.requests++
				seen.method = r.Method
				seen.path = r.URL.Path
				seen.query = r.URL.RawQuery
				seen.idem = r.Header.Get("Idempotency-Key")
				seen.contentType = r.Header.Get("Content-Type")
				seen.body, _ = readAll(r)
				w.Header().Set("content-type", "application/json")
				_, _ = w.Write([]byte(`{"id":"x_1"}`))
			}))

			if _, err := tc.call(context.Background(), c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}

			if seen.requests != 1 {
				t.Fatalf("issued %d requests, want 1", seen.requests)
			}
			if seen.method != http.MethodPost {
				t.Errorf("method = %q, want POST", seen.method)
			}
			if seen.path != tc.path {
				t.Errorf("path = %q, want %q", seen.path, tc.path)
			}
			if seen.query != "" {
				t.Errorf("sent query %q; this route takes no parameters", seen.query)
			}
			// len, not a JSON comparison: `{}` and `null` both decode to
			// something a lenient check would accept, and the point is that
			// NOTHING was written to the wire.
			if len(seen.body) != 0 {
				t.Errorf("sent a %d-byte body %q; this route takes none", len(seen.body), seen.body)
			}
			// The corollary — no payload means no Content-Type claim.
			if seen.contentType != "" {
				t.Errorf("sent Content-Type %q with no body", seen.contentType)
			}
			if len(seen.idem) != 36 {
				t.Errorf("Idempotency-Key = %q, want a 36-char uuid", seen.idem)
			}
		})
	}
}

// ── The two refusals that are not about the key ───────────────────────────
//
// visitors.start_conversation is the one route on this surface whose
// commonest 403 has nothing to do with the caller's scopes: it is behind a
// per-workspace opt-in that is off by default. The SDK cannot fix that, but
// it must not swallow it either — both refusals have to arrive as the
// sentinels a caller branches on, with the API's own message intact, because
// that message is the only thing that names the setting.

func TestStartConversationSurfacesTheWorkspaceOptIn(t *testing.T) {
	const msg = "This workspace does not allow an API key to open conversations. " +
		"An owner or admin enables it with `PATCH /v1/workspace/settings` " +
		`{ "proactiveOutreach": { "apiKeysMayStartConversations": true } }. Nothing was sent.`

	requests := 0
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":` + jsonString(t, msg) + `}}`))
	}))

	_, err := c.Visitors.StartConversation(context.Background(), "ct_1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an *APIError carrying the body", err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", apiErr.Status)
	}
	// The message is the only place the setting is named, so losing it is
	// losing the answer: an integrator then re-mints a key that was fine.
	if !strings.Contains(apiErr.Message, "apiKeysMayStartConversations") {
		t.Errorf("message = %q, want it to name the workspace setting", apiErr.Message)
	}
	// A 403 is not retried — only 429 and 5xx are. Retrying an opt-in
	// refusal three times is three audit entries for one mistake.
	if requests != 1 {
		t.Errorf("made %d requests for a 403; a 4xx that is not 429 must not be retried", requests)
	}
}

// The per-contact limit's 429 carries NO Retry-After, and this test exists to
// pin that rather than to wish otherwise.
//
// THE FIXTURE IS THE POINT. An earlier version of this test answered with
// `Retry-After: 1800` and asserted APIError.RetryAfter == 30m, and it passed —
// against a server shape that does not exist. The real path:
// consumePerContactOutreachToken throws RateLimitedError(ttl), which is an
// AppError with httpStatus 429 and details{retryAfterSeconds}; the API's
// exception filter sends wireError(err.toJSON()) and sets NO headers; and the
// only thing that ever synthesises a plain Retry-After is an onSend hook in
// apps/api/src/main.ts that derives it from the `Retry-After-{bucket}`
// headers @nestjs/throttler emits. This refusal does not come from the
// throttler, so there are no bucket headers, and no Retry-After is sent.
//
// So on THIS limit the TTL is reachable only from the body. Asserting a
// header here would have been a fixture inventing the server's shape, which
// makes the suite agree with the bug instead of catching it.
func TestStartConversationPerContactLimitSendsNoRetryAfter(t *testing.T) {
	var sentHeader string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		// The exact envelope RateLimitedError(1800) produces through
		// wireError: code, message, and the TTL under details.
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"Too many requests",` +
			`"details":{"retryAfterSeconds":1800}}}`))
		sentHeader = w.Header().Get("Retry-After")
	}), WithMaxAttempts(1))

	_, err := c.Visitors.StartConversation(context.Background(), "ct_1")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an *APIError", err)
	}
	if apiErr.Status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", apiErr.Status)
	}
	if apiErr.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", apiErr.Code)
	}
	if sentHeader != "" {
		t.Fatalf("the fixture itself sent Retry-After %q; this refusal does not", sentHeader)
	}
	// Unset, because the header it is parsed from was never sent. A caller
	// that backs off on RetryAfter alone would retry immediately here.
	if apiErr.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 — this 429 carries no Retry-After header", apiErr.RetryAfter)
	}
	// The TTL is in the body and nowhere else, so Body is the documented way
	// to reach it. Parsed here to prove it survives the error mapping.
	var envelope struct {
		Error struct {
			Details struct {
				RetryAfterSeconds int `json:"retryAfterSeconds"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(apiErr.Body), &envelope); err != nil {
		t.Fatalf("APIError.Body is not the wire envelope: %v (%q)", err, apiErr.Body)
	}
	if envelope.Error.Details.RetryAfterSeconds != 1800 {
		t.Errorf("details.retryAfterSeconds = %d, want 1800",
			envelope.Error.Details.RetryAfterSeconds)
	}
}

// The OTHER 429 this route can produce, which does carry the header.
//
// Both directions are pinned deliberately. The route inherits the global
// throttler buckets (`short` 30/s, `login` 100/15min) from
// throttler.config.ts, and a 429 from those goes through the guard, so the
// onSend hook does synthesise a plain Retry-After. Without this test, the
// one above reads as "this SDK ignores Retry-After on 429" and someone
// "fixes" it by adding a header to that fixture.
func TestStartConversationThrottlerLimitDoesCarryRetryAfter(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"Too many requests"}}`))
	}), WithMaxAttempts(1))

	_, err := c.Visitors.StartConversation(context.Background(), "ct_1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an *APIError", err)
	}
	if apiErr.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s from the header", apiErr.RetryAfter)
	}
}

// An empty contact id is refused locally, before a request goes out: the
// server would answer 404 for the route "/v1/visitors//start-conversation",
// which reads like "that contact does not exist".
func TestStartConversationRefusesAnEmptyContactID(t *testing.T) {
	requests := 0
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{}`))
	}))
	if _, err := c.Visitors.StartConversation(context.Background(), ""); err == nil {
		t.Fatal("accepted an empty contact id")
	}
	if requests != 0 {
		t.Errorf("made %d requests for an empty id; it must not reach the network", requests)
	}
}

// jsonString quotes a string for embedding in a hand-written JSON fixture,
// so the fixture stays readable instead of being a wall of escapes.
func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("quoting fixture string: %v", err)
	}
	return string(b)
}
