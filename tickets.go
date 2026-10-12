package chatly

import (
	"context"
	"errors"
)

// TicketsService is the /v1/tickets surface. Reach it as client.Tickets.
//
// All of it sits under the conversations:read / conversations:write scopes,
// not a tickets scope of its own.
type TicketsService struct{ c *Client }

// List returns one page of tickets.
//
// GET /v1/tickets — scope conversations:read. This route speaks the generic
// list grammar, so ListOptions applies in full:
//
//	opts := &chatly.ListOptions{
//	    Limit:  50,
//	    Sort:   "-updatedAt",
//	    Filter: []string{"status:in:open,pending", "priority:urgent"},
//	}
//
// Sortable: id, createdAt, updatedAt, dueAt. Filterable: status
// (new|open|pending|on_hold|resolved|closed|merged), priority
// (low|normal|high|urgent), type (question|incident|problem|task),
// assigneeUserId, teamId, conversationId, queueId, splitFromTicketId.
// An unknown filter FIELD is a 400, unlike the conversations route.
func (s *TicketsService) List(ctx context.Context, opts *ListOptions) (*Page[Object], error) {
	return s.c.getPage(ctx, "/v1/tickets", opts.values())
}

// ListAll walks every page of tickets.
func (s *TicketsService) ListAll(opts *ListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/tickets", opts.values())
}

// Comments returns a ticket's comments, oldest first.
//
// GET /v1/tickets/{id}/comments — scope conversations:read. A BARE ARRAY,
// pagination-exempt, and SERVER-CAPPED AT 500: a ticket with more comments
// than that is silently truncated, with no cursor to continue from. Each
// item is { id, ticketId, body, isInternal, authorUserId, authorEmail,
// createdAt, editedAt }.
//
// isInternal separates agent-only notes from what the requester can see.
// Anything you surface to an end user must filter on it.
func (s *TicketsService) Comments(ctx context.Context, ticketID string) ([]Object, error) {
	if ticketID == "" {
		return nil, errors.New("chatly: Tickets.Comments: empty ticket id")
	}
	return s.c.getObjects(ctx, "/v1/tickets/"+pathEscape(ticketID)+"/comments", nil)
}

// ForConversation returns the tickets attached to a conversation.
//
// GET /v1/tickets/for-conversation/{conversationId} — scope
// conversations:read. A bare array of { id, number, title, status, priority,
// type, dueAt, assigneeUserId, isOrigin, conversationCount }.
//
// This is the lookup to make from a webhook: a conversation event gives you a
// conversation id, and this turns it into the ticket your helpdesk cares
// about. isOrigin marks the ticket the conversation created, as opposed to
// one it was later linked to.
func (s *TicketsService) ForConversation(ctx context.Context, conversationID string) ([]Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Tickets.ForConversation: empty conversation id")
	}
	return s.c.getObjects(ctx, "/v1/tickets/for-conversation/"+pathEscape(conversationID), nil)
}

// Queues lists the workspace's ticket queues.
//
// GET /v1/tickets/queues — scope conversations:read. A bare array of
// { id, workspaceId, createdAt, updatedAt, name, description, color,
// defaultAssigneeUserId, slaPolicyId }. Call it once and cache: queues are
// operator-configured and change rarely.
func (s *TicketsService) Queues(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/tickets/queues", nil)
}

// Merge folds loserID into winnerID.
//
// POST /v1/tickets/{id}/merge — scope conversations:write. The PATH id is the
// winner; the body id is absorbed, its conversations moved across, and its
// status set to `merged`. Returns { winnerId, loserId, movedConversations }.
//
// Not reversible. Check which way round you have them.
func (s *TicketsService) Merge(ctx context.Context, winnerID, loserID string) (Object, error) {
	if winnerID == "" || loserID == "" {
		return nil, errors.New("chatly: Tickets.Merge: both winnerID and loserID are required")
	}
	body := map[string]any{"loserId": loserID}
	return s.c.writeObject(ctx, "POST", "/v1/tickets/"+pathEscape(winnerID)+"/merge", nil, body)
}

