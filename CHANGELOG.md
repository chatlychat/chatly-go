# Changelog

All notable changes to the Chatly Go SDK are recorded here. This project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `Bots.RotateExternalSecret` takes an optional
  `RotateExternalSecretOptions{ActivateAt time.Time}` (or `ActivateAtRaw`
  string) and sends `{"activateAt": "..."}`: the old signing secret keeps
  signing deliveries until that instant, at most 168h ahead. Variadic, so
  `RotateExternalSecret(ctx, id)` compiles unchanged and still sends no body
  — the hard cut. The response now carries `activatesAt` (null = the new
  secret is already signing).
- **The whole key-accessible surface: 107 new methods, 162 in total** — the
  manifest and the set of routes carrying `@AllowApiKey()` are now the same
  162, with no gap in either direction, so `Request` is for what the API adds
  next rather than for a known omission.
- `Client.Me` (`GET /v1/me`), `Workspace.Mine` (`GET /v1/workspaces/me`) and
  `Webhooks.Delete` (`DELETE /v1/webhooks/{id}`): three routes this SDK
  reported as key-accessible but absent from the manifest, listed later the
  same day. `Me` needs no permission and answers on a suspended workspace,
  which makes it the liveness check `Whoami` (`workspace:read`) cannot be;
  its body is discriminated on `principal`, hence an `Object` rather than a
  struct. `Mine` holds exactly one entry for a key — the tenant it points at.
  `Webhooks.Delete` closes an asymmetry where a key could register a delivery
  target and never remove one.
- **104 of those methods, 159 in total, in the first pass.** Eleven
  new namespaces — `AI`, `Bots`, `BotRoutes`, `Brands`, `CanvasSteps`,
  `CustomerCards`, `CustomerCardEndpoints`, `DocsSources`, `EmailTemplates`,
  `KnowledgeGaps`, `Triggers` — plus additions to `Contacts` (`Cards`,
  `Mood`), `Conversations` (`Linked`, `UnreadCounts`, `UpdateBlocks`,
  `ReturnToBot`, `Outcomes`, `RecordOutcome`), `Tickets` (`Conversations`,
  `OpenCount`, `Time`, `StatusHistory`, `Lineage`), `Macros` (`Get`,
  `Actions`, `Templates`, `Template`), `Webhooks` (`List`, `Create`,
  `Events`, `SchemaVersions`, `SetSchemaVersion`), `Reports` (nine),
  `Channels` (`Adapters`, `Create`, `Update`, `RotateSecret`), `Billing`
  (`Summary`, `Invoices`, `UsageDaily`, `UsageMonthly`) and `Workspace`
  (`KeyScopes`, `Secrets`).
- Three new auto-paginators, for the three `page` routes that came with it:
  `Bots.ListAll`, `Bots.DeliveriesAll`, `Triggers.ListAll`. The manifest's
  `paged` count is 13 now, and `billing.invoices` is deliberately NOT one of
  them: it is cursor-paginated but spells its envelope `{data, nextCursor}`,
  so the generic walker — which reads `items` — cannot be pointed at it and
  would report an empty page forever.
- `Client.getStrings` behind `Channels.Adapters`, the one route on the
  surface whose bare array holds strings rather than objects
  (`z.array(z.string())`). A method built on the usual decoder would have
  failed on every real call, while the shared `[{"id":"x_1"}]` fixture
  happily agreed with it — hence `nonObjectArrayFixtures` in the suite, which
  overrides that one body and is itself asserted to still be an array.
- `chatly.Bool` and `chatly.Float64`, for the input fields whose SERVER-SIDE
  DEFAULT IS TRUE or whose zero value is meaningful. A plain `bool` with
  `omitempty` could not tell "leave it alone" from "turn it off" and would
  silently enable the thing you were disabling.

### Changed

- The surface lock is 162 rows; the suite's floor moved 55 → 159 → 162 and the
  declared shape distribution 14/10/30/1 → 32/13/113/1 → 33 array / 13 page /
  115 object / 1 empty, each row read from its handler's `@ZodResponse`. Both are
  anti-vacuity figures: a column of 162 `object`s would satisfy every other
  assertion here.
- The 162-row table moved out of `surface_test.go` into
  `surfacecases_test.go` (the conversational surface) and
  `surfacecases_config_test.go` (the configuration surface), because one file
  of it was well past this repo's 650-line ceiling.

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
- Each lock row also carries the top-level response `shape` that route
  answers — `array`, `page`, `object` or `empty`. The column is hand-written
  in `surfaceCases` from the handlers and deliberately NOT read from the
  shared manifest: a fake transport fed the manifest's own shape agrees with
  whatever the manifest says, including when it is wrong, which is how two
  sibling SDKs shipped a paginator over the bare-array `GET /v1/macros` with
  green suites. The fake now answers each route with a body of that route's
  declared shape, every method is asserted to hand back a value of that
  shape, and every auto-paginator is asserted to ERROR on a bare array rather
  than return an empty page.

### Known limitations

Both are API-side and documented at the method and in the README rather than
hidden:

- `SearchOptions.Kinds` with exactly one value is rejected by the API with
  400: its schema wants an array and its query parser yields a bare string
  for a single occurrence. Pass two or more kinds, or filter client-side.
- `APIError.RequestID` reads `x-request-id`, which the API does not currently
  set on any route. Expect it to be empty unless your own edge adds it.
