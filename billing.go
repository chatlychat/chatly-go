package chatly

import (
	"context"
	"errors"
	"net/url"
	"time"
)

// BillingService is the /v1/billing surface. Reach it as client.Billing.
// Both routes need the workspace:billing:read scope, which keys do not carry
// by default.
type BillingService struct{ c *Client }

// Plans lists the purchasable plans and their prices.
//
// GET /v1/billing/plans — a BARE ARRAY of { plan, cadence, priceId,
// unitAmount, currency, interval }. `plan` is free, starter, growth, business
// or enterprise; `cadence` is monthly or annual; `unitAmount` is in MINOR
// UNITS (cents) and may be null.
//
// An EMPTY ARRAY is the expected answer on a self-hosted install with no
// Stripe configured — it is not an error and not a workspace with no plans.
func (s *BillingService) Plans(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/billing/plans", nil)
}

// The metered event types Usage accepts. Anything else is a 400.
const (
	UsageEventConversationOpened = "conversation.opened"
	UsageEventMessageSentAgent   = "message.sent.agent"
	UsageEventMessageSentBot     = "message.sent.bot"
	UsageEventAITokens           = "ai.tokens"
)

// UsageOptions bounds a usage query.
type UsageOptions struct {
	// From defaults, server-side, to the start of the current month.
	From time.Time
	// To defaults to the first of next month and is EXCLUSIVE.
	To time.Time
}

func (o *UsageOptions) values(eventType string) url.Values {
	q := url.Values{}
	q.Set("eventType", eventType)
	if o == nil {
		return q
	}
	setTime(q, "from", o.From)
	setTime(q, "to", o.To)
	return q
}

// Usage returns metered usage for ONE event type over a window.
//
// GET /v1/billing/usage — { workspaceId, eventType, from, to, total, count }.
//
// eventType is effectively required: the route rejects a missing or unknown
// value with 400, so this method refuses an empty one rather than making the
// round trip. Use the UsageEvent* constants. There is no "all event types"
// form — call it once per type you care about.
//
// `total` is the summed quantity and `count` the number of metered events;
// for ai.tokens those are very different numbers, and billing follows total.
func (s *BillingService) Usage(ctx context.Context, eventType string, opts *UsageOptions) (Object, error) {
	if eventType == "" {
		return nil, errors.New("chatly: Billing.Usage: eventType is required (see the UsageEvent* constants)")
	}
	return s.c.getObject(ctx, "/v1/billing/usage", opts.values(eventType))
}
