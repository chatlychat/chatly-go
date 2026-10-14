package chatly

import (
	"context"
	"errors"
	"strconv"
)

// AIService is the key-accessible half of the AI configuration surface:
// BYOK provider credentials, the spend meter, the house tone and the custom
// HTTP tools an agent may call. Reach it as client.AI.
//
// Three different scopes live here, which is easy to get wrong when minting a
// key: the tone and tool reads want ai:read, their writes ai:write, and Quota
// wants workspace:billing:read — an AI ceiling is a billing fact, not an AI
// one.
type AIService struct{ c *Client }

// The three provider keys AddProvider and TestProvider accept. Anything else
// is a 400 from a zod enum, not a 404.
const (
	AIProviderOpenAI    = "openai"
	AIProviderAnthropic = "anthropic"
	AIProviderDeepgram  = "deepgram"
)

// The five tone categories. SetTone is strict about them: a sixth name is a
// 400 rather than a dropped rule.
const (
	ToneCategoryEmpathy     = "empathy"
	ToneCategoryLanguage    = "language"
	ToneCategoryFormality   = "formality"
	ToneCategoryPersonality = "personality"
	ToneCategoryWarmth      = "warmth"
)

// Providers returns one row per supported provider, configured or not.
//
// GET /v1/ai-providers — scope ai:read. An OBJECT, `{ providers: [...] }`,
// not the bare array the route name suggests: reaching for [0] gets nothing.
//
// The list is built from a static table rather than from stored rows, so an
// empty workspace gets three rows with `configured: false` rather than an
// empty list. No key value ever comes back — only `keyHint` — and that plus
// the validation fields are forced to null whenever `configured` is false,
// even when the stored row still holds them. There is no reveal route.
func (s *AIService) Providers(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/ai-providers", nil)
}

// AddProvider stores a provider key for this workspace (BYOK).
//
// POST /v1/ai-providers — scope ai:write, answers 201 with
// `{ provider, validation, reindexQueued }`. The body field is `apiKey`, not
// `key` and not `token`; it is trimmed and then held to 20..512 characters,
// rejected rather than truncated.
//
// The server makes a REAL call to the provider before storing. Only an
// authentication-class refusal blocks the save — that comes back as a 400
// carrying the provider's own sentence — while a provider timeout or outage
// stores the key anyway with `validation.status: "unverified"`, which is a
// success. So read validation.status rather than inferring it from the 201.
//
// A second call for the same provider REPLACES the stored key; there is no
// 409 for "already configured". `reindexQueued` is how many KB articles were
// queued for embedding, which is nonzero for openai only and capped at 500 —
// the rest index on their next publish.
//
// One trap: this route requires an Idempotency-Key, and a replay returns the
// stored answer WITHOUT re-validating or re-storing. Do not pin a key across
// two genuine saves.
func (s *AIService) AddProvider(ctx context.Context, provider, apiKey string) (Object, error) {
	if provider == "" {
		return nil, errors.New("chatly: AI.AddProvider: provider is required (see the AIProvider* constants)")
	}
	if apiKey == "" {
		return nil, errors.New("chatly: AI.AddProvider: apiKey is required")
	}
	body := map[string]any{"provider": provider, "apiKey": apiKey}
	return s.c.writeObject(ctx, "POST", "/v1/ai-providers", nil, body)
}

// TestProvider re-validates a stored key without changing it.
//
// POST /v1/ai-providers/{provider}/test — scope ai:write (not ai:read: it
// spends a request against the customer's vendor account). Answers 201 with
// `{ provider, validation }` and sends no body.
//
// The path segment is the PROVIDER NAME, not an id, and the handler parses it
// against the enum — an unknown one is a 400 whose message reads "Invalid
// request body" even though nothing was in the body. No key on file is a 404
// naming the provider's label.
//
// A key that has started failing is recorded as failing and deliberately LEFT
// IN PLACE: this route never deletes a credential, and it answers 201 with
// the failure in validation.status rather than an HTTP error. Branch on the
// body, not the status.
func (s *AIService) TestProvider(ctx context.Context, provider string) (Object, error) {
	if provider == "" {
		return nil, errors.New("chatly: AI.TestProvider: provider is required (see the AIProvider* constants)")
	}
	path := "/v1/ai-providers/" + pathEscape(provider) + "/test"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}

