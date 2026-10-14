package chatly

import (
	"context"
	"errors"
)

// The outcome-reporting and bot-handoff half of ConversationsService: how a
// conversation ENDED, and handing a thread back to an assistant. Split out of
// conversations.go, which is at the file-size ceiling.
//
// These three routes exist FOR out-of-band callers. Nothing on this path
// records who the actor was — provenance is the literal string
// "agent_report" — which is exactly why an API key may drive them.

// The three outcomes RecordOutcome accepts.
const (
	// OutcomeResolved writes an ai_resolutions row with source
	// "agent_report": the same bucket the visitor's own "✓ This answered"
	// chip lands in, counted by the deflection funnel and by the
	// per-assistant funnel's resolvedByAi.
	OutcomeResolved = "resolved"
	// OutcomeEscalated is not merely a record — see RecordOutcome.
	OutcomeEscalated = "escalated"
	// OutcomePurchased writes a conversation.purchased event on the CONTACT,
	// which can complete a conversion goal and shows on the contact
	// timeline.
	OutcomePurchased = "purchased"
)

// ConversationOutcomeInput is the body of RecordOutcome. It is strict, so a
// misspelled key is a 400.
type ConversationOutcomeInput struct {
	// Outcome is one of the Outcome* constants. Required.
	Outcome string `json:"outcome"`

	// Reason, max 2000 chars. Required in practice on an escalation: it is
	// what the human picking the thread up reads.
	Reason string `json:"reason,omitempty"`

	// MessageID pins the outcome to a message — your own last reply,
	// usually.
	MessageID string `json:"messageId,omitempty"`

	// ValueCents is 0..1000000000 and is REFUSED on anything but
	// "purchased" — a 400, not a silently dropped field, because a caller
	// who believed a value was recorded is worse off than one who got an
	// error.
	ValueCents int `json:"valueCents,omitempty"`

	// Currency is ISO-4217, three letters, upper-cased by the schema.
	// Required whenever ValueCents is given, and refused on anything but
	// "purchased".
	Currency string `json:"currency,omitempty"`

	// OutcomeRef is your own order id, 1..200 chars. REQUIRED on
	// "purchased" and refused elsewhere. It is unique per workspace forever,
	// enforced by an index, and it is what makes a retry a no-op instead of
	// a second purchase — the only such guard on this path, which takes no
	// idempotency protection of its own beyond the header.
	OutcomeRef string `json:"outcomeRef,omitempty"`
}

// RecordOutcome reports how a conversation ended, so the AI funnels count it.
//
// POST /v1/conversations/{id}/outcomes — scope conversations:write, answers
// 201 with
// `{ outcome, conversationId, recordId, recordedIn, recordedAt, created,
// feeds }`.
//
// Each word is written where a report already looks, and `recordedIn` names
// the table it landed in: ai_resolutions, conversation_escalations or
// cdp_events.
//
// ESCALATED IS NOT JUST A RECORD. It moves the conversation to `pending`,
// releases bot ownership, and posts your Reason to the receiving human as an
// internal note — the same effect as the bot surface's handoff call, reported
// in outcome vocabulary.
//
// Three things to read in the response:
//
//   - `created: false` IS A SUCCESS. It means an equal outcome was already on
//     record and this call changed nothing: a replayed resolution inside 24h,
//     an escalation inside 60s, or a purchase whose OutcomeRef we already
//     hold.
//   - `recordId` is ALWAYS NULL for an escalation, because that service
//     returns no row id. Do not type it non-null.
//   - `feeds` is a human-readable sentence, not an identifier.
//
// And one thing about ValueCents: CHATLY HAS NO REVENUE REPORT. The value is
// stored on the event and summed by nothing. It is said here, in the
// response's own `feeds` field and in the guide, because the one wrong
// conclusion to draw is that this populates a revenue dashboard.
//
// Nothing here CLOSES the conversation. Reporting an outcome is reporting,
// not finishing.
func (s *ConversationsService) RecordOutcome(ctx context.Context, conversationID string, in ConversationOutcomeInput) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.RecordOutcome: empty conversation id")
	}
	if in.Outcome == "" {
		return nil, errors.New("chatly: Conversations.RecordOutcome: outcome is required (see the Outcome* constants)")
	}
	if in.Outcome == OutcomePurchased && in.OutcomeRef == "" {
		return nil, errors.New("chatly: Conversations.RecordOutcome: outcomeRef is required on a purchase (your own order id)")
	}
	if in.ValueCents > 0 && in.Currency == "" {
		return nil, errors.New("chatly: Conversations.RecordOutcome: currency is required whenever valueCents is sent")
	}
	path := "/v1/conversations/" + pathEscape(conversationID) + "/outcomes"
	return s.c.writeObject(ctx, "POST", path, nil, in)
}

