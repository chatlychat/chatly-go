package chatly

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Minting a v2 signature — the other direction from signatures.go.
//
// Verifying is what a customer's handler does and it lives next door.
// MINTING is what a test harness does: to prove a verifier accepts a genuine
// signature you have to be able to produce one, and a test that hard-codes a
// hex digest proves only that the digest has not changed, not that it is the
// right digest for the string it claims to cover.
//
// This mirrors @chatlychat/sdk-node's `signChatlyV2` / `chatlyV2BaseString`
// (packages/sdk-node/src/signature-mint.ts) so the two can be held against
// each other: ChatlyV2BaseString's own tests pin the exact signed string and
// the exact digest from the cross-SDK corpus, which is the only way a port
// can be shown to agree byte for byte rather than merely to round-trip
// against itself. A self-consistent signer and verifier agree on a wrong
// construction perfectly happily — that is how an earlier implementation in
// this repository shipped a body-only digest under a header that documented
// a timestamped one.
//
// ═══ WHY IT RETURNS AN ERROR ═════════════════════════════════════════════
//
// VerifyChatlySignature deliberately never fails loudly; a verifier that
// panics is an accepted webhook in a handler with no recover. Minting is the
// opposite situation. There is no caller to protect — the caller IS the test
// — and every way of getting this wrong produces a signature that is
// perfectly well formed and covers the wrong bytes:
//
//   - a zero-valued Base would sign one of the two constructions by
//     accident, and the two are INDISTINGUISHABLE on the wire without the
//     b= marker this function emits. That is the whole reason the marker
//     exists, so there is no default and omitting it is an error, exactly as
//     the reference implementation requires the field.
//   - MintBaseTarget with no Method or Path signs "<t>..." — identical for
//     every request that surface ever makes and replayable against any path.
//     That is the precise weakness the target base was introduced to close
//     (see signature_table.go), so minting one silently would manufacture
//     the bug the construction exists to prevent.
//   - a pre-1970 At renders a negative t, which every verifier in this
//     family refuses as malformed_timestamp because a timestamp is a run of
//     digits. Minting it would produce a signature nothing can ever accept
//     and send whoever wrote the test hunting their secret.
//
// Rejected alternative: SignChatlyV2Target / SignChatlyV2Body as two
// functions, which makes the base impossible to omit and needs no error. It
// was not taken because `sign_chatly_v2` is the cross-SDK name this helper
// is ported under in all eight backend SDKs, and two Go-only names would
// make the one function a reader compares against the reference the one
// function that is not there.

// ChatlyV2MintBase is which string a v2 signature is taken over.
//
// A named string type rather than a bool, because "which of two
// constructions" is what this is, and a third would be a constant here
// instead of a second parameter everywhere.
type ChatlyV2MintBase string

const (
	// MintBaseTarget signs `<t>.<METHOD>.<path>.<rawBody>` — the unified
	// base — and emits `b=t`.
	MintBaseTarget ChatlyV2MintBase = "target"
	// MintBaseBody signs `<t>.<rawBody>` and emits `b=b`. This is the
	// workflow action's pre-unified construction; mint it to test a receiver
	// that still has to accept one.
	MintBaseBody ChatlyV2MintBase = "body"
)

// ErrChatlyV2Mint is the sentinel every SignChatlyV2 / ChatlyV2BaseString
// failure wraps, so a caller can errors.Is it without matching on text.
var ErrChatlyV2Mint = errors.New("chatly: cannot mint a v2 signature")

