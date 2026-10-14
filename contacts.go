package chatly

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

// ContactsService is the /v1/contacts surface. Reach it as client.Contacts.
type ContactsService struct{ c *Client }

// ContactInput is the body of an upsert. Every field is optional — the API's
// schema has no required field — but a contact with nothing identifying in it
// is not useful, so send at least one of ExternalID, Email or Phone.
//
// Matching is by ExternalID first, then Email, then Phone: an upsert with the
// same ExternalID updates the existing contact rather than creating a second
// one. That is what makes this safe to call on every login.
//
// The `omitempty` tags matter: an absent field leaves the stored value alone,
// whereas sending "" would overwrite a real name with an empty string.
// To clear a field deliberately, use Client.Request with an explicit null.
type ContactInput struct {
	// ExternalID is your own primary key for this person, max 256 chars.
	// The field that makes an upsert idempotent and the one identity
	// verification signs (see IdentityHash).
	ExternalID string `json:"externalId,omitempty"`

	Email string `json:"email,omitempty"`

	// Phone, max 32 chars. E.164 travels best.
	Phone string `json:"phone,omitempty"`

	// Name, max 160 chars.
	Name string `json:"name,omitempty"`

	AvatarURL string `json:"avatarUrl,omitempty"`

	// Locale, max 10 chars, e.g. "en-GB".
	Locale string `json:"locale,omitempty"`

	// Timezone, max 64 chars, e.g. "Europe/London".
	Timezone string `json:"timezone,omitempty"`

	// Attributes is free-form custom data. Keys are yours; values must be
	// JSON-encodable. This is where plan tier, MRR, signup date and anything
	// else you want an agent or a workflow condition to see goes.
	Attributes map[string]any `json:"attributes,omitempty"`
}

// ContactListOptions are the query parameters GET /v1/contacts accepts.
//
// Note it does NOT accept the generic sort/fields/filter grammar — the only
// parameters are cursor, limit and blocked. Anything else is ignored.
type ContactListOptions struct {
	Cursor string
	// Limit is 1..100; the API defaults to 25.
	Limit int
	// Blocked filters on the block flag: true for blocked contacts only,
	// false to exclude them, nil (the default) for all of them.
	Blocked *bool
	// Params is an escape hatch for a parameter added after this release.
	Params url.Values
}

