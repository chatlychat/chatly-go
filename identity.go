package chatly

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ErrEmptyIdentityInput is returned by IdentityHash for an empty secret or an
// empty external id.
//
// Both are rejected rather than hashed because both have a plausible accident
// behind them — an unset environment variable, a user record whose id field
// was never populated — and the hash of "" is a perfectly valid-looking
// digest that would let one anonymous visitor impersonate every other
// visitor whose id was also missing.
var ErrEmptyIdentityInput = errors.New("chatly: identity hash requires a non-empty secret and external id")

// IdentityHash computes the `userHash` the browser widget needs for identity
// verification: lowercase hex HMAC-SHA256 of externalID under the workspace's
// identity-verification secret.
//
// When a workspace turns identity verification on, the widget must prove the
// externalId it claims was issued by your backend rather than typed into a
// browser console — otherwise any visitor can open a chat as any customer.
// This is the backend half of that handshake:
//
//	hash, err := chatly.IdentityHash(os.Getenv("CHATLY_IDV_SECRET"), user.ID)
//	if err != nil { return err }
//	// render into the page:
//	//   Chatly('identify', { externalId: ..., userHash: ..., email: ... })
//
// The secret is a workspace-wide credential and must never reach a browser,
// a mobile app, or a log line. Only the digest crosses to the client; the
// digest is specific to one externalID and discloses nothing about the
// secret.
//
// It is NOT the webhook secret and NOT the API key — three different
// credentials, and substituting one produces a digest the widget rejects with
// no explanation of why.
func IdentityHash(secret, externalID string) (string, error) {
	if secret == "" || externalID == "" {
		return "", ErrEmptyIdentityInput
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(externalID))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
