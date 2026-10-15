package chatly

// attachServices wires every resource namespace to one client value.
//
// It is the ONLY place namespaces are constructed, and that is the point.
// WithIdempotencyKey returns a derived client, and the reference
// implementation of this SDK in another language rebuilt each namespace by
// hand in its clone path — then forgot one, so a derived client's `search`
// namespace still called the ORIGINAL client. It was harmless there only
// because that namespace happened to be read-only; the next write added to it
// would have silently escaped the pinned idempotency key.
//
// With one function, a new namespace cannot be added to New and missed in the
// clone path: both call this.
func attachServices(c *Client) {
	c.Contacts = &ContactsService{c: c}
	c.Conversations = &ConversationsService{c: c}
	c.Tickets = &TicketsService{c: c}
	c.Macros = &MacrosService{c: c}
	c.KB = &KBService{c: c}
	c.Search = &SearchService{c: c}
	c.Events = &EventsService{c: c}
	c.Webhooks = &WebhooksService{c: c}
	c.Reports = &ReportsService{c: c}
	c.Channels = &ChannelsService{c: c}
	c.Billing = &BillingService{c: c}
	c.Workspace = &WorkspaceService{c: c}
	c.AI = &AIService{c: c}
	c.Bots = &BotsService{c: c}
	c.BotRoutes = &BotRoutesService{c: c}
	c.Brands = &BrandsService{c: c}
	c.CanvasSteps = &CanvasStepsService{c: c}
	c.CustomerCards = &CustomerCardsService{c: c}
	c.CustomerCardEndpoints = &CustomerCardEndpointsService{c: c}
	c.DocsSources = &DocsSourcesService{c: c}
	c.EmailTemplates = &EmailTemplatesService{c: c}
	c.KnowledgeGaps = &KnowledgeGapsService{c: c}
	c.Triggers = &TriggersService{c: c}
	c.Visitors = &VisitorsService{c: c}
}
