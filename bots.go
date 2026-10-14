package chatly

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

// BotsService is the /v1/bots surface — AI personas, BYO external agents, and
// the delivery ledger for the latter. Reach it as client.Bots.
//
// Two scopes split this namespace along an unobvious line: the reads want
// triggers:read, the writes ai:configure, and Analytics wants reports:read
// because it returns counts and percentiles with no transcript in them.
type BotsService struct{ c *Client }

// The two kinds of bot. Create's body is a union discriminated on this, and
// the EXTERNAL arm is tried first — so a stray external field on a Chatly bot
// changes which arm parses.
const (
	// BotTypeChatly is a Chatly-hosted persona: a prompt, a model, tools and
	// a KB scope, run by us.
	BotTypeChatly = "chatly"
	// BotTypeExternal is your own agent behind an https webhook. Chatly posts
	// signed deliveries to it and never runs a model for it.
	BotTypeExternal = "external"
)

// The delivery states BotDeliveryOptions.Status accepts. A word outside these
// four is a 400 from a zod enum, not an empty page — which is the reason the
// route has a dedicated parameter instead of leaving it to `filter`.
const (
	BotDeliveryPending   = "pending"
	BotDeliveryDelivered = "delivered"
	BotDeliveryFailed    = "failed"
	// BotDeliveryDead will NOT be retried; BotDeliveryFailed still will.
	BotDeliveryDead = "dead"
)

// List returns one page of bots.
//
// GET /v1/bots — scope triggers:read. Speaks the generic list grammar:
// sortable on id, name, createdAt, updatedAt, status; filterable on status
// (active|paused|draft), model, templateOrigin, createdAt, updatedAt. An
// unknown sort key, field or filter field is a 400 here, and a limit outside
// 1..100 is a 400 rather than a clamp.
//
// Default order is createdAt descending, deliberately not id: this table's
// ids are a mix of UUID v4 and v7, so id order is not time order.
//
// Two things about an item. It is a PARTIAL row — the response schema marks
// every field optional because `fields` projects — and `type` and
// `externalAgent` are not projectable at all, so any `fields` you set drops
// the external-agent block entirely. Use Get for the whole object.
func (s *BotsService) List(ctx context.Context, opts *ListOptions) (*Page[Object], error) {
	return s.c.getPage(ctx, "/v1/bots", opts.values())
}

// ListAll walks every page of bots.
func (s *BotsService) ListAll(opts *ListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/bots", opts.values())
}

// Get returns one bot in full.
//
// GET /v1/bots/{id} — scope triggers:read. `externalAgent` is null on a
// Chatly persona and, on an external one, carries
// `signingSecretSet: boolean` — never the secret itself. An external agent's
// `systemPrompt` holds a placeholder sentence and its `model` is the literal
// string "external"; neither is read by anything.
//
// The id is parsed as a UUID inside the handler, so a malformed one is a 400
// rather than a 404.
func (s *BotsService) Get(ctx context.Context, botID string) (Object, error) {
	if botID == "" {
		return nil, errors.New("chatly: Bots.Get: empty bot id")
	}
	return s.c.getObject(ctx, "/v1/bots/"+pathEscape(botID), nil)
}

