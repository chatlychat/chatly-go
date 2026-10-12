package chatly

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ConversationsService is the /v1/conversations surface, including messages,
// agent handling, generated replies and scheduled sends. Reach it as
// client.Conversations.
type ConversationsService struct{ c *Client }

// ConversationListOptions are the query parameters GET /v1/conversations
// accepts. All are optional and a nil *ConversationListOptions is fine.
//
// Two things to know about how this route reads them:
//
//   - The multi-valued ones (Status, Channel, Tags, Priority) are
//     COMMA-JOINED into a single parameter, not repeated. That is this
//     route's own grammar; `filter[]`-style repetition belongs to the generic
//     list endpoints.
//   - An unrecognised token is DROPPED, not rejected. `Status: []string{"opne"}`
//     returns 200 and every conversation, so a typo looks like a wide result
//     rather than an error. Filter is the only parameter here that can 400.
type ConversationListOptions struct {
	// Status: open, pending, closed, snoozed… Comma-joined.
	Status []string
	// Assignee is an agent user id. The literal "unassigned" is accepted by
	// the API for the unassigned queue.
	Assignee string
	// Unread, when true, sends the flag. These four are presence flags: the
	// API treats `?unread` and `?unread=true` as on and ANY other value as
	// off, so there is no way to say "only read conversations" here.
	Unread bool
	// Mentioned restricts to conversations mentioning the calling identity.
	// Of limited use to an API key, which is not a person.
	Mentioned bool
	// Channel types: web, email, whatsapp, sms… Comma-joined.
	Channel []string
	// Tags, comma-joined.
	Tags []string
	// From and To bound last activity. Sent as RFC3339.
	From time.Time
	To   time.Time
	// Segment is a saved-segment id.
	Segment string
	// Priority: low, normal, high, urgent. Comma-joined.
	Priority []string
	// Team is a team id (must be a UUIDv7 or it is dropped).
	Team string
	// CreatedFrom and CreatedTo bound creation time. Sent as RFC3339.
	CreatedFrom time.Time
	CreatedTo   time.Time
	// Unattended restricts to conversations waiting on a human.
	Unattended bool
	// UnattendedFor is a threshold in MINUTES, capped at 10080 (7 days).
	UnattendedFor int
	// SLA restricts to conversations with an SLA breach or warning.
	SLA bool
	// Filter is a JSON filter tree. When present it SUPERSEDES every flat
	// parameter above, and it is the only one that can return a 400.
	Filter string
	// Brand and Sort are passed through; unrecognised values are dropped.
	Brand string
	Sort  string

	Cursor string
	// Limit is 1..100; the API defaults to 25.
	Limit int

	Params url.Values
}

func (o *ConversationListOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	for k, vs := range o.Params {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	setCSV(q, "status", o.Status)
	setCSV(q, "channel", o.Channel)
	setCSV(q, "tags", o.Tags)
	setCSV(q, "priority", o.Priority)
	setIf(q, "assignee", o.Assignee)
	setIf(q, "segment", o.Segment)
	setIf(q, "team", o.Team)
	setIf(q, "filter", o.Filter)
	setIf(q, "brand", o.Brand)
	setIf(q, "sort", o.Sort)
	setIf(q, "cursor", o.Cursor)
	setFlag(q, "unread", o.Unread)
	setFlag(q, "mentioned", o.Mentioned)
	setFlag(q, "unattended", o.Unattended)
	setFlag(q, "sla", o.SLA)
	setTime(q, "from", o.From)
	setTime(q, "to", o.To)
	setTime(q, "createdFrom", o.CreatedFrom)
	setTime(q, "createdTo", o.CreatedTo)
	if o.UnattendedFor > 0 {
		q.Set("unattendedFor", strconv.Itoa(o.UnattendedFor))
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	return q
}

func setIf(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

func setCSV(q url.Values, key string, values []string) {
	if len(values) > 0 {
		q.Set(key, strings.Join(values, ","))
	}
}

func setFlag(q url.Values, key string, on bool) {
	if on {
		q.Set(key, "true")
	}
}

func setTime(q url.Values, key string, t time.Time) {
	if !t.IsZero() {
		q.Set(key, t.UTC().Format(time.RFC3339))
	}
}

// List returns one page of conversations.
//
// GET /v1/conversations — scope conversations:read (or
// conversations:read:assigned).
func (s *ConversationsService) List(ctx context.Context, opts *ConversationListOptions) (*Page[Object], error) {
	return s.c.getPage(ctx, "/v1/conversations", opts.values())
}

// ListAll walks every page of conversations matching the options.
func (s *ConversationsService) ListAll(opts *ConversationListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/conversations", opts.values())
}

// Get returns one conversation.
//
// GET /v1/conversations/{id} — ErrNotFound when the id is outside this
// workspace, which is also what a wrong-workspace id looks like: the API does
// not distinguish "does not exist" from "not yours", on purpose.
func (s *ConversationsService) Get(ctx context.Context, conversationID string) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.Get: empty conversation id")
	}
	return s.c.getObject(ctx, "/v1/conversations/"+pathEscape(conversationID), nil)
}

// Messages returns one page of a conversation's messages.
//
// GET /v1/conversations/{id}/messages — scope messages:read. Note the page
// size default here is 50, not the 25 most list routes use.
//
// Read the thread before replying to it: an integration that answers without
// looking answers the wrong message surprisingly often.
func (s *ConversationsService) Messages(ctx context.Context, conversationID string, opts *ListOptions) (*Page[Object], error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.Messages: empty conversation id")
	}
	return s.c.getPage(ctx, "/v1/conversations/"+pathEscape(conversationID)+"/messages", opts.values())
}

