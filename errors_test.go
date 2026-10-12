package chatly

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The API's real error envelope, verified against
// apps/api/src/common/filters/app-exception.filter.ts: a nested `error`
// object, not {statusCode,message} and not a bare {code,message}. A verifier
// written against the wrong envelope still "works" — it just reports an empty
// code for every failure — so the fixture here is the shape the server
// actually sends.
func apiError(code, message string) string {
	return `{"error":{"code":"` + code + `","message":"` + message + `"}}`
}

// ── CONTRACT §10.4 ─────────────────────────────────────────────────────────
// "A 404 raises the not-found type, a 422 the validation type, a 429 the
// rate-limit type with retry_after populated, and each carries request_id."
func TestStatusesMapToSentinels(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		sentinel error
		code     string
	}{
		{http.StatusUnauthorized, apiError("unauthenticated", "Authentication required"), ErrUnauthorized, "unauthenticated"},
		{http.StatusForbidden, apiError("forbidden", "Missing scope"), ErrForbidden, "forbidden"},
		{http.StatusNotFound, apiError("not_found", "Contact not found"), ErrNotFound, "not_found"},
		{http.StatusConflict, apiError("idempotency_conflict", "Key reused with a different body"), ErrConflict, "idempotency_conflict"},
		{http.StatusUnprocessableEntity, apiError("validation_failed", "Invalid request body"), ErrValidation, "validation_failed"},
		// 400 is the status this API actually answers for a schema failure.
		{http.StatusBadRequest, apiError("validation_failed", "Invalid request body"), ErrValidation, "validation_failed"},
		{http.StatusTooManyRequests, apiError("rate_limited", "Too many requests"), ErrRateLimited, "rate_limited"},
		{http.StatusInternalServerError, apiError("unknown", "boom"), ErrServer, "unknown"},
		{http.StatusBadGateway, apiError("dependency_failure", "upstream"), ErrServer, "dependency_failure"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("x-request-id", "req_abc123")
				if tc.status == http.StatusTooManyRequests {
					w.Header().Set("retry-after", "7")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}), WithMaxAttempts(1)) // no retries, so a 429/5xx surfaces immediately

			_, err := c.Contacts.Get(context.Background(), "ct_1")
			if err == nil {
				t.Fatal("want an error")
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("errors.Is(err, %v) = false; err = %v", tc.sentinel, err)
			}
			// Every APIError also matches the catch-all.
			if !errors.Is(err, ErrAPI) {
				t.Fatal("an API error must match ErrAPI")
			}
			// And must NOT be mistaken for a transport failure.
			if errors.Is(err, ErrTransport) {
				t.Fatal("an HTTP status matched ErrTransport")
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("errors.As to *APIError failed for %T", err)
			}
			if apiErr.Status != tc.status {
				t.Fatalf("Status = %d, want %d", apiErr.Status, tc.status)
			}
			if apiErr.Code != tc.code {
				t.Fatalf("Code = %q, want %q (is the envelope being parsed?)", apiErr.Code, tc.code)
			}
			if apiErr.Message == "" {
				t.Fatal("Message was not parsed out of the envelope")
			}
			if apiErr.RequestID != "req_abc123" {
				t.Fatalf("RequestID = %q, want the x-request-id header", apiErr.RequestID)
			}
			if apiErr.Body != tc.body {
				t.Fatalf("Body = %q, want the raw response verbatim", apiErr.Body)
			}
			if apiErr.Method != "GET" || apiErr.Path != "/v1/contacts/ct_1" {
				t.Fatalf("error lost the request it answered: %q %q", apiErr.Method, apiErr.Path)
			}
			if tc.status == http.StatusTooManyRequests && apiErr.RetryAfter != 7*time.Second {
				t.Fatalf("RetryAfter = %v, want 7s from the header", apiErr.RetryAfter)
			}
			if !strings.Contains(apiErr.Error(), "req_abc123") {
				t.Fatalf("Error() drops the request id: %q", apiErr.Error())
			}
		})
	}
}

