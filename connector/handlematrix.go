package connector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	// "github.com/rs/zerolog"
	tinder "github.com/dvcrn/go-tinder"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// GetUserInfo fetches info about a remote user (Tinder profile).
func (a *TinderClientAdapter) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if ghost == nil {
		return nil, fmt.Errorf("ghost is nil")
	}

	log := a.log.With().Str("ghost_id", string(ghost.ID)).Logger()
	ctx = log.WithContext(ctx)
	log.Debug().Msg("GetUserInfo called")

	var profileName string
	var photos []*tinder.Photo

	if a.IsThisUser(ctx, ghost.ID) {
		if a.meta.OwnuserProfile == nil {
			log.Warn().Msg("Own user profile not loaded in metadata")
			// Attempt to fetch own profile? Or return error?
			// For now, return error if essential info is missing.
			return nil, fmt.Errorf("own user profile not available")
		}
		profileName = a.meta.OwnuserProfile.Name
		photos = a.meta.OwnuserProfile.Photos // Get photos from own profile
		log.Debug().Str("name", profileName).Msg("Returning info for bridged user")
	} else {
		log.Debug().Msg("Fetching profile for remote user")
		profile, err := a.tClient.GetUser(string(ghost.ID))
		if err != nil {
			return nil, fmt.Errorf("failed to get Tinder profile for %s: %w", string(ghost.ID), err)
		}

		if profile == nil || profile.Results == nil {
			return nil, fmt.Errorf("received nil profile or results for user %s", string(ghost.ID))
		}

		if profile.Results.Name == "" {
			log.Warn().Msg("Received profile without a name")
			// Allow empty name for now, maybe Matrix requires one?
		}
		profileName = profile.Results.Name
		photos = profile.Results.Photos // Get photos from fetched profile
		log.Debug().Str("name", profileName).Msg("Got profile for remote user")
	}

	userInfo := &bridgev2.UserInfo{
		Name: &profileName,
	}

	// Handle Avatar using Get function
	photoURL := ""
	var photoID string
	if len(photos) > 0 {
		photoURL = photos[0].URL
		photoID = photos[0].ID // Use the photo's ID for the Avatar ID
	}

	if photoURL != "" && photoID != "" {
		log.Debug().Str("photo_url", photoURL).Msg("Found photo URL for avatar Get function")
		userInfo.Avatar = &bridgev2.Avatar{
			ID: networkid.AvatarID(photoID), // Use photo ID
			Get: func(ctx context.Context) ([]byte, error) {
				log.Info().Str("photo_url", photoURL).Msg("Executing Avatar.Get for UserInfo")
				resp, err := http.Get(photoURL)
				if err != nil {
					log.Warn().Err(err).Str("photo_url", photoURL).Msg("Failed to fetch avatar image")
					return nil, fmt.Errorf("failed to fetch avatar image: %w", err)
				}
				defer resp.Body.Close()

				imageData, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Warn().Err(err).Msg("Failed to read avatar image data")
					return nil, fmt.Errorf("failed to read avatar image data: %w", err)
				}
				log.Debug().Msg("Avatar.Get for UserInfo successful")
				return imageData, nil
			},
		}
	} else {
		log.Debug().Msg("No suitable photo URL/ID found for UserInfo avatar")
	}

	return userInfo, nil
}

