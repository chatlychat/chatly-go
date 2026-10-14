package chatly

// Every test in this package runs against net/http/httptest on loopback.
// None of them needs network, an API key, or a running Chatly API — which is
// the contract's rule and also the only way a test of "did we send the right
// header" can be trusted.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient wires a client to a loopback server and removes the backoff
// WAIT (not the retry) so a retry test costs microseconds instead of seconds.
// sleepFn is unexported, so this seam is not part of the public surface.
func newTestClient(t *testing.T, handler http.Handler, opts ...Option) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	all := append([]Option{WithBaseURL(srv.URL)}, opts...)
	c, err := New("ck_test_key", all...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.sleepFn = func(context.Context, time.Duration) error { return nil }
	return c, srv
}

func jsonHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

// ── CONTRACT §10.1 ─────────────────────────────────────────────────────────
// "The default base URL is the production host."
//
// This test exists because four sibling SDKs shipped pointing at
// api.livechat.local or api.chatly.example, and no test caught it: every
// other test in every one of them passes an explicit URL, exactly like
// newTestClient above does. So this one deliberately does NOT.
func TestDefaultBaseURLIsProduction(t *testing.T) {
	if DefaultBaseURL != "https://api.chatlychat.com" {
		t.Fatalf("DefaultBaseURL = %q, want https://api.chatlychat.com", DefaultBaseURL)
	}
	c, err := New("ck_test_key")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.BaseURL(); got != "https://api.chatlychat.com" {
		t.Fatalf("client base url = %q, want the production host", got)
	}
	for _, bad := range []string{".local", ".example", "localhost", "127.0.0.1", "staging", "dev."} {
		if strings.Contains(c.BaseURL(), bad) {
			t.Fatalf("default base url %q contains %q", c.BaseURL(), bad)
		}
	}
}

func TestNewRejectsEmptyKey(t *testing.T) {
	// Rejected at construction, not at the first call: an empty key is
	// almost always an unset environment variable.
	for _, key := range []string{"", "   ", "\t"} {
		if _, err := New(key); err == nil {
			t.Fatalf("New(%q) = nil error, want a construction error", key)
		}
	}
}

func TestWithBaseURLRejectsNonsense(t *testing.T) {
	for _, bad := range []string{"", "   ", "api.chatlychat.com", "ftp://x.test", "https://"} {
		if _, err := New("ck_x", WithBaseURL(bad)); err == nil {
			t.Fatalf("WithBaseURL(%q) accepted; want an error", bad)
		}
	}
	c, err := New("ck_x", WithBaseURL("https://self.hosted.test/"))
	if err != nil {
		t.Fatalf("WithBaseURL: %v", err)
	}
	if c.BaseURL() != "https://self.hosted.test" {
		t.Fatalf("trailing slash not trimmed: %q", c.BaseURL())
	}
}

// ── CONTRACT §10.2 ─────────────────────────────────────────────────────────
// "Authorization carries the key."
func TestRequestHeaders(t *testing.T) {
	var got http.Header
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"workspaceId":"ws_1","scopes":["contacts:read"]}`))
	}))

	me, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if me.WorkspaceID != "ws_1" || len(me.Scopes) != 1 {
		t.Fatalf("Whoami decoded wrong: %+v", me)
	}
	if got.Get("Authorization") != "Bearer ck_test_key" {
		t.Fatalf("Authorization = %q", got.Get("Authorization"))
	}
	if got.Get("Accept") != "application/json" {
		t.Fatalf("Accept = %q", got.Get("Accept"))
	}
	if ua := got.Get("User-Agent"); !strings.HasPrefix(ua, "chatly-sdk-go/") {
		t.Fatalf("User-Agent = %q, want the chatly-sdk-go/<version> token", ua)
	}
	// A GET has no body, so it must not claim a content type.
	if ct := got.Get("Content-Type"); ct != "" {
		t.Fatalf("GET sent Content-Type %q", ct)
	}
	// And a read must not carry an idempotency key.
	if k := got.Get("Idempotency-Key"); k != "" {
		t.Fatalf("GET sent Idempotency-Key %q", k)
	}
}

func TestUserAgentSuffix(t *testing.T) {
	var ua string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{}`))
	}), WithUserAgentSuffix("acme-billing/2.1"))
	if _, err := c.Workspace.Settings(context.Background()); err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if !strings.HasPrefix(ua, "chatly-sdk-go/") || !strings.HasSuffix(ua, "acme-billing/2.1") {
		t.Fatalf("User-Agent = %q", ua)
	}
}

