package chatly

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The cross-SDK corpus. This file is a byte-identical copy of the one every
// other SDK runs, and sdk-node's spec fails if any copy drifts — so it is
// read, never edited, here. The path is relative to this package directory,
// which is also the repository root in the public mirror; it must never
// reach for ../sdk-node or ../sdk-spec, which do not exist there.
const signatureCorpusPath = "testdata/signature_vectors.json"

// signatureCorpusFloor: a loader that silently reads zero vectors passes
// every per-vector assertion.
//
// It is the corpus's CURRENT size, and keeping it there is the whole point.
// It sat at 59 — the size when this port was written — while the corpus held
// 61, so for two vectors a loader that dropped them passed. A floor below
// the real count is a gate that has already gone part-way blind; raise this
// whenever the corpus grows, which sdk-node's spec makes impossible to do
// silently because every copy must stay byte-identical.
const signatureCorpusFloor = 61

type corpusVector struct {
	Name  string `json:"name"`
	Input struct {
		Surface                string                     `json:"surface"`
		Body                   string                     `json:"body"`
		Headers                map[string]json.RawMessage `json:"headers"`
		Secret                 json.RawMessage            `json:"secret"`
		Method                 *string                    `json:"method"`
		Path                   *string                    `json:"path"`
		V2                     *string                    `json:"v2"`
		AllowUnprotectedReplay *bool                      `json:"allowUnprotectedReplay"`
		ToleranceSeconds       *int64                     `json:"toleranceSeconds"`
		NowMs                  int64                      `json:"nowMs"`
	} `json:"input"`
	// Every field is decoded as raw JSON so the test can tell "absent" from
	// "null" from a value — timestampMs: null (the legacy surface) is an
	// assertion, not a missing field.
	Expect map[string]json.RawMessage `json:"expect"`
}

// stringOrList decodes a corpus value that is either "x" or ["x","y"]. The
// corpus mirrors the TypeScript input type, which accepts both.
func stringOrList(t *testing.T, name string, raw json.RawMessage) []string {
	t.Helper()
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		t.Fatalf("%s: neither a string nor a list of strings: %s", name, raw)
	}
	return many
}

