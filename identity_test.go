package chatly

import (
	"errors"
	"strings"
	"testing"
)

// ── CONTRACT §10.8 ─────────────────────────────────────────────────────────
// "identity_hash matches a known vector. This vector is computed and
// cross-checked in two implementations. If yours disagrees, yours is wrong."
func TestIdentityHashPinnedVector(t *testing.T) {
	const (
		secret = "sek_test_secret"
		id     = "user_42"
		want   = "5e26772dce97d5ba995796e621f83f2e97e6f8c3f9386dbf153a98ab24c10271"
	)

	got, err := IdentityHash(secret, id)
	if err != nil {
		t.Fatalf("IdentityHash: %v", err)
	}
	if got != want {
		t.Fatalf("IdentityHash(%q, %q)\n got %s\nwant %s", secret, id, got, want)
	}
	if got != strings.ToLower(got) {
		t.Fatalf("digest must be lowercase hex, got %q", got)
	}
	if len(got) != 64 {
		t.Fatalf("digest is %d chars, want 64", len(got))
	}
}

// The arguments are (secret, externalID) in that order, and swapping them
// produces a different digest — so a test that only checks "64 hex chars"
// would pass with the arguments reversed. This pins the order.
func TestIdentityHashArgumentOrderMatters(t *testing.T) {
	forward, err := IdentityHash("sek_test_secret", "user_42")
	if err != nil {
		t.Fatalf("IdentityHash: %v", err)
	}
	reversed, err := IdentityHash("user_42", "sek_test_secret")
	if err != nil {
		t.Fatalf("IdentityHash: %v", err)
	}
	if forward == reversed {
		t.Fatal("HMAC is not symmetric; if these matched, something is hashing a concatenation")
	}
}

func TestIdentityHashRejectsEmptyInput(t *testing.T) {
	// An unset env var and a user record with no id are both plausible
	// accidents, and hashing "" would hand every one of them the same
	// valid-looking digest.
	for _, tc := range []struct{ secret, id string }{
		{"", "user_42"},
		{"sek_test_secret", ""},
		{"", ""},
	} {
		got, err := IdentityHash(tc.secret, tc.id)
		if !errors.Is(err, ErrEmptyIdentityInput) {
			t.Errorf("IdentityHash(%q, %q) err = %v, want ErrEmptyIdentityInput", tc.secret, tc.id, err)
		}
		if got != "" {
			t.Errorf("IdentityHash(%q, %q) returned a digest %q alongside an error", tc.secret, tc.id, got)
		}
	}
}

func TestIdentityHashIsStable(t *testing.T) {
	a, _ := IdentityHash("sek_x", "user_1")
	b, _ := IdentityHash("sek_x", "user_1")
	c, _ := IdentityHash("sek_x", "user_2")
	if a != b {
		t.Fatal("the same inputs produced two digests")
	}
	if a == c {
		t.Fatal("two different external ids produced the same digest")
	}
}
