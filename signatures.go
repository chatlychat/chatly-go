package chatly

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// One verifier for every signature Chatly puts on an outbound request — the
// Go port of verifyChatlySignature in packages/sdk-node/src/signatures.ts,
// held to the same cross-SDK corpus (testdata/signature_vectors.json).
//
// VerifyWebhookSignature in webhooks.go is untouched and still answers the
// one question it was written for (an event webhook, yes or no). This is the
// general form: eight surfaces, the unified v2 header, key rotation on both
// ends, and a REASON instead of a bool.
//
// WHY A REASON. VerifyWebhookSignature returns false for nine different
// failures, and an integrator cannot tell "your clock is wrong" from "your
// secret is wrong" from "you passed the parsed body" — each of which is a
// support ticket. The Message is written to be printed.
//
// WHY IT NEVER PANICS. CONTRACT.md §6: a verifier that panics is an ACCEPTED
// webhook in a handler whose recover() logs and returns 200. Every input —
// nil headers, a nil body, an empty secret slice, a 30-digit timestamp —
// comes back as OK == false.

// ChatlyV2Policy is how hard to insist on the unified v2 header.
//
//   - V2Prefer (the zero value) — if x-chatly-signature-v2 is present AND
//     Method and Path were supplied, verify THAT and nothing else; otherwise
//     verify the surface's own legacy header. A receiver that passes its
//     method and path gets the stronger check the day the sender starts
//     emitting v2, with no code change.
//   - V2Require — refuse unless the v2 header is there.
//   - V2Ignore — the surface's own construction only.
//
// Prefer never cross-falls-back: once v2 is chosen, a bad v2 digest is a
// refusal, not a reason to try the legacy header. Both come from the same
// secret, so a good legacy digest beside a bad v2 one is tampering or a
// sender bug, and accepting it would hide both.
type ChatlyV2Policy string

const (
	// V2Prefer is the default; "" means the same thing, so a zero-valued
	// input gets the migration-free behaviour.
	V2Prefer  ChatlyV2Policy = "prefer"
	V2Require ChatlyV2Policy = "require"
	V2Ignore  ChatlyV2Policy = "ignore"
)

// ChatlySignatureToleranceUnlimited disables the freshness check. Only for
// re-verifying something out of an archive; on a live endpoint it removes the
// replay bound the timestamp exists to provide. Same sentinel shape as
// WebhookToleranceUnlimited, so the two verifiers read alike.
const ChatlySignatureToleranceUnlimited = time.Duration(math.MaxInt64)

// VerifyChatlySignatureInput is the input to VerifyChatlySignature.
type VerifyChatlySignatureInput struct {
	// Surface is which route this request arrived on. Required; never
	// inferred, because the header name does not determine the construction.
	Surface ChatlySignatureSurface

	// Body is the EXACT bytes received, before any JSON parsing. A nil Body
	// is the empty body. In net/http: io.ReadAll(r.Body), verify, and only
	// then json.Unmarshal.
	Body []byte

	// Headers is the request's headers. The type is map[string][]string so
	// an http.Header passes straight in (r.Header), but lookup does NOT rely
	// on http.Header's canonical form: every key that matches the wanted
	// name case-insensitively contributes its values. A hand-built map with
	// both "x-a" and "X-A" therefore holds TWO values, which for a signature
	// header is a duplicate_signature_header refusal — exactly what the other
	// seven SDKs say for the same input.
	Headers map[string][]string

	// Secrets is the secret for THIS surface, or every secret currently live
	// on it during a rotation. A single secret is a one-element slice:
	// []string{secret}.
	//
	// A slice field rather than a variadic parameter because the input is a
	// struct, and a struct is what keeps a call with eleven knobs readable
	// and lets fields be added later without breaking callers; a variadic
	// would have forced the secret out of the struct and into a positional
	// argument beside it. Empty strings are skipped, and SecretIndex on the
	// result still indexes the slice AS PASSED, so {"", s} verifying with s
	// reports 1 — a config loader that leaves a blank slot does not shift
	// which secret you think is in use.
	Secrets []string

	// Tolerance is the freshness window, applied in BOTH directions; exactly
	// the tolerance away is still accepted. Zero means
	// ChatlySignatureToleranceSeconds (300s), matching WebhookVerification —
	// a zero window would refuse nearly everything and is not a setting
	// anyone means. ChatlySignatureToleranceUnlimited skips the check. A
	// negative value refuses every timestamped request, as it does in the
	// reference implementation.
	Tolerance time.Duration

	// Method is the request's HTTP method; case is normalised. It is in the
	// signed string on unified_v2 and ai_tool_call, and supplying it (with
	// Path) on any surface is what lets V2Prefer pick up the v2 header.
	Method string

	// Path is pathname + search of the request as it arrived (r.URL.RequestURI()
	// in net/http) — never the scheme or the host.
	Path string

	// V2 is the v2 policy. "" means V2Prefer.
	V2 ChatlyV2Policy

	// AllowUnprotectedReplay is for workflow_webhook_legacy only, and is
	// required there: that surface signs the body with NO timestamp, so a
	// captured request verifies forever. Accepting it has to be deliberate.
	AllowUnprotectedReplay bool

	// Now overrides the clock; the zero value means time.Now().
	//
	// A time.Time rather than the reference's nowMs number, and that choice
	// is what makes "zero means unset" safe here: time.Time's zero is
	// 0001-01-01, not the Unix epoch, so no caller can mean it. An *int64 of
	// milliseconds would also work but makes every test write a pointer to a
	// local; an int64 with 0 as the sentinel would steal 1970-01-01T00:00Z,
	// which is a valid instant in theory. Tests convert with time.UnixMilli.
	Now time.Time
}

