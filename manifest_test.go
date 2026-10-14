package chatly

// The machinery between the surface table and the committed artifact: reading
// the shared manifest, templating an observed request path back against it,
// and emitting surface.lock.json, plus the two halves of the response-shape
// check: the fixture a declared shape becomes on the wire (responseForShape)
// and the reader that names the shape a decoded value really has
// (actualShape). Split out of surface_test.go, which holds the shape
// vocabulary and the assertions; the 162-row table itself lives in
// surfacecases_test.go and surfacecases_config_test.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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
	// route with a page envelope agrees with the mistake. Hence the
	// cross-check in TestEveryMethodHitsItsDocumentedRoute against this
	// SDK's own `shape` column, and TestPagedEntriesHavePagers.
	Paged bool `json:"paged"`

	// Shape is the manifest's claim about the TOP-LEVEL shape of the
	// successful response: array, page, object or empty. It is read here
	// only to be DIFFED against this SDK's own hand-written declaration (the
	// `shape` column in surfaceCases) — never to build a fixture from. See
	// the comment on surfaceCases for why that direction matters.
	Shape shape `json:"shape"`
}

// lockEntry is one row of surface.lock.json. Field order here IS the key
// order in the emitted JSON.
type lockEntry struct {
	NS    string `json:"ns"`
	Name  string `json:"name"`
	HTTP  string `json:"http"`
	Path  string `json:"path"`
	Shape shape  `json:"shape"`
}

// responseForShape is the success body a route of the given shape really
// sends, as the fake transport should answer it.
//
// One body per shape, chosen by the route's OWN declaration — the single
// most important property of this suite. A fake that answers every route
// with `{items, nextCursor}` agrees with a manifest that wrongly flags a
// bare-array route as paged, which is exactly how two sibling SDKs shipped a
// paginator over macros.list with green suites.
func responseForShape(t *testing.T, s shape) (status int, body string) {
	t.Helper()
	switch s {
	case shapePage:
		return http.StatusOK, `{"items":[{"id":"x_1"}],"nextCursor":null}`
	case shapeArray:
		return http.StatusOK, `[{"id":"x_1"}]`
	case shapeObject:
		return http.StatusOK, `{"id":"x_1"}`
	case shapeEmpty:
		// A real 204 has NO body. A decoder that insists on parsing one
		// fails against this, which is the only reason `empty` exists as a
		// value separate from an empty object.
		return http.StatusNoContent, ""
	}
	t.Fatalf("no fixture for shape %q: it is not one of the four", s)
	return 0, ""
}

