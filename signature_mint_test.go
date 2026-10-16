package chatly

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// SignChatlyV2, and the three refusals the brief for this port calls out by
// name. Two kinds of test here and the distinction matters:
//
//   - PINNED: the exact base string and the exact digest, taken from the
//     cross-SDK corpus (testdata/signature_vectors.json) and recomputed by
//     hand. These are what make this a port rather than an independent
//     invention. A mint tested only by feeding its output to this package's
//     own verifier agrees with a wrong construction perfectly happily, which
//     is the failure mode this repository has already shipped once.
//   - ROUND-TRIP: mint, then verify, to prove the two halves meet. Useful,
//     but worthless without the pins above.

const (
	mintSecret = "whsec_vector_secret"
	mintBody   = `{"id":"evt_1","type":"message.created"}`
	mintSecs   = int64(1760000000)
	// The corpus's digest for the unified base over mintBody at mintSecs,
	// POST /hooks/chatly?tenant=acme. Identical to the v1 in the
	// "unified_v2 verifies t.METHOD.path.body" vector.
	mintTargetDigest = "666eb5b283d58737b989ca99a0652aba73754024def8bc1c857d5d179d4b640d"
	// The corpus's digest for the body base over mintBody at mintSecs,
	// shared by the agent_delivery, event_webhook and workflow_webhook
	// vectors — one construction, three headers.
	mintBodyDigest = "68c8081fadfddd5bded0385d719bd284e690ec14847d59a15a30816bf65c4f1e"
	// The corpus's digest for the ai_tool_call GET vector: an EMPTY body,
	// and a method the corpus supplies lower-case.
	mintGetDigest = "c44ee8addde7bed00977a7250a24913142f53f0ecb4fcab6cdf2ce485dc51b66"
	mintPath      = "/hooks/chatly?tenant=acme"
)

func mintAt() time.Time { return time.Unix(mintSecs, 0) }

