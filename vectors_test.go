package chatly

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

// TestPrintCrossLanguageVectors prints the digests this implementation
// produces, so they can be compared against the other language SDKs by eye
// or by a gate:
//
//	go test -run TestPrintCrossLanguageVectors -v
//
// It ASSERTS the pinned values as well as printing them — a printer that
// cannot fail is not a test, and this one has to fail if a refactor changes a
// digest. The assertions duplicate TestIdentityHashPinnedVector and
// TestPinnedWebhookVector on purpose: those two are the contract's required
// tests and must stand alone.
func TestPrintCrossLanguageVectors(t *testing.T) {
	identity, err := IdentityHash("sek_test_secret", "user_42")
	if err != nil {
		t.Fatalf("IdentityHash: %v", err)
	}
	t.Logf("identity_hash(%q, %q) = %s", "sek_test_secret", "user_42", identity)
	if identity != "5e26772dce97d5ba995796e621f83f2e97e6f8c3f9386dbf153a98ab24c10271" {
		t.Fatalf("identity vector drifted: %s", identity)
	}

	const body = `{"id":"evt_1"}`
	header := SignWebhookPayload([]byte(body), "whsec_test", time.Unix(1700000000, 0))
	t.Logf("webhook header for secret=%q body=%s t=1700000000", "whsec_test", body)
	t.Logf("  %s", header)
	if header != "t=1700000000,v1=c89214b5b5da833daed6f0b8c5bb6bd58cea9022bd80ccc78230f3942d632925" {
		t.Fatalf("webhook vector drifted: %s", header)
	}

	// The wrong construction, printed so the two are never confused when
	// comparing across languages: this is HMAC over the body with no
	// timestamp, which an earlier Node implementation shipped.
	mac := hmac.New(sha256.New, []byte("whsec_test"))
	mac.Write([]byte(body))
	t.Logf("body-only digest (MUST be rejected) = %s", hex.EncodeToString(mac.Sum(nil)))
}
