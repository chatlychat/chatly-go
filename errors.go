package chatly

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The sentinels a caller branches on. Match them with errors.Is:
//
//	if errors.Is(err, chatly.ErrForbidden) { /* key lacks a scope */ }
//
// The pair that matters most is ErrUnauthorized (the key is wrong, expired or
// revoked) versus ErrForbidden (the key is fine but was not granted the scope
// this route needs). Those are different fixes — rotate a key versus edit its
// scopes — and telling them apart must never require parsing a message
// string, because the message is the API's to change.
var (
	// ErrAPI matches EVERY *APIError, whatever the status. Use it to mean
	// "the API answered and said no", as opposed to ErrTransport.
	ErrAPI = errors.New("chatly: api error")

	// ErrUnauthorized is HTTP 401: the credential is missing, malformed,
	// expired or revoked.
	ErrUnauthorized = errors.New("chatly: unauthorized")

	// ErrForbidden is HTTP 403. For a `ck_` workspace key this nearly always
	// means one of two things: the key lacks the scope the route requires, or
	// the route is not key-accessible at all (it needs a signed-in person —
	// see the package doc on which routes those are).
	//
	// There is a THIRD cause and exactly one route has it:
	// Visitors.StartConversation is behind a per-workspace opt-in that is off
	// by default, so its 403 is usually about
	// settings.proactiveOutreach.apiKeysMayStartConversations rather than
	// about the key at all. Read APIError.Message there before re-minting a
	// key that was never the problem.
	ErrForbidden = errors.New("chatly: forbidden")

	// ErrNotFound is HTTP 404.
	ErrNotFound = errors.New("chatly: not found")

	// ErrConflict is HTTP 409. Two causes: an ordinary resource conflict, and
	// an idempotency-key replay mismatch — the same Idempotency-Key presented
	// with a DIFFERENT body than the first time. Check APIError.Code to tell
	// them apart.
	ErrConflict = errors.New("chatly: conflict")

	// ErrValidation is a request the API's schema rejected. It matches BOTH
	// 400 and 422, and that is measured, not sloppy: the contract these SDKs
	// share specifies 422 for validation, but this API's zod pipe raises its
	// shared ValidationError with httpStatus 400 and code "validation_failed"
	// — so on every route a hand-written method here touches, a schema
	// failure arrives as 400. 422 is also mapped, because a handful of other
	// routes (reachable via Client.Request) do raise it. Read APIError.Status
	// when you need to tell them apart, and APIError.Body for the per-field
	// `details.issues` array the API sends.
	ErrValidation = errors.New("chatly: validation failed")

	// ErrRateLimited is HTTP 429. APIError.RetryAfter is populated when the
	// response carried a Retry-After header. Note that the client already
	// retried this request (see RetryPolicy) before surfacing it.
	ErrRateLimited = errors.New("chatly: rate limited")

	// ErrServer is any 5xx.
	ErrServer = errors.New("chatly: server error")

	// ErrTransport matches every *TransportError: DNS failure, refused
	// connection, TLS failure, timeout, cancelled context. Distinct from
	// ErrAPI because nothing was ever served — the request may or may not
	// have been applied, which is exactly why writes carry an
	// Idempotency-Key.
	ErrTransport = errors.New("chatly: transport failure")
)

// APIError is a non-2xx answer from the Chatly API.
//
// Every field here comes off the wire; none is inferred. Body is kept
// verbatim so that anything the typed fields lose — per-field validation
// detail, an unrecognised envelope, a plain-text proxy error page — is still
// reachable by the caller.
type APIError struct {
	// Status is the HTTP status code.
	Status int

	// Code is the API's machine-readable `code` from the JSON body, if the
	// body had one. Empty string when it did not.
	Code string

	// Message is the API's human-readable `message`, if present. Never
	// branch on it; branch on Status, the sentinels, or Code.
	Message string

	// RequestID is the `x-request-id` response header.
	//
	// Expect it to be EMPTY against api.chatlychat.com today. The field is
	// here because the cross-language SDK contract requires it and because a
	// proxy or load balancer in front of a self-hosted install commonly adds
	// the header — but the API itself sets no request-id header on any route
	// (its Fastify adapter is built without requestIdHeader/genReqId, and no
	// interceptor adds one). Do not build a support workflow that depends on
	// this being populated without checking your own edge first.
	RequestID string

	// Body is the raw response body, as bytes, truncated to the first
	// maxErrorBodyBytes. Nothing is parsed out of it that is not above.
	Body string

	// RetryAfter is the parsed Retry-After header. Zero when absent. The
	// header may be seconds or an HTTP-date; both are handled.
	RetryAfter time.Duration

	// Method and Path are the request this answered, for the error string.
	Method string
	Path   string

	// Attempts is how many HTTP attempts were made before this answer,
	// including the one that produced it. 1 means it was not retried.
	Attempts int

	sentinel error
}

// maxErrorBodyBytes bounds what an error holds in memory. A 5xx from a proxy
// can be an HTML page of unbounded size, and an SDK error that pins megabytes
// into a caller's log aggregator is its own outage.
const maxErrorBodyBytes = 64 * 1024

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "chatly: %s %s: %d", e.Method, e.Path, e.Status)
	if e.Code != "" {
		fmt.Fprintf(&b, " (%s)", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	} else if e.Body != "" {
		fmt.Fprintf(&b, ": %s", firstLine(e.Body, 200))
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " [request-id %s]", e.RequestID)
	}
	return b.String()
}

