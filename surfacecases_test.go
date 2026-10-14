package chatly

// THE SURFACE TABLE, first half: the conversational surface — the routes an
// integration touches while handling customers.
//
// Split in two because one file of 162 rows is well past this repo's
// 650-line ceiling, and split HERE rather than at row 80 because the halves
// answer different questions: this file is "talk to people", its sibling
// (surfacecases_config_test.go) is "configure the workspace". surfaceCases
// concatenates them and is what every test reads.

import (
	"context"
	"time"
)

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
	core := coreSurfaceCases()
	return append(core, configSurfaceCases()...)
}

// coreSurfaceCases is the conversational surface: the credential, contacts,
// conversations, tickets, macros, the knowledge base, search, CDP events, the
// webhook delivery log, reports, channels, billing and the workspace reads.
//
// These were the SDK's first 55 routes, which is why the shape distribution
// in TestDeclaredShapesCoverTheSurface records a figure for them as well as
// for the whole surface.
func coreSurfaceCases() []surfaceCase {
	return []surfaceCase{
		// root
		{"root.whoami", "GET", "/v1/api-keys/whoami", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Whoami(ctx)
		}},
		// Discriminated on `principal`, so an Object rather than a struct —
		// the key branch has no user-shaped fields at all.
		{"root.me", "GET", "/v1/me", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Me(ctx)
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
		// A bare array of one entry for a key: the workspace it was minted in.
		{"workspace.mine", "GET", "/v1/workspaces/me", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Workspace.Mine(ctx)
		}},
	}
}
