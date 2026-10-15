# The Chatly backend-SDK contract

Every backend SDK in this repo — Node, Python, Go, Ruby, PHP, Java, .NET,
Rust — implements the same thing. This file is what "the same thing" means.
`surface.json` beside it lists the methods; this file says how they behave.
`scripts/check-sdk-surface-parity.mjs` enforces the list. Nothing enforces
the prose, so read it.

## What these SDKs are for

A **backend** SDK. It authenticates with a workspace API key (`ck_…`), from
server-side code. It is not the browser SDK (`@chatlychat/sdk-js`) and must
never be shipped to a browser: the key is a bearer credential for the whole
workspace.

Only key-accessible routes are surface. 162 of the deployed API's 916
operations carry `@AllowApiKey()` — the authoritative figure is
`keyAccessible` in `docs/api-counts.json`, generated from the deployed
OpenAPI document, and it is 163 in a local build because
`visitors.start_conversation` opened on 2026-10-14 and has not shipped yet.
Do NOT take that number from `report-api-key-surface.mjs`, which counts
DECORATORS rather than routes and so always reads higher; `$auditedAgainst`
in `surface.json` has the whole trap written out.

The rest answer a `ck_` key with 403 because they need a signed-in person — a
reaction records _who_ reacted, and a key is not a person. Shipping a method
that always fails is worse than not shipping it: the failure arrives at
runtime, in production, in someone else's integration. `surface.json` covers
every key-accessible route today; a route that opens server-side before the
clients catch up is reachable through the escape hatch below until they do.

## 1. Construction

- One required input: the API key. One optional: the base URL, defaulting to
  **`https://api.chatlychat.com`**.
- **Never default to a development or example host.** `api.livechat.local`
  shipped in two SDKs and `api.chatly.example` in two more; all four resolve
  nowhere, so every call failed for anyone who followed the docs and did not
  override it. A customer reported it; no test caught it, because every test
  passes an explicit URL.
- Optional: request timeout (default 15s), retry policy, an injectable HTTP
  transport so tests need no network.
- Reject an empty key at construction, not at the first call.

## 2. Request shape

- `Authorization: Bearer <key>`, `Accept: application/json`,
  `Content-Type: application/json` on bodies.
- `User-Agent: chatly-sdk-<lang>/<version>`.
- Every write (POST/PATCH/PUT/DELETE) carries an `Idempotency-Key` header.
  Mint a fresh UUID per call by default, so an SDK-level retry cannot
  double-write. Offer a way to **pin** a key across calls, for retrying the
  _same_ logical operation: same key + same body replays the first response,
  same key + different body is a 409.
- Query parameters: `cursor`, `limit`, `filter[]`, `sort`, `fields`. Repeated
  keys stay repeated — `kinds=kb&kinds=ticket` is two parameters, not one
  comma-joined value.
- **Send `kinds` as a repeated PLAIN key, not `kinds[]`.** This document said
  `kinds[]` until 2026-10-12 and that spelling did not work: the search query
  schema is a zod object, so the bracketed key was an unknown key and got
  stripped — the search ran unnarrowed and returned 200. `sdk-node`, the MCP
  server and the dashboard all sent it. The schema now folds `kinds[]` and a
  lone scalar too, but plain repeated keys are the form to send. `filter[]`
  genuinely is bracketed; it has its own fold, for the same reason.

## 3. Errors

A typed error, never a bare string. It carries, at minimum:

| Field        | From                                                |
| ------------ | --------------------------------------------------- |
| `status`     | HTTP status                                         |
| `code`       | the API's `code` field in the JSON body             |
| `message`    | the API's `message`                                 |
| `request_id` | the `x-request-id` response header                  |
| `body`       | the raw response text, for anything the above loses |

Subtype or expose predicates for the cases a caller branches on:
authentication (401), permission/scope (403), not found (404), conflict and
idempotency replay mismatch (409), validation, rate limit (429, with
`retry_after`), and server (5xx). A caller must be able to tell "my key is
wrong" from "my key lacks a scope" without parsing a message string.

**Schema validation is 400 on this API. 422 is a different failure and it
does happen.** `ValidationError` in `packages/shared/src/errors` is
`httpStatus: 400, code: 'validation_failed'`, and the exception filter maps a
raw `ZodError` the same way — so a malformed body is a 400, and a client that
mapped only 422 put the commonest error in its generic branch. The Java SDK
shipped exactly that.

But 422 is NOT unused. `macro-action-runner.ts` throws it twice, naming the
action and its index, when a macro row references an action that has been
removed from the registry or one fails mid-run — and `POST /v1/macros` and
`PATCH /v1/macros/{id}` are both key-accessible, so an SDK caller can see it.
`hired-agents.service.ts` throws it twice more.

Map BOTH to your validation type. This paragraph read "nothing returns 422"
for half a day, which was wrong; the author of the Java SDK found it by
reading the handlers rather than believing the contract.

