package chatly

import (
	"context"
	"errors"
)

// CustomerCardEndpointsService is /v1/customer-card-endpoints — ONE signing
// secret per card URL, instead of one per card. Reach it as
// client.CustomerCardEndpoints.
//
// Why it exists: a single card request carries several cards, because Chatly
// batches by url and headers, and before this it was signed with ONE of their
// per-card secrets, named to you in `x-chatly-card-id`. A receiver serving six
// cards therefore had to hold six secrets and pick by that header, and could
// not predict which — a card still in Chatly's cache is not in the request, so
// the signer changed with the cache. Give the url its own secret and that all
// goes away: one secret, verified once per request, and no `x-chatly-card-id`
// on the wire at all (it is replaced by `x-chatly-secret-scope: endpoint`).
//
// THE PER-CARD SECRET STILL WORKS. It is the documented fallback for any url
// with no endpoint secret, so an existing integration keeps verifying
// untouched — but the endpoint secret takes precedence the MOMENT Create
// returns, which is why you deploy it first.
//
// Reads want integrations:read, writes integrations:write. As on the cards
// controller, the {id} parameters are not validated server-side, so a
// malformed UUID is an opaque 500.
type CustomerCardEndpointsService struct{ c *Client }

// List returns every card URL that has its own secret.
//
// GET /v1/customer-card-endpoints — scope integrations:read. A bare array of
// `{ id, url, cardKeys, pendingSecretSet, pendingSecretMintedAt, createdAt,
// updatedAt }`, ordered by url.
//
// `cardKeys` is the cards currently pointing at that url, and an EMPTY list
// means this secret signs NOTHING — every card that used the url was deleted
// or re-pointed. It is the only way an operator can see such an orphan.
//
// `pendingSecretSet` is a bare boolean on purpose: not a prefix, not a
// last-four, not a hash. A prefix of a 24-byte secret is a prefix of a
// secret. There is no read-back route for the value at all.
func (s *CustomerCardEndpointsService) List(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/customer-card-endpoints", nil)
}

// Create gives one card URL its own signing secret.
//
// POST /v1/customer-card-endpoints — scope integrations:write, answers 201
// with `{ endpoint, secret }`.
//
// The secret is `cce_`-prefixed, deliberately NOT the card's `cc_`: the two
// go in different slots of your config and are never interchangeable. Shown
// once.
//
// DEPLOY THE VERIFIER BEFORE CALLING THIS. From the moment it returns, every
// request to that url is signed with the new secret and carries no
// `x-chatly-card-id`.
//
// The url is compared BYTE FOR BYTE against your configured cards' urls,
// trailing slash included: a url no card uses is a 400 whose message
// enumerates the urls that do exist, rather than a secret that silently never
// signs anything. A url that already has an endpoint is a 409 — rotate it
// instead.
func (s *CustomerCardEndpointsService) Create(ctx context.Context, url string) (Object, error) {
	if url == "" {
		return nil, errors.New("chatly: CustomerCardEndpoints.Create: url is required")
	}
	body := map[string]any{"url": url}
	return s.c.writeObject(ctx, "POST", "/v1/customer-card-endpoints", nil, body)
}

// RotateSecret mints the NEXT secret for an endpoint — which signs nothing yet.
//
// POST /v1/customer-card-endpoints/{id}/rotate-secret — scope
// integrations:write, answers 201 with `{ secret, active: false }`. No
// request body.
//
// `active: false` is not decoration: the CURRENT secret keeps signing until
// ActivateSecret is called, and a caller that reads only `secret` and deploys
// it has no other signal in the body. The correct order is MINT, DEPLOY,
// ACTIVATE.
//
// That two-step IS the overlap window, and it is an order of operations
// rather than a span of time — Chatly emits exactly one signature per
// request, so at any instant exactly one secret signs.
//
// Calling it again before activating REPLACES the pending secret rather than
// failing, because an operator who lost the displayed value cannot read it
// back and should not have to break their endpoint to fix a typo. This route
// is therefore not naturally idempotent: pin an Idempotency-Key if you retry
// it after a timeout.
func (s *CustomerCardEndpointsService) RotateSecret(ctx context.Context, endpointID string) (Object, error) {
	if endpointID == "" {
		return nil, errors.New("chatly: CustomerCardEndpoints.RotateSecret: empty endpoint id")
	}
	path := "/v1/customer-card-endpoints/" + pathEscape(endpointID) + "/rotate-secret"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}

// ActivateSecret promotes the pending secret to the only one that signs.
//
// POST /v1/customer-card-endpoints/{id}/activate-secret — scope
// integrations:write, answers 201 with `{ activated: true }`. No request
// body.
//
// NO UNDO: the previous secret is gone the instant this returns, and it was
// never readable, so no column could restore it.
//
// A second call is a 409 ("there is no pending secret — rotate first"), and
// that is deliberately not papered over: it is the truthful answer to a
// caller who has lost track of a rotation. DO NOT RETRY THIS ON 409. A 404
// means the endpoint itself is gone, which the service distinguishes by
// reading the row first.
func (s *CustomerCardEndpointsService) ActivateSecret(ctx context.Context, endpointID string) (Object, error) {
	if endpointID == "" {
		return nil, errors.New("chatly: CustomerCardEndpoints.ActivateSecret: empty endpoint id")
	}
	path := "/v1/customer-card-endpoints/" + pathEscape(endpointID) + "/activate-secret"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}

// Delete removes an endpoint secret.
//
// DELETE /v1/customer-card-endpoints/{id} — scope integrations:write. Answers
// 200 with `{ ok: true }`, not a 204. A hard delete; a missing row is a 404.
//
// THIS IS A BEHAVIOUR CHANGE AT YOUR VERIFIER, not a cleanup: signing for
// that url reverts to the per-card secret of whichever card leads the group,
// and `x-chatly-card-id` starts being sent again. Treat it as a migration
// step and deploy the fallback verification first.
func (s *CustomerCardEndpointsService) Delete(ctx context.Context, endpointID string) (Object, error) {
	if endpointID == "" {
		return nil, errors.New("chatly: CustomerCardEndpoints.Delete: empty endpoint id")
	}
	path := "/v1/customer-card-endpoints/" + pathEscape(endpointID)
	return s.c.writeObject(ctx, "DELETE", path, nil, nil)
}
