package chatly

// The machinery between the surface table and the committed artifact: reading
// the shared manifest, templating an observed request path back against it,
// and emitting surface.lock.json. Split out of surface_test.go, which is now
// the 55-method table and nothing else.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// manifestLocation finds the cross-language SDK surface contract, which is
// read rather than duplicated: the tests assert against the spec itself, so a
// spec change shows up as a failure here instead of as two files that quietly
// disagree.
//
// Two candidates because this package is published from two places.
// `../sdk-spec/surface.json` is the monorepo layout; a public mirror has no
// `packages/` above it, so `scripts/sync-sdk-repos.mjs` vendors the manifest
// at the mirror root. Falling back to the monorepo path when neither exists
// gives loadManifest a concrete name to report as missing.
func manifestLocation() string {
	for _, candidate := range []string{"../sdk-spec/surface.json", "sdk-spec/surface.json"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "../sdk-spec/surface.json"
}

// lockFilePath is the committed artifact this test emits.
const lockFilePath = "surface.lock.json"

type manifestEntry struct {
	NS   string `json:"ns"`
	Name string `json:"name"`
	HTTP string `json:"http"`
	Path string `json:"path"`

	// Paged is the manifest's claim that the route answers
	// {items, nextCursor}. It is NOT a formality: macros.list carried it for
	// a day while the route actually returns a bare array capped at 200, and
	// two sibling SDKs raised a decode error on every real call while their
	// own suites stayed green — because a fake transport that answers every
	// route with a page envelope agrees with the mistake. Hence
	// TestFixtureShapesMatchTheManifest and TestPagedEntriesHavePagers below.
	Paged bool `json:"paged"`
}

// lockEntry is one row of surface.lock.json. Field order here IS the key
// order in the emitted JSON.
type lockEntry struct {
	NS   string `json:"ns"`
	Name string `json:"name"`
	HTTP string `json:"http"`
	Path string `json:"path"`
}

func loadManifest(t *testing.T) map[string]manifestEntry {
	t.Helper()
	raw, err := os.ReadFile(manifestLocation())
	if err != nil {
		t.Fatalf("reading %s: %v", manifestLocation(), err)
	}
	var doc struct {
		Entries []manifestEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", manifestLocation(), err)
	}
	out := make(map[string]manifestEntry, len(doc.Entries))
	for _, e := range doc.Entries {
		if e.NS == "" || e.Name == "" || e.HTTP == "" || e.Path == "" {
			t.Fatalf("manifest entry is missing a field: %+v", e)
		}
		key := e.NS + "." + e.Name
		if _, dup := out[key]; dup {
			t.Fatalf("manifest has two entries named %s", key)
		}
		out[key] = e
	}
	return out
}

// templatePath turns the path that was actually requested back into the
// manifest's templated form, substituting each interpolated segment by
// POSITION with the manifest's own parameter name:
//
//	observed /v1/contacts/ct_1/tags/vip
//	template /v1/contacts/{id}/tags/{tag}
//	result   /v1/contacts/{id}/tags/{tag}
//
// Every literal segment must match exactly, which is what makes this a
// comparison rather than a rubber stamp — a method calling
// /v1/contact/ct_1/tags (singular) does not quietly become the template.
func templatePath(observed, template string) (string, error) {
	gotParts := strings.Split(observed, "/")
	wantParts := strings.Split(template, "/")
	if len(gotParts) != len(wantParts) {
		return "", fmt.Errorf("path %q has %d segments, manifest template %q has %d",
			observed, len(gotParts), template, len(wantParts))
	}
	out := make([]string, len(wantParts))
	for i, want := range wantParts {
		if strings.HasPrefix(want, "{") && strings.HasSuffix(want, "}") {
			if gotParts[i] == "" {
				return "", fmt.Errorf("path %q has an empty segment where template %q expects %s",
					observed, template, want)
			}
			out[i] = want
			continue
		}
		if gotParts[i] != want {
			return "", fmt.Errorf("path %q does not match template %q at segment %d (%q vs %q)",
				observed, template, i, gotParts[i], want)
		}
		out[i] = want
	}
	return strings.Join(out, "/"), nil
}

// assertCoversManifest is the equality check: every manifest row is covered
// by an observed call, with the same verb and the same templated path, and
// nothing extra is recorded.
func assertCoversManifest(t *testing.T, recorded []lockEntry, manifest map[string]manifestEntry) {
	t.Helper()
	byKey := make(map[string]lockEntry, len(recorded))
	for _, r := range recorded {
		key := r.NS + "." + r.Name
		if _, dup := byKey[key]; dup {
			t.Errorf("recorded %s twice", key)
		}
		byKey[key] = r
	}
	for key, want := range manifest {
		got, ok := byKey[key]
		if !ok {
			t.Errorf("manifest entry %s was never exercised by a method", key)
			continue
		}
		if got.HTTP != want.HTTP || got.Path != want.Path {
			t.Errorf("%s: SDK calls %s %s, manifest says %s %s", key, got.HTTP, got.Path, want.HTTP, want.Path)
		}
	}
	for key := range byKey {
		if _, ok := manifest[key]; !ok {
			t.Errorf("recorded %s, which is not in the manifest", key)
		}
	}
}

// writeSurfaceLock emits surface.lock.json, sorted by ns then name.
//
// ─── Why a lock file, and why it is written here ──────────────────────────
//
// The cross-language parity gate used to match route strings in SDK SOURCE
// TEXT. That counts a path in a comment, and it did: a boxed doc comment in
// this very file's package naming /v1/agents/skills made the gate report a
// method that called no such route. Three SDKs passed it the same way.
//
// A path in a comment is not evidence. An HTTP request arriving at a server
// is. This test already makes all 55 of those requests, so it is the right
// place to record what was observed — and the gate reads the recording rather
// than guessing from source.
//
// The file is committed, which means it can go stale: change the table, skip
// the suite, and a gate would read yesterday's evidence as today's. So this
// rewrites it AND fails when what it wrote differs from what was there. A
// failing run therefore means "regenerate and commit", and a green `go test`
// is the proof that the committed artifact matches the code.
func writeSurfaceLock(t *testing.T, recorded []lockEntry) {
	t.Helper()

	sorted := append([]lockEntry(nil), recorded...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].NS != sorted[j].NS {
			return sorted[i].NS < sorted[j].NS
		}
		return sorted[i].Name < sorted[j].Name
	})

	doc := struct {
		SDK         string      `json:"sdk"`
		GeneratedBy string      `json:"generatedBy"`
		Entries     []lockEntry `json:"entries"`
	}{SDK: "go", GeneratedBy: "surface_test.go", Entries: sorted}

	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("encoding the surface lock: %v", err)
	}
	encoded = append(encoded, '\n')

	previous, readErr := os.ReadFile(lockFilePath)
	if err := os.WriteFile(lockFilePath, encoded, 0o644); err != nil {
		t.Fatalf("writing %s: %v", lockFilePath, err)
	}
	switch {
	case os.IsNotExist(readErr):
		t.Fatalf("%s did not exist; it has been written with %d entries — commit it", lockFilePath, len(sorted))
	case readErr != nil:
		t.Fatalf("reading %s: %v", lockFilePath, readErr)
	case !bytes.Equal(previous, encoded):
		t.Fatalf("%s was stale and has been rewritten with %d entries — commit it", lockFilePath, len(sorted))
	}
}

