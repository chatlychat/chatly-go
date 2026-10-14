package chatly

import (
	"context"
	"errors"
)

// DocsSourcesService is /v1/docs-sources — connect a documentation provider
// and pull it into the knowledge base. Reach it as client.DocsSources.
//
// Reads want kb:read; EVERY write wants kb:write, including Discover, which
// reads like a read and is a POST because it carries a credential.
//
// One thing worth knowing before you start: every write on this controller
// REQUIRES an Idempotency-Key in production (428 without one). This SDK always
// sends one, so it only matters if you reach the routes through
// Client.Request.
type DocsSourcesService struct{ c *Client }

// The five providers Discover and Create accept, in the order Providers()
// returns them. `website` is last on purpose: it is the sitemap crawler, the
// fallback for a provider we do not integrate with.
const (
	DocsProviderGitBook    = "gitbook"
	DocsProviderForumulate = "forumulate"
	DocsProviderDiscourse  = "discourse"
	DocsProviderReadMe     = "readme"
	DocsProviderWebsite    = "website"
)

// Providers returns the static provider registry.
//
// GET /v1/docs-sources/providers — scope kb:read. A bare array of
// `{ name, label, needsBaseUrl, needsToken }`, with no workspace read behind
// it at all.
//
// DRIVE YOUR CONNECT FORM OFF THIS rather than hard-coding: needsBaseUrl is
// true for discourse and website only, and needsToken is false for website
// only. A copy of those two facts in your own code is the copy that goes
// stale the first time a provider is added.
func (s *DocsSourcesService) Providers(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/docs-sources/providers", nil)
}

// List returns the connected sources.
//
// GET /v1/docs-sources — scope kb:read. A bare array; the handler reads no
// query parameters.
//
// READ `syncStatus`, NOT `articleCount`. It is derived rather than stored and
// is one of queued, synced, synced_empty, synced_partial, failed or paused —
// and `articleCount: 0` on a fresh source means "has not run yet", which is
// exactly what made scripts poll forever or report the connect as failed.
//
// No field carries the stored token back, in any form — not a prefix, not a
// hint. And note that `status` here is a plain string (active, paused or
// error) while the patch accepts only two of those values, so the read and
// the write are not the same vocabulary.
func (s *DocsSourcesService) List(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/docs-sources", nil)
}

// DocsDiscoverInput is the body of Discover.
type DocsDiscoverInput struct {
	// Provider is one of the DocsProvider* constants.
	Provider string `json:"provider"`

	// Token is REQUIRED AND MAY BE EMPTY — note the missing omitempty. A
	// public Discourse category needs no key and the sitemap crawler reads
	// only what any visitor can, so `""` is a legal value; but the key must
	// be PRESENT, and an SDK that omitted it produced a 400 for every
	// `website` connect.
	Token string `json:"token"`

	// BaseURL is the customer's own instance. Schema-optional, and a 400 in
	// practice for discourse and website, which have nowhere else to look.
	BaseURL string `json:"baseUrl,omitempty"`

	// OrganizationID narrows the listing to one organisation.
	//
	// It matters in both directions: an ORG-SCOPED Forumulate token cannot
	// read the organisation list at all, so discovery without this fails the
	// whole flow for such a token, while an account-wide token without it
	// returns every space in every organisation — 60 unrelated spaces, which
	// is the right answer only for someone who has not chosen yet.
	OrganizationID string `json:"organizationId,omitempty"`
}

// Discover lists the spaces a credential can see, before you connect one.
//
// POST /v1/docs-sources/discover — scope kb:write, answers 201 with a bare
// array of `{ id, title, url, organizationId, organizationName, kind,
// visibility, itemCount, itemsUpdatedAt }`.
//
// The three credential fields are FLAT — provider, token, baseUrl — with no
// `credentials` wrapper, which is the shape most people reach for first. An
// item's `id` and `organizationId` are what Create wants as SpaceID and
// OrganizationID.
//
// It makes a LIVE call to the provider with the token you supply, so a bad
// credential surfaces as whatever the provider said. For `website` the
// "spaces" are synthetic: id "*" for the whole site plus one entry per first
// path segment, with the host as the organisation — and no sitemap at all is
// a 400.
//
// It writes nothing and still requires an Idempotency-Key, because the
// controller declares one class-wide.
func (s *DocsSourcesService) Discover(ctx context.Context, in DocsDiscoverInput) ([]Object, error) {
	if in.Provider == "" {
		return nil, errors.New("chatly: DocsSources.Discover: provider is required (see the DocsProvider* constants)")
	}
	return s.c.writeObjects(ctx, "POST", "/v1/docs-sources/discover", in)
}