// "A caller must be able to tell 'my key is wrong' from 'my key lacks a
// scope' without parsing a message string."
func TestUnauthorizedAndForbiddenAreDistinct(t *testing.T) {
	c401, _ := newTestClient(t, jsonHandler(401, apiError("unauthenticated", "x")), WithMaxAttempts(1))
	c403, _ := newTestClient(t, jsonHandler(403, apiError("forbidden", "x")), WithMaxAttempts(1))

	_, err401 := c401.Contacts.Get(context.Background(), "ct_1")
	_, err403 := c403.Contacts.Get(context.Background(), "ct_1")

	if !errors.Is(err401, ErrUnauthorized) || errors.Is(err401, ErrForbidden) {
		t.Fatal("401 must match ErrUnauthorized and only that")
	}
	if !errors.Is(err403, ErrForbidden) || errors.Is(err403, ErrUnauthorized) {
		t.Fatal("403 must match ErrForbidden and only that")
	}
}

// A status with no sentinel is still a typed error with everything on it.
// 428 is the real one: it means a write arrived without an Idempotency-Key.
func TestUnmappedStatusIsStillTyped(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(428, apiError("precondition_required", "Idempotency-Key required")), WithMaxAttempts(1))
	_, err := c.Contacts.Upsert(context.Background(), ContactInput{Email: "a@b.test"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 428 {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, ErrAPI) {
		t.Fatal("428 must still match ErrAPI")
	}
	for _, s := range []error{ErrValidation, ErrNotFound, ErrServer, ErrConflict} {
		if errors.Is(err, s) {
			t.Fatalf("428 wrongly matched %v", s)
		}
	}
}

// A non-JSON error body (a proxy's HTML page, a gateway's plain text) must not
// break error construction.
func TestNonJSONErrorBodyIsPreserved(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}), WithMaxAttempts(1))

	_, err := c.Contacts.Get(context.Background(), "ct_1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Code != "" || apiErr.Message != "" {
		t.Fatalf("invented a code/message from HTML: %q / %q", apiErr.Code, apiErr.Message)
	}
	if !strings.Contains(apiErr.Body, "Bad Gateway") {
		t.Fatalf("Body = %q", apiErr.Body)
	}
	if !errors.Is(err, ErrServer) {
		t.Fatal("502 must match ErrServer")
	}
}

// Nest's default filter answers `message` as an ARRAY of strings. Parsing
// only the string form leaves the message empty on exactly the errors a
// caller most needs to read.
func TestArrayMessageIsJoined(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(400, `{"statusCode":400,"message":["email must be an email","name should not be empty"]}`), WithMaxAttempts(1))
	_, err := c.Contacts.Upsert(context.Background(), ContactInput{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(apiErr.Message, "must be an email") || !strings.Contains(apiErr.Message, "should not be empty") {
		t.Fatalf("Message = %q, want both issues", apiErr.Message)
	}
}

func TestRetryAfterHTTPDateIsParsed(t *testing.T) {
	now := time.Now()
	if got := parseRetryAfter(now.Add(42*time.Second).UTC().Format(http.TimeFormat), now); got < 40*time.Second || got > 43*time.Second {
		t.Fatalf("parseRetryAfter(http-date) = %v, want ~42s", got)
	}
	if got := parseRetryAfter("", now); got != 0 {
		t.Fatalf("empty header = %v, want 0", got)
	}
	if got := parseRetryAfter("not-a-date", now); got != 0 {
		t.Fatalf("garbage header = %v, want 0", got)
	}
	// A past date means "now", not a negative wait.
	if got := parseRetryAfter(now.Add(-time.Hour).UTC().Format(http.TimeFormat), now); got != 0 {
		t.Fatalf("past http-date = %v, want 0", got)
	}
}

// ── CONTRACT §10.5 ─────────────────────────────────────────────────────────
// "A 429 is retried and then succeeds; a 400 is not retried."
func TestRateLimitIsRetriedThenSucceeds(t *testing.T) {
	var calls atomic.Int64
	var keys []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if calls.Add(1) == 1 {
			w.Header().Set("retry-after", "1")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(apiError("rate_limited", "slow down")))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ct_1"}`))
	}))

	got, err := c.Contacts.Upsert(context.Background(), ContactInput{Email: "a@b.test"})
	if err != nil {
		t.Fatalf("Upsert after a 429: %v", err)
	}
	if got["id"] != "ct_1" {
		t.Fatalf("got %v", got)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d attempts, want 2 (one 429, one success)", n)
	}
	// CONTRACT §4: "A retried write reuses the same Idempotency-Key as the
	// attempt that failed — that is the whole reason the header exists."
	if keys[0] != keys[1] {
		t.Fatalf("retry minted a NEW idempotency key (%q then %q): the retry could double-write", keys[0], keys[1])
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 428} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int64
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(apiError("x", "y")))
			}))
			if _, err := c.Contacts.Upsert(context.Background(), ContactInput{Email: "a@b.test"}); err == nil {
				t.Fatal("want an error")
			}
			if n := calls.Load(); n != 1 {
				t.Fatalf("%d attempts for a %d; a 4xx that is not 429 must never be retried", n, status)
			}
			var apiErr *APIError
			_ = errors.As(nil, &apiErr)
		})
	}
}

