package chatly

import (
	"context"
	"errors"
	"net/url"
	"strconv"
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

// Summary returns plan, seats, period and balance in one object.
//
// GET /v1/billing — scope workspace:billing:read. No query parameters.
// `{ plan, status, trialEnd, currentPeriodEnd, seatsUsed, seatsBilled,
// seatsLimit, seatsRemaining, seatsAtLimit, stripeConnected, hasSubscription,
// upcomingInvoiceAmountCents, upcomingInvoiceCurrency }`.
//
// `seatsLimit: -1` MEANS UNCAPPED, and that is every plan today (ADR 0034);
// so does `seatsRemaining: -1`. Treating -1 as a real ceiling inverts the
// check.
//
// `stripeConnected: false` is a SUCCESS: it means the deployment has no
// Stripe keys and `plan` is whatever the operator set. `status` is an
// unconstrained string (trialing, active, past_due, canceled, incomplete),
// deliberately not an enum.
//
// The whole billing controller keeps working on a SUSPENDED workspace, where
// the rest of /v1/* does not — so this is the one family to call when
// diagnosing a suspension.
func (s *BillingService) Summary(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/billing", nil)
}

// InvoiceListOptions are the two parameters the invoice list reads.
type InvoiceListOptions struct {
	// Cursor is the previous page's nextCursor, which on this route is an
	// ISO TIMESTAMP rather than an opaque token. An unparseable one is
	// SILENTLY the first page with a 200, not a 400.
	Cursor string
	// Limit is clamped 1..100, default 25, never rejected.
	Limit int
}

func (o *InvoiceListOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	setIf(q, "cursor", o.Cursor)
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	return q
}

// Invoices returns one page of Stripe invoices, newest first.
//
// GET /v1/billing/invoices — scope workspace:billing:read.
//
// GENUINELY CURSOR-PAGINATED, AND STILL AN OBJECT RATHER THAN A PAGE: the
// envelope is `{ data, nextCursor }`, the one route on this surface that
// spells it `data` instead of `items`. That is why there is no InvoicesAll —
// a walker keyed on `items` would read nothing and report an empty page
// forever. Follow `nextCursor` yourself and read the rows off `data`.
//
// The keyset is strict on created_at, so two invoices sharing an exact
// timestamp across a page boundary can drop one. Rare with Stripe webhooks,
// but real.
//
// These rows are Chatly's local MIRROR of Stripe, written by the webhook, not
// a live fetch: `invoicePdfUrl` and `hostedInvoiceUrl` are nullable, and
// `status` is paid, open or void as a bare string.
func (s *BillingService) Invoices(ctx context.Context, opts *InvoiceListOptions) (Object, error) {
	return s.c.getObject(ctx, "/v1/billing/invoices", opts.values())
}

// UsageDaily returns the daily series for ONE meter.
//
// GET /v1/billing/usage/daily — scope workspace:billing:read.
// `{ workspaceId, eventType, from, to, days }`, with the series under `days`
// as `{ day, total, count }`.
//
// eventType is effectively REQUIRED — omitted, empty or unrecognised is a
// 400 listing the known four — so this method refuses an empty one rather
// than making the round trip. Use the UsageEvent* constants.
//
// From and To are INDEPENDENTLY defaulted here, unlike the report routes'
// paired window: From omitted means the current UTC month's start, To omitted
// means From plus one month, and To alone is legal and shifts only the upper
// bound. An unparseable value or To <= From is a 400.
//
// `days` is DENSE — one entry per UTC day in the window, zeros included — so
// do not infer a gap from a missing key, and `day` is a full ISO string at
// UTC midnight rather than a bare YYYY-MM-DD.
//
// One silent limit: a range longer than 366 days is accepted with a 200 and
// the series is TRUNCATED at 366 entries with no flag, while `from` and `to`
// still echo the range you asked for. Bound the window yourself.
func (s *BillingService) UsageDaily(ctx context.Context, eventType string, opts *UsageOptions) (Object, error) {
	if eventType == "" {
		return nil, errors.New("chatly: Billing.UsageDaily: eventType is required (see the UsageEvent* constants)")
	}
	return s.c.getObject(ctx, "/v1/billing/usage/daily", opts.values(eventType))
}

// MonthlyUsageOptions is the one parameter the monthly rollup reads.
type MonthlyUsageOptions struct {
	// Month is "YYYY-MM" in UTC and is REJECTED with a 400 in any other
	// form — including "2026-6", a full ISO date, or "June 2026" — by a
	// hand-written parser. Omitted means the current calendar month.
	Month string
}

func (o *MonthlyUsageOptions) values() url.Values {
	q := url.Values{}
	if o != nil {
		setIf(q, "month", o.Month)
	}
	return q
}

// UsageMonthly returns the whole month's usage for EVERY meter.
//
// GET /v1/billing/usage/monthly — scope workspace:billing:read.
// `{ workspaceId, monthStart, items, total }`, an object and not a page: the
// `items` key has no nextCursor beside it.
//
// IT DOES NOT TAKE eventType. That is the asymmetry worth knowing: Usage and
// UsageDaily require it and 400 without it, while this route does not read
// the parameter at all and always rolls up all four meters. Passing it is
// neither an error nor a filter — it is ignored, and you get everything back
// looking as though you had asked for it.
//
// `items` is ALWAYS exactly four entries, one per metered event type, zeros
// included. The top-level `total` sums every items[].total and therefore adds
// token counts to conversation counts: a convenience sum, not a billable
// figure.
//
// The window is always a WHOLE calendar month in UTC; Usage is the
// arbitrary-range sibling.
func (s *BillingService) UsageMonthly(ctx context.Context, opts *MonthlyUsageOptions) (Object, error) {
	return s.c.getObject(ctx, "/v1/billing/usage/monthly", opts.values())
}