func loadSignatureCorpus(t *testing.T) []corpusVector {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(signatureCorpusPath))
	if err != nil {
		t.Fatalf("reading corpus: %v", err)
	}
	var doc struct {
		Version int            `json:"version"`
		Vectors []corpusVector `json:"vectors"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing corpus: %v", err)
	}
	if doc.Version != 1 {
		t.Fatalf("corpus version %d; this test understands version 1", doc.Version)
	}
	if len(doc.Vectors) < signatureCorpusFloor {
		t.Fatalf("corpus has %d vectors; the floor is %d — a broken loader must not pass on fewer", len(doc.Vectors), signatureCorpusFloor)
	}
	return doc.Vectors
}

func corpusInput(t *testing.T, v corpusVector) VerifyChatlySignatureInput {
	t.Helper()
	in := VerifyChatlySignatureInput{
		Surface: ChatlySignatureSurface(v.Input.Surface),
		Body:    []byte(v.Input.Body), // Go strings are UTF-8, so this IS the UTF-8 encoding the corpus means.
		Headers: map[string][]string{},
		Secrets: stringOrList(t, v.Name+" secret", v.Input.Secret),
		Now:     time.UnixMilli(v.Input.NowMs),
	}
	// Keys are copied verbatim — NOT through http.Header.Set, which would
	// canonicalise "x-livechat-signature" and "X-Livechat-Signature" into one
	// key and hide the two-casings vector the corpus exists to pin.
	for name, raw := range v.Input.Headers {
		in.Headers[name] = stringOrList(t, v.Name+" header "+name, raw)
	}
	if v.Input.Method != nil {
		in.Method = *v.Input.Method
	}
	if v.Input.Path != nil {
		in.Path = *v.Input.Path
	}
	if v.Input.V2 != nil {
		in.V2 = ChatlyV2Policy(*v.Input.V2)
	}
	if v.Input.AllowUnprotectedReplay != nil {
		in.AllowUnprotectedReplay = *v.Input.AllowUnprotectedReplay
	}
	if v.Input.ToleranceSeconds != nil {
		in.Tolerance = time.Duration(*v.Input.ToleranceSeconds) * time.Second
	}
	return in
}

func TestVerifyChatlySignatureCorpus(t *testing.T) {
	vectors := loadSignatureCorpus(t)
	known := map[string]bool{"ok": true, "surface": true, "reason": true, "base": true, "secretIndex": true, "timestampMs": true}
	for _, v := range vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			for key := range v.Expect {
				// A field this test does not assert is a field it silently
				// passes. Fail so a corpus extension forces a test extension.
				if !known[key] {
					t.Fatalf("expect carries %q, which this test does not assert", key)
				}
			}
			res := VerifyChatlySignature(corpusInput(t, v))

			var wantOK bool
			mustJSON(t, v.Expect["ok"], &wantOK)
			if res.OK != wantOK {
				t.Fatalf("ok = %v, want %v (reason %q: %s)", res.OK, wantOK, res.Reason, res.Message)
			}
			var wantSurface string
			mustJSON(t, v.Expect["surface"], &wantSurface)
			if string(res.Surface) != wantSurface {
				t.Errorf("surface = %q, want %q", res.Surface, wantSurface)
			}

			if !wantOK {
				var wantReason string
				mustJSON(t, v.Expect["reason"], &wantReason)
				if string(res.Reason) != wantReason {
					t.Errorf("reason = %q, want %q (message: %s)", res.Reason, wantReason, res.Message)
				}
				if strings.TrimSpace(res.Message) == "" {
					t.Errorf("refusal %q carries no message", res.Reason)
				}
				return
			}

			var wantBase string
			mustJSON(t, v.Expect["base"], &wantBase)
			if string(res.Base) != wantBase {
				t.Errorf("base = %q, want %q", res.Base, wantBase)
			}
			var wantIndex int
			mustJSON(t, v.Expect["secretIndex"], &wantIndex)
			if res.SecretIndex != wantIndex {
				t.Errorf("secretIndex = %d, want %d", res.SecretIndex, wantIndex)
			}
			rawTS, present := v.Expect["timestampMs"]
			if !present {
				t.Fatalf("an ok vector without timestampMs: the corpus format changed")
			}
			if string(rawTS) == "null" {
				if res.TimestampMs != nil {
					t.Errorf("timestampMs = %d, want null", *res.TimestampMs)
				}
			} else {
				var wantTS int64
				mustJSON(t, rawTS, &wantTS)
				if res.TimestampMs == nil || *res.TimestampMs != wantTS {
					t.Errorf("timestampMs = %v, want %d", res.TimestampMs, wantTS)
				}
			}
			if res.Reason != "" || res.Message != "" {
				t.Errorf("ok result carries reason %q / message %q", res.Reason, res.Message)
			}
		})
	}
}

func mustJSON(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	if raw == nil {
		t.Fatalf("expected field absent")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
}

// Every surface in the exported list has a table row, and vice versa. A
// surface added to one and forgotten in the other would be an
// unknown_surface refusal at runtime with every other test green.
func TestSignatureSurfaceTableIsComplete(t *testing.T) {
	if len(ChatlySignatureSurfaces) != 8 || len(signatureSurfaces) != len(ChatlySignatureSurfaces) {
		t.Fatalf("%d listed, %d in the table; want 8 and 8", len(ChatlySignatureSurfaces), len(signatureSurfaces))
	}
	for _, s := range ChatlySignatureSurfaces {
		if _, ok := signatureSurfaces[s]; !ok {
			t.Errorf("%s has no table row", s)
		}
	}
	if ChatlySignatureToleranceSeconds != 300 || ChatlyV2SignatureHeader != "x-chatly-signature-v2" {
		t.Errorf("exported constants drifted")
	}
}

const (
	goodBody = `{"id":"evt_1","type":"message.created"}`
	goodSig  = "t=1760000000,v1=68c8081fadfddd5bded0385d719bd284e690ec14847d59a15a30816bf65c4f1e"
	goodNow  = int64(1760000000000)
)

func goodInput() VerifyChatlySignatureInput {
	return VerifyChatlySignatureInput{
		Surface: SurfaceEventWebhook,
		Body:    []byte(goodBody),
		Headers: map[string][]string{"x-livechat-signature": {goodSig}},
		Secrets: []string{"whsec_vector_secret"},
		Now:     time.UnixMilli(goodNow),
	}
}

// The control for everything below: the baseline input DOES verify, so a
// refusal in a mutated copy is the mutation's doing.
func TestVerifyChatlySignatureBaselineVerifies(t *testing.T) {
	if res := VerifyChatlySignature(goodInput()); !res.OK {
		t.Fatalf("baseline refused: %s %s", res.Reason, res.Message)
	}
}

// http.Header passes straight in, canonicalised keys and all.
func TestVerifyChatlySignatureAcceptsHTTPHeaderShape(t *testing.T) {
	in := goodInput()
	in.Headers = map[string][]string{"X-Livechat-Signature": {goodSig}}
	if res := VerifyChatlySignature(in); !res.OK {
		t.Fatalf("canonical-form key refused: %s %s", res.Reason, res.Message)
	}
}

// Never panics on garbage, and never says OK to it. Each case is run inside
// a function with no recover of its own, but VerifyChatlySignature HAS one —
// so the second assertion matters: a recovered panic becomes
// signature_mismatch, and the message prefix tells the two apart.
func TestVerifyChatlySignatureNeverPanicsOnGarbage(t *testing.T) {
	cases := map[string]func(*VerifyChatlySignatureInput){
		"zero input":          func(in *VerifyChatlySignatureInput) { *in = VerifyChatlySignatureInput{} },
		"nil headers":         func(in *VerifyChatlySignatureInput) { in.Headers = nil },
		"nil header value":    func(in *VerifyChatlySignatureInput) { in.Headers = map[string][]string{"x-livechat-signature": nil} },
		"empty header slice":  func(in *VerifyChatlySignatureInput) { in.Headers = map[string][]string{"x-livechat-signature": {}} },
		"empty header string": func(in *VerifyChatlySignatureInput) { in.Headers = map[string][]string{"x-livechat-signature": {""}} },
		"nil body":            func(in *VerifyChatlySignatureInput) { in.Body = nil },
		"nil secrets":         func(in *VerifyChatlySignatureInput) { in.Secrets = nil },
		"empty secrets":       func(in *VerifyChatlySignatureInput) { in.Secrets = []string{} },
		"only commas":         func(in *VerifyChatlySignatureInput) { in.Headers["x-livechat-signature"] = []string{",,,,"} },
		"only equals":         func(in *VerifyChatlySignatureInput) { in.Headers["x-livechat-signature"] = []string{"===="} },
		"invalid utf8 header": func(in *VerifyChatlySignatureInput) {
			in.Headers["x-livechat-signature"] = []string{"t=\xff\xfe,v1=\xc0"}
		},
		"invalid utf8 surface": func(in *VerifyChatlySignatureInput) { in.Surface = "\xff\xfe" },
		"empty v1": func(in *VerifyChatlySignatureInput) {
			in.Headers["x-livechat-signature"] = []string{"t=1760000000,v1="}
		},
		"huge timestamp": func(in *VerifyChatlySignatureInput) {
			in.Headers["x-livechat-signature"] = []string{"t=999999999999999999999999999999," + goodSig[13:]}
		},
		"seconds overflow ×1000": func(in *VerifyChatlySignatureInput) {
			in.Headers["x-livechat-signature"] = []string{"t=9223372036854775," + goodSig[13:]}
		},
		"negative tolerance":   func(in *VerifyChatlySignatureInput) { in.Tolerance = -time.Second },
		"now far before epoch": func(in *VerifyChatlySignatureInput) { in.Now = time.Date(-200000000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"unknown v2 policy": func(in *VerifyChatlySignatureInput) {
			in.V2 = "sometimes"
			in.Headers["x-livechat-signature"] = []string{"garbage"}
		},
		"legacy, nil headers": func(in *VerifyChatlySignatureInput) {
			in.Surface = SurfaceWorkflowWebhookLegacy
			in.AllowUnprotectedReplay = true
			in.Headers = nil
		},
		"ms surface, empty ts header": func(in *VerifyChatlySignatureInput) {
			in.Surface = SurfaceCustomerCard
			in.Headers = map[string][]string{"x-chatly-timestamp": {}, "x-chatly-signature": {"sha256="}}
		},
	}
	for name, mutate := range cases {
		mutate := mutate
		t.Run(name, func(t *testing.T) {
			in := goodInput()
			in.Headers = map[string][]string{"x-livechat-signature": {goodSig}}
			mutate(&in)
			res := VerifyChatlySignature(in)
			if res.OK {
				t.Fatalf("garbage verified: %+v", res)
			}
			if res.Reason == "" || res.Message == "" {
				t.Fatalf("refusal with no reason or message: %+v", res)
			}
			if strings.HasPrefix(res.Message, "Internal error") {
				t.Fatalf("recovered a panic: %s", res.Message)
			}
		})
	}
}

// The overflow decisions the corpus cannot express (JavaScript has no int64
// to overflow), pinned here so a refactor to a different parse is caught.
func TestVerifyChatlySignatureTimestampOverflowIsMalformed(t *testing.T) {
	for _, ts := range []string{"9223372036854775808", "9223372036854776", "999999999999999999999999"} {
		in := goodInput()
		in.Headers["x-livechat-signature"] = []string{"t=" + ts + goodSig[len("t=1760000000"):]}
		if res := VerifyChatlySignature(in); res.Reason != ReasonMalformedTimestamp {
			t.Errorf("t=%s: reason %q, want malformed_timestamp (%s)", ts, res.Reason, res.Message)
		}
	}
	// The largest seconds value that still fits after ×1000 is NOT malformed;
	// it is merely 292 million years out.
	in := goodInput()
	in.Headers["x-livechat-signature"] = []string{"t=9223372036854775" + goodSig[len("t=1760000000"):]}
	if res := VerifyChatlySignature(in); res.Reason != ReasonTimestampOutsideTolerance {
		t.Errorf("boundary: reason %q, want timestamp_outside_tolerance", res.Reason)
	}
}

func TestVerifyChatlySignatureUnlimitedToleranceSkipsTheClock(t *testing.T) {
	in := goodInput()
	in.Now = time.UnixMilli(goodNow).Add(24 * 365 * time.Hour)
	if res := VerifyChatlySignature(in); res.Reason != ReasonTimestampOutsideTolerance {
		t.Fatalf("a year late should be stale; got %q", res.Reason)
	}
	in.Tolerance = ChatlySignatureToleranceUnlimited
	if res := VerifyChatlySignature(in); !res.OK {
		t.Fatalf("unlimited tolerance refused: %s", res.Message)
	}
}

// A zero Now means the real clock, so the corpus's 2025 timestamp is stale
// today. This pins that zero is "unset", not the epoch or year 1.
func TestVerifyChatlySignatureZeroNowUsesTheClock(t *testing.T) {
	in := goodInput()
	in.Now = time.Time{}
	if res := VerifyChatlySignature(in); res.Reason != ReasonTimestampOutsideTolerance {
		t.Fatalf("zero Now: reason %q, want timestamp_outside_tolerance", res.Reason)
	}
}

// Go randomises map order; the same two-casing timestamp input must give the
// same answer every time.
func TestVerifyChatlySignatureIsDeterministicAcrossKeyCasings(t *testing.T) {
	in := VerifyChatlySignatureInput{
		Surface: SurfaceCustomerCard,
		Body:    []byte(goodBody),
		Headers: map[string][]string{
			"x-chatly-timestamp": {"1760000000789"},
			"X-Chatly-Timestamp": {"1760000000999"},
			"x-chatly-signature": {"sha256=29656d7159b8131a53299d697b8f37962b031f1985625d8266cca7ec860399f4"},
		},
		Secrets: []string{"whsec_vector_secret"},
		Now:     time.UnixMilli(goodNow),
	}
	first := VerifyChatlySignature(in)
	for i := 0; i < 50; i++ {
		if got := VerifyChatlySignature(in); got.OK != first.OK || got.Reason != first.Reason {
			t.Fatalf("run %d disagreed with run 0: %+v vs %+v", i, got, first)
		}
	}
}