// Quota returns subsidised AI spend against the workspace's cap.
//
// GET /v1/ai/quota — scope workspace:billing:read, NOT ai:read.
//
// Four spend figures, and labelling the wrong one is the mistake to avoid.
// `spent` is PLATFORM-KEY spend only: the one figure `budget` bounds and the
// only spend that can refuse a call. `totalSpent` is the user-facing total,
// with `platformSpent` and `byokSpent` beside it — so a workspace on its own
// provider key can show "$412" of total spend against a $5 platform budget,
// and a client that renders `spent` as "your AI spend" is wrong about them.
//
// `budget`, `remaining`, `utilization` and `projectedExhaustionAt` are
// NULLABLE, and null means uncapped — a normal answer for enterprise and for
// free-with-BYOK, not an omission. `capBasis` is the frozen literal
// "platform_key_spend".
//
// This one proxies to the AI service, so a 503 here is that service being
// down rather than the API; it is retried by this client like any other 5xx.
func (s *AIService) Quota(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/ai/quota", nil)
}

// Tone returns every tone rule, as a BARE ARRAY.
//
// GET /v1/ai/tone — scope ai:read. Each item is
// `{ id, category, rule, enabled, position }`.
//
// Ordered by category then position, not by insertion, and `position` is
// per CATEGORY — so position 0 appears up to five times in one response.
// Bounded at 40 rows (8 per category × 5), which is why there is no cursor
// and never will be.
//
// Do not keep an `id` from here as a handle: SetTone deletes and re-inserts
// the whole set, so every id changes on every save.
func (s *AIService) Tone(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/ai/tone", nil)
}

// TonePreview returns the rendered system instruction the tone rules produce.
//
// GET /v1/ai/tone/preview — scope ai:read. `{ instruction }`, an object and
// not a bare string, so the shape can grow without breaking eight SDKs.
//
// An empty string is the NORMAL answer for a workspace that configured
// nothing — no default house style is invented. The block is Redis-cached for
// 24 hours and the empty string is cached too; SetTone invalidates it, but a
// failed invalidation is swallowed, so a stale block can survive the day.
// Render this rather than reassembling it from Tone(): the renderer lives
// server-side on purpose.
func (s *AIService) TonePreview(ctx context.Context) (Object, error) {
	return s.c.getObject(ctx, "/v1/ai/tone/preview", nil)
}

// ToneRuleInput is one rule in a SetTone call.
type ToneRuleInput struct {
	// Category must be one of the five ToneCategory* constants. The write
	// schema is strict: a sixth name is a 400.
	Category string `json:"category"`

	// Rule is the instruction itself, trimmed, 3..400 characters. Rejected
	// outside that range, never truncated.
	Rule string `json:"rule"`

	// Enabled defaults to TRUE server-side, which is why this is a pointer:
	// a plain bool would make the zero value indistinguishable from "not
	// set", and `false` would be dropped by omitempty and silently become
	// enabled. Use chatly.Bool(false) to disable a rule.
	Enabled *bool `json:"enabled,omitempty"`
}

// SetTone REPLACES the whole tone rule set and returns it as stored.
//
// PUT /v1/ai/tone — scope ai:write. The body is `{ rules: [...] }` and it is
// strict at BOTH levels, so a misspelled key is a 400 rather than a silently
// dropped field — for once a typo is visible. At most 40 rules and at most 8
// per category.
//
// A full replace, not a patch: passing an empty slice DELETES every rule,
// which is the only way to clear them (there is no delete route). The
// response is the same bare array Tone returns, re-read after the write — not
// `{ rules }` and not a 204.
//
// `position` is assigned server-side from the order you send, counted per
// category, and is not accepted in the body.
func (s *AIService) SetTone(ctx context.Context, rules []ToneRuleInput) ([]Object, error) {
	for i, r := range rules {
		if r.Category == "" || r.Rule == "" {
			return nil, errors.New("chatly: AI.SetTone: rule " + strconv.Itoa(i) + " needs both a category and a rule")
		}
	}
	// Non-nil even when empty: `{"rules":null}` fails the array check, while
	// `{"rules":[]}` is the legal "wipe the set" request.
	if rules == nil {
		rules = []ToneRuleInput{}
	}
	return s.c.writeObjects(ctx, "PUT", "/v1/ai/tone", map[string]any{"rules": rules})
}

