package chatly

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

// WebhooksService is the /v1/webhooks/deliveries surface: the delivery log for
// outbound webhooks, and the redelivery button. Reach it as client.Webhooks.
//
// Verifying an INBOUND delivery's signature is a free function, not part of
// this service — see VerifyWebhookSignature. That split is deliberate: you
// verify a webhook in an HTTP handler where there may be no client and no API
// key at all.
type WebhooksService struct{ c *Client }

// DeliveryListOptions are the query parameters the deliveries list reads.
//
// The route parses the full generic grammar and then forwards only these
// four, so Sort, Fields and Filter are accepted and IGNORED — which looks
// exactly like a filter that matched everything. They are therefore not
// offered here.
type DeliveryListOptions struct {
	// EndpointID narrows to one configured endpoint.
	EndpointID string
	// Status is pending, delivered or failed. Not validated server-side: an
	// unknown value simply matches nothing.
	Status string
	Cursor string
	// Limit is 1..100; the API defaults to 25.
	Limit int
}

func (o *DeliveryListOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	setIf(q, "endpointId", o.EndpointID)
	setIf(q, "status", o.Status)
	setIf(q, "cursor", o.Cursor)
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	return q
}

// Deliveries returns one page of the outbound delivery log.
//
// GET /v1/webhooks/deliveries — scope integrations:read. Each item is
// { id, endpointId, eventType, attempt, status, httpStatus, durationMs,
// error, nextRetryAt, deliveredAt, createdAt } — note NO payload and no url;
// those are only on the single-delivery read.
//
// A malformed cursor is a 400 (ErrValidation) with details.field = "cursor",
// not an empty page.
func (s *WebhooksService) Deliveries(ctx context.Context, opts *DeliveryListOptions) (*Page[Object], error) {
	return s.c.getPage(ctx, "/v1/webhooks/deliveries", opts.values())
}

// DeliveriesAll walks every page of the delivery log.
func (s *WebhooksService) DeliveriesAll(opts *DeliveryListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/webhooks/deliveries", opts.values())
}

// Delivery returns one delivery, including the payload that was sent and the
// URL it was sent to.
//
// GET /v1/webhooks/deliveries/{id} — scope integrations:read. The list item's
// fields plus `payload` and `url`. This is the call for "what exactly did you
// send me?", and the payload here is the body the signature was computed
// over.
func (s *WebhooksService) Delivery(ctx context.Context, deliveryID string) (Object, error) {
	if deliveryID == "" {
		return nil, errors.New("chatly: Webhooks.Delivery: empty delivery id")
	}
	return s.c.getObject(ctx, "/v1/webhooks/deliveries/"+pathEscape(deliveryID), nil)
}

// Redeliver queues a failed delivery to be sent again.
//
// POST /v1/webhooks/deliveries/{id}/redeliver — scope integrations:write. No
// request body. Answers 201 { queued: true, eventType, url }.
//
// `queued: true` means ACCEPTED FOR SENDING, not delivered: the new attempt
// happens on a worker and shows up as a fresh row in the delivery log. Poll
// Deliveries for the outcome rather than treating this response as success.
func (s *WebhooksService) Redeliver(ctx context.Context, deliveryID string) (Object, error) {
	if deliveryID == "" {
		return nil, errors.New("chatly: Webhooks.Redeliver: empty delivery id")
	}
	path := "/v1/webhooks/deliveries/" + pathEscape(deliveryID) + "/redeliver"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}