// DocsSourceInput is the body of Create.
type DocsSourceInput struct {
	// Provider is one of the DocsProvider* constants.
	Provider string `json:"provider"`

	// Token is REQUIRED and may be the empty string — same rule as on
	// Discover, same missing omitempty, same reason.
	Token string `json:"token"`

	BaseURL string `json:"baseUrl,omitempty"`

	// OrganizationID is REQUIRED even for providers with no organisation
	// concept: for `website` it is the host string Discover returned. The
	// field is `organizationId`, not `orgId`.
	OrganizationID string `json:"organizationId"`

	// SpaceID is REQUIRED and is the `id` from a Discover item. The field is
	// `spaceId`, not `space`.
	SpaceID string `json:"spaceId"`

	// Label, 1..160 chars. Defaulted from the provider label and space
	// title, with the path filter appended.
	Label string `json:"label,omitempty"`

	// PublishOnSync publishes synced articles instead of leaving them as
	// drafts. The field is `publishOnSync`, not `publish`.
	PublishOnSync *bool `json:"publishOnSync,omitempty"`

	// SyncIntervalMinutes is 15..10080 and rejected out of range. The floor
	// matches the background scan cadence: a shorter interval could not be
	// honoured and would only look like it was.
	SyncIntervalMinutes int `json:"syncIntervalMinutes,omitempty"`

	// IncludePaths and ExcludePaths narrow a source to part of a space, max
	// 50 entries of 300 chars each.
	//
	// SUPPORTED ON forumulate AND website ONLY — a 400 for the other three,
	// rather than an ignored field. A filter of only "/" or "" is also a 400
	// ("inert"). Prefixes match at segment boundaries, so /guides/selling
	// does not match /guides/selling-tips, and EXCLUDE BEATS INCLUDE.
	IncludePaths []string `json:"includePaths,omitempty"`
	ExcludePaths []string `json:"excludePaths,omitempty"`
}

// Create connects a space and returns the source.
//
// POST /v1/docs-sources — scope kb:write, answers 201.
//
// The token is VERIFIED at connect time: the provider's space list is fetched
// and SpaceID must appear in it, otherwise the call is a 400 that enumerates
// the ids the token can actually see. So this route can fail on the network,
// not only on validation.
//
// A first sync is enqueued before the response returns, which means the
// source comes back with `syncStatus: "queued"` and every count at zero —
// the counts in THIS response are meaningless, so re-read List to watch it
// land.
//
// Connecting the same space twice with different path filters is a supported
// case, and each source gets its own KB collection.
func (s *DocsSourcesService) Create(ctx context.Context, in DocsSourceInput) (Object, error) {
	if in.Provider == "" || in.OrganizationID == "" || in.SpaceID == "" {
		return nil, errors.New("chatly: DocsSources.Create: provider, organizationId and spaceId are required")
	}
	return s.c.writeObject(ctx, "POST", "/v1/docs-sources", nil, in)
}

// DocsSourcePatch is the body of Update. Every field is optional, an absent
// one is left alone, and NO FIELD CAN CLEAR ANYTHING: there is no nullable
// field here, so a label, a base URL or a path filter cannot be removed
// through this route. IncludePaths and ExcludePaths are not patchable at all.
type DocsSourcePatch struct {
	Label string `json:"label,omitempty"`

	// Status accepts "active" and "paused" ONLY. "error" is a read-only
	// state the sync sets.
	//
	// Sending "active" also clears the stored lastError, as a side effect.
	Status string `json:"status,omitempty"`

	PublishOnSync *bool `json:"publishOnSync,omitempty"`

	// SyncIntervalMinutes, 15..10080.
	SyncIntervalMinutes int `json:"syncIntervalMinutes,omitempty"`

	// Token replaces the stored credential, 1..500 chars here — note the
	// limits differ from Create (1..400 there) for no reason worth relying
	// on.
	//
	// A replacement is PROVED against the provider before it is stored, and
	// outside the transaction: a token that authenticates but cannot see
	// this source's space is refused and the working credential survives.
	// Sending it also clears lastError.
	Token string `json:"token,omitempty"`

	// BaseURL, max 500 chars here against 300 on create. It is MERGED into
	// the stored config and NORMALISED TO ITS ORIGIN — any path, query or
	// fragment you send is discarded.
	BaseURL string `json:"baseUrl,omitempty"`
}