// BotInput is the body of Create and the two update methods.
//
// It carries BOTH arms of the create union, and they must not be mixed: the
// API tries the EXTERNAL arm first and neither arm is strict, so a field from
// the wrong arm is silently dropped rather than refused. Set Type to
// BotTypeExternal and the external fields, or leave Type empty (or
// BotTypeChatly) and set SystemPrompt.
type BotInput struct {
	// Type is "external" or "chatly". Omitted means chatly. Not patchable.
	Type string `json:"type,omitempty"`

	// Name, 1..160 chars, required on create.
	Name string `json:"name,omitempty"`

	// Slug, 1..80 chars of [a-z0-9-], is CREATE-ONLY: there is no way to
	// change it later. Omitted, one is allocated from Name with a collision
	// suffix.
	Slug string `json:"slug,omitempty"`

	Description string `json:"description,omitempty"`

	// Status is active, paused or draft. An EXTERNAL agent created without
	// this defaults to active, not draft, because the router refuses a
	// non-active bot and a draft external agent would answer nothing.
	Status string `json:"status,omitempty"`

	// PersonaMeta is the presentation bag: tone, language, tagline,
	// avatarUrl. Strict server-side — an unknown key is a 400.
	PersonaMeta map[string]any `json:"personaMeta,omitempty"`

	// --- external agent (Type == BotTypeExternal) ------------------------

	// WebhookURL is required on an external create. It must be https and is
	// put through an SSRF guard, so a private address is refused at save
	// time rather than at delivery time.
	WebhookURL string `json:"webhookUrl,omitempty"`

	// TimeoutSeconds is 1..60, defaulting to 20 on create. On
	// UpdateExternal, omitting it leaves the stored value alone.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// FallbackMessage, 1..500 chars, is what the visitor is told when your
	// endpoint never answers.
	FallbackMessage string `json:"fallbackMessage,omitempty"`

	// CanStartConversations lets the agent open a thread rather than only
	// answer one. Pointer because false is both meaningful and the default,
	// and because sending a default on a PATCH would turn "leave alone" into
	// "revoke".
	CanStartConversations *bool `json:"canStartConversations,omitempty"`

	// --- Chatly persona (Type empty or BotTypeChatly) --------------------

	// SystemPrompt is REQUIRED on a Chatly create and must be at least 20
	// characters.
	SystemPrompt string `json:"systemPrompt,omitempty"`

	Model string `json:"model,omitempty"`

	// EnabledTools names tools from the AI tool registry, max 50.
	EnabledTools []string `json:"enabledTools,omitempty"`

	// KBScope is `{ collectionIds: [...] }`, max 20 collections. Strict.
	KBScope map[string]any `json:"kbScope,omitempty"`

	// MaxToolCalls is 1..32.
	MaxToolCalls int `json:"maxToolCalls,omitempty"`

	// Guardrails is `{ maxOutputChars, bannedKeywords, piiPolicy }`, strict,
	// where piiPolicy is allow, redact or deny.
	//
	// REPLACED WHOLESALE on update, not merged: sending just piiPolicy
	// erases maxOutputChars.
	Guardrails map[string]any `json:"guardrails,omitempty"`

	// EscalationRules, max 20, each
	// `{ id, triggerType, trigger, action, actionArgs }` where triggerType is
	// sentiment_negative, keyword, tool_failure or after_turns and action is
	// human.handoff or apply_tag. An apply_tag rule MUST carry
	// actionArgs.tag or the write is a 400.
	//
	// Also replaced wholesale on update.
	EscalationRules []map[string]any `json:"escalationRules,omitempty"`
}

// Create adds a bot and returns it.
//
// POST /v1/bots — scope ai:configure, answers 201.
//
// The response shape depends on the arm. A Chatly persona answers with the
// bot. An EXTERNAL agent answers with the bot PLUS `signingSecret`, `token`,
// `credentialId` and `note` — two credentials, each shown exactly once and
// recoverable from nowhere: the secret is stored encrypted and the `cb_`
// token only as a hash. Store both before you do anything else.
//
// Because a required Idempotency-Key replays the first response, a pinned key
// here returns the ORIGINAL credentials rather than minting a second bot.
func (s *BotsService) Create(ctx context.Context, in BotInput) (Object, error) {
	if in.Name == "" {
		return nil, errors.New("chatly: Bots.Create: name is required")
	}
	if in.Type == BotTypeExternal && in.WebhookURL == "" {
		return nil, errors.New("chatly: Bots.Create: webhookUrl is required for an external agent")
	}
	if in.Type != BotTypeExternal && in.SystemPrompt == "" {
		return nil, errors.New("chatly: Bots.Create: systemPrompt is required for a Chatly bot (min 20 chars)")
	}
	return s.c.writeObject(ctx, "POST", "/v1/bots", nil, in)
}

