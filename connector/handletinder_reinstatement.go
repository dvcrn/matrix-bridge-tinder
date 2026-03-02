package connector

import (
	"context"
	"fmt"

	tinder "github.com/dvcrn/go-tinder"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	nobodyPowerLevel  = 100
	defaultPowerLevel = 0
)

// --- Self-healing reinstatement helpers ---

// isMatrixRoomLocked checks if the Matrix room is locked (read-only) by inspecting power levels.
func (a *TinderClientAdapter) isMatrixRoomLocked(ctx context.Context, mxid id.RoomID) (bool, error) {
	client := a.connector.Bridge.Matrix
	if client == nil {
		return false, fmt.Errorf("Matrix client is nil")
	}
	powerLevels, err := client.GetPowerLevels(ctx, mxid)
	if err != nil {
		return false, fmt.Errorf("failed to fetch power levels: %w", err)
	}
	msgLevel := nobodyPowerLevel
	if pl, ok := powerLevels.Events[event.EventMessage.String()]; ok {
		msgLevel = pl
	} else {
		msgLevel = powerLevels.EventsDefault
	}
	return msgLevel >= nobodyPowerLevel, nil
}

// forceUnlockMatrixRoom unlocks a Matrix room and sends a reinstatement notice, even if UnmatchedAt is nil.
func (a *TinderClientAdapter) forceUnlockMatrixRoom(ctx context.Context, portal *bridgev2.Portal, name string) error {
	log := a.log.With().
		Str("tinder_match_id", string(portal.ID)).
		Str("subcomponent", "force-unlock").
		Logger()
	ctx = log.WithContext(ctx)

	log.Info().Msg("Force-unlocking Matrix room due to desync (locked but UnmatchedAt is nil)")

	// Send reinstatement notification
	if name == "" {
		name = "This user"
	}
	content := &event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    fmt.Sprintf("✨ %s is back! (auto-unlocked due to desync)", name),
	}
	_, _ = a.connector.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Parsed: content}, nil)

	// Restore normal power levels
	powerLevelChanges := &bridgev2.PowerLevelOverrides{
		UsersDefault:  ptr.Ptr(0),
		EventsDefault: ptr.Ptr(0),
		StateDefault:  ptr.Ptr(50),
		Ban:           ptr.Ptr(50),
		Kick:          ptr.Ptr(50),
		Invite:        ptr.Ptr(50),
		Events: map[event.Type]int{
			event.EventMessage:     0,
			event.StateRoomName:    50,
			event.StateRoomAvatar:  50,
			event.StateTopic:       50,
			event.StatePowerLevels: 50,
			event.EventReaction:    0,
			event.EventRedaction:   0,
		},
	}
	chatInfoChange := &simplevent.ChatInfoChange{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatInfoChange,
			PortalKey: portal.PortalKey,
		},
		ChatInfoChange: &bridgev2.ChatInfoChange{
			MemberChanges: &bridgev2.ChatMemberList{
				PowerLevels: powerLevelChanges,
			},
		},
	}
	if a.userLogin != nil && a.userLogin.User != nil && a.userLogin.User.GetDefaultLogin() != nil {
		a.connector.Bridge.QueueRemoteEvent(a.userLogin.User.GetDefaultLogin(), chatInfoChange)
		log.Info().Msg("Queued power level restoration to unlock room (force-unlock)")
	} else {
		log.Error().Msg("Cannot queue ChatInfoChange for power levels: UserLogin is nil")
		return fmt.Errorf("failed to queue room unlock due to missing login context")
	}
	return nil
}

// checkAndForceReinstatementIfDesynced checks if the Matrix room is locked but UnmatchedAt is nil, and force-unlocks if so.
func (a *TinderClientAdapter) checkAndForceReinstatementIfDesynced(ctx context.Context, portal *bridgev2.Portal, name string) error {
	if portal == nil || portal.MXID == "" {
		return nil
	}
	locked, err := a.isMatrixRoomLocked(ctx, portal.MXID)
	if err != nil {
		return nil // Don't block on error
	}
	metadata, _ := portal.Metadata.(*PortalMetadata)
	if locked && (metadata == nil || metadata.UnmatchedAt == nil) {
		return a.forceUnlockMatrixRoom(ctx, portal, name)
	}
	return nil
}

