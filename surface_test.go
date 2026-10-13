package chatly

import (
	"context"
	"net/http"
	"testing"
	"time"
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

// surfaceCase is one row of the table: a method, the verb and path it is
// supposed to request, the response shape its decoder expects, and the call.
type surfaceCase struct {
	name   string
	method string
	path   string
	shape  shape
	call   func(context.Context, *Client) (any, error)
}

// surfaceCases is every hand-written method in this package, paired with the
// VERB, PATH and RESPONSE SHAPE it is supposed to deal in.
//
// Why this table exists: a typo in a path is invisible to a type checker, to
// go vet, and to every other test here, and it surfaces as a 404 in someone
// else's integration. The paths below are copied from the SDK surface
// contract (packages/sdk-spec/surface.json), so this is a comparison against
// the spec rather than against the implementation's own idea of itself.
//
// ─── The `shape` column is HAND-WRITTEN, and must stay that way ───────────
//
// It is read from the handlers, NOT from surface.json — even though this
// suite loads that manifest for the verb and path templates. A fixture
// derived from the manifest agrees with whatever the manifest says,
// including when the manifest is wrong, and that is not hypothetical:
// `macros.list` was flagged `paged: true` for a day while its handler is
// @PaginationExempt and answers a bare 200-row array. The .NET and Rust
// clients built paginators over it and raised a decode error on every real
// call with green suites, because their fake transports answered EVERY
// route with `{items, nextCursor}`. One of them was worse than an error:
// the Rust client's derived Page deserializer read the bare array as an
// EMPTY PAGE and returned it with a 200 — a wrong answer rather than a
// missing one.
//
// So this column is a second opinion. The suite feeds each method the shape
// declared HERE (see responseForShape), asserts the value handed back really
// has that shape (actualShape), and writes the declaration into
// surface.lock.json; scripts/check-sdk-surface-parity.mjs then diffs eight
// independently written columns against the manifest. A wrong manifest row
// needs eight authors to misread the same handler.
//
// Three rows look wrong and are not. `contacts.remove_tag` is a DELETE that
// answers with the surviving contact, and `conversations.cancel_scheduled` a
// DELETE that answers with the row it just cancelled — so two of the three
// deletes here have bodies, and `macros.delete` is the only real 204 on the
// surface. Two of the four report routes are bare arrays.
//
// Two rows are `page` while their single-page method returns an Object
// rather than a *Page: conversations.unattended and search.query answer the
// page envelope PLUS fields a *Page would discard (`total`; `tier` and
// `facets`), so those methods hand back the whole object and the pager walks
// the cursor. The response shape is still `page`, which is what this column
// records — it describes the WIRE, not the Go return type.
//
// This table also EMITS surface.lock.json — see writeSurfaceLock for why the
// cross-language parity gate reads that file instead of reading this source.
func surfaceCases() []surfaceCase {
	return []surfaceCase{
		// root
		{"root.whoami", "GET", "/v1/api-keys/whoami", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Whoami(ctx)
		}},

		// contacts
		{"contacts.list", "GET", "/v1/contacts", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.List(ctx, nil)
		}},
		{"contacts.get", "GET", "/v1/contacts/ct_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.Get(ctx, "ct_1")
		}},
		{"contacts.upsert", "POST", "/v1/contacts", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.Upsert(ctx, ContactInput{Email: "a@b.test"})
		}},
		{"contacts.bulk_upsert", "POST", "/v1/contacts/bulk", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.BulkUpsert(ctx, []ContactInput{{Email: "a@b.test"}})
		}},
		{"contacts.merge", "POST", "/v1/contacts/ct_1/merge", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.Merge(ctx, "ct_1", "ct_2")
		}},
		{"contacts.add_tags", "POST", "/v1/contacts/ct_1/tags", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.AddTags(ctx, "ct_1", "vip")
		}},
		{"contacts.remove_tag", "DELETE", "/v1/contacts/ct_1/tags/vip", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.RemoveTag(ctx, "ct_1", "vip")
		}},
		{"contacts.unsubscribe", "POST", "/v1/contacts/ct_1/unsubscribe", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.Unsubscribe(ctx, "ct_1", UnsubscribeInput{Unsubscribed: true})
		}},
		{"contacts.journey", "GET", "/v1/contacts/ct_1/journey", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.Journey(ctx, "ct_1", nil)
		}},

		// conversations
		{"conversations.list", "GET", "/v1/conversations", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.List(ctx, nil)
		}},
		{"conversations.get", "GET", "/v1/conversations/cv_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.Get(ctx, "cv_1")
		}},
		{"conversations.messages", "GET", "/v1/conversations/cv_1/messages", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.Messages(ctx, "cv_1", nil)
		}},
		{"conversations.send", "POST", "/v1/conversations/cv_1/messages", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.Send(ctx, "cv_1", SendMessageInput{Body: "hi"})
		}},
		{"conversations.unattended", "GET", "/v1/conversations/unattended", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.Unattended(ctx, nil)
		}},
		{"conversations.agent_handling", "GET", "/v1/conversations/cv_1/agent-handling", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.AgentHandling(ctx, "cv_1")
		}},
		{"conversations.set_agent_handling", "PUT", "/v1/conversations/cv_1/agent-handling", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.SetAgentHandling(ctx, "cv_1", SetAgentHandlingInput{State: "handled"})
		}},
		{"conversations.generated_replies", "GET", "/v1/conversations/cv_1/generated-replies", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.GeneratedReplies(ctx, "cv_1")
		}},
		{"conversations.generate_reply", "POST", "/v1/conversations/cv_1/generated-replies", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.GenerateReply(ctx, "cv_1", GenerateReplyInput{Body: "draft"})
		}},
		{"conversations.scheduled_messages", "GET", "/v1/conversations/cv_1/scheduled-messages", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.ScheduledMessages(ctx, "cv_1")
		}},
		{"conversations.schedule_message", "POST", "/v1/conversations/cv_1/scheduled-messages", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.ScheduleMessage(ctx, "cv_1", ScheduleMessageInput{Body: "later", SendAt: time.Now().Add(time.Hour)})
		}},
		{"conversations.cancel_scheduled", "DELETE", "/v1/conversations/scheduled-messages/sm_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.CancelScheduled(ctx, "sm_1")
		}},

		// tickets
		{"tickets.list", "GET", "/v1/tickets", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.List(ctx, nil)
		}},
		{"tickets.comments", "GET", "/v1/tickets/tk_1/comments", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.Comments(ctx, "tk_1")
		}},
		{"tickets.for_conversation", "GET", "/v1/tickets/for-conversation/cv_1", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.ForConversation(ctx, "cv_1")
		}},
		{"tickets.queues", "GET", "/v1/tickets/queues", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.Queues(ctx)
		}},
		{"tickets.merge", "POST", "/v1/tickets/tk_1/merge", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.Merge(ctx, "tk_1", "tk_2")
		}},
		{"tickets.split", "POST", "/v1/tickets/tk_1/split", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.Split(ctx, "tk_1", TicketSplitInput{Title: "Follow-up"})
		}},
		{"tickets.bulk", "POST", "/v1/tickets/bulk", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.Bulk(ctx, []string{"tk_1"}, TicketBulkStatus, TicketBulkPayload{Status: "open"})
		}},

		// macros
		{"macros.list", "GET", "/v1/macros", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Macros.List(ctx, nil)
		}},
		{"macros.create", "POST", "/v1/macros", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Macros.Create(ctx, MacroInput{Name: "Refund", Scope: "workspace", Body: "text"})
		}},
		{"macros.update", "PATCH", "/v1/macros/mc_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Macros.Update(ctx, "mc_1", MacroInput{Name: "Refund v2"})
		}},
		{"macros.delete", "DELETE", "/v1/macros/mc_1", shapeEmpty, func(ctx context.Context, c *Client) (any, error) {
			return nil, c.Macros.Delete(ctx, "mc_1")
		}},

		// kb
		{"kb.articles", "GET", "/v1/kb/articles", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.KB.Articles(ctx, nil)
		}},
		{"kb.article", "GET", "/v1/kb/articles/reset-password", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.KB.Article(ctx, "reset-password")
		}},
		{"kb.collections", "GET", "/v1/kb/collections", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.KB.Collections(ctx)
		}},

		// search
		{"search.query", "GET", "/v1/search", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Search.Query(ctx, "refund", nil)
		}},

		// events
		{"events.track", "POST", "/v1/cdp/events", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Events.Track(ctx, TrackEventInput{ContactID: "ct_1", Name: "order_placed"})
		}},
		{"events.list", "GET", "/v1/cdp/events", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Events.List(ctx, nil)
		}},

		// webhooks
		{"webhooks.deliveries", "GET", "/v1/webhooks/deliveries", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.Deliveries(ctx, nil)
		}},
		{"webhooks.delivery", "GET", "/v1/webhooks/deliveries/wd_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.Delivery(ctx, "wd_1")
		}},
		{"webhooks.redeliver", "POST", "/v1/webhooks/deliveries/wd_1/redeliver", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.Redeliver(ctx, "wd_1")
		}},

		// reports
		{"reports.overview", "GET", "/v1/reports/overview", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Overview(ctx, nil)
		}},
		{"reports.agents", "GET", "/v1/reports/agents", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Agents(ctx, nil)
		}},
		{"reports.conversations_by_day", "GET", "/v1/reports/conversations-by-day", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.ConversationsByDay(ctx, nil)
		}},
		{"reports.sla", "GET", "/v1/reports/sla", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.SLA(ctx, nil)
		}},

		// channels
		{"channels.list", "GET", "/v1/channels", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Channels.List(ctx)
		}},
		{"channels.get", "GET", "/v1/channels/ch_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Channels.Get(ctx, "ch_1")
		}},

		// billing
		{"billing.plans", "GET", "/v1/billing/plans", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Billing.Plans(ctx)
		}},
		{"billing.usage", "GET", "/v1/billing/usage", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Billing.Usage(ctx, UsageEventConversationOpened, nil)
		}},

		// workspace
		{"workspace.settings", "GET", "/v1/workspace/settings", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Workspace.Settings(ctx)
		}},
		{"workspace.labels", "GET", "/v1/labels", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Workspace.Labels(ctx, nil)
		}},
		{"workspace.inboxes", "GET", "/v1/inboxes", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Workspace.Inboxes(ctx)
		}},
		{"workspace.team_members", "GET", "/v1/teams/tm_1/members", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Workspace.TeamMembers(ctx, "tm_1")
		}},
		{"workspace.agent_skills", "GET", "/v1/agents/skills", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Workspace.AgentSkills(ctx)
		}},
	}
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
	if len(manifest) < 55 {
		t.Fatalf("manifest has %d entries; it had 55 when this SDK was written, so something truncated it", len(manifest))
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
// Anti-vacuity. A column that said `object` 55 times would satisfy every
// other assertion in this package — the fake would answer `{"id":"x_1"}`
// everywhere, every decoder would cope, and the gate would see unanimity
// about nothing. The numbers come from reading all 55 @ZodResponse schemas on
// 2026-10-12: 14 array, 10 page, 30 object, 1 empty. If the surface grows
// these change, but deliberately and with handler evidence per row — and
// `empty` stays at exactly one until a second real 204 ships.
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

	want := map[shape]int{shapeArray: 14, shapePage: 10, shapeObject: 30, shapeEmpty: 1}
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
