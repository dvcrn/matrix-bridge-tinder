package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	tinder "github.com/dvcrn/go-tinder"
)

var _ bridgev2.RemoteMessage = (*TinderRemoteMessage)(nil)

// handleIncomingMessage bridges a Tinder message to Matrix by queueing the RemoteMessage directly.
func (a *TinderClientAdapter) handleIncomingMessage(msg *tinder.Message) error {
	ctx := context.Background()
	log := a.log.With().Str("subcomponent", "message-handler").Str("tinder_msg_id", msg.ID).Logger()
	ctx = log.WithContext(ctx)

	log.Debug().Msg("Processing incoming Tinder message")

	// 1. Find Portal Key (used by RemoteMessage methods)
	portalKey := networkid.PortalKey{ID: networkid.PortalID(msg.MatchID)}

	// 2. Check if portal exists and if user is unmatched
	// Acquire lock early to prevent race conditions during reinstatement
	a.lock(portalKey.ID)
	defer a.unlock(portalKey.ID)

	portal, err := a.connector.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get portal for message")
		return fmt.Errorf("failed to get portal for message: %w", err)
	}
	if portal == nil {
		log.Warn().Msg("Portal not found for message, ignoring")
		return fmt.Errorf("portal not found for message")
	}

	// Check if portal is unmatched
	if metadata, ok := portal.Metadata.(*PortalMetadata); ok && metadata != nil && metadata.UnmatchedAt != nil {
		log.Info().
			Time("unmatched_at", *metadata.UnmatchedAt).
			Str("match_id", msg.MatchID).
			Msg("Received message from previously unmatched user - user may be reinstated")

		// Handle reinstatement
		err := a.handleUserReinstatement(ctx, portal, msg)
		if err != nil {
			log.Error().Err(err).Msg("Failed to handle user reinstatement")
			return fmt.Errorf("failed to handle user reinstatement: %w", err)
		}
		// Continue processing the message after successful reinstatement
	}

	// 3. Determine Sender Network ID
	senderNetworkUserID := networkid.UserID(msg.From)
	// Perform IsFromMe check here
	isFromMe := a.IsThisUser(ctx, senderNetworkUserID)

	// 4. Get UserLogin needed for queueing
	if a.userLogin == nil || a.userLogin.User == nil {
		log.Error().Msg("UserLogin or associated User is nil, cannot queue remote event")
		return fmt.Errorf("UserLogin or associated User is nil, cannot queue remote event")
	}
	login := a.userLogin.User.GetDefaultLogin()
	if login == nil {
		log.Error().Str("matrix_user_id", string(a.userLogin.User.MXID)).Msg("Default login for user is nil, cannot queue remote event")
		return fmt.Errorf("Default login for user is nil, cannot queue remote event")
	}

	// 5. Create Remote Message Wrapper using type from types.go
	remoteMsgWrapper := &TinderRemoteMessage{
		Msg:      msg,
		Sender:   senderNetworkUserID, // Correct field name
		IsFromMe: a.IsThisUser(ctx, senderNetworkUserID),
		Login:    login,
	}

	// 6. Queue the *TinderRemoteMessage directly as the event.
	// It implements both RemoteEvent and RemoteMessage.
	a.connector.Bridge.QueueRemoteEvent(login, remoteMsgWrapper)

	// Use the previously calculated isFromMe boolean for logging
	log.Info().Str("tinder_msg_id", msg.ID).Bool("is_from_me", isFromMe).Msg("Queued incoming Tinder message directly as RemoteMessage event")
	return nil
}

