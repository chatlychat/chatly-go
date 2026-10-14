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

// Adapters lists the channel TYPES this deployment can receive on.
//
// GET /v1/channels/adapters — scope channels:read. A bare array of PLAIN
// STRINGS — not objects with an id or a label — sorted and de-duplicated.
// That is why this is the one method here returning []string: a decoder built
// on the usual []Object would fail on every real call.
//
// The values are the union of the inbound adapter registry and every
// realtime-routed outbound type, which is why "web" appears despite having no
// inbound adapter. Deployment-wide rather than workspace-scoped: nothing in
// it is tenant data.
func (s *ChannelsService) Adapters(ctx context.Context) ([]string, error) {
	return s.c.getStrings(ctx, "/v1/channels/adapters", nil)
}

// The 17 types Create accepts. Narrower than what a channel RESPONSE may
// carry: `telegram_user` exists in the database enum and is readable, but is
// deliberately not creatable.
const (
	ChannelTypeWeb       = "web"
	ChannelTypeEmail     = "email"
	ChannelTypeWhatsApp  = "whatsapp"
	ChannelTypeMessenger = "messenger"
	ChannelTypeInstagram = "instagram"
	ChannelTypeSMS       = "sms"
	ChannelTypeVoice     = "voice"
	ChannelTypeAPI       = "api"
	ChannelTypeTelegram  = "telegram"
	ChannelTypeDiscord   = "discord"
	ChannelTypeApple     = "apple"
	ChannelTypeLINE      = "line"
	ChannelTypeViber     = "viber"
	ChannelTypeSlack     = "slack"
	ChannelTypeMSTeams   = "msteams"
	ChannelTypeTikTok    = "tiktok"
	ChannelTypeTwitter   = "twitter"
)

// ChannelInput is the body of Create. Only the first three fields are
// required.
type ChannelInput struct {
	// Type is one of the ChannelType* constants. Required, and NOT
	// patchable afterwards.
	Type string `json:"type,omitempty"`

	// Name, 1..120 chars. Required.
	Name string `json:"name,omitempty"`

	// PublicID is the id the embed snippet and the inbound webhook URLs
	// carry, 1..64 chars. Required, not patchable, and UNIQUE ACROSS EVERY
	// WORKSPACE — a collision is an unmapped constraint violation and comes
	// back as a 500, so namespace it rather than using "web" or "support".
	PublicID string `json:"publicId,omitempty"`

	// BrandID. Omitted, the workspace's DEFAULT brand is used — and a
	// workspace with no default brand cannot create a channel at all: that
	// is a 404 naming the missing default, which is a confusing answer to a
	// create.
	BrandID string `json:"brandId,omitempty"`

	// Enabled defaults to true.
	Enabled *bool `json:"enabled,omitempty"`

	// Config is the per-type configuration bag — credentials, numbers,
	// routing. Its shape is validated per channel type server-side, so a bad
	// voice or carrier shape is a 400 before anything is written.
	//
	// Whatever you send comes BACK REDACTED: every credential-bearing key
	// reads "__redacted__" in the response.
	Config map[string]any `json:"config,omitempty"`
}

// Create adds a channel and returns it.
//
// POST /v1/channels — scope channels:write, answers 201. channels:write IS a
// grantable key scope, precisely so a widget can be stood up from code.
//
// It is also plan-limited: at the plan's channel cap this is a 403 rather
// than a validation error.
//
// The response is the channel plus any provider-registration results
// (telegramWebhook, tiktokWebhook, twitterWebhook, viberWebhook,
// metaSubscription), each `{ ok, detail? }` — saving re-runs the provider
// registration against the plaintext config and reports what happened.
func (s *ChannelsService) Create(ctx context.Context, in ChannelInput) (Object, error) {
	if in.Type == "" || in.Name == "" || in.PublicID == "" {
		return nil, errors.New("chatly: Channels.Create: type, name and publicId are required")
	}
	return s.c.writeObject(ctx, "POST", "/v1/channels", nil, in)
}

// ChannelPatch is the body of Update. Every field is optional.
//
// `type`, `publicId` and `brandId` are NOT patchable, and because the body
// schema strips unknown keys rather than rejecting them, sending one of them
// is a 200 that changed nothing.
type ChannelPatch struct {
	Name    string `json:"name,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`

	AutoCreateTickets *bool `json:"autoCreateTickets,omitempty"`
	AutoAcknowledge   *bool `json:"autoAcknowledge,omitempty"`

	// AcknowledgeTemplate and AcknowledgeSubject are nullable server-side,
	// where an explicit null RESTORES the built-in copy. omitempty cannot
	// send a null, so use Client.Request for that.
	AcknowledgeTemplate string `json:"acknowledgeTemplate,omitempty"`
	AcknowledgeSubject  string `json:"acknowledgeSubject,omitempty"`

	// Config is applied according to ConfigMode.
	//
	// CREDENTIALS ROUND-TRIP: a stored secret reads "__redacted__" on every
	// read, and ECHOING THAT LITERAL BACK means "leave this one alone". So a
	// read-modify-write is safe — but a helper that strips or rewrites the
	// marker destroys the credential.
	Config map[string]any `json:"config,omitempty"`

	// ConfigMode is "merge" (the default) or "replace". Under merge the
	// patch follows RFC 7396, so a null VALUE inside Config deletes that
	// key; replace overwrites the whole object.
	ConfigMode string `json:"configMode,omitempty"`
}

// Update patches a channel and returns it.
//
// PATCH /v1/channels/{id} — scope channels:write.
//
// A body with no RECOGNISED field is a 409 ("No fields to update"), not a
// no-op 200 — and that includes a body whose only keys were the unpatchable
// ones, which is the confusing case.
//
// Changing the identity-verification secret demotes the old one to a
// previousSecret for a grace window, so a widget mid-session keeps working.
func (s *ChannelsService) Update(ctx context.Context, channelID string, in ChannelPatch) (Object, error) {
	if channelID == "" {
		return nil, errors.New("chatly: Channels.Update: empty channel id")
	}
	return s.c.writeObject(ctx, "PATCH", "/v1/channels/"+pathEscape(channelID), nil, in)
}

// RotateSecret mints a new Chatly-issued channel secret.
//
// POST /v1/channels/{id}/secret/rotate — scope channels:write, answers 201
// with `{ secret }`. No request body.
//
// ONLY an `api` channel has one: anything else is a 409, not a 404 and not a
// fresh secret.
//
// A HARD CUT with no grace window — whatever was using the old `csk_` value
// breaks the moment this returns. And note the asymmetry with reading: this
// rotate IS key-accessible while GET /v1/channels/{id}/secret is not,
// deliberately, because reading a secret leaves no trace and rotating one is
// loud.
func (s *ChannelsService) RotateSecret(ctx context.Context, channelID string) (Object, error) {
	if channelID == "" {
		return nil, errors.New("chatly: Channels.RotateSecret: empty channel id")
	}
	path := "/v1/channels/" + pathEscape(channelID) + "/secret/rotate"
	return s.c.writeObject(ctx, "POST", path, nil, nil)
}
