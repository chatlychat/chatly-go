package chatly

import (
	"context"
	"errors"
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

// ── The report window is not spelled the same way twice ──────────────────
//
// No reports route validates its query with zod. They read raw query strings
// and clamp with min/max, so A MISSPELLED WINDOW PARAMETER IS NOT A 400 — it
// is silently the default window, and the report comes back looking
// plausible. That is worse than the usual zod-strips-unknown-keys trap,
// because there is no schema to read. Hence a separate options type per
// shape rather than one shared bag:
//
//	sources, attribution, response_times   days 1..90
//	voice                                  days 1..365
//	cohorts                                WEEKS 2..26, and no days at all
//	teams, labels                          days 1..366 + from/to/group_by
//	themes                                 date — one batch date, not a range
//	themes/{id}/conversations              nothing at all
//
// `days` and `weeks` are CLAMPED silently. An explicit from/to pair is
// REJECTED when it is wrong: one without the other is a 400, to <= from is a
// 400, and a span over the route's maximum is a 400 rather than a clamp.

// Sources returns where conversations came from, by channel.
//
// GET /v1/reports/sources — scope reports:read. A BARE ARRAY of
// `{ channelId, channelType, channelName, conversations, percentage }`.
//
// The window is Days, clamped 1..90, and the default here is 7 — not the 30
// its siblings use.
//
// `channelId` is null for a channel type whose channel row is gone;
// `channelName` then holds a human label for the type and is never null. Note
// that `percentage` is 0..100 on this route, where the voice report's rates
// are 0..1.
//
// This is the CHANNEL rollup. Attribution is the campaign one.
func (s *ReportsService) Sources(ctx context.Context, w *ReportWindow) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/reports/sources", w.values())
}

// AttributionOptions narrows the attribution report.
type AttributionOptions struct {
	// Dimension is the page-context key to group on: utm_source (the
	// default), utm_medium, utm_campaign, utm_content, utm_term,
	// referrer_url, page_url_at_open, device_type, browser or os.
	//
	// An unrecognised value is REFUSED with a 400 — the handler validates it
	// and throws before querying — so there is no "unknown dimension falls
	// back to the default" behaviour to lean on.
	Dimension string
	// Days is clamped 1..90, default 30.
	Days int
}

func (o *AttributionOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	setIf(q, "dimension", o.Dimension)
	if o.Days > 0 {
		q.Set("days", strconv.Itoa(o.Days))
	}
	return q
}

// Attribution returns conversation volume and resolution grouped on one
// page-context key.
//
// GET /v1/reports/attribution — scope reports:read. A BARE ARRAY of
// `{ value, label, conversations, percentage, resolved, resolutionRate }`.
//
// It is attribution of CONVERSATIONS, not of revenue and not of contacts: the
// grouping comes from the campaign and page data the widget captured when the
// thread opened.
//
// `value: null` IS AMBIGUOUS, and the field to discriminate on is `label`.
// Rows are capped at 50; the untagged bucket is a real row with
// `label: "(not tagged)"`, and when the cap is exceeded the tail is FOLDED
// into one row with `label: "(other)"` — both carry a null value. The
// untagged row is in the percentage denominator deliberately, so dropping it
// inflates every other percentage. Nothing is lost to the cap: the tail is
// summed rather than truncated, which is also why there is no cursor.
func (s *ReportsService) Attribution(ctx context.Context, opts *AttributionOptions) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/reports/attribution", opts.values())
}

// CohortOptions is the window for the retention grid, and it is NOT days.
type CohortOptions struct {
	// Weeks is clamped 2..26, default 8.
	//
	// There is no `days` parameter on this route. Sending one is not an
	// error and not a window — it is ignored, and you get the default 8
	// weeks back with `weeks: 8` in the response echoing a window you did
	// not ask for.
	Weeks int
}

func (o *CohortOptions) values() url.Values {
	q := url.Values{}
	if o != nil && o.Weeks > 0 {
		q.Set("weeks", strconv.Itoa(o.Weeks))
	}
	return q
}

// Cohorts returns the weekly CONTACT retention grid.
//
// GET /v1/reports/cohorts — scope reports:read. An object,
// `{ weeks, cells }`, where the rows live under `cells` as
// `{ cohortWeek, periodOffset, contactsActive, cohortSize, percentage }`.
//
// `weeks` in the response is the CLAMPED window actually used, not your input
// echoed, so label your axes from it rather than from your own request. The
// grid is sparse — it is a cell list, so a caller building a matrix fills the
// gaps itself — and it measures contact retention rather than conversation
// volume.
func (s *ReportsService) Cohorts(ctx context.Context, opts *CohortOptions) (Object, error) {
	return s.c.getObject(ctx, "/v1/reports/cohorts", opts.values())
}

