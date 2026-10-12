package chatly

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Response is one HTTP answer, fully read. The body is read and the
// connection closed before the response leaves the transport, so a caller can
// never leak one by forgetting to close it.
type Response struct {
	Status    int
	Header    http.Header
	Body      []byte
	RequestID string
}

// RetryPolicy decides what gets tried again and how long we wait.
//
// Only three things are retried: a transport failure, a 429, and a 5xx.
// Never a 4xx that is not 429 — a 400, 401, 403, 404, 409 or 422 is the
// caller's request or the caller's state, and sending it again produces the
// same answer while hiding the real error behind a delay.
type RetryPolicy struct {
	// MaxAttempts is the total number of HTTP attempts, not the number of
	// retries: 3 means one try and two retries. 1 disables retrying.
	MaxAttempts int

	// BaseDelay is the first backoff interval. Each subsequent attempt
	// doubles it, then full jitter is applied.
	BaseDelay time.Duration

	// MaxDelay caps a single backoff wait, jitter included.
	MaxDelay time.Duration

	// MaxRetryAfter caps how long a server's Retry-After header can park the
	// call. A server is allowed to say "come back in an hour"; a client
	// library that silently blocks a request goroutine for an hour is a
	// worse outcome than surfacing the 429, so past this we stop retrying
	// and return the APIError with RetryAfter set, letting the caller
	// schedule it.
	MaxRetryAfter time.Duration
}

// DefaultRetryPolicy is three attempts with exponential backoff and full
// jitter, starting at 250ms and capped at 8s per wait.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:   3,
		BaseDelay:     250 * time.Millisecond,
		MaxDelay:      8 * time.Second,
		MaxRetryAfter: 30 * time.Second,
	}
}

// writeMethods are the verbs that carry an Idempotency-Key.
func isWrite(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// do performs one API call, with retries, and returns the raw response.
//
// The Idempotency-Key is minted ONCE, outside the attempt loop, and every
// retry of this call reuses it. That is the entire reason the header exists:
// a write that failed with a 500 or a dropped connection may or may not have
// been applied, and replaying it under the same key makes the second attempt
// either a no-op that replays the first response or an explicit 409 — never a
// duplicate contact, message or ticket.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) (*Response, error) {
	if ctx == nil {
		return nil, fmt.Errorf("chatly: nil context passed to %s %s", method, path)
	}

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("chatly: encoding request body for %s %s: %w", method, path, err)
		}
	}

	endpoint := c.baseURL + path
	if len(query) > 0 {
		// url.Values.Encode keeps repeated keys repeated, which is what the
		// API's `filter[]` and `kinds[]` parameters require: two parameters,
		// not one comma-joined value.
		endpoint += "?" + query.Encode()
	}

	var idempotencyKey string
	if isWrite(method) {
		idempotencyKey = c.pinnedIdempotencyKey
		if idempotencyKey == "" {
			var err error
			idempotencyKey, err = newUUIDv4()
			if err != nil {
				return nil, fmt.Errorf("chatly: generating idempotency key: %w", err)
			}
		}
	}

	attempts := c.retry.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastTransportErr error
	for attempt := 1; ; attempt++ {
		resp, err := c.attempt(ctx, method, endpoint, payload, idempotencyKey)
		if err != nil {
			// A cancelled or expired CALLER context is final: the caller has
			// gone away, and retrying burns the remaining deadline for
			// nothing. A per-attempt timeout (see Client.timeout) is not
			// final and does retry.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, &TransportError{Method: method, URL: endpoint, Attempts: attempt, Err: err}
			}
			lastTransportErr = err
			if attempt >= attempts {
				return nil, &TransportError{Method: method, URL: endpoint, Attempts: attempt, Err: lastTransportErr}
			}
			if waitErr := c.sleep(ctx, c.backoff(attempt)); waitErr != nil {
				return nil, &TransportError{Method: method, URL: endpoint, Attempts: attempt, Err: waitErr}
			}
			continue
		}

		if resp.Status < 400 {
			return resp, nil
		}

		apiErr := newAPIError(method, path, resp, attempt)
		if !apiErr.Retryable() || attempt >= attempts {
			return nil, apiErr
		}

		wait := c.backoff(attempt)
		if apiErr.RetryAfter > 0 {
			if apiErr.RetryAfter > c.retry.MaxRetryAfter {
				// Too far out to hold the goroutine. Hand the caller the
				// error with RetryAfter populated so it can schedule.
				return nil, apiErr
			}
			wait = apiErr.RetryAfter
		}
		if waitErr := c.sleep(ctx, wait); waitErr != nil {
			return nil, &TransportError{Method: method, URL: endpoint, Attempts: attempt, Err: waitErr}
		}
	}
}

// attempt is a single HTTP round trip. The request body is rebuilt from the
// marshalled bytes on every attempt — a used io.Reader is drained, so sharing
// one across retries sends an empty body the second time.
func (c *Client) attempt(ctx context.Context, method, endpoint string, payload []byte, idempotencyKey string) (*Response, error) {
	reqCtx := ctx
	var cancel context.CancelFunc
	if c.timeout > 0 {
		// Per ATTEMPT, not per call: a call that retries twice may take up to
		// three timeouts in the worst case. Documented on WithTimeout.
		reqCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	for k, vs := range c.extraHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	// LimitReader, not io.ReadAll: an unbounded read of a hostile or broken
	// upstream is how an SDK turns a bad response into the caller's OOM.
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxResponseBytes {
		return nil, fmt.Errorf("chatly: response body exceeded %d bytes", maxResponseBytes)
	}

	return &Response{
		Status:    httpResp.StatusCode,
		Header:    httpResp.Header,
		Body:      raw,
		RequestID: httpResp.Header.Get("x-request-id"),
	}, nil
}

// maxResponseBytes bounds a single response. 32MiB is far above any list page
// this API serves and far below a number that matters to a server process.
const maxResponseBytes int64 = 32 << 20

// backoff is exponential with FULL jitter: the wait is uniform in
// [0, base*2^(attempt-1)], capped at MaxDelay. Full jitter rather than
// equal jitter because the failure mode being defended against is a fleet of
// clients retrying in lockstep after a shared outage, and full jitter spreads
// them widest.
func (c *Client) backoff(attempt int) time.Duration {
	base := c.retry.BaseDelay
	if base <= 0 {
		base = DefaultRetryPolicy().BaseDelay
	}
	maxDelay := c.retry.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultRetryPolicy().MaxDelay
	}

	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	// Guard the shift itself: 1<<64 is undefined-shaped, and a caller is
	// free to set MaxAttempts absurdly high.
	if shift > bits.UintSize-2 {
		shift = bits.UintSize - 2
	}
	window := base << uint(shift)
	if window <= 0 || window > maxDelay {
		window = maxDelay
	}
	return time.Duration(mrand.Int64N(int64(window) + 1))
}

// sleep waits, but gives up the moment the caller's context does. A naive
// time.Sleep here would hold a cancelled request for the full backoff.
func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if c.sleepFn != nil {
		return c.sleepFn(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// newUUIDv4 is a random UUID from crypto/rand, RFC 4122 variant and version
// bits set. Hand-rolled because the module takes no third-party dependency
// and the standard library has no UUID type; it is 12 lines and the
// alternative is google/uuid in every consumer's go.sum.
func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:]), nil
}

// NewIdempotencyKey mints the same kind of key the client puts on every write.
// Exported because pinning one (see Client.WithIdempotencyKey) requires the
// caller to have produced it, and making them reach for a UUID dependency to
// use this SDK's own feature would be rude.
func NewIdempotencyKey() (string, error) { return newUUIDv4() }
