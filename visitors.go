package chatly

import (
	"context"
	"errors"
)

// VisitorsService is the /v1/visitors surface. Reach it as client.Visitors.
//
// ONE method, and the other two routes on that controller are deliberately
// absent. GET /v1/visitors/live and GET /v1/visitors/{contactId}/pages carry
// no @AllowApiKey() in the API, so a ck_ key gets a 403 from both — they are
// not SDK surface. A method that always fails is worse than a missing one:
// the failure arrives at runtime, in production, in someone else's
// integration. Reach them with a session credential, not a key.
type VisitorsService struct{ c *Client }

// StartConversation opens a conversation with a contact who did NOT write
// first, and returns { conversationId }.
//
// POST /v1/visitors/{contactId}/start-conversation — scope
// conversations:write. No request body and no query parameters: the contact
// id is the whole input. The controller declares @RequireIdempotencyKey() at
// class level with no opt-out on this handler, so the key is REQUIRED — a
// write without one is a 428 in production. This SDK mints one per call, and
// WithIdempotencyKey pins one if you want a retried outreach to replay rather
// than open a second thread.
//
// ─── A 403 HERE IS PROBABLY NOT YOUR SCOPES ───────────────────────────────
//
// This route is OFF BY DEFAULT, per workspace. The flag is
// settings.proactiveOutreach.apiKeysMayStartConversations; an owner or admin
// turns it on with PATCH /v1/workspace/settings, which needs a signed-in
// person and so is not reachable from this SDK, and you can read its current
// value with client.Workspace.Settings, which is.
//
// So an ErrForbidden from this method means one of two quite different
// things, and the workspace setting is the likelier — it is the one route on
// this surface where a 403 is usually NOT about the key's scopes. The scope
// is not the opt-in: conversations:write was already grantable and is held by
// keys minted for ticket bulk actions and schedule-send, so opening this
// route on the permission alone would have handed cold outreach to
// credentials nobody re-reviewed. Check the setting before you go looking at
// your key.
//
// ─── THE OTHER TWO THINGS TO KNOW ─────────────────────────────────────────
//
// Rate limit: THREE PER CONTACT PER HOUR, counted per workspace and per
// CONTACT rather than per caller — the abuse shape is N threads at one
// person, which a per-caller limit cannot see. The fourth call inside the
// window is an ErrRateLimited.
//
// And this route can answer 429 in TWO ways that do not look alike:
//
//   - the per-contact limit above sends NO Retry-After header, so
//     APIError.RetryAfter is ZERO. Its TTL is in the body instead, at
//     error.details.retryAfterSeconds, reachable through APIError.Body.
//     A caller that backs off on RetryAfter alone retries immediately.
//   - the global throttler buckets this route inherits (30/second,
//     100/15 minutes) DO carry Retry-After, and APIError.RetryAfter is set.
//
// Do not read a zero RetryAfter as "retry now".
//
// An offline visitor is NOT notified. The greeting is persisted and the
// widget renders it from history on their next page view: no email, no push,
// no provider message, and nothing queued or dropped. An integration that
// needs to reach someone who has left the site wants an email or SMS
// conversation, not this.
func (s *VisitorsService) StartConversation(ctx context.Context, contactID string) (Object, error) {
	if contactID == "" {
		return nil, errors.New("chatly: Visitors.StartConversation: empty contact id")
	}
	path := "/v1/visitors/" + pathEscape(contactID) + "/start-conversation"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}
