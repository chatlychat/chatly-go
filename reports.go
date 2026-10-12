package chatly

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// ReportsService is the /v1/reports surface. Reach it as client.Reports.
// Everything here needs the reports:read scope.
type ReportsService struct{ c *Client }

// ReportWindow is the time window for Overview, Agents and ConversationsByDay.
//
// Those three take ONE parameter, `days`, and nothing else — no from/to, no
// grouping. Days is clamped server-side rather than rejected: 500 silently
// becomes 90.
type ReportWindow struct {
	// Days is 1..90. Zero means the route's own default: 7 for Overview and
	// Agents, 14 for ConversationsByDay.
	Days int
}

func (w *ReportWindow) values() url.Values {
	q := url.Values{}
	if w != nil && w.Days > 0 {
		q.Set("days", strconv.Itoa(w.Days))
	}
	return q
}

// Overview returns the headline numbers for a window.
//
// GET /v1/reports/overview — { windowDays, openConversations, messagesSent,
// csat: { average, sampleSize } }.
//
// Read sampleSize before quoting average: a 5.0 CSAT over two responses is
// not a score, and this endpoint will hand you one without comment.
func (s *ReportsService) Overview(ctx context.Context, w *ReportWindow) (Object, error) {
	return s.c.getObject(ctx, "/v1/reports/overview", w.values())
}

// Agents returns per-agent performance for a window.
//
// GET /v1/reports/agents — a BARE ARRAY of { userId, name,
// conversationsHandled, avgFirstResponseSeconds, avgResolutionSeconds,
// csatAvg, csatCount }.
//
// The three averages are null when there is nothing to average — an agent
// with no resolved conversation has no resolution time. Treat null as absent,
// not as zero; a dashboard that coerces it shows an agent resolving
// everything instantly.
func (s *ReportsService) Agents(ctx context.Context, w *ReportWindow) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/reports/agents", w.values())
}

// ConversationsByDay returns a daily conversation count.
//
// GET /v1/reports/conversations-by-day — a BARE ARRAY of
// { day: "YYYY-MM-DD", count }. Default window 14 days.
func (s *ReportsService) ConversationsByDay(ctx context.Context, w *ReportWindow) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/reports/conversations-by-day", w.values())
}

// SLAOptions is the window for the SLA report, which takes a richer set than
// the other three.
type SLAOptions struct {
	// Days is 1..366, default 30. Ignored when From and To are both set.
	Days int

	// From and To must be supplied TOGETHER — one alone is a 400 — and when
	// set they win over Days. Max span is 366 days. Sent as RFC3339.
	From time.Time
	To   time.Time

	// GroupBy is hour, day, week or month. An invalid value is a 400, and
	// "hour" is refused on a window longer than 14 days.
	//
	// It goes on the wire as `group_by`: this one route is snake_case while
	// the rest of the API is camelCase. The SDK handles that; it is noted
	// because `groupBy` typed into Params would be silently ignored.
	GroupBy string
}

func (o *SLAOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	if o.Days > 0 {
		q.Set("days", strconv.Itoa(o.Days))
	}
	setTime(q, "from", o.From)
	setTime(q, "to", o.To)
	setIf(q, "group_by", o.GroupBy)
	return q
}

// SLA returns the SLA attainment report.
//
// GET /v1/reports/sla — a large object: { from, to, windowDays,
// coverage: { conversations, governed, coveragePct }, clocks, byTarget,
// byPolicy, byTeam, byAgent, breaches, breachesTruncated }.
//
// Two fields decide whether the rest means anything. `coverage.coveragePct`
// is how much of the traffic any SLA policy governed at all — attainment over
// 4% coverage is a statement about 4% of the workspace. And
// `breachesTruncated` says the `breaches` list was cut short, so counting its
// length instead of reading `byTarget[].breached` undercounts.
func (s *ReportsService) SLA(ctx context.Context, opts *SLAOptions) (Object, error) {
	return s.c.getObject(ctx, "/v1/reports/sla", opts.values())
}