// ResponseTimes returns first-response and resolution percentiles.
//
// GET /v1/reports/response-times — scope reports:read. An object:
// `{ windowDays, conversationsCreated, conversationsClosed, firstResponse,
// resolution, byDay }`, where the two middle objects are
// `{ p50, p95, sampleSize }`.
//
// The percentiles are SECONDS and are NULL — not 0 — when sampleSize is 0.
// This is the only route carrying percentiles for the whole workspace;
// Reports.Agents carries means only.
//
// `byDay` is the same shape GET /v1/reports/conversations-by-day returns
// BARE. The two routes are not interchangeable, so do not alias them.
//
// Days is clamped 1..90, default 30.
func (s *ReportsService) ResponseTimes(ctx context.Context, w *ReportWindow) (Object, error) {
	return s.c.getObject(ctx, "/v1/reports/response-times", w.values())
}

// Voice returns the voice-call rollup.
//
// GET /v1/reports/voice — scope reports:read. An object with three array
// members rather than one rows key: `{ windowDays, totalCalls, inboundCalls,
// outboundCalls, answeredCalls, answerRate, abandonRate, abandonedCalls,
// talkTime, byHour, byProvider, agents, recordingStorage }`.
//
// Days is clamped 1..365 here, where the sibling rollups cap at 90.
//
// `answerRate` and `abandonRate` are 0..1 FRACTIONS and nullable — unlike the
// 0..100 `percentage` on the sources and attribution reports. talkTime's
// averages are null when its sampleSize is 0, and byHour's `weekday` plus
// `hour` form the heatmap key.
//
// Read `recordingStorage.exact` before quoting a byte count: when it is
// false, `totalBytesUpperBound` is part measured and part bitrate
// extrapolation over `unmeasuredCalls`. The flag is as much the contract as
// the number.
func (s *ReportsService) Voice(ctx context.Context, w *ReportWindow) (Object, error) {
	return s.c.getObject(ctx, "/v1/reports/voice", w.values())
}

// ReportRange is the richer window the teams and labels rollups accept — and
// only those two.
type ReportRange struct {
	// Days is clamped 1..366, default 30, and is IGNORED when From and To
	// are both set.
	Days int

	// From and To must be supplied TOGETHER: exactly one of them is a 400.
	// From is inclusive and TO IS EXCLUSIVE — a bare calendar date in To is
	// pushed forward one day, so it means "through the end of that day".
	//
	// Unlike Days, these are rejected rather than clamped: to <= from is a
	// 400 and a span over 366 days is a 400.
	From time.Time
	To   time.Time

	// GroupBy is hour, day, week or month, default day. An invalid value is
	// a 400, and "hour" over a span longer than 14 days is a 400 — which
	// fires on the teams report too, even though its response carries no
	// granularity field at all.
	//
	// IT GOES ON THE WIRE AS `group_by`. These two routes and the SLA report
	// are snake_case while the rest of the API is camelCase; the SDK handles
	// it, which is why passing "groupBy" through Params would be silently
	// ignored.
	GroupBy string

	// BusinessHours measures durations inside the workspace's business hours
	// instead of in calendar time. It goes on the wire as `business_hours`,
	// and the server reads only the literal "true" or "1" as true — "TRUE"
	// and "yes" silently mean false.
	BusinessHours bool
}

func (o *ReportRange) values() url.Values {
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
	if o.BusinessHours {
		q.Set("business_hours", "true")
	}
	return q
}

// Teams returns the per-team rollup.
//
// GET /v1/reports/teams — scope reports:read. An object,
// `{ from, to, windowDays, businessHours, rows }`, with the rows under
// `rows`: `{ teamId, teamName, memberCount, conversationsCreated,
// conversationsResolved, openNow, firstResponse, resolution, csatAverage,
// csatCount, slaBreaches }`.
//
// `rows` contains a row with `teamId: null` — the unassigned bucket.
//
// Read `businessHours.applied` before captioning any duration. When it is
// false the figures are CALENDAR time, with `businessHours.reason` naming why
// (disabled, not_configured, always_closed or invalid_timezone); the numbers
// are never zeroed and never relabelled. `sampleTruncated` means the
// business-hours computation sampled rather than completed.
func (s *ReportsService) Teams(ctx context.Context, opts *ReportRange) (Object, error) {
	return s.c.getObject(ctx, "/v1/reports/teams", opts.values())
}