// GenerateTone turns a prose style guide into DRAFT tone rules.
//
// POST /v1/ai/tone/generate — scope ai:write, answers 201 with
// `{ rules, note }`. The body is `{ styleGuide }`, strict, trimmed, and
// 40..20000 characters: a one-line style guide is a 400, which is the trap
// here.
//
// NOTHING IS SAVED. The rules come back as a proposal and only take effect
// when you pass them to SetTone — this is the single most likely misuse of
// the method. `note` is the model's remark about what it could not extract,
// or null.
//
// A draft rule's `category` is a free string rather than one of the five on
// purpose: a model can name a sixth category, and SetTone is where it has to
// become a real one.
//
// It is a live LLM call: up to 60s per attempt. Expect 409 when the workspace
// has no provider key configured and 402 when the platform AI budget is
// spent; neither is retried, by design.
func (s *AIService) GenerateTone(ctx context.Context, styleGuide string) (Object, error) {
	if styleGuide == "" {
		return nil, errors.New("chatly: AI.GenerateTone: styleGuide is required (40..20000 chars)")
	}
	body := map[string]any{"styleGuide": styleGuide}
	return s.c.writeObject(ctx, "POST", "/v1/ai/tone/generate", nil, body)
}

// Tools lists the custom HTTP tools an AI agent may call, as a BARE ARRAY.
//
// GET /v1/ai/tools — scope ai:read, ordered by name, soft-deleted rows
// excluded.
//
// Secret VALUES are never returned: you get `secretHeaderNames` and
// `signingSecretSet` only, and there is no reveal route. An empty
// `secretHeaderNames` does not prove a tool has no secrets — a row encrypted
// under a rotated key reads as empty rather than as an error.
//
// `requiresApproval` is a union on the wire: false, true, "agent" or
// "end_user". Today's rows normalise to false/"agent"/"end_user", but the
// legacy bare `true` is still permitted, so do not decode it as a bool.
func (s *AIService) Tools(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/ai/tools", nil)
}

// AIToolParameter is one argument a tool accepts.
type AIToolParameter struct {
	// Name, 1..48 chars, matching ^[a-z][a-z0-9_]*$.
	Name string `json:"name"`
	// Type is string, number or boolean.
	Type string `json:"type"`
	// Description is REQUIRED, 1..500 chars — it is what the model reads to
	// decide what to put here.
	Description string `json:"description"`
	// Required marks the parameter mandatory for the model.
	Required bool `json:"required,omitempty"`
	// Source is where the value comes from: "model" (the default — the model
	// invents it), or one of contact.id, contact.email, contact.external_id,
	// contact.external_id_verified, conversation.id, or
	// context.verified.<key>. A verified source obliges SigningSecret.
	Source string `json:"source,omitempty"`
}

