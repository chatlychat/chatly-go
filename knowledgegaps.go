package chatly

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

// KnowledgeGapsService is /v1/knowledge-gaps — the questions the knowledge
// base could not answer, and the drafting of articles from them. Reach it as
// client.KnowledgeGaps.
//
// Reads want kb:read, writes kb:write.
type KnowledgeGapsService struct{ c *Client }

// The four gap statuses. `drafted` is the one that is easy to forget: it
// means an article was raised from the gap but nobody has published it.
const (
	KnowledgeGapOpen      = "open"
	KnowledgeGapDrafted   = "drafted"
	KnowledgeGapResolved  = "resolved"
	KnowledgeGapDismissed = "dismissed"
)

// KnowledgeGapListOptions are the two query parameters the list takes. Note
// that NEITHER is validated strictly, in opposite ways.
type KnowledgeGapListOptions struct {
	// Status is one of the KnowledgeGap* constants. An unrecognised value is
	// SILENTLY IGNORED — you get the unfiltered list with a 200, not a 400 —
	// so a typo here looks like a filter that matched everything.
	Status string

	// Limit is CLAMPED to 1..200 rather than rejected, defaulting to 100.
	// There is no cursor, and there does not need to be: the recorder caps a
	// workspace at 200 open gaps, so the list is the list.
	Limit int
}

func (o *KnowledgeGapListOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	setIf(q, "status", o.Status)
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	return q
}

// List returns the recorded gaps, most-asked first.
//
// GET /v1/knowledge-gaps — scope kb:read. A bare array of
// `{ id, question, occurrences, firstSeenAt, lastSeenAt, status, articleId,
// conversationId }`.
//
// The sort is occurrences then lastSeenAt, both descending — A QUEUE, NOT A
// LOG. It is never recency-first, so the top row is the question most worth
// answering rather than the most recent one.
//
// `conversationId: null` means the gap was recorded outside a conversation,
// and Draft will refuse it with a 400. `articleId` is non-null once something
// has been drafted.
func (s *KnowledgeGapsService) List(ctx context.Context, opts *KnowledgeGapListOptions) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/knowledge-gaps", opts.values())
}

// Update sets a gap's status and returns the gap.
//
// PATCH /v1/knowledge-gaps/{id} — scope kb:write. The body is strict and
// holds exactly one field, `{ status }`: there is no `question` to edit and
// no note to attach. The manifest calls this `update` and the handler calls
// it `setStatus`; the handler has the honest name.
//
// It does NOT touch articleId, so setting "drafted" by hand leaves the gap
// claiming a draft that does not exist. Use Draft for that.
//
// The id is not validated server-side, so a malformed one comes back as a 500
// rather than a 400; an unknown but well-formed one is a 404.
func (s *KnowledgeGapsService) Update(ctx context.Context, gapID, status string) (Object, error) {
	if gapID == "" {
		return nil, errors.New("chatly: KnowledgeGaps.Update: empty gap id")
	}
	if status == "" {
		return nil, errors.New("chatly: KnowledgeGaps.Update: status is required (see the KnowledgeGap* constants)")
	}
	body := map[string]any{"status": status}
	return s.c.writeObject(ctx, "PATCH", "/v1/knowledge-gaps/"+pathEscape(gapID), nil, body)
}

// Draft asks the AI to write a KB article answering the gap.
//
// POST /v1/knowledge-gaps/{id}/draft — scope kb:write, answers 201 with
// `{ gap, articleId, reason }`. No request body.
//
// BOTH NULLABLE FIELDS ARE LOAD-BEARING and a 201 can carry either pair. When
// the model judges the conversation holds no reusable answer you get
// `articleId: null` WITH a `reason` — surfaced rather than swallowed, because
// "we looked and there is nothing to write" is useful. On success it is an id
// with `reason: null`, and the nested gap comes back at status "drafted".
// Never infer one field from the other.
//
// A gap with no conversation behind it, or one whose transcript renders
// empty, is a 400 — a different shape entirely, not `articleId: null`.
//
// The article is created as a DRAFT and never published. This makes a live
// LLM call over up to 200 messages, so it is slow and metered: raise this
// client's per-attempt timeout before calling it in a loop.
func (s *KnowledgeGapsService) Draft(ctx context.Context, gapID string) (Object, error) {
	if gapID == "" {
		return nil, errors.New("chatly: KnowledgeGaps.Draft: empty gap id")
	}
	path := "/v1/knowledge-gaps/" + pathEscape(gapID) + "/draft"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}
