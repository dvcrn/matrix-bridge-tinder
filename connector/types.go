package connector

import (
	"context"
	"time"

	tinder "github.com/dvcrn/go-tinder"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// TinderLoginMetadata stores Tinder authentication information securely in the bridge database.
type TinderLoginMetadata struct {
	AccessToken        string    `json:"access_token"`         // The Tinder API access token.
	RefreshToken       string    `json:"refresh_token"`        // The Tinder API refresh token.
	AccessTokenExpires time.Time `json:"access_token_expires"` // When the access token expires.
	TinderUserID       string    `json:"tinder_user_id"`       // The user's Tinder ID.
	DeviceID           string    `json:"device_id,omitempty"`  // The device ID used for authentication.

	// Add any other necessary Tinder session/auth info here
	OwnuserProfile *tinder.UserProfile `json:"-"`
}

// New creates a new instance for the database registration.
func (t *TinderLoginMetadata) New() any {
	return &TinderLoginMetadata{}
}

// TinderGhostMetadata stores Tinder-specific information associated with a Matrix ghost.
// The Ghost.ID (networkid.UserID) will correspond to the Tinder PersonID.
type TinderGhostMetadata struct {
	TinderUserID  string `json:"tinder_user_id"`  // Redundant with Ghost.ID, but explicit
	TinderMatchID string `json:"tinder_match_id"` // ID of the match this ghost represents
	// Include other relevant details like Tinder username, profile info, etc. if needed
	// Example: TinderUsername string `json:"tinder_username"`
	// Example: ProfilePhotoURL string `json:"profile_photo_url"`
}

// New creates a new instance for the database registration.
func (t *TinderGhostMetadata) New() any {
	return &TinderGhostMetadata{}
}

// PortalMetadata stores Tinder-specific information associated with a Matrix portal (room).
type PortalMetadata struct {
	// Initial data captured when the match was first processed or the portal created.
	InitialName      string              `json:"initial_name,omitempty"`
	TinderMatch      *tinder.Match       `json:"tinder_match,omitempty"`
	InitialAvatarMXC id.ContentURIString `json:"initial_avatar_mxc,omitempty"`
	GhostUserID      networkid.UserID    `json:"ghost_user_id,omitempty"`
	MatchCreatedDate time.Time           `json:"match_created_date,omitempty"`
	UnmatchedAt      *time.Time          `json:"unmatched_at,omitempty"` // Timestamp when the match was detected as unmatched/blocked
	// Add other portal-specific metadata if needed in the future
}

// New creates a new instance for the database registration.
func (t *PortalMetadata) New() any {
	return &PortalMetadata{}
}

// --- Remote Event Wrappers for HandleMessage ---

// TinderRemoteMessage wraps a Tinder message to implement bridgev2 interfaces.
type TinderRemoteMessage struct {
	Msg      *tinder.Message
	Sender   networkid.UserID // The network ID of the sender (ghost or user)
	IsFromMe bool
	Login    *bridgev2.UserLogin // The login associated with the sender if IsFromMe is true
}

// Ensure it implements RemoteMessage
var _ bridgev2.RemoteMessage = (*TinderRemoteMessage)(nil)

func (trm *TinderRemoteMessage) GetID() networkid.MessageID {
	return networkid.MessageID(trm.Msg.ID)
}

func (trm *TinderRemoteMessage) GetSender() bridgev2.EventSender {
	sender := bridgev2.EventSender{
		Sender:   trm.Sender,
		IsFromMe: trm.IsFromMe,
	}
	// Set SenderLogin only if it's from the bridged user and Login is available
	if trm.IsFromMe && trm.Login != nil {
		sender.SenderLogin = trm.Login.ID
	}
	return sender
}

func (trm *TinderRemoteMessage) GetConversationID() networkid.PortalID {
	// Assuming MatchID corresponds to the PortalID
	return networkid.PortalID(trm.Msg.MatchID)
}

// GetPortalKey implements bridgev2.RemoteMessage (and RemoteEventWithTimestamp implicitly)
func (trm *TinderRemoteMessage) GetPortalKey() networkid.PortalKey {
	// Assuming MatchID corresponds to the PortalID's ID field.
	// Receiver might be empty for this bridge type.
	return networkid.PortalKey{ID: networkid.PortalID(trm.Msg.MatchID), Receiver: ""}
}

// ConvertMessage implements bridgev2.RemoteMessage
// Signature updated to match interface: needs Portal and MatrixAPI (intent)
func (trm *TinderRemoteMessage) ConvertMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	// Basic text message conversion
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    trm.Msg.Message,
	}

	convertedMsg := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			Type:    event.EventMessage,
			Content: content,
		}},
		// TODO: Handle replies, threads, media, etc. later if needed
	}
	return convertedMsg, nil
}

// AddLogContext implements bridgev2.RemoteMessage
func (trm *TinderRemoteMessage) AddLogContext(logCtx zerolog.Context) zerolog.Context {
	// Add relevant context
	return logCtx.Str("tinder_msg_id", trm.Msg.ID).
		Str("tinder_sender", string(trm.Sender))
}

// GetType implements bridgev2.RemoteEvent
func (trm *TinderRemoteMessage) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessage // Assuming it's always a message for now
}

// Ensure it implements RemoteEventWithTimestamp
var _ bridgev2.RemoteEventWithTimestamp = (*TinderRemoteMessage)(nil)

func (trm *TinderRemoteMessage) GetTimestamp() time.Time {
	return time.UnixMilli(int64(trm.Msg.Timestamp))
}

// GetText returns the text content of the message.
// This might need to be adjusted if ConvertRemoteMessage expects it.
func (trm *TinderRemoteMessage) GetText() string {
	return trm.Msg.Message
}

// Note: This wrapper might need more methods depending on what HandleMessage
// and ConvertRemoteMessage actually require (e.g., handling edits, reactions, media).
