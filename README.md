# chatly-go

The official Go SDK for [Chatly](https://chatlychat.com) — a live-chat and
customer-messaging platform: a chat widget on your site, an agent inbox,
tickets, a help centre, and automation across email, WhatsApp, SMS and the
other channels a workspace has connected.

This package talks to Chatly's **backend** API with a workspace API key. Zero
third-party dependencies: `net/http`, `crypto/hmac` and `encoding/json` from
the standard library, and nothing else.

```bash
go get github.com/chatlychat/chatly-go
```

Requires Go 1.23 or newer.

---

## Before your first call: what this key is

A Chatly API key (`ck_…`) is a bearer credential for an **entire workspace** —
every contact, every conversation, every ticket in it. Treat it like a
database password:

- Server-side only. Never ship it to a browser, a mobile app, or a CLI you
  distribute. The browser widget uses a _public channel id_ instead, which you
  can read with `client.Channels.List` (the `publicId` field).
- Load it from the environment or a secret manager, never from source.
- It is one of **three** different Chatly secrets, and they are not
  interchangeable: the API key authenticates this SDK, the `whsec_…` webhook
  secret verifies deliveries _you_ receive, and the `sek_…` identity secret
  signs widget user hashes.

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	chatly "github.com/chatlychat/chatly-go"
)

