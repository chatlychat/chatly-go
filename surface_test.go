package chatly

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
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

// manifestLocation finds the cross-language SDK surface contract, read rather than
// duplicated. The test asserts against the spec itself, so a spec change
// shows up here as a failure instead of as two files that disagree.
// The manifest, in the monorepo or in a public mirror.
//
// `../sdk-spec/surface.json` in the monorepo; a mirror has no `packages/`
// above it, so `scripts/sync-sdk-repos.mjs` vendors it at the mirror root.
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
}

// lockEntry is one row of surface.lock.json. Field order here IS the key
// order in the emitted JSON.
type lockEntry struct {
	NS   string `json:"ns"`
	Name string `json:"name"`
	HTTP string `json:"http"`
	Path string `json:"path"`
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
// The cross-language parity gate used to match route strings in SDK SOURCE
// TEXT. That counts a path in a comment, and it did: a boxed doc comment in
// this very file's package naming /v1/agents/skills made the gate report a
// method that called no such route. Three SDKs passed it the same way.
//
// A path in a comment is not evidence. An HTTP request arriving at a server
// is. This test already makes all 55 of those requests, so it is the right
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

// The auto-paginating variants must target the same route as their single-page
// twin. They share one helper, but a wrong path argument would not be caught
// anywhere else.
func TestPagerVariantsTargetTheSameRoutes(t *testing.T) {
	cases := map[string]func(*Client) *Pager[Object]{
		"/v1/contacts":                    func(c *Client) *Pager[Object] { return c.Contacts.ListAll(nil) },
		"/v1/contacts/ct_1/journey":       func(c *Client) *Pager[Object] { return c.Contacts.JourneyAll("ct_1", nil) },
		"/v1/conversations":               func(c *Client) *Pager[Object] { return c.Conversations.ListAll(nil) },
		"/v1/conversations/cv_1/messages": func(c *Client) *Pager[Object] { return c.Conversations.MessagesAll("cv_1", nil) },
		"/v1/tickets":                     func(c *Client) *Pager[Object] { return c.Tickets.ListAll(nil) },
		"/v1/kb/articles":                 func(c *Client) *Pager[Object] { return c.KB.ArticlesAll(nil) },
		"/v1/cdp/events":                  func(c *Client) *Pager[Object] { return c.Events.ListAll(nil) },
		"/v1/webhooks/deliveries":         func(c *Client) *Pager[Object] { return c.Webhooks.DeliveriesAll(nil) },
		"/v1/search":                      func(c *Client) *Pager[Object] { return c.Search.QueryAll("q", nil) },
	}

	for wantPath, build := range cases {
		wantPath, build := wantPath, build
		t.Run(wantPath, func(t *testing.T) {
			var gotPath string
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{"items":[{"id":"x"}],"nextCursor":null}`))
			}))
			p := build(c)
			if !p.Next(context.Background()) {
				t.Fatalf("pager yielded nothing: %v", p.Err())
			}
			if gotPath != wantPath {
				t.Fatalf("path = %s, want %s", gotPath, wantPath)
			}
		})
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