// actualShape names the shape a decoded value really has, in the same
// four-word vocabulary the declaration uses.
//
// Go's methods return concrete types rather than `any`, so most of this is a
// type switch — but the type alone is not the answer. Two `page` routes
// return an Object because their envelopes carry extra fields, so a map is
// inspected for `items` and `nextCursor`; and a nil map, nil slice or empty
// `Items` means nothing was decoded, which is reported rather than passed off
// as the declared shape.
func actualShape(value any) string {
	if value == nil {
		return string(shapeEmpty)
	}
	switch v := value.(type) {
	case *Page[Object]:
		if v == nil {
			return "nil *Page"
		}
		if len(v.Items) == 0 {
			// The envelope decoded into nothing. This is the Rust client's
			// bug: a bare array read as an empty page and returned with a
			// 200 — a wrong answer rather than a missing one.
			return "page(no items decoded)"
		}
		return string(shapePage)
	case []Object:
		if v == nil {
			return string(shapeEmpty)
		}
		return string(shapeArray)
	case []string:
		// One route answers a bare array of STRINGS
		// (GET /v1/channels/adapters, `z.array(z.string())`). It is still
		// the `array` shape; what differs is the fixture, not the
		// declaration — see nonObjectArrayFixtures.
		if v == nil {
			return string(shapeEmpty)
		}
		return string(shapeArray)
	case Object:
		if v == nil {
			return string(shapeEmpty)
		}
		items, hasItems := v["items"].([]any)
		if _, hasCursor := v["nextCursor"]; hasItems && hasCursor {
			if len(items) == 0 {
				return "page(no items decoded)"
			}
			return string(shapePage)
		}
		return string(shapeObject)
	case *WhoAmI:
		if v == nil {
			return "nil *WhoAmI"
		}
		// A struct decode cannot be told apart from a zero one here, so this
		// route's object-ness rests on its type. It is the only struct
		// return on the surface.
		return string(shapeObject)
	}
	return fmt.Sprintf("unhandled(%T)", value)
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
		// The shape diff, reported here so a disagreement fails locally
		// instead of only in the cross-language gate. Both sides were
		// written by reading the handler; a mismatch means one of the two
		// readings is wrong, and the handler settles it — never this file.
		switch {
		case want.Shape == "":
			t.Errorf("%s: the manifest entry carries no `shape`; it is required, and a lock "+
				"written against a manifest without one would be unverifiable", key)
		case got.Shape != want.Shape:
			t.Errorf("%s: this SDK declares the response shape %s, the manifest says %s — "+
				"read the handler's @ZodResponse and fix whichever is wrong",
				key, got.Shape, want.Shape)
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
// Each row records the verb and path OBSERVED on the wire plus the response
// shape this SDK's own table declares for that route, in the key order the
// gate reads: ns, name, http, path, shape.
//
// The cross-language parity gate used to match route strings in SDK SOURCE
// TEXT. That counts a path in a comment, and it did: a boxed doc comment in
// this very file's package naming /v1/agents/skills made the gate report a
// method that called no such route. Three SDKs passed it the same way.
//
// A path in a comment is not evidence. An HTTP request arriving at a server
// is. This test already makes all 162 of those requests, so it is the right
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

// The lock file must be written exactly as the repo's formatter would write
// it, or it oscillates forever.
//
// The PHP SDK hit this: its emitter used JSON_PRETTY_PRINT, which indents
// with four spaces, while prettier rewrites the file with two. Every run
// rewrote what every commit hook reformatted, and a committed artifact
// churned in both directions indefinitely.
//
// Go's json.MarshalIndent(doc, "", "  ") plus one trailing newline matches
// prettier's JSON output byte for byte with this repo's .prettierrc
// (tabWidth 2, endOfLine lf) — verified by running the repo's own prettier
// against the committed file. This test cannot run prettier, so it pins the
// three properties that differed: indent width, line endings, and exactly one
// terminating newline.
func TestSurfaceLockIsFormatterStable(t *testing.T) {
	raw, err := os.ReadFile(lockFilePath)
	if err != nil {
		t.Fatalf("reading %s: %v", lockFilePath, err)
	}

	if !bytes.HasSuffix(raw, []byte("\n")) {
		t.Error("the lock file does not end with a newline; prettier would add one")
	}
	if bytes.HasSuffix(raw, []byte("\n\n")) {
		t.Error("the lock file ends with a blank line; prettier would remove it")
	}
	if bytes.Contains(raw, []byte("\r")) {
		t.Error("the lock file contains CR; .prettierrc pins endOfLine to lf")
	}
	if bytes.Contains(raw, []byte("\t")) {
		t.Error("the lock file contains a tab; .prettierrc pins tabWidth to 2 spaces")
	}

	// Indentation must step in twos. A four-space emitter shows up here as a
	// level that is not a multiple of two levels deep.
	for i, line := range bytes.Split(raw, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " ")
		if len(trimmed) == 0 {
			continue
		}
		indent := len(line) - len(trimmed)
		if indent%2 != 0 {
			t.Errorf("line %d is indented %d spaces, which is not a multiple of 2", i+1, indent)
		}
		if indent > 6 {
			t.Errorf("line %d is indented %d spaces; this document is at most 3 levels deep, "+
				"so anything past 6 means a wider indent unit than prettier's", i+1, indent)
		}
	}
}

// fixtureIsPage reports whether a fixture body is a page envelope — an object
// carrying both `items` and `nextCursor`.
//
// Containment rather than byte-equality, because a paged route is allowed to
// send more than those two keys and several here do. A bare array, an empty
// object, or an object with only one of the two is NOT a page.
func fixtureIsPage(t *testing.T, body string) bool {
	t.Helper()
	if body == "" {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		// A bare array or a non-object fixture. Not a page, and not an error:
		// most routes here legitimately answer one.
		return false
	}
	_, hasItems := fields["items"]
	_, hasCursor := fields["nextCursor"]
	return hasItems && hasCursor
}

// fixtureShape names the shape a fixture BODY has, in the same four-word
// vocabulary — the counterpart of actualShape, which reads a decoded value.
//
// It exists for the one route whose fixture is overridden
// (nonObjectArrayFixtures): an override has to be proved to be of the shape
// it replaces, or the override becomes a hole in the declaration.
func fixtureShape(t *testing.T, body string) shape {
	t.Helper()
	if body == "" {
		return shapeEmpty
	}
	if fixtureIsPage(t, body) {
		return shapePage
	}
	var arr []json.RawMessage
	if json.Unmarshal([]byte(body), &arr) == nil {
		return shapeArray
	}
	return shapeObject
}

// The fixtures everything else trusts, checked against the vocabulary they
// are named in.
//
// responseForShape is the single point where a declared shape becomes bytes
// on a wire, so if it answered a page envelope for `array` the whole suite
// would quietly go back to agreeing with itself. Four bodies, four
// assertions, plus the 204 that must carry no body at all.
func TestFixturesHaveTheShapeTheyAreNamedFor(t *testing.T) {
	status, body := responseForShape(t, shapePage)
	if status != http.StatusOK || !fixtureIsPage(t, body) {
		t.Errorf("the `page` fixture is %d %q, which is not {items,nextCursor}", status, body)
	}

	status, body = responseForShape(t, shapeArray)
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(body), &arr); err != nil || len(arr) != 1 || status != http.StatusOK {
		t.Errorf("the `array` fixture is %d %q, which is not a one-element bare array", status, body)
	}
	if fixtureIsPage(t, body) {
		t.Error("the `array` fixture parses as a page envelope")
	}

	status, body = responseForShape(t, shapeObject)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &obj); err != nil || len(obj) == 0 || status != http.StatusOK {
		t.Errorf("the `object` fixture is %d %q, which is not a non-empty JSON object", status, body)
	}
	if fixtureIsPage(t, body) {
		t.Error("the `object` fixture parses as a page envelope; it would hide a page/object mix-up")
	}

	if status, body = responseForShape(t, shapeEmpty); status != http.StatusNoContent || body != "" {
		t.Errorf("the `empty` fixture is %d %q; it must be a bodyless 204, or a decoder that "+
			"insists on parsing a body would never be caught", status, body)
	}
}