// AIToolConfirmTemplate is the confirmation card shown before an end-user
// approves a tool call. Strict: an unknown key is a 400.
type AIToolConfirmTemplate struct {
	Title string `json:"title,omitempty"`
	// Fields are label/value pairs rendered on the card.
	Fields []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"fields,omitempty"`
	ConfirmLabel string `json:"confirmLabel,omitempty"`
	// CancelLabel defaults to "Cancel" server-side.
	CancelLabel string `json:"cancelLabel,omitempty"`
}

// AIToolInput is the body of CreateTool and UpdateTool.
//
// Field names worth getting right, because the schema STRIPS unknown keys on
// this route rather than rejecting them — a wrong name is a 200 that changed
// nothing: `urlTemplate` not `url`, `bodyTemplate` not `body`,
// `secretHeaders` for the write-only values, `timeoutSeconds` not `timeout`.
type AIToolInput struct {
	// Name is required on create: 2..64 chars, ^[a-z][a-z0-9_]*(\.[a-z]...)*$,
	// and refused if it collides with a built-in agent tool key.
	//
	// A DUPLICATE name is a 500 rather than a 409, and a soft-deleted tool
	// keeps its name reserved forever — the unique index is not partial on
	// deleted_at. Pick a new name rather than reusing a deleted one.
	Name string `json:"name,omitempty"`

	// Description is required on create, 1..1000 chars.
	Description string `json:"description,omitempty"`

	// Method is GET, POST, PUT, PATCH or DELETE. Defaults to GET.
	Method string `json:"method,omitempty"`

	// URLTemplate is required on create, max 2048. The HOST must be literal —
	// a {{placeholder}} in the host is a 400 — and the rendered URL is put
	// through the outbound SSRF guard at save time, so a private address is
	// refused here rather than at call time.
	URLTemplate string `json:"urlTemplate,omitempty"`

	// Headers are sent as-is and are readable back. The reserved names are
	// refused on both header maps: x-chatly-signature,
	// x-chatly-contact-external-id, x-chatly-identity-verified,
	// x-chatly-conversation-id, x-chatly-timestamp, host, content-length.
	Headers map[string]string `json:"headers,omitempty"`

	// SecretHeaders hold values that are encrypted at rest and never read
	// back. On a patch this REPLACES the whole map rather than merging it;
	// to clear it you need an explicit null, which omitempty cannot send —
	// use Client.Request for that.
	SecretHeaders map[string]string `json:"secretHeaders,omitempty"`

	// SigningSecret, min 32 chars, is what your endpoint verifies
	// x-chatly-signature against. Required for any tool with
	// RequiresVerifiedIdentity or a verified parameter source.
	SigningSecret string `json:"signingSecret,omitempty"`

	// RequiresVerifiedIdentity limits the tool to conversations whose
	// visitor passed identity verification. Pointer because false is
	// meaningful and is also the server's default.
	RequiresVerifiedIdentity *bool `json:"requiresVerifiedIdentity,omitempty"`

	// BodyTemplate, max 20000 chars, for the verbs that carry one.
	BodyTemplate string `json:"bodyTemplate,omitempty"`

	// Parameters, max 16.
	Parameters []AIToolParameter `json:"parameters,omitempty"`

	// TimeoutSeconds is 1..30 and REJECTED out of range, not clamped.
	// Defaults to 20 on create; on a patch, omitting it leaves it alone.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// Enabled defaults to true on create. Pointer, so disabling is sendable.
	Enabled *bool `json:"enabled,omitempty"`

	// RequiresApproval is a union: false, true, "agent" or "end_user". Pass a
	// bool or a string. "end_user" OBLIGES ConfirmTemplate, and a
	// ConfirmTemplate on anything else is refused rather than ignored.
	RequiresApproval any `json:"requiresApproval,omitempty"`

	// ConfirmTemplate is the card an end user approves.
	ConfirmTemplate *AIToolConfirmTemplate `json:"confirmTemplate,omitempty"`

	// ApprovalThresholdParam names a parameter whose value decides whether
	// approval is needed; ApprovalThresholdValue is the number it is compared
	// against. It round-trips as a JSON number but is stored as numeric, so
	// expect formatting differences on read.
	ApprovalThresholdParam string   `json:"approvalThresholdParam,omitempty"`
	ApprovalThresholdValue *float64 `json:"approvalThresholdValue,omitempty"`
}

// CreateTool declares a custom HTTP tool and returns the stored row.
//
// POST /v1/ai/tools — scope ai:write, answers 201.
//
// Three cross-field rules are enforced at write time, all as 400s:
// RequiresApproval "end_user" requires a ConfirmTemplate; a ConfirmTemplate
// on a tool that is not "end_user" is refused rather than ignored; and a tool
// with RequiresVerifiedIdentity or a verified parameter source must carry a
// SigningSecret.
func (s *AIService) CreateTool(ctx context.Context, in AIToolInput) (Object, error) {
	if in.Name == "" || in.Description == "" || in.URLTemplate == "" {
		return nil, errors.New("chatly: AI.CreateTool: name, description and urlTemplate are required")
	}
	return s.c.writeObject(ctx, "POST", "/v1/ai/tools", nil, in)
}

// UpdateTool patches a tool and returns the whole updated row.
//
// PATCH /v1/ai/tools/{id} — scope ai:write. Every field is optional and an
// omitted one is left alone; a map or slice that IS sent replaces its stored
// value wholesale rather than merging.
//
// The coherence rules run against the MERGED row, not against your patch, so
// a one-field patch can be refused for a field it does not mention: clearing
// a ConfirmTemplate on an "end_user" tool is a 400, as is clearing the
// signing secret on a tool with a verified parameter source.
//
// The handler does not validate the id, so a non-UUID here reaches Postgres
// and comes back as a 500 rather than a 400 — this method refuses an empty
// one, but pass a real tool id.
func (s *AIService) UpdateTool(ctx context.Context, toolID string, in AIToolInput) (Object, error) {
	if toolID == "" {
		return nil, errors.New("chatly: AI.UpdateTool: empty tool id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/ai/tools/"+pathEscape(toolID), nil, in)
}

// DeleteTool soft-deletes a tool.
//
// DELETE /v1/ai/tools/{id} — scope ai:write. Answers 200 with
// `{ deleted: true }`, NOT a 204; macros.Delete is the only real 204 on this
// surface.
//
// It NEVER answers 404. The update is a bare WHERE on the id with no
// existence check and no row count, so a wrong id, an already-deleted tool
// and another workspace's id all answer `{ deleted: true }` having changed
// nothing. Read Tools() afterwards if you need to know it was really there.
//
// The row survives with deleted_at set and enabled false, which means its
// name stays reserved and cannot be reused.
func (s *AIService) DeleteTool(ctx context.Context, toolID string) (Object, error) {
	if toolID == "" {
		return nil, errors.New("chatly: AI.DeleteTool: empty tool id")
	}
	return s.c.writeObject(ctx, "DELETE", "/v1/ai/tools/"+pathEscape(toolID), nil, nil)
}
