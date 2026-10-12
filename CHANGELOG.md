# Changelog

All notable changes to the Chatly Go SDK are recorded here. This project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — 2026-10-12

First release.

### Added

- `chatly.New(apiKey, opts...)` with functional options: `WithBaseURL`,
  `WithHTTPClient`, `WithRoundTripper`, `WithTimeout`, `WithMaxAttempts`,
  `WithRetryPolicy`, `WithUserAgentSuffix`, `WithHeader`. An empty API key is
  rejected at construction. The default base URL is the production host,
  `https://api.chatlychat.com`.
- 55 typed methods across twelve namespaces — `Contacts`, `Conversations`,
  `Tickets`, `Macros`, `KB`, `Search`, `Events`, `Webhooks`, `Reports`,
  `Channels`, `Billing`, `Workspace` — plus `Client.Whoami`. Every one takes
  a `context.Context` first.
- `Client.Request` / `RequestInto` / `Fetch[T]`: the escape hatch to any
  key-accessible route, through the same auth, idempotency, retry and error
  handling as a typed method.
- Typed errors. `*APIError` carries `Status`, `Code`, `Message`, `RequestID`,
  `Body`, `RetryAfter`, `Method`, `Path` and `Attempts`, and matches the
  sentinels `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrConflict`,
  `ErrValidation`, `ErrRateLimited`, `ErrServer` and the catch-all `ErrAPI`
  via `errors.Is`. `*TransportError` is distinct, matches `ErrTransport`, and
  unwraps to its cause so `errors.Is(err, context.DeadlineExceeded)` works.
- Retries for connection errors, 429 and 5xx only — three attempts by
  default, exponential backoff with full jitter, `Retry-After` honoured, and
  a cap past which a long `Retry-After` surfaces instead of parking the
  caller's goroutine. A 4xx that is not 429 is never retried.
- An `Idempotency-Key` on every write, minted once per call and reused by
  every retry of it. `Client.WithIdempotencyKey(key)` returns a derived
  client that pins one across calls; `NewIdempotencyKey()` mints one.
- Auto-pagination: `Pager[T]` (`Next(ctx)` / `Value()` / `Err()`, plus
  `Pages`, `Count` and `Collect`) and a `Seq(ctx)` adapter returning an
  `iter.Seq2[T, error]` for range-over-func. The pager stops on a null cursor
  and returns `ErrCursorLoop` rather than spinning if the server repeats one.
- `VerifyWebhookSignature` / `VerifyWebhook`: constant-time HMAC-SHA256 over
  `<t>.<raw body>`, 300s default tolerance, `WebhookToleranceUnlimited` for
  archived deliveries, and a bool return that never panics on a malformed
  header. `SignWebhookPayload` builds a genuine delivery for your own tests.
- `IdentityHash(secret, externalID)` for widget identity verification.
- Zero third-party dependencies: `net/http`, `crypto/hmac`, `encoding/json`
  and friends only. Go 1.23+ (for `iter`).

- `surface.lock.json`: a committed record of the verb and path every one of
  the 55 methods was observed to request, emitted by `surface_test.go` from
  the requests themselves and templated back against the manifest's own
  parameter names. The cross-language parity gate reads this instead of
  grepping source text, where a path in a comment counted as a call. The test
  rewrites the file and fails when it differed, so a green run is the proof
  that the committed artifact matches the code.

### Known limitations

Both are API-side and documented at the method and in the README rather than
hidden:

- `SearchOptions.Kinds` with exactly one value is rejected by the API with
  400: its schema wants an array and its query parser yields a bare string
  for a single occurrence. Pass two or more kinds, or filter client-side.
- `APIError.RequestID` reads `x-request-id`, which the API does not currently
  set on any route. Expect it to be empty unless your own edge adds it.