// handleUserReinstatement processes a user reinstatement (receiving messages after being unmatched).
// msg can be nil when called from handleIncomingMatch
func (a *TinderClientAdapter) handleUserReinstatement(ctx context.Context, portal *bridgev2.Portal, msg *tinder.Message) error {
	log := a.log.With().
		Str("tinder_match_id", string(portal.ID)).
		Str("subcomponent", "reinstatement-handler").
		Logger()
	ctx = log.WithContext(ctx)

	log.Info().Msg("Processing user reinstatement...")

	// Lock is already acquired by the caller (handleIncomingMessage or handleIncomingMatch)

	// 1. Clear unmatch status in metadata
	metadata, ok := portal.Metadata.(*PortalMetadata)
	if !ok || metadata == nil {
		log.Error().Msg("Portal metadata is nil or not of expected type")
		return fmt.Errorf("invalid portal metadata type")
	}

	// Store the previous unmatch time for logging
	previousUnmatchTime := metadata.UnmatchedAt
	if previousUnmatchTime == nil {
		log.Warn().Msg("UnmatchedAt is already nil, no reinstatement needed")
		return nil
	}
	metadata.UnmatchedAt = nil

	// Save updated metadata
	err := a.connector.Bridge.DB.Portal.Update(ctx, portal.Portal)
	if err != nil {
		log.Error().Err(err).Msg("Failed to save portal metadata after clearing unmatch status")
		return fmt.Errorf("failed to save portal: %w", err)
	}

	log.Info().
		Time("previous_unmatch_time", *previousUnmatchTime).
		Msg("Cleared unmatch status from portal metadata")

	// 2. Send reinstatement notification to Matrix room
	name := metadata.InitialName
	if name == "" {
		name = "This user"
	}

	// Create message content
	content := &event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    fmt.Sprintf("✨ **%s** is back! Their account has been reinstated.", name),
	}

	// Send the message using the bot, same as unmatch notification
	_, err = a.connector.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Parsed: content}, nil)
	if err != nil {
		log.Error().Err(err).Str("matrix_room_id", portal.MXID.String()).Msg("Failed to send reinstatement notification message")
		// Continue anyway - don't let notification failure prevent room unlock
	} else {
		log.Info().Str("matrix_room_id", portal.MXID.String()).Msg("Sent reinstatement notification to room")
	}

	// 3. Unlock the room by restoring normal power levels
	log.Info().Msg("Queueing ChatInfoChange to restore room power levels")

	// Reset power levels to defaults (allowing normal room operations)
	powerLevelChanges := &bridgev2.PowerLevelOverrides{
		UsersDefault:  ptr.Ptr(0),  // Default user power level
		EventsDefault: ptr.Ptr(0),  // Default event power level
		StateDefault:  ptr.Ptr(50), // Default state event power level
		Ban:           ptr.Ptr(50), // Default ban power level
		Kick:          ptr.Ptr(50), // Default kick power level
		Invite:        ptr.Ptr(50), // Default invite power level
		Events: map[event.Type]int{
			event.EventMessage:     0,  // Allow messages
			event.StateRoomName:    50, // Restrict room name changes
			event.StateRoomAvatar:  50, // Restrict avatar changes
			event.StateTopic:       50, // Restrict topic changes
			event.StatePowerLevels: 50, // Restrict power level changes
			event.EventReaction:    0,  // Allow reactions
			event.EventRedaction:   0,  // Allow redactions
		},
	}

	chatInfoChange := &simplevent.ChatInfoChange{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatInfoChange,
			PortalKey: portal.PortalKey,
		},
		ChatInfoChange: &bridgev2.ChatInfoChange{
			MemberChanges: &bridgev2.ChatMemberList{
				PowerLevels: powerLevelChanges,
			},
		},
	}

	if a.userLogin != nil && a.userLogin.User != nil && a.userLogin.User.GetDefaultLogin() != nil {
		a.connector.Bridge.QueueRemoteEvent(a.userLogin.User.GetDefaultLogin(), chatInfoChange)
		log.Info().Msg("Queued power level restoration to unlock room")
	} else {
		log.Error().Msg("Cannot queue ChatInfoChange for power levels: UserLogin is nil")
		return fmt.Errorf("failed to queue room unlock due to missing login context")
	}

	log.Info().Msg("Successfully processed user reinstatement")
	return nil
}