func (o *ContactListOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	for k, vs := range o.Params {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Blocked != nil {
		q.Set("blocked", strconv.FormatBool(*o.Blocked))
	}
	return q
}

// List returns one page of contacts, newest first.
//
// GET /v1/contacts — scope contacts:read.
func (s *ContactsService) List(ctx context.Context, opts *ContactListOptions) (*Page[Object], error) {
	return s.c.getPage(ctx, "/v1/contacts", opts.values())
}

// ListAll walks every page of contacts.
//
//	pager := client.Contacts.ListAll(nil)
//	for pager.Next(ctx) { use(pager.Value()) }
//	if err := pager.Err(); err != nil { return err }
func (s *ContactsService) ListAll(opts *ContactListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/contacts", opts.values())
}

// Get returns one contact by its Chatly id (not your external id — to find a
// contact by yours, use Upsert, which returns the matched row, or Search).
//
// GET /v1/contacts/{id} — scope contacts:read. ErrNotFound if it does not
// exist in this workspace.
func (s *ContactsService) Get(ctx context.Context, contactID string) (Object, error) {
	if contactID == "" {
		return nil, errors.New("chatly: Contacts.Get: empty contact id")
	}
	return s.c.getObject(ctx, "/v1/contacts/"+pathEscape(contactID), nil)
}

// Upsert creates or updates one contact and returns it.
//
// POST /v1/contacts — scope contacts:write. Safe to call on every login: the
// match rules in ContactInput mean a repeat updates rather than duplicates.
func (s *ContactsService) Upsert(ctx context.Context, in ContactInput) (Object, error) {
	return s.c.writeObject(ctx, "POST", "/v1/contacts", nil, in)
}

// BulkUpsert upserts up to 5000 contacts in one request.
//
// POST /v1/contacts/bulk — scope contacts:write. It answers 200 with a
// summary, NOT the contacts:
//
//	{ total, created, updated, skipped, errors: [{ row, message }] }
//
// `errors` is capped at the first 100 and is a PARTIAL FAILURE report, not an
// HTTP error: a request where every row fails still returns 200, so check
// `skipped`/`errors` rather than trusting the status. `row` is the index in
// the slice you passed.
func (s *ContactsService) BulkUpsert(ctx context.Context, contacts []ContactInput) (Object, error) {
	if len(contacts) == 0 {
		return nil, errors.New("chatly: Contacts.BulkUpsert: no contacts (the API requires 1..5000)")
	}
	body := map[string]any{"contacts": contacts}
	return s.c.writeObject(ctx, "POST", "/v1/contacts/bulk", nil, body)
}

// Merge folds mergedID into survivorID and returns the surviving contact.
//
// POST /v1/contacts/{id}/merge — scope contacts:write. The path id SURVIVES;
// the one in the body is absorbed and gone. Getting the two the wrong way
// round is not reversible, so check which is which before the first call.
func (s *ContactsService) Merge(ctx context.Context, survivorID, mergedID string) (Object, error) {
	if survivorID == "" || mergedID == "" {
		return nil, errors.New("chatly: Contacts.Merge: both survivorID and mergedID are required")
	}
	body := map[string]any{"mergedId": mergedID}
	return s.c.writeObject(ctx, "POST", "/v1/contacts/"+pathEscape(survivorID)+"/merge", nil, body)
}

// AddTags adds ONE tag to a contact and returns the updated contact.
//
// POST /v1/contacts/{id}/tags — scope contacts:write.
//
// Singular, despite the plural name: the route's body is `{ "tag": "..." }`
// and takes exactly one. The name mirrors this SDK's cross-language surface
// entry (`contacts.add_tags`), which the parity gate keys on. Call it once
// per tag; each call is a separate write with its own idempotency key.
func (s *ContactsService) AddTags(ctx context.Context, contactID, tag string) (Object, error) {
	if contactID == "" || tag == "" {
		return nil, errors.New("chatly: Contacts.AddTags: contactID and tag are required")
	}
	body := map[string]any{"tag": tag}
	return s.c.writeObject(ctx, "POST", "/v1/contacts/"+pathEscape(contactID)+"/tags", nil, body)
}

// RemoveTag removes one tag and returns the updated contact.
//
// DELETE /v1/contacts/{id}/tags/{tag} — scope contacts:write.
//
// The tag travels in the PATH, so it is percent-encoded here. Tags routinely
// contain spaces and may contain a slash; an unescaped one would address a
// different route entirely.
func (s *ContactsService) RemoveTag(ctx context.Context, contactID, tag string) (Object, error) {
	if contactID == "" || tag == "" {
		return nil, errors.New("chatly: Contacts.RemoveTag: contactID and tag are required")
	}
	path := "/v1/contacts/" + pathEscape(contactID) + "/tags/" + pathEscape(tag)
	return s.c.writeObject(ctx, "DELETE", path, nil, nil)
}

// UnsubscribeInput is the body of Unsubscribe.
type UnsubscribeInput struct {
	// Unsubscribed is required. Pass false to RE-subscribe — this one route
	// does both directions, which is why the field is not implied by the
	// method name.
	Unsubscribed bool `json:"unsubscribed"`
	// Reason is optional, max 500 chars, and is stored for audit. Worth
	// filling in: "GDPR request" and "bounced" are different facts later.
	Reason string `json:"reason,omitempty"`
}

// Unsubscribe sets (or clears) a contact's marketing unsubscribe flag.
//
// POST /v1/contacts/{id}/unsubscribe — scope contacts:write. This governs
// campaigns and bulk email, not transactional conversation replies.
func (s *ContactsService) Unsubscribe(ctx context.Context, contactID string, in UnsubscribeInput) (Object, error) {
	if contactID == "" {
		return nil, errors.New("chatly: Contacts.Unsubscribe: empty contact id")
	}
	path := "/v1/contacts/" + pathEscape(contactID) + "/unsubscribe"
	return s.c.writeObject(ctx, "POST", path, nil, in)
}

// JourneyOptions are the query parameters GET /v1/contacts/{id}/journey takes.
//
// Unlike the list route, this one is strict: a Days or Limit outside its
// range is a 400, not a clamp.
type JourneyOptions struct {
	// Days is the lookback window, 1..365. Zero means the API's default, 90.
	Days int
	// Limit is 1..100. Zero means the API's default, 25.
	Limit int
	// Cursor here is NOT an opaque blob: this route's cursor is an ISO-8601
	// timestamp with an offset, from the previous page's nextCursor. Pass it
	// through unchanged — or let JourneyAll do it.
	Cursor string
}

func (o *JourneyOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	if o.Days > 0 {
		q.Set("days", strconv.Itoa(o.Days))
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	return q
}

// Journey returns one page of a contact's timeline: page views, tracked
// events, conversations, messages and surveys, newest first.
//
// GET /v1/contacts/{id}/journey — scope contacts:read. Each item is
// `{ kind, id, occurredAt, title, subtitle, meta }` where kind is one of
// page_view, event, conversation, message, survey.
func (s *ContactsService) Journey(ctx context.Context, contactID string, opts *JourneyOptions) (*Page[Object], error) {
	if contactID == "" {
		return nil, errors.New("chatly: Contacts.Journey: empty contact id")
	}
	return s.c.getPage(ctx, "/v1/contacts/"+pathEscape(contactID)+"/journey", opts.values())
}

// JourneyAll walks every page of a contact's timeline within the window.
func (s *ContactsService) JourneyAll(contactID string, opts *JourneyOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/contacts/"+pathEscape(contactID)+"/journey", opts.values())
}

// Cards runs the operator's customer cards for one contact.
//
// POST /v1/contacts/{contactId}/cards — scope contacts:read, answers 201 with
// `{ cards: [...] }`. The collection key is `cards`, not `items`.
//
// A POST THAT IS A READ, and not a create — the create is
// CustomerCards.Create. POST because the response is per contact and per
// conversation, is cacheable by nobody, and a GET would put a conversation id
// in an access log beside the contact's email.
//
// It is a LIVE OUTBOUND FETCH to your own backend: Chatly signs a request to
// each enabled card's URL, batching cards that share a URL and headers, with
// a 15-second timeout per card. Expect multi-second latency, and set this
// client's timeout accordingly.
//
// Each card comes back as `{ key, title, state, blocks, text }` where state is
// ok, empty, missing, invalid or error. `blocks: null` is a real value
// meaning "no data for this contact" rather than a failure, and `text` is the
// plain-text rendition for a surface that cannot render blocks.
//
// It NEVER answers 404: a workspace with no cards gets `{ cards: [] }` before
// the contact is even looked up, and so does an unknown or deleted contact.
//
// The optional conversationID is FORWARDED to your endpoint — as
// `conversation.id`, with `reference` always null — but it is not authorised
// against the contact, and it is NOT part of Chatly's per-card cache key. A
// result fetched with no conversation can therefore be replayed to a request
// that has one.
//
// The path parameter is spelled `contactId` rather than `id`, the only
// contacts route like that, and it is not validated: a malformed id is a 500
// on a workspace that has cards.
func (s *ContactsService) Cards(ctx context.Context, contactID, conversationID string) (Object, error) {
	if contactID == "" {
		return nil, errors.New("chatly: Contacts.Cards: empty contact id")
	}
	// The body is strict and still REQUIRED: an absent body is a 400, so an
	// empty object goes up when there is no conversation to name.
	body := map[string]any{}
	if conversationID != "" {
		body["conversationId"] = conversationID
	}
	return s.c.writeObject(ctx, "POST", "/v1/contacts/"+pathEscape(contactID)+"/cards", nil, body)
}

// MoodOptions is the one parameter the mood route takes.
type MoodOptions struct {
	// Days is 7..365, defaulting to 90, and REJECTED out of range rather
	// than clamped.
	//
	// Note the FLOOR OF 7, where the sibling Journey route's Days floor is 1.
	// The two are not interchangeable and a shared validator would be wrong
	// about one of them.
	Days int
}

func (o *MoodOptions) values() url.Values {
	q := url.Values{}
	if o != nil && o.Days > 0 {
		q.Set("days", strconv.Itoa(o.Days))
	}
	return q
}

// Mood returns weekly sentiment buckets for one contact.
//
// GET /v1/contacts/{id}/mood — scope contacts:read.
// `{ contactId, days, sampleCount, buckets }`, where each bucket is
// `{ weekStart, averageScore, count }` snapped to UTC Monday and sorted
// oldest first.
//
// READ `sampleCount`, NOT len(buckets). Fewer than three scored messages
// suppresses the series entirely, so an empty `buckets` with a low
// sampleCount is "not enough signal", which is a success and not an empty
// result for "no data".
//
// `days` is echoed back so a chart can label itself — it is the window asked
// for, not a count of anything. The scores come from the cached
// per-message sentiment rather than from the messages, so a workspace that
// has sentiment analysis turned off reports no samples.
func (s *ContactsService) Mood(ctx context.Context, contactID string, opts *MoodOptions) (Object, error) {
	if contactID == "" {
		return nil, errors.New("chatly: Contacts.Mood: empty contact id")
	}
	return s.c.getObject(ctx, "/v1/contacts/"+pathEscape(contactID)+"/mood", opts.values())
}