// SignChatlyV2Input is the input to SignChatlyV2 and ChatlyV2BaseString.
type SignChatlyV2Input struct {
	// Base is which string to sign. Required; there is no default. See
	// ChatlyV2MintBase and this file's header.
	Base ChatlyV2MintBase

	// Secret is the signing secret for the surface being imitated. An empty
	// secret is NOT an error here: HMAC accepts a zero-length key, the
	// reference implementation signs with one, and a verifier refusing an
	// empty secret as missing_secret is a case worth being able to mint.
	Secret string

	// Body is the exact bytes that will be sent. A nil Body is the empty
	// body, which is what a GET signs.
	Body []byte

	// At is when the signature is being minted. Only whole seconds reach the
	// wire — the v2 construction's t is Unix SECONDS — so a sub-second part
	// is truncated, and the TRUNCATED value is what gets signed. Signing
	// at.Unix() while sending a rounded t is the mirror-image bug that once
	// shipped in this repository's own signing scheme and refused 100% of
	// real traffic; there is one conversion here and both uses read it.
	At time.Time

	// Method is the request's HTTP method, upper-cased before signing, so a
	// test may pass "get" exactly as the corpus does. Required for
	// MintBaseTarget, ignored for MintBaseBody.
	Method string

	// Path is pathname + search of the request line (r.URL.RequestURI()),
	// never the scheme or the host. Required for MintBaseTarget, ignored for
	// MintBaseBody.
	Path string
}

// timestamp is the single rendering of At that both the signed string and
// the header use.
func (in SignChatlyV2Input) timestamp() (string, error) {
	seconds := in.At.Unix()
	if seconds < 0 {
		return "", fmt.Errorf("%w: At is %s, which renders t=%d — every verifier in this family requires t to be a run of digits and would refuse it as malformed_timestamp", ErrChatlyV2Mint, in.At.UTC().Format(time.RFC3339), seconds)
	}
	return strconv.FormatInt(seconds, 10), nil
}

// ChatlyV2BaseString is the exact string the MAC is taken over, exported so
// a test can pin it independently of the digest.
//
// Worth having separately: a wrong base string and a wrong HMAC are the same
// symptom at the verifier ("signature mismatch") and completely different
// bugs, and pinning the plaintext is what tells them apart. It is also the
// value to print when a cross-language comparison disagrees.
func ChatlyV2BaseString(in SignChatlyV2Input) (string, error) {
	ts, err := in.timestamp()
	if err != nil {
		return "", err
	}
	switch in.Base {
	case MintBaseBody:
		return ts + "." + string(in.Body), nil
	case MintBaseTarget:
		if in.Method == "" || in.Path == "" {
			return "", fmt.Errorf("%w: MintBaseTarget signs <t>.<METHOD>.<path>.<rawBody>, so both Method and Path are required; got Method=%q Path=%q. Signing without them yields \"<t>...\" — the same string for every request on the surface, replayable against any path, which is the weakness the target base exists to close", ErrChatlyV2Mint, in.Method, in.Path)
		}
		return ts + "." + strings.ToUpper(in.Method) + "." + in.Path + "." + string(in.Body), nil
	default:
		return "", fmt.Errorf("%w: Base is %q; it must be MintBaseTarget or MintBaseBody. There is no default: the two constructions are indistinguishable on the wire without the b= marker this emits, so a guess here would sign one and label it the other", ErrChatlyV2Mint, string(in.Base))
	}
}

// SignChatlyV2 returns the x-chatly-signature-v2 value for one request:
// `t=<unix seconds>,v1=<hex>,b=<t|b>`.
//
// `b=t` for the unified base and `b=b` for the body base. An ABSENT marker
// means the body base to a verifier — every signature minted before the
// marker existed was that construction — but this always emits one, because
// there is no reason for something minted today to be ambiguous.
//
// To mint the dual-digest value a sender emits mid key-rotation, call it
// once per secret and join the v1 fields; VerifyChatlySignature accepts a
// value carrying several v1s when any one of them matches.
func SignChatlyV2(in SignChatlyV2Input) (string, error) {
	base, err := ChatlyV2BaseString(in)
	if err != nil {
		return "", err
	}
	ts, err := in.timestamp()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(in.Secret))
	// Write cannot fail: hash.Hash's contract is that it never returns an
	// error, which is why the reference ignores it too.
	mac.Write([]byte(base))
	marker := "b"
	if in.Base == MintBaseTarget {
		marker = "t"
	}
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil)) + ",b=" + marker, nil
}
