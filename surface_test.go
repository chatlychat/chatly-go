package chatly

import (
	"context"
	"net/http"
	"testing"
)

// shape is the TOP-LEVEL shape of a route's successful response body.
//
// Four values, and every route on this surface is exactly one of them:
//
//	array   a bare JSON array
//	page    the cursor envelope — `items` plus `nextCursor`, with whatever
//	        sits beside them (`hasMore` on most, `total` on
//	        conversations.unattended, `tier` and `facets` on search.query)
//	object  a JSON object that is not a page
//	empty   no body at all, a real 204
type shape string

const (
	shapeArray  shape = "array"
	shapePage   shape = "page"
	shapeObject shape = "object"
	shapeEmpty  shape = "empty"
)

// allShapes is the closed set. `shape` is a string type, so a typo in the
// table below would compile; TestDeclaredShapesCoverTheSurface rejects
// anything that is not one of these four.
var allShapes = []shape{shapeArray, shapePage, shapeObject, shapeEmpty}

// nonObjectArrayFixtures overrides the `array` fixture for a route whose
// array does NOT hold objects, with the body the real route sends.
//
// One entry, and the mechanism is deliberately this narrow. GET
// /v1/channels/adapters is `z.array(z.string())` — `["email","web"]` — which
// cannot decode into []Object at all, so the shared one-element fixture
// `[{"id":"x_1"}]` would fail a method that is correct and pass one built on
// the wrong decoder. The override is still asserted to BE an array, both at
// the point of use and in TestNonObjectArrayFixturesAreStillArrays, so this
// widens the fixture set by one body rather than excusing a route from
// declaring a shape.
//
// Absolve by name, never by prefix: an entry here must name a case that
// exists and declares `array`.
var nonObjectArrayFixtures = map[string]string{
	"channels.adapters": `["email","web"]`,
}

// An override must name a real `array` case and must itself be a bare array.
//
// Without this, an entry could sit here naming a route that has since changed
// shape — or carrying a page envelope, which would quietly put this suite
// back to agreeing with itself on the one route the mechanism exists for.
func TestNonObjectArrayFixturesAreStillArrays(t *testing.T) {
	declared := make(map[string]shape)
	for _, tc := range surfaceCases() {
		declared[tc.name] = tc.shape
	}
	for name, body := range nonObjectArrayFixtures {
		s, ok := declared[name]
		if !ok {
			t.Errorf("nonObjectArrayFixtures overrides %s, which is not a case in this table", name)
			continue
		}
		if s != shapeArray {
			t.Errorf("nonObjectArrayFixtures overrides %s, which this SDK declares %s, not array", name, s)
		}
		if got := fixtureShape(t, body); got != shapeArray {
			t.Errorf("the override for %s is %s, which parses as %s rather than a bare array", name, body, got)
		}
	}
}

// surfaceCase is one row of the table: a method, the verb and path it is
// supposed to request, the response shape its decoder expects, and the call.
type surfaceCase struct {
	name   string
	method string
	path   string
	shape  shape
	call   func(context.Context, *Client) (any, error)
}