// actualShape is the other half of that pair: the reader. If it called
// everything an object, every per-route assertion would pass.
func TestActualShapeNamesWhatItIsGiven(t *testing.T) {
	cursor := "c_2"
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "empty"},
		{"page pointer", &Page[Object]{Items: []Object{{"id": "x_1"}}}, "page"},
		{"page pointer with a cursor", &Page[Object]{Items: []Object{{"id": "x_1"}}, NextCursor: &cursor}, "page"},
		{"slice", []Object{{"id": "x_1"}}, "array"},
		{"empty slice", []Object{}, "array"},
		{"object", Object{"id": "x_1"}, "object"},
		// The two routes whose envelope carries extra fields and whose
		// method therefore returns the whole object. Still a page.
		{"object carrying a page", Object{"items": []any{Object{"id": "x_1"}}, "nextCursor": nil, "total": 3}, "page"},
		{"whoami", &WhoAmI{WorkspaceID: "ws_1"}, "object"},
		// Nothing decoded. These must NOT read as the shape that was asked
		// for — that is the whole failure being guarded against.
		{"nil map", Object(nil), "empty"},
		{"nil slice", []Object(nil), "empty"},
		{"page that decoded nothing", &Page[Object]{}, "page(no items decoded)"},
		{"object with an empty items array", Object{"items": []any{}, "nextCursor": nil}, "page(no items decoded)"},
		{"half an envelope", Object{"items": []any{Object{"id": "x_1"}}}, "object"},
		{"a type this suite does not model", 42, "unhandled(int)"},
	}
	for _, tc := range cases {
		if got := actualShape(tc.value); got != tc.want {
			t.Errorf("%s: actualShape = %q, want %q", tc.name, got, tc.want)
		}
	}
}