// templatePath is the one piece of machinery between an observed request and
// the committed artifact a cross-language gate trusts, so it gets its own
// test with a negative case.
//
// The failure being guarded against is a normaliser that maps anything onto
// the template it was handed — which would make every path in the lock agree
// with the manifest no matter what the SDK called.
func TestTemplatePath(t *testing.T) {
	ok := []struct{ observed, template, want string }{
		{"/v1/contacts", "/v1/contacts", "/v1/contacts"},
		{"/v1/contacts/ct_1", "/v1/contacts/{id}", "/v1/contacts/{id}"},
		{"/v1/contacts/ct_1/tags/vip", "/v1/contacts/{id}/tags/{tag}", "/v1/contacts/{id}/tags/{tag}"},
		{"/v1/kb/articles/reset-password", "/v1/kb/articles/{slug}", "/v1/kb/articles/{slug}"},
		{
			"/v1/conversations/scheduled-messages/sm_1",
			"/v1/conversations/scheduled-messages/{scheduled_id}",
			"/v1/conversations/scheduled-messages/{scheduled_id}",
		},
		{
			"/v1/tickets/for-conversation/cv_1",
			"/v1/tickets/for-conversation/{conversation_id}",
			"/v1/tickets/for-conversation/{conversation_id}",
		},
		// A literal segment that happens to look like an id must still match
		// literally: /v1/conversations/unattended is a route, not an {id}.
		{"/v1/conversations/unattended", "/v1/conversations/unattended", "/v1/conversations/unattended"},
	}
	for _, tc := range ok {
		got, err := templatePath(tc.observed, tc.template)
		if err != nil {
			t.Errorf("templatePath(%q, %q): %v", tc.observed, tc.template, err)
			continue
		}
		if got != tc.want {
			t.Errorf("templatePath(%q, %q) = %q, want %q", tc.observed, tc.template, got, tc.want)
		}
	}

	bad := []struct{ name, observed, template string }{
		{"singular typo", "/v1/contact/ct_1", "/v1/contacts/{id}"},
		{"hyphen typo", "/v1/tickets/for_conversation/cv_1", "/v1/tickets/for-conversation/{conversation_id}"},
		{"missing version", "/contacts/ct_1", "/v1/contacts/{id}"},
		{"segment too few", "/v1/contacts/ct_1", "/v1/contacts/{id}/tags/{tag}"},
		{"segment too many", "/v1/contacts/ct_1/tags/vip", "/v1/contacts/{id}"},
		{"literal where param expected is fine, param where literal expected is not",
			"/v1/conversations/cv_1", "/v1/conversations/unattended"},
		{"empty interpolation", "/v1/contacts//tags/vip", "/v1/contacts/{id}/tags/{tag}"},
	}
	for _, tc := range bad {
		if got, err := templatePath(tc.observed, tc.template); err == nil {
			t.Errorf("%s: templatePath(%q, %q) = %q, want an error",
				tc.name, tc.observed, tc.template, got)
		}
	}
}
