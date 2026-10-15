package chatly

// Which outbound surfaces Chatly signs, and what each one signs.
//
// This is the Go port of packages/sdk-node/src/signature-table.ts, and it is
// split from signatures.go for the reason the TypeScript is: the data and the
// verifier each read better on their own, and together they would brush this
// repository's 650-line ceiling.
//
//	| surface                 | header(s)                                             | base string                     | t unit  |
//	| ----------------------- | ----------------------------------------------------- | ------------------------------- | ------- |
//	| unified_v2              | x-chatly-signature-v2: t=…,v1=…                       | <t>.<METHOD>.<path>.<rawBody>   | seconds |
//	| agent_delivery          | x-chatly-signature: t=…,v1=…                          | <t>.<rawBody>                   | seconds |
//	| event_webhook           | x-livechat-signature: t=…,v1=…                        | <t>.<rawBody>                   | seconds |
//	| ai_tool_call            | x-chatly-signature: t=…,v1=…                          | <t>.<METHOD>.<path>.<rawBody>   | seconds |
//	| canvas_step             | x-chatly-canvas-signature: sha256=… + -ts             | <ts>.<rawBody>                  | MS      |
//	| customer_card           | x-chatly-signature: sha256=… + x-chatly-timestamp     | <ts>.<rawBody>                  | MS      |
//	| workflow_webhook        | x-chatly-signature-v2: t=…,v1=…                       | <t>.<rawBody>                   | seconds |
//	| workflow_webhook_legacy | x-chatly-signature: sha256=…                          | <rawBody> — NO timestamp        | n/a     |
//
// THREE SURFACES SEND x-chatly-signature AND THEY SIGN THREE DIFFERENT
// STRINGS. Agent delivery signs <t>.<body> under a t=…,v1=… value; a tool
// call signs <t>.<METHOD>.<path>.<body> under the same-looking value; a
// customer-card fetch signs <ts>.<body> with the timestamp in a SEPARATE
// header and the value prefixed sha256=. A verifier that keys on the header
// name can be pointed at the wrong construction while looking completely
// correct, which is why Surface is a required input and is never inferred.
//
// unified_v2 is where every surface is going, sent ALONGSIDE each legacy
// header. workflow_webhook ALREADY sends x-chatly-signature-v2 and signs
// <t>.<rawBody> with it — the v2 header predates the unified base — so that
// one surface accepts both bases while its sender migrates. The value may
// carry a base marker (b=t target, b=b body, absent = body); the verifier
// reads it to NARROW candidates and to name a refusal, never to widen what a
// surface accepts, because anyone on the wire can write a marker.
//
// THE UNIT TRAP. Two surfaces send MILLISECONDS. A millisecond timestamp read
// as seconds lands ~54,000 years out, every genuine request is refused, and
// the failure looks like a wrong secret. This repository has shipped that
// mistake in both directions, so the unit is a property of the SURFACE here,
// never guessed from the value.

// ChatlySignatureSurface names the outbound surface a request came from. A
// named string type rather than an int enum so a value read from config, a
// log line or the cross-SDK corpus round-trips with no lookup table, and so
// an unknown value can be reported back verbatim in the refusal.
type ChatlySignatureSurface string

// The eight surfaces. See this file's header for what each one signs.
const (
	SurfaceUnifiedV2             ChatlySignatureSurface = "unified_v2"
	SurfaceAgentDelivery         ChatlySignatureSurface = "agent_delivery"
	SurfaceEventWebhook          ChatlySignatureSurface = "event_webhook"
	SurfaceAIToolCall            ChatlySignatureSurface = "ai_tool_call"
	SurfaceCanvasStep            ChatlySignatureSurface = "canvas_step"
	SurfaceCustomerCard          ChatlySignatureSurface = "customer_card"
	SurfaceWorkflowWebhook       ChatlySignatureSurface = "workflow_webhook"
	SurfaceWorkflowWebhookLegacy ChatlySignatureSurface = "workflow_webhook_legacy"
)

// ChatlySignatureSurfaces lists every surface, in the order the TypeScript
// reference declares them. A slice rather than only the map below because Go
// map iteration is randomised and the unknown_surface message lists these:
// an error string that reorders itself between runs is a flaky test waiting
// to happen and a confusing thing to paste into a support ticket.
var ChatlySignatureSurfaces = []ChatlySignatureSurface{
	SurfaceUnifiedV2,
	SurfaceAgentDelivery,
	SurfaceEventWebhook,
	SurfaceAIToolCall,
	SurfaceCanvasStep,
	SurfaceCustomerCard,
	SurfaceWorkflowWebhook,
	SurfaceWorkflowWebhookLegacy,
}

// ChatlySignatureFailure says why a verification failed. Stable strings, safe
// to branch on and to log; they are identical in all eight SDKs.
type ChatlySignatureFailure string

// The eleven refusal reasons.
const (
	ReasonUnknownSurface            ChatlySignatureFailure = "unknown_surface"
	ReasonMissingSecret             ChatlySignatureFailure = "missing_secret"
	ReasonMissingSignatureHeader    ChatlySignatureFailure = "missing_signature_header"
	ReasonDuplicateSignatureHeader  ChatlySignatureFailure = "duplicate_signature_header"
	ReasonMalformedSignatureHeader  ChatlySignatureFailure = "malformed_signature_header"
	ReasonMissingTimestamp          ChatlySignatureFailure = "missing_timestamp"
	ReasonMalformedTimestamp        ChatlySignatureFailure = "malformed_timestamp"
	ReasonTimestampOutsideTolerance ChatlySignatureFailure = "timestamp_outside_tolerance"
	ReasonMissingRequestTarget      ChatlySignatureFailure = "missing_request_target"
	ReasonReplayUnprotected         ChatlySignatureFailure = "replay_unprotected"
	ReasonSignatureMismatch         ChatlySignatureFailure = "signature_mismatch"
)