// Every method is driven against a recording server, asserting the VERB, the
// PATH and the SHAPE of what it hands back — and the run emits the lock.
func TestEveryMethodHitsItsDocumentedRoute(t *testing.T) {
	cases := surfaceCases()
	manifest := loadManifest(t)

	// The expected count comes from the manifest, not from a number typed
	// here — but with a floor, because a manifest that fails to parse down to
	// three entries must fail loudly rather than quietly auditing three
	// methods and printing a pass.
	// The floor's history, so a change to it is never mistakable for a parse
	// failure being papered over:
	//
	//	55   the manifest's size when this SDK was written
	//	159  most of the key-accessible surface, implemented 2026-10-14
	//	162  ALL of it, later the same day: webhooks.delete, root.me and
	//	     workspace.mine were listed after this SDK reported them
	//	     key-accessible but absent from the manifest
	//
	// 162 routes carry @AllowApiKey() in apps/api and the manifest lists 162,
	// with no gap in either direction — so a route appearing here that this
	// package does not implement is now a NEW route rather than a known
	// omission. If this number has to move DOWN, say which route left the
	// surface and why, in the commit.
	if len(manifest) < 162 {
		t.Fatalf("manifest has %d entries; it had 162 when this SDK covered the whole surface, "+
			"so either a route was deliberately removed (lower the floor, and say which) or the "+
			"manifest is no longer parsing", len(manifest))
	}
	if len(cases) != len(manifest) {
		t.Fatalf("%d methods covered, manifest lists %d", len(cases), len(manifest))
	}

	recorded := make([]lockEntry, 0, len(cases))
	seen := make(map[string]bool, len(cases))
	for _, tc := range cases {
		tc := tc
		if seen[tc.name] {
			t.Fatalf("duplicate case %q", tc.name)
		}
		seen[tc.name] = true

		entry, ok := manifest[tc.name]
		if !ok {
			t.Errorf("%s is not in the manifest", tc.name)
			continue
		}

		t.Run(tc.name, func(t *testing.T) {
			// The fake answers with a body of THIS ROUTE'S declared shape —
			// not one body for everything. A single fake answering every
			// route with a page envelope is how macros.list passed a sibling
			// SDK's suite and failed on every real call.
			status, body := responseForShape(t, tc.shape)
			if override, ok := nonObjectArrayFixtures[tc.name]; ok {
				// A route whose array does not hold objects. The override is
				// still of the DECLARED shape — asserted below and again in
				// TestNonObjectArrayFixturesAreStillArrays — so this widens
				// the fixture set by one body, it does not excuse a route
				// from declaring a shape.
				status, body = http.StatusOK, override
				if got := fixtureShape(t, override); got != tc.shape {
					t.Fatalf("%s overrides its fixture with %s, which is a %s and not the declared %s",
						tc.name, override, got, tc.shape)
				}
			}

			var gotMethod, gotPath string
			var hits int
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				hits++
				if body == "" {
					// A real 204: header only, no body written at all.
					w.WriteHeader(status)
					return
				}
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}))

			value, err := tc.call(context.Background(), c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if hits != 1 {
				t.Fatalf("%d requests, want exactly 1", hits)
			}
			if gotMethod != tc.method {
				t.Errorf("method = %s, want %s", gotMethod, tc.method)
			}
			if gotPath != tc.path {
				t.Errorf("path = %s, want %s", gotPath, tc.path)
			}

			// And the value handed back really has the declared shape.
			//
			// This is what catches a method that wraps or unwraps: a List
			// that returned page.Items from a `page` route, one that reached
			// for `items` on a bare-array route, or a DELETE typed to return
			// nothing from a route that answers with a row — each disagrees
			// here even though the request it issued was perfect.
			if got := actualShape(value); got != string(tc.shape) {
				t.Errorf("%s decoded to %s, but this SDK declares the route answers %s "+
					"(value %#v)", tc.name, got, tc.shape, value)
			}

			// `paged` and `shape: page` are two spellings of one fact about
			// the route. Checking the manifest's flag against this file's
			// own column — rather than against the fixture, which is now
			// derived from that column — is the diff that catches a drifting
			// manifest in either direction.
			_, justified := pagersBeyondTheManifest[tc.name]
			switch {
			case entry.Paged && tc.shape != shapePage:
				t.Errorf("%s is `paged` in the manifest but this SDK declares its shape %s; "+
					"a walker needs {items,nextCursor} and one of the two is wrong", tc.name, tc.shape)
			case !entry.Paged && tc.shape == shapePage && !justified:
				t.Errorf("%s is not `paged` in the manifest but this SDK declares it a page; "+
					"either the manifest is wrong or this SDK is agreeing with a bug", tc.name)
			}

			// What goes in the lock is the OBSERVED request, templated back
			// against the manifest — never the manifest row itself. Copying
			// the manifest across would make the artifact agree with the
			// spec by construction, which is the whole failure this replaces.
			// The shape beside it is this file's own declaration, for the
			// same reason.
			templated, err := templatePath(gotPath, entry.Path)
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
				return
			}
			if gotMethod != entry.HTTP {
				t.Errorf("%s: called %s, manifest says %s", tc.name, gotMethod, entry.HTTP)
			}
			recorded = append(recorded, lockEntry{
				NS:    entry.NS,
				Name:  entry.Name,
				HTTP:  gotMethod,
				Path:  templated,
				Shape: tc.shape,
			})
		})
	}

	if t.Failed() {
		// Do not write a lock from a failing run: a half-recorded artifact
		// that a gate then reads as truth is worse than no artifact.
		t.Fatal("not writing surface.lock.json from a failing run")
	}
	if len(recorded) != len(manifest) {
		t.Fatalf("recorded %d of %d manifest entries", len(recorded), len(manifest))
	}
	assertCoversManifest(t, recorded, manifest)
	writeSurfaceLock(t, recorded)
}

// Every route declares a shape, all four shapes appear, and the DISTRIBUTION
// is the one reading the handlers produced.
//
// Anti-vacuity. A column that said `object` 162 times would satisfy every
// other assertion in this package — the fake would answer `{"id":"x_1"}`
// everywhere, every decoder would cope, and the gate would see unanimity
// about nothing. The numbers come from reading every @ZodResponse schema on
// the surface: 14/10/30/1 over the first 55 routes on 2026-10-12, then
// 32/13/113/1 over 159 on 2026-10-14, and 33 array / 13 page / 115 object /
// 1 empty once webhooks.delete (`{ok:true}`), root.me (a discriminated union
// of objects) and workspace.mine (`z.array(workspaceListItemSchema)`) were
// listed.
//
// If the surface grows these change, but deliberately and with handler
// evidence per row — and `empty` stays at exactly one until a second real 204
// ships.
//
// Six near misses worth recording, because all six are `object` and look like
// something else. `billing.invoices` IS cursor-paginated and still not a
// `page`: it spells its envelope `{data, nextCursor}`, so a walker keyed on
// `items` would read nothing and report an empty page forever. And five
// routes answer an `items` key with NO cursor beside it —
// conversations.linked, conversations.outcomes, macros.templates,
// tickets.time, tickets.status_history — where "has items, therefore a page"
// is exactly the heuristic that types them wrong.
func TestDeclaredShapesCoverTheSurface(t *testing.T) {
	cases := surfaceCases()

	valid := make(map[shape]bool, len(allShapes))
	for _, s := range allShapes {
		valid[s] = true
	}

	counts := make(map[shape]int, len(allShapes))
	for _, tc := range cases {
		if !valid[tc.shape] {
			t.Errorf("%s declares shape %q, which is not one of the four", tc.name, tc.shape)
			continue
		}
		counts[tc.shape]++
	}

	want := map[shape]int{shapeArray: 33, shapePage: 13, shapeObject: 115, shapeEmpty: 1}
	total := 0
	for _, s := range allShapes {
		if counts[s] != want[s] {
			t.Errorf("%d routes declared %s, expected %d", counts[s], s, want[s])
		}
		if counts[s] == 0 {
			t.Errorf("no route declares %s; a shape nothing exercises proves nothing", s)
		}
		total += counts[s]
	}
	if total != len(cases) {
		t.Errorf("%d of %d cases carry a valid shape", total, len(cases))
	}
}