// Labels returns the per-label rollup.
//
// GET /v1/reports/labels — scope reports:read. Same window grammar as Teams.
// An object: `{ from, to, windowDays, granularity, businessHours,
// labelledConversations, unlabelledConversations, otherLabels, rows, trend }`.
//
// `rows` is capped at the 50 BUSIEST LABELS and the remainder is a COUNT in
// `otherLabels`, not rows — there is no cursor, so the tail is unreachable
// from this route. `trend` covers the top 8 labels only, and its `counts` is
// keyed BY LABEL NAME, which is customer data: decode it as a map, never a
// struct.
//
// Unlike Teams, this one echoes `granularity`, so that is the value to label
// buckets with.
func (s *ReportsService) Labels(ctx context.Context, opts *ReportRange) (Object, error) {
	return s.c.getObject(ctx, "/v1/reports/labels", opts.values())
}

// ThemeOptions selects which clustered-themes batch to read.
type ThemeOptions struct {
	// Date selects ONE batch date as "YYYY-MM-DD" — it is not a range.
	// Omitted means the newest batch. `days`, `from` and `to` are not read
	// on this route.
	//
	// It is unvalidated server-side and bound straight into a date column,
	// so "yesterday" is a 500 rather than a 400. This method refuses
	// anything that is not 10 characters of YYYY-MM-DD shape.
	Date string
}

func (o *ThemeOptions) values() url.Values {
	q := url.Values{}
	if o != nil {
		setIf(q, "date", o.Date)
	}
	return q
}

// Themes returns the latest clustered-conversation themes.
//
// GET /v1/reports/themes — scope reports:read. An object,
// `{ batchDate, themes }`.
//
// `batchDate: null` WITH AN EMPTY LIST IS A NORMAL 200: no batch has ever
// been generated for this workspace. It is also what an explicit Date with no
// batch returns, so the two are indistinguishable — and a brand-new
// workspace, or one with no conversation embeddings, legitimately sits in
// that state for up to a day, because the batch is produced by a scheduled
// worker job every 24 hours rather than on read. A batch is a SNAPSHOT:
// re-running a date that already has one is a no-op, never a replacement.
//
// Caps per batch: 20 themes over 5,000 conversations, clustered on a 7-day
// window. A theme's `summary` is null when the naming model call failed — the
// theme is still real and still sized — and `sentimentAvg` is -1..1 and null
// when no member has a score.
//
// A theme row deliberately omits its conversation ids; ThemeConversations is
// the drill-down.
func (s *ReportsService) Themes(ctx context.Context, opts *ThemeOptions) (Object, error) {
	if opts != nil && opts.Date != "" && !isCalendarDate(opts.Date) {
		return nil, errors.New("chatly: Reports.Themes: date must be YYYY-MM-DD (the API binds it straight into a date column)")
	}
	return s.c.getObject(ctx, "/v1/reports/themes", opts.values())
}

// ThemeConversations returns the conversation ids clustered into one theme.
//
// GET /v1/reports/themes/{id}/conversations — scope reports:read. An object
// with a single key, `{ conversationIds }`.
//
// The id is a THEME id, not a conversation id. The route takes no query
// parameters at all — no cursor, no limit — and the batch cap of 5,000 is the
// only bound, so this can be 5,000 ids in one response.
//
// A well-formed but unknown or purged theme id answers 200 WITH AN EMPTY LIST
// rather than a 404, so a caller cannot tell "theme gone" from "theme with no
// conversations". The id is not validated either, so a non-UUID is a 500.
//
// The ids are a snapshot of the batch moment: a conversation deleted since may
// still be listed.
func (s *ReportsService) ThemeConversations(ctx context.Context, themeID string) (Object, error) {
	if themeID == "" {
		return nil, errors.New("chatly: Reports.ThemeConversations: empty theme id")
	}
	path := "/v1/reports/themes/" + pathEscape(themeID) + "/conversations"
	return s.c.getObject(ctx, path, nil)
}

// isCalendarDate reports whether s is exactly YYYY-MM-DD.
//
// Cheap and local rather than a time.Parse, because the only thing the server
// needs is a shape its date column will accept; anything else 500s there
// instead of 400ing, which is the failure this guards.
func isCalendarDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, c := range s {
		if i == 4 || i == 7 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
