package chatly

import (
	"context"
	"errors"
)

// KBService is the /v1/kb surface — help-centre articles and collections.
// Reach it as client.KB.
type KBService struct{ c *Client }

// Articles returns one page of knowledge-base articles.
//
// GET /v1/kb/articles — scope kb:read. Speaks the generic list grammar:
//
//	opts := &chatly.ListOptions{
//	    Filter: []string{"status:published", "locale:en"},
//	    Sort:   "-updatedAt",
//	}
//
// Sortable: id, title, status, publishedAt, createdAt, updatedAt, position.
// Filterable: collectionId, status (draft|published|archived), locale,
// authorId, createdAt, updatedAt, sourceId, sourceStale.
//
// Mind the status filter. Without it you get drafts and archived articles
// alongside published ones, so anything rendering these to the public needs
// `status:published` — a draft is editable prose nobody has signed off.
func (s *KBService) Articles(ctx context.Context, opts *ListOptions) (*Page[Object], error) {
	return s.c.getPage(ctx, "/v1/kb/articles", opts.values())
}

// ArticlesAll walks every page of articles.
func (s *KBService) ArticlesAll(opts *ListOptions) *Pager[Object] {
	return listPager[Object](s.c, "/v1/kb/articles", opts.values())
}

// Article returns one article BY SLUG, not by id.
//
// GET /v1/kb/articles/{slug} — scope kb:read. The slug is the URL segment
// ("how-to-reset-your-password"); passing a UUID here is a 404.
//
// The response carries both `body` (markdown) and `bodyHtml` (rendered), plus
// the draft fields (draftTitle, draftBody, …) when an unpublished edit
// exists. Render `body`/`bodyHtml`, not the draft, unless you are building a
// preview.
func (s *KBService) Article(ctx context.Context, slug string) (Object, error) {
	if slug == "" {
		return nil, errors.New("chatly: KB.Article: empty slug")
	}
	return s.c.getObject(ctx, "/v1/kb/articles/"+pathEscape(slug), nil)
}

// Collections lists the help centre's collections.
//
// GET /v1/kb/collections — scope kb:read. A BARE ARRAY, pagination-exempt:
// { id, workspaceId, createdAt, updatedAt, name, slug, description, iconKey,
// order, parentId, docsSourceId }. parentId makes it a tree; docsSourceId is
// non-null on a collection synced from an external docs source.
func (s *KBService) Collections(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/kb/collections", nil)
}
