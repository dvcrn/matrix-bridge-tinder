package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	tinder "github.com/dvcrn/go-tinder"
)

// handleIncomingMatch bridges a new Tinder match to Matrix.
func (a *TinderClientAdapter) handleIncomingMatch(match *tinder.Match) error {
	ctx := context.Background()
	// Create a logger specific to this match event
	log := a.log.With().Str("tinder_match_id", match.ID).Str("subcomponent", "match-handler").Logger()
	ctx = log.WithContext(ctx) // Update context with the new logger

	person := match.Person

	portalNetworkID := networkid.PortalID(match.ID)
	portalKey := networkid.PortalKey{ID: portalNetworkID}

	// Acquire lock for this portal ID to prevent race conditions
	log.Debug().Msg("Attempting to acquire portal lock for match") // Use specific logger
	a.lock(portalKey.ID)
	defer a.unlock(portalKey.ID)
	log.Debug().Msg("Acquired portal lock for match") // Use specific logger

	portal, err := a.connector.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		log.Error().Err(err).Str("portal_key_id", string(portalKey.ID)).Msg("Failed to get or provision portal by key") // Use specific logger
		return fmt.Errorf("failed to get or provision portal by key: %w", err)
	}
	log.Info().Str("portal_mxid", string(portal.MXID)).Str("portal_key_id", string(portalKey.ID)).Msg("Successfully retrieved/provisioned portal") // Use specific logger

	if person == nil {
		if portal.MXID == "" {
			log.Warn().Any("match", match).Msg("Received match without person data for new portal, skipping")
			return fmt.Errorf("received match without person data for new portal")
		}
		log.Info().Str("portal_mxid", string(portal.MXID)).Msg("Received match without person data, but portal exists, proceeding with stored metadata")
		// Use existing metadata for further processing
	} else {
		log.Info().Str("name", match.Person.Name).Msg("Processing incoming Tinder match") // Use specific logger
		// Update logger with person ID
		log = log.With().Str("tinder_person_id", person.ID).Str("tinder_person_name", person.Name).Logger()
		ctx = log.WithContext(ctx) // Update context again
	}

	// Update logger with person ID
	ctx = log.WithContext(ctx) // Update context again

	var ghostNetworkUserID networkid.UserID
	if person != nil {
		ghostNetworkUserID = networkid.UserID(person.ID)

	} else if portal.OtherUserID != "" {
		ghostNetworkUserID = portal.OtherUserID
	}

	existingMetadata, hasMetadata := portal.Metadata.(*PortalMetadata)
	if hasMetadata && existingMetadata != nil && existingMetadata.UnmatchedAt != nil {
		log.Info().
			Time("unmatched_at", *existingMetadata.UnmatchedAt).
			Msg("Match was previously unmatched - user has been reinstated")

		// Handle reinstatement (pass nil for msg since this is from match processing)
		err := a.handleUserReinstatement(ctx, portal, nil)
		if err != nil {
			log.Error().Err(err).Msg("Failed to handle user reinstatement during match processing")
			// Continue anyway to update match data
		}
	} else {
		// Only check for desync and force unlock if not just reinstated
		var nameForReinstate string
		if person != nil {
			nameForReinstate = person.Name
		} else if hasMetadata && existingMetadata != nil {
			nameForReinstate = existingMetadata.InitialName
		}
		if err := a.checkAndForceReinstatementIfDesynced(ctx, portal, nameForReinstate); err != nil {
			log.Error().Err(err).Msg("Failed to check/force room unlock after match handling")
		}
	}

	portalNeedsCreate := portal.MXID == "" // Check if the portal MXID is empty (meaning it's new)

	// Prepare ChatInfo (used only if creating the room)
	var chatInfo *bridgev2.ChatInfo

	// --- Prepare and Save Initial Portal Metadata (Always run, use avatarMxcURI if set/retrieved) ---
	var initialMeta *PortalMetadata
	if person != nil {
		initialMeta = &PortalMetadata{
			InitialName:      person.Name,
			TinderMatch:      match,
			GhostUserID:      ghostNetworkUserID,
			MatchCreatedDate: match.CreatedDate,
			UnmatchedAt:      nil, // Clear any previous unmatch status
		}
	} else if hasMetadata && existingMetadata != nil {
		// Only update UnmatchedAt, preserve other fields
		initialMeta = &PortalMetadata{
			InitialName:      existingMetadata.InitialName,
			TinderMatch:      existingMetadata.TinderMatch,
			GhostUserID:      existingMetadata.GhostUserID,
			MatchCreatedDate: existingMetadata.MatchCreatedDate,
			UnmatchedAt:      nil,
		}
	}
	if initialMeta != nil {
		// Assign the struct directly; bridge framework handles (un)marshalling
		portal.Metadata = initialMeta
	}
	if ghostNetworkUserID != "" {
		portal.OtherUserID = ghostNetworkUserID // Set the ID of the other participant
	}
	portal.RoomType = database.RoomTypeDM

	err = a.connector.Bridge.DB.Portal.Update(ctx, portal.Portal) // portal.Portal accesses the embedded database.Portal
	if err != nil {
		log.Error().Err(err).Msg("Failed to update portal with OtherUserID") // Use specific logger
		return fmt.Errorf("failed to update portal with OtherUserID: %w", err)
	}

	log.Info().Msg("Successfully updated portal with OtherUserID") // Use specific logger
	if portalNeedsCreate {
		// Queue the ChatResync event
		a.connector.Bridge.QueueRemoteEvent(a.userLogin.User.GetDefaultLogin(), &simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type:         bridgev2.RemoteEventChatResync,
				LogContext:   nil, // Can enhance logging context here if needed
				PortalKey:    portalKey,
				CreatePortal: portalNeedsCreate, // Tell the bridge whether to create the room
			},
			ChatInfo:        chatInfo,          // Pass ChatInfo (nil if not creating, or with power levels if updating)
			LatestMessageTS: match.CreatedDate, // Provide timestamp for potential backfill trigger point
		})

		log.Info().Msg("Queued ChatResync event") // Use specific logger
	}
	// No further action needed here, backfill/sync will handle messages.
	return nil
}
