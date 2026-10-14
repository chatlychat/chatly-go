package chatly

import (
	"context"
	"errors"
	"net/url"
)

// MacrosService is the /v1/macros surface — saved replies and the conversation
// actions attached to them. Reach it as client.Macros.
type MacrosService struct{ c *Client }

// MacroListOptions are the query parameters GET /v1/macros accepts.
//
// There is no cursor, limit or filter on this route: it returns every macro
// the caller can see. Note the two parameter names that are not what you
// would guess — Query is `query`, not `q`, and Team is `team`, not `teamId`.
type MacroListOptions struct {
	// Scope: workspace, team or personal.
	Scope string
	// Team is a team id (sent as `team`).
	Team string
	// Query is a free-text match on name and body (sent as `query`).
	Query string
	// Language is a 2..8 char language code.
	Language string
}

func (o *MacroListOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	setIf(q, "scope", o.Scope)
	setIf(q, "team", o.Team)
	setIf(q, "query", o.Query)
	setIf(q, "language", o.Language)
	return q
}

// List returns the macros visible to this credential.
//
// GET /v1/macros — scope macros:read. A BARE ARRAY, not a page. Each item is
// { id, workspaceId, name, body, actions, shortCode, scope, teamId, userId,
// language, tags, usageCount, createdByUserId, createdAt, updatedAt }.
//
// There is no ListAll, because the route is @PaginationExempt and sends no
// cursor — there is nothing for a pager to walk. What it does instead is
// TRUNCATE: the service caps the result at 200 rows, ordered by usage rank.
// So on a workspace with more than 200 macros this is the top 200, not the
// library, and there is no way to reach the rest through this route. Narrow
// with Scope, Team, Query or Language rather than expecting completeness.
//
// The shared SDK manifest claimed this route was paged for a day. Two
// sibling SDKs modelled it as {items, nextCursor} and raised a decode error
// on every real call while their own suites stayed green, because their fake
// transports answered every route with a page envelope. This package decodes
// into a slice and rejects an envelope, and its fixture for this route is a
// bare array — see TestMacrosListIsABareArrayNotAPage.
func (s *MacrosService) List(ctx context.Context, opts *MacroListOptions) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/macros", opts.values())
}

// MacroAction is one conversation action a macro performs when applied —
// assigning, tagging, closing and so on.
//
// Key must name an action in the API's CONVERSATION_ACTIONS registry; an
// unknown key is a 400. Params is that action's own arguments, validated
// server-side, which is why it is a free map rather than a union this package
// would have to track.
type MacroAction struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

// MacroInput is the body of Create and Update.
//
// A macro must DO something: the API rejects one with neither a Body nor at
// least one Action. On Update that rule is only enforced when both fields are
// present in the same request.
type MacroInput struct {
	// Name, 1..200 chars. Required on Create.
	Name string `json:"name,omitempty"`
	// Body is the reply text, max 20000 chars.
	Body string `json:"body,omitempty"`
	// Actions, max 16.
	Actions []MacroAction `json:"actions,omitempty"`
	// ShortCode is the "/" trigger an agent types, max 65 chars, matching
	// ^/?[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$.
	ShortCode string `json:"shortCode,omitempty"`
	// Scope is required on Create: workspace, team or personal.
	Scope string `json:"scope,omitempty"`
	// TeamID is REQUIRED when Scope is "team" and meaningless otherwise.
	TeamID string `json:"teamId,omitempty"`
	// Language, 2..8 chars. The API defaults it to "en".
	Language string `json:"language,omitempty"`
	// Tags, each 1..40 chars, max 20.
	Tags []string `json:"tags,omitempty"`
}

// Create adds a macro and returns it.
//
// POST /v1/macros — scope macros:write. Name and Scope are required; so is
// Body or at least one Action.
func (s *MacrosService) Create(ctx context.Context, in MacroInput) (Object, error) {
	if in.Name == "" {
		return nil, errors.New("chatly: Macros.Create: name is required")
	}
	if in.Scope == "" {
		return nil, errors.New("chatly: Macros.Create: scope is required (workspace, team or personal)")
	}
	if in.Scope == "team" && in.TeamID == "" {
		return nil, errors.New("chatly: Macros.Create: teamId is required when scope is \"team\"")
	}
	return s.c.writeObject(ctx, "POST", "/v1/macros", nil, in)
}