// GetChatInfo fetches info about a remote chat (Tinder match).
// This function provides the complete desired state for the Matrix room representing the Tinder match.
func (a *TinderClientAdapter) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	tinderMatchID := string(portal.ID)
	log := a.log.With().Str("portal_mxid", string(portal.MXID)).Str("tinder_match_id", tinderMatchID).Logger()
	ctx = log.WithContext(ctx)
	log.Debug().Msg("GetChatInfo called")

	tinderPersonID := string(portal.OtherUserID)
	if tinderPersonID == "" {
		log.Warn().Msg("Portal OtherUserID is empty, cannot fetch chat info accurately")
		return nil, fmt.Errorf("portal %s has no OtherUserID defined", portal.MXID)
	}

	log = log.With().Str("tinder_person_id", tinderPersonID).Logger()
	ctx = log.WithContext(ctx)

	log.Info().Msg("Fetching Tinder user profile for ChatInfo")
	tinderProfile, err := a.tClient.GetUser(tinderPersonID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get Tinder user profile for ChatInfo")
		return nil, fmt.Errorf("failed to get Tinder profile for %s: %w", tinderPersonID, err)
	}

	if tinderProfile == nil || tinderProfile.Results == nil {
		log.Error().Msg("Tinder GetUser returned nil profile or results for ChatInfo")
		return nil, fmt.Errorf("Tinder GetUser returned nil profile for %s", tinderPersonID)
	}
	person := tinderProfile.Results
	log.Info().Str("tinder_name", person.Name).Msg("Got Tinder user profile for ChatInfo")

	chatName := fmt.Sprintf("%s 🔥", person.Name)
	chatTopic := fmt.Sprintf("Chat with %s on Tinder", person.Name)

	// Construct Members & Power Levels first
	ghostNetworkUserID := networkid.UserID(tinderPersonID)
	bridgedUserNetworkID := networkid.UserID(a.meta.TinderUserID)

	members := &bridgev2.ChatMemberList{
		OtherUserID:      ghostNetworkUserID,
		CheckAllLogins:   true,
		IsFull:           true,
		TotalMemberCount: 2,
		MemberMap: map[networkid.UserID]bridgev2.ChatMember{
			ghostNetworkUserID: {
				EventSender: bridgev2.EventSender{
					Sender:   ghostNetworkUserID,
					IsFromMe: false,
				},
				PowerLevel: ptr.Ptr(defaultPowerLevel),
			},
			bridgedUserNetworkID: {
				EventSender: bridgev2.EventSender{
					Sender:      networkid.UserID(a.meta.TinderUserID),
					SenderLogin: a.userLogin.ID,
					IsFromMe:    true,
				},
				PowerLevel: ptr.Ptr(defaultPowerLevel),
			},
		},
		PowerLevels: &bridgev2.PowerLevelOverrides{
			EventsDefault: ptr.Ptr(defaultPowerLevel),
			StateDefault:  ptr.Ptr(nobodyPowerLevel),
			Ban:           ptr.Ptr(nobodyPowerLevel),
			Kick:          ptr.Ptr(nobodyPowerLevel),
			Invite:        ptr.Ptr(nobodyPowerLevel),
			Events: map[event.Type]int{
				event.StateRoomName:    nobodyPowerLevel,
				event.StateRoomAvatar:  nobodyPowerLevel,
				event.StateTopic:       nobodyPowerLevel,
				event.StatePowerLevels: nobodyPowerLevel,
				event.EventReaction:    defaultPowerLevel,
				event.EventRedaction:   nobodyPowerLevel,
			},
		},
	}

	// Base ChatInfo
	chatInfo := &bridgev2.ChatInfo{
		Name:    &chatName,
		Topic:   &chatTopic,
		Type:    ptr.Ptr(database.RoomTypeDM),
		Members: members,
	}

	// Handle Avatar using Get function
	photoURL := ""
	var photoID string
	if len(person.Photos) > 0 {
		photoURL = person.Photos[0].URL
		photoID = person.Photos[0].ID
	}

	if photoURL != "" && photoID != "" {
		log.Debug().Str("photo_url", photoURL).Msg("Found photo URL for avatar Get function")
		chatInfo.Avatar = &bridgev2.Avatar{
			ID: networkid.AvatarID(photoID),
			Get: func(ctx context.Context) ([]byte, error) {
				log.Info().Str("photo_url", photoURL).Msg("Executing Avatar.Get for ChatInfo")
				resp, err := http.Get(photoURL)
				if err != nil {
					log.Warn().Err(err).Str("photo_url", photoURL).Msg("Failed to fetch avatar image")
					return nil, fmt.Errorf("failed to fetch avatar image: %w", err)
				}
				defer resp.Body.Close()

				imageData, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Warn().Err(err).Msg("Failed to read avatar image data")
					return nil, fmt.Errorf("failed to read avatar image data: %w", err)
				}
				log.Debug().Msg("Avatar.Get for ChatInfo successful")
				return imageData, nil
			},
		}
	} else {
		log.Debug().Msg("No suitable photo URL/ID found for ChatInfo avatar")
	}

	log.Debug().Msg("Returning constructed ChatInfo")
	return chatInfo, nil
}

// GetCapabilities returns features supported for a specific chat/portal.
func (a *TinderClientAdapter) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	// Return capabilities similar to the reference implementation (assuming full support for now)
	return &event.RoomFeatures{
		MaxTextLength: 65536, // TODO: Check actual Tinder limits
		Formatting: event.FormattingFeatureMap{
			// Use event.CapLevelFullySupported (adjust later based on actual Tinder features)
			event.FmtBold:          event.CapLevelFullySupported,
			event.FmtItalic:        event.CapLevelFullySupported,
			event.FmtUnderline:     event.CapLevelFullySupported,
			event.FmtStrikethrough: event.CapLevelFullySupported,
			event.FmtInlineCode:    event.CapLevelFullySupported,
			event.FmtCodeBlock:     event.CapLevelFullySupported,
		},
		// Use event.CapLevelFullySupported (adjust later based on actual Tinder features)
		Edit:   event.CapLevelUnsupported,
		Reply:  event.CapLevelUnsupported,
		Thread: event.CapLevelUnsupported,
	}
}

// HandleMatrixMessage sends a message from Matrix to Tinder.
func (a *TinderClientAdapter) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	log := a.log.With().
		Str("portal_id", string(msg.Portal.ID)).
		Str("sender_mxid", string(msg.Event.Sender)).
		Str("event_id", string(msg.Event.ID)).
		Logger()
	ctx = log.WithContext(ctx)

	log.Info().Msg("HandleMatrixMessage called")

	// 1. Extract Message Content
	content := msg.Event.Content.AsMessage()
	if content == nil {
		return nil, fmt.Errorf("failed to parse message content")
	}

	// 2. Validate Message Type
	if content.MsgType != event.MsgText {
		log.Warn().
			Str("msgtype", string(content.MsgType)).
			Msg("Unsupported message type, only text messages are supported")
		return nil, fmt.Errorf("unsupported message type %q, only text messages are supported", content.MsgType)
	}

	// 3. Get Match ID from Portal ID
	// The portal ID is our Tinder Match ID (set during portal creation)
	matchID := string(msg.Portal.ID)

	// 4. Send Message via Tinder Client
	log.Debug().
		Str("match_id", matchID).
		Str("message", content.Body).
		Msg("Sending message to Tinder")

	// Get required IDs for sending message
	userID := a.meta.TinderUserID
	otherID := string(msg.Portal.OtherUserID)

	sentMsg, err := a.tClient.SendMessage(userID, otherID, matchID, "", content.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to send message to Tinder")
		return nil, fmt.Errorf("failed to send message to Tinder: %w", err)
	}

	// 5. Create Response with Message Details
	log.Info().
		Str("remote_msg_id", sentMsg.ID).
		Msg("Message sent to Tinder successfully")

	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        networkid.MessageID(sentMsg.ID),
			SenderID:  networkid.UserID(a.meta.TinderUserID), // Set sender as our Tinder user
			Timestamp: time.UnixMilli(int64(sentMsg.Timestamp)),
		},
	}, nil
}
