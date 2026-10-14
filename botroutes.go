package chatly

import (
	"context"
	"errors"
)

// BotRoutesService is /v1/bot-routes — the rules that decide WHICH bot
// answers an incoming conversation. Reach it as client.BotRoutes.
//
// Note the scopes: triggers:read and triggers:write, the same pair the
// triggers namespace uses, because a bot route is a routing rule rather than
// part of the bot.
type BotRoutesService struct{ c *Client }

// When a route is eligible to be evaluated.
const (
	// BotRouteEvaluateConversationStart only considers the route on the
	// first inbound message.
	BotRouteEvaluateConversationStart = "conversation_start"
	// BotRouteEvaluateEveryMessage keeps the route eligible for the whole
	// thread. A route that "works on the first message and then stops" is
	// almost always one that was left on conversation_start.
	BotRouteEvaluateEveryMessage = "every_message"
)

// List returns every bot route in EVALUATION ORDER.
//
// GET /v1/bot-routes — scope triggers:read. A bare array, ordered by priority
// then createdAt, and the first matching route wins.
//
// The handler reads NO query parameters at all — not merely a schema that
// ignores them, there is no @Query on it — so cursor, limit, sort, fields and
// filter are not options here. There is also no GET /v1/bot-routes/{id}: this
// list is the only read.
//
// `channelTypes` and `conditions` are always arrays (both columns are NOT NULL
// with a `[]` default), and a condition's `value` may be absent entirely —
// zod cannot express a required `unknown` — so decode it loosely.
func (s *BotRoutesService) List(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/bot-routes", nil)
}

// Fields returns the condition vocabulary a route may be keyed on.
//
// GET /v1/bot-routes/fields — scope triggers:read. An OBJECT with four keys,
// `{ fields, prefixes, operators, evaluate }`, not the bare array the route
// name suggests.
//
// Read it rather than guessing: the vocabulary is CLOSED (six fixed keys plus
// three open prefixes) and an unknown field name is a 400 at write time. The
// response is entirely static — no workspace read, no query — so cache it.
//
// One caveat about `operators`: it is hand-written beside the validator
// rather than derived from it, so treat "served" as a strong hint rather than
// proof of "accepted".
func (s *BotRoutesService) Fields(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/bot-routes/fields", nil)
}

// BotRouteCondition is one clause a route matches on.
type BotRouteCondition struct {
	// Field must come from the Fields() vocabulary: one of the fixed keys
	// (page.path, page.url, contact.identityVerified, channel.publicId,
	// locale, body) or a key under an allowed prefix
	// (context.verified., context.unverified., contact.attributes.). The
	// open part is one segment of ^[A-Za-z][A-Za-z0-9_-]{0,39}$ — no dots.
	//
	// This was free-form before migration 0602, so a body this endpoint
	// used to accept can now be a 400.
	Field string `json:"field"`
	// Op is eq, neq, in, gt, lt, contains or matches.
	Op string `json:"op"`
	// Value is compared against the resolved field. Any JSON type.
	Value any `json:"value,omitempty"`
}

// BotRouteInput is the body of Create and Update.
type BotRouteInput struct {
	// BotID is required on create and must name a bot in THIS workspace —
	// checked at write time, because a bad id would otherwise surface much
	// later as "no route ever matched".
	//
	// On Update it is NOT checked: only the foreign key is, so a valid id
	// belonging to another tenant is accepted and yields a route that
	// silently never answers.
	BotID string `json:"botId,omitempty"`

	// Name, 1..160 chars, required on create.
	Name string `json:"name,omitempty"`

	// Priority is 0..10000, lower first. Defaulted by the COLUMN (100), not
	// by the schema, so omitting it on create is not the same as sending 0.
	Priority int `json:"priority,omitempty"`

	// Enabled defaults to true at the column. A DISABLED route is omitted
	// from Resolve's `routes` list entirely rather than reported with a
	// verdict, so this list is where you learn the flag.
	Enabled *bool `json:"enabled,omitempty"`

	// ChannelTypes narrows the route to particular channels, max 20. An
	// EMPTY list means any channel.
	ChannelTypes []string `json:"channelTypes,omitempty"`

	// Conditions, max 20, all of which must hold. An empty list matches
	// everything. Replaced wholesale on update, never merged.
	Conditions []BotRouteCondition `json:"conditions,omitempty"`

	// BrandID narrows the route to one brand.
	BrandID string `json:"brandId,omitempty"`

	// Evaluate is one of the BotRouteEvaluate* constants and is defaulted by
	// the column rather than the schema, deliberately, so the client and the
	// database cannot disagree about the default.
	Evaluate string `json:"evaluate,omitempty"`
}