// Outcomes returns what is already on record for one thread.
//
// GET /v1/conversations/{id}/outcomes — scope conversations:read.
// `{ conversationId, items }` — an object that CARRIES items and has no
// cursor, like Linked: read items yourself rather than pointing a paginator
// at it. Merged from three tables, newest first, each arm capped at 20.
//
// Every row carries `source`, and that is the field to read: it tells your
// own `agent_report` apart from the visitor's `visitor_chip`, and an
// escalation a human requested (`agent_request`) apart from one a bot gave up
// on (`bot_handoff`). It is a plain string rather than an enum.
//
// On this read `recordId` is non-null, unlike on the write.
func (s *ConversationsService) Outcomes(ctx context.Context, conversationID string) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.Outcomes: empty conversation id")
	}
	path := "/v1/conversations/" + pathEscape(conversationID) + "/outcomes"
	return s.c.getObject(ctx, path, nil)
}

// ReturnToBot hands an open thread back to an assistant after a human took it.
//
// POST /v1/conversations/{id}/return-to-bot — scope conversations:ASSIGN, not
// conversations:write, because this moves a thread to somebody else. Answers
// 201 with
// `{ returned: true, conversationId, botId, botName, returnedAt }`.
//
// The reverse of an escalation, and it did not exist before 2026-10-13:
// handoff was one-directional, so once a person answered one question the
// assistant was out of the conversation for good.
//
// botID is REQUIRED and is NOT defaulted to whoever owned the thread before.
// A thread three assistants have touched has no obvious "the" bot, and
// guessing wrong means the visitor talks to the returns agent about a size
// exchange. Bots.List has the choices.
//
// It refuses rather than quietly doing nothing, all as 400s with the state in
// `details`:
//
//   - a `pending` conversation — a human was summoned and has not finished,
//     and answering over a colleague mid-reply is worse than silence;
//   - `snoozed`, `closed` or `merged` — a promise, or somebody's deliberate
//     end;
//   - a `paused` or `draft` bot, which would answer nothing while the thread
//     had already left the human queue;
//   - a thread a DIFFERENT bot owns, which is not a takeover.
//
// A thread that bot already owns is a SUCCESS and a no-op with a fresh
// returnedAt, not a 409: a retry, or two teammates pressing the same button,
// should not fail for a state that is already what was asked for.
//
// Distinct from SetAgentHandling, which is a BYO agent reporting its own
// progress and can only make the caller the owner or stop being one.
func (s *ConversationsService) ReturnToBot(ctx context.Context, conversationID, botID string) (Object, error) {
	if conversationID == "" {
		return nil, errors.New("chatly: Conversations.ReturnToBot: empty conversation id")
	}
	if botID == "" {
		return nil, errors.New("chatly: Conversations.ReturnToBot: botId is required (the API does not default it)")
	}
	body := map[string]any{"botId": botID}
	path := "/v1/conversations/" + pathEscape(conversationID) + "/return-to-bot"
	return s.c.writeObject(ctx, "POST", path, nil, body)
}
