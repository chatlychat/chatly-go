package chatly

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"
)

// WebhookSignatureHeader is the header Chatly signs a delivery with.
//
// It is `x-livechat-signature`, NOT `x-chatly-signature`. The name predates
// the rename and is part of the wire contract, so it did not change with the
// product name; a published README once documented the other name and every
// handler written from it rejected every delivery.
const WebhookSignatureHeader = "x-livechat-signature"

// DefaultWebhookTolerance is how far the signed timestamp may be from now
// before a delivery is treated as a replay.
const DefaultWebhookTolerance = 300 * time.Second

// WebhookToleranceUnlimited disables the freshness check entirely. Use it only
// to re-verify an archived delivery; on a live endpoint it removes the replay
// protection that binding the timestamp into the digest exists to provide.
const WebhookToleranceUnlimited = time.Duration(math.MaxInt64)

// WebhookVerification is the input to VerifyWebhookSignature.
type WebhookVerification struct {
	// Body is the EXACT bytes that arrived. Read them before anything parses
	// them: re-serialising decoded JSON reorders keys and changes whitespace,
	// and the digest will not match. In net/http that means io.ReadAll on
	// r.Body, verify, and only then json.Unmarshal.
	Body []byte

	// SignatureHeader is the raw value of the x-livechat-signature header:
	// `t=<unix seconds>,v1=<hex>`.
	SignatureHeader string

	// Secret is the endpoint's `whsec_…` signing secret.
	Secret string

	// Tolerance is the freshness window. Zero means DefaultWebhookTolerance;
	// pass WebhookToleranceUnlimited to skip the check.
	Tolerance time.Duration

	// Now overrides the clock. Zero means time.Now(). For tests, and for
	// re-verifying a delivery against the moment it arrived.
	Now time.Time
}

// VerifyWebhookSignature reports whether a delivery was signed by the holder
// of Secret and arrived inside the tolerance window.
//
// The digest is HMAC-SHA256 over the bytes `<t>.<raw body>` — a literal dot
// between the timestamp and the body. The timestamp is bound INTO the digest;
// that is what makes the replay window enforceable, because an attacker
// replaying an old body cannot move `t` forward without invalidating `v1`.
//
// An earlier implementation in another language HMAC'd the body ALONE and so
// rejected 100% of genuine deliveries. There is a pinned test vector for the
// exact construction in webhooks_test.go; keep it.
//
// It returns false — it never panics and has no error return — for a
// malformed header, a missing field, a non-numeric or non-hex field, an empty
// secret, or a stale timestamp. That is deliberate: a verifier that panics is
// an ACCEPTED webhook in a handler whose recover() logs and returns 200, and
// a verifier with two failure channels invites a handler that checks one.
func VerifyWebhookSignature(v WebhookVerification) bool {
	if v.Secret == "" {
		// No secret cannot verify anything. Returning true here would make an
		// unconfigured endpoint accept every forgery, which is the worst
		// possible default.
		return false
	}

	rawTS, tag, ok := parseSignatureHeader(v.SignatureHeader)
	if !ok {
		return false
	}

	issuedAt, err := strconv.ParseInt(rawTS, 10, 64)
	if err != nil {
		return false
	}

	tolerance := v.Tolerance
	if tolerance == 0 {
		tolerance = DefaultWebhookTolerance
	}
	if tolerance != WebhookToleranceUnlimited {
		now := v.Now
		if now.IsZero() {
			now = time.Now()
		}
		// Absolute skew, so a clock ahead of ours is rejected too: a
		// timestamp from the future is either a broken producer or an
		// attacker buying themselves a window.
		drift := now.Unix() - issuedAt
		if drift < 0 {
			drift = -drift
		}
		if drift > int64(tolerance/time.Second) {
			return false
		}
	}

	expected, err := hex.DecodeString(tag)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(v.Secret))
	// rawTS, not a re-formatted issuedAt: the producer signed the exact
	// characters it sent. "01700000000" would re-format to a different
	// string and a different digest.
	mac.Write([]byte(rawTS))
	mac.Write([]byte{'.'})
	mac.Write(v.Body)
	actual := mac.Sum(nil)

	// Length first, then constant-time compare. Go's hmac.Equal is already
	// safe on unequal lengths (unlike some languages' compare, which raises),
	// but the explicit check documents the requirement and costs nothing.
	if len(expected) != len(actual) {
		return false
	}
	return hmac.Equal(expected, actual)
}

// VerifyWebhook is the one-line form for the common case: current clock,
// default 300s tolerance.
//
//	if !chatly.VerifyWebhook(body, r.Header.Get(chatly.WebhookSignatureHeader), secret) {
//	    http.Error(w, "bad signature", http.StatusBadRequest)
//	    return
//	}
func VerifyWebhook(body []byte, signatureHeader, secret string) bool {
	return VerifyWebhookSignature(WebhookVerification{
		Body:            body,
		SignatureHeader: signatureHeader,
		Secret:          secret,
	})
}

// SignWebhookPayload produces the header value Chatly would send for a body
// and timestamp.
//
// Exported for ONE reason: testing a handler. It lets a caller's own test
// suite build a genuine delivery without copying the construction (and
// copying it wrong, which is how this whole class of bug happens). It is not
// needed to receive webhooks.
func SignWebhookPayload(body []byte, secret string, at time.Time) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// parseSignatureHeader pulls `t` and `v1` out of `t=…,v1=…`.
//
// Unknown parts are ignored rather than rejected, so adding a `v2=` scheme
// later does not break every verifier in the field. A part without `=`, or
// with an empty name or value, makes the whole header malformed.
func parseSignatureHeader(header string) (ts, tag string, ok bool) {
	for _, part := range strings.Split(header, ",") {
		eq := strings.Index(part, "=")
		if eq <= 0 {
			continue
		}
		name := strings.TrimSpace(part[:eq])
		value := strings.TrimSpace(part[eq+1:])
		if value == "" {
			continue
		}
		switch name {
		case "t":
			ts = value
		case "v1":
			tag = value
		}
	}
	return ts, tag, ts != "" && tag != ""
}