// Create adds a routing rule and returns it.
//
// POST /v1/bot-routes — scope triggers:write, answers 201.
func (s *BotRoutesService) Create(ctx context.Context, in BotRouteInput) (Object, error) {
	if in.BotID == "" || in.Name == "" {
		return nil, errors.New("chatly: BotRoutes.Create: botId and name are required")
	}
	return s.c.writeObject(ctx, "POST", "/v1/bot-routes", nil, in)
}

// Update patches a routing rule and returns it.
//
// PATCH /v1/bot-routes/{id} — scope triggers:write. Every field is optional
// and, unlike Bots.Update, an EMPTY body is accepted: it simply bumps
// updated_at.
//
// ChannelTypes and Conditions are replaced wholesale rather than merged, and
// a BotID sent here is not validated for workspace ownership (see
// BotRouteInput.BotID).
func (s *BotRoutesService) Update(ctx context.Context, routeID string, in BotRouteInput) (Object, error) {
	if routeID == "" {
		return nil, errors.New("chatly: BotRoutes.Update: empty route id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/bot-routes/"+pathEscape(routeID), nil, in)
}

// Delete removes a routing rule.
//
// DELETE /v1/bot-routes/{id} — scope triggers:write. Answers 200 with
// `{ deleted: true }`, not a 204, and `deleted` is a literal rather than a
// result: the statement carries no existence check and no row-count check, so
// an unknown id, an already-deleted route and another workspace's id all
// answer the same thing.
//
// A hard delete — there is no deleted_at on this table — and conversations
// already routed are unaffected.
func (s *BotRoutesService) Delete(ctx context.Context, routeID string) (Object, error) {
	if routeID == "" {
		return nil, errors.New("chatly: BotRoutes.Delete: empty route id")
	}
	return s.c.writeObject(ctx, "DELETE", "/v1/bot-routes/"+pathEscape(routeID), nil, nil)
}

// BotRouteResolveInput is the hypothetical visitor Resolve evaluates against.
//
// The body is STRICT — the only strict body in this namespace — so an unknown
// key is a 400 rather than a silently dropped field. Every field has a
// server-side default, and an empty struct is a valid request.
type BotRouteResolveInput struct {
	// Page is `{ url, path }`. The two are derived from each other, and an
	// unparseable url yields a null path rather than a 400.
	Page map[string]any `json:"page,omitempty"`

	// Context is `{ verified, unverified }`, each a map of string to string.
	// These are what `context.verified.*` conditions read.
	Context map[string]any `json:"context,omitempty"`

	// Contact is `{ identityVerified, attributes }`.
	Contact map[string]any `json:"contact,omitempty"`

	// ChannelType defaults to "web" server-side.
	ChannelType string `json:"channelType,omitempty"`

	ChannelPublicID string `json:"channelPublicId,omitempty"`
	BrandID         string `json:"brandId,omitempty"`
	Locale          string `json:"locale,omitempty"`

	// Body is the visitor's message text, max 4000 chars.
	Body string `json:"body,omitempty"`

	// Moment is one of the BotRouteEvaluate* constants — "conversation_start"
	// (the default) or "follow_up". At follow_up only routes that opted into
	// every_message are eligible at all, which is the field to reach for when
	// a route answers the first message and then goes quiet.
	Moment string `json:"moment,omitempty"`
}

// Resolve dry-runs the routing table against a hypothetical visitor.
//
// POST /v1/bot-routes/resolve — scope triggers:read. A read dressed as a POST
// (the body carries a page, a context and a contact, which is too much
// structure for a query string) and it WRITES NOTHING: no conversation is
// read, no contact, no stored visitor context.
//
// `{ selected, routes, event }`. Three things make it worth reading rather
// than guessing:
//
//   - DISABLED ROUTES ARE OMITTED ENTIRELY rather than reported with a
//     verdict, so a route missing from `routes` is a disabled route. List()
//     shows the flag.
//   - Several routes can report `matched`; only the first carries
//     `selected: true`. That is deliberate, so a priority problem does not
//     look like a condition problem.
//   - A failed condition reports `actual`, the value the field resolved to.
//     Absent means the visitor has no such attribute; a real value means
//     routing works and the expectation was wrong. Different bugs, identical
//     symptom.
//
// This is the production decision rather than an approximation of it — the
// worker calls a wrapper over the same function — but it applies none of the
// answerability guards (thread open, human took it, bot already owns it), so
// a `selected` route here is not a promise the bot would actually answer.
func (s *BotRoutesService) Resolve(ctx context.Context, in BotRouteResolveInput) (Object, error) {
	return s.c.writeObject(ctx, "POST", "/v1/bot-routes/resolve", nil, in)
}
