package chatly

import (
	"context"
	"errors"
)

// ChannelsService is the /v1/channels surface. Reach it as client.Channels.
// Read-only: both routes need channels:read, and no key-accessible route
// writes a channel.
type ChannelsService struct{ c *Client }

// List returns every channel in the workspace.
//
// GET /v1/channels — a BARE ARRAY, no pagination, no query parameters. Each
// item is { id, workspaceId, brandId, type, name, publicId, enabled, config,
// autoCreateTickets, autoAcknowledge, acknowledgeTemplate,
// acknowledgeSubject, createdAt, updatedAt, originsRestricted, embed }.
//
// Three fields earn a note:
//
//   - `publicId` is the id the BROWSER widget uses. It is safe to render into
//     a page; your API key is not. This is the call that gets it.
//   - credential values inside `config` read "__redacted__". The API will not
//     hand a stored access token back, so you cannot use this to re-read a
//     secret you have lost.
//   - `originsRestricted` is a boolean that answers a question the config
//     cannot: an empty origin allow-list means "any origin", which is
//     indistinguishable from "not configured" in the raw config. False here
//     means the widget will load on any site.
func (s *ChannelsService) List(ctx context.Context) ([]Object, error) {
	return s.c.getObjects(ctx, "/v1/channels", nil)
}

// Get returns one channel.
//
// GET /v1/channels/{id} — same shape as a List item, including the `embed`
// object with the loader URL, the snippet and the CSP directives a host page
// needs.
func (s *ChannelsService) Get(ctx context.Context, channelID string) (Object, error) {
	if channelID == "" {
		return nil, errors.New("chatly: Channels.Get: empty channel id")
	}
	return s.c.getObject(ctx, "/v1/channels/"+pathEscape(channelID), nil)
}
