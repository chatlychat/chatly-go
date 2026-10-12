package chatly_test

// These are the README's snippets, compiled.
//
// A README is prose to every other tool we run, and this repo has shipped a
// README documenting an API that did not exist — a customer found it. Keeping
// the published examples in a file the compiler reads means a renamed field
// or a changed signature breaks the build instead of breaking a reader.
//
// They are compile-only (no "Output:" comment), because running them would
// need a live API — which the test suite is not allowed to require.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	chatly "github.com/chatlychat/chatly-go"
)

func ExampleNew() {
	client, err := chatly.New(os.Getenv("CHATLY_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	me, err := client.Whoami(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("workspace:", me.WorkspaceID, "scopes:", me.Scopes)

	contact, err := client.Contacts.Upsert(ctx, chatly.ContactInput{
		ExternalID: "user_42",
		Email:      "ada@example.com",
		Name:       "Ada Lovelace",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("contact:", contact["id"])
}

func ExampleNew_options() {
	client, err := chatly.New(os.Getenv("CHATLY_API_KEY"),
		chatly.WithBaseURL("https://chatly.internal.example"),
		chatly.WithHTTPClient(&http.Client{}),
		chatly.WithTimeout(30*time.Second),
		chatly.WithMaxAttempts(5),
		chatly.WithUserAgentSuffix("acme-billing/2.1"),
		chatly.WithHeader("X-Trace", "abc"),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(client.BaseURL())
}

func ExampleAPIError() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	page, err := client.Contacts.List(ctx, nil)
	switch {
	case err == nil:
		fmt.Println(len(page.Items), "contacts")
	case errors.Is(err, chatly.ErrUnauthorized):
		log.Println("the key is wrong, revoked or expired")
	case errors.Is(err, chatly.ErrForbidden):
		log.Println("the key lacks the scope this route needs")
	case errors.Is(err, chatly.ErrRateLimited):
		log.Println("already retried; back off further")
	case errors.Is(err, chatly.ErrTransport):
		log.Println("nothing was served: DNS, connect, TLS or timeout")
	default:
		log.Println(err)
	}

	var apiErr *chatly.APIError
	if errors.As(err, &apiErr) {
		log.Printf("chatly %d %s: %s (request %q)\n%s",
			apiErr.Status, apiErr.Code, apiErr.Message, apiErr.RequestID, apiErr.Body)
	}
}

func ExampleClient_WithIdempotencyKey() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	key, err := chatly.NewIdempotencyKey() // store this with your job
	if err != nil {
		log.Fatal(err)
	}
	pinned := client.WithIdempotencyKey(key)
	contact, err := pinned.Contacts.Upsert(ctx, chatly.ContactInput{ExternalID: "user_42"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(contact["id"])
}

func ExamplePage() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	page, err := client.Contacts.List(ctx, &chatly.ContactListOptions{Limit: 50})
	if err != nil {
		log.Fatal(err)
	}
	for _, contact := range page.Items {
		fmt.Println(contact["id"])
	}
	if page.NextCursor != nil {
		fmt.Println("next page starts at", *page.NextCursor)
	}
}

func ExamplePager() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	pager := client.Contacts.ListAll(nil)
	for pager.Next(ctx) {
		contact := pager.Value()
		fmt.Println(contact["id"])
	}
	if err := pager.Err(); err != nil { // ALWAYS check this
		log.Fatal(err)
	}
}

func ExamplePager_Seq() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	for contact, err := range client.Contacts.ListAll(nil).Seq(ctx) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(contact["id"])
	}
}

func ExampleVerifyWebhook() {
	secret := os.Getenv("CHATLY_WEBHOOK_SECRET")

	http.HandleFunc("/webhooks/chatly", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}

		if !chatly.VerifyWebhook(raw, r.Header.Get(chatly.WebhookSignatureHeader), secret) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}

		var event struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
		log.Println("received", event.Event, len(event.Data), "bytes of data")
	})
}

func ExampleVerifyWebhookSignature() {
	raw := []byte(`{"id":"evt_1"}`)
	header := "t=1700000000,v1=c89214b5b5da833daed6f0b8c5bb6bd58cea9022bd80ccc78230f3942d632925"

	ok := chatly.VerifyWebhookSignature(chatly.WebhookVerification{
		Body:            raw,
		SignatureHeader: header,
		Secret:          "whsec_test",
		Tolerance:       chatly.DefaultWebhookTolerance,
		Now:             time.Unix(1700000000, 0),
	})
	fmt.Println(ok)
	// Output: true
}

func ExampleIdentityHash() {
	userID := "user_42"
	userHash, err := chatly.IdentityHash(os.Getenv("CHATLY_IDV_SECRET"), userID)
	if err != nil {
		log.Fatal(err)
	}
	// Render into the page:
	//   Chatly('identify', { externalId: ..., userHash: ..., email: ... })
	fmt.Println(len(userHash))
}

func ExampleClient_Request() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()
	ticketID := "tk_1"

	// Key-accessible, and no typed method in this package:
	raw, err := client.Request(ctx, "GET", "/v1/tickets/"+ticketID+"/status-history", nil, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(raw), "bytes of JSON")

	// Explicit nulls, which the typed input structs cannot express.
	_, err = client.Request(ctx, "POST", "/v1/tickets/bulk", nil, map[string]any{
		"ids":     []string{ticketID},
		"action":  "assign",
		"payload": map[string]any{"assigneeUserId": nil},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleFetch() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	type contact struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	page, err := chatly.Fetch[chatly.Page[contact]](ctx, client, "GET", "/v1/contacts", nil, nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range page.Items {
		fmt.Println(c.ID, c.Email)
	}
}

func ExampleConversationsService_Send() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()
	conversationID, orderID := "cv_1", "ord_7"

	_, err := client.Conversations.Send(ctx, conversationID, chatly.SendMessageInput{
		Body:      "Your refund is on its way.",
		ClientRef: "refund-" + orderID,
	})
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleTicketsService_List() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	page, err := client.Tickets.List(ctx, &chatly.ListOptions{
		Limit:  50,
		Sort:   "-updatedAt",
		Filter: []string{"status:in:open,pending", "priority:urgent"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(page.Items))
}

func ExampleSearchService_Query() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	res, err := client.Search.Query(ctx, "refund policy", &chatly.SearchOptions{
		Kinds: []string{chatly.SearchKindKB, chatly.SearchKindTicket},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res["tier"], res["facets"])
}

func ExampleEventsService_Track() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()
	contactID := "ct_1"

	_, err := client.Events.Track(ctx, chatly.TrackEventInput{
		ContactID:  contactID,
		Name:       "order_placed",
		Properties: map[string]any{"total_cents": 4999, "currency": "EUR"},
	})
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleWorkspaceService_Labels() {
	client, _ := chatly.New("ck_example")
	ctx := context.Background()

	labels, err := client.Workspace.Labels(ctx, &chatly.LabelOptions{WithUsage: true})
	if err != nil {
		log.Fatal(err)
	}
	for _, l := range labels {
		fmt.Println(l["name"], l["conversations"])
	}
}

func ExampleSignWebhookPayload() {
	header := chatly.SignWebhookPayload([]byte(`{"id":"evt_1"}`), "whsec_test", time.Unix(1700000000, 0))
	fmt.Println(header)
	// Output: t=1700000000,v1=c89214b5b5da833daed6f0b8c5bb6bd58cea9022bd80ccc78230f3942d632925
}
