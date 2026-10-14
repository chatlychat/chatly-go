package chatly

import (
	"context"
	"errors"
)

// CustomerCardsService is /v1/customer-cards — the operator-configured panels
// an agent sees beside a conversation, each of which Chatly fetches LIVE from
// a URL of yours. Reach it as client.CustomerCards.
//
// Reads want integrations:read, writes integrations:write. The live pull
// itself is Contacts.Cards, and it wants contacts:read instead.
//
// None of the {id} parameters on this controller is validated server-side, so
// a malformed UUID reaches Postgres and comes back as an opaque 500 rather
// than a 400 or a 404. Pass ids you got from List.
type CustomerCardsService struct{ c *Client }

// List returns every configured card.
//
// GET /v1/customer-cards — scope integrations:read. A bare array of
// `{ id, key, title, url, headerNames, ttlSeconds, enabled, position }`,
// ordered by position then key. At most 25 cards exist per workspace, which
// is why there is no cursor.
//
// `headerNames` is NAMES ONLY — never values, never a prefix of the signing
// secret, and there is no read-back route for either.
//
// This list includes DISABLED cards; the live pull (Contacts.Cards) only ever
// returns enabled ones, so the two lists legitimately differ in length.
func (s *CustomerCardsService) List(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/customer-cards", nil)
}

// CustomerCardInput is the body of Create and Update.
//
// The body is STRICT on both, so an unknown key is a 400 rather than a
// silently dropped field — one of the few places on this surface where a typo
// is visible.
type CustomerCardInput struct {
	// Key, 1..64 chars of ^[A-Za-z0-9_-]+$, required on create and unique in
	// the workspace. A duplicate on CREATE is a 409; renaming into a
	// duplicate on UPDATE hits the index with no handler and comes back as a
	// 500.
	Key string `json:"key,omitempty"`

	// Title, 1..80 chars, required on create. What the agent sees as the
	// panel heading.
	Title string `json:"title,omitempty"`

	// URL is required on create, max 2000 chars. OUR SERVER fetches it, so
	// it must be https with no embedded credentials and no private,
	// link-local, .local, .internal or IPv6-literal host — the refusal is a
	// 400 from that guard rather than from a URL parser.
	URL string `json:"url,omitempty"`

	// Headers are sent with every fetch: at most 10, names
	// ^[A-Za-z0-9-]+$ (1..100 chars), values 1..1000. The reserved names are
	// refused: x-chatly-signature, x-chatly-timestamp,
	// x-chatly-workspace-id, x-chatly-card-id, x-chatly-secret-scope,
	// content-type, accept, host, content-length, user-agent.
	//
	// On Update, an EMPTY map CLEARS the bag rather than meaning "no
	// change" — omit the field to leave it alone.
	Headers map[string]string `json:"headers,omitempty"`

	// TTLSeconds is REQUIRED on create (there is no default) and is
	// 15..31536000, rejected out of range rather than clamped. It is how
	// long Chatly caches your answer per contact.
	TTLSeconds int `json:"ttlSeconds,omitempty"`

	// Enabled defaults to true on create. Pointer so that disabling is
	// expressible; note that on Update an absent value does NOT re-apply the
	// create default.
	Enabled *bool `json:"enabled,omitempty"`

	// Position, 0..1000, orders the panels. UPDATE-ONLY: it is not a create
	// field (the strict body refuses it there), and create assigns the next
	// free slot.
	Position int `json:"position,omitempty"`
}

// Create configures a card and returns it WITH THE SECRET.
//
// POST /v1/customer-cards — scope integrations:write, answers 201 with
// `{ card, secret }` — the secret sits BESIDE the card, not inside it.
//
// That `cc_` secret is shown once and never again: no read path decrypts it
// outward, so store it with the endpoint that will verify signatures. A
// workspace is capped at 25 cards and the 26th is a 400.
func (s *CustomerCardsService) Create(ctx context.Context, in CustomerCardInput) (Object, error) {
	if in.Key == "" || in.Title == "" || in.URL == "" {
		return nil, errors.New("chatly: CustomerCards.Create: key, title and url are required")
	}
	if in.TTLSeconds <= 0 {
		return nil, errors.New("chatly: CustomerCards.Create: ttlSeconds is required (15..31536000)")
	}
	return s.c.writeObject(ctx, "POST", "/v1/customer-cards", nil, in)
}

// Update patches a card and returns it — without a secret.
//
// PATCH /v1/customer-cards/{id} — scope integrations:write. Every field is
// optional and an absent one is left alone, with one exception worth
// repeating: `headers: {}` CLEARS the header bag.
func (s *CustomerCardsService) Update(ctx context.Context, cardID string, in CustomerCardInput) (Object, error) {
	if cardID == "" {
		return nil, errors.New("chatly: CustomerCards.Update: empty card id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/customer-cards/"+pathEscape(cardID), nil, in)
}

// RotateSecret mints a new signing secret for one card.
//
// POST /v1/customer-cards/{id}/rotate-secret — scope integrations:write,
// answers 201 with `{ secret }`. No request body.
//
// SINGLE-PHASE AND IMMEDIATE: the previous card secret stops verifying the
// instant this returns, so update your receiver first. Unlike the endpoint
// rotation next door there is no pending-then-activate step here, and the
// value is shown once — a caller who loses it must rotate again.
func (s *CustomerCardsService) RotateSecret(ctx context.Context, cardID string) (Object, error) {
	if cardID == "" {
		return nil, errors.New("chatly: CustomerCards.RotateSecret: empty card id")
	}
	path := "/v1/customer-cards/" + pathEscape(cardID) + "/rotate-secret"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}

// Delete removes a card.
//
// DELETE /v1/customer-cards/{id} — scope integrations:write. Answers 200 with
// `{ ok: true }`, not a 204. A hard delete: there is no deleted_at on this
// table, and a missing row is a 404.
//
// Deleting the last card pointing at a URL leaves any customer-card ENDPOINT
// secret for that URL orphaned. Nothing errors; it shows up as
// `cardKeys: []` on CustomerCardEndpoints.List.
func (s *CustomerCardsService) Delete(ctx context.Context, cardID string) (Object, error) {
	if cardID == "" {
		return nil, errors.New("chatly: CustomerCards.Delete: empty card id")
	}
	return s.c.writeObject(ctx, "DELETE", "/v1/customer-cards/"+pathEscape(cardID), nil, nil)
}
