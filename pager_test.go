package chatly

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// ── CONTRACT §10.6 ─────────────────────────────────────────────────────────
// "The auto-paginator walks two pages and stops on a null cursor."
func TestPagerWalksTwoPagesAndStops(t *testing.T) {
	var cursors []string
	var calls atomic.Int64

	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		cursors = append(cursors, cursor)
		calls.Add(1)
		switch cursor {
		case "":
			_, _ = w.Write([]byte(`{"items":[{"id":"ct_1"},{"id":"ct_2"}],"nextCursor":"cur_2"}`))
		case "cur_2":
			_, _ = w.Write([]byte(`{"items":[{"id":"ct_3"}],"nextCursor":null}`))
		default:
			t.Errorf("unexpected cursor %q", cursor)
			w.WriteHeader(500)
		}
	}))

	pager := c.Contacts.ListAll(&ContactListOptions{Limit: 2})
	var ids []string
	for pager.Next(context.Background()) {
		ids = append(ids, pager.Value()["id"].(string))
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}

	if fmt.Sprint(ids) != "[ct_1 ct_2 ct_3]" {
		t.Fatalf("ids = %v, want all three across both pages in order", ids)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d requests, want exactly 2 — a null cursor must end the walk, not trigger a third", n)
	}
	if pager.Pages() != 2 || pager.Count() != 3 {
		t.Fatalf("Pages=%d Count=%d", pager.Pages(), pager.Count())
	}
	if cursors[0] != "" || cursors[1] != "cur_2" {
		t.Fatalf("cursors = %v", cursors)
	}

	// Exhausted and idempotent: calling Next again must not re-fetch.
	if pager.Next(context.Background()) {
		t.Fatal("Next returned true after the stream ended")
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("a post-exhaustion Next made a %dth request", n)
	}
}

// The non-cursor query parameters must survive onto page two, and the
// caller's starting cursor must not linger beside the server's.
func TestPagerKeepsFiltersAndReplacesTheCursor(t *testing.T) {
	var queries []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("cursor") == "cur_2" {
			_, _ = w.Write([]byte(`{"items":[],"nextCursor":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"t_1"}],"nextCursor":"cur_2"}`))
	}))

	pager := c.Tickets.ListAll(&ListOptions{
		Cursor: "cur_START",
		Limit:  10,
		Filter: []string{"status:open"},
	})
	for pager.Next(context.Background()) {
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("queries = %v", queries)
	}
	for i, q := range queries {
		if !strings.Contains(q, "filter=status%3Aopen") || !strings.Contains(q, "limit=10") {
			t.Fatalf("page %d dropped the caller's parameters: %q", i+1, q)
		}
	}
	if strings.Contains(queries[1], "cur_START") {
		t.Fatalf("page 2 still carries the starting cursor: %q", queries[1])
	}
	if !strings.Contains(queries[1], "cursor=cur_2") {
		t.Fatalf("page 2 did not use the server's cursor: %q", queries[1])
	}
}

// "It must not spin forever when the server repeats a cursor."
//
// Without the guard this handler is an infinite loop that bills the caller
// for every request in it, so the test is a liveness property, not a nicety.
func TestPagerStopsOnARepeatedCursor(t *testing.T) {
	var calls atomic.Int64
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) > 50 {
			t.Error("pager made more than 50 requests: the loop guard did not fire")
			w.WriteHeader(500)
			return
		}
		// Always the SAME cursor back, forever.
		_, _ = w.Write([]byte(`{"items":[{"id":"ct_x"}],"nextCursor":"stuck"}`))
	}))

	pager := c.Contacts.ListAll(nil)
	seen := 0
	for pager.Next(context.Background()) {
		seen++
	}
	if !errors.Is(pager.Err(), ErrCursorLoop) {
		t.Fatalf("Err = %v, want ErrCursorLoop", pager.Err())
	}
	// Page one (cursor "") and page two (cursor "stuck") are legitimate; the
	// third request would repeat "stuck" and is what gets refused.
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d requests, want 2 before the guard fired", n)
	}
	if seen != 2 {
		t.Fatalf("yielded %d items, want the 2 real ones before stopping", seen)
	}
}

// An empty page with a live cursor is legal — a filtered page can be empty —
// and must not end the walk early.
func TestPagerSkipsEmptyPages(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"items":[],"nextCursor":"p2"}`))
		case "p2":
			_, _ = w.Write([]byte(`{"items":[],"nextCursor":"p3"}`))
		default:
			_, _ = w.Write([]byte(`{"items":[{"id":"ct_9"}],"nextCursor":null}`))
		}
	}))

	got, err := c.Contacts.ListAll(nil).Collect(context.Background(), 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 || got[0]["id"] != "ct_9" {
		t.Fatalf("got %v, want the single item on page three", got)
	}
}

// A failure mid-walk must surface through Err, not look like the end of the
// stream. This is the trap the Pager docs warn about.
func TestPagerSurfacesAnErrorMidWalk(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"items":[{"id":"ct_1"}],"nextCursor":"cur_2"}`))
			return
		}
		w.WriteHeader(403)
		_, _ = w.Write([]byte(apiError("forbidden", "scope lost")))
	}), WithMaxAttempts(1))

	pager := c.Contacts.ListAll(nil)
	n := 0
	for pager.Next(context.Background()) {
		n++
	}
	if n != 1 {
		t.Fatalf("yielded %d items", n)
	}
	if !errors.Is(pager.Err(), ErrForbidden) {
		t.Fatalf("Err = %v, want ErrForbidden — a failed walk must not look like a short one", pager.Err())
	}
}

// The range-over-func form must yield the same items and surface the error as
// the second value.
func TestPagerSeq(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"items":[{"id":"e_1"},{"id":"e_2"}],"nextCursor":"c2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"e_3"}],"nextCursor":null}`))
	}))

	var ids []string
	for item, err := range c.Events.ListAll(nil).Seq(context.Background()) {
		if err != nil {
			t.Fatalf("Seq yielded an error: %v", err)
		}
		ids = append(ids, item["id"].(string))
	}
	if fmt.Sprint(ids) != "[e_1 e_2 e_3]" {
		t.Fatalf("ids = %v", ids)
	}

	// Breaking out early must stop fetching.
	var calls atomic.Int64
	c2, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"items":[{"id":"e_1"}],"nextCursor":"next"}`))
	}))
	for range c2.Events.ListAll(nil).Seq(context.Background()) {
		break
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d requests after an immediate break, want 1", n)
	}
}

func TestSeqYieldsTheError(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(404, apiError("not_found", "gone")), WithMaxAttempts(1))
	var gotErr error
	for _, err := range c.Contacts.ListAll(nil).Seq(context.Background()) {
		gotErr = err
	}
	if !errors.Is(gotErr, ErrNotFound) {
		t.Fatalf("Seq error = %v", gotErr)
	}
}

func TestCollectRespectsALimit(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":"a"},{"id":"b"},{"id":"c"}],"nextCursor":"more"}`))
	}))
	got, err := c.Contacts.ListAll(nil).Collect(context.Background(), 2)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want the 2 asked for", len(got))
	}
}

// Page.NextCursor must distinguish absent from empty: an explicit null is the
// end, and the pointer is what carries that.
func TestNextCursorNullVersusValue(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200, `{"items":[],"nextCursor":"cur_7"}`))
	page, err := c.Contacts.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.NextCursor == nil || *page.NextCursor != "cur_7" {
		t.Fatalf("NextCursor = %v", page.NextCursor)
	}
}
