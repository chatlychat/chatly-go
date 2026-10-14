package chatly

import (
	"context"
	"errors"
)

// CanvasStepsService is /v1/canvas-steps — the back half of an ASYNC canvas
// step. Reach it as client.CanvasSteps.
//
// The flow it completes: your canvas endpoint answered `{ pending: true }`
// because a payment needed 3-D Secure, a fraud check had to clear or a PSP
// was slow, and was handed a correlation id. Minutes later a backend process
// of yours — holding a `ck_` key and no visitor session — reads the step and
// posts the real next canvas. That caller is precisely the one a ten-second
// synchronous timeout could not serve, which is why the async form exists.
type CanvasStepsService struct{ c *Client }

// Get reads one pending interactive step.
//
// GET /v1/canvas-steps/{id} — scope messages:read.
// `{ id, conversationId, messageId, canvasId, reason, actionId, status,
// expiresAt, settledAt, createdAt }`, where reason is step or submit and
// status is pending, resolved or expired.
//
// It carries NO canvas: read the canvas off the message if you need it.
//
// A 404 means the id is unknown, belongs to another workspace, or the step
// has expired — and the route deliberately cannot tell you which. An id that
// reported "expired" rather than "unknown" would confirm that somebody else's
// step id is real.
func (s *CanvasStepsService) Get(ctx context.Context, stepID string) (Object, error) {
	if stepID == "" {
		return nil, errors.New("chatly: CanvasSteps.Get: empty step id")
	}
	return s.c.getObject(ctx, "/v1/canvas-steps/"+pathEscape(stepID), nil)
}

// Resolve posts the real next canvas for a pending step.
//
// POST /v1/canvas-steps/{id}/resolve — scope messages:write. Answers with
// `{ stepId, conversationId, messageId, canvasId }`.
//
// The body is `{ canvas }` and BOTH levels are strict: an unrecognised key is
// a 400 rather than a silently dropped field, and `hostEvent` in particular
// is refused on purpose. Inside the canvas, `blocks` and `app` are mutually
// exclusive and exactly one is required; `issuedAt` is accepted and then
// OVERWRITTEN server-side.
//
// SINGLE-USE BY CONSTRUCTION, not by an idempotency key: the claiming update
// carries `status = 'pending' AND expires_at > now()` in its WHERE clause, so
// exactly one resolve wins and a second gets a 409 saying the step is already
// resolved or expired (the TTL is 10 minutes). That is why the route honours
// an Idempotency-Key without demanding one — requiring a header would add a
// 428 for a forgetful integrator in exchange for a guarantee the route
// already makes. A retry after a timeout is therefore safe, and a retry whose
// first attempt SUCCEEDED is told so, rather than putting a second canvas in
// the thread.
//
// One 404 with a non-guessable cause: the pending row is written AFTER the
// widget has been answered, so resolving from inside the same request that
// returned `{ pending: true }` races that write. Resolve from a later
// process, not from the handler that created the step.
func (s *CanvasStepsService) Resolve(ctx context.Context, stepID string, canvas map[string]any) (Object, error) {
	if stepID == "" {
		return nil, errors.New("chatly: CanvasSteps.Resolve: empty step id")
	}
	if len(canvas) == 0 {
		return nil, errors.New("chatly: CanvasSteps.Resolve: canvas is required")
	}
	body := map[string]any{"canvas": canvas}
	return s.c.writeObject(ctx, "POST", "/v1/canvas-steps/"+pathEscape(stepID)+"/resolve", nil, body)
}