func TestWithHeaderCannotOverrideOwnedHeaders(t *testing.T) {
	for _, name := range []string{"authorization", "Idempotency-Key", "content-type", "Accept", "user-agent"} {
		if _, err := New("ck_x", WithHeader(name, "x")); err == nil {
			t.Fatalf("WithHeader(%q) accepted; the SDK owns that header", name)
		}
	}
	c, _ := newTestClient(t, jsonHandler(200, `{}`), WithHeader("X-Trace", "abc"))
	if c.extraHeaders.Get("X-Trace") != "abc" {
		t.Fatal("WithHeader did not record the header")
	}
}

// ── CONTRACT §10.3 ─────────────────────────────────────────────────────────
// "A write sends an Idempotency-Key; two writes send different ones; a pinned
// key sends the same one twice."
func TestWritesSendFreshIdempotencyKeys(t *testing.T) {
	var keys []string
	var method, contentType string
	var body []byte
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		method = r.Method
		contentType = r.Header.Get("Content-Type")
		body, _ = readAll(r)
		_, _ = w.Write([]byte(`{"id":"ct_1"}`))
	}))

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.Contacts.Upsert(ctx, ContactInput{Email: "a@b.test"}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	if method != http.MethodPost || contentType != "application/json" {
		t.Fatalf("method=%q content-type=%q", method, contentType)
	}
	if string(body) != `{"email":"a@b.test"}` {
		t.Fatalf("body = %s — omitempty should drop every unset field", body)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d requests, want 2", len(keys))
	}
	for _, k := range keys {
		if len(k) != 36 {
			t.Fatalf("idempotency key %q is not a 36-char uuid", k)
		}
	}
	if keys[0] == keys[1] {
		t.Fatalf("two independent writes reused one key %q — an SDK retry could not tell them apart", keys[0])
	}
}

func TestPinnedIdempotencyKeyIsReusedAndDoesNotLeak(t *testing.T) {
	var keys []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		_, _ = w.Write([]byte(`{"id":"ct_1"}`))
	}))

	ctx := context.Background()
	pinned := c.WithIdempotencyKey("fixed-key-1")
	for i := 0; i < 2; i++ {
		if _, err := pinned.Contacts.Upsert(ctx, ContactInput{Email: "a@b.test"}); err != nil {
			t.Fatalf("pinned Upsert: %v", err)
		}
	}
	if keys[0] != "fixed-key-1" || keys[1] != "fixed-key-1" {
		t.Fatalf("pinned keys = %v, want both fixed-key-1", keys)
	}

	// The ORIGINAL client must be untouched: WithIdempotencyKey derives, it
	// does not mutate.
	if c.IdempotencyKey() != "" {
		t.Fatalf("parent client picked up the pinned key %q", c.IdempotencyKey())
	}
	if _, err := c.Contacts.Upsert(ctx, ContactInput{Email: "a@b.test"}); err != nil {
		t.Fatalf("parent Upsert: %v", err)
	}
	if keys[2] == "fixed-key-1" {
		t.Fatal("parent client sent the derived client's pinned key")
	}
}