// MessagesAll walks every page of a conversation's messages.
func (s *ConversationsService) MessagesAll(conversationID string, opts *ListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/conversations/"+pathEscape(conversationID)+"/messages", opts.values())
}

// MessageAttachment is one file on a message. Every field is required by the
// API; the file must already have been uploaded, because this route takes
// metadata, not bytes.
type MessageAttachment struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	URL       string `json:"url"`
}

// SendMessageInput is the body of Send.
type SendMessageInput struct {
	// Body is the message text, up to 10000 chars. Required — it may be ""
	// only when Attachments or BodyHTML carry the content.
	Body string `json:"body"`

	// Kind defaults to "text" when empty. One of: text, rich, note,
	// system_event, attachment_only. "note" is an INTERNAL note — it is
	// stored on the conversation and never delivered to the contact, which
	// makes it the safe kind for an integration leaving context behind.
	Kind string `json:"kind,omitempty"`

	// Attachments, max 10. Metadata for files already uploaded.
	Attachments []MessageAttachment `json:"attachments,omitempty"`

	// Metadata is free-form data stored with the message, not shown to the
	// contact.
	Metadata map[string]any `json:"metadata,omitempty"`

	// BodyHTML, max 40000 chars, is sanitised server-side and the plain Body
	// is DERIVED from it — a Body you send alongside it is ignored.
	BodyHTML string `json:"bodyHtml,omitempty"`

	// Blocks and Canvas are the rich widget surfaces. They are mutually
	// exclusive: sending both is a 400. Shapes are validated server-side
	// against the authoritative schema, so they are plain values here rather
	// than types this package would have to keep in step.
	Blocks any `json:"blocks,omitempty"`
	Canvas any `json:"canvas,omitempty"`

	// ClientRef is YOUR dedupe key for this message, 1..128 chars, unique
	// per conversation: a repeat returns the existing message instead of
	// posting a second one.
	//
	// It is not the same thing as an Idempotency-Key and it is better for
	// this job. The idempotency key dedupes an HTTP request for ~24h;
	// ClientRef dedupes the MESSAGE for the life of the conversation, so a
	// job that re-runs tomorrow still will not double-post.
	ClientRef string `json:"clientRef,omitempty"`

	// ReplyToMessageID threads this message under another one on the same
	// conversation.
	ReplyToMessageID string `json:"replyToMessageId,omitempty"`
}

// Send posts a message to a conversation and returns the created message.
//
// POST /v1/conversations/{id}/messages — scope messages:write.
//
// The conversation id comes from the path argument. SendMessageInput has no
// conversationId field for that reason: the API ignores a body one and uses
// the path, and offering both is how a caller ends up posting to the wrong
// thread.
func (s *ConversationsService) Send(ctx context.Context, conversationID string, in SendMessageInput) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.Send: empty conversation id")
	}
	path := "/v1/conversations/" + pathEscape(conversationID) + "/messages"
	return s.c.writeObject(ctx, "POST", path, nil, in)
}

// UnattendedOptions are the query parameters GET /v1/conversations/unattended
// accepts. They are NOT the same set as the main list route.
type UnattendedOptions struct {
	// Assignee is an agent user id.
	Assignee string
	// Team must be a UUIDv7 or it is dropped.
	Team string
	// Channel is a SINGLE channel type here, not a list.
	Channel string
	// OlderThanMinutes is the waiting threshold, capped at 10080 (7 days).
	OlderThanMinutes int
	Cursor           string
	Limit            int
}

