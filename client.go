// Package chatly is the official Go SDK for the Chatly customer messaging
// platform.
//
// It is a BACKEND SDK. It authenticates with a workspace API key (`ck_…`),
// which is a bearer credential for the entire workspace: every contact, every
// conversation, every ticket. Never ship it to a browser, a mobile app, a
// CLI you distribute, or anything else a customer can read. The browser-side
// SDK is a different package (@chatlychat/sdk-js) and uses a public channel
// id, not this key.
//
// # Quickstart
//
//	client, err := chatly.New(os.Getenv("CHATLY_API_KEY"))
//	if err != nil { log.Fatal(err) }
//
//	me, err := client.Whoami(ctx)          // which workspace, which scopes
//	page, err := client.Contacts.List(ctx, nil)
//	_, err = client.Conversations.Send(ctx, convID, chatly.SendMessageInput{Body: "hi"})
//
// # What is and is not here
//
// The Chatly API has 886 routes; 141 of them accept an API key, and the rest
// answer a `ck_` key with 403 because they need a signed-in person (a
// reaction records WHO reacted, and a key is not a person). This package
// hand-writes methods for 55 of the 141 — the ones an integration actually
// reaches for — and gives you Client.Request for the other 85. A method that
// always fails is worse than no method, so none of the person-only routes
// appear here at all.
//
// # Errors
//
// Every failure is either an *APIError (the API answered) or a
// *TransportError (nothing was served). Branch with errors.Is on the
// sentinels — ErrUnauthorized, ErrForbidden, ErrNotFound, ErrConflict,
// ErrValidation, ErrRateLimited, ErrServer, ErrTransport — never by matching
// on a message string.
//
// # Response types
//
// Reads return Object (map[string]any), []Object, or *Page[Object] rather
// than generated structs. That is a deliberate trade: the API's response
// shapes are versioned server-side and add fields without a major version,
// and a generated struct silently drops every field it has not been
// regenerated for. If you want your own types, use Fetch:
//
//	type Contact struct{ ID, Email string }
//	c, err := chatly.Fetch[Contact](ctx, client, "GET", "/v1/contacts/"+id, nil, nil)
package chatly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Version is the SDK version, reported in the User-Agent header.
const Version = "0.1.0"

// DefaultBaseURL is the Chatly production API.
//
// PRODUCTION, not a development or example hostname. Four sibling SDKs
// shipped with `api.livechat.local` or `api.chatly.example` baked in as the
// default; both resolve nowhere, so every call failed for anyone who followed
// the docs and did not override it. No test caught it, because every test
// passes an explicit URL — including the tests in this package, which is why
// there is one test here that asserts this constant and nothing else.
//
// Override it with WithBaseURL only for a self-hosted install.
const DefaultBaseURL = "https://api.chatlychat.com"

// DefaultTimeout is the per-attempt request timeout.
const DefaultTimeout = 15 * time.Second

// Object is a decoded JSON object from the API. See the package doc for why
// responses are maps and not structs, and Fetch for how to use your own type.
type Object = map[string]any

// Client is a Chatly API client. It is safe for concurrent use by multiple
// goroutines, and is meant to be built once and shared, not per request.
type Client struct {
	apiKey    string
	baseURL   string
	userAgent string

	httpClient   *http.Client
	timeout      time.Duration
	retry        RetryPolicy
	extraHeaders http.Header

	// pinnedIdempotencyKey, when non-empty, is used for every write made
	// through THIS client value instead of a fresh UUID per call. Set only
	// via WithIdempotencyKey, which returns a derived client and leaves the
	// original alone.
	pinnedIdempotencyKey string

	// sleepFn replaces the backoff wait in tests, so a retry test does not
	// cost real seconds. Unexported: not part of the public surface.
	sleepFn func(context.Context, time.Duration) error

	Contacts      *ContactsService
	Conversations *ConversationsService
	Tickets       *TicketsService
	Macros        *MacrosService
	KB            *KBService
	Search        *SearchService
	Events        *EventsService
	Webhooks      *WebhooksService
	Reports       *ReportsService
	Channels      *ChannelsService
	Billing       *BillingService
	Workspace     *WorkspaceService
}