// Every namespace on a derived client must route through the DERIVED client.
//
// This is the bug the Node SDK shipped: its clone path rebuilt namespaces by
// hand and missed one, so that namespace still called the parent and would
// have escaped the pinned key the moment it gained a write. Here the wiring
// lives in attachServices, and this test asserts the property rather than the
// implementation.
func TestEveryNamespaceOnADerivedClientUsesTheDerivedClient(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200, `{}`))
	d := c.WithIdempotencyKey("pinned")

	type namespace struct {
		name string
		got  *Client
	}
	for _, ns := range []namespace{
		{"Contacts", d.Contacts.c},
		{"Conversations", d.Conversations.c},
		{"Tickets", d.Tickets.c},
		{"Macros", d.Macros.c},
		{"KB", d.KB.c},
		{"Search", d.Search.c},
		{"Events", d.Events.c},
		{"Webhooks", d.Webhooks.c},
		{"Reports", d.Reports.c},
		{"Channels", d.Channels.c},
		{"Billing", d.Billing.c},
		{"Workspace", d.Workspace.c},
	} {
		if ns.got != d {
			t.Errorf("%s namespace points at the wrong client (pinned key %q)", ns.name, ns.got.IdempotencyKey())
		}
	}
}

// ── CONTRACT §2: query parameters ──────────────────────────────────────────
func TestRepeatedQueryKeysStayRepeated(t *testing.T) {
	var raw string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"items":[],"nextCursor":null}`))
	}))

	_, err := c.Tickets.List(context.Background(), &ListOptions{
		Limit:  50,
		Sort:   "-updatedAt",
		Filter: []string{"status:in:open,pending", "priority:urgent"},
	})
	if err != nil {
		t.Fatalf("Tickets.List: %v", err)
	}

	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", raw, err)
	}
	if len(q["filter"]) != 2 {
		t.Fatalf("filter arrived as %v — two clauses must stay two parameters, not one comma-joined value", q["filter"])
	}
	if q.Get("limit") != "50" || q.Get("sort") != "-updatedAt" {
		t.Fatalf("query = %v", q)
	}
	// Not the bracketed form: the API's parser leaves `filter[]` as a
	// literal key and routes that do not fold it discard the clause in
	// silence.
	if strings.Contains(raw, "filter%5B%5D") || strings.Contains(raw, "filter[]") {
		t.Fatalf("sent the bracketed filter form: %q", raw)
	}
}

func TestSearchSendsRepeatedKindsAndNotBrackets(t *testing.T) {
	var raw string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"items":[],"nextCursor":null,"tier":"postgres","facets":{}}`))
	}))

	_, err := c.Search.Query(context.Background(), "refund policy", &SearchOptions{
		Kinds: []string{SearchKindKB, SearchKindTicket},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search.Query: %v", err)
	}
	q, _ := url.ParseQuery(raw)
	if len(q["kinds"]) != 2 || q["kinds"][0] != "kb" || q["kinds"][1] != "ticket" {
		t.Fatalf("kinds = %v, want two repeated plain keys in order", q["kinds"])
	}
	if strings.Contains(raw, "kinds%5B%5D") {
		t.Fatalf("sent kinds[]: %q — the API strips that key and searches everything", raw)
	}
	if q.Get("q") != "refund policy" {
		t.Fatalf("q = %q", q.Get("q"))
	}
}

