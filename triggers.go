package chatly

import (
	"context"
	"errors"
)

// TriggersService is /v1/triggers — the rule engine that reacts to an event
// with a set of actions. Reach it as client.Triggers.
//
// Reads want triggers:read, writes triggers:write.
type TriggersService struct{ c *Client }

// List returns one page of triggers.
//
// GET /v1/triggers — scope triggers:read. Speaks the generic list grammar and
// nothing else: there are no trigger-specific parameters, so narrowing goes
// through Filter as `field:value` or `field:op:value` over enabled,
// eventType, brandId, createdAt and updatedAt. Sortable on id, name,
// priority, createdAt, updatedAt.
//
// A limit outside 1..100 is a 400 rather than a clamp, and an unknown sort or
// filter field is a 400 too — but a malformed CURSOR is silently treated as
// page one. Default order is createdAt descending, deliberately not id: this
// table's ids are a mix of v4 and v7.
//
// Items are projected when Fields is set, so every key may be absent. Note
// also that `op` and `actions[].type` are plain strings on READ even though
// the writes enforce enums: pre-enum rows are still in the table.
func (s *TriggersService) List(ctx context.Context, opts *ListOptions) (*Page[Object], error) {
	return s.c.getPage(ctx, "/v1/triggers", opts.values())
}

// ListAll walks every page of triggers.
func (s *TriggersService) ListAll(opts *ListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/triggers", opts.values())
}

// TriggerCondition is one clause that must hold for a trigger to fire.
type TriggerCondition struct {
	Field string `json:"field"`
	// Op is eq, neq, in, gt, lt, contains or matches.
	Op string `json:"op"`
	// Value is compared against the resolved field. Any JSON type.
	Value any `json:"value,omitempty"`
}

// TriggerAction is one thing a trigger does when it fires.
type TriggerAction struct {
	// Type names an action in the API's trigger-action catalogue (about 60
	// of them). An unknown type is a 400.
	Type string `json:"type"`
	// Params are that action's own arguments, validated per action
	// server-side — hence a free map rather than a union this package would
	// have to track.
	Params map[string]any `json:"params"`
}

// TriggerInput is the body of Create, which is an UPSERT.
//
// Send the WHOLE object every time. The update arm is a full replace rather
// than a patch: it SETs every settable column from the body, so an omitted
// DebounceSeconds is written as null and an omitted Priority as 0.
type TriggerInput struct {
	// ID present means UPDATE, absent means INSERT. Both answer 201.
	ID string `json:"id,omitempty"`

	// Name, 1..160 chars, required.
	Name string `json:"name,omitempty"`

	// EventType is REQUIRED and is a free string (1..64 chars) on the wire —
	// it is NOT checked against the workflow trigger catalogue. A typo is
	// therefore accepted, stored, and never fires, because the dispatcher
	// matches the name by literal string equality. Take the name from the
	// catalogue rather than typing it.
	EventType string `json:"eventType,omitempty"`

	// Enabled is a REQUIRED KEY with no default — omitting it is a 400 —
	// which is why it is a pointer rather than a bool.
	//
	// (The trigger schema's own docblock claims this field is vestigial and
	// the dispatcher ignores it. That comment is wrong: the worker filters
	// on enabled = true.)
	Enabled *bool `json:"enabled"`

	// Conditions is a REQUIRED KEY, max 50 clauses. An empty slice is legal
	// and means "always"; absent is a 400. It is marshalled as `[]` rather
	// than null for exactly that reason.
	Conditions []TriggerCondition `json:"conditions"`

	// Actions is REQUIRED, at least one, max 20.
	Actions []TriggerAction `json:"actions"`

	// Priority defaults to 0.
	Priority int `json:"priority,omitempty"`

	// DebounceSeconds collapses repeat firings, max 604800 (7 days).
	DebounceSeconds int `json:"debounceSeconds,omitempty"`

	// DebounceKeyTemplate scopes the debounce to something narrower than the
	// trigger, max 256 chars.
	DebounceKeyTemplate string `json:"debounceKeyTemplate,omitempty"`
}

// Create inserts or updates a trigger and returns the row.
//
// POST /v1/triggers — scope triggers:write. AN UPSERT: with ID it UPDATEs,
// without one it INSERTs, and BOTH ARMS ANSWER 201 — there is no @HttpCode on
// the handler, so do not read the status as "created".
//
// `brandId` is never written by either arm, so a trigger created through the
// API is always workspace-level; there is no way to attach it to a brand from
// here. An ID naming another workspace's trigger is a 404.
func (s *TriggersService) Create(ctx context.Context, in TriggerInput) (Object, error) {
	if in.Name == "" || in.EventType == "" {
		return nil, errors.New("chatly: Triggers.Create: name and eventType are required")
	}
	if in.Enabled == nil {
		return nil, errors.New("chatly: Triggers.Create: enabled is required (the API has no default; use chatly.Bool)")
	}
	if len(in.Actions) == 0 {
		return nil, errors.New("chatly: Triggers.Create: at least one action is required")
	}
	// Required KEYS, not required values: the schema wants the arrays
	// present, and nil would marshal as null and fail validation.
	if in.Conditions == nil {
		in.Conditions = []TriggerCondition{}
	}
	return s.c.writeObject(ctx, "POST", "/v1/triggers", nil, in)
}

// Delete removes a trigger.
//
// DELETE /v1/triggers/{id} — scope triggers:write. Answers 200 with
// `{ ok: true }`, not a 204, and `ok` can only ever be true: a missing row is
// a 404 instead.
//
// A HARD delete, unlike Brands.Delete — there is no row left afterwards.
func (s *TriggersService) Delete(ctx context.Context, triggerID string) (Object, error) {
	if triggerID == "" {
		return nil, errors.New("chatly: Triggers.Delete: empty trigger id")
	}
	return s.c.writeObject(ctx, "DELETE", "/v1/triggers/"+pathEscape(triggerID), nil, nil)
}