func (o *UnattendedOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	setIf(q, "assignee", o.Assignee)
	setIf(q, "team", o.Team)
	setIf(q, "channel", o.Channel)
	setIf(q, "cursor", o.Cursor)
	if o.OlderThanMinutes > 0 {
		q.Set("olderThanMinutes", strconv.Itoa(o.OlderThanMinutes))
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	return q
}

// Unattended lists conversations waiting on a human reply — the queue an
// escalation or alerting integration watches.
//
// GET /v1/conversations/unattended — scope conversations:read. The response
// is {items, nextCursor} plus a `total` that ignores cursor and limit, so it
// is the whole backlog size rather than this page's.
//
// This returns the whole object rather than a Page so that `total` survives.
// It does paginate: see UnattendedAll to walk the cursor.
func (s *ConversationsService) Unattended(ctx context.Context, opts *UnattendedOptions) (Object, error) {
	return s.c.getObject(ctx, "/v1/conversations/unattended", opts.values())
}

// UnattendedAll walks every page of the unattended backlog.
//
// It yields conversations and discards the `total` that sits beside them in
// each page — use Unattended if you need the backlog size, which is the
// number a dashboard or an alert threshold actually wants. The two together
// are the usual shape: read Unattended once for the figure, then walk this
// when you intend to act on every row.
func (s *ConversationsService) UnattendedAll(opts *UnattendedOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/conversations/unattended", opts.values())
}

// AgentHandling reports who or what is currently handling a conversation.
//
// GET /v1/conversations/{id}/agent-handling — scope conversations:read.
// Returns { conversationId, handling: { state, actorUserId, reason,
// updatedAt }, escalated, status }. `escalated` is always false on the read.
func (s *ConversationsService) AgentHandling(ctx context.Context, conversationID string) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.AgentHandling: empty conversation id")
	}
	return s.c.getObject(ctx, "/v1/conversations/"+pathEscape(conversationID)+"/agent-handling", nil)
}

// SetAgentHandlingInput is the body of SetAgentHandling.
type SetAgentHandlingInput struct {
	// State is required: "in_progress", "handed_off" or "handled".
	// "taken_over" is NOT settable through this route — a human taking over
	// is recorded by the dashboard, not claimed by a bot.
	State string `json:"state"`
	// Reason is optional, max 2000 chars. Explicit null is legal; omit it
	// here to send nothing.
	Reason string `json:"reason,omitempty"`
}

// SetAgentHandling declares that your bot has picked up, handed off, or
// finished a conversation.
//
// PUT /v1/conversations/{id}/agent-handling — scope conversations:write.
//
// TWO constraints worth knowing before you build on it:
//
//   - The key must be linked to a MACHINE USER. A key without one gets 403
//     even with conversations:write, because the route records which
//     non-human actor is handling the thread. Link the key to a machine user
//     in the dashboard; ErrForbidden here with a working Whoami is almost
//     always this.
//   - Its controller does NOT require an Idempotency-Key. This SDK sends one
//     anyway — it is harmless, and a write without one is a write a retry can
//     duplicate.
func (s *ConversationsService) SetAgentHandling(ctx context.Context, conversationID string, in SetAgentHandlingInput) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.SetAgentHandling: empty conversation id")
	}
	if in.State == "" {
		return nil, errors.New("chatly: Conversations.SetAgentHandling: state is required")
	}
	path := "/v1/conversations/" + pathEscape(conversationID) + "/agent-handling"
	return s.c.writeObject(ctx, "PUT", path, nil, in)
}

// GeneratedReplies lists the replies proposed for a conversation and awaiting
// a human decision.
//
// GET /v1/conversations/{id}/generated-replies — scope conversations:read.
// Returns a BARE ARRAY (not a page): this route is pagination-exempt.
func (s *ConversationsService) GeneratedReplies(ctx context.Context, conversationID string) ([]Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.GeneratedReplies: empty conversation id")
	}
	return s.c.getObjects(ctx, "/v1/conversations/"+pathEscape(conversationID)+"/generated-replies", nil)
}