// VerifyChatlySignatureResult is the outcome. When OK is true, Surface,
// TimestampMs, Base and SecretIndex are set; when false, Surface, Reason and
// Message are. One struct rather than an interface with two implementations,
// because a Go caller's natural `if !res.OK { log(res.Reason) }` should not
// need a type switch.
type VerifyChatlySignatureResult struct {
	OK bool

	// Surface is what actually verified — or was being verified when it
	// failed. On a preferred v2 header that is SurfaceUnifiedV2 rather than
	// the surface asked for; on unknown_surface it is the string given.
	Surface ChatlySignatureSurface

	// TimestampMs is when the sender says it signed, in Unix MILLISECONDS,
	// normalised so seconds and milliseconds surfaces compare. nil on
	// workflow_webhook_legacy, which sends no timestamp — a pointer so that
	// "none" cannot be confused with the epoch.
	TimestampMs *int64

	// Base is the string the matching MAC was taken over.
	Base ChatlySignatureBase

	// SecretIndex indexes Secrets as passed. During a rotation this is how
	// you learn the old secret is no longer in use.
	SecretIndex int

	// Reason is the stable failure code. "" when OK.
	Reason ChatlySignatureFailure

	// Message is a sentence written for whoever has to fix it. "" when OK.
	Message string
}

func sigFail(surface ChatlySignatureSurface, reason ChatlySignatureFailure, message string) VerifyChatlySignatureResult {
	return VerifyChatlySignatureResult{Surface: surface, Reason: reason, Message: message}
}