// A tag or id with a slash or a space must not retarget the request.
func TestPathSegmentsAreEscaped(t *testing.T) {
	var path string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.EscapedPath is what travelled; r.URL.Path is decoded.
		path = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"id":"ct_1"}`))
	}))

	if _, err := c.Contacts.RemoveTag(context.Background(), "ct_1", "vip/eu tier"); err != nil {
		t.Fatalf("RemoveTag: %v", err)
	}
	if strings.Contains(path, "vip/eu") {
		t.Fatalf("unescaped slash reached the wire: %q", path)
	}
	if !strings.Contains(path, "vip%2Feu%20tier") {
		t.Fatalf("escaped path = %q, want the tag percent-encoded", path)
	}
}

// ── CONTRACT §8: the escape hatch ──────────────────────────────────────────
func TestRequestEscapeHatch(t *testing.T) {
	var seen struct {
		method, path, idem string
		body               []byte
	}
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.method, seen.path = r.Method, r.URL.Path
		seen.idem = r.Header.Get("Idempotency-Key")
		seen.body, _ = readAll(r)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	raw, err := c.Request(context.Background(), "post", "/v1/anything/at/all", url.Values{"x": {"1"}}, map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("raw = %s", raw)
	}
	if seen.method != "POST" {
		t.Fatalf("method = %q — a lowercase verb must be normalised", seen.method)
	}
	if seen.path != "/v1/anything/at/all" || string(seen.body) != `{"a":1}` {
		t.Fatalf("path=%q body=%s", seen.path, seen.body)
	}
	// The escape hatch is not a bypass: it goes through the same auth,
	// idempotency, retry and error handling as a typed method.
	if seen.idem == "" {
		t.Fatal("escape-hatch write sent no Idempotency-Key")
	}
	if _, err := c.Request(context.Background(), "GET", "v1/no/leading/slash", nil, nil); err == nil {
		t.Fatal("a path without a leading slash was accepted")
	}
}

func TestFetchDecodesIntoCallerTypes(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200, `{"items":[{"id":"ct_1","email":"a@b.test"}],"nextCursor":null}`))

	type contact struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	page, err := Fetch[Page[contact]](context.Background(), c, "GET", "/v1/contacts", nil, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Email != "a@b.test" {
		t.Fatalf("decoded %+v", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatal("nextCursor should decode to nil")
	}
}

// A 204 must not be decoded as JSON — DELETE /v1/macros/{id} answers one.
func TestNoContentIsNotADecodeError(t *testing.T) {
	var method string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	if err := c.Macros.Delete(context.Background(), "mc_1"); err != nil {
		t.Fatalf("Macros.Delete: %v", err)
	}
	if method != http.MethodDelete {
		t.Fatalf("method = %q", method)
	}
}

// Several routes answer a bare array rather than {items,nextCursor}, and
// decoding one into the other is a silent empty list.
func TestBareArrayRoutesDecodeAsSlices(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200, `[{"id":"q_1","name":"Tier 1"},{"id":"q_2","name":"Tier 2"}]`))
	queues, err := c.Tickets.Queues(context.Background())
	if err != nil {
		t.Fatalf("Tickets.Queues: %v", err)
	}
	if len(queues) != 2 || queues[1]["name"] != "Tier 2" {
		t.Fatalf("queues = %v", queues)
	}
}

// One route's bare array holds STRINGS, and it needs its own decoder.
//
// GET /v1/channels/adapters is `z.array(z.string())`. Built on the usual
// []Object helper it would fail on every real call — and the surface suite's
// shared `[{"id":"x_1"}]` fixture would have agreed with that mistake, which
// is why the fixture for it is overridden there. This is the positive half:
// the real body decodes, in order, with no envelope.
func TestAdaptersDecodeAsStrings(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200, `["api","email","web"]`))
	adapters, err := c.Channels.Adapters(context.Background())
	if err != nil {
		t.Fatalf("Channels.Adapters: %v", err)
	}
	if len(adapters) != 3 || adapters[0] != "api" || adapters[2] != "web" {
		t.Fatalf("adapters = %v", adapters)
	}

	// And the negative half: a body of objects is a decode error rather than
	// three empty strings, so a route that changed shape cannot pass as an
	// empty-ish success.
	objs, _ := newTestClient(t, jsonHandler(200, `[{"name":"web"}]`))
	if _, err := objs.Channels.Adapters(context.Background()); err == nil {
		t.Fatal("Channels.Adapters accepted an array of objects: the decode is lenient, so a " +
			"route that gained an envelope would hand callers blank strings instead of an error")
	}
}

// Client-side guards: refuse a call that cannot possibly succeed instead of
// spending a round trip to be told so.
func TestClientSideArgumentGuards(t *testing.T) {
	var calls atomic.Int64
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	ctx := context.Background()

	cases := map[string]error{
		"Contacts.Get":            first(c.Contacts.Get(ctx, "")),
		"Contacts.Merge":          first(c.Contacts.Merge(ctx, "a", "")),
		"Contacts.AddTags":        first(c.Contacts.AddTags(ctx, "a", "")),
		"Contacts.BulkUpsert":     first(c.Contacts.BulkUpsert(ctx, nil)),
		"Conversations.Send":      first(c.Conversations.Send(ctx, "", SendMessageInput{Body: "x"})),
		"Conversations.Handling":  first(c.Conversations.SetAgentHandling(ctx, "cv_1", SetAgentHandlingInput{})),
		"Conversations.Schedule":  first(c.Conversations.ScheduleMessage(ctx, "cv_1", ScheduleMessageInput{Body: "x"})),
		"Tickets.Bulk":            first(c.Tickets.Bulk(ctx, nil, TicketBulkStatus, TicketBulkPayload{})),
		"Tickets.BulkNoAction":    first(c.Tickets.Bulk(ctx, []string{"t_1"}, "", TicketBulkPayload{})),
		"Macros.CreateNoName":     first(c.Macros.Create(ctx, MacroInput{Scope: "workspace"})),
		"Macros.CreateTeamNoTeam": first(c.Macros.Create(ctx, MacroInput{Name: "x", Scope: "team"})),
		"KB.Article":              first(c.KB.Article(ctx, "")),
		"Search.Query":            first(c.Search.Query(ctx, "", nil)),
		"Events.TrackNoContact":   first(c.Events.Track(ctx, TrackEventInput{Name: "x"})),
		"Events.TrackNoName":      first(c.Events.Track(ctx, TrackEventInput{ContactID: "ct_1"})),
		"Billing.Usage":           first(c.Billing.Usage(ctx, "", nil)),
		"Webhooks.Redeliver":      first(c.Webhooks.Redeliver(ctx, "")),

		// The surface added on 2026-10-14. Each of these refuses something
		// the API refuses too, so the guard is a faster 400 rather than a
		// second opinion about the schema.
		"AI.AddProviderNoKey":   first(c.AI.AddProvider(ctx, AIProviderOpenAI, "")),
		"AI.TestProviderNoName": first(c.AI.TestProvider(ctx, "")),
		"AI.SetToneBadRule":     first(c.AI.SetTone(ctx, []ToneRuleInput{{Category: ToneCategoryWarmth}})),
		"AI.GenerateTone":       first(c.AI.GenerateTone(ctx, "")),
		"AI.CreateToolNoURL":    first(c.AI.CreateTool(ctx, AIToolInput{Name: "x", Description: "y"})),
		// A Chatly bot needs a prompt and an external one needs a URL, and
		// the union is tried external-arm-first, so sending the wrong arm's
		// fields is a 200 that changed nothing rather than a refusal.
		"Bots.CreateNoPrompt":      first(c.Bots.Create(ctx, BotInput{Name: "x"})),
		"Bots.CreateExternalNoURL": first(c.Bots.Create(ctx, BotInput{Type: BotTypeExternal, Name: "x"})),
		"Bots.DeliveryNoDelivery":  first(c.Bots.Delivery(ctx, "bt_1", "")),
		"BotRoutes.CreateNoBot":    first(c.BotRoutes.Create(ctx, BotRouteInput{Name: "x"})),
		"Brands.CreateNoSlug":      first(c.Brands.Create(ctx, BrandInput{Name: "x"})),
		"CanvasSteps.ResolveEmpty": first(c.CanvasSteps.Resolve(ctx, "cs_1", nil)),
		"Channels.CreateNoPublic":  first(c.Channels.Create(ctx, ChannelInput{Type: ChannelTypeWeb, Name: "x"})),
		"CustomerCards.NoTTL": first(c.CustomerCards.Create(ctx, CustomerCardInput{
			Key: "k", Title: "t", URL: "https://x.test",
		})),
		"CardEndpoints.CreateNoURL": first(c.CustomerCardEndpoints.Create(ctx, "")),
		"DocsSources.CreateNoSpace": first(c.DocsSources.Create(ctx, DocsSourceInput{
			Provider: DocsProviderGitBook, OrganizationID: "org_1",
		})),
		"EmailTemplates.UpdateEmptyBody": first(c.EmailTemplates.Update(ctx, EmailTemplateWorkspaceInvite, "s", "")),
		"EmailTemplates.PreviewNoKind":   first(c.EmailTemplates.Preview(ctx, "", "s", "b")),
		"KnowledgeGaps.UpdateNoStatus":   first(c.KnowledgeGaps.Update(ctx, "kg_1", "")),
		// `enabled` and at least one action are REQUIRED KEYS on the trigger
		// upsert — there is no server-side default for either.
		"Triggers.CreateNoEnabled": first(c.Triggers.Create(ctx, TriggerInput{
			Name: "x", EventType: "conversation.created",
			Actions: []TriggerAction{{Type: "send_message"}},
		})),
		"Triggers.CreateNoActions": first(c.Triggers.Create(ctx, TriggerInput{
			Name: "x", EventType: "conversation.created", Enabled: Bool(true),
		})),
		"Webhooks.CreateNoEvents":   first(c.Webhooks.Create(ctx, WebhookEndpointInput{URL: "https://x.test"})),
		"Webhooks.SetSchemaNoValue": first(c.Webhooks.SetSchemaVersion(ctx, "wh_1", "")),
		// An empty id on a DELETE would address the collection, not a row —
		// which on this route would be a 404 rather than a mass deletion, but
		// refusing locally is still the right answer.
		"Webhooks.DeleteNoID": first(c.Webhooks.Delete(ctx, "")),
		"Conversations.OutcomeNoRef": first(c.Conversations.RecordOutcome(ctx, "cv_1", ConversationOutcomeInput{
			Outcome: OutcomePurchased,
		})),
		"Conversations.BlocksEmpty":  first(c.Conversations.UpdateBlocks(ctx, "cv_1", "ms_1", nil)),
		"Conversations.ReturnNoBot":  first(c.Conversations.ReturnToBot(ctx, "cv_1", "")),
		"Billing.UsageDailyNoEvent":  first(c.Billing.UsageDaily(ctx, "", nil)),
		"Reports.ThemesBadDate":      first(c.Reports.Themes(ctx, &ThemeOptions{Date: "yesterday"})),
		"Reports.ThemeConversations": first(c.Reports.ThemeConversations(ctx, "")),
	}
	for name, err := range cases {
		if err == nil {
			t.Errorf("%s: accepted an argument the API requires", name)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d request(s) reached the server for calls that should have failed locally", n)
	}
}

// ScheduleMessage must put an absolute RFC3339 UTC instant on the wire.
func TestScheduleMessageSerialisesSendAt(t *testing.T) {
	var body []byte
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = readAll(r)
		_, _ = w.Write([]byte(`{"id":"sm_1"}`))
	}))

	when := time.Date(2026, 10, 12, 9, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	if _, err := c.Conversations.ScheduleMessage(context.Background(), "cv_1", ScheduleMessageInput{
		Body:   "morning",
		SendAt: when,
	}); err != nil {
		t.Fatalf("ScheduleMessage: %v", err)
	}
	var sent struct {
		Body   string `json:"body"`
		SendAt string `json:"sendAt"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	if sent.SendAt != "2026-10-12T07:30:00Z" {
		t.Fatalf("sendAt = %q, want the UTC instant 2026-10-12T07:30:00Z", sent.SendAt)
	}
}

func first[T any](_ T, err error) error { return err }

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}