// ChatlySignatureBase is which string the MAC was taken over.
type ChatlySignatureBase string

const (
	// BaseBody is `<t>.<rawBody>`.
	BaseBody ChatlySignatureBase = "body"
	// BaseTarget is `<t>.<METHOD>.<path>.<rawBody>` — the unified base.
	BaseTarget ChatlySignatureBase = "target"
	// BaseBodyOnly is `<rawBody>` with no timestamp anywhere: unbounded replay.
	BaseBodyOnly ChatlySignatureBase = "body_only"
)

// ChatlySignatureToleranceSeconds is five minutes, matching
// SIGNATURE_TOLERANCE_SECONDS on the producing side. It is exported as a
// plain number (as in every other SDK) as well as being the default behind
// a zero VerifyChatlySignatureInput.Tolerance.
const ChatlySignatureToleranceSeconds = 300

// ChatlyV2SignatureHeader is the header the unified construction travels in,
// on every surface.
const ChatlyV2SignatureHeader = "x-chatly-signature-v2"

type signatureFormat int

const (
	formatTV1    signatureFormat = iota // t=…,v1=<hex>[,v1=…][,b=…]
	formatSHA256                        // sha256=<hex>
)

type timestampUnit int

const (
	unitNone timestampUnit = iota
	unitSeconds
	unitMilliseconds
)

// surfaceSpec is one row of the table. Unexported because the TypeScript
// exports its table for documentation, and in Go the doc comment above IS the
// documentation; exporting the struct would freeze its field set into the
// public API for no caller's benefit.
type surfaceSpec struct {
	signatureHeader string
	// timestampHeader is the header carrying the timestamp on its own, or ""
	// when it is the digest header's own t= field (or there is none — see
	// unit).
	timestampHeader string
	format          signatureFormat
	unit            timestampUnit
	base            ChatlySignatureBase
	// alsoAcceptsTarget: workflow_webhook only, which sent the v2 header
	// before the base gained the method and path.
	alsoAcceptsTarget bool
	describe          string
}

func (s surfaceSpec) unitName() string {
	switch s.unit {
	case unitSeconds:
		return "seconds"
	case unitMilliseconds:
		return "milliseconds"
	default:
		return "no timestamp"
	}
}

// signatureSurfaces is the table as data. Data rather than a switch so the
// test can assert every constant in ChatlySignatureSurfaces has a row: a
// surface added to the list and forgotten here would otherwise be an
// unknown_surface refusal at runtime with a green suite.
var signatureSurfaces = map[ChatlySignatureSurface]surfaceSpec{
	SurfaceUnifiedV2: {
		signatureHeader: ChatlyV2SignatureHeader,
		format:          formatTV1,
		unit:            unitSeconds,
		base:            BaseTarget,
		describe:        "the unified v2 signature, sent alongside every surface's legacy header, over t.METHOD.path.body",
	},
	SurfaceAgentDelivery: {
		signatureHeader: "x-chatly-signature",
		format:          formatTV1,
		unit:            unitSeconds,
		base:            BaseBody,
		describe:        "external-agent push delivery, signed with the bot's signingSecret",
	},
	SurfaceEventWebhook: {
		signatureHeader: "x-livechat-signature",
		format:          formatTV1,
		unit:            unitSeconds,
		base:            BaseBody,
		describe:        "event webhook, signed with that subscription's whsec_ secret",
	},
	SurfaceAIToolCall: {
		signatureHeader: "x-chatly-signature",
		format:          formatTV1,
		unit:            unitSeconds,
		base:            BaseTarget,
		describe:        "custom AI tool call, signed with that tool's secret over t.METHOD.path.body",
	},
	SurfaceCanvasStep: {
		signatureHeader: "x-chatly-canvas-signature",
		timestampHeader: "x-chatly-canvas-ts",
		format:          formatSHA256,
		unit:            unitMilliseconds,
		base:            BaseBody,
		describe:        "canvas / interactive-step callback, timestamp in MILLISECONDS",
	},
	SurfaceCustomerCard: {
		signatureHeader: "x-chatly-signature",
		timestampHeader: "x-chatly-timestamp",
		format:          formatSHA256,
		unit:            unitMilliseconds,
		base:            BaseBody,
		describe:        "customer-card fetch, timestamp in MILLISECONDS",
	},
	SurfaceWorkflowWebhook: {
		signatureHeader:   ChatlyV2SignatureHeader,
		format:            formatTV1,
		unit:              unitSeconds,
		base:              BaseBody,
		alsoAcceptsTarget: true,
		describe:          `workflow "call a webhook" action, replay-bounded v2 header`,
	},
	SurfaceWorkflowWebhookLegacy: {
		signatureHeader: "x-chatly-signature",
		format:          formatSHA256,
		unit:            unitNone,
		base:            BaseBodyOnly,
		describe:        "workflow action, LEGACY header over the body alone — no replay protection",
	},
}
