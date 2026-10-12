package chatly

import (
	"context"
	"errors"
	"net/url"
)

// EventsService is the /v1/cdp/events surface — the customer-data event
// stream. Reach it as client.Events.
type EventsService struct{ c *Client }

// TrackEventInput is the body of Track.
type TrackEventInput struct {
	// ContactID is required and must be a Chatly contact id (UUIDv7), not
	// your own external id. Upsert the contact first and use the id it
	// returns.
	ContactID string `json:"contactId"`

	// Name is required, 1..120 chars. Pick a stable vocabulary
	// ("order_placed", "trial_started") and keep it: workflow triggers and
	// segments match on this string, so renaming an event orphans every rule
	// built on the old name.
	Name string `json:"name"`

	// Properties is free-form, JSON-encodable data about this occurrence.
	Properties map[string]any `json:"properties,omitempty"`
}

// Track records one event against a contact.
//
// POST /v1/cdp/events — scope contacts:write:events (a scope of its own, not
// plain contacts:write). Returns the stored row.
//
// Events are what make a workflow fire and a segment match, so this is the
// call that connects your product's behaviour to Chatly's automation.
func (s *EventsService) Track(ctx context.Context, in TrackEventInput) (Object, error) {
	if in.ContactID == "" {
		return nil, errors.New("chatly: Events.Track: contactId is required")
	}
	if in.Name == "" {
		return nil, errors.New("chatly: Events.Track: name is required")
	}
	return s.c.writeObject(ctx, "POST", "/v1/cdp/events", nil, in)
}

// EventListOptions are the query parameters GET /v1/cdp/events accepts: the
// generic list grammar plus a contact filter.
type EventListOptions struct {
	ListOptions
	// ContactID restricts the stream to one contact. The same narrowing is
	// available as Filter: []string{"contactId:<id>"}; this is the shorthand.
	ContactID string
}

func (o *EventListOptions) values() url.Values {
	if o == nil {
		return url.Values{}
	}
	q := o.ListOptions.values()
	setIf(q, "contactId", o.ContactID)
	return q
}

// List reads the event stream back.
//
// GET /v1/cdp/events — scope contacts:read. Default order is occurredAt
// descending. Sortable: id, createdAt, occurredAt, name. Filterable:
// contactId, name, occurredAt, createdAt.
//
// This is how you answer "did my tracking call actually land?" without
// opening the dashboard.
func (s *EventsService) List(ctx context.Context, opts *EventListOptions) (*Page[Object], error) {
	return s.c.getPage(ctx, "/v1/cdp/events", opts.values())
}

// ListAll walks every page of the event stream.
//
// On a busy workspace this is an unbounded walk through history — scope it
// with ContactID, a `name` filter or an `occurredAt` filter first.
func (s *EventsService) ListAll(opts *EventListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/cdp/events", opts.values())
}
