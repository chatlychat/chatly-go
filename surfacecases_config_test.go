package chatly

// THE SURFACE TABLE, second half: the configuration surface — the routes that
// set a workspace up rather than serve a customer.
//
// AI providers, tone and tools; bots and the routing rules that pick one;
// brands; async canvas steps; channels; customer cards and their endpoint
// secrets; documentation sources; email templates; knowledge gaps; triggers;
// and the webhook ENDPOINT half of the webhooks namespace (its delivery log
// lives in the core half).
//
// Same rules as the core half, and the one that matters most: the `shape`
// column is read from each handler's @ZodResponse schema BY HAND and never
// copied from surface.json. See the comment on surfaceCases.

import (
	"context"
)

func configSurfaceCases() []surfaceCase {
	return []surfaceCase{
		// ai
		{"ai.providers", "GET", "/v1/ai-providers", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.Providers(ctx)
		}},
		{"ai.add_provider", "POST", "/v1/ai-providers", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.AddProvider(ctx, AIProviderOpenAI, "sk-0123456789abcdefghij")
		}},
		{"ai.test_provider", "POST", "/v1/ai-providers/openai/test", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.TestProvider(ctx, AIProviderOpenAI)
		}},
		{"ai.quota", "GET", "/v1/ai/quota", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.Quota(ctx)
		}},
		{"ai.tone", "GET", "/v1/ai/tone", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.Tone(ctx)
		}},
		{"ai.tone_preview", "GET", "/v1/ai/tone/preview", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.TonePreview(ctx)
		}},
		// A PUT that answers with the same bare array the GET does, re-read
		// after the write — not `{rules}` and not a 204.
		{"ai.set_tone", "PUT", "/v1/ai/tone", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.SetTone(ctx, []ToneRuleInput{{Category: ToneCategoryWarmth, Rule: "Be brief"}})
		}},
		{"ai.generate_tone", "POST", "/v1/ai/tone/generate", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.GenerateTone(ctx, "We write plainly and we never blame the customer for a bug of ours.")
		}},
		{"ai.tools", "GET", "/v1/ai/tools", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.Tools(ctx)
		}},
		{"ai.create_tool", "POST", "/v1/ai/tools", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.CreateTool(ctx, AIToolInput{
				Name: "order_lookup", Description: "Find an order", URLTemplate: "https://api.acme.test/orders/{{id}}",
			})
		}},
		{"ai.update_tool", "PATCH", "/v1/ai/tools/tl_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.UpdateTool(ctx, "tl_1", AIToolInput{Description: "Find an order by number"})
		}},
		// A DELETE with a body: `{deleted:true}` at 200.
		{"ai.delete_tool", "DELETE", "/v1/ai/tools/tl_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.AI.DeleteTool(ctx, "tl_1")
		}},

		// billing
		{"billing.summary", "GET", "/v1/billing", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Billing.Summary(ctx)
		}},
		// Cursor-paginated and still an OBJECT: the envelope is
		// {data, nextCursor}, so it is not the `page` shape and has no pager.
		{"billing.invoices", "GET", "/v1/billing/invoices", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Billing.Invoices(ctx, nil)
		}},
		{"billing.usage_daily", "GET", "/v1/billing/usage/daily", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Billing.UsageDaily(ctx, UsageEventAITokens, nil)
		}},
		{"billing.usage_monthly", "GET", "/v1/billing/usage/monthly", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Billing.UsageMonthly(ctx, nil)
		}},

		// bot_routes
		{"bot_routes.list", "GET", "/v1/bot-routes", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.BotRoutes.List(ctx)
		}},
		{"bot_routes.fields", "GET", "/v1/bot-routes/fields", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.BotRoutes.Fields(ctx)
		}},
		{"bot_routes.create", "POST", "/v1/bot-routes", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.BotRoutes.Create(ctx, BotRouteInput{BotID: "bt_1", Name: "Billing"})
		}},
		{"bot_routes.update", "PATCH", "/v1/bot-routes/br_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.BotRoutes.Update(ctx, "br_1", BotRouteInput{Priority: 10})
		}},
		{"bot_routes.resolve", "POST", "/v1/bot-routes/resolve", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.BotRoutes.Resolve(ctx, BotRouteResolveInput{Body: "where is my order"})
		}},
		{"bot_routes.delete", "DELETE", "/v1/bot-routes/br_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.BotRoutes.Delete(ctx, "br_1")
		}},

		// bots
		{"bots.list", "GET", "/v1/bots", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.List(ctx, nil)
		}},
		{"bots.get", "GET", "/v1/bots/bt_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.Get(ctx, "bt_1")
		}},
		{"bots.create", "POST", "/v1/bots", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.Create(ctx, BotInput{Name: "Returns", SystemPrompt: "Answer questions about returns."})
		}},
		{"bots.update", "PATCH", "/v1/bots/bt_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.Update(ctx, "bt_1", BotInput{Status: "paused"})
		}},
		{"bots.update_external", "PATCH", "/v1/bots/bt_1/external", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.UpdateExternal(ctx, "bt_1", BotInput{TimeoutSeconds: 30})
		}},
		{"bots.rotate_external_secret", "POST", "/v1/bots/bt_1/external/rotate-secret", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.RotateExternalSecret(ctx, "bt_1")
		}},
		{"bots.analytics", "GET", "/v1/bots/bt_1/analytics", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.Analytics(ctx, "bt_1", nil)
		}},
		{"bots.deliveries", "GET", "/v1/bots/bt_1/deliveries", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.Deliveries(ctx, "bt_1", nil)
		}},
		{"bots.delivery", "GET", "/v1/bots/bt_1/deliveries/dl_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.Delivery(ctx, "bt_1", "dl_1")
		}},
		{"bots.redeliver", "POST", "/v1/bots/bt_1/deliveries/dl_1/redeliver", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.Redeliver(ctx, "bt_1", "dl_1")
		}},
		{"bots.test_delivery", "POST", "/v1/bots/bt_1/test-delivery", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Bots.TestDelivery(ctx, "bt_1")
		}},

		// brands
		{"brands.list", "GET", "/v1/brands", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Brands.List(ctx)
		}},
		{"brands.create", "POST", "/v1/brands", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Brands.Create(ctx, BrandInput{Name: "Acme Support", Slug: "acme"})
		}},
		{"brands.update", "PATCH", "/v1/brands/bd_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Brands.Update(ctx, "bd_1", BrandInput{PrimaryColor: "#112233"})
		}},
		// A DELETE that answers `{id}` rather than a 204 — the soft-deleted
		// id is the caller's only confirmation.
		{"brands.delete", "DELETE", "/v1/brands/bd_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Brands.Delete(ctx, "bd_1")
		}},

		// canvas_steps
		{"canvas_steps.get", "GET", "/v1/canvas-steps/cs_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CanvasSteps.Get(ctx, "cs_1")
		}},
		{"canvas_steps.resolve", "POST", "/v1/canvas-steps/cs_1/resolve", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CanvasSteps.Resolve(ctx, "cs_1", map[string]any{"v": 1, "canvasId": "receipt"})
		}},

		// channels
		// The one route on this surface whose array holds STRINGS, which is
		// why it has a fixture of its own — see nonObjectArrayFixtures.
		{"channels.adapters", "GET", "/v1/channels/adapters", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Channels.Adapters(ctx)
		}},
		{"channels.create", "POST", "/v1/channels", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Channels.Create(ctx, ChannelInput{Type: ChannelTypeWeb, Name: "Website", PublicID: "acme-web"})
		}},
		{"channels.update", "PATCH", "/v1/channels/ch_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Channels.Update(ctx, "ch_1", ChannelPatch{Name: "Website (EU)"})
		}},
		{"channels.rotate_secret", "POST", "/v1/channels/ch_1/secret/rotate", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Channels.RotateSecret(ctx, "ch_1")
		}},

		// contacts (the two added after the original 55)
		{"contacts.cards", "POST", "/v1/contacts/ct_1/cards", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.Cards(ctx, "ct_1", "")
		}},
		{"contacts.mood", "GET", "/v1/contacts/ct_1/mood", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Contacts.Mood(ctx, "ct_1", nil)
		}},

		// conversations (added after the original 55)
		{"conversations.linked", "GET", "/v1/conversations/cv_1/linked", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.Linked(ctx, "cv_1")
		}},
		{"conversations.unread_counts", "GET", "/v1/conversations/unread-counts", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.UnreadCounts(ctx, nil)
		}},
		{"conversations.update_blocks", "PATCH", "/v1/conversations/cv_1/messages/ms_1/blocks", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.UpdateBlocks(ctx, "cv_1", "ms_1", []MessageBlockPatch{
				{ID: "blk_1", Set: map[string]any{"text": "Refunded"}},
			})
		}},
		{"conversations.return_to_bot", "POST", "/v1/conversations/cv_1/return-to-bot", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.ReturnToBot(ctx, "cv_1", "bt_1")
		}},
		{"conversations.outcomes", "GET", "/v1/conversations/cv_1/outcomes", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.Outcomes(ctx, "cv_1")
		}},
		{"conversations.record_outcome", "POST", "/v1/conversations/cv_1/outcomes", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Conversations.RecordOutcome(ctx, "cv_1", ConversationOutcomeInput{Outcome: OutcomeResolved})
		}},

		// customer_cards
		{"customer_cards.list", "GET", "/v1/customer-cards", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCards.List(ctx)
		}},
		{"customer_cards.create", "POST", "/v1/customer-cards", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCards.Create(ctx, CustomerCardInput{
				Key: "orders", Title: "Recent orders", URL: "https://api.acme.test/cards/orders", TTLSeconds: 60,
			})
		}},
		{"customer_cards.update", "PATCH", "/v1/customer-cards/cc_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCards.Update(ctx, "cc_1", CustomerCardInput{Title: "Orders"})
		}},
		{"customer_cards.rotate_secret", "POST", "/v1/customer-cards/cc_1/rotate-secret", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCards.RotateSecret(ctx, "cc_1")
		}},
		{"customer_cards.delete", "DELETE", "/v1/customer-cards/cc_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCards.Delete(ctx, "cc_1")
		}},

		// customer_card_endpoints
		{"customer_card_endpoints.list", "GET", "/v1/customer-card-endpoints", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCardEndpoints.List(ctx)
		}},
		{"customer_card_endpoints.create", "POST", "/v1/customer-card-endpoints", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCardEndpoints.Create(ctx, "https://api.acme.test/cards/orders")
		}},
		{"customer_card_endpoints.rotate_secret", "POST", "/v1/customer-card-endpoints/ce_1/rotate-secret", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCardEndpoints.RotateSecret(ctx, "ce_1")
		}},
		{"customer_card_endpoints.activate_secret", "POST", "/v1/customer-card-endpoints/ce_1/activate-secret", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCardEndpoints.ActivateSecret(ctx, "ce_1")
		}},
		{"customer_card_endpoints.delete", "DELETE", "/v1/customer-card-endpoints/ce_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.CustomerCardEndpoints.Delete(ctx, "ce_1")
		}},

		// docs_sources
		{"docs_sources.providers", "GET", "/v1/docs-sources/providers", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.Providers(ctx)
		}},
		{"docs_sources.list", "GET", "/v1/docs-sources", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.List(ctx)
		}},
		// A POST that answers with a bare array of spaces.
		{"docs_sources.discover", "POST", "/v1/docs-sources/discover", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.Discover(ctx, DocsDiscoverInput{Provider: DocsProviderGitBook, Token: "gb_test"})
		}},
		{"docs_sources.create", "POST", "/v1/docs-sources", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.Create(ctx, DocsSourceInput{
				Provider: DocsProviderGitBook, Token: "gb_test", OrganizationID: "org_1", SpaceID: "sp_1",
			})
		}},
		{"docs_sources.update", "PATCH", "/v1/docs-sources/ds_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.Update(ctx, "ds_1", DocsSourcePatch{Status: "paused"})
		}},
		{"docs_sources.sync", "POST", "/v1/docs-sources/ds_1/sync", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.Sync(ctx, "ds_1", true)
		}},
		{"docs_sources.webhook", "POST", "/v1/docs-sources/ds_1/webhook", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.Webhook(ctx, "ds_1")
		}},
		{"docs_sources.delete", "DELETE", "/v1/docs-sources/ds_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.DocsSources.Delete(ctx, "ds_1")
		}},

		// email_templates
		{"email_templates.list", "GET", "/v1/email-templates", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.EmailTemplates.List(ctx)
		}},
		{"email_templates.preview", "POST", "/v1/email-templates/preview", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.EmailTemplates.Preview(ctx, EmailTemplateWorkspaceInvite, "Join us", "Hello {{inviteeEmail}}")
		}},
		{"email_templates.update", "PUT", "/v1/email-templates/workspace_invite", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.EmailTemplates.Update(ctx, EmailTemplateWorkspaceInvite, "Join us", "Hello")
		}},
		// "Restore the built-in" — a DELETE that answers `{restored}`.
		{"email_templates.delete", "DELETE", "/v1/email-templates/workspace_invite", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.EmailTemplates.Delete(ctx, EmailTemplateWorkspaceInvite)
		}},

		// knowledge_gaps
		{"knowledge_gaps.list", "GET", "/v1/knowledge-gaps", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.KnowledgeGaps.List(ctx, nil)
		}},
		{"knowledge_gaps.update", "PATCH", "/v1/knowledge-gaps/kg_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.KnowledgeGaps.Update(ctx, "kg_1", KnowledgeGapDismissed)
		}},
		{"knowledge_gaps.draft", "POST", "/v1/knowledge-gaps/kg_1/draft", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.KnowledgeGaps.Draft(ctx, "kg_1")
		}},

		// macros (added after the original 55)
		{"macros.get", "GET", "/v1/macros/mc_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Macros.Get(ctx, "mc_1")
		}},
		{"macros.actions", "GET", "/v1/macros/actions", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Macros.Actions(ctx)
		}},
		{"macros.templates", "GET", "/v1/macros/templates", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Macros.Templates(ctx)
		}},
		{"macros.template", "GET", "/v1/macros/templates/refund-apology", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Macros.Template(ctx, "refund-apology")
		}},

		// reports (added after the original 55)
		{"reports.sources", "GET", "/v1/reports/sources", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Sources(ctx, nil)
		}},
		{"reports.attribution", "GET", "/v1/reports/attribution", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Attribution(ctx, nil)
		}},
		{"reports.cohorts", "GET", "/v1/reports/cohorts", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Cohorts(ctx, nil)
		}},
		{"reports.response_times", "GET", "/v1/reports/response-times", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.ResponseTimes(ctx, nil)
		}},
		{"reports.voice", "GET", "/v1/reports/voice", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Voice(ctx, nil)
		}},
		{"reports.teams", "GET", "/v1/reports/teams", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Teams(ctx, nil)
		}},
		{"reports.labels", "GET", "/v1/reports/labels", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Labels(ctx, nil)
		}},
		{"reports.themes", "GET", "/v1/reports/themes", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.Themes(ctx, nil)
		}},
		{"reports.theme_conversations", "GET", "/v1/reports/themes/th_1/conversations", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Reports.ThemeConversations(ctx, "th_1")
		}},

		// tickets (added after the original 55)
		{"tickets.conversations", "GET", "/v1/tickets/tk_1/conversations", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.Conversations(ctx, "tk_1")
		}},
		{"tickets.open_count", "GET", "/v1/tickets/open-count/cv_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.OpenCount(ctx, "cv_1")
		}},
		{"tickets.time", "GET", "/v1/tickets/tk_1/time", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.Time(ctx, "tk_1")
		}},
		{"tickets.status_history", "GET", "/v1/tickets/tk_1/status-history", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.StatusHistory(ctx, "tk_1")
		}},
		{"tickets.lineage", "GET", "/v1/tickets/tk_1/lineage", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Tickets.Lineage(ctx, "tk_1")
		}},

		// triggers
		{"triggers.list", "GET", "/v1/triggers", shapePage, func(ctx context.Context, c *Client) (any, error) {
			return c.Triggers.List(ctx, nil)
		}},
		{"triggers.create", "POST", "/v1/triggers", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Triggers.Create(ctx, TriggerInput{
				Name: "Welcome", EventType: "conversation.created", Enabled: Bool(true),
				Actions: []TriggerAction{{Type: "send_message", Params: map[string]any{"body": "hi"}}},
			})
		}},
		{"triggers.delete", "DELETE", "/v1/triggers/tg_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Triggers.Delete(ctx, "tg_1")
		}},

		// webhooks (the endpoint half, added after the original 55)
		{"webhooks.list", "GET", "/v1/webhooks", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.List(ctx)
		}},
		{"webhooks.events", "GET", "/v1/webhooks/events", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.Events(ctx)
		}},
		{"webhooks.create", "POST", "/v1/webhooks", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.Create(ctx, WebhookEndpointInput{
				URL: "https://acme.test/hooks/chatly", EventTypes: []string{"conversation.created"},
			})
		}},
		{"webhooks.schema_versions", "GET", "/v1/webhooks/schema-versions", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.SchemaVersions(ctx)
		}},
		{"webhooks.set_schema_version", "PATCH", "/v1/webhooks/wh_1/schema-version", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.SetSchemaVersion(ctx, "wh_1", "2026-10-14")
		}},
		// A DELETE with a body: `{ok:true}` at 200.
		{"webhooks.delete", "DELETE", "/v1/webhooks/wh_1", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Webhooks.Delete(ctx, "wh_1")
		}},

		// workspace (added after the original 55)
		{"workspace.key_scopes", "GET", "/v1/api-keys/scopes", shapeArray, func(ctx context.Context, c *Client) (any, error) {
			return c.Workspace.KeyScopes(ctx)
		}},
		{"workspace.secrets", "GET", "/v1/workspace/secrets", shapeObject, func(ctx context.Context, c *Client) (any, error) {
			return c.Workspace.Secrets(ctx)
		}},
	}
}