// Option configures a Client. Options are applied in order and may fail, so
// a bad base URL is a construction error rather than a surprise at the first
// call.
type Option func(*Client) error

// WithBaseURL points the client at a different API host — a self-hosted
// install, or a loopback test server. A trailing slash is trimmed. The URL
// must be absolute and http(s).
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		raw = strings.TrimRight(strings.TrimSpace(raw), "/")
		if raw == "" {
			return errors.New("chatly: WithBaseURL: empty url")
		}
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("chatly: WithBaseURL: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("chatly: WithBaseURL: scheme must be http or https, got %q", u.Scheme)
		}
		if u.Host == "" {
			return errors.New("chatly: WithBaseURL: url has no host")
		}
		c.baseURL = raw
		return nil
	}
}

// WithHTTPClient injects the *http.Client used for every call. This is the
// seam tests use: point it at an httptest.Server, or give it a RoundTripper
// that answers from fixtures, and no test needs network or a key.
//
// If your client sets its own Timeout, note that it bounds the whole call
// including body read, while WithTimeout bounds each attempt via context;
// setting both means the smaller one wins.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error {
		if hc == nil {
			return errors.New("chatly: WithHTTPClient: nil client")
		}
		c.httpClient = hc
		return nil
	}
}

// WithRoundTripper swaps the transport while keeping the rest of the default
// http.Client. Convenience over WithHTTPClient for the common case of
// intercepting requests.
func WithRoundTripper(rt http.RoundTripper) Option {
	return func(c *Client) error {
		if rt == nil {
			return errors.New("chatly: WithRoundTripper: nil round tripper")
		}
		c.httpClient = &http.Client{Transport: rt}
		return nil
	}
}

// WithTimeout sets the per-ATTEMPT timeout (default 15s), applied as a
// context deadline around each HTTP attempt.
//
// Per attempt, not per call: a call that exhausts three attempts can take up
// to three timeouts plus two backoff waits. To bound the whole operation,
// pass a context with your own deadline — that is honoured across retries and
// stops them early.
//
// Zero disables the per-attempt deadline, leaving only the caller's context.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d < 0 {
			return errors.New("chatly: WithTimeout: negative duration")
		}
		c.timeout = d
		return nil
	}
}

// WithRetryPolicy replaces the whole retry policy. See RetryPolicy for what
// is and is not retried; that part is not configurable, because retrying a
// 422 is never right.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(c *Client) error {
		if p.MaxAttempts < 1 {
			return errors.New("chatly: WithRetryPolicy: MaxAttempts must be >= 1")
		}
		c.retry = p
		return nil
	}
}

// WithMaxAttempts sets the total attempt count, keeping the default backoff.
// 1 disables retrying.
func WithMaxAttempts(n int) Option {
	return func(c *Client) error {
		if n < 1 {
			return errors.New("chatly: WithMaxAttempts: must be >= 1")
		}
		c.retry.MaxAttempts = n
		return nil
	}
}

// WithUserAgentSuffix appends your application's name to the User-Agent, e.g.
// "acme-billing/2.1". It is how Chatly support can tell your traffic apart
// from every other Go integration; the SDK's own token stays first.
func WithUserAgentSuffix(suffix string) Option {
	return func(c *Client) error {
		suffix = strings.TrimSpace(suffix)
		if suffix == "" {
			return nil
		}
		c.userAgent = "chatly-sdk-go/" + Version + " " + suffix
		return nil
	}
}

// WithHeader adds a header to every request. It cannot override the headers
// the SDK owns — Authorization, Idempotency-Key, Accept, Content-Type,
// User-Agent — because silently breaking authentication or the idempotency
// contract from a config option is not a trade worth offering.
func WithHeader(name, value string) Option {
	return func(c *Client) error {
		switch http.CanonicalHeaderKey(name) {
		case "Authorization", "Idempotency-Key", "Accept", "Content-Type", "User-Agent":
			return fmt.Errorf("chatly: WithHeader: %s is owned by the SDK", name)
		}
		if c.extraHeaders == nil {
			c.extraHeaders = http.Header{}
		}
		c.extraHeaders.Add(name, value)
		return nil
	}
}

