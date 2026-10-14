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

// KeyScopes returns every scope an API key may be granted.
//
// GET /v1/api-keys/scopes — scope workspace:read. A bare array of
// `{ value, group }`, where group is the part of the name before the first
// colon. Static and workspace-independent — no database read, nothing about
// which keys exist — so cache it for the process lifetime.
//
// It is deliberately NARROWER than the full permission vocabulary: it is
// exactly the set of permissions with at least one key-accessible route
// behind them. It used to return all eighty, so a customer could mint a key
// scoped to something no route honours and be refused on every call. Note the
// asymmetry that remains: POST /v1/api-keys validates `scopes` as a bare
// string array, so the create route accepts names that never appear here.
//
// This is the SECOND of exactly two routes on the api-keys controller a key
// may call; Client.Whoami is the other. Managing keys with a key — listing,
// creating, revoking — stays closed, because that is a privilege-escalation
// shape, while a static vocabulary of permission names is not.
func (s *WorkspaceService) KeyScopes(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/api-keys/scopes", nil)
}

// Secrets enumerates every credential the workspace holds — existence and
// timestamps, never a value.
//
// GET /v1/workspace/secrets — and it needs BOTH workspace:write AND
// integrations:read, which the guard ANDs: a key with only one of them gets
// ErrForbidden. It is the one route on this surface requiring two
// permissions.
//
// `{ items, total, truncated }`, an object and NOT a page: `items` has no
// nextCursor beside it, so a client that pattern-matches on the key and
// synthesises a paginator is wrong about this route.
//
// A census across nine tables — api keys, channels, customer cards, webhook
// endpoints, bots, AI tools, inbound webhooks, docs sources and DKIM keys.
// Each item carries a stable id, its `source` table, a `kind`, the `location`
// the value sits at (including the dot path inside a JSON column, e.g.
// channels.config.identityVerification.secret), `owner`, `status`,
// timestamps, and the concrete `rotateRoute` where one call replaces it. It
// carries NO value, no prefix of one, no hash and no length — that rule is
// what makes this route key-accessible at all.
//
// Three fields decide whether the rest means anything:
//
//   - `truncated: true` means a source hit its 250-row cap AND THE CENSUS IS
//     INCOMPLETE. There is no cursor and no way to fetch the remainder, so
//     read it before concluding a credential does not exist.
//   - `rotatedAtBasis` and `lastUsedBasis` qualify the null timestamps.
//     "none" means NOTHING IN THE PRODUCT RECORDS THAT FACT — true of eight
//     of the nine sources for rotation — while "column" or "derived" means it
//     was looked up and the credential really has not been rotated or used.
//     Treating the first as "never used" is how a webhook secret that signs a
//     delivery every minute gets revoked.
//   - `rotateRoute: null` does not mean "cannot rotate"; it means rotation
//     takes more than one call (an API key is create-then-delete) or that
//     there is no rotate route at all, as for webhook endpoints.
//
// Two more distinctions worth keeping: `headerNames: null` is NOT the same as
// `[]` — null means the encrypted header bag could not be decrypted, so the
// credential is present and unusable, which is the state that needs action.
// And `status: "inactive"` still holds live credential material.
func (s *WorkspaceService) Secrets(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/workspace/secrets", nil)
}

// Mine lists the workspaces this credential can reach.
//
// GET /v1/workspaces/me — a BARE ARRAY of
// `{ id, name, slug, plan, role, createdAt }`, where `role` is the CALLER's
// role in that workspace rather than a property of the workspace.
//
// For a `ck_` key the array holds EXACTLY ONE entry: the workspace the key
// was minted in. That makes it the cheap way to confirm which tenant a key
// actually points at — a question worth answering before a script writes
// anything. It needs no permission at all, like Client.Me.
//
// `role` for a key is the SYNTHETIC "restricted" the auth layer mints for
// every key, not a stored membership role. That is the honest value: what a
// key may do is its scope set, which Client.Me answers, and reporting
// "owner" here because the person who created the key was one would overstate
// the credential.
//
// It also answers on a SUSPENDED workspace, where most of /v1/* does not,
// because this is how a caller discovers the others — for a user, the
// healthy workspace one click away; for a key, confirmation that the tenant
// it points at is the suspended one.
func (s *WorkspaceService) Mine(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/workspaces/me", nil)
}
