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

The Chatly API has 141 key-accessible routes; this package hand-writes methods
for 55 of them. For the rest, `Request` goes through the same auth,
idempotency, retry and error handling:

```go
// Key-accessible, and no typed method here:
raw, err := client.Request(ctx, "GET", "/v1/tickets/"+ticketID+"/status-history", nil, nil)
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

55 methods. Verb and path are the API route each one calls.

### Credential

| Method          | Route                     |
| --------------- | ------------------------- |
| `client.Whoami` | `GET /v1/api-keys/whoami` |

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

`AddTags` adds **one** tag per call — the route's body is `{"tag": "..."}`.
`Merge` keeps the path id and absorbs the other; it is not reversible.
`BulkUpsert` reports partial failure in its response body with HTTP 200, so
read `skipped` and `errors` rather than trusting the status.

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

```go
page, err := client.Tickets.List(ctx, &chatly.ListOptions{
	Limit:  50,
	Sort:   "-updatedAt",
	Filter: []string{"status:in:open,pending", "priority:urgent"},
})
```

`Comments` is capped server-side at 500 with no cursor. `Bulk` returns
`{updated, skipped}` with HTTP 201 even when everything was skipped.

### `client.Macros`

| Method   | Route                    |
| -------- | ------------------------ |
| `List`   | `GET /v1/macros`         |
| `Create` | `POST /v1/macros`        |
| `Update` | `PATCH /v1/macros/{id}`  |
| `Delete` | `DELETE /v1/macros/{id}` |

`List` returns a **bare array**, not a page: the route sends no cursor and the
service caps the result at 200 rows ordered by usage rank. On a workspace with
more macros than that you get the top 200, so narrow with `Scope`, `Team`,
`Query` or `Language` rather than expecting the whole library.

`Update` replaces any field it mentions — `Tags` sets the whole list, it does
not append.

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

`Redeliver` answers `{queued: true}` — accepted for sending, not delivered.
Poll `Deliveries` for the outcome.

### `client.Reports`

| Method               | Route                                  |
| -------------------- | -------------------------------------- |
| `Overview`           | `GET /v1/reports/overview`             |
| `Agents`             | `GET /v1/reports/agents`               |
| `ConversationsByDay` | `GET /v1/reports/conversations-by-day` |
| `SLA`                | `GET /v1/reports/sla`                  |

The first three take only `Days` (1–90, clamped server-side). `SLA` takes a
richer window (`Days`, or `From`+`To` together, plus `GroupBy`). In the SLA
report, read `coverage.coveragePct` before quoting attainment, and
`breachesTruncated` before counting the `breaches` array.

### `client.Channels`

| Method | Route                   |
| ------ | ----------------------- |
| `List` | `GET /v1/channels`      |
| `Get`  | `GET /v1/channels/{id}` |

This is where the widget's `publicId` comes from. Credential values inside
`config` read `__redacted__`. `originsRestricted: false` means the widget will
load on any site.

### `client.Billing`

| Method  | Route                   |
| ------- | ----------------------- |
| `Plans` | `GET /v1/billing/plans` |
| `Usage` | `GET /v1/billing/usage` |

Both need the `workspace:billing:read` scope. `Usage` requires one event type
(`chatly.UsageEventConversationOpened`, `…MessageSentAgent`,
`…MessageSentBot`, `…AITokens`) — there is no "everything" form. `Plans`
returns an empty array on an install with no Stripe configured, which is not
an error.

### `client.Workspace`

| Method        | Route                        |
| ------------- | ---------------------------- |
| `Settings`    | `GET /v1/workspace/settings` |
| `Labels`      | `GET /v1/labels`             |
| `Inboxes`     | `GET /v1/inboxes`            |
| `TeamMembers` | `GET /v1/teams/{id}/members` |
| `AgentSkills` | `GET /v1/agents/skills`      |

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

## What is deliberately missing

86 key-accessible routes have no typed method; reach them with `Request`. Beyond that, most of the API — reactions, channel configuration,
anything that records _who_ did something — requires a signed-in person and
answers an API key with 403. Those have no method here on purpose: a method
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
