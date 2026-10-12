package chatly

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// Every hand-written method in this package, driven against a recording
// server, asserting the VERB and PATH it puts on the wire.
//
// Why this table exists: a typo in a path is invisible to a type checker, to
// go vet, and to every other test here, and it surfaces as a 404 in someone
// else's integration. The paths below are copied from the SDK surface
// contract (packages/sdk-spec/surface.json), so this is a comparison against
// the spec rather than against the implementation's own idea of itself.
//
// `body` is the fixture the fake API answers with — it has to match the shape
// the method decodes into, because a bare-array route decoded as an object
// (or the reverse) is a silent empty result rather than an error.
//
// This test also EMITS surface.lock.json — see writeSurfaceLock below for why
// the cross-language parity gate reads that file instead of reading this
// source.
func TestEveryMethodHitsItsDocumentedRoute(t *testing.T) {
	const (
		obj   = `{}`
		arr   = `[]`
		page  = `{"items":[],"nextCursor":null}`
		idObj = `{"id":"x"}`
	)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		call   func(context.Context, *Client) error
	}{
		// root
		{"root.whoami", "GET", "/v1/api-keys/whoami", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Whoami(ctx)
			return err
		}},

		// contacts
		{"contacts.list", "GET", "/v1/contacts", page, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.List(ctx, nil)
			return err
		}},
		{"contacts.get", "GET", "/v1/contacts/ct_1", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.Get(ctx, "ct_1")
			return err
		}},
		{"contacts.upsert", "POST", "/v1/contacts", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.Upsert(ctx, ContactInput{Email: "a@b.test"})
			return err
		}},
		{"contacts.bulk_upsert", "POST", "/v1/contacts/bulk", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.BulkUpsert(ctx, []ContactInput{{Email: "a@b.test"}})
			return err
		}},
		{"contacts.merge", "POST", "/v1/contacts/ct_1/merge", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.Merge(ctx, "ct_1", "ct_2")
			return err
		}},
		{"contacts.add_tags", "POST", "/v1/contacts/ct_1/tags", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.AddTags(ctx, "ct_1", "vip")
			return err
		}},
		{"contacts.remove_tag", "DELETE", "/v1/contacts/ct_1/tags/vip", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.RemoveTag(ctx, "ct_1", "vip")
			return err
		}},
		{"contacts.unsubscribe", "POST", "/v1/contacts/ct_1/unsubscribe", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.Unsubscribe(ctx, "ct_1", UnsubscribeInput{Unsubscribed: true})
			return err
		}},
		{"contacts.journey", "GET", "/v1/contacts/ct_1/journey", page, func(ctx context.Context, c *Client) error {
			_, err := c.Contacts.Journey(ctx, "ct_1", nil)
			return err
		}},

		// conversations
		{"conversations.list", "GET", "/v1/conversations", page, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.List(ctx, nil)
			return err
		}},
		{"conversations.get", "GET", "/v1/conversations/cv_1", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.Get(ctx, "cv_1")
			return err
		}},
		{"conversations.messages", "GET", "/v1/conversations/cv_1/messages", page, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.Messages(ctx, "cv_1", nil)
			return err
		}},
		{"conversations.send", "POST", "/v1/conversations/cv_1/messages", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.Send(ctx, "cv_1", SendMessageInput{Body: "hi"})
			return err
		}},
		{"conversations.unattended", "GET", "/v1/conversations/unattended", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.Unattended(ctx, nil)
			return err
		}},
		{"conversations.agent_handling", "GET", "/v1/conversations/cv_1/agent-handling", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.AgentHandling(ctx, "cv_1")
			return err
		}},
		{"conversations.set_agent_handling", "PUT", "/v1/conversations/cv_1/agent-handling", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.SetAgentHandling(ctx, "cv_1", SetAgentHandlingInput{State: "handled"})
			return err
		}},
		{"conversations.generated_replies", "GET", "/v1/conversations/cv_1/generated-replies", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.GeneratedReplies(ctx, "cv_1")
			return err
		}},
		{"conversations.generate_reply", "POST", "/v1/conversations/cv_1/generated-replies", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.GenerateReply(ctx, "cv_1", GenerateReplyInput{Body: "draft"})
			return err
		}},
		{"conversations.scheduled_messages", "GET", "/v1/conversations/cv_1/scheduled-messages", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.ScheduledMessages(ctx, "cv_1")
			return err
		}},
		{"conversations.schedule_message", "POST", "/v1/conversations/cv_1/scheduled-messages", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.ScheduleMessage(ctx, "cv_1", ScheduleMessageInput{Body: "later", SendAt: time.Now().Add(time.Hour)})
			return err
		}},
		{"conversations.cancel_scheduled", "DELETE", "/v1/conversations/scheduled-messages/sm_1", idObj, func(ctx context.Context, c *Client) error {
			_, err := c.Conversations.CancelScheduled(ctx, "sm_1")
			return err
		}},

		// tickets
		{"tickets.list", "GET", "/v1/tickets", page, func(ctx context.Context, c *Client) error {
			_, err := c.Tickets.List(ctx, nil)
			return err
		}},
		{"tickets.comments", "GET", "/v1/tickets/tk_1/comments", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Tickets.Comments(ctx, "tk_1")
			return err
		}},
		{"tickets.for_conversation", "GET", "/v1/tickets/for-conversation/cv_1", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Tickets.ForConversation(ctx, "cv_1")
			return err
		}},
		{"tickets.queues", "GET", "/v1/tickets/queues", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Tickets.Queues(ctx)
			return err
		}},
		{"tickets.merge", "POST", "/v1/tickets/tk_1/merge", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Tickets.Merge(ctx, "tk_1", "tk_2")
			return err
		}},
		{"tickets.split", "POST", "/v1/tickets/tk_1/split", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Tickets.Split(ctx, "tk_1", TicketSplitInput{Title: "Follow-up"})
			return err
		}},
		{"tickets.bulk", "POST", "/v1/tickets/bulk", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Tickets.Bulk(ctx, []string{"tk_1"}, TicketBulkStatus, TicketBulkPayload{Status: "open"})
			return err
		}},

		// macros
		{"macros.list", "GET", "/v1/macros", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Macros.List(ctx, nil)
			return err
		}},
		{"macros.create", "POST", "/v1/macros", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Macros.Create(ctx, MacroInput{Name: "Refund", Scope: "workspace", Body: "text"})
			return err
		}},
		{"macros.update", "PATCH", "/v1/macros/mc_1", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Macros.Update(ctx, "mc_1", MacroInput{Name: "Refund v2"})
			return err
		}},
		{"macros.delete", "DELETE", "/v1/macros/mc_1", "", func(ctx context.Context, c *Client) error {
			return c.Macros.Delete(ctx, "mc_1")
		}},

		// kb
		{"kb.articles", "GET", "/v1/kb/articles", page, func(ctx context.Context, c *Client) error {
			_, err := c.KB.Articles(ctx, nil)
			return err
		}},
		{"kb.article", "GET", "/v1/kb/articles/reset-password", obj, func(ctx context.Context, c *Client) error {
			_, err := c.KB.Article(ctx, "reset-password")
			return err
		}},
		{"kb.collections", "GET", "/v1/kb/collections", arr, func(ctx context.Context, c *Client) error {
			_, err := c.KB.Collections(ctx)
			return err
		}},

		// search
		{"search.query", "GET", "/v1/search", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Search.Query(ctx, "refund", nil)
			return err
		}},

		// events
		{"events.track", "POST", "/v1/cdp/events", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Events.Track(ctx, TrackEventInput{ContactID: "ct_1", Name: "order_placed"})
			return err
		}},
		{"events.list", "GET", "/v1/cdp/events", page, func(ctx context.Context, c *Client) error {
			_, err := c.Events.List(ctx, nil)
			return err
		}},

		// webhooks
		{"webhooks.deliveries", "GET", "/v1/webhooks/deliveries", page, func(ctx context.Context, c *Client) error {
			_, err := c.Webhooks.Deliveries(ctx, nil)
			return err
		}},
		{"webhooks.delivery", "GET", "/v1/webhooks/deliveries/wd_1", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Webhooks.Delivery(ctx, "wd_1")
			return err
		}},
		{"webhooks.redeliver", "POST", "/v1/webhooks/deliveries/wd_1/redeliver", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Webhooks.Redeliver(ctx, "wd_1")
			return err
		}},

		// reports
		{"reports.overview", "GET", "/v1/reports/overview", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Reports.Overview(ctx, nil)
			return err
		}},
		{"reports.agents", "GET", "/v1/reports/agents", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Reports.Agents(ctx, nil)
			return err
		}},
		{"reports.conversations_by_day", "GET", "/v1/reports/conversations-by-day", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Reports.ConversationsByDay(ctx, nil)
			return err
		}},
		{"reports.sla", "GET", "/v1/reports/sla", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Reports.SLA(ctx, nil)
			return err
		}},

		// channels
		{"channels.list", "GET", "/v1/channels", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Channels.List(ctx)
			return err
		}},
		{"channels.get", "GET", "/v1/channels/ch_1", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Channels.Get(ctx, "ch_1")
			return err
		}},

		// billing
		{"billing.plans", "GET", "/v1/billing/plans", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Billing.Plans(ctx)
			return err
		}},
		{"billing.usage", "GET", "/v1/billing/usage", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Billing.Usage(ctx, UsageEventConversationOpened, nil)
			return err
		}},

		// workspace
		{"workspace.settings", "GET", "/v1/workspace/settings", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Workspace.Settings(ctx)
			return err
		}},
		{"workspace.labels", "GET", "/v1/labels", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Workspace.Labels(ctx, nil)
			return err
		}},
		{"workspace.inboxes", "GET", "/v1/inboxes", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Workspace.Inboxes(ctx)
			return err
		}},
		{"workspace.team_members", "GET", "/v1/teams/tm_1/members", arr, func(ctx context.Context, c *Client) error {
			_, err := c.Workspace.TeamMembers(ctx, "tm_1")
			return err
		}},
		{"workspace.agent_skills", "GET", "/v1/agents/skills", obj, func(ctx context.Context, c *Client) error {
			_, err := c.Workspace.AgentSkills(ctx)
			return err
		}},
	}

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
			var gotMethod, gotPath string
			var hits int
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				hits++
				if tc.body == "" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))

			if err := tc.call(context.Background(), c); err != nil {
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

			// The fixture this case answered with must be the shape the
			// route really returns. A page envelope handed to a bare-array
			// route is the exact way two sibling SDKs stayed green while
			// failing on every real call.
			isPageFixture := tc.body == page
			_, justified := pagersBeyondTheManifest[tc.name]
			switch {
			case entry.Paged && !isPageFixture:
				t.Errorf("%s is paged in the manifest but its fixture is not a page envelope", tc.name)
			case !entry.Paged && isPageFixture && !justified:
				t.Errorf("%s is not paged in the manifest but its fixture answers {items,nextCursor}; "+
					"either the manifest is wrong or this test is agreeing with a bug", tc.name)
			}

			// What goes in the lock is the OBSERVED request, templated back
			// against the manifest — never the manifest row itself. Copying
			// the manifest across would make the artifact agree with the
			// spec by construction, which is the whole failure this replaces.
			templated, err := templatePath(gotPath, entry.Path)
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
				return
			}
			if gotMethod != entry.HTTP {
				t.Errorf("%s: called %s, manifest says %s", tc.name, gotMethod, entry.HTTP)
			}
			recorded = append(recorded, lockEntry{
				NS:   entry.NS,
				Name: entry.Name,
				HTTP: gotMethod,
				Path: templated,
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
