package chatly

import (
	"context"
	"errors"
	"net/url"
)

// WorkspaceService is the assorted workspace-level reads: settings, labels,
// inboxes, teams and agents. Reach it as client.Workspace.
//
// They live together because each is a single endpoint that configures or
// describes the workspace itself rather than a resource you act on.
type WorkspaceService struct{ c *Client }

// Settings returns the workspace's settings document.
//
// GET /v1/workspace/settings — scope workspace:read. Every key is always
// present: { autoTagging: { enabled, taxonomy }, dlqRetentionDays,
// aiSimilarityThreshold, notifications: {...}, robotsTxt,
// accountFitScorecard: {...}, onboarding: {...},
// privacy: { visitorReadReceipts }, quickReactions }.
//
// There is no key-accessible WRITE for this. Changing a setting is a
// dashboard action by a person, so this method is read-only by design and not
// an omission.
func (s *WorkspaceService) Settings(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/workspace/settings", nil)
}

// LabelOptions is the one parameter GET /v1/labels takes.
type LabelOptions struct {
	// WithUsage adds per-label counts to each row. It CHANGES THE RESPONSE
	// SHAPE rather than adding an optional field: with it, each item gains
	// `conversations`, `contacts` and `registered`.
	//
	// It also makes the list longer. Labels that are in use but were never
	// registered appear with registered=false, an EMPTY id, empty timestamps
	// and a placeholder colour — so code that keys rows by id will collide
	// on "" if it does not filter them out.
	WithUsage bool
}

func (o *LabelOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	if o.WithUsage {
		// The route accepts only "0" or "1" here; "true" is a 400.
		q.Set("usage", "1")
	}
	return q
}

// Labels lists the workspace's conversation labels.
//
// GET /v1/labels — scope conversations:read (not a labels scope). A BARE
// ARRAY of { id, name, description, color, showInSidebar, createdAt,
// updatedAt }, plus the usage fields when WithUsage is set.
func (s *WorkspaceService) Labels(ctx context.Context, opts *LabelOptions) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/labels", opts.values())
}

// Inboxes lists the workspace's inboxes and their routing configuration.
//
// GET /v1/inboxes — scope channels:read. A BARE ARRAY of { channelId, name,
// type, enabled, settings }, where settings carries the routing strategy,
// capacity limits, skills rules, sticky-assignment window, CSAT mode and
// business hours.
//
// An inbox is one per channel, so channelId is the key, and `settings` comes
// from a LEFT JOIN — expect it to be null on a channel nobody has configured
// routing for.
func (s *WorkspaceService) Inboxes(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/inboxes", nil)
}

// TeamMembers lists the members of one team.
//
// GET /v1/teams/{id}/members — scope members:read. A BARE ARRAY of
// { teamMemberId, userId, name, email, avatarUrl, role, joinedAt }.
//
// Deliberately not paginated: a team roster is a subset of the workspace's
// seats and is read as one panel list.
//
// Two things the shape is easy to misread. `role` is the member's role in
// THIS TEAM (`team_members.role`), not their workspace membership role — the
// same person can be a lead here and a plain agent on the roster. And the
// list is NOT joined against workspace memberships, so a member whose
// workspace seat has been revoked still appears; that is deliberate, since
// the stale row is exactly what an operator opens this list to remove. Do
// not treat presence in this list as proof of an active seat.
//
// The parent collection, GET /v1/teams, is not key-accessible (its handler
// reads a calling user id), so there is no Teams() here. To discover team
// ids from a key, read them off the objects that reference one — a
// conversation's `teamId`, a ticket queue's, an inbox's routing settings.
func (s *WorkspaceService) TeamMembers(ctx context.Context, teamID string) ([]Object, error) {
	if teamID == "" {
		return nil, errors.New("chatly: Workspace.TeamMembers: empty team id")
	}
	return s.c.getObjects(ctx, "/v1/teams/"+pathEscape(teamID)+"/members", nil)
}

// AgentSkills returns the workspace's skill taxonomy.
//
// GET /v1/agents/skills — scope members:read. An OBJECT, not an array:
// { "skills": ["billing", "german", ...] }, distinct labels in alphabetical
// order. An empty list is the normal state for a workspace that has not set
// any up, not an error.
//
// These are the labels skill-based inbox routing matches on. Reading an
// individual agent's skills (GET /v1/agents/{userId}/skills) and writing them
// are not key-accessible — they identify a person — so this is the whole
// key-visible surface of that feature.
func (s *WorkspaceService) AgentSkills(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/agents/skills", nil)
}