// asciiEqualFold compares header names case-insensitively over ASCII only.
// strings.EqualFold applies Unicode simple folding, under which the KELVIN
// SIGN (U+212A) equals 'k'; a header name is an RFC 9110 token and is ASCII
// by definition, so anything wider is a way for a crafted name to alias a
// real one.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// headerValues collects every value under every key that matches name.
//
// Matching keys are visited in sorted order. Go randomises map iteration,
// and where the reference reads "the first value" (a separate timestamp
// header) a random order would make the same input verify on one run and
// not the next. Sorted is arbitrary but STABLE; within one key the slice
// order is kept, which is the arrival order net/http records.
func headerValues(headers map[string][]string, name string) []string {
	var keys []string
	for key := range headers {
		if asciiEqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var found []string
	for _, key := range keys {
		found = append(found, headers[key]...)
	}
	return found
}

type indexedSecret struct {
	value string
	index int
}

func (in VerifyChatlySignatureInput) hasTarget() bool {
	return in.Method != "" && in.Path != ""
}

func (in VerifyChatlySignatureInput) policy() ChatlyV2Policy {
	if in.V2 == "" {
		return V2Prefer
	}
	return in.V2
}

// isHexSHA256 is ^[0-9a-fA-F]{64}$ without regexp. Checked BEFORE decoding so
// a garbage digest is reported as malformed rather than reaching the compare
// as a length mismatch and reading as a wrong secret.
func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseTV1 parses `t=…,v1=…[,b=…]`, collecting EVERY v1 (a sender mid-rotation
// signs with both secrets). It keeps the FIRST t and the FIRST b, so a crafted
// second one appended on the wire cannot move the window or the base, and it
// ignores unknown keys so a future v3 field does not break a v1 verifier.
func parseTV1(raw string) (t string, v1 []string, b string, ok bool) {
	haveT, haveB := false, false
	for _, part := range strings.Split(raw, ",") {
		eq := strings.Index(part, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(part[:eq])
		value := strings.TrimSpace(part[eq+1:])
		switch key {
		case "t":
			if !haveT {
				t, haveT = value, true
			}
		case "b":
			if !haveB {
				b, haveB = value, true
			}
		case "v1":
			v1 = append(v1, value)
		}
	}
	return t, v1, b, haveT && len(v1) > 0
}

// applyV2Policy takes the v2 decision before anything is parsed. done is true
// when it answered the whole question — by delegating to unified_v2 or by
// refusing — and false when verification continues on the surface asked for.
func applyV2Policy(in VerifyChatlySignatureInput, spec surfaceSpec) (VerifyChatlySignatureResult, bool) {
	policy := in.policy()
	// A surface whose OWN header is the v2 header handles both bases in its
	// candidate list instead; there is nothing to delegate to.
	if policy == V2Ignore || in.Surface == SurfaceUnifiedV2 || spec.signatureHeader == ChatlyV2SignatureHeader {
		return VerifyChatlySignatureResult{}, false
	}
	present := len(headerValues(in.Headers, ChatlyV2SignatureHeader)) > 0
	if policy == V2Require && !present {
		return sigFail(in.Surface, ReasonMissingSignatureHeader, fmt.Sprintf(
			`v2: "require" was asked for and this request carries no %s header. %s alone means the sender has not moved to the unified construction yet — use the default v2: "prefer" until it has.`,
			ChatlyV2SignatureHeader, spec.signatureHeader)), true
	}
	if !present {
		return VerifyChatlySignatureResult{}, false
	}
	if !in.hasTarget() {
		if policy == V2Require {
			return sigFail(in.Surface, ReasonMissingRequestTarget, fmt.Sprintf(
				"The unified %s signs <t>.<METHOD>.<path>.<rawBody>, so both Method and Path are required to verify it. Path is pathname + search of the request as it arrived — not the host, not the scheme.",
				ChatlyV2SignatureHeader)), true
		}
		// prefer, with no target to verify against: the legacy header is
		// still sent beside it, so fall through rather than refuse a request
		// the caller CAN verify.
		return VerifyChatlySignatureResult{}, false
	}

	delegated := in
	delegated.Surface = SurfaceUnifiedV2
	delegated.V2 = V2Ignore
	r := verifyChatlySignature(delegated)
	if r.OK {
		return r, true
	}
	return sigFail(SurfaceUnifiedV2, r.Reason, fmt.Sprintf(
		`%s (This request carried %s, so it was verified as the unified v2 construction rather than as %s. Pass V2: V2Ignore to verify %s instead.)`,
		r.Message, ChatlyV2SignatureHeader, in.Surface, spec.signatureHeader)), true
}

// candidateBases returns which strings the MAC may have been taken over, most
// specific first, or a refusal when the b= marker rules every candidate out.
//
// The marker is read ONLY off the v2 header (fromV2) and only NARROWS: it is
// something anyone on the wire can write, so it may never widen what the
// surface and the policy allow. Absent or unrecognised (b=z) is treated as
// absent, which is every v2 value minted before the marker existed. What it
// buys is the error: once the workflow sender moves to b=t, a receiver that
// passes no method/path gets missing_request_target, not a signature_mismatch
// that sends them hunting their secret.
func candidateBases(in VerifyChatlySignatureInput, spec surfaceSpec, marker string) ([]ChatlySignatureBase, *VerifyChatlySignatureResult) {
	policy := in.policy()
	fromV2 := spec.signatureHeader == ChatlyV2SignatureHeader
	if fromV2 && marker == "b" && (spec.base == BaseTarget || policy == V2Require) {
		hint := `v2: "require" accepts only the unified base (b=t).`
		if in.Surface == SurfaceUnifiedV2 {
			hint = `unified_v2 accepts only b=t. Verify it with surface "workflow_webhook", which accepts both.`
		}
		r := sigFail(in.Surface, ReasonSignatureMismatch, fmt.Sprintf(
			"This %s is marked b=b: it was signed over <t>.<rawBody>, the workflow action's pre-unified base. %s",
			ChatlyV2SignatureHeader, hint))
		return nil, &r
	}
	if !spec.alsoAcceptsTarget || policy == V2Ignore {
		return []ChatlySignatureBase{spec.base}, nil
	}
	if marker == "t" {
		return []ChatlySignatureBase{BaseTarget}, nil
	}
	if marker == "b" || !in.hasTarget() {
		return []ChatlySignatureBase{spec.base}, nil
	}
	if policy == V2Require {
		return []ChatlySignatureBase{BaseTarget}, nil
	}
	return []ChatlySignatureBase{BaseTarget, spec.base}, nil
}

// basePrefix is everything before the raw body. rawTS is the timestamp text
// EXACTLY as received — "01700000000" re-formatted would be a different
// string and a different digest.
func basePrefix(base ChatlySignatureBase, rawTS string, in VerifyChatlySignatureInput) string {
	switch base {
	case BaseBodyOnly:
		return ""
	case BaseBody:
		return rawTS + "."
	default:
		return rawTS + "." + strings.ToUpper(in.Method) + "." + in.Path + "."
	}
}

// absDiff is |a-b| without overflow. Unsigned subtraction wraps mod 2^64 and
// the true distance between two int64s always fits in a uint64, so ordering
// the operands is all it takes; the naive int64 a-b overflows when Now is far
// before 1970 and a timestamp is near MaxInt64.
func absDiff(a, b int64) uint64 {
	if a >= b {
		return uint64(a) - uint64(b)
	}
	return uint64(b) - uint64(a)
}

// VerifyChatlySignature verifies one signed request from Chatly.
//
//	body, _ := io.ReadAll(r.Body)
//	res := chatly.VerifyChatlySignature(chatly.VerifyChatlySignatureInput{
//	    Surface: chatly.SurfaceEventWebhook,
//	    Body:    body,
//	    Headers: r.Header,
//	    Secrets: []string{os.Getenv("CHATLY_WEBHOOK_SECRET")},
//	    Method:  r.Method,
//	    Path:    r.URL.RequestURI(),
//	})
//	if !res.OK {
//	    log.Printf("chatly signature: %s: %s", res.Reason, res.Message)
//	    http.Error(w, "bad signature", http.StatusBadRequest)
//	    return
//	}
//
// It never panics. See this file's header and signature_table.go.
func VerifyChatlySignature(in VerifyChatlySignatureInput) (res VerifyChatlySignatureResult) {
	// Belt and braces: nothing below is known to panic, and the corpus plus
	// the garbage tests try hard to make it. But the cost of being wrong is
	// an accepted forgery in a handler with a recover(), so a panic is turned
	// into a refusal here — never into OK.
	defer func() {
		if p := recover(); p != nil {
			res = sigFail(in.Surface, ReasonSignatureMismatch,
				fmt.Sprintf("Internal error while verifying %s; refused rather than accepted: %v", in.Surface, p))
		}
	}()
	return verifyChatlySignature(in)
}

func verifyChatlySignature(in VerifyChatlySignatureInput) VerifyChatlySignatureResult {
	surface := in.Surface
	spec, known := signatureSurfaces[surface]
	if !known {
		names := make([]string, len(ChatlySignatureSurfaces))
		for i, s := range ChatlySignatureSurfaces {
			names[i] = string(s)
		}
		return sigFail(surface, ReasonUnknownSurface, fmt.Sprintf(
			"%q is not a Chatly signature surface. One of: %s.", preview(string(surface)), strings.Join(names, ", ")))
	}

	var secrets []indexedSecret
	for i, s := range in.Secrets {
		if s != "" {
			// The CALLER's index survives the filter.
			secrets = append(secrets, indexedSecret{value: s, index: i})
		}
	}
	if len(secrets) == 0 {
		return sigFail(surface, ReasonMissingSecret, fmt.Sprintf(
			"No secret given for %s. Each surface has its own secret — %s. A secret from another surface will never verify.",
			surface, spec.describe))
	}

	if surface == SurfaceWorkflowWebhookLegacy && !in.AllowUnprotectedReplay {
		return sigFail(surface, ReasonReplayUnprotected,
			`The legacy workflow header signs the body with no timestamp, so a captured request verifies forever and nothing here can bound a replay. The same call also sends x-chatly-signature-v2 — verify that instead, with surface "workflow_webhook". If you must accept the legacy header, set AllowUnprotectedReplay and make the endpoint idempotent.`)
	}

	if r, done := applyV2Policy(in, spec); done {
		return r
	}

	// ── The digest header ────────────────────────────────────────────────
	sigHeaders := headerValues(in.Headers, spec.signatureHeader)
	if len(sigHeaders) == 0 {
		return sigFail(surface, ReasonMissingSignatureHeader, fmt.Sprintf(
			"No %s header. The %s always sends one; if the request carries a differently named signature header, you have the wrong surface.",
			spec.signatureHeader, spec.describe))
	}
	if len(sigHeaders) > 1 {
		// Two values means something between Chatly and you merged or
		// injected one, and picking either is a guess. Refuse rather than
		// trial-verify both.
		return sigFail(surface, ReasonDuplicateSignatureHeader, fmt.Sprintf(
			"%d %s headers arrived. Chatly sends exactly one; a proxy merged or added one, and choosing between them would be a guess.",
			len(sigHeaders), spec.signatureHeader))
	}
	rawSignature := strings.TrimSpace(sigHeaders[0])

	var digests []string
	var rawTS, marker string
	if spec.format == formatTV1 {
		t, v1, b, ok := parseTV1(rawSignature)
		if !ok {
			return sigFail(surface, ReasonMalformedSignatureHeader, fmt.Sprintf(
				`%s should be "t=<unix seconds>,v1=<hex>" on %s; got %q.`, spec.signatureHeader, surface, preview(rawSignature)))
		}
		digests, rawTS, marker = v1, t, b
	} else {
		rest, ok := strings.CutPrefix(rawSignature, "sha256=")
		if !ok {
			return sigFail(surface, ReasonMalformedSignatureHeader, fmt.Sprintf(
				`%s should be "sha256=<hex>" on %s; got %q. A "t=…,v1=…" value here means you have the wrong surface — that is the other signature family.`,
				spec.signatureHeader, surface, preview(rawSignature)))
		}
		digests = []string{strings.TrimSpace(rest)}
	}

	for _, d := range digests {
		if !isHexSHA256(d) {
			msg := fmt.Sprintf("A digest in %s is not 64 hex characters (got %d).", spec.signatureHeader, len(d))
			if len(digests) > 1 {
				msg += fmt.Sprintf(" %d v1 values arrived; every one has to be well formed.", len(digests))
			}
			return sigFail(surface, ReasonMalformedSignatureHeader, msg)
		}
	}

	// ── The timestamp, and its unit ──────────────────────────────────────
	var timestampMs *int64
	if spec.unit != unitNone {
		if spec.timestampHeader != "" {
			ts := headerValues(in.Headers, spec.timestampHeader)
			if len(ts) == 0 {
				return sigFail(surface, ReasonMissingTimestamp, fmt.Sprintf(
					"No %s header. On %s the timestamp travels in its own header and is part of the signed string, so it cannot be reconstructed.",
					spec.timestampHeader, surface))
			}
			rawTS = strings.TrimSpace(ts[0])
		}
		if rawTS == "" {
			return sigFail(surface, ReasonMissingTimestamp, fmt.Sprintf("No timestamp found for %s.", surface))
		}
		// Digits only, checked first: ParseInt would accept "+17" and "-5".
		if !isDigits(rawTS) {
			return sigFail(surface, ReasonMalformedTimestamp, fmt.Sprintf(
				"The timestamp on %s is not a run of digits: %q.", surface, preview(rawTS)))
		}
		n, err := strconv.ParseInt(rawTS, 10, 64)
		// Overflow is malformed, not "outside tolerance": a value past int64
		// is not a time anyone signed, and n*1000 on a seconds surface must
		// not wrap into a plausible-looking instant. See the report on where
		// this differs from the TypeScript, which has no int64 to overflow.
		if err != nil || (spec.unit == unitSeconds && n > math.MaxInt64/1000) {
			return sigFail(surface, ReasonMalformedTimestamp, fmt.Sprintf(
				"The timestamp on %s does not fit a 64-bit millisecond count: %q.", surface, preview(rawTS)))
		}
		ms := n
		if spec.unit == unitSeconds {
			ms = n * 1000
		}
		timestampMs = &ms

		tolerance := in.Tolerance
		if tolerance == 0 {
			tolerance = ChatlySignatureToleranceSeconds * time.Second
		}
		if tolerance != ChatlySignatureToleranceUnlimited {
			now := in.Now
			if now.IsZero() {
				now = time.Now()
			}
			nowMs := now.UnixMilli()
			drift := absDiff(nowMs, ms)
			// Strictly greater: exactly the tolerance away is allowed.
			// Milliseconds() truncates, which is exact here because drift
			// is a whole number of milliseconds.
			if tolerance < 0 || drift > uint64(tolerance.Milliseconds()) {
				return sigFail(surface, ReasonTimestampOutsideTolerance, toleranceMessage(surface, spec, drift, ms > nowMs, tolerance))
			}
		}
	}

	// ── The base string, and the compare ─────────────────────────────────
	bases, refusal := candidateBases(in, spec, marker)
	if refusal != nil {
		return *refusal
	}
	for _, b := range bases {
		if b == BaseTarget && !in.hasTarget() {
			return sigFail(surface, ReasonMissingRequestTarget, fmt.Sprintf(
				"%s signs <t>.<METHOD>.<path>.<rawBody>, so both Method and Path are required. Path is pathname + search of the request as Chatly rendered it — not the host, not the scheme.",
				surface))
		}
	}

	given := make([][]byte, 0, len(digests))
	for _, d := range digests {
		decoded, err := hex.DecodeString(d)
		if err != nil {
			// Unreachable after isHexSHA256; a refusal rather than a skip
			// all the same.
			return sigFail(surface, ReasonMalformedSignatureHeader, "A digest could not be decoded as hex.")
		}
		given = append(given, decoded)
	}

	for _, b := range bases {
		prefix := basePrefix(b, rawTS, in)
		for _, s := range secrets {
			mac := hmac.New(sha256.New, []byte(s.value))
			mac.Write([]byte(prefix))
			mac.Write(in.Body)
			expected := mac.Sum(nil)
			for _, candidate := range given {
				// Lengths first, then constant time. hmac.Equal is already
				// safe on unequal lengths in Go, but the explicit check keeps
				// this line identical in intent to the seven other SDKs.
				if len(candidate) == len(expected) && hmac.Equal(candidate, expected) {
					return VerifyChatlySignatureResult{OK: true, Surface: surface, TimestampMs: timestampMs, Base: b, SecretIndex: s.index}
				}
			}
		}
	}

	msg := fmt.Sprintf(
		"The digest does not match for %s. The three usual causes, in order: a re-serialised body (pass the RAW bytes, not json.Marshal of the decoded value), the secret from a different surface, or the wrong surface — %s.",
		surface, spec.describe)
	if len(secrets) > 1 {
		msg += fmt.Sprintf(" None of the %d secrets matched.", len(secrets))
	}
	return sigFail(surface, ReasonSignatureMismatch, msg)
}

// toleranceMessage says which DIRECTION the drift is in. A stale timestamp is
// a replay or a slow queue; a far-future one is a sender clock or — far more
// often — the wrong unit, which lands thousands of years out and reads as
// "expired" unless something says so.
func toleranceMessage(surface ChatlySignatureSurface, spec surfaceSpec, driftMs uint64, future bool, tolerance time.Duration) string {
	direction := "in the past"
	if future {
		direction = "in the FUTURE"
	}
	msg := fmt.Sprintf("The timestamp on %s is %ds %s, outside the %ds tolerance.",
		surface, (driftMs+500)/1000, direction, int64(tolerance/time.Second))
	if years := float64(driftMs) / (1000 * 60 * 60 * 24 * 365); years > 1 {
		msg += fmt.Sprintf(" That is %.0f year(s) out, which is what a wrong UNIT looks like: %s sends %s.",
			years, surface, spec.unitName())
	}
	return msg
}

// preview clips an untrusted value so an error message stays one line. It
// cuts on a rune boundary: slicing a string by bytes can split a UTF-8
// sequence, and %q would then print an escape rather than the character.
func preview(value string) string {
	r := []rune(value)
	if len(r) > 48 {
		return string(r[:47]) + "…"
	}
	return value
}