// Is makes errors.Is work for both the status sentinel and the catch-all
// ErrAPI. It is a method rather than an Unwrap chain because an APIError
// matches two sentinels and Unwrap only models one.
func (e *APIError) Is(target error) bool {
	if target == ErrAPI {
		return true
	}
	return e.sentinel != nil && target == e.sentinel
}

// Retryable reports whether this status is one the client is willing to try
// again: 429 and 5xx, and nothing else. A 4xx that is not 429 is the
// caller's bug or state and will fail identically forever.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// TransportError is a failure to get any HTTP response at all.
//
// It deliberately does NOT carry a status, because there wasn't one. Whether
// the server applied the request is unknown; for a write, retrying with the
// same Idempotency-Key is the safe move and is what RetryPolicy already did
// before this surfaced.
type TransportError struct {
	Method   string
	URL      string
	Attempts int
	Err      error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("chatly: %s %s: transport failure after %d attempt(s): %v",
		e.Method, e.URL, e.Attempts, e.Err)
}

// Unwrap exposes the underlying net/url or context error, so
// errors.Is(err, context.DeadlineExceeded) works on a timeout.
func (e *TransportError) Unwrap() error { return e.Err }

// Is matches the ErrTransport sentinel. Returning false here lets errors.Is
// fall through to Unwrap, so the wrapped cause stays matchable too.
func (e *TransportError) Is(target error) bool { return target == ErrTransport }

// sentinelFor maps a status onto the sentinel a caller branches on. A status
// with no sentinel (400, 405, 418…) still produces an *APIError, which still
// matches ErrAPI — it just has no narrower name.
func sentinelFor(status int) error {
	switch {
	case status == http.StatusUnauthorized:
		return ErrUnauthorized
	case status == http.StatusForbidden:
		return ErrForbidden
	case status == http.StatusNotFound:
		return ErrNotFound
	case status == http.StatusConflict:
		return ErrConflict
	case status == http.StatusUnprocessableEntity, status == http.StatusBadRequest:
		// Both, deliberately. See ErrValidation: this API answers 400 for a
		// schema failure. A 400 is still never retried.
		return ErrValidation
	case status == http.StatusTooManyRequests:
		return ErrRateLimited
	case status >= 500:
		return ErrServer
	default:
		return nil
	}
}

// errorEnvelope covers the shapes the API has been observed to answer with.
//
// Chatly's own filter answers {"code","message"}; a few paths nest it under
// "error"; anything that escapes the filter (a Nest default, a proxy) answers
// {"statusCode","message"} where "message" may be a string OR an array of
// strings. Parsing all of them is three extra fields and removes a class of
// "the SDK said nothing was wrong with my request" report. What is NOT done
// here is inventing a field: every name below was taken from a real response
// shape, and anything unrecognised is left in Body rather than guessed at.
type errorEnvelope struct {
	Code    string          `json:"code"`
	Message json.RawMessage `json:"message"`
	Error   *struct {
		Code    string          `json:"code"`
		Message json.RawMessage `json:"message"`
	} `json:"error"`
	StatusCode int    `json:"statusCode"`
	ErrorText  string `json:"error_description"`
}

// newAPIError builds the typed error from a response. It never fails: a body
// that is not JSON, or is JSON of an unknown shape, yields an error with
// empty Code/Message and the bytes in Body.
func newAPIError(method, path string, resp *Response, attempts int) *APIError {
	body := resp.Body
	if len(body) > maxErrorBodyBytes {
		body = body[:maxErrorBodyBytes]
	}
	e := &APIError{
		Status:    resp.Status,
		RequestID: resp.RequestID,
		Body:      string(body),
		Method:    method,
		Path:      path,
		Attempts:  attempts,
		sentinel:  sentinelFor(resp.Status),
	}

	var env errorEnvelope
	if err := json.Unmarshal(resp.Body, &env); err == nil {
		e.Code = env.Code
		e.Message = decodeMessage(env.Message)
		if env.Error != nil {
			if e.Code == "" {
				e.Code = env.Error.Code
			}
			if e.Message == "" {
				e.Message = decodeMessage(env.Error.Message)
			}
		}
		if e.Message == "" {
			e.Message = env.ErrorText
		}
	}

	if resp.Status == http.StatusTooManyRequests {
		e.RetryAfter = parseRetryAfter(resp.Header.Get("retry-after"), time.Now())
	}
	return e
}

// decodeMessage accepts a string or an array of strings, because Nest's
// default validation filter answers an array and Chatly's own filter answers
// a string.
func decodeMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return strings.Join(list, "; ")
	}
	return ""
}

// parseRetryAfter handles both forms RFC 9110 allows: a delay in seconds, and
// an HTTP-date. A negative or unparseable value yields zero, meaning "the
// server did not tell us".
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(header, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs * float64(time.Second))
	}
	if at, err := http.ParseTime(header); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

func firstLine(s string, max int) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