**The error body is NESTED: `{"error": {"code", "message"}}`.** `wireError`
in `packages/shared/src/errors` is the single source of that envelope. A
client reading `code` at the top level gets null for every real refusal —
that bug shipped in the widget. Read `error.code` first and fall back to the
top level.

A transport failure (DNS, connect, timeout) is its own error type, distinct
from an HTTP status.

## 4. Retries

Retry only what is safe: connection errors, 429, and 5xx. Never retry a 4xx
that is not 429. Exponential backoff with jitter, 3 attempts by default,
honour `Retry-After` when present. A retried write reuses the same
`Idempotency-Key` as the attempt that failed — that is the whole reason the
header exists.

## 5. Pagination — and the shape of the answer

### 5a. Four shapes, and the manifest says which

`surface.json` carries a `shape` on every entry: the TOP-LEVEL shape of a
successful body, read off the handler's `@ZodResponse` schema (or, where it
has none, the service return type it forwards). There are four, and every
route is exactly one of them:

| `shape`  | What comes back                                 | Count |
| -------- | ----------------------------------------------- | ----: |
| `array`  | a bare JSON array                               |    33 |
| `page`   | the cursor envelope — `items` plus `nextCursor` |    13 |
| `object` | a JSON object that is not a page                |   116 |
| `empty`  | no body at all, a real 204                      |     1 |

These four numbers are `$shapeMeans` in `surface.json`, and that is the copy
to trust. This table sat at 14 / 10 / 30 / 1 — the 55-route era — while the
manifest grew past 160, which is what `$shapeMeans` means when it says the
numbers had not moved since the manifest covered 55 routes. If this table
disagrees with what `check-sdk-surface-parity.mjs` prints, the script is
right and this is stale.

A `page` may carry fields beside the two that define it, and nine do.
`hasMore` on the seven that go through `buildPaginatedResponse` —
`tickets.list`, `kb.articles`, `events.list`, `webhooks.deliveries`,
`bots.list`, `bots.deliveries` and `triggers.list`. `total` on
`conversations.unattended` (the badge number, which ignores the cursor).
`tier` plus `facets` on `search.query`. The remaining four are built by the
`pageOf` helpers local to the contacts and conversations modules and carry
nothing else at all — so `hasMore` is on seven of thirteen, still not on all
of them, and a client that reads it as a required field is wrong about six
routes. A
decoder must tolerate the extras rather
than reject the body; `search.query`'s single-page form in particular must
return the WHOLE envelope, because a client that yields only hits throws the
facets away.

**Derive the shape from the schema, never from the route's name.** Three
routes read like one thing and answer another:

- `contacts.remove_tag` — `DELETE /v1/contacts/{id}/tags/{tag}` — answers
  **200 with the surviving contact**, the same object the tag POST returns.
- `conversations.cancel_scheduled` — a DELETE — answers **200 with the row it
  just cancelled**, so a caller can show what it called off.
- `macros.delete` is the **only** real 204 on the surface.

And two of the four `reports.*` routes are bare arrays (`reports.agents`,
`reports.conversations_by_day`) while the other two are objects.

The thirteen `page` routes are exactly the thirteen carrying `paged: true`.
`scripts/check-sdk-surface-parity.mjs` asserts that correspondence in both
directions, so the two fields cannot drift apart: `shape` says what comes
back, `paged` says the SDK also offers a walker over it.

**Every SDK declares its own expected shape per route, by hand, and writes it
into `surface.lock.json`.** It is deliberately NOT read from the manifest.
A fake transport fed from the manifest agrees with whatever the manifest says,
including when it is wrong — which is not hypothetical: `macros.list` was
flagged `paged` for a day and is `@PaginationExempt`, answering a bare
200-row array, and the .NET and Rust clients both raised a decode error on it
in production while their suites were green. The Rust one was worse than a
throw: its derived `Page` deserializer read the bare array as an _empty page_
and returned it with a 200, which is a wrong answer rather than a missing
error. So the gate diffs eight independently written columns against the
manifest, and a wrong manifest row needs eight authors to misread the same
handler.

### 5b. Pagination

List endpoints return `{ items, nextCursor }`, where `nextCursor` is null on
the last page. Every SDK offers both:

1. the single page, as returned, and
2. an **auto-paginating iterator** in the language's own idiom — a generator
   in Python, a channel-free `Iterator`/`Seq` in Go and Java, `each` in Ruby,
   `IAsyncEnumerable` in .NET, a `Stream`/`Iterator` in Rust, `Generator` in
   PHP. It must stop on a null cursor and must not spin forever when the
   server repeats a cursor.

Never sort or page on `id`. Ids in this product are mixed UUID v4 and v7, so
`id` order is not time order; the cursor is the only correct sequence.

## 6. Webhook verification

`verify_webhook_signature(body, signature_header, secret, tolerance=300)`.

