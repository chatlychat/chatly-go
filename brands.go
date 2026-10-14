package chatly

import (
	"context"
	"errors"
)

// BrandsService is /v1/brands — the optional sub-tenants inside a workspace
// that let one account serve several products under different names, logos
// and reply domains. Reach it as client.Brands.
//
// Reads want workspace:read, writes workspace:write.
type BrandsService struct{ c *Client }

// List returns every brand, with usage counts.
//
// GET /v1/brands — scope workspace:read. A bare array, and the handler reads
// no query parameters at all, so a cursor or filter is ignored rather than
// refused. Soft-deleted brands are excluded.
//
// A LIST ROW IS NOT THE SAME SHAPE as the single brand the writes answer
// with: it carries an extra `usage` object (`channels`, `conversations`,
// `triggers`, `workflows`) that create, update and delete do not.
//
// Several fields that look like columns are not: logoUrl, primaryColor,
// supportEmail, replyDomain and icons are flattened out of a stored
// `branding` blob, and `branding` itself never appears on the wire.
func (s *BrandsService) List(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/brands", nil)
}

// BrandInput is the body of Create and Update.
//
// Nothing here can CLEAR a field: no field is nullable, so a null is a 400
// and an omitted one means "leave alone". Icons merge per icon key, which
// means an individual icon cannot be removed either.
type BrandInput struct {
	// Name, 1..120 chars, required on create.
	Name string `json:"name,omitempty"`

	// Slug, 1..64 chars of ^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$, required
	// on create and unique per workspace.
	//
	// The unique index is NOT partial on deleted_at, so reusing a
	// soft-deleted brand's slug raises an unmapped constraint violation and
	// comes back as a 500 rather than a 409. Pick a fresh slug.
	Slug string `json:"slug,omitempty"`

	// Domain is the help-centre hostname, max 253 chars.
	Domain string `json:"domain,omitempty"`

	// ReplyDomain is the OUTBOUND MAIL hostname — a different thing from
	// Domain, and setting it enables nothing until the domain is verified
	// through /v1/domains. Stored trimmed and lower-cased.
	ReplyDomain string `json:"replyDomain,omitempty"`

	LogoURL string `json:"logoUrl,omitempty"`

	// PrimaryColor must match ^#[0-9a-fA-F]{6}$ — a three-digit hex or a
	// named colour is a 400.
	PrimaryColor string `json:"primaryColor,omitempty"`

	SupportEmail string `json:"supportEmail,omitempty"`

	// Icons maps a widget icon name to its value. Refused if the value
	// contains script or event-handler markup.
	Icons map[string]string `json:"icons,omitempty"`

	// IsDefault true DEMOTES EVERY OTHER BRAND in the same transaction. It
	// is a pointer only for symmetry with the other optional flags; false
	// and absent behave the same, because there is no way to un-default a
	// brand except by promoting another.
	IsDefault *bool `json:"isDefault,omitempty"`
}

// Create adds a brand and returns it.
//
// POST /v1/brands — scope workspace:write, answers 201.
func (s *BrandsService) Create(ctx context.Context, in BrandInput) (Object, error) {
	if in.Name == "" || in.Slug == "" {
		return nil, errors.New("chatly: Brands.Create: name and slug are required")
	}
	return s.c.writeObject(ctx, "POST", "/v1/brands", nil, in)
}

// Update patches a brand and returns it.
//
// PATCH /v1/brands/{id} — scope workspace:write. An empty body is accepted
// (it rewrites the row and answers with it), unlike the channel patch next
// door, which answers 409.
//
// It does not filter deleted_at, so a soft-deleted brand is still patchable.
func (s *BrandsService) Update(ctx context.Context, brandID string, in BrandInput) (Object, error) {
	if brandID == "" {
		return nil, errors.New("chatly: Brands.Update: empty brand id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/brands/"+pathEscape(brandID), nil, in)
}

// Delete soft-deletes a brand.
//
// DELETE /v1/brands/{id} — scope workspace:write. Answers 200 with `{ id }`,
// NOT a 204: the id it retired is the caller's only confirmation, and an SDK
// written against an empty-body assumption throws it away.
//
// Two refusals, both 409 rather than 400: the brand is the workspace default
// ("make another brand the default first"), or a channel still points at it —
// the message carries the count. The row itself survives with deleted_at set,
// so conversations that reference it keep rendering.
func (s *BrandsService) Delete(ctx context.Context, brandID string) (Object, error) {
	if brandID == "" {
		return nil, errors.New("chatly: Brands.Delete: empty brand id")
	}
	return s.c.writeObject(ctx, "DELETE", "/v1/brands/"+pathEscape(brandID), nil, nil)
}