// TicketSplitInput is the body of Split.
type TicketSplitInput struct {
	// Title is required, 1..280 chars, and is the new ticket's title.
	Title string `json:"title"`
	// Description is optional, max 10000 chars.
	Description string `json:"description,omitempty"`
	// Priority: low, normal, high, urgent. Omit to inherit from the origin.
	Priority string `json:"priority,omitempty"`
	// Type: question, incident, problem, task. Omit to inherit.
	Type string `json:"type,omitempty"`
	// AssigneeUserID omitted inherits the origin's assignee. To create the
	// child UNASSIGNED you need an explicit null, which this field cannot
	// express — use Client.Request for that case.
	AssigneeUserID string `json:"assigneeUserId,omitempty"`
	// Tags, each max 40 chars, max 20 tags.
	Tags []string `json:"tags,omitempty"`
	// ConversationIDs, max 100, are MOVED from the origin ticket to the new
	// one. Omit to split a ticket without moving any conversation.
	ConversationIDs []string `json:"conversationIds,omitempty"`
}

// Split carves a new ticket out of an existing one.
//
// POST /v1/tickets/{id}/split — scope conversations:write. Returns
// { originId, originNumber, childId, childNumber, movedConversations,
// sharedConversations }.
//
// The route takes no status, queueId or customFields: a split child starts
// fresh on those.
func (s *TicketsService) Split(ctx context.Context, ticketID string, in TicketSplitInput) (Object, error) {
	if ticketID == "" {
		return nil, errors.New("chatly: Tickets.Split: empty ticket id")
	}
	if in.Title == "" {
		return nil, errors.New("chatly: Tickets.Split: title is required")
	}
	return s.c.writeObject(ctx, "POST", "/v1/tickets/"+pathEscape(ticketID)+"/split", nil, in)
}

// TicketBulkAction is the one field that decides which Payload key matters.
type TicketBulkAction string

// The four bulk actions, and the Payload key each one requires.
const (
	// TicketBulkStatus requires Payload.Status (new, open, pending, on_hold,
	// resolved, closed — `merged` is not settable).
	TicketBulkStatus TicketBulkAction = "status"
	// TicketBulkAssign requires Payload.AssigneeUserID. Explicit null
	// unassigns, which this typed struct cannot express — see TicketBulkPayload.
	TicketBulkAssign TicketBulkAction = "assign"
	// TicketBulkPriority requires Payload.Priority.
	TicketBulkPriority TicketBulkAction = "priority"
	// TicketBulkQueue requires Payload.QueueID. Explicit null dequeues.
	TicketBulkQueue TicketBulkAction = "queue"
)

// TicketBulkPayload carries the value for the chosen action. Fill exactly the
// one the action needs; the others are ignored.
//
// To UNASSIGN or DEQUEUE you must send an explicit JSON null, which an
// omitempty string field cannot produce. Use Client.Request with
// map[string]any{"assigneeUserId": nil} for that, and see the README.
type TicketBulkPayload struct {
	Status         string `json:"status,omitempty"`
	AssigneeUserID string `json:"assigneeUserId,omitempty"`
	Priority       string `json:"priority,omitempty"`
	QueueID        string `json:"queueId,omitempty"`
}

// Bulk applies one action to up to 200 tickets.
//
// POST /v1/tickets/bulk — scope conversations:write. Returns
// { updated: number, skipped: [{ id, reason }] }.
//
// PARTIAL SUCCESS IS THE NORMAL CASE and arrives as 201, not an error: a
// ticket the caller cannot see, or one already in the target state, lands in
// `skipped` with a reason while the rest go through. Code that checks only
// the status code will report success for a request that changed nothing.
//
// This is also the one write on this controller that does NOT require an
// Idempotency-Key. The SDK still sends one.
func (s *TicketsService) Bulk(ctx context.Context, ids []string, action TicketBulkAction, payload TicketBulkPayload) (Object, error) {
	if len(ids) == 0 {
		return nil, errors.New("chatly: Tickets.Bulk: no ticket ids (the API requires 1..200)")
	}
	if action == "" {
		return nil, errors.New("chatly: Tickets.Bulk: action is required (status, assign, priority or queue)")
	}
	body := map[string]any{"ids": ids, "action": string(action), "payload": payload}
	return s.c.writeObject(ctx, "POST", "/v1/tickets/bulk", nil, body)
}
