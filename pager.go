package chatly

import (
	"context"
	"errors"
	"iter"
	"net/url"
)

// Page is one page of a list endpoint, exactly as the API returns it.
//
// NextCursor is nil on the last page. It is a *string and not a string
// because the distinction is load-bearing: `null` means "no more pages",
// whereas an empty string would be a cursor value the API never sends but
// which a non-pointer field could not tell apart from absent.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// ErrCursorLoop is returned by a Pager when the API hands back a cursor it has
// already been asked for.
//
// That is a server-side bug, but the client is where it becomes an infinite
// loop that quietly bills the caller for a million requests, so the pager
// stops and says so rather than either spinning or silently truncating. If
// you hit this, the page you were on is the last one you can trust.
var ErrCursorLoop = errors.New("chatly: server repeated a pagination cursor")

// pageFetcher fetches one page given a cursor ("" for the first page).
type pageFetcher[T any] func(ctx context.Context, cursor string) (*Page[T], error)

// Pager walks every page of a list endpoint, one item at a time.
//
// It is the explicit-iterator form rather than a range-over-func, and both
// are offered — see Seq. This one is primary for three reasons: it takes a
// context per step, so a long walk honours a deadline that arrives mid-walk;
// its error is retrievable after the loop rather than yielded as a second
// range value a caller can ignore; and it compiles for anyone on Go 1.18+
// even though this module's floor is 1.23. Seq exists because
// `for item, err := range pager.Seq(ctx)` is what a 2024-and-later Go
// programmer reaches for first, and it is a 20-line adapter over this.
//
//	pager := client.Contacts.ListAll(nil)
//	for pager.Next(ctx) {
//	    fmt.Println(pager.Value()["id"])
//	}
//	if err := pager.Err(); err != nil { return err }
//
// A Pager is single-use and is NOT safe for concurrent use.
type Pager[T any] struct {
	fetch pageFetcher[T]

	buf  []T
	idx  int
	cur  T
	zero T

	cursor  string
	started bool
	done    bool
	err     error

	// requested is every cursor already sent to the server, including "" for
	// the first page. The loop guard.
	requested map[string]struct{}

	// pages and items are counters, exposed for progress logging on a long
	// walk and for tests that assert how many requests a walk made.
	pages int
	items int
}

func newPager[T any](fetch pageFetcher[T]) *Pager[T] {
	return &Pager[T]{fetch: fetch, requested: make(map[string]struct{})}
}

// Next advances to the next item, fetching the next page when the current one
// is exhausted. It returns false when the stream ends OR when it fails; call
// Err afterwards to tell those two apart.
func (p *Pager[T]) Next(ctx context.Context) bool {
	if p.err != nil || p.done {
		return false
	}

	for p.idx >= len(p.buf) {
		if p.started && p.cursor == "" {
			// NextCursor was null: that is the end of the stream, and the
			// only clean way out of this loop.
			p.done = true
			return false
		}
		if _, seen := p.requested[p.cursor]; seen {
			p.err = ErrCursorLoop
			return false
		}
		p.requested[p.cursor] = struct{}{}

		page, err := p.fetch(ctx, p.cursor)
		if err != nil {
			p.err = err
			return false
		}
		p.started = true
		p.pages++
		p.buf = page.Items
		p.idx = 0
		if page.NextCursor == nil {
			p.cursor = ""
		} else {
			p.cursor = *page.NextCursor
		}
		// An empty page with a non-nil cursor is legal (a filtered page can
		// be empty) and the loop will fetch again — the `requested` set is
		// what stops that being unbounded.
	}

	p.cur = p.buf[p.idx]
	p.idx++
	p.items++
	return true
}

// Value is the item the last successful Next landed on. Calling it before the
// first Next, or after Next returned false, yields the zero value.
func (p *Pager[T]) Value() T {
	if !p.started || p.done || p.err != nil {
		if p.idx == 0 {
			return p.zero
		}
	}
	return p.cur
}

// Err is the error that ended the walk, or nil if the stream simply ran out.
// ALWAYS check it: a `for pager.Next(ctx)` loop that ends because the API
// returned 401 looks exactly like one that ends because there were no more
// contacts.
func (p *Pager[T]) Err() error { return p.err }

// Pages is how many HTTP requests the walk has made so far.
func (p *Pager[T]) Pages() int { return p.pages }

// Count is how many items the walk has yielded so far.
func (p *Pager[T]) Count() int { return p.items }

// Seq adapts the pager to a range-over-func loop (Go 1.23+):
//
//	for contact, err := range client.Contacts.ListAll(nil).Seq(ctx) {
//	    if err != nil { return err }
//	    fmt.Println(contact["id"])
//	}
//
// The error is yielded as the second value and the sequence stops
// immediately after. Note the trap this form carries and Pager.Next does
// not: a loop body that ignores `err` turns a failed walk into a short one.
// If that worries you, use Next/Err.
func (p *Pager[T]) Seq(ctx context.Context) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for p.Next(ctx) {
			if !yield(p.Value(), nil) {
				return
			}
		}
		if err := p.Err(); err != nil {
			yield(p.zero, err)
		}
	}
}

// Collect walks the whole stream into a slice.
//
// Convenient, and a foot-gun on a large workspace: a million contacts is a
// million maps in memory and a million/limit HTTP requests before this
// returns anything. Prefer Next for anything unbounded. Pass a limit > 0 to
// stop early; the slice returned alongside an error holds whatever was
// collected before the failure.
func (p *Pager[T]) Collect(ctx context.Context, limit int) ([]T, error) {
	var out []T
	for p.Next(ctx) {
		out = append(out, p.Value())
		if limit > 0 && len(out) >= limit {
			return out, nil
		}
	}
	return out, p.Err()
}

// listPager builds a Pager over a GET endpoint that answers {items,nextCursor}.
//
// Every auto-paginating method in this package goes through here, so the
// cursor handling, the loop guard and the per-page query construction exist
// in exactly one place.
func listPager[T any](c *Client, path string, base url.Values) *Pager[T] {
	return newPager(func(ctx context.Context, cursor string) (*Page[T], error) {
		q := cloneValues(base)
		if cursor != "" {
			// Set, not Add: the caller's starting cursor must not survive
			// alongside the server's next one.
			q.Set("cursor", cursor)
		}
		var page Page[T]
		if err := c.getInto(ctx, path, q, &page); err != nil {
			return nil, err
		}
		return &page, nil
	})
}

func cloneValues(in url.Values) url.Values {
	out := make(url.Values, len(in)+1)
	for k, vs := range in {
		out[k] = append([]string(nil), vs...)
	}
	return out
}
