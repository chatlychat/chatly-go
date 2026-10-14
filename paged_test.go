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
		"conversations.unattended": func(c *Client) *Pager[Object] {
			return c.Conversations.UnattendedAll(nil)
		},
		"tickets.list":        func(c *Client) *Pager[Object] { return c.Tickets.ListAll(nil) },
		"kb.articles":         func(c *Client) *Pager[Object] { return c.KB.ArticlesAll(nil) },
		"events.list":         func(c *Client) *Pager[Object] { return c.Events.ListAll(nil) },
		"webhooks.deliveries": func(c *Client) *Pager[Object] { return c.Webhooks.DeliveriesAll(nil) },
		"search.query":        func(c *Client) *Pager[Object] { return c.Search.QueryAll("q", nil) },
		"bots.list":           func(c *Client) *Pager[Object] { return c.Bots.ListAll(nil) },
		"bots.deliveries":     func(c *Client) *Pager[Object] { return c.Bots.DeliveriesAll("bt_1", nil) },
		"triggers.list":       func(c *Client) *Pager[Object] { return c.Triggers.ListAll(nil) },
	}
}

// pagersBeyondTheManifest absolves a pager the manifest does not flag `paged`.
//
// EMPTY, and that is the correct state rather than a dormant feature. It held
// contacts.journey and search.query while the manifest was missing both; the
// manifest flags all three of those routes now (conversations.unattended was
// the third), so both justifications were obsolete the moment it was
// corrected and have been deleted.
//
// The mechanism stays for the next genuine case, with two rules enforced by
// TestPagerAbsolutionsAreNotStale below: an entry must name a pager that
// exists, and it must NOT name a route the manifest already flags. A stale
// exemption is worse than no exemption — it reads as a considered decision
// while silently excusing something nothing is checking any more, which is
// how a gate here ends up auditing a shrinking set and still printing green.
// Absolve by name, never by prefix or by count.
var pagersBeyondTheManifest = map[string]string{}

// An absolution must still describe something true.
//
// This is the check that would have caught today's drift on its own: when the
// manifest gained `paged` on contacts.journey and search.query, their entries
// here became claims that the manifest does not flag routes it now flags.
// Without this, they would have sat there indefinitely, excusing nothing and
// asserting something false.
func TestPagerAbsolutionsAreNotStale(t *testing.T) {
	manifest := loadManifest(t)
	pagers := sdkPagers()

	for key, reason := range pagersBeyondTheManifest {
		if reason == "" {
			t.Errorf("%s is absolved with no reason given", key)
		}
		if _, ok := pagers[key]; !ok {
			t.Errorf("pagersBeyondTheManifest absolves %s, but this SDK has no pager for it; delete the entry", key)
		}
		if entry, ok := manifest[key]; ok && entry.Paged {
			t.Errorf("pagersBeyondTheManifest still absolves %s, but the manifest now flags it `paged` — "+
				"the justification is obsolete, delete it", key)
		}
	}
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

	// Anti-vacuity floor, and a short history so a change to this number is
	// never mistakable for a parse failure being papered over:
	//
	//	8  the manifest's original count
	//	7  macros.list came OUT — its handler is @PaginationExempt and
	//	   returns a bare array capped at 200 rows by usage rank
	//	10 contacts.journey, conversations.unattended and search.query went
	//	   IN — all three answer {items, nextCursor} and were simply never
	//	   flagged; found by reading the handlers, not by trusting the file
	//	13 bots.list, bots.deliveries and triggers.list came with the rest
	//	   of the surface on 2026-10-14; all three answer
	//	   {items, nextCursor, hasMore} from buildPaginatedResponse
	//
	// Every move was handler evidence in one direction or the other. If this
	// has to move again, say which route and why, in the commit.
	//
	// One route reads like a fourteenth and is not: billing.invoices IS
	// cursor-paginated, but its envelope is {data, nextCursor}, so the
	// generic walker — which reads `items` — cannot be pointed at it and
	// would report an empty page forever. It is declared `object` and has no
	// pager on purpose; follow its cursor by hand.
	const pagedFloor = 13
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

// The negative direction: a paginator fed the WRONG shape must fail LOUDLY.
//
// TestEveryMethodHitsItsDocumentedRoute proves each method copes with the
// shape this SDK declares its route sends. It cannot prove the declaration
// matters — a decoder that ignored the body entirely would pass it. This one
// feeds every auto-paginator a bare array where it expects
// {items, nextCursor} and requires an error, which is the answer the real API
// handed the .NET and Rust clients on macros.list.
//
// "Loudly" is the whole point. Go's encoding/json will decode some wrong
// bodies into a zero-valued struct and report nothing, and the Rust client
// did exactly that: it read a bare array as an EMPTY PAGE and returned it
// with a 200, so the caller got "no results" instead of an error. A pager
// here that ends the walk with Err() == nil has that bug, and the assertion
// below distinguishes the two cases rather than only checking Next.
func TestEveryAutoPaginatorRejectsABareArrayWhereAPageIsExpected(t *testing.T) {
	pagers := sdkPagers()

	// One pager per `page` route, and nothing else — the floor that stops
	// this test quietly shrinking. The set comes from this SDK's own
	// hand-written shape column, not from the manifest's `paged` flag, so a
	// manifest that lost a flag cannot shrink what is exercised here.
	declared := make(map[string]bool)
	for _, tc := range surfaceCases() {
		if tc.shape == shapePage {
			declared[tc.name] = true
		}
	}
	if len(declared) != 13 {
		t.Fatalf("%d routes are declared `page`, expected 13", len(declared))
	}
	for key := range declared {
		if _, ok := pagers[key]; !ok {
			t.Errorf("%s is declared `page` but has no auto-paginator to exercise", key)
		}
	}
	for key := range pagers {
		if !declared[key] {
			t.Errorf("there is a pager for %s, which this SDK does not declare `page`", key)
		}
	}

	for key, build := range pagers {
		key, build := key, build
		t.Run(key, func(t *testing.T) {
			var hits int
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				w.Header().Set("content-type", "application/json")
				_, _ = w.Write([]byte(`[{"id":"x_1"}]`))
			}))
			p := build(c)
			if p.Next(context.Background()) {
				t.Fatalf("%s yielded %v from a bare array", key, p.Value())
			}
			if p.Err() == nil {
				t.Fatalf("%s's paginator accepted a bare array as a page and ended the walk with "+
					"no error: a caller sees an empty result set instead of a decode failure, "+
					"which is a wrong answer rather than a missing one", key)
			}
			// The error has to come from reading the RESPONSE. A pager that
			// never issued the request would satisfy the assertion above for
			// the wrong reason.
			if hits != 1 {
				t.Errorf("%s made %d requests before failing, want exactly 1", key, hits)
			}
			if p.Count() != 0 {
				t.Errorf("%s yielded %d items from a non-page body", key, p.Count())
			}
		})
	}
}