// GenerateReplyInput is the body of GenerateReply.
type GenerateReplyInput struct {
	// Body is the proposed reply text, 1..10000 chars, required.
	Body string `json:"body"`
	// BasedOnMessageID must be a CONTACT-authored message on this
	// conversation. It is what lets the dashboard show the proposal as stale
	// when the contact has since written again.
	BasedOnMessageID string `json:"basedOnMessageId,omitempty"`
	// ProposedByLabel, 1..160 chars, is the name a human sees next to the
	// proposal ("Acme Copilot"). Honoured only on the API-key path, which is
	// this one.
	ProposedByLabel string `json:"proposedByLabel,omitempty"`
}

// GenerateReply proposes a reply for a human to approve, edit or discard.
//
// POST /v1/conversations/{id}/generated-replies — scope messages:write.
//
// Despite the name this does NOT ask Chatly's AI for a draft: YOUR system
// supplies the text and this records it as a proposal. Nothing is delivered
// to the contact until a human sends it in the dashboard — and the send and
// discard routes are deliberately not key-accessible, so an integration can
// propose but never approve its own proposal.
func (s *ConversationsService) GenerateReply(ctx context.Context, conversationID string, in GenerateReplyInput) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.GenerateReply: empty conversation id")
	}
	if strings.TrimSpace(in.Body) == "" {
		return nil, errors.New("chatly: Conversations.GenerateReply: body is required")
	}
	path := "/v1/conversations/" + pathEscape(conversationID) + "/generated-replies"
	return s.c.writeObject(ctx, "POST", path, nil, in)
}

// ScheduledMessages lists messages queued to send later on a conversation.
//
// GET /v1/conversations/{id}/scheduled-messages — scope conversations:read.
// Returns a BARE ARRAY; pagination-exempt. Status is one of pending, sent,
// cancelled, failed.
func (s *ConversationsService) ScheduledMessages(ctx context.Context, conversationID string) ([]Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.ScheduledMessages: empty conversation id")
	}
	return s.c.getObjects(ctx, "/v1/conversations/"+pathEscape(conversationID)+"/scheduled-messages", nil)
}

// ScheduleMessageInput is the body of ScheduleMessage.
type ScheduleMessageInput struct {
	// Body is 1..10000 chars, required.
	Body string `json:"body"`
	// SendAt is an ABSOLUTE instant, not a delay. Serialised as RFC3339 in
	// UTC. A time in the past is the API's to accept or reject; this SDK
	// does not second-guess it.
	SendAt time.Time `json:"-"`
}

// MarshalJSON renders SendAt as the RFC3339 string the API's ISO-8601
// validator expects. A time.Time marshals to RFC3339Nano by default, which
// is also accepted — this is explicit so the wire form cannot drift with a
// struct-tag change, and so SendAt is always UTC rather than whatever zone
// the caller's machine is in.
func (in ScheduleMessageInput) MarshalJSON() ([]byte, error) {
	type wire struct {
		Body   string `json:"body"`
		SendAt string `json:"sendAt"`
	}
	return json.Marshal(wire{Body: in.Body, SendAt: in.SendAt.UTC().Format(time.RFC3339)})
}

// ScheduleMessage queues a message to be sent at a future instant.
//
// POST /v1/conversations/{id}/scheduled-messages — needs BOTH messages:write
// and conversations:write. A key with only one of them gets ErrForbidden.
func (s *ConversationsService) ScheduleMessage(ctx context.Context, conversationID string, in ScheduleMessageInput) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.ScheduleMessage: empty conversation id")
	}
	if in.SendAt.IsZero() {
		return nil, errors.New("chatly: Conversations.ScheduleMessage: sendAt is required (an absolute instant)")
	}
	path := "/v1/conversations/" + pathEscape(conversationID) + "/scheduled-messages"
	return s.c.writeObject(ctx, "POST", path, nil, in)
}

// CancelScheduled cancels a scheduled message and returns the row.
//
// DELETE /v1/conversations/scheduled-messages/{scheduledId} — scope
// conversations:write ONLY (not messages:write, deliberately: cancelling is
// not sending).
//
// Note the path: it hangs off /v1/conversations/scheduled-messages and takes
// the SCHEDULED MESSAGE id, not a conversation id. Cancelling one that has
// already sent is not an error — you get the row back with its real status,
// so check that rather than assuming a 2xx meant it was stopped.
func (s *ConversationsService) CancelScheduled(ctx context.Context, scheduledID string) (Object, error) {
	if scheduledID == "" {
		return nil, errors.New("chatly: Conversations.CancelScheduled: empty scheduled message id")
	}
	path := "/v1/conversations/scheduled-messages/" + pathEscape(scheduledID)
	return s.c.writeObject(ctx, "DELETE", path, nil, nil)
}
