package chatly

import (
	"context"
	"errors"
)

// EmailTemplatesService is /v1/email-templates — the operator's control over
// the words in Chatly's transactional mail. Reach it as
// client.EmailTemplates.
//
// Reads want workspace:read, writes workspace:write. The bar is
// workspace:write rather than something softer because this copy goes out
// under the workspace's brand to people who are NOT in the workspace: a new
// teammate, the billing owner, a hired agent.
//
// THE RESOURCE IS THE KIND. There is one override per workspace per kind,
// there are no row ids, and both writes address a kind in the path — which is
// what makes Update idempotent by construction and Delete a restore.
type EmailTemplatesService struct{ c *Client }

// The five overridable transactional emails. The {kind} path segment is
// parsed against this list, so an unknown value is a 400 naming it, not a
// 404.
const (
	EmailTemplateWorkspaceInvite             = "workspace_invite"
	EmailTemplateTrialEndingReminder         = "trial_ending_reminder"
	EmailTemplateConversationCapWarning      = "conversation_cap_warning"
	EmailTemplateHiredAgentEngagementInvite  = "hired_agent_engagement_invite"
	EmailTemplateHiredAgentEngagementStarted = "hired_agent_engagement_started"
)

// List returns all five kinds, with this workspace's override where there is
// one.
//
// GET /v1/email-templates — scope workspace:read. A bare array, ALWAYS of
// length five: the catalogue is the spine, not the rows. Each item is
// `{ kind, label, whenSent, audience, builtInSummary, tokens, override }`.
//
// `override: null` means the workspace is on the built-in. There is no third
// state, and that is what makes Delete a restore rather than a deletion.
//
// Two shape details: the built-in subject and body text are NOT returned,
// only `builtInSummary` prose; and a token's `optional` key is ABSENT rather
// than false when the token is always supplied.
func (s *EmailTemplatesService) List(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/email-templates", nil)
}

// Preview renders unsaved copy through the real send-time renderer.
//
// POST /v1/email-templates/preview — scope workspace:read, answers 201 with
// `{ subject, html, text, unknownTokens }`. Nothing is written.
//
// A POST rather than a GET because the subject and body ARE the request, they
// are far too long for a query string, and a preview of somebody's draft copy
// has no business in an access log.
//
// Here `kind` IS a body field — the mirror image of Update, where a `kind` in
// the body is a 400 — and the body is strict with all three fields required.
// `subject` and `body` may be EMPTY STRINGS on this route only.
//
// `unknownTokens` is the typo net: tokens you wrote that this kind does not
// supply, deduplicated and capped at ten. An empty array is the healthy
// answer. The renderer's two token rules are worth knowing, because the
// preview reflects both: an UNKNOWN token is left verbatim in the sent mail
// (`{{contct.name}}` ships as written), while an allow-listed but unset token
// renders empty.
func (s *EmailTemplatesService) Preview(ctx context.Context, kind, subject, body string) (Object, error) {
	if kind == "" {
		return nil, errors.New("chatly: EmailTemplates.Preview: kind is required (see the EmailTemplate* constants)")
	}
	payload := map[string]any{"kind": kind, "subject": subject, "body": body}
	return s.c.writeObject(ctx, "POST", "/v1/email-templates/preview", nil, payload)
}

// Update replaces this workspace's copy for one transactional email.
//
// PUT /v1/email-templates/{kind} — scope workspace:write, answers 200 with
// the kind as stored (the same shape as one List element).
//
// The body is STRICT and holds only `{ subject, body }`: the kind lives in
// the path, and including it in the body is a 400. Both are required and both
// are trimmed, 1..300 and 1..10000 characters — this is a full replace, with
// no partial form and no way to clear one half.
//
// An upsert on (workspace, kind), so it is idempotent by construction and
// last-writer-wins. It also writes an audit row carrying the full before and
// after text.
func (s *EmailTemplatesService) Update(ctx context.Context, kind, subject, body string) (Object, error) {
	if kind == "" {
		return nil, errors.New("chatly: EmailTemplates.Update: kind is required (see the EmailTemplate* constants)")
	}
	if subject == "" || body == "" {
		return nil, errors.New("chatly: EmailTemplates.Update: subject and body are both required and must be non-empty")
	}
	payload := map[string]any{"subject": subject, "body": body}
	return s.c.writeObject(ctx, "PUT", "/v1/email-templates/"+pathEscape(kind), nil, payload)
}

// Delete drops the override and goes back to the built-in copy.
//
// DELETE /v1/email-templates/{kind} — scope workspace:write. Answers 200 with
// `{ restored }`, NOT a 204, and it does not return the restored template.
//
// `{ restored: false }` with a 200 means the workspace was already on the
// built-in: the requested state holds, so it is a success and the flag is the
// only way to tell the two apart. Do not map a falsy flag to an error.
//
// "Restore the built-in" means tracking whatever the built-in says TODAY,
// rather than a frozen copy of it.
func (s *EmailTemplatesService) Delete(ctx context.Context, kind string) (Object, error) {
	if kind == "" {
		return nil, errors.New("chatly: EmailTemplates.Delete: kind is required (see the EmailTemplate* constants)")
	}
	return s.c.writeObject(ctx, "DELETE", "/v1/email-templates/"+pathEscape(kind), nil, nil)
}