// New builds a client for a workspace API key.
//
// The key is rejected here, at construction, rather than at the first call:
// an empty CHATLY_API_KEY is nearly always an unset environment variable, and
// finding that out at process start beats finding it out as a 401 in the
// middle of a request an hour later.
func New(apiKey string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("chatly: New: api key is empty (expected a ck_… workspace key)")
	}

	c := &Client{
		apiKey:     apiKey,
		baseURL:    DefaultBaseURL,
		userAgent:  "chatly-sdk-go/" + Version,
		httpClient: &http.Client{},
		timeout:    DefaultTimeout,
		retry:      DefaultRetryPolicy(),
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	attachServices(c)
	return c, nil
}

// WithIdempotencyKey returns a copy of the client whose every write carries
// this exact Idempotency-Key instead of a fresh one per call.
//
// Use it to retry the SAME logical operation across process restarts or
// across your own retry loop: same key + same body replays the first
// response, same key + a different body is a 409. Do NOT hold one of these
// for unrelated writes — the second write would 409 against the first.
//
//	key, _ := chatly.NewIdempotencyKey()
//	pinned := client.WithIdempotencyKey(key)
//	contact, err := pinned.Contacts.Upsert(ctx, input) // safe to repeat
//
// The receiver is unchanged.
func (c *Client) WithIdempotencyKey(key string) *Client {
	clone := *c
	clone.pinnedIdempotencyKey = key
	// Re-point every namespace at the clone. This is one line because
	// attachServices is the single place namespaces are wired; the Node SDK
	// rebuilt them by hand in its clone path and forgot one, so a derived
	// client's `search` namespace still called the ORIGINAL client — which
	// would have been a live bug the moment that namespace gained a write.
	attachServices(&clone)
	return &clone
}

// BaseURL is the host this client talks to. Exported for diagnostics: an
// integration that is somehow pointed at the wrong install should be able to
// log where it is sending a workspace key.
func (c *Client) BaseURL() string { return c.baseURL }

// IdempotencyKey is the pinned key, or "" when each write mints its own.
func (c *Client) IdempotencyKey() string { return c.pinnedIdempotencyKey }

// WhoAmI is what GET /v1/api-keys/whoami answers about the credential.
//
// Only the two fields the SDK models are here; any others the API adds are
// dropped. Use Client.Request on the same path for the full document.
type WhoAmI struct {
	WorkspaceID string   `json:"workspaceId"`
	Scopes      []string `json:"scopes"`
}