// The base string, pinned as plaintext. A wrong base string and a wrong HMAC
// are the same symptom at a verifier and different bugs; this is the test
// that tells them apart, and the one to read when a language disagrees.
func TestChatlyV2BaseStringIsPinned(t *testing.T) {
	cases := []struct {
		name string
		in   SignChatlyV2Input
		want string
	}{
		{
			name: "target base",
			in:   SignChatlyV2Input{Base: MintBaseTarget, Body: []byte(mintBody), At: mintAt(), Method: "POST", Path: mintPath},
			want: "1760000000.POST./hooks/chatly?tenant=acme." + mintBody,
		},
		{
			name: "body base ignores method and path",
			in:   SignChatlyV2Input{Base: MintBaseBody, Body: []byte(mintBody), At: mintAt(), Method: "POST", Path: mintPath},
			want: "1760000000." + mintBody,
		},
		{
			// The method is upper-cased, and an empty body contributes
			// nothing after the final separator — so a GET signs a string
			// ending in ".", not one that silently loses it.
			name: "lower-case method and an empty body",
			in:   SignChatlyV2Input{Base: MintBaseTarget, At: mintAt(), Method: "get", Path: "/orders/4471?expand=lines"},
			want: "1760000000.GET./orders/4471?expand=lines.",
		},
		{
			name: "a nil body is the empty body, not a missing one",
			in:   SignChatlyV2Input{Base: MintBaseBody, Body: nil, At: mintAt()},
			want: "1760000000.",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, err := ChatlyV2BaseString(c.in)
			if err != nil {
				t.Fatalf("ChatlyV2BaseString: %v", err)
			}
			if got != c.want {
				t.Errorf("base string\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// The digests, pinned against the corpus every other SDK runs. These three
// values are the whole claim that this port agrees byte for byte.
func TestSignChatlyV2MatchesTheCorpusDigests(t *testing.T) {
	cases := []struct {
		name string
		in   SignChatlyV2Input
		want string
	}{
		{
			name: "target base",
			in:   SignChatlyV2Input{Base: MintBaseTarget, Secret: mintSecret, Body: []byte(mintBody), At: mintAt(), Method: "POST", Path: mintPath},
			want: "t=1760000000,v1=" + mintTargetDigest + ",b=t",
		},
		{
			name: "body base",
			in:   SignChatlyV2Input{Base: MintBaseBody, Secret: mintSecret, Body: []byte(mintBody), At: mintAt()},
			want: "t=1760000000,v1=" + mintBodyDigest + ",b=b",
		},
		{
			name: "the ai_tool_call GET vector",
			in:   SignChatlyV2Input{Base: MintBaseTarget, Secret: mintSecret, At: mintAt(), Method: "get", Path: "/orders/4471?expand=lines"},
			want: "t=1760000000,v1=" + mintGetDigest + ",b=t",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, err := SignChatlyV2(c.in)
			if err != nil {
				t.Fatalf("SignChatlyV2: %v", err)
			}
			if got != c.want {
				t.Errorf("header\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

// Sub-second precision is truncated, and the SAME truncated value is both
// signed and sent. If the two ever diverge the header verifies nowhere, and
// the symptom is indistinguishable from a wrong secret — this repository's
// own signing scheme shipped the mirror image of this bug once.
func TestSignChatlyV2TruncatesToWholeSecondsConsistently(t *testing.T) {
	in := SignChatlyV2Input{Base: MintBaseBody, Secret: mintSecret, Body: []byte(mintBody), At: mintAt().Add(999 * time.Millisecond)}
	got, err := SignChatlyV2(in)
	if err != nil {
		t.Fatalf("SignChatlyV2: %v", err)
	}
	// Identical to the whole-second case, digest included: proof the signed
	// string used the truncated t rather than a rounded or fractional one.
	if want := "t=1760000000,v1=" + mintBodyDigest + ",b=b"; got != want {
		t.Fatalf("header\n got %s\nwant %s", got, want)
	}
}

// mintedVerifies is the round-trip: whatever SignChatlyV2 produced is handed
// to this package's verifier on `surface`, and must be accepted over `base`.
func mintedVerifies(t *testing.T, surface ChatlySignatureSurface, in SignChatlyV2Input, wantBase ChatlySignatureBase) {
	t.Helper()
	value, err := SignChatlyV2(in)
	if err != nil {
		t.Fatalf("SignChatlyV2: %v", err)
	}
	res := VerifyChatlySignature(VerifyChatlySignatureInput{
		Surface: surface,
		Body:    in.Body,
		Headers: map[string][]string{ChatlyV2SignatureHeader: {value}},
		Secrets: []string{in.Secret},
		Method:  in.Method,
		Path:    in.Path,
		Now:     in.At,
	})
	if !res.OK {
		t.Fatalf("minted %s did not verify on %s: %s — %s", in.Base, surface, res.Reason, res.Message)
	}
	if res.Base != wantBase {
		t.Errorf("verified over base %q, want %q", res.Base, wantBase)
	}
}

func TestSignChatlyV2RoundTripsThroughTheVerifier(t *testing.T) {
	target := SignChatlyV2Input{Base: MintBaseTarget, Secret: mintSecret, Body: []byte(mintBody), At: mintAt(), Method: "POST", Path: mintPath}
	body := SignChatlyV2Input{Base: MintBaseBody, Secret: mintSecret, Body: []byte(mintBody), At: mintAt()}

	t.Run("unified_v2 accepts the target base", func(t *testing.T) {
		mintedVerifies(t, SurfaceUnifiedV2, target, BaseTarget)
	})
	t.Run("workflow_webhook accepts the target base", func(t *testing.T) {
		mintedVerifies(t, SurfaceWorkflowWebhook, target, BaseTarget)
	})
	t.Run("workflow_webhook accepts its own body base", func(t *testing.T) {
		mintedVerifies(t, SurfaceWorkflowWebhook, body, BaseBody)
	})
	t.Run("a legacy surface prefers the v2 header when given a target", func(t *testing.T) {
		// agent_delivery's own header is x-chatly-signature; the v2 value
		// arrives beside it and V2Prefer picks it up, reporting unified_v2.
		value, err := SignChatlyV2(target)
		if err != nil {
			t.Fatalf("SignChatlyV2: %v", err)
		}
		res := VerifyChatlySignature(VerifyChatlySignatureInput{
			Surface: SurfaceAgentDelivery,
			Body:    []byte(mintBody),
			Headers: map[string][]string{ChatlyV2SignatureHeader: {value}},
			Secrets: []string{mintSecret},
			Method:  "POST",
			Path:    mintPath,
			Now:     mintAt(),
		})
		if !res.OK {
			t.Fatalf("refused: %s — %s", res.Reason, res.Message)
		}
		if res.Surface != SurfaceUnifiedV2 {
			t.Errorf("surface = %q, want unified_v2 — a preferred v2 reports what actually verified", res.Surface)
		}
	})
}

// The mid-rotation dual-digest value: one t, two v1 fields, no marker. The
// sender signs with the old and the new secret; a receiver holding either
// one must accept it. This is the shape the workflow action emits, and it is
// built here from two REAL mints rather than from a literal, so it cannot
// drift from the construction.
func TestVerifyAcceptsADualDigestValueFromEitherSecret(t *testing.T) {
	const older, newer = "whsec_old", "whsec_new"
	digest := func(secret string) string {
		t.Helper()
		value, err := SignChatlyV2(SignChatlyV2Input{Base: MintBaseBody, Secret: secret, Body: []byte(mintBody), At: mintAt()})
		if err != nil {
			t.Fatalf("SignChatlyV2: %v", err)
		}
		// "t=…,v1=<hex>,b=b" → "<hex>"
		return strings.TrimSuffix(strings.SplitN(value, "v1=", 2)[1], ",b=b")
	}
	// No b= marker, which is what the workflow sender emits today and which
	// a verifier must read as the body base rather than as "unknown".
	dual := "t=1760000000,v1=" + digest(older) + ",v1=" + digest(newer)

	for name, secrets := range map[string][]string{
		"holding only the old secret": {older},
		"holding only the new secret": {newer},
		"holding both, old first":     {older, newer},
		"holding both, new first":     {newer, older},
	} {
		secrets := secrets
		t.Run(name, func(t *testing.T) {
			res := VerifyChatlySignature(VerifyChatlySignatureInput{
				Surface: SurfaceWorkflowWebhook,
				Body:    []byte(mintBody),
				Headers: map[string][]string{ChatlyV2SignatureHeader: {dual}},
				Secrets: secrets,
				Now:     mintAt(),
			})
			if !res.OK {
				t.Fatalf("dual-digest value refused: %s — %s", res.Reason, res.Message)
			}
			if res.Base != BaseBody {
				t.Errorf("base = %q, want body (an absent marker means the body base)", res.Base)
			}
		})
	}

	// And the control: a value carrying two digests from secrets the
	// receiver does NOT hold is still a mismatch, so the loop above is not
	// passing because any two-digest value is waved through.
	res := VerifyChatlySignature(VerifyChatlySignatureInput{
		Surface: SurfaceWorkflowWebhook,
		Body:    []byte(mintBody),
		Headers: map[string][]string{ChatlyV2SignatureHeader: {dual}},
		Secrets: []string{"whsec_neither"},
		Now:     mintAt(),
	})
	if res.OK || res.Reason != ReasonSignatureMismatch {
		t.Fatalf("an unheld secret pair: OK=%v reason=%q, want a signature_mismatch", res.OK, res.Reason)
	}
}

// ── The three hard requirements, stated as tests ────────────────────────────

// A one-byte body change must fail. The corpus's "re-serialised body" vector
// changes whitespace in several places at once; this changes exactly one byte
// in the middle of a value, which is the tamper a length check or a prefix
// comparison would miss.
func TestOneByteBodyChangeFails(t *testing.T) {
	value, err := SignChatlyV2(SignChatlyV2Input{Base: MintBaseTarget, Secret: mintSecret, Body: []byte(mintBody), At: mintAt(), Method: "POST", Path: mintPath})
	if err != nil {
		t.Fatalf("SignChatlyV2: %v", err)
	}
	tampered := strings.Replace(mintBody, `"evt_1"`, `"evt_2"`, 1)
	if len(tampered) != len(mintBody) {
		t.Fatalf("the tamper changed the length (%d vs %d); it must be one byte so a length check cannot catch it", len(tampered), len(mintBody))
	}
	verify := func(body string) VerifyChatlySignatureResult {
		return VerifyChatlySignature(VerifyChatlySignatureInput{
			Surface: SurfaceUnifiedV2,
			Body:    []byte(body),
			Headers: map[string][]string{ChatlyV2SignatureHeader: {value}},
			Secrets: []string{mintSecret},
			Method:  "POST",
			Path:    mintPath,
			Now:     mintAt(),
		})
	}
	// The control, in the same test: the untampered body DOES verify, so the
	// refusal below is the one changed byte's doing and not a broken setup.
	if res := verify(mintBody); !res.OK {
		t.Fatalf("the untampered body was refused: %s — %s", res.Reason, res.Message)
	}
	res := verify(tampered)
	if res.OK {
		t.Fatalf("a one-byte body change verified")
	}
	if res.Reason != ReasonSignatureMismatch {
		t.Errorf("reason = %q, want signature_mismatch", res.Reason)
	}
}

// A t 301 seconds old must give timestamp_outside_tolerance — and 300
// exactly must not, because the boundary is where an off-by-one lives and
// "stale" is otherwise indistinguishable from a clock drift of one second.
func TestTimestampToleranceBoundaryIs300Seconds(t *testing.T) {
	value, err := SignChatlyV2(SignChatlyV2Input{Base: MintBaseTarget, Secret: mintSecret, Body: []byte(mintBody), At: mintAt(), Method: "POST", Path: mintPath})
	if err != nil {
		t.Fatalf("SignChatlyV2: %v", err)
	}
	at := func(offset time.Duration) VerifyChatlySignatureResult {
		return VerifyChatlySignature(VerifyChatlySignatureInput{
			Surface: SurfaceUnifiedV2,
			Body:    []byte(mintBody),
			Headers: map[string][]string{ChatlyV2SignatureHeader: {value}},
			Secrets: []string{mintSecret},
			Method:  "POST",
			Path:    mintPath,
			Now:     mintAt().Add(offset),
		})
	}
	for _, offset := range []time.Duration{299 * time.Second, 300 * time.Second, -300 * time.Second} {
		if res := at(offset); !res.OK {
			t.Errorf("%s inside the window was refused: %s — %s", offset, res.Reason, res.Message)
		}
	}
	// Both directions: a far-FUTURE t is a sender clock or the wrong unit,
	// and reads as "expired" unless it is refused for its own reason.
	for _, offset := range []time.Duration{301 * time.Second, -301 * time.Second} {
		res := at(offset)
		if res.OK {
			t.Errorf("%s outside the window verified", offset)
			continue
		}
		if res.Reason != ReasonTimestampOutsideTolerance {
			t.Errorf("%s: reason = %q, want timestamp_outside_tolerance", offset, res.Reason)
		}
	}
}

// Two x-chatly-signature-v2 headers must give duplicate_signature_header.
//
// The corpus pins this on x-livechat-signature only, where the check is
// reached directly. On the V2 header it is reached through the v2 policy —
// applyV2Policy sees one header PRESENT and delegates to unified_v2, which is
// where the count happens — so this is a genuinely different path, and on a
// legacy surface the refusal is reported against unified_v2 rather than
// against the surface asked for.
func TestDuplicateV2HeaderIsRefused(t *testing.T) {
	value, err := SignChatlyV2(SignChatlyV2Input{Base: MintBaseTarget, Secret: mintSecret, Body: []byte(mintBody), At: mintAt(), Method: "POST", Path: mintPath})
	if err != nil {
		t.Fatalf("SignChatlyV2: %v", err)
	}
	cases := []struct {
		name        string
		surface     ChatlySignatureSurface
		headers     map[string][]string
		wantSurface ChatlySignatureSurface
	}{
		{
			name:        "on unified_v2, the same value twice",
			surface:     SurfaceUnifiedV2,
			headers:     map[string][]string{ChatlyV2SignatureHeader: {value, value}},
			wantSurface: SurfaceUnifiedV2,
		},
		{
			name:        "on unified_v2, under two casings",
			surface:     SurfaceUnifiedV2,
			headers:     map[string][]string{ChatlyV2SignatureHeader: {value}, "X-Chatly-Signature-V2": {value}},
			wantSurface: SurfaceUnifiedV2,
		},
		{
			// Reported against unified_v2: that is what was being verified.
			name:        "through the v2 policy on a legacy surface",
			surface:     SurfaceAgentDelivery,
			headers:     map[string][]string{ChatlyV2SignatureHeader: {value, value}},
			wantSurface: SurfaceUnifiedV2,
		},
		{
			name:        "on workflow_webhook, whose own header IS the v2 header",
			surface:     SurfaceWorkflowWebhook,
			headers:     map[string][]string{ChatlyV2SignatureHeader: {value, value}},
			wantSurface: SurfaceWorkflowWebhook,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			res := VerifyChatlySignature(VerifyChatlySignatureInput{
				Surface: c.surface,
				Body:    []byte(mintBody),
				Headers: c.headers,
				Secrets: []string{mintSecret},
				Method:  "POST",
				Path:    mintPath,
				Now:     mintAt(),
			})
			if res.OK {
				t.Fatalf("two v2 headers verified")
			}
			if res.Reason != ReasonDuplicateSignatureHeader {
				t.Fatalf("reason = %q, want duplicate_signature_header — %s", res.Reason, res.Message)
			}
			if res.Surface != c.wantSurface {
				t.Errorf("surface = %q, want %q", res.Surface, c.wantSurface)
			}
		})
	}

	// The control: ONE of those headers verifies. Without this the test
	// above would pass against a verifier that refuses the v2 header
	// outright, which is a different bug with the same green tick.
	res := VerifyChatlySignature(VerifyChatlySignatureInput{
		Surface: SurfaceAgentDelivery,
		Body:    []byte(mintBody),
		Headers: map[string][]string{ChatlyV2SignatureHeader: {value}},
		Secrets: []string{mintSecret},
		Method:  "POST",
		Path:    mintPath,
		Now:     mintAt(),
	})
	if !res.OK {
		t.Fatalf("a single v2 header was refused: %s — %s", res.Reason, res.Message)
	}
}

// ── Minting refusals ───────────────────────────────────────────────────────

// Every way of asking for a signature that would cover the wrong bytes is an
// error, not a quietly-wrong digest. Each case asserts BOTH functions,
// because ChatlyV2BaseString is the one a cross-language comparison calls
// and it must not be the lenient door.
func TestSignChatlyV2RefusesAnInputThatWouldSignTheWrongString(t *testing.T) {
	cases := map[string]struct {
		in   SignChatlyV2Input
		want string // a fragment of the explanation, so the message stays useful
	}{
		"a zero Base": {
			in:   SignChatlyV2Input{Secret: mintSecret, Body: []byte(mintBody), At: mintAt()},
			want: "indistinguishable on the wire",
		},
		"an unknown Base": {
			in:   SignChatlyV2Input{Base: "both", Secret: mintSecret, At: mintAt()},
			want: "MintBaseTarget or MintBaseBody",
		},
		"the target base with no Method": {
			in:   SignChatlyV2Input{Base: MintBaseTarget, Secret: mintSecret, At: mintAt(), Path: mintPath},
			want: "both Method and Path are required",
		},
		"the target base with no Path": {
			in:   SignChatlyV2Input{Base: MintBaseTarget, Secret: mintSecret, At: mintAt(), Method: "POST"},
			want: "both Method and Path are required",
		},
		"a pre-1970 At": {
			in:   SignChatlyV2Input{Base: MintBaseBody, Secret: mintSecret, At: time.Unix(-1, 0)},
			want: "run of digits",
		},
	}
	for name, c := range cases {
		c := c
		t.Run(name, func(t *testing.T) {
			for fn, call := range map[string]func(SignChatlyV2Input) (string, error){
				"SignChatlyV2":       SignChatlyV2,
				"ChatlyV2BaseString": ChatlyV2BaseString,
			} {
				got, err := call(c.in)
				if err == nil {
					t.Fatalf("%s returned %q and no error", fn, got)
				}
				if got != "" {
					t.Errorf("%s returned %q beside its error; a half-answer invites being used", fn, got)
				}
				if !errors.Is(err, ErrChatlyV2Mint) {
					t.Errorf("%s: %v does not wrap ErrChatlyV2Mint, so errors.Is cannot match it", fn, err)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("%s: %q does not explain the problem (wanted a mention of %q)", fn, err, c.want)
				}
			}
		})
	}

	// An EMPTY secret is deliberately not in that list: HMAC takes a
	// zero-length key, the reference mints with one, and a test proving a
	// verifier answers missing_secret has to be able to produce the input.
	if _, err := SignChatlyV2(SignChatlyV2Input{Base: MintBaseBody, Secret: "", Body: []byte(mintBody), At: mintAt()}); err != nil {
		t.Errorf("an empty secret should mint, not refuse: %v", err)
	}
}