func main() {
	client, err := chatly.New(os.Getenv("CHATLY_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	me, err := client.Whoami(ctx) // which workspace is this key for?
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("workspace:", me.WorkspaceID, "scopes:", me.Scopes)

	contact, err := client.Contacts.Upsert(ctx, chatly.ContactInput{
		ExternalID: "user_42",
		Email:      "ada@example.com",
		Name:       "Ada Lovelace",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("contact:", contact["id"])
}
```

`Whoami` is the call to make first. It is the only route on the API-keys
controller that accepts a key, and it turns a wrong or under-scoped key into
one obvious error at integration time instead of a puzzling 403 from an
unrelated endpoint next week.

## Construction

```go
client, err := chatly.New(apiKey,
	chatly.WithBaseURL("http://127.0.0.1:3001"), // another environment, or a test server
	chatly.WithHTTPClient(myHTTPClient),
	chatly.WithTimeout(30*time.Second),
	chatly.WithMaxAttempts(5),
	chatly.WithUserAgentSuffix("acme-billing/2.1"),
	chatly.WithHeader("X-Trace", "abc"),
)
```

| Option                | Default                      | Notes                                                                                                                   |
| --------------------- | ---------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `WithBaseURL`         | `https://api.chatlychat.com` | Another Chatly environment, or a test server.                                                                           |
| `WithHTTPClient`      | `&http.Client{}`             | Your own client: proxies, connection pools, instrumentation.                                                            |
| `WithRoundTripper`    | —                            | Swap only the transport, keep the rest.                                                                                 |
| `WithTimeout`         | 15s                          | **Per attempt**, as a context deadline. To bound the whole call including retries, pass your own `context.WithTimeout`. |
| `WithMaxAttempts`     | 3                            | Total attempts, not retries. 1 disables retrying.                                                                       |
| `WithRetryPolicy`     | see below                    | Replaces the whole policy.                                                                                              |
| `WithUserAgentSuffix` | —                            | Appended after the SDK's own token.                                                                                     |
| `WithHeader`          | —                            | Added to every request. Cannot override the headers the SDK owns.                                                       |

An empty API key is rejected by `New` rather than at the first call — an
unset `CHATLY_API_KEY` should fail at process start. A `*Client` is safe for
concurrent use: build one and share it.

## Errors

Every failure is one of two types, and you branch on them with `errors.Is`:

```go
page, err := client.Contacts.List(ctx, nil)
switch {
case err == nil:
	// ...
case errors.Is(err, chatly.ErrUnauthorized):
	// the key is wrong, revoked or expired → rotate it
case errors.Is(err, chatly.ErrForbidden):
	// the key is fine but lacks the scope this route needs → edit its scopes
case errors.Is(err, chatly.ErrRateLimited):
	// already retried; back off further
case errors.Is(err, chatly.ErrTransport):
	// nothing was served: DNS, connect, TLS, timeout
default:
	return err
}
```

Telling `ErrUnauthorized` from `ErrForbidden` is the point of having typed
errors at all: they are different fixes, and neither should require matching
on an error message, which is the API's to reword.

| Sentinel          | Status      | Means                                                                      |
| ----------------- | ----------- | -------------------------------------------------------------------------- |
| `ErrUnauthorized` | 401         | Credential missing, malformed, expired or revoked.                         |
| `ErrForbidden`    | 403         | Valid key, missing scope — or a route that needs a signed-in person.       |
| `ErrNotFound`     | 404         | No such resource _in this workspace_.                                      |
| `ErrConflict`     | 409         | Resource conflict, or an idempotency key replayed with a different body.   |
| `ErrValidation`   | 400 and 422 | The request failed the API's schema.                                       |
| `ErrRateLimited`  | 429         | Throttled. `RetryAfter` is populated when the response carried the header. |
| `ErrServer`       | 5xx         | Server-side failure.                                                       |
| `ErrAPI`          | any         | Matches **every** `*APIError`, whatever the status.                        |
| `ErrTransport`    | —           | Matches every `*TransportError`. No HTTP response existed.                 |

`ErrValidation` covers 400 as well as 422 because that is what this API
actually answers for a schema failure (code `validation_failed`); 422 is
mapped too, for the routes that use it.

For the detail, pull the typed error out:

```go
var apiErr *chatly.APIError
if errors.As(err, &apiErr) {
	log.Printf("chatly %d %s: %s (request %q)\n%s",
		apiErr.Status, apiErr.Code, apiErr.Message, apiErr.RequestID, apiErr.Body)
}
```

`Body` is the raw response, kept so nothing the typed fields lose is
unreachable — on a validation failure it carries the per-field
`details.issues` array.

> **`RequestID` is the id the API gave your request.** It reads the
> `x-request-id` response header, which the API sets on every response,
> errors included — quote it in a support request and it finds the exact
> request in our logs. Send your own `x-request-id` and the API reuses that
> value instead, so an id your tracing already knows comes back to you.

A `*TransportError` wraps its cause, so `errors.Is(err, context.DeadlineExceeded)`
works on a timeout.

## Retries and idempotency

The client retries **connection errors, 429 and 5xx** — three attempts by
default, exponential backoff with full jitter, honouring `Retry-After` when
the server sends one. A 4xx that is not 429 is never retried: it will fail
identically forever, and a delay would only hide it.

Every write (`POST`, `PATCH`, `PUT`, `DELETE`) carries an `Idempotency-Key`
header: a fresh UUID per call, minted once and **reused by every retry of that
call**. That is the whole reason the header exists — a write that failed with
a 500 or a dropped connection may or may not have been applied, and replaying
it under the same key either replays the original response or returns an
explicit 409, instead of creating a second contact.

To retry _the same logical operation_ yourself — across a job restart, say —
pin the key:

```go
key, err := chatly.NewIdempotencyKey() // store this with your job
if err != nil {
	return err
}
pinned := client.WithIdempotencyKey(key)
contact, err := pinned.Contacts.Upsert(ctx, input) // safe to repeat forever
```

`WithIdempotencyKey` returns a **derived** client; the original is unchanged.
Same key + same body replays the first response; same key + a _different_ body
is a 409. Do not hold one pinned client for unrelated writes.

For messages specifically, prefer `SendMessageInput.ClientRef`: an
idempotency key dedupes an HTTP request, while `ClientRef` dedupes the
_message_ for the life of the conversation.

## Pagination

List endpoints return `{ items, nextCursor }`, where `nextCursor` is `null`
on the last page. Both forms are available.

One page, as the API returned it:

```go
page, err := client.Contacts.List(ctx, &chatly.ContactListOptions{Limit: 50})
for _, contact := range page.Items {
	fmt.Println(contact["id"])
}
if page.NextCursor != nil {
	// pass *page.NextCursor back as Cursor
}
```

Or walk every page with a `*Pager`:

```go
pager := client.Contacts.ListAll(nil)
for pager.Next(ctx) {
	contact := pager.Value()
	fmt.Println(contact["id"])
}
if err := pager.Err(); err != nil { // ALWAYS check this
	return err
}
```

**Check `Err()`.** A loop that ends because the API returned 403 looks exactly
like one that ends because there were no more contacts.

A Go 1.23 range-over-func form is also there, if you prefer it:

```go
for contact, err := range client.Contacts.ListAll(nil).Seq(ctx) {
	if err != nil {
		return err
	}
	fmt.Println(contact["id"])
}
```

`Pager` is the primary form and `Seq` is the adapter, for three reasons:
`Next` takes a context per step, so a deadline arriving mid-walk is honoured;
the error is retrieved after the loop rather than yielded as a second value a
loop body can ignore; and the explicit form compiles on older Go. `Seq` exists
because range-over-func is what a Go programmer reaches for first now.

The pager stops on a null cursor, and stops with `chatly.ErrCursorLoop` if the
server ever hands back a cursor it has already been asked for — otherwise a
server-side bug becomes an infinite billable loop in your process.

`Collect(ctx, limit)` gathers a walk into a slice; on a large workspace,
prefer `Next`.

Never sort or page on `id`. Chatly ids are a mix of UUID v4 and v7, so id
order is not time order. The cursor is the only correct sequence.

## Webhook verification

Chatly signs every outbound delivery with `x-livechat-signature:
t=<unix>,v1=<hex>`, where `v1` is HMAC-SHA256 over the bytes
`<t>.<raw body>` — the timestamp is signed _into_ the digest, which is what
makes the 5-minute replay window enforceable.

```go
func chatlyWebhook(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}

		if !chatly.VerifyWebhook(raw, r.Header.Get(chatly.WebhookSignatureHeader), secret) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}

		var event struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		// Acknowledge fast; do the work elsewhere.
		w.WriteHeader(http.StatusOK)
		go handle(event.Event, event.Data)
	}
}
```

Three things that are easy to get wrong:

1. **Verify the raw bytes.** Read the body, verify, _then_ unmarshal.
   Re-serialising parsed JSON reorders keys and changes whitespace, and the
   digest will not match.
2. The header is `x-livechat-signature`, not `x-chatly-signature`. Use the
   `chatly.WebhookSignatureHeader` constant.
3. `VerifyWebhook` returns a bool and never panics — a malformed header, a
   non-numeric timestamp, an empty secret and a stale delivery are all plain
   `false`.

For control over the clock or the window, use the long form:

```go
ok := chatly.VerifyWebhookSignature(chatly.WebhookVerification{
	Body:            raw,
	SignatureHeader: r.Header.Get(chatly.WebhookSignatureHeader),
	Secret:          secret,
	Tolerance:       chatly.DefaultWebhookTolerance, // 300s; 0 means this
	Now:             time.Now(),
})
```

Pass `chatly.WebhookToleranceUnlimited` only to re-verify an archived
delivery — on a live endpoint it removes the replay protection.

To test your own handler, build a genuine delivery with
`chatly.SignWebhookPayload(body, secret, time.Now())`.

The delivery log is readable too: `client.Webhooks.Deliveries` for the list,
`client.Webhooks.Delivery` for one including the payload and target URL, and
`client.Webhooks.Redeliver` to queue a failed one again.

## Identity hash (for the browser widget)

When a workspace turns identity verification on, the widget must prove that
the `externalId` it claims was issued by your backend rather than typed into
a browser console. Your server computes the digest; only the digest reaches
the page.

```go
userHash, err := chatly.IdentityHash(os.Getenv("CHATLY_IDV_SECRET"), user.ID)
if err != nil {
	return err
}
// render into the page:
//   Chatly('identify', { externalId: user.ID, userHash: userHash, email: user.Email })
```

It is HMAC-SHA256 of the external id under the workspace's
identity-verification secret (`sek_…`), as lowercase hex. The secret must
never reach a browser, a mobile app or a log line. An empty secret or an empty
id is `ErrEmptyIdentityInput`, not a hash of nothing.

## The escape hatch

The Chatly API has 162 key-accessible routes and this package hand-writes a
method for **every one of them** — as of 2026-10-14 the shared SDK manifest
and the set of routes carrying `@AllowApiKey()` are the same 162, with no gap
in either direction. `Request` is therefore for what the API adds after this
release, and for the things a typed method cannot express. It goes through the
same auth, idempotency, retry and error handling:

```go
// An explicit null, which an input struct's omitempty cannot send:
raw, err := client.Request(ctx, "PATCH", "/v1/ai/tools/"+toolID, nil,
	map[string]any{"signingSecret": nil})
```

```go
// Explicit nulls, which the typed input structs cannot express — this is how
// you unassign a ticket in bulk:
_, err := client.Request(ctx, "POST", "/v1/tickets/bulk", nil, map[string]any{
	"ids":     []string{ticketID},
	"action":  "assign",
	"payload": map[string]any{"assigneeUserId": nil},
})
```

```go
// Decode straight into your own types:
type contact struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}
page, err := chatly.Fetch[chatly.Page[contact]](ctx, client, "GET", "/v1/contacts", nil, nil)
```

`RequestInto` is the non-generic form. `url.Values` is used for the query, so
repeated keys stay repeated.

## Response types

Reads return `chatly.Object` (`map[string]any`), `[]chatly.Object`, or
`*chatly.Page[chatly.Object]` — not generated structs. The API's response
shapes gain fields without a major version, and a struct silently drops every
field it has not been regenerated for. Each method's doc comment lists the
fields that route returns at the time of writing; use `Fetch` when you want
your own types. The signature also tells you the envelope: `[]Object` is a
bare array, `*Page[Object]` is paginated.

## Methods

162 methods across twenty-three namespaces — every key-accessible route in the
API, which is every route on the shared SDK manifest (`packages/sdk-spec/surface.json`). Verb and path are the API route
each one calls.

### Credential

| Method          | Route                     |
| --------------- | ------------------------- |
| `client.Whoami` | `GET /v1/api-keys/whoami` |
| `client.Me`     | `GET /v1/me`              |

`Me` is the **liveness check**: it carries no permission requirement, where
`Whoami` needs `workspace:read`, and it answers on a suspended workspace. So a
key with no useful scope still gets a 200 from it, and that answer is true.
Its body is discriminated on `principal` — a `ck_` key gets
`{principal: "api_key", workspaceId, scopes}`, the same two fields `Whoami`
returns from the same function — so use `Whoami` when you want the typed
struct and `Me` when you want to know whether the credential works at all.

### `client.Contacts`

| Method                   | Route                                 |
| ------------------------ | ------------------------------------- |
| `List` / `ListAll`       | `GET /v1/contacts`                    |
| `Get`                    | `GET /v1/contacts/{id}`               |
| `Upsert`                 | `POST /v1/contacts`                   |
| `BulkUpsert`             | `POST /v1/contacts/bulk`              |
| `Merge`                  | `POST /v1/contacts/{id}/merge`        |
| `AddTags`                | `POST /v1/contacts/{id}/tags`         |
| `RemoveTag`              | `DELETE /v1/contacts/{id}/tags/{tag}` |
| `Unsubscribe`            | `POST /v1/contacts/{id}/unsubscribe`  |
| `Journey` / `JourneyAll` | `GET /v1/contacts/{id}/journey`       |
| `Cards`                  | `POST /v1/contacts/{contactId}/cards` |
| `Mood`                   | `GET /v1/contacts/{id}/mood`          |

`AddTags` adds **one** tag per call — the route's body is `{"tag": "..."}`.
`Merge` keeps the path id and absorbs the other; it is not reversible.
`BulkUpsert` reports partial failure in its response body with HTTP 200, so
read `skipped` and `errors` rather than trusting the status.

`Cards` is a POST that is a **read**: it fetches the operator's customer cards
live from your own backend, so expect multi-second latency, and the collection
key is `cards` rather than `items`. `Mood` has a `Days` **floor of 7**, not 1
like `Journey`, and an empty `buckets` with a low `sampleCount` means "not
enough signal" rather than "no sentiment".

### `client.Conversations`

| Method                     | Route                                                       |
| -------------------------- | ----------------------------------------------------------- |
| `List` / `ListAll`         | `GET /v1/conversations`                                     |
| `Get`                      | `GET /v1/conversations/{id}`                                |
| `Messages` / `MessagesAll` | `GET /v1/conversations/{id}/messages`                       |
| `Send`                     | `POST /v1/conversations/{id}/messages`                      |
| `Unattended`               | `GET /v1/conversations/unattended`                          |
| `AgentHandling`            | `GET /v1/conversations/{id}/agent-handling`                 |
| `SetAgentHandling`         | `PUT /v1/conversations/{id}/agent-handling`                 |
| `GeneratedReplies`         | `GET /v1/conversations/{id}/generated-replies`              |
| `GenerateReply`            | `POST /v1/conversations/{id}/generated-replies`             |
| `ScheduledMessages`        | `GET /v1/conversations/{id}/scheduled-messages`             |
| `ScheduleMessage`          | `POST /v1/conversations/{id}/scheduled-messages`            |
| `CancelScheduled`          | `DELETE /v1/conversations/scheduled-messages/{scheduledId}` |
| `Linked`                   | `GET /v1/conversations/{id}/linked`                         |
| `UnreadCounts`             | `GET /v1/conversations/unread-counts`                       |
| `UpdateBlocks`             | `PATCH /v1/conversations/{id}/messages/{messageId}/blocks`  |
| `ReturnToBot`              | `POST /v1/conversations/{id}/return-to-bot`                 |
| `Outcomes`                 | `GET /v1/conversations/{id}/outcomes`                       |
| `RecordOutcome`            | `POST /v1/conversations/{id}/outcomes`                      |

```go
_, err := client.Conversations.Send(ctx, conversationID, chatly.SendMessageInput{
	Body:      "Your refund is on its way.",
	ClientRef: "refund-" + orderID, // dedupes for the life of the conversation
})
```

`Kind: "note"` posts an **internal** note the contact never sees — the safe
kind for an integration leaving context behind.

`SetAgentHandling` needs a key linked to a **machine user**; otherwise it is
403 however the key is scoped. `GenerateReply` records a proposal for a human
to approve — it does not ask Chatly's AI for a draft, and nothing is
delivered until a person sends it.

`RecordOutcome` reports how a thread ended so the AI funnels count it, and
`escalated` is not merely a record: it moves the conversation to `pending`,
releases bot ownership and posts your reason as an internal note.
`created: false` in its response means an equal outcome was already on record
— still a success. Chatly has no revenue report, so `valueCents` is stored and
summed by nothing.

`Linked` and `Outcomes` both answer an object carrying `items` with **no
cursor**: read the slice, do not point a paginator at it. `UnreadCounts` is
agent-oriented — "unread" is per user, and an API key is not a person, so
`mentions` and `participating` come back as 0.

### `client.Tickets`

| Method             | Route                                               |
| ------------------ | --------------------------------------------------- |
| `List` / `ListAll` | `GET /v1/tickets`                                   |
| `Comments`         | `GET /v1/tickets/{id}/comments`                     |
| `ForConversation`  | `GET /v1/tickets/for-conversation/{conversationId}` |
| `Queues`           | `GET /v1/tickets/queues`                            |
| `Merge`            | `POST /v1/tickets/{id}/merge`                       |
| `Split`            | `POST /v1/tickets/{id}/split`                       |
| `Bulk`             | `POST /v1/tickets/bulk`                             |
| `Conversations`    | `GET /v1/tickets/{id}/conversations`                |
| `OpenCount`        | `GET /v1/tickets/open-count/{conversationId}`       |
| `Time`             | `GET /v1/tickets/{id}/time`                         |
| `StatusHistory`    | `GET /v1/tickets/{id}/status-history`               |
| `Lineage`          | `GET /v1/tickets/{id}/lineage`                      |

```go
page, err := client.Tickets.List(ctx, &chatly.ListOptions{
	Limit:  50,
	Sort:   "-updatedAt",
	Filter: []string{"status:in:open,pending", "priority:urgent"},
})
```

`Comments` is capped server-side at 500 with no cursor. `Bulk` returns
`{updated, skipped}` with HTTP 201 even when everything was skipped.

`OpenCount` takes a **conversation** id, not a ticket id, and counts anything
not resolved or closed. `StatusHistory` holds the 500 newest transitions with
`truncated` as the only signal there are more. In `Lineage`, a non-null
`splitFromTicketId` beside a null `splitFrom` is a dangling pointer — neither
column has a foreign key. `Time` and `Conversations` answer 200 with
emptiness for an unknown ticket rather than 404.

### `client.Macros`

| Method      | Route                             |
| ----------- | --------------------------------- |
| `List`      | `GET /v1/macros`                  |
| `Create`    | `POST /v1/macros`                 |
| `Update`    | `PATCH /v1/macros/{id}`           |
| `Delete`    | `DELETE /v1/macros/{id}`          |
| `Get`       | `GET /v1/macros/{id}`             |
| `Actions`   | `GET /v1/macros/actions`          |
| `Templates` | `GET /v1/macros/templates`        |
| `Template`  | `GET /v1/macros/templates/{slug}` |

`List` returns a **bare array**, not a page: the route sends no cursor and the
service caps the result at 200 rows ordered by usage rank. On a workspace with
more macros than that you get the top 200, so narrow with `Scope`, `Team`,
`Query` or `Language` rather than expecting the whole library.

`Update` replaces any field it mentions — `Tags` sets the whole list, it does
not append.

`Actions` and `Templates` are **objects**, not arrays: `{limit, actions}` and
`{items, total}`. `limit` is the per-macro action ceiling a caller needs in
order to validate a macro before posting it. `Get` has no visibility clause,
so a personal-scope macro belonging to another agent is readable by id even
though `List` would never show it.

### `client.KB`

| Method                     | Route                        |
| -------------------------- | ---------------------------- |
| `Articles` / `ArticlesAll` | `GET /v1/kb/articles`        |
| `Article`                  | `GET /v1/kb/articles/{slug}` |
| `Collections`              | `GET /v1/kb/collections`     |

`Article` takes a **slug**, not an id. Filter articles on
`status:published` before showing them to anyone — the unfiltered list
includes drafts.

### `client.Search`

| Method               | Route            |
| -------------------- | ---------------- |
| `Query` / `QueryAll` | `GET /v1/search` |

```go
res, err := client.Search.Query(ctx, "refund policy", &chatly.SearchOptions{
	Kinds: []string{chatly.SearchKindKB, chatly.SearchKindTicket},
})
```

`Query` returns the whole response object rather than a page, because it
carries `facets` (hit counts per kind) and `tier` (which engine answered)
beyond `items` and `nextCursor`. `rank` is on a different scale per tier,
so never threshold it across tiers.

> **`Kinds` with a single value currently fails.** The route's schema requires
> an array, and the API's query parser hands it a bare string when a parameter
> appears once, so `Kinds: []string{"kb"}` is rejected with 400 while
> `[]string{"kb","ticket"}` works. Pass two or more, or leave `Kinds` empty
> and filter the `kind` field yourself. (The bracketed `kinds[]=` form that
> some clients send is dropped silently by the API — 200 OK, every kind
> searched — so this SDK does not send it.)

### `client.Events`

| Method             | Route                 |
| ------------------ | --------------------- |
| `Track`            | `POST /v1/cdp/events` |
| `List` / `ListAll` | `GET /v1/cdp/events`  |

```go
_, err := client.Events.Track(ctx, chatly.TrackEventInput{
	ContactID:  contactID, // a Chatly id, not your external id
	Name:       "order_placed",
	Properties: map[string]any{"total_cents": 4999, "currency": "EUR"},
})
```

Events drive workflow triggers and segments, so keep the `Name` vocabulary
stable: renaming an event orphans every rule built on the old name.

### `client.Webhooks`

| Method                         | Route                                         |
| ------------------------------ | --------------------------------------------- |
| `Deliveries` / `DeliveriesAll` | `GET /v1/webhooks/deliveries`                 |
| `Delivery`                     | `GET /v1/webhooks/deliveries/{id}`            |
| `Redeliver`                    | `POST /v1/webhooks/deliveries/{id}/redeliver` |
| `List`                         | `GET /v1/webhooks`                            |
| `Create`                       | `POST /v1/webhooks`                           |
| `Delete`                       | `DELETE /v1/webhooks/{id}`                    |
| `Events`                       | `GET /v1/webhooks/events`                     |
| `SchemaVersions`               | `GET /v1/webhooks/schema-versions`            |
| `SetSchemaVersion`             | `PATCH /v1/webhooks/{id}/schema-version`      |

`Redeliver` answers `{queued: true}` — accepted for sending, not delivered.
Poll `Deliveries` for the outcome.

`Create` is the **only** response anywhere that carries the signing `secret`:
there is no reveal route and no rotation route, so store it on the spot.
`Events` is the authoritative subscribable catalogue and the exact enum
`Create` validates against. `SetSchemaVersion`'s body field is
`schemaVersion`, while `SchemaVersions` calls the same thing
`versions[].version` — the two names are not interchangeable.

`Delete` answers `{ok: true}` with a 200, not a 204. It was closed to API keys
until 2026-10-14 while `Create` was open — so a pipeline registering an
endpoint per deploy leaked one on every run with nothing able to clean up, and
the workaround that used to be suggested (repointing it at a dead URL) leaves
the subscription alive and the delivery log filling with failures. Deleting
destroys the only copy of the signing secret: recreating mints a new one.

### `client.Reports`

| Method               | Route                                       |
| -------------------- | ------------------------------------------- |
| `Overview`           | `GET /v1/reports/overview`                  |
| `Agents`             | `GET /v1/reports/agents`                    |
| `ConversationsByDay` | `GET /v1/reports/conversations-by-day`      |
| `SLA`                | `GET /v1/reports/sla`                       |
| `Sources`            | `GET /v1/reports/sources`                   |
| `Attribution`        | `GET /v1/reports/attribution`               |
| `Cohorts`            | `GET /v1/reports/cohorts`                   |
| `ResponseTimes`      | `GET /v1/reports/response-times`            |
| `Voice`              | `GET /v1/reports/voice`                     |
| `Teams`              | `GET /v1/reports/teams`                     |
| `Labels`             | `GET /v1/reports/labels`                    |
| `Themes`             | `GET /v1/reports/themes`                    |
| `ThemeConversations` | `GET /v1/reports/themes/{id}/conversations` |

The first three take only `Days` (1–90, clamped server-side). `SLA` takes a
richer window (`Days`, or `From`+`To` together, plus `GroupBy`). In the SLA
report, read `coverage.coveragePct` before quoting attainment, and
`breachesTruncated` before counting the `breaches` array.

**The window is not spelled the same way twice, and nothing validates it.**
These routes read raw query strings and clamp, so a misspelled parameter is
silently the default window and the report still looks plausible. Hence a
separate options type per shape: `Days` 1–90 for `Sources` (default **7**,
not 30), `Attribution` and `ResponseTimes`; 1–365 for `Voice`; **`Weeks`**
2–26 for `Cohorts`, which has no `Days` at all; `ReportRange` with
`From`+`To`, `GroupBy` and `BusinessHours` for `Teams` and `Labels` — those
two go on the wire as `group_by` and `business_hours`; and a single batch
**`Date`** for `Themes`.

`Attribution` and `Sources` are bare arrays. In `Attribution`, discriminate
on `label` rather than `value`: both the untagged bucket and the folded tail
row carry `value: null`. `Themes` answers `batchDate: null` with an empty list
until the daily worker job has run, which is a normal 200.

### `client.Channels`

| Method         | Route                                  |
| -------------- | -------------------------------------- |
| `List`         | `GET /v1/channels`                     |
| `Get`          | `GET /v1/channels/{id}`                |
| `Adapters`     | `GET /v1/channels/adapters`            |
| `Create`       | `POST /v1/channels`                    |
| `Update`       | `PATCH /v1/channels/{id}`              |
| `RotateSecret` | `POST /v1/channels/{id}/secret/rotate` |

This is where the widget's `publicId` comes from. Credential values inside
`config` read `__redacted__`. `originsRestricted: false` means the widget will
load on any site.

`Adapters` is the one method here returning `[]string` — the route answers a
bare array of channel-type names rather than objects.

Echoing `__redacted__` back in an `Update` means "leave this credential
alone", so a read-modify-write is safe; a helper that strips the marker
destroys the credential. A patch with no recognised field is a **409**, not a
no-op. `PublicID` is unique across every workspace and a collision surfaces as
a 500, so namespace it. `RotateSecret` works on `api` channels only (anything
else is a 409) and cuts over with no grace window.

### `client.Billing`

| Method         | Route                           |
| -------------- | ------------------------------- |
| `Plans`        | `GET /v1/billing/plans`         |
| `Usage`        | `GET /v1/billing/usage`         |
| `Summary`      | `GET /v1/billing`               |
| `Invoices`     | `GET /v1/billing/invoices`      |
| `UsageDaily`   | `GET /v1/billing/usage/daily`   |
| `UsageMonthly` | `GET /v1/billing/usage/monthly` |

Both need the `workspace:billing:read` scope. `Usage` requires one event type
(`chatly.UsageEventConversationOpened`, `…MessageSentAgent`,
`…MessageSentBot`, `…AITokens`) — there is no "everything" form. `Plans`
returns an empty array on an install with no Stripe configured, which is not
an error.

`UsageDaily` needs the same event type; `UsageMonthly` **does not read it at
all** and always rolls up all four meters, so passing one is silently ignored
rather than refused. In `Summary`, `seatsLimit: -1` means uncapped — which is
every plan today — and `stripeConnected: false` is a success.

`Invoices` is genuinely cursor-paginated and still has no `InvoicesAll`: its
envelope is `{data, nextCursor}`, the one route on the surface that does not
spell it `items`, so the generic walker cannot be pointed at it. Follow
`nextCursor` yourself.

### `client.Workspace`

| Method        | Route                        |
| ------------- | ---------------------------- |
| `Settings`    | `GET /v1/workspace/settings` |
| `Labels`      | `GET /v1/labels`             |
| `Inboxes`     | `GET /v1/inboxes`            |
| `TeamMembers` | `GET /v1/teams/{id}/members` |
| `AgentSkills` | `GET /v1/agents/skills`      |
| `KeyScopes`   | `GET /v1/api-keys/scopes`    |
| `Secrets`     | `GET /v1/workspace/secrets`  |
| `Mine`        | `GET /v1/workspaces/me`      |

`Labels(ctx, &chatly.LabelOptions{WithUsage: true})` changes the response
shape rather than adding an optional field, and includes unregistered labels
whose `id` is `""`.

`TeamMembers` is a team roster: `role` is the member's role in _that team_,
not their workspace role, and the list is not joined against workspace
memberships, so a member whose seat has been revoked still appears. The
parent collection `GET /v1/teams` is not key-accessible, so team ids come
from the objects that reference one — a conversation's `teamId`, a ticket
queue's, an inbox's routing settings.

`AgentSkills` returns `{"skills": [...]}` — an object, not an array. An empty
list is the normal state for a workspace that has not configured any.

`Mine` is a bare array that, for a key, holds **exactly one entry**: the
workspace the key was minted in. It needs no permission and answers on a
suspended workspace, which makes it the cheap way to confirm which tenant a
credential actually points at before a script writes anything. Its `role` is
the synthetic `restricted` the auth layer mints for every key, not a stored
membership role — what a key may _do_ is its scope set, which `Me` answers.

`KeyScopes` is the second of exactly two routes on the api-keys controller a
key may call (`Whoami` is the other) and is deliberately narrower than the
full permission vocabulary: it lists only the scopes with a key-accessible
route behind them.

`Secrets` needs **both** `workspace:write` and `integrations:read` — the guard
ANDs them. It is a census across nine tables that carries no value, no prefix
and no hash. Read `truncated` before concluding a credential does not exist,
and read `rotatedAtBasis` / `lastUsedBasis` before acting on a null timestamp:
`"none"` means nothing in the product records that fact, which is not the same
as "never used".

### `client.AI`

| Method         | Route                                   |
| -------------- | --------------------------------------- |
| `Providers`    | `GET /v1/ai-providers`                  |
| `AddProvider`  | `POST /v1/ai-providers`                 |
| `TestProvider` | `POST /v1/ai-providers/{provider}/test` |
| `Quota`        | `GET /v1/ai/quota`                      |
| `Tone`         | `GET /v1/ai/tone`                       |
| `TonePreview`  | `GET /v1/ai/tone/preview`               |
| `SetTone`      | `PUT /v1/ai/tone`                       |
| `GenerateTone` | `POST /v1/ai/tone/generate`             |
| `Tools`        | `GET /v1/ai/tools`                      |
| `CreateTool`   | `POST /v1/ai/tools`                     |
| `UpdateTool`   | `PATCH /v1/ai/tools/{id}`               |
| `DeleteTool`   | `DELETE /v1/ai/tools/{id}`              |

`AddProvider` makes a live call to the provider before storing the key: only
an authentication failure refuses the save, while a provider outage stores it
with `validation.status: "unverified"`, which is a success. Read the body
rather than the status.

`SetTone` is a **full replace** — an empty slice deletes every rule, and there
is no delete route — and it answers with the same bare array `Tone` does.
`GenerateTone` saves nothing: the rules come back as a proposal and only take
effect when you pass them to `SetTone`.

`Quota`'s `spent` is **platform-key spend only**, the one figure `budget`
bounds; `totalSpent` is the user-facing total. A null `budget` means uncapped.

`DeleteTool` never answers 404: the update carries no existence check, so a
wrong id answers `{deleted: true}` having changed nothing.

### `client.Bots`

| Method                         | Route                                                  |
| ------------------------------ | ------------------------------------------------------ |
| `List` / `ListAll`             | `GET /v1/bots`                                         |
| `Get`                          | `GET /v1/bots/{id}`                                    |
| `Create`                       | `POST /v1/bots`                                        |
| `Update`                       | `PATCH /v1/bots/{id}`                                  |
| `UpdateExternal`               | `PATCH /v1/bots/{id}/external`                         |
| `RotateExternalSecret`         | `POST /v1/bots/{id}/external/rotate-secret`            |
| `Analytics`                    | `GET /v1/bots/{id}/analytics`                          |
| `Deliveries` / `DeliveriesAll` | `GET /v1/bots/{id}/deliveries`                         |
| `Delivery`                     | `GET /v1/bots/{id}/deliveries/{deliveryId}`            |
| `Redeliver`                    | `POST /v1/bots/{id}/deliveries/{deliveryId}/redeliver` |
| `TestDelivery`                 | `POST /v1/bots/{id}/test-delivery`                     |

`Create`'s body is a union discriminated on `Type`, and the external arm is
tried first — so a field from the wrong arm is silently dropped rather than
refused. An external agent's response carries `signingSecret` and `token`,
each shown **exactly once** and recoverable from nowhere.

`Update` cannot touch the external configuration and `UpdateExternal` cannot
touch anything else; because both schemas strip unknown keys, crossing them is
a 200 that changed nothing. `RotateExternalSecret` has no overlap window.

`TestDelivery` is synchronous and writes nothing — it signs a rehearsal
payload, posts it to your real URL and returns what came back, so a non-2xx
from you is a 201 from us with `ok: false`. `Redeliver` replays the recorded
bytes with the same `eventId`, to the agent's **current** URL.

In `Analytics`, null is never zero: a null count means the kind of bot cannot
produce that number, and a null rate means its sample size was 0.

### `client.BotRoutes`

| Method    | Route                         |
| --------- | ----------------------------- |
| `List`    | `GET /v1/bot-routes`          |
| `Fields`  | `GET /v1/bot-routes/fields`   |
| `Create`  | `POST /v1/bot-routes`         |
| `Update`  | `PATCH /v1/bot-routes/{id}`   |
| `Resolve` | `POST /v1/bot-routes/resolve` |
| `Delete`  | `DELETE /v1/bot-routes/{id}`  |

`List` is in evaluation order and the first match wins. `Fields` is the closed
condition vocabulary — read it rather than guessing, because an unknown field
name is a 400 at write time.

`Resolve` dry-runs the table against a hypothetical visitor and writes
nothing. Disabled routes are **omitted entirely** rather than reported with a
verdict, several routes may report `matched` while only the first is
`selected`, and a failed condition reports the value the field actually
resolved to.

### `client.Brands`

| Method   | Route                    |
| -------- | ------------------------ |
| `List`   | `GET /v1/brands`         |
| `Create` | `POST /v1/brands`        |
| `Update` | `PATCH /v1/brands/{id}`  |
| `Delete` | `DELETE /v1/brands/{id}` |

A list row carries a `usage` object the single-brand responses do not.
`Domain` is the help-centre hostname and `ReplyDomain` the outbound-mail one —
two different things. `IsDefault: true` demotes every other brand. `Delete` is
a soft delete answering `{id}`, and is a 409 if the brand is the default or
any channel still points at it.

### `client.CanvasSteps`

| Method    | Route                                |
| --------- | ------------------------------------ |
| `Get`     | `GET /v1/canvas-steps/{id}`          |
| `Resolve` | `POST /v1/canvas-steps/{id}/resolve` |

The back half of an async canvas step, for a backend process holding a `ck_`
key and no visitor session. `Resolve` is single-use by construction — the
claiming update filters on `status = pending` — so a retry after a timeout is
safe and a retry of a successful call is told the step is already resolved.

### `client.CustomerCards` and `client.CustomerCardEndpoints`

| Method                                 | Route                                                   |
| -------------------------------------- | ------------------------------------------------------- |
| `CustomerCards.List`                   | `GET /v1/customer-cards`                                |
| `CustomerCards.Create`                 | `POST /v1/customer-cards`                               |
| `CustomerCards.Update`                 | `PATCH /v1/customer-cards/{id}`                         |
| `CustomerCards.RotateSecret`           | `POST /v1/customer-cards/{id}/rotate-secret`            |
| `CustomerCards.Delete`                 | `DELETE /v1/customer-cards/{id}`                        |
| `CustomerCardEndpoints.List`           | `GET /v1/customer-card-endpoints`                       |
| `CustomerCardEndpoints.Create`         | `POST /v1/customer-card-endpoints`                      |
| `CustomerCardEndpoints.RotateSecret`   | `POST /v1/customer-card-endpoints/{id}/rotate-secret`   |
| `CustomerCardEndpoints.ActivateSecret` | `POST /v1/customer-card-endpoints/{id}/activate-secret` |
| `CustomerCardEndpoints.Delete`         | `DELETE /v1/customer-card-endpoints/{id}`               |

Card secrets (`cc_`) rotate in one step and cut over immediately. **Endpoint
secrets (`cce_`) rotate in two**: `RotateSecret` mints a secret that signs
nothing — the response says `active: false` — and `ActivateSecret` promotes
it. Mint, deploy, activate. Activating twice is a 409 and must not be retried.

Creating an endpoint secret changes your verifier's job immediately: that URL
is signed with it from the moment `Create` returns, and `x-chatly-card-id`
stops being sent. Deleting one reverses that.

### `client.DocsSources`

| Method      | Route                                |
| ----------- | ------------------------------------ |
| `Providers` | `GET /v1/docs-sources/providers`     |
| `List`      | `GET /v1/docs-sources`               |
| `Discover`  | `POST /v1/docs-sources/discover`     |
| `Create`    | `POST /v1/docs-sources`              |
| `Update`    | `PATCH /v1/docs-sources/{id}`        |
| `Sync`      | `POST /v1/docs-sources/{id}/sync`    |
| `Webhook`   | `POST /v1/docs-sources/{id}/webhook` |
| `Delete`    | `DELETE /v1/docs-sources/{id}`       |

`Token` is required on `Discover` and `Create` and **may be the empty
string** — a public Discourse category needs no key and the sitemap crawler
reads only what a visitor can — so the field is always sent.

Read `syncStatus`, not `articleCount`: a fresh source reports 0 articles
because it has not run yet, which is what makes scripts poll forever.
`Webhook` returning `subscribed: false` with a reason is a **success** and the
common case (a read-only token cannot manage webhooks). `Sync` never checks
that the source exists, so a 201 is not proof it was found. `Delete` answers
`{kept}` — how many articles were handed back as manual ones — and deletes
nothing.

### `client.EmailTemplates`

| Method    | Route                               |
| --------- | ----------------------------------- |
| `List`    | `GET /v1/email-templates`           |
| `Preview` | `POST /v1/email-templates/preview`  |
| `Update`  | `PUT /v1/email-templates/{kind}`    |
| `Delete`  | `DELETE /v1/email-templates/{kind}` |

The resource is the **kind**, not a row id, and `List` always returns all five
with `override: null` where the workspace is on the built-in. `Delete`
restores the built-in and answers `{restored}`; `{restored: false}` with a 200
means it was already on the built-in, which is a success.

Note the mirror image in the two bodies: `kind` belongs in `Preview`'s body
and is a 400 in `Update`'s, where it lives in the path.

### `client.KnowledgeGaps`

| Method   | Route                                |
| -------- | ------------------------------------ |
| `List`   | `GET /v1/knowledge-gaps`             |
| `Update` | `PATCH /v1/knowledge-gaps/{id}`      |
| `Draft`  | `POST /v1/knowledge-gaps/{id}/draft` |

`List` is a queue, not a log: it sorts by occurrences, and an unrecognised
`Status` is silently ignored rather than refused. `Draft` can answer 201 with
`articleId: null` **and** a `reason` — "there is nothing reusable to write" is
an answer, not a failure — and it makes a live LLM call, so give it a generous
timeout.

### `client.Triggers`

| Method             | Route                      |
| ------------------ | -------------------------- |
| `List` / `ListAll` | `GET /v1/triggers`         |
| `Create`           | `POST /v1/triggers`        |
| `Delete`           | `DELETE /v1/triggers/{id}` |

`Create` is an **upsert**: with `ID` it updates, without one it inserts, and
both answer 201 — do not read the status as "created". The update arm is a
full replace, so send the whole object; an omitted `DebounceSeconds` is
written as null. `EventType` is unvalidated, so a typo is accepted, stored and
never fires.

## What is deliberately missing

Nothing key-accessible is missing: all 162 such routes have a typed method as
of 2026-10-14. Reach anything added later with `Request`. Beyond that, most of
the API — reactions, blocking a contact, anything that records _who_ did
something — requires a signed-in person and answers an API key with 403. Those have no method here on purpose: a method
that always fails is worse than no method, because the failure arrives at
runtime in someone else's integration.

## Testing against this SDK

Point it at an `httptest.Server` with `chatly.New("ck_test",
chatly.WithBaseURL(srv.URL))`. This package's own suite does exactly that —
no network, no API key, no running Chatly — via `go test ./... -v`.

## Licence

MIT. See [LICENSE](./LICENSE).

---

## About this repository

This is a **mirror**. `chatly-go` is built from a private monorepo where the rest
of the platform, its migrations and its contract checks live, so a pull request here
cannot be merged directly — but a patch is still the fastest way to get something
fixed and we will port it with attribution.

What you can do here: read the source, run the tests, open an issue, or open a PR
and we will treat the diff as the proposal.

Issues: https://github.com/chatlychat/chatly-go/issues · Docs: https://chatlychat.com/docs
