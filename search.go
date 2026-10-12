package chatly

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"
)

// SearchService is the /v1/search surface. Reach it as client.Search.
type SearchService struct{ c *Client }

// The five entity kinds search can return.
const (
	SearchKindContact      = "contact"
	SearchKindConversation = "conversation"
	SearchKindMessage      = "message"
	SearchKindTicket       = "ticket"
	SearchKindKB           = "kb"
)

// SearchOptions narrows a search. Everything is optional except the query
// itself, which is a separate argument.
type SearchOptions struct {
	// Kinds restricts which entity types are searched. Empty means all five.
	//
	// ──  READ THIS BEFORE USING IT  ──────────────────────────────────────
	// Sent as a repeated plain parameter (`kinds=ticket&kinds=kb`), which is
	// the only form the API reads. Two traps, both server-side and both
	// measured by reading the schema rather than guessing:
	//
	//   * A SINGLE kind is currently REJECTED with 400 validation_failed.
	//     The route's schema wants an array; the API's query parser hands it
	//     a bare string when a key appears once, and the schema does not
	//     coerce. So Kinds: []string{"kb"} fails while
	//     []string{"kb","ticket"} works.
	//   * The bracketed form other SDKs send (`kinds[]=kb`) is dropped
	//     silently — the parser leaves the key as the literal "kinds[]" and
	//     the schema strips it. Result: 200 OK, every kind searched, no
	//     error. This SDK does not send that form.
	//
	// Until the API folds the single-value case, either pass two or more
	// kinds or leave this empty and filter the `kind` field client-side.
	Kinds []string

	// From and To bound creation time (From inclusive, To exclusive).
	From time.Time
	To   time.Time

	// ChannelID, AssigneeUserID and Status narrow conversation-ish hits.
	ChannelID      string
	AssigneeUserID string
	Status         string

	// Limit is 1..100; the API defaults to 25.
	Limit int
	// Cursor comes from the previous response's nextCursor, max 512 chars.
	Cursor string
}

func (o *SearchOptions) values(q string) url.Values {
	v := url.Values{}
	v.Set("q", q)
	if o == nil {
		return v
	}
	for _, kind := range o.Kinds {
		// Add, not Set: repeated keys stay repeated.
		v.Add("kinds", kind)
	}
	setTime(v, "from", o.From)
	setTime(v, "to", o.To)
	setIf(v, "channelId", o.ChannelID)
	setIf(v, "assigneeUserId", o.AssigneeUserID)
	setIf(v, "status", o.Status)
	setIf(v, "cursor", o.Cursor)
	if o.Limit > 0 {
		v.Set("limit", strconv.Itoa(o.Limit))
	}
	return v
}

// Query runs a full-text search across contacts, conversations, messages,
// tickets and KB articles.
//
// GET /v1/search — scope search:read. The query is 1..256 chars and
// required. The response is
//
//	{ items: [{ kind, id, title, snippet, rank, createdAt, conversationId }],
//	  nextCursor, tier, facets }
//
// `facets` counts hits per kind for the whole result set, so a UI can show
// "12 tickets, 3 articles" without five requests. `tier` is "postgres" or
// "opensearch" and tells you WHICH engine answered — it matters because
// `rank` is on a different scale in each (ts_rank_cd versus BM25), so never
// compare or threshold a rank across tiers.
//
// It is not a page: Query returns the raw object, because the response has
// two fields beyond {items,nextCursor} that a Page would throw away. Use
// QueryAll to walk the cursor.
func (s *SearchService) Query(ctx context.Context, q string, opts *SearchOptions) (Object, error) {
	if q == "" {
		return nil, errors.New("chatly: Search.Query: empty query")
	}
	return s.c.getObject(ctx, "/v1/search", opts.values(q))
}

// QueryAll walks every page of search hits, discarding the per-page `tier`
// and `facets` fields. Use Query if you need those.
func (s *SearchService) QueryAll(q string, opts *SearchOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/search", opts.values(q))
}