// Whoami describes the credential this client is holding: which workspace it
// belongs to, and which scopes it was granted.
//
// Call it first, at integration time. It is the only route on the api-keys
// controller that accepts an API key — managing keys with a key is a
// privilege-escalation shape — and it turns a mis-scoped key into one
// obvious error here instead of a confusing 403 from an unrelated endpoint
// later. If this returns ErrUnauthorized the key is wrong; if a later call
// returns ErrForbidden while this one works, the key is fine and is missing a
// scope.
func (c *Client) Whoami(ctx context.Context) (*WhoAmI, error) {
	var out WhoAmI
	if err := c.getInto(ctx, "/v1/api-keys/whoami", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Request is the escape hatch: any key-accessible route, through the same
// auth, idempotency, retry and error handling as the typed methods.
//
// 86 of the 141 key-accessible routes have no typed method in this package.
// Without this, reaching one means abandoning the SDK and hand-rolling HTTP
// with a bearer token — and then reimplementing idempotency keys and retries,
// usually not at all.
//
//	raw, err := client.Request(ctx, "POST", "/v1/some/route", nil, map[string]any{"x": 1})
//
// path must start with "/" and should include the version prefix ("/v1/…").
// query may be nil. body may be nil; when non-nil it is JSON-encoded. The
// returned bytes are the response body verbatim, or nil for a 204.
func (c *Client) Request(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("chatly: Request: path must start with '/', got %q", path)
	}
	resp, err := c.do(ctx, strings.ToUpper(method), path, query, body)
	if err != nil {
		return nil, err
	}
	if resp.Status == http.StatusNoContent || len(resp.Body) == 0 {
		return nil, nil
	}
	return json.RawMessage(resp.Body), nil
}

// RequestInto is Request with the response decoded into out. A nil out
// discards the body, which is how the DELETE methods in this package work.
func (c *Client) RequestInto(ctx context.Context, method, path string, query url.Values, body, out any) error {
	raw, err := c.Request(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("chatly: decoding %s %s response: %w", method, path, err)
	}
	return nil
}

// Fetch is the generic form of the escape hatch, for callers who want their
// own types:
//
//	page, err := chatly.Fetch[chatly.Page[MyContact]](ctx, client, "GET", "/v1/contacts", nil, nil)
//
// It is a function and not a method because Go methods cannot have type
// parameters.
func Fetch[T any](ctx context.Context, c *Client, method, path string, query url.Values, body any) (T, error) {
	var out T
	if err := c.RequestInto(ctx, method, path, query, body, &out); err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// --- internal request helpers used by every resource file -------------------

func (c *Client) getInto(ctx context.Context, path string, query url.Values, out any) error {
	return c.RequestInto(ctx, http.MethodGet, path, query, nil, out)
}

func (c *Client) getObject(ctx context.Context, path string, query url.Values) (Object, error) {
	var out Object
	if err := c.getInto(ctx, path, query, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) getObjects(ctx context.Context, path string, query url.Values) ([]Object, error) {
	var out []Object
	if err := c.getInto(ctx, path, query, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) getPage(ctx context.Context, path string, query url.Values) (*Page[Object], error) {
	var out Page[Object]
	if err := c.getInto(ctx, path, query, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) writeObject(ctx context.Context, method, path string, query url.Values, body any) (Object, error) {
	var out Object
	if err := c.RequestInto(ctx, method, path, query, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) writeObjects(ctx context.Context, method, path string, body any) ([]Object, error) {
	var out []Object
	if err := c.RequestInto(ctx, method, path, nil, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListOptions are the four query parameters every list endpoint in the API
// accepts, plus a bag for anything else.
//
// A nil *ListOptions is valid everywhere one is taken, and means "defaults".
type ListOptions struct {
	// Cursor starts the page after a previous page's NextCursor. Leave empty
	// for the first page. The auto-paginating ListAll methods manage this
	// for you.
	Cursor string

	// Limit is the page size, 1..100. Zero means the API's default (25 on
	// most routes, 50 for conversation messages).
	Limit int

	// Sort is a route-specific sort key, e.g. "-createdAt". Each route
	// allows its own small set and ignores anything else.
	Sort string

	// Fields narrows the returned columns, comma-separated.
	Fields string

	// Filter holds `field:value` or `field:op:value` clauses, where op is one
	// of eq, ne, in, gt, gte, lt, lte, null, notnull. Max 32 clauses.
	//
	// Sent as a REPEATED plain `filter` parameter, not `filter[]` and not
	// comma-joined. The bracketed form is accepted by only some routes:
	// the API's query parser leaves the key as the literal "filter[]" and
	// routes that do not explicitly fold it discard the clause silently —
	// 200 OK, every row, no filter applied. The plain repeated key works
	// everywhere, so that is what this sends.
	Filter []string

	// Params is anything else the route accepts (see each method's doc for
	// its own parameters). Repeated keys stay repeated.
	Params url.Values
}

func (o *ListOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	for k, vs := range o.Params {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Sort != "" {
		q.Set("sort", o.Sort)
	}
	if o.Fields != "" {
		q.Set("fields", o.Fields)
	}
	for _, f := range o.Filter {
		q.Add("filter", f)
	}
	return q
}

// pathEscape makes a caller-supplied id safe inside a path segment.
//
// Used on every {id} and {tag} interpolation. Contact tags are
// caller-defined strings that routinely contain spaces and can contain a
// slash, and an unescaped one would silently retarget the request at a
// different route.
func pathEscape(segment string) string { return url.PathEscape(segment) }