// Update patches a macro and returns it.
//
// PATCH /v1/macros/{id} — scope macros:write. Every field is optional and an
// omitted one is left alone.
//
// A PATCH here REPLACES the whole of any field it does mention: sending
// Tags: []string{"billing"} sets the tag list to exactly that, it does not
// append. Same for Actions. To clear ShortCode you need an explicit null,
// which omitempty cannot send — use Client.Request for that.
func (s *MacrosService) Update(ctx context.Context, macroID string, in MacroInput) (Object, error) {
	if macroID == "" {
		return nil, errors.New("chatly: Macros.Update: empty macro id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/macros/"+pathEscape(macroID), nil, in)
}

// Delete removes a macro.
//
// DELETE /v1/macros/{id} — scope macros:write. Answers 204 with no body, so
// there is nothing to return; a missing macro is ErrNotFound.
func (s *MacrosService) Delete(ctx context.Context, macroID string) error {
	if macroID == "" {
		return errors.New("chatly: Macros.Delete: empty macro id")
	}
	return s.c.RequestInto(ctx, "DELETE", "/v1/macros/"+pathEscape(macroID), nil, nil, nil)
}

// Get returns one macro by id.
//
// GET /v1/macros/{id} — scope macros:read. The id is parsed as a UUID inside
// the handler, so a v4 id or a slug is a 400 here rather than a 404.
//
// One thing the list does not do: THERE IS NO VISIBILITY CLAUSE on this
// route. The query filters on id and workspace only, so a `personal`-scope
// macro belonging to another agent IS readable by id even though List would
// never show it to that caller. Treat a macro id as workspace-wide
// information.
//
// `shortCode` is nullable and is stored without the leading slash — the write
// paths strip it.
func (s *MacrosService) Get(ctx context.Context, macroID string) (Object, error) {
	if macroID == "" {
		return nil, errors.New("chatly: Macros.Get: empty macro id")
	}
	return s.c.getObject(ctx, "/v1/macros/"+pathEscape(macroID), nil)
}

// Actions returns the catalogue of actions a macro may reference.
//
// GET /v1/macros/actions — scope macros:read. An OBJECT, `{ limit, actions }`,
// not the bare array the route name suggests: reaching for [0] on this
// response gets nothing.
//
// The ceiling travels with the vocabulary on purpose. `limit` is the maximum
// number of actions ONE macro may carry (16 today, typed as a number) and a
// caller needs it to validate a macro before posting it; `actions` is about a
// dozen entries of `{ key, label, description, permission }`.
//
// Each action's `permission` is what is needed to RUN it, not to list it — so
// a key can read actions it may not execute.
//
// Worth knowing when you write one: a macro referencing an action the
// registry no longer has throws 422, not 400, from both Create and Update.
func (s *MacrosService) Actions(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/macros/actions", nil)
}

// Templates returns the starter-template catalogue.
//
// GET /v1/macros/templates — scope macros:read. An object, `{ items, total }`,
// and deliberately NOT a page: there is no nextCursor and no hasMore, so a
// client that decodes it as a cursor envelope gets an empty walk. `total`
// equals len(items), because the list is the whole list — a frozen
// in-process catalogue with no cursor, ever.
//
// The handler reads NO query parameters: there is no category, language or
// tag filter, so narrow it client-side. `category` is one of greeting,
// acknowledgement, resolution, billing, support, product, lifecycle or
// compliance.
//
// A row is the LITE shape: `bodyPreview` and no `body`, no actions, no short
// code. Fetch Template for those.
func (s *MacrosService) Templates(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/macros/templates", nil)
}

// Template returns one starter template BY SLUG.
//
// GET /v1/macros/templates/{slug} — scope macros:read. The slug is not
// checked against the catalogue's enum, deliberately, so an unknown one is a
// 404 rather than a 400.
//
// `{ slug, name, body, category, language, tags }` plus `actions` and
// `shortCode`, which are ABSENT KEYS rather than nulls when the template has
// neither — most do not. Model them as optional, not nullable.
func (s *MacrosService) Template(ctx context.Context, slug string) (Object, error) {
	if slug == "" {
		return nil, errors.New("chatly: Macros.Template: empty template slug")
	}
	return s.c.getObject(ctx, "/v1/macros/templates/"+pathEscape(slug), nil)
}
