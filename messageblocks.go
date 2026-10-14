package chatly

import (
	"context"
	"errors"
	"strconv"
)

// Patching the rich blocks of a message that has already been delivered —
// the one write on ConversationsService that edits a message in place rather
// than adding one. Split out of conversations.go, which is at the file-size
// ceiling.

// MessageBlockPatch is one block-level edit.
//
// Each patch is strict, so only these two keys are accepted.
type MessageBlockPatch struct {
	// ID is a BLOCK id, 1..64 chars — not the message's and not the
	// conversation's. It resolves at the top level of the envelope or inside
	// a grid's cards, and an unknown one is a 404.
	ID string `json:"id"`

	// Set is a SHALLOW MERGE over that one block's own fields: an omitted
	// field is left alone, and there is no way to delete a field. It may not
	// contain `type` or `id` — either refuses the whole batch.
	Set map[string]any `json:"set"`
}

// UpdateBlocks patches rich blocks on a message already in the thread.
//
// PATCH /v1/conversations/{id}/messages/{messageId}/blocks — scope
// messages:write. Answers `{ id, blocks, body }` where `id` is the MESSAGE id
// and `blocks` is the full re-validated envelope rather than your patch.
//
// Two path parameters, in this order: the CONVERSATION id, then the MESSAGE
// id. Both are parsed as UUIDs server-side, so a malformed one is a 400.
//
// What it can and cannot do: blocks are addressed individually BY ID, so this
// is neither a whole-array replace nor an append — blocks cannot be added,
// removed or reordered, and a new block means a new message. 1..50 patches
// per call.
//
// Failures are all-or-nothing and loud: an unknown block id is a 404, and an
// invalid merged envelope is a 400 that discards THE WHOLE BATCH rather than
// the one patch. A message with no rich blocks is a 400 ("nothing to patch")
// and a deleted one is a 404.
//
// `body` is only re-rendered when the stored body still equalled the
// pre-patch plain-text rendition, so an author's hand-written body survives a
// block edit.
//
// There is deliberately no author check on this route: the intended caller is
// an API key, whose messages have no author, and an author check would refuse
// it.
func (s *ConversationsService) UpdateBlocks(ctx context.Context, conversationID, messageID string, patches []MessageBlockPatch) (Object, error) {
	if conversationID == "" || messageID == "" {
		return nil, errors.New("chatly: Conversations.UpdateBlocks: conversationID and messageID are required")
	}
	if len(patches) == 0 {
		return nil, errors.New("chatly: Conversations.UpdateBlocks: at least one patch is required (the API takes 1..50)")
	}
	for i, p := range patches {
		if p.ID == "" {
			return nil, errors.New("chatly: Conversations.UpdateBlocks: patch " + strconv.Itoa(i) + " has no block id")
		}
	}
	body := map[string]any{"patches": patches}
	path := "/v1/conversations/" + pathEscape(conversationID) +
		"/messages/" + pathEscape(messageID) + "/blocks"
	return s.c.writeObject(ctx, "PATCH", path, nil, body)
}