func TestServerErrorsAreRetriedUpToTheLimit(t *testing.T) {
	var calls atomic.Int64
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = w.Write([]byte(apiError("service_unavailable", "down")))
	}))

	_, err := c.Contacts.Get(context.Background(), "ct_1")
	if !errors.Is(err, ErrServer) {
		t.Fatalf("err = %v", err)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("%d attempts, want the default 3", n)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Attempts != 3 {
		t.Fatalf("APIError.Attempts = %d, want 3", apiErr.Attempts)
	}
}

// A Retry-After further out than MaxRetryAfter must surface rather than park
// the caller's goroutine for the duration.
func TestLongRetryAfterSurfacesInsteadOfBlocking(t *testing.T) {
	var calls atomic.Int64
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("retry-after", "3600")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(apiError("rate_limited", "come back in an hour")))
	}))

	_, err := c.Contacts.Get(context.Background(), "ct_1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.RetryAfter != time.Hour {
		t.Fatalf("RetryAfter = %v, want 1h so the caller can schedule it", apiErr.RetryAfter)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d attempts; a one-hour Retry-After must not be slept through", n)
	}
}

// ── CONTRACT §3: a transport failure is its own type ───────────────────────
func TestTransportFailureIsItsOwnType(t *testing.T) {
	// A server that accepts the connection and closes it mid-response is the
	// cheapest honest transport failure: no status is ever produced.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Skip("no hijack support")
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	c, err := New("ck_x", WithBaseURL(srv.URL), WithMaxAttempts(2))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.sleepFn = func(context.Context, time.Duration) error { return nil }

	_, err = c.Contacts.Get(context.Background(), "ct_1")
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("errors.Is(err, ErrTransport) = false; err = %T %v", err, err)
	}
	if errors.Is(err, ErrAPI) {
		t.Fatal("a transport failure must not look like an API answer")
	}
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("errors.As to *TransportError failed: %T", err)
	}
	if te.Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2 (a connection error is retryable)", te.Attempts)
	}
	if te.Err == nil {
		t.Fatal("TransportError lost its cause")
	}
}

// A cancelled caller context must stop immediately and stay matchable to the
// underlying cause.
func TestCancelledContextIsNotRetried(t *testing.T) {
	var calls atomic.Int64
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Contacts.Get(ctx, "ct_1")
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancellation cause was lost: %v", err)
	}
	if n := calls.Load(); n > 1 {
		t.Fatalf("%d attempts after cancellation", n)
	}
}

func TestNilContextIsRejected(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200, `{}`))
	//nolint:staticcheck // deliberately passing a nil context: callers do.
	if _, err := c.Contacts.Get(nil, "ct_1"); err == nil {
		t.Fatal("a nil context must be an error, not a panic inside net/http")
	}
}

func TestNewUUIDv4Shape(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		id, err := NewIdempotencyKey()
		if err != nil {
			t.Fatalf("NewIdempotencyKey: %v", err)
		}
		if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[14] != '4' || id[18] != '-' || id[23] != '-' {
			t.Fatalf("uuid %q is not a v4 in canonical form", id)
		}
		if variant := id[19]; !strings.ContainsRune("89ab", rune(variant)) {
			t.Fatalf("uuid %q has variant nibble %q, want RFC 4122", id, variant)
		}
		if seen[id] {
			t.Fatalf("duplicate uuid %q in 64 draws", id)
		}
		seen[id] = true
	}
}
