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

Only key-accessible routes are surface. 140 of the API's 886 routes carry
`@AllowApiKey()`; the rest answer a `ck_` key with 403 because they need a
signed-in person — a reaction records *who* reacted, and a key is not a
person. Shipping a method that always fails is worse than not shipping it:
the failure arrives at runtime, in production, in someone else's
integration. `surface.json` is the subset we hand-wrote methods for; every
other key-accessible route is reachable through the escape hatch below.

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
  *same* logical operation: same key + same body replays the first response,
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

| Field | From |
| --- | --- |
| `status` | HTTP status |
| `code` | the API's `code` field in the JSON body |
| `message` | the API's `message` |
| `request_id` | the `x-request-id` response header |
| `body` | the raw response text, for anything the above loses |

Subtype or expose predicates for the cases a caller branches on:
authentication (401), permission/scope (403), not found (404), conflict and
idempotency replay mismatch (409), validation, rate limit (429, with
`retry_after`), and server (5xx). A caller must be able to tell "my key is
wrong" from "my key lacks a scope" without parsing a message string.

**Validation is 400 on this API, not 422.** `ValidationError` in
`packages/shared/src/errors` is `httpStatus: 400, code: 'validation_failed'`,
and the exception filter maps a raw `ZodError` the same way; nothing returns
422. Map both, so a client is correct either way, but do not expect 422.

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

## 5. Pagination

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
and goes through the same auth, idempotency, retry and error handling. 85 of
the 140 key-accessible routes have no typed method; without this, reaching
one means abandoning the SDK and hand-rolling HTTP with a bearer token.

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