// Update patches a source and returns it.
//
// PATCH /v1/docs-sources/{id} — scope kb:write. The id is parsed as a UUID
// server-side, so a malformed one is a clean 400 here (unlike the
// customer-cards routes).
//
// Prefer this over Delete-then-Create for anything reversible: pausing with
// Status, or rotating Token, keeps the sync checkpoint and the webhook
// subscription that a disconnect destroys.
func (s *DocsSourcesService) Update(ctx context.Context, sourceID string, in DocsSourcePatch) (Object, error) {
	if sourceID == "" {
		return nil, errors.New("chatly: DocsSources.Update: empty source id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/docs-sources/"+pathEscape(sourceID), nil, in)
}

// Sync queues a sync for one source.
//
// POST /v1/docs-sources/{id}/sync — scope kb:write, answers 201 with
// `{ queued: true, full }`.
//
// `full` is BOTH a request field and a response field: you send it, and the
// response echoes what you sent. The flag is `full`, not `force` and not
// `fullSync`, and it DROPS THE INCREMENTAL WATERMARK first. Reach for it when
// the READER changed rather than the content — a source holding a checkpoint
// answers "sync now" with a delta and finds nothing, leaving the KB as wrong
// as it was. The default is false, because a scheduled sync must stay a delta
// or every space is re-read on every pass.
//
// THIS ROUTE NEVER VERIFIES THE SOURCE EXISTS. There is no existence check on
// the checkpoint reset and none before the enqueue, so a well-formed id for
// another workspace's source — or for no source at all — still answers 201
// `{ queued: true }`. Do not read a 201 here as proof the source was found.
//
// Completion is asynchronous: poll List and read syncStatus. N calls enqueue
// N jobs; only the inbound webhook path dedupes.
func (s *DocsSourcesService) Sync(ctx context.Context, sourceID string, full bool) (Object, error) {
	if sourceID == "" {
		return nil, errors.New("chatly: DocsSources.Sync: empty source id")
	}
	// The body is parsed even when it is empty, so `{}` always goes up.
	body := map[string]any{"full": full}
	return s.c.writeObject(ctx, "POST", "/v1/docs-sources/"+pathEscape(sourceID)+"/sync", nil, body)
}

// Webhook asks the PROVIDER to start pushing change events to Chatly.
//
// POST /v1/docs-sources/{id}/webhook — scope kb:write, answers 201 with
// `{ subscribed, reason? }`. No request body.
//
// This is a SUBSCRIBE, not a receiver, despite the path. The inbound receiver
// is a different, public route (POST /v1/webhooks/docs/{sourceId}) which is
// correctly absent from the SDK surface and must never get a key-authenticated
// method.
//
// `subscribed: false` WITH A REASON IS A SUCCESS, and it is the common case:
// customers paste read-only tokens, and a provider that needs a
// webhook-management scope says so. The source keeps polling on its schedule
// either way and `reason` names what to change, so do not raise on it.
// Already-subscribed is idempotent and registers nothing, because a second
// registration would make the space deliver twice.
//
// It does throw for three configuration faults, all 400: the source has no
// stored credentials, no organisation id is recorded, or the SERVER has no
// public URL configured to offer as a callback — that last one is our
// misconfiguration surfacing on your call.
func (s *DocsSourcesService) Webhook(ctx context.Context, sourceID string) (Object, error) {
	if sourceID == "" {
		return nil, errors.New("chatly: DocsSources.Webhook: empty source id")
	}
	path := "/v1/docs-sources/" + pathEscape(sourceID) + "/webhook"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}

// Delete disconnects a source and returns how many ARTICLES were kept.
//
// DELETE /v1/docs-sources/{id} — scope kb:write. Answers 200 with
// `{ kept }`, not a 204.
//
// `kept` counts KB articles that SURVIVED and were handed back to the
// customer: every article synced from this source is flipped to a manual
// article with no source, and nothing is deleted. `kept: 0` therefore means
// the source had synced nothing yet, not that articles were lost. The KB
// collection survives too, as an ordinary renameable one.
//
// It is NOT reversible as a reconnect: the row, its sync checkpoint, its
// webhook subscription and its signing secret all go, so reconnecting re-reads
// the whole space and push has to be set up again. Prefer Update with
// `Status: "paused"` or a rotated token.
//
// There is no 404: the statements carry no existence check, so a well-formed
// id for a nonexistent or other-workspace source answers 200 `{ kept: 0 }`.
func (s *DocsSourcesService) Delete(ctx context.Context, sourceID string) (Object, error) {
	if sourceID == "" {
		return nil, errors.New("chatly: DocsSources.Delete: empty source id")
	}
	return s.c.writeObject(ctx, "DELETE", "/v1/docs-sources/"+pathEscape(sourceID), nil, nil)
}
