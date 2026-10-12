package chatly

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bodyOnlyDigest is the WRONG construction — HMAC over the body with no
// timestamp — kept here on purpose. An earlier Node implementation did this
// and so rejected 100% of genuine deliveries; a verifier that accepts it
// instead would accept an infinitely replayable delivery. Both directions are
// asserted below.
func bodyOnlyDigest(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ── CONTRACT §10.9 ─────────────────────────────────────────────────────────
// The pinned delivery, verbatim. These three constants are cross-checked
// against the other language SDKs by the parity gate; if this test fails,
// this implementation is wrong, not the vector.
const (
	pinnedSecret = "whsec_test"
	pinnedBody   = `{"id":"evt_1"}`
	pinnedHeader = "t=1700000000,v1=c89214b5b5da833daed6f0b8c5bb6bd58cea9022bd80ccc78230f3942d632925"
	pinnedNow    = 1700000000
)

func TestPinnedWebhookVector(t *testing.T) {
	if len(pinnedBody) != 14 {
		t.Fatalf("the pinned body must be exactly those 14 bytes, got %d", len(pinnedBody))
	}

	// With a fixed clock at the signed instant.
	if !VerifyWebhookSignature(WebhookVerification{
		Body:            []byte(pinnedBody),
		SignatureHeader: pinnedHeader,
		Secret:          pinnedSecret,
		Now:             time.Unix(pinnedNow, 0),
	}) {
		t.Fatal("rejected the pinned delivery with a fixed now — the digest construction is wrong")
	}

	// And with tolerance disabled, which is how an archived delivery is
	// re-verified years later.
	if !VerifyWebhookSignature(WebhookVerification{
		Body:            []byte(pinnedBody),
		SignatureHeader: pinnedHeader,
		Secret:          pinnedSecret,
		Tolerance:       WebhookToleranceUnlimited,
	}) {
		t.Fatal("rejected the pinned delivery with unlimited tolerance")
	}

	// The same delivery signed over the BODY ALONE must be rejected.
	wrong := "t=" + strconv.Itoa(pinnedNow) + ",v1=" + bodyOnlyDigest(pinnedSecret, []byte(pinnedBody))
	if VerifyWebhookSignature(WebhookVerification{
		Body:            []byte(pinnedBody),
		SignatureHeader: wrong,
		Secret:          pinnedSecret,
		Now:             time.Unix(pinnedNow, 0),
	}) {
		t.Fatal("ACCEPTED a digest taken over the body alone: the timestamp is not bound into the digest, so the replay window is unenforceable")
	}

	// Belt and braces: our own recomputation must equal the pinned hex.
	mac := hmac.New(sha256.New, []byte(pinnedSecret))
	mac.Write([]byte("1700000000."))
	mac.Write([]byte(pinnedBody))
	if got := hex.EncodeToString(mac.Sum(nil)); !strings.HasSuffix(pinnedHeader, got) {
		t.Fatalf("recomputed digest %s does not match the pinned header", got)
	}
}

// ── CONTRACT §10.7 ─────────────────────────────────────────────────────────
// "accepts a genuine t=…,v1= built over <t>.<body>, rejects a digest over the
// body alone, rejects a stale t, rejects a malformed header, and does not
// throw on any of them."
//
// In Go "does not throw" means does not panic: every subtest below would fail
// the whole run on a panic, and the malformed-header table is where one would
// come from.
func TestVerifyWebhookSignature(t *testing.T) {
	secret := "whsec_live_abc"
	body := []byte(`{"event":"conversation.created","data":{"id":"cv_1"}}`)
	now := time.Unix(1_760_000_000, 0)
	genuine := SignWebhookPayload(body, secret, now)

	t.Run("accepts a genuine delivery", func(t *testing.T) {
		if !VerifyWebhookSignature(WebhookVerification{
			Body: body, SignatureHeader: genuine, Secret: secret, Now: now,
		}) {
			t.Fatal("rejected a genuine delivery")
		}
	})

	t.Run("rejects the body-alone digest", func(t *testing.T) {
		header := "t=" + strconv.FormatInt(now.Unix(), 10) + ",v1=" + bodyOnlyDigest(secret, body)
		if VerifyWebhookSignature(WebhookVerification{
			Body: body, SignatureHeader: header, Secret: secret, Now: now,
		}) {
			t.Fatal("accepted a body-only digest")
		}
	})

	t.Run("rejects a stale timestamp", func(t *testing.T) {
		// Genuine signature, 301 seconds old, default 300s tolerance.
		if VerifyWebhookSignature(WebhookVerification{
			Body: body, SignatureHeader: genuine, Secret: secret, Now: now.Add(301 * time.Second),
		}) {
			t.Fatal("accepted a delivery outside the tolerance window")
		}
		// Just inside is still good.
		if !VerifyWebhookSignature(WebhookVerification{
			Body: body, SignatureHeader: genuine, Secret: secret, Now: now.Add(299 * time.Second),
		}) {
			t.Fatal("rejected a delivery inside the tolerance window")
		}
		// And a timestamp from the FUTURE is skew too — otherwise an
		// attacker simply signs for tomorrow.
		if VerifyWebhookSignature(WebhookVerification{
			Body: body, SignatureHeader: genuine, Secret: secret, Now: now.Add(-301 * time.Second),
		}) {
			t.Fatal("accepted a delivery 301s in the future")
		}
	})

	t.Run("rejects a tampered body", func(t *testing.T) {
		if VerifyWebhookSignature(WebhookVerification{
			Body: append(body, ' '), SignatureHeader: genuine, Secret: secret, Now: now,
		}) {
			t.Fatal("accepted a body with a single trailing space added — this is what re-serialising parsed JSON looks like")
		}
	})

	t.Run("rejects the wrong secret", func(t *testing.T) {
		if VerifyWebhookSignature(WebhookVerification{
			Body: body, SignatureHeader: genuine, Secret: "whsec_other", Now: now,
		}) {
			t.Fatal("accepted a signature made with a different secret")
		}
	})

	t.Run("rejects an empty secret", func(t *testing.T) {
		// An unconfigured endpoint must verify NOTHING, not everything.
		if VerifyWebhookSignature(WebhookVerification{
			Body: body, SignatureHeader: genuine, Secret: "", Now: now,
		}) {
			t.Fatal("accepted a delivery with no secret configured")
		}
	})

	t.Run("rejects malformed headers without panicking", func(t *testing.T) {
		for _, header := range []string{
			"",
			"garbage",
			"t=",
			"v1=",
			"t=,v1=",
			"t=1760000000",                 // no v1
			"v1=deadbeef",                  // no t
			"t=abc,v1=deadbeef",            // non-numeric t
			"t=1760000000,v1=nothexatall!", // non-hex digest
			"t=1760000000,v1=aa",           // right hex, wrong length
			"t=1760000000;v1=deadbeef",     // wrong separator
			"=1760000000,v1=deadbeef",      // empty name
			",,,",
			"t=1760000000,v1=" + strings.Repeat("a", 64), // well-formed, wrong value
			"t=9999999999999999999999,v1=deadbeef",       // t overflows int64
		} {
			if VerifyWebhookSignature(WebhookVerification{
				Body: body, SignatureHeader: header, Secret: secret, Now: now,
			}) {
				t.Errorf("accepted malformed header %q", header)
			}
		}
	})

	t.Run("ignores unknown parts and whitespace", func(t *testing.T) {
		// A future v2= scheme, or a producer that pads with spaces, must not
		// break every verifier in the field.
		padded := strings.ReplaceAll(genuine, ",", " , ") + ",v2=future"
		if !VerifyWebhookSignature(WebhookVerification{
			Body: body, SignatureHeader: padded, Secret: secret, Now: now,
		}) {
			t.Fatalf("rejected a padded header with an extra scheme: %q", padded)
		}
	})

	t.Run("VerifyWebhook convenience form", func(t *testing.T) {
		fresh := SignWebhookPayload(body, secret, time.Now())
		if !VerifyWebhook(body, fresh, secret) {
			t.Fatal("the three-argument form rejected a fresh genuine delivery")
		}
		if VerifyWebhook(body, genuine, secret) {
			t.Fatal("the three-argument form accepted a 2025 timestamp against the real clock")
		}
	})
}

func TestWebhookSignatureHeaderName(t *testing.T) {
	// x-livechat-signature, not x-chatly-signature: a README once documented
	// the latter and every handler written from it rejected everything.
	if WebhookSignatureHeader != "x-livechat-signature" {
		t.Fatalf("WebhookSignatureHeader = %q", WebhookSignatureHeader)
	}
}

// The documented http.HandlerFunc shape, exercised end to end — including the
// part that actually bites: verify the BYTES, then unmarshal.
func TestWebhookHandlerIntegration(t *testing.T) {
	secret := "whsec_handler"
	var accepted int

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		if !VerifyWebhook(raw, r.Header.Get(WebhookSignatureHeader), secret) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		accepted++
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	post := func(body string, header string) int {
		req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set(WebhookSignatureHeader, header)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	body := `{"event":"ticket.created","data":{"id":"tk_1"}}`
	good := SignWebhookPayload([]byte(body), secret, time.Now())
	if code := post(body, good); code != http.StatusOK {
		t.Fatalf("genuine delivery got %d", code)
	}
	if code := post(body, "t=1,v1=deadbeef"); code != http.StatusUnauthorized {
		t.Fatalf("forged delivery got %d, want 401", code)
	}
	// Re-serialised JSON: same data, different bytes. This is the mistake the
	// contract warns about, and it must fail closed.
	reordered := `{"data":{"id":"tk_1"},"event":"ticket.created"}`
	if code := post(reordered, good); code != http.StatusUnauthorized {
		t.Fatalf("re-serialised body got %d, want 401", code)
	}
	if accepted != 1 {
		t.Fatalf("handler accepted %d deliveries, want 1", accepted)
	}
}

func TestSignWebhookPayloadRoundTrip(t *testing.T) {
	// SignWebhookPayload exists so a caller's own tests can build a genuine
	// delivery; it has to agree with the verifier beside it.
	at := time.Unix(1_770_000_000, 0)
	header := SignWebhookPayload([]byte("{}"), "whsec_rt", at)
	if !strings.HasPrefix(header, "t=1770000000,v1=") {
		t.Fatalf("header = %q", header)
	}
	if len(header) != len("t=1770000000,v1=")+64 {
		t.Fatalf("digest is not 64 hex chars: %q", header)
	}
	if !VerifyWebhookSignature(WebhookVerification{
		Body: []byte("{}"), SignatureHeader: header, Secret: "whsec_rt", Now: at,
	}) {
		t.Fatal("the signer and the verifier disagree")
	}
}

// An empty body is a legal delivery and must verify, not short-circuit.
func TestVerifyEmptyBody(t *testing.T) {
	at := time.Unix(1_770_000_001, 0)
	header := SignWebhookPayload(nil, "whsec_empty", at)
	if !VerifyWebhookSignature(WebhookVerification{
		Body: nil, SignatureHeader: header, Secret: "whsec_empty", Now: at,
	}) {
		t.Fatal("rejected a genuinely signed empty body")
	}
}