- The header is `x-livechat-signature: t=<unix seconds>,v1=<hex>`.
- `v1` is HMAC-SHA256 over the exact bytes `` `<t>.<raw body>` ``. The
  timestamp is bound INTO the digest; that is what makes the replay window
  enforceable.
- Verify against the **raw bytes received**. Re-serialising parsed JSON
  reorders keys and changes whitespace, and the digest will not match. Take
  bytes or a string, never a decoded object.
- Compare in constant time, and compare lengths first where the language's
  constant-time compare throws on a mismatch — a verifier that throws is an
  accepted webhook in a handler that forgot to catch.
- Return false, do not throw, for a malformed header, a missing field, a
  non-numeric `t`, or a timestamp outside tolerance.

An earlier Node implementation HMAC'd the body alone and so rejected 100% of
genuine deliveries. There is a test for the exact construction; keep it.

## 7. Identity hash

`identity_hash(secret, external_id)` → lowercase hex HMAC-SHA256 of
`external_id` under the workspace's identity-verification secret. This is the
backend half of widget identity verification; the secret must never reach a
browser, only the digest. Reject an empty secret or an empty id.

## 8. The escape hatch

A public `request(method, path, query?, body?)` that returns the decoded JSON
and goes through the same auth, idempotency, retry and error handling.

This said "85 of the 140 key-accessible routes have no typed method", and as
of 2026-10-14 that figure is **zero**: all eight SDKs cover the whole
manifest and the manifest covers every key-accessible route. The hatch is not
redundant for that. A route opens server-side before eight clients catch up —
`visitors.start_conversation` was in exactly that state for part of a day —
and without the hatch, reaching it means abandoning the SDK and hand-rolling
HTTP with a bearer token.

## 9. Shape of the code

- The language's own idiom wins over cross-language symmetry: `snake_case` in
  Python and Rust, `CamelCase` in Go, `camelCase` in PHP and Java, PascalCase
  methods in .NET.
- **Zero runtime dependencies** wherever the standard library can do it —
  Python (`urllib`), Go (`net/http`), Ruby (`net/http`), PHP (`curl`), Java
  (`java.net.http`), .NET (`HttpClient`), Rust is the exception and may use
  `ureq` or `reqwest` plus `serde`. A customer should not inherit our
  dependency tree to send a message.
- No file over 650 lines. Split by responsibility — transport, errors,
  pagination, resources, webhooks — not by layer.
- Tests run offline against an injected transport or a loopback HTTP server.
  No test may require network, a key, or a running API.

## 10. What every SDK's tests must cover

Parity here is checked by `scripts/check-sdk-surface-parity.mjs` (the method
list) and by each SDK's own suite (the behaviour). At minimum:

1. The default base URL is the production host.
2. `Authorization` carries the key.
3. A write sends an `Idempotency-Key`; two writes send different ones; a
   pinned key sends the same one twice.
4. A 404 raises the not-found type, a 422 the validation type, a 429 the
   rate-limit type with `retry_after` populated, and each carries
   `request_id`.
5. A 429 is retried and then succeeds; a 400 is **not** retried.
6. The auto-paginator walks two pages and stops on a null cursor.
7. `verify_webhook_signature` accepts a genuine `t=…,v1=…` built over
   `<t>.<body>`, rejects a digest over the body alone, rejects a stale `t`,
   rejects a malformed header, and does not throw on any of them.
8. `identity_hash` matches a known vector:
   `identity_hash("sek_test_secret", "user_42")` =
   `5e26772dce97d5ba995796e621f83f2e97e6f8c3f9386dbf153a98ab24c10271`
   This vector is computed and cross-checked in two implementations. If
   yours disagrees, yours is wrong. The parity gate runs each SDK's vector
   test and compares the digests across languages.
9. `verify_webhook_signature` accepts this exact delivery:
   - secret `whsec_test`
   - body `{"id":"evt_1"}` (those 14 bytes, no trailing newline)
   - header `t=1700000000,v1=c89214b5b5da833daed6f0b8c5bb6bd58cea9022bd80ccc78230f3942d632925`
   - `tolerance` wide enough to admit a 2023 timestamp (pass infinity, or a
     fixed `now` of 1700000000)
     and rejects the same delivery with the digest taken over the body alone.
10. The surface lock covers **response shapes**, in both directions. The
    suite declares, by hand and not from the manifest, the shape it expects
    from each of the manifest's routes (§5a); feeds each method a body of
    THAT shape;
    asserts the value handed back really has it — which is what catches a
    method that wraps or unwraps; asserts the distribution is still
    33 `array` / 13 `page` / 116 `object` / 1 `empty`, so a column of 163
    `object`s cannot pass; and feeds every auto-paginator a bare array where
    it expects a page and requires it to **fail loudly**. The last one is the
    only part that proves the declaration matters: a decoder that ignores the
    body passes everything else. The declared column goes into
    `surface.lock.json` and the parity gate diffs all eight against the
    manifest.