// Update patches a bot's own fields and returns the whole row.
//
// PATCH /v1/bots/{id} — scope ai:configure.
//
// Notably absent from what it can change: Slug, Type, and every
// external-agent field. Those live on UpdateExternal, and because the body
// schema strips unknown keys rather than rejecting them, sending WebhookURL
// here is a 200 that changed nothing. An EMPTY body is a 400 ("No fields to
// update"), not a no-op.
//
// There is no type guard either: this route will happily write a
// SystemPrompt onto an external agent, where nothing reads it. Every jsonb
// field it does write is replaced wholesale, not merged.
func (s *BotsService) Update(ctx context.Context, botID string, in BotInput) (Object, error) {
	if botID == "" {
		return nil, errors.New("chatly: Bots.Update: empty bot id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/bots/"+pathEscape(botID), nil, in)
}

// UpdateExternal patches an external agent's delivery configuration.
//
// PATCH /v1/bots/{id}/external — scope ai:configure. It reads four fields and
// four only: WebhookURL, TimeoutSeconds, FallbackMessage,
// CanStartConversations. An empty body is a 400.
//
// Aimed at a Chatly persona it is a 400 ("no webhook to change"), not a 404 —
// the bot exists, it just has no endpoint.
//
// The stored config bag is read-modify-written rather than merged at the
// database, which is what keeps the signing secret intact across a partial
// patch.
func (s *BotsService) UpdateExternal(ctx context.Context, botID string, in BotInput) (Object, error) {
	if botID == "" {
		return nil, errors.New("chatly: Bots.UpdateExternal: empty bot id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/bots/"+pathEscape(botID)+"/external", nil, in)
}

// RotateExternalSecret mints a new signing secret for an external agent.
//
// POST /v1/bots/{id}/external/rotate-secret — scope ai:configure, answers 201
// with `{ signingSecret }`. That field name, not `secret` and not `token`. No
// request body.
//
// THERE IS NO OVERLAP WINDOW. The old secret stops verifying the instant this
// returns, so deploy the new one to your endpoint first, or deliveries fail
// in between. And since a replayed Idempotency-Key returns the cached
// response, a pinned key hands back the previous secret instead of rotating —
// use a fresh key per rotation.
func (s *BotsService) RotateExternalSecret(ctx context.Context, botID string) (Object, error) {
	if botID == "" {
		return nil, errors.New("chatly: Bots.RotateExternalSecret: empty bot id")
	}
	path := "/v1/bots/" + pathEscape(botID) + "/external/rotate-secret"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}

// BotAnalyticsOptions is the ONE parameter the analytics route accepts.
type BotAnalyticsOptions struct {
	// Days is 1..365, default 30, and REJECTED out of range rather than
	// clamped. `from`, `to`, `weeks` and `date` are refused BY NAME with a
	// 400 on this route — a deliberate choice, because this product has
	// shipped a report that stripped from/to and confidently answered for
	// seven days instead.
	Days int
}

func (o *BotAnalyticsOptions) values() url.Values {
	q := url.Values{}
	if o != nil && o.Days > 0 {
		q.Set("days", strconv.Itoa(o.Days))
	}
	return q
}

// Analytics returns one bot's numbers, for either kind of bot.
//
// GET /v1/bots/{id}/analytics — scope reports:read, not the triggers:read the
// ledger needs: there is no transcript, payload, conversation id or contact
// in the response.
//
// `{ botId, botName, botType, ledger, unit, windowDays, since, until, volume,
// latency, reliability, outcomes }`. Three things to read before plotting it:
//
//   - NULL IS NEVER ZERO here. volume.retrying, reliability.fallbackCount and
//     reliability.retriedCount are null for a Chatly persona (it has no retry
//     ladder and no fallback — it escalates), volume.cancelled is null for an
//     external agent, and every rate is null when its sampleSize is 0.
//     `botType` is the discriminator for all of it.
//   - Latency is percentiles, never a mean: p50DurationMs, p95DurationMs,
//     maxDurationMs, with the sampleSize they came from. latency.basis says
//     what was timed, and the two values are not comparable —
//     "last_attempt" is one HTTP round trip, "run_wall_clock" is a whole
//     tool loop.
//   - outcomes.conversations counts threads this bot TOUCHED, so two bots
//     that shared a thread both count it and per-bot rows do not add up to a
//     workspace total.
//
// Read windowDays back rather than trusting your own Days: the service clamps
// independently of the schema.
func (s *BotsService) Analytics(ctx context.Context, botID string, opts *BotAnalyticsOptions) (Object, error) {
	if botID == "" {
		return nil, errors.New("chatly: Bots.Analytics: empty bot id")
	}
	return s.c.getObject(ctx, "/v1/bots/"+pathEscape(botID)+"/analytics", opts.values())
}

// BotDeliveryOptions are the query parameters the delivery ledger reads.
type BotDeliveryOptions struct {
	// Status is one of the BotDelivery* constants. A word outside them is a
	// 400, not an empty page.
	Status string
	Cursor string
	// Limit is 1..100 (default 25) and a value outside that is a 400, not a
	// clamp.
	Limit int
	// Sort accepts "createdAt" or "-createdAt" and NOTHING else — anything
	// else is a 400. Not id: this table's ids are v4, so ordering by them
	// orders by nothing.
	Sort string
}

func (o *BotDeliveryOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	setIf(q, "status", o.Status)
	setIf(q, "cursor", o.Cursor)
	setIf(q, "sort", o.Sort)
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	return q
}

// Deliveries returns one page of an external agent's delivery ledger, newest
// first.
//
// GET /v1/bots/{id}/deliveries — scope triggers:read. A 404 here means the
// bot does not exist OR is not an external agent: a Chatly persona has no
// ledger, because nothing is pushed anywhere.
//
// A list row carries NO payload — a page of fifty would be megabytes of
// transcript — so call Delivery for one. `attempt` is attempts MADE, so 0
// means untried; `httpStatus` null means the request never completed (a
// timeout or a DNS failure); `error` is the first 300 characters, truncated
// when the row was written.
//
// `fields` and `filter` are accepted by the shared parser here and then
// IGNORED by the service, which looks exactly like a filter that matched
// everything — hence Status as a real parameter.
func (s *BotsService) Deliveries(ctx context.Context, botID string, opts *BotDeliveryOptions) (*Page[Object], error) {
	if botID == "" {
		return nil, errors.New("chatly: Bots.Deliveries: empty bot id")
	}
	return s.c.getPage(ctx, "/v1/bots/"+pathEscape(botID)+"/deliveries", opts.values())
}

// DeliveriesAll walks every page of an external agent's delivery ledger.
func (s *BotsService) DeliveriesAll(botID string, opts *BotDeliveryOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/bots/"+pathEscape(botID)+"/deliveries", opts.values())
}

// Delivery returns one delivery, including the payload that was signed.
//
// GET /v1/bots/{id}/deliveries/{deliveryId} — scope triggers:read. This is
// the call for "what exactly did you send me?": `payload` is the object whose
// serialisation the signature was computed over, and the delivery's own id is
// the `eventId` inside it.
//
// Scoped to the bot in the path as well as the workspace, so bot A's URL with
// bot B's delivery id is a 404 rather than a 200. Decode `payload` loosely: a
// row written before a key existed carries the shape of that day.
func (s *BotsService) Delivery(ctx context.Context, botID, deliveryID string) (Object, error) {
	if botID == "" || deliveryID == "" {
		return nil, errors.New("chatly: Bots.Delivery: botID and deliveryID are required")
	}
	path := "/v1/bots/" + pathEscape(botID) + "/deliveries/" + pathEscape(deliveryID)
	return s.c.getObject(ctx, path, nil)
}

// Redeliver queues one recorded delivery again, from attempt 0.
//
// POST /v1/bots/{id}/deliveries/{deliveryId}/redeliver — scope ai:configure,
// answers 201 with `{ queued: true, deliveryId, url }`. No request body.
//
// `url` is the agent's CURRENT webhook, not the one the original attempt
// used: if you moved your endpoint, the replay goes to the new address. And
// `queued: true` means a job was enqueued — not delivered and not accepted —
// so poll Deliveries for the outcome.
//
// It replays the RECORDED bytes with the SAME eventId, which is the point:
// dedupe-by-eventId works across a retry, and therefore a receiver that
// already processed that id will correctly discard the replay. Replay the
// ones that never got through. It does not un-ring the fallback — if the
// visitor was already told and the thread released to a human, that stands.
func (s *BotsService) Redeliver(ctx context.Context, botID, deliveryID string) (Object, error) {
	if botID == "" || deliveryID == "" {
		return nil, errors.New("chatly: Bots.Redeliver: botID and deliveryID are required")
	}
	path := "/v1/bots/" + pathEscape(botID) + "/deliveries/" + pathEscape(deliveryID) + "/redeliver"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}

// TestDelivery signs a rehearsal payload with the agent's real secret, posts
// it to its real URL, and answers with what came back.
//
// POST /v1/bots/{id}/test-delivery — scope ai:configure, answers 201 with
// `{ ok, httpStatus, durationMs, error, responseBody, url, signature,
// payload }`. No request body.
//
// SYNCHRONOUS: it blocks for up to the agent's own timeout, which can be 60
// seconds, so raise this client's per-attempt timeout before calling it. A
// non-2xx from your endpoint is a 201 from us with `ok: false` — branch on
// the body, not the status — and httpStatus null with ok false means the
// request never completed at all.
//
// It WRITES NOTHING: no ledger row, no conversation, no retry. So it does not
// show up under Deliveries and calling it a hundred times costs a visitor
// nothing. `signature` and `payload` come back so you can check your verifier
// offline against the exact header value and the exact bytes.
//
// The payload carries `test: true` and all-zero UUIDs. Acknowledge with a 2xx
// and do not reply: a reply would target a conversation that does not exist.
func (s *BotsService) TestDelivery(ctx context.Context, botID string) (Object, error) {
	if botID == "" {
		return nil, errors.New("chatly: Bots.TestDelivery: empty bot id")
	}
	path := "/v1/bots/" + pathEscape(botID) + "/test-delivery"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}
