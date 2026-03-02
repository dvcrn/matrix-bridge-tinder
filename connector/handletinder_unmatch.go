package connector

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
)

// handleTinderUnmatch processes a detected unmatch event (received via Blocks list).
func (a *TinderClientAdapter) handleTinderUnmatch(ctx context.Context, tinderMatchID string) error {
	// Create a logger specific to this unmatch event
	log := a.log.With().Str("tinder_match_id", tinderMatchID).Str("subcomponent", "unmatch-handler").Logger()
	ctx = log.WithContext(ctx) // Update context with the new logger

	log.Info().Msg("Processing unmatch/block...")

	portalKey := networkid.PortalKey{ID: networkid.PortalID(tinderMatchID)}

	// Log before attempting to acquire lock
	log.Debug().Msg("Attempting to acquire portal lock for unmatch")
	// Acquire lock for this portal ID to prevent race conditions
	a.lock(portalKey.ID)
	defer a.unlock(portalKey.ID)
	log.Debug().Msg("Acquired portal lock for unmatch")

	// 1. Find Portal from DB
	// Use the logger 'log' which already has the tinder_match_id context
	portal, err := a.connector.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		log.Error().Err(err).Msg("Failed to find portal for unmatch/block") // Use specific logger
		return fmt.Errorf("failed to find portal for remote ID %s: %w", tinderMatchID, err)
	}
	if portal.MXID == "" {
		log.Warn().Msg("Received unmatch/block for a Tinder Match ID whose portal was not found in DB. Ignoring.") // Use specific logger
		return nil                                                                                                 // Not an error, nothing to process here.
	}

	log.Info().
		Interface("raw_metadata", portal.Metadata).
		Str("metadata_type", fmt.Sprintf("%T", portal.Metadata)).
		Msg("Raw metadata before type assertion")

	metadata, ok := portal.Metadata.(*PortalMetadata)
	if !ok || metadata == nil {
		log.Error().
			Interface("raw_metadata", portal.Metadata).
			Bool("type_assertion_ok", ok).
			Msg("Portal metadata is nil or not of expected type *PortalMetadata") // Use specific logger
		metadata = &PortalMetadata{}
		portal.Metadata = metadata
	}

	if metadata.UnmatchedAt != nil {
		log.Info().Time("unmatched_at", *metadata.UnmatchedAt).Msg("Unmatch/block already processed previously.") // Use specific logger
		return nil                                                                                                // Already done.
	}

	log.Info().Str("matrix_room_id", portal.MXID.String()).Msg("Found portal, proceeding to mark as unmatched.") // Use specific logger

	// 3. Update Metadata
	metadata.UnmatchedAt = ptr.Ptr(time.Now())

	portal.Portal.Metadata = metadata
	portal.Metadata = metadata

	// 4. Save Portal with updated metadata
	err = a.connector.Bridge.DB.Portal.Update(ctx, portal.Portal)
	if err != nil {
		log.Error().Err(err).Msg("Failed to update portal metadata in DB") // Use specific logger
		// If we fail to save the metadata, we should probably not proceed with Matrix actions
		return fmt.Errorf("failed to save updated portal %s: %w", portal.MXID, err)
	}
	log.Info().Msg("Marked portal as unmatched in DB.") // Use specific logger

	// 5. Send notification message and update power levels
	name := portal.Name // Use portal name as a fallback
	if metadata.InitialName != "" {
		name = metadata.InitialName // Prefer initial name from metadata if available
	}
	if name == "" {
		name = "This user" // Ultimate fallback
	}

	// Create message content
	content := &event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    fmt.Sprintf("%s is no longer a match. 💔", name),
	}

	// Send the message
	_, err = a.connector.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Parsed: content}, nil)
	if err != nil {
		log.Error().Err(err).Str("matrix_room_id", portal.MXID.String()).Msg("Failed to send unmatch notification message (continuing)") // Use specific logger
		// Log and continue - don't let notification failure prevent read-only attempt
	} else {
		log.Info().Str("matrix_room_id", portal.MXID.String()).Msg("Sent unmatch notification to room") // Use specific logger
	}

	// Set room to read-only by queueing a ChatInfoChange event
	log.Info().Msg("Queueing ChatInfoChange to set room to read-only") // Use specific logger
	powerLevelChanges := &bridgev2.PowerLevelOverrides{
		UsersDefault:  ptr.Ptr(defaultPowerLevel),
		EventsDefault: ptr.Ptr(int(nobodyPowerLevel)),
		StateDefault:  ptr.Ptr(nobodyPowerLevel),
		Ban:           ptr.Ptr(nobodyPowerLevel),
		Kick:          ptr.Ptr(nobodyPowerLevel),
		Invite:        ptr.Ptr(nobodyPowerLevel),
		Events: map[event.Type]int{
			event.EventMessage:     nobodyPowerLevel,
			event.StateRoomName:    nobodyPowerLevel,
			event.StateRoomAvatar:  nobodyPowerLevel,
			event.StateTopic:       nobodyPowerLevel,
			event.StatePowerLevels: nobodyPowerLevel,
			event.EventReaction:    nobodyPowerLevel,
			event.EventRedaction:   nobodyPowerLevel,
		},
	}

	// Use ChatInfoChange for delta power level update, following the correct structure
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

	// Queue the event using the default login for the user associated with this adapter
	if a.userLogin == nil || a.userLogin.User == nil || a.userLogin.User.GetDefaultLogin() == nil {
		log.Error().Msg("Cannot queue ChatInfoChange for power levels: UserLogin, User, or default login is nil.") // Use specific logger
		return fmt.Errorf("failed to queue room info change due to missing login context")
	}
	// Pass the user's default login as the first argument for sender context
	a.connector.Bridge.QueueRemoteEvent(a.userLogin.User.GetDefaultLogin(), chatInfoChange)

	log.Info().Msg("Successfully processed unmatch/block.") // Use specific logger
	return nil
}

