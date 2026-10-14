package chatly

import (
	"context"
	"errors"
)

// The webhook ENDPOINT half of WebhooksService: registering a subscriber,
// reading the event catalogue, and pinning a payload schema version. The
// delivery log lives in webhookdeliveries.go and signature verification is a
// free function in webhooks.go.
//
// Everything here needs integrations:read or integrations:write.

// List returns the workspace's webhook endpoints.
//
// GET /v1/webhooks — scope integrations:read. A bare array of exactly six
// fields: `{ id, url, description, eventTypes, enabled, schemaVersion }`. No
// workspaceId, no timestamps, and NO SECRET — the select names six columns
// precisely so the signing secret is readable from no route at all.
//
// Note the naming, which cost this SDK a method: `webhooks.list` is this
// route, and `webhooks.deliveries` is the delivery log. Pointing List at the
// log left no method for the endpoints themselves.
//
// `eventTypes` is a plain string array on read (it is a stored column), not
// the closed enum the write validates against.
func (s *WebhooksService) List(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/webhooks", nil)
}

// Events returns the subscribable event catalogue.
//
// GET /v1/webhooks/events — scope integrations:read. An OBJECT with one key,
// `{ eventTypes: [...] }`, wrapping a flat array of NAMES: no descriptions,
// no grouping, nothing per-workspace. Static, so cache it.
//
// It is the exact enum Create validates against, which makes it the
// authoritative list. Three names that look plausible are deliberately
// absent because nothing publishes them — conversation.bot_changed,
// ai.run.completed, ai.tool_call.pending — so do not offer them as choices.
func (s *WebhooksService) Events(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/webhooks/events", nil)
}

// WebhookEndpointInput is the body of Create.
type WebhookEndpointInput struct {
	// URL is required. Any scheme that parses as a URL is accepted here; use
	// https.
	URL string `json:"url"`

	// Description, max 280 chars.
	Description string `json:"description,omitempty"`

	// EventTypes is REQUIRED with at least one entry, and the field name is
	// `eventTypes` — not `events`. Each name is validated against the closed
	// catalogue Events() returns, so an unknown one is a 400.
	//
	// Three old spellings are refused on purpose: ticket.transitioned is now
	// ticket.status_changed, and survey.submitted is now
	// survey.response_received.
	EventTypes []string `json:"eventTypes"`

	// SchemaVersion pins the payload shape, e.g. "2026-10-14". Omitted, the
	// column's default decides what "latest" means. An unrecognised id is a
	// 400 here — loud on the write, generous at dispatch.
	SchemaVersion string `json:"schemaVersion,omitempty"`
}

// Create registers a webhook endpoint and returns it WITH THE SECRET.
//
// POST /v1/webhooks — scope integrations:write, answers 201 with the six list
// fields plus `workspaceId`, `secret`, `createdAt` and `updatedAt`.
//
// This is the ONLY response anywhere that carries `secret` (a `whsec_` value,
// which is what VerifyWebhookSignature wants). There is no reveal route and
// no rotation route, so losing it means deleting the endpoint and creating
// another. Store it before you do anything else.
//
// You do not supply the secret; the server mints it. `enabled` is not
// settable either — the column defaults to true.
func (s *WebhooksService) Create(ctx context.Context, in WebhookEndpointInput) (Object, error) {
	if in.URL == "" {
		return nil, errors.New("chatly: Webhooks.Create: url is required")
	}
	if len(in.EventTypes) == 0 {
		return nil, errors.New("chatly: Webhooks.Create: at least one eventType is required (see Webhooks.Events)")
	}
	return s.c.writeObject(ctx, "POST", "/v1/webhooks", nil, in)
}

// SchemaVersions lists the published payload schema versions.
//
// GET /v1/webhooks/schema-versions — scope integrations:read.
// `{ latest, versions }`, where versions is OLDEST FIRST and `latest` is the
// last entry. Each version carries the `eventTypes` a pin at that version
// will actually receive.
//
// `isDeprecated` is advisory ONLY: nothing refuses a deprecated pin, and
// every published version is currently undeprecated. Static and
// workspace-independent.
func (s *WebhooksService) SchemaVersions(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/webhooks/schema-versions", nil)
}

// SetSchemaVersion pins one endpoint to a payload schema version.
//
// PATCH /v1/webhooks/{id}/schema-version — scope integrations:write. The body
// is strict and holds one field, `schemaVersion`. That spelling matters:
// SchemaVersions calls the same thing `versions[].version`, the two are not
// interchangeable, and `{ version }` here is a 400 rather than a silent
// no-op — which is the lucky outcome.
//
// It cannot change the url, the event types or the enabled flag. The response
// carries `missingEventTypes`, a list of event NAMES this pin still does not
// receive — names, not a count.
//
// Moving a pin BACKWARD stops delivering the types newer versions added, and
// both directions take effect on the next event: the dispatcher reads the
// column per delivery with no cache. An unknown version is refused here, but
// an unknown version already stored resolves to latest at dispatch time; the
// asymmetry is intentional.
func (s *WebhooksService) SetSchemaVersion(ctx context.Context, endpointID, schemaVersion string) (Object, error) {
	if endpointID == "" || schemaVersion == "" {
		return nil, errors.New("chatly: Webhooks.SetSchemaVersion: endpointID and schemaVersion are required")
	}
	body := map[string]any{"schemaVersion": schemaVersion}
	path := "/v1/webhooks/" + pathEscape(endpointID) + "/schema-version"
	return s.c.writeObject(ctx, "PATCH", path, nil, body)
}

// Delete removes a webhook subscription.
//
// DELETE /v1/webhooks/{id} — scope integrations:write. Answers 200 with
// `{ ok: true }`, not a 204.
//
// It was closed to API keys until 2026-10-14 while Create was open, and that
// asymmetry was worse than closing the whole surface would have been: a
// deploy pipeline that registers an endpoint per environment leaked one into
// the customer's workspace on every run with nothing able to clean up. The
// workaround that used to be recommended — repointing the endpoint at a dead
// URL — leaves the subscription alive and the delivery log filling with
// failures, so it is not one.
//
// Safe by the argument Create already makes: both want integrations:write,
// and a credential that can add a delivery target can already do the thing
// with consequences.
//
// There is no undo and no reveal route, so deleting an endpoint destroys the
// only copy of its signing secret: recreating it mints a new one, and every
// receiver verifying the old value has to be redeployed.
func (s *WebhooksService) Delete(ctx context.Context, endpointID string) (Object, error) {
	if endpointID == "" {
		return nil, errors.New("chatly: Webhooks.Delete: empty endpoint id")
	}
	return s.c.writeObject(ctx, "DELETE", "/v1/webhooks/"+pathEscape(endpointID), nil, nil)
}
