package chatly

// Which routes this SDK paginates, and why — checked against the manifest's
// own `paged` flags rather than against a second copy of them typed here.

import (
	"context"
	"net/http"
	"testing"
)

// sdkPagers is every auto-paginating method this package offers, keyed by the
// manifest's ns.name. One registry, used by the two tests below, so a pager
// cannot exist without being accounted for.
func sdkPagers() map[string]func(*Client) *Pager[Object] {
	return map[string]func(*Client) *Pager[Object]{
		"contacts.list":          func(c *Client) *Pager[Object] { return c.Contacts.ListAll(nil) },
		"contacts.journey":       func(c *Client) *Pager[Object] { return c.Contacts.JourneyAll("ct_1", nil) },
		"conversations.list":     func(c *Client) *Pager[Object] { return c.Conversations.ListAll(nil) },
		"conversations.messages": func(c *Client) *Pager[Object] { return c.Conversations.MessagesAll("cv_1", nil) },
		"tickets.list":           func(c *Client) *Pager[Object] { return c.Tickets.ListAll(nil) },
		"kb.articles":            func(c *Client) *Pager[Object] { return c.KB.ArticlesAll(nil) },
		"events.list":            func(c *Client) *Pager[Object] { return c.Events.ListAll(nil) },
		"webhooks.deliveries":    func(c *Client) *Pager[Object] { return c.Webhooks.DeliveriesAll(nil) },
		"search.query":           func(c *Client) *Pager[Object] { return c.Search.QueryAll("q", nil) },
	}
}

// pagersBeyondTheManifest are routes this SDK walks that the manifest does not
// flag `paged`. Absolved BY NAME, never by prefix or by count, and each needs
// a reason that was checked against the handler — an unexplained extra pager
// is a route being paginated on a guess, which is how a client ends up
// looping on a cursor the server never sends.
var pagersBeyondTheManifest = map[string]string{
	"contacts.journey": "the route answers {items, nextCursor}; its cursor is an ISO-8601 instant " +
		"rather than an opaque blob, which is why the manifest may not count it, but it does page",
	"search.query": "the route answers {items, nextCursor} alongside `tier` and `facets`; the single-page " +
		"method returns the whole object so those two are not thrown away, and QueryAll walks the cursor",
}

// Every manifest entry flagged `paged` must have a pager, and every pager
// must correspond to a flagged entry or to a named justification.
//
// This is the assertion macros.list needed and did not have anywhere: the
// manifest claimed it paged, and nothing tied that claim to either a
// paginator or the response shape.
func TestPagedEntriesHavePagers(t *testing.T) {
	manifest := loadManifest(t)
	pagers := sdkPagers()

	paged := make(map[string]manifestEntry)
	for key, entry := range manifest {
		if entry.Paged {
			paged[key] = entry
		}
	}

	// Anti-vacuity floor. It was 8 and is now 7 — NOT because the manifest
	// stopped parsing, but because macros.list was removed from the paged set
	// on evidence: its handler is @PaginationExempt and returns a bare array
	// capped at 200 rows. If this number has to move again, the commit that
	// moves it should say which route changed and why, so a lowered floor can
	// never read as a parse failure being papered over.
	const pagedFloor = 7
	if len(paged) < pagedFloor {
		t.Fatalf("manifest flags %d paged entries, floor is %d: either a route legitimately became "+
			"pagination-exempt (say which, and lower the floor deliberately) or the manifest is no "+
			"longer parsing — do not assume the former", len(paged), pagedFloor)
	}

	for key := range paged {
		if _, ok := pagers[key]; !ok {
			t.Errorf("%s is paged in the manifest but this SDK offers no auto-paginator for it", key)
		}
	}
	for key := range pagers {
		if _, ok := paged[key]; ok {
			continue
		}
		if reason, ok := pagersBeyondTheManifest[key]; ok {
			t.Logf("pager beyond the manifest: %s — %s", key, reason)
			continue
		}
		t.Errorf("this SDK paginates %s, which the manifest does not flag `paged` and which has no "+
			"entry in pagersBeyondTheManifest; prove the route sends a cursor or delete the pager", key)
	}
}

// Each auto-paginating method must target the same route as its single-page
// twin. They share one helper, but a wrong path argument would not be caught
// anywhere else — and the path is checked against the manifest, not against a
// second copy of it typed into this test.
func TestPagerVariantsTargetTheSameRoutes(t *testing.T) {
	manifest := loadManifest(t)

	for key, build := range sdkPagers() {
		key, build := key, build
		t.Run(key, func(t *testing.T) {
			entry, ok := manifest[key]
			if !ok {
				t.Fatalf("%s is not in the manifest", key)
			}

			var gotPath string
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{"items":[{"id":"x"}],"nextCursor":null}`))
			}))

			p := build(c)
			if !p.Next(context.Background()) {
				t.Fatalf("pager yielded nothing: %v", p.Err())
			}
			if err := p.Err(); err != nil {
				t.Fatalf("Err: %v", err)
			}

			templated, err := templatePath(gotPath, entry.Path)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if templated != entry.Path {
				t.Fatalf("pager called %s, manifest says %s", templated, entry.Path)
			}
		})
	}
}

// macros.list returns a BARE ARRAY, and this SDK must both decode that and
// refuse a page envelope.
//
// The manifest flagged it `paged: true` for a day. The .NET and Rust clients
// modelled it as a page and raised a decode error on every real call, while
// their own suites stayed green — their fakes answered a page envelope, so the
// test agreed with the bug. Two assertions here, because only the pair is
// evidence: the first shows the right shape works, the second shows the wrong
// shape is NOT quietly tolerated. A lenient decode would pass the first alone
// and hand callers an empty list forever.
func TestMacrosListIsABareArrayNotAPage(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200, `[{"id":"mc_1","name":"Refund"},{"id":"mc_2","name":"Escalate"}]`))
	macros, err := c.Macros.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("Macros.List against a bare array: %v", err)
	}
	if len(macros) != 2 || macros[1]["name"] != "Escalate" {
		t.Fatalf("decoded %v", macros)
	}

	envelope, _ := newTestClient(t, jsonHandler(200, `{"items":[{"id":"mc_1"}],"nextCursor":null}`))
	if _, err := envelope.Macros.List(context.Background(), nil); err == nil {
		t.Fatal("Macros.List accepted a {items,nextCursor} envelope: the decode is lenient, so a route " +
			"that silently changed shape would hand callers an empty slice instead of an error")
	}

	if _, ok := sdkPagers()["macros.list"]; ok {
		t.Fatal("there is a pager for macros.list; the route is @PaginationExempt and sends no cursor")
	}
}
