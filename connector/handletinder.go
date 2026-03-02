package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/getsentry/sentry-go"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	pb "github.com/dvcrn/go-tinder/pb"
)

// initialSync performs the first data fetch after connection.
func (a *TinderClientAdapter) initialSync(ctx context.Context) error {
	log := a.log.With().Str("subcomponent", "initial-sync").Logger()
	log.Info().Msg("Starting initial data sync...")

	// Wait a bit for WS connection to potentially establish (optional)
	// time.Sleep(5 * time.Second)

	// 1. Fetch own profile (optional, might be needed for user's Tinder ID)
	// profile, err := a.tClient.GetOwnUser()
	// if err != nil { ... handle error ...}
	// store profile.ID if needed for IsThisUser check

	// 2. Fetch initial matches
	log.Info().Msg("Fetching initial matches...")
	matches, err := a.tClient.GetMatches(30) // Fetch recent 30 matches initially
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch initial matches")
		// Capture initial sync failure to Sentry
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("error_type", "initial_sync_failure")
			scope.SetTag("sync_stage", "fetch_matches")
			scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
			sentry.CaptureException(err)
		})
		return err
	}
	log.Info().Int("count", len(matches)).Msg("Processing initial matches...")

	// 3. Process matches
	for _, match := range matches {
		// Process each match concurrently? Or sequentially?
		// Sequential might be safer for initial setup.
		if err := a.handleIncomingMatch(match); err != nil {
			log.Error().Err(err).Str("match_id", match.ID).Msg("Failed to handle incoming match during initial sync")
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("error_type", "initial_sync_failure")
				scope.SetTag("sync_stage", "handle_incoming_match")
				scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
				scope.SetExtra("match_id", match.ID)
				sentry.CaptureException(err)
			})
			// Continue processing other matches
		}
	}

	log.Info().Msg("Running 10 minute nudge check")
	since := time.Now().UTC().Add(-10 * time.Minute) // Sync last 10 mins

	// 4. Initial message backfill (optional, can be complex)
	// Maybe trigger syncTinderUpdatesSince with a very old timestamp?
	// Or loop through processed matches and call GetMessages like v1's syncTinderMessages?
	if err := a.syncTinderUpdatesSince(a.ctx, since); err != nil {
		log.Error().Err(err).Msg("Failed to sync Tinder updates during initial sync")
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("error_type", "initial_sync_failure")
			scope.SetTag("sync_stage", "sync_tinder_updates")
			scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
			sentry.CaptureException(err)
		})
		return err
	}
	log.Info().Msg("Initial data sync completed.")
	return nil
}

// handleIncomingEventsGoroutine triggers sync on Nudge and handles errors.
func (a *TinderClientAdapter) handleIncomingEventsGoroutine() {
	log := a.log.With().Str("subcomponent", "ws-handler").Logger()
	log.Info().Msg("Starting incoming event handler goroutine...")
	defer log.Info().Msg("Stopping incoming event handler goroutine.")

	for {
		select {
		case <-a.ctx.Done():
			return
		case eventData, ok := <-a.eventChan:
			if !ok {
				log.Warn().Msg("Event channel closed unexpectedly.")
				return
			}

			// Trigger sync based on Nudge
			go func(data *pb.ClientData) {
				if data == nil {
					log.Warn().Msg("Received nil data on event channel")
					return
				}
				if data.GetTypingIndicator() != nil {
					// log.Debug().Msg("Ignoring typing indicator") // Optional: too noisy?
					return
				}
				if nudge := data.GetNudge(); nudge != nil {
					b, _ := json.MarshalIndent(nudge, "", "  ")
					log.Info().RawJSON("nudge_data", b).Msg("Received Nudge")
					// Trigger sync based on nudge update time
					if updateTime := nudge.GetUpdateTime(); updateTime != nil {
						since := updateTime.AsTime().Add(-2 * time.Minute) // Sync last 2 mins
						log.Info().Time("since", since).Msg("Triggering sync handler from nudge")
						// Run sync in its own goroutine to avoid blocking event handler
						go func() {
							if err := a.syncTinderUpdatesSince(a.ctx, since); err != nil {
								log.Error().Err(err).Msg("Error during syncTinderUpdatesSince triggered by nudge")
								sentry.WithScope(func(scope *sentry.Scope) {
									scope.SetTag("error_type", "sync_updates_failure")
									scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
									scope.SetContext("sync", map[string]interface{}{
										"since":  since.Format(time.RFC3339),
										"source": "handleIncomingEventsGoroutine",
									})
									sentry.CaptureException(err)
								})
							}
						}()
					} else {
						log.Warn().Msg("Nudge received without UpdateTime, cannot sync")
					}
				} else {
					// Log other unexpected event types
					b, _ := json.MarshalIndent(data, "", "  ")
					log.Warn().RawJSON("event_data", b).Msg("Received unhandled event type from WebSocket")
				}
			}(eventData) // Pass eventData to goroutine
		}
	}
}

// syncTinderUpdatesSince fetches and processes updates since a given time.
func (a *TinderClientAdapter) syncTinderUpdatesSince(ctx context.Context, since time.Time) error {
	log := a.log.With().Str("subcomponent", "sync-handler").Time("since", since).Logger()
	log.Info().Msg("Syncing Tinder updates...")

	// 1. Get Updates from Tinder API
	updates, err := a.tClient.GetUpdates(since)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get Tinder updates")
		// Capture sync failure to Sentry
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("error_type", "sync_updates_failure")
			scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
			scope.SetContext("sync", map[string]interface{}{
				"since": since.Format(time.RFC3339),
			})
			sentry.CaptureException(err)
		})
		return err
	}

	log.Info().
		Int("match_count", len(updates.Matches)).
		Int("block_count", len(updates.Blocks)). // Log the number of blocks/unmatches received
		Msg("Fetched updates from API")

	// 2. Process Unmatches (Blocks)
	var syncErr error
	if len(updates.Blocks) > 0 {
		log.Info().Strs("blocked_ids", updates.Blocks).Msg("Processing unmatches (blocks)...")
		for _, tinderMatchID := range updates.Blocks {
			// Process unmatch sequentially for now.
			err := a.handleTinderUnmatch(ctx, tinderMatchID)
			if err != nil {
				// Log the error but continue processing other blocks/matches
				log.Error().Err(err).Str("tinder_match_id", tinderMatchID).Msg("Failed to process unmatch/block")
				syncErr = err
			}
		}
	} else {
		log.Debug().Msg("No unmatches (blocks) in this update.")
	}

	// 3. Process Matches and Messages from API Updates
	log.Info().Msg("Processing matches and messages received from API...")
	for _, match := range updates.Matches {
		matchLog := log.With().Str("tinder_match_id", match.ID).Logger()

		// A. Process the Match itself (Create/Update)
		// handleIncomingMatch should be idempotent or handle updates correctly.
		if err := a.handleIncomingMatch(match); err != nil {
			matchLog.Error().Err(err).Msg("Failed to handle incoming match")
			syncErr = err
			continue
		}

		// B. Process Messages for the Match (if included in update)
		// Rely on messages included in the update payload. Backfill is handled elsewhere (FetchMessages).
		if len(match.Messages) > 0 {
			matchLog.Info().Int("message_count", len(match.Messages)).Msg("Processing messages included in match update")
			// Sort messages by timestamp to process in order
			sort.Slice(match.Messages, func(i, j int) bool {
				return match.Messages[i].Timestamp < match.Messages[j].Timestamp
			})
			for _, msg := range match.Messages {
				msgLog := matchLog.With().Str("tinder_msg_id", msg.ID).Logger()
				msgLog.Debug().Msg("Processing incoming message from sync update")
				if err := a.handleIncomingMessage(msg); err != nil {
					msgLog.Error().Err(err).Msg("Failed to handle incoming message")
					syncErr = err
				}
			}
		} else if match.LastActivityDate.After(since) {
			// Log if activity is recent but no messages in payload, indicating potential need for backfill later.
			matchLog.Debug().Msg("Match has recent activity but no messages in this update payload.")
		}
	}

	log.Info().Msg("Finished syncing Tinder updates")
	return syncErr
}

// startPeriodicSyncAndRefresh starts a ticker to periodically refresh the
// Tinder API token and sync recent updates.
func (a *TinderClientAdapter) startPeriodicSyncAndRefresh() {
	log := a.log.With().Str("subcomponent", "periodic-sync").Logger()
	log.Info().Msg("Starting periodic sync and refresh ticker...")

	ticker := time.NewTicker(10 * time.Minute) // TODO: Make interval configurable?

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				log.Info().Msg("Context done, stopping periodic sync ticker.")
				return

			case <-ticker.C:
				log.Info().Msg("[TICK] Starting periodic tasks...")

				// --- Token Refresh ---
				log.Info().Msg("[TICK] Attempting token refresh...")
				if a.userLogin == nil || a.userLogin.Metadata == nil {
					log.Error().Msg("[TICK] Cannot refresh token: UserLogin or Metadata is nil.")
					// Optionally continue to sync even if token refresh fails?
					// continue or proceed to sync below
				} else {
					meta, ok := a.userLogin.Metadata.(*TinderLoginMetadata)
					if !ok || meta == nil {
						log.Error().Msg("[TICK] Cannot refresh token: Failed to assert UserLogin.Metadata to *TinderLoginMetadata or it's nil.")
					} else if meta.RefreshToken == "" {
						log.Warn().Msg("[TICK] Cannot refresh token: RefreshToken is empty in metadata.")
					} else {
						tokenRes, err := a.tClient.RefreshToken(meta.RefreshToken)
						if err != nil || tokenRes == nil {
							log.Error().Err(err).Msg("[TICK] Failed to refresh Tinder token.")
							// Capture token refresh failure to Sentry
							sentry.WithScope(func(scope *sentry.Scope) {
								scope.SetTag("error_type", "periodic_token_refresh_failure")
								scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
								if err != nil {
									sentry.CaptureException(err)
								} else {
									sentry.CaptureMessage("Token refresh returned nil response")
								}
							})
							// Decide how to handle refresh failure (e.g., maybe disconnect?)
						} else {
							log.Info().Msg("[TICK] Successfully refreshed Tinder token.")
							meta.RefreshToken = tokenRes.RefreshToken
							meta.AccessToken = tokenRes.AuthToken
							// Ensure AuthTokenTtl is not nil before accessing its Value
							if tokenRes.AuthTokenTtl != nil {
								ttlMillis := tokenRes.AuthTokenTtl.Value // Access Value directly
								meta.AccessTokenExpires = time.Now().Add(time.Millisecond * time.Duration(ttlMillis))
								log.Debug().Time("new_expiry", meta.AccessTokenExpires).Msg("Updated access token expiry.")
							} else {
								log.Warn().Msg("[TICK] Refreshed token response missing AuthTokenTtl, expiry not updated.")
							}

							// Update the metadata on the UserLogin object itself (in memory)
							a.userLogin.Metadata = meta

							// Persist updated user login data (including metadata)
							// Get the embedded database.UserLogin
							dbUserLogin := a.userLogin.UserLogin // Access the embedded database.UserLogin
							if dbUserLogin == nil {
								log.Error().Msg("[TICK] Cannot update database: UserLogin.UserLogin is nil")
							} else {
								// Update the metadata on the database.UserLogin struct
								dbUserLogin.Metadata = meta // Assign the updated metadata
								// Call Update on the UserLogin query interface
								if err := a.connector.Bridge.DB.UserLogin.Update(a.ctx, dbUserLogin); err != nil {
									log.Error().Err(err).Msg("[TICK] Failed to update user login data in database after token refresh.")
								} else {
									log.Info().Msg("[TICK] Successfully updated user login data in database.")
								}
							}
						}
					}
				}

				// --- Data Sync ---
				log.Info().Msg("[TICK] Triggering data sync...")
				// Sync updates since the last ticker interval
				since := time.Now().UTC().Add(-10 * time.Minute) // Sync last 10 mins
				// Run sync in its own goroutine to avoid blocking the ticker loop
				// if the sync takes longer than the interval (though syncTinderUpdatesSince is already async internally)
				go a.syncTinderUpdatesSince(a.ctx, since)
				log.Info().Msg("[TICK] Finished periodic tasks.")
			}
		}
	}()
}

// handleIncomingTinderEvent is NO LONGER the primary dispatcher.
// Kept temporarily, might remove later.
func (a *TinderClientAdapter) handleIncomingTinderEvent(eventData *pb.ClientData) {
	// This function is likely redundant now as dispatch happens in handleIncomingEventsGoroutine
	log := a.log.With().Str("subcomponent", "event-parser-unused").Logger()
	b, _ := json.MarshalIndent(eventData, "", "  ")
	log.Warn().RawJSON("event_data", b).Msg("handleIncomingTinderEvent called directly (should be handled by goroutine)")
}

// FetchMessages implements BackfillingNetworkAPI.FetchMessages
func (a *TinderClientAdapter) FetchMessages(ctx context.Context, fetchParams bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	log := a.log.With().Str("subcomponent", "backfill").Logger()
	portal := fetchParams.Portal

	a.lock(portal.ID)
	defer a.unlock(portal.ID)

	log.Info().Str("portal_mxid", string(portal.MXID)).Msg("Fetching messages for backfill")

	// Extract match ID from portal key
	matchID := string(portal.ID)

	// Flag to indicate if this is the very first population of the room
	isInitialPopulation := false
	// Slice to hold prepended initial messages
	initialMessages := make([]*bridgev2.BackfillMessage, 0, 2)

	// --- Check if we need to prepend initial messages ---
	// if !fetchParams.Forward {
	// Check DB count only if fetching backwards
	// Use GetLastNInPortal instead of CountByPortal
	lastMsgs, err := a.connector.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 1)
	if err != nil {
		log.Error().Err(err).Msg("Failed to check for existing messages in portal")
		// Continue anyway, but might prepend unnecessarily if check failed
	} else if len(lastMsgs) == 0 {
		log.Info().Msg("First backwards fetch for portal (message count is 0), attempting to prepend initial messages.")
		isInitialPopulation = true

		// Retrieve and assert portal metadata
		if portal.Metadata != nil {
			if meta, ok := portal.Metadata.(*PortalMetadata); ok && meta != nil {
				log.Info().Msg("Successfully retrieved PortalMetadata")

				// 1. Create Welcome Message
				// No need to explicitly set Sender for bot, IsFromMe: false should suffice
				welcomeText := fmt.Sprintf("You matched with %s on Tinder! 🔥", meta.InitialName)
				welcomeContent := &event.MessageEventContent{
					MsgType: event.MsgText,
					Body:    welcomeText,
				}
				welcomeMsg := &bridgev2.BackfillMessage{
					ID:        networkid.MessageID("initial_welcome_" + portal.ID),
					Timestamp: meta.MatchCreatedDate,
					Sender: bridgev2.EventSender{
						// Sender field omitted, bridge should handle bot attribution
						IsFromMe: false,
					},
					ConvertedMessage: &bridgev2.ConvertedMessage{
						Parts: []*bridgev2.ConvertedMessagePart{{
							Type:    event.EventMessage,
							Content: welcomeContent,
						}},
					},
				}
				initialMessages = append(initialMessages, welcomeMsg)

				// 2. Create Photo Message (if avatar exists)
				if portal.AvatarMXC != "" {
					photoContent := &event.MessageEventContent{
						MsgType: event.MsgImage,
						Body:    fmt.Sprintf("%s's profile photo", meta.InitialName),
						URL:     portal.AvatarMXC,
						Info: &event.FileInfo{
							MimeType: "image/jpeg", // Assuming jpeg, could enhance later
						},
						FileName: fmt.Sprintf("%s_avatar.jpg", meta.GhostUserID), // Use ghost ID for filename
					}
					photoMsg := &bridgev2.BackfillMessage{
						ID:        networkid.MessageID("initial_photo_" + portal.ID),
						Timestamp: meta.MatchCreatedDate.Add(1 * time.Second),
						Sender: bridgev2.EventSender{
							Sender:   meta.GhostUserID, // Ghost sends their own photo
							IsFromMe: false,            // It's not from the bridged user
						},
						ConvertedMessage: &bridgev2.ConvertedMessage{
							Parts: []*bridgev2.ConvertedMessagePart{{
								Type:    event.EventMessage,
								Content: photoContent,
							}},
						},
					}
					initialMessages = append(initialMessages, photoMsg)
					log.Info().Int("count", len(initialMessages)).Msg("Prepended initial welcome/photo messages.")
				} else {
					log.Info().Msg("No InitialAvatarMXC in metadata, skipping initial photo message.")
				}
			} else {
				log.Warn().Msg("Portal metadata is nil or not of type PortalMetadata, cannot prepend initial messages.")
			}
		}
	}
	// }

	// --- Fetch actual messages from Tinder API ---
	messages, err := a.tClient.GetMessages(matchID, portal.Bridge.Config.Backfill.Queue.BatchSize)
	if err != nil {
		// If we failed to get messages from Tinder API
		if len(initialMessages) > 0 {
			// If we already created initial messages, return those and log the Tinder API error
			log.Warn().Err(err).Msg("Failed to fetch messages from Tinder, but returning prepended initial messages.")
			return &bridgev2.FetchMessagesResponse{
				Messages:                initialMessages,
				HasMore:                 false,
				Forward:                 fetchParams.Forward,
				MarkRead:                !fetchParams.Forward && !isInitialPopulation,
				AggressiveDeduplication: true,
			}, nil
		}
		// If API failed AND we have no initial messages, return the original error
		log.Error().Err(err).Msg("Failed to fetch messages from Tinder and no initial messages were prepended.")
		return nil, fmt.Errorf("failed to fetch messages for backfill: %w", err)
	}

	log.Debug().Int("message_count", len(messages)).Msg("Fetched messages for backfill")

	// Convert Tinder messages to BackfillMessage format
	backfillMsgs := make([]*bridgev2.BackfillMessage, 0, len(messages)+len(initialMessages))

	// Prepend initial messages if any were created
	backfillMsgs = append(backfillMsgs, initialMessages...)

	for _, msg := range messages {
		// Convert Tinder timestamp to time.Time
		timestamp := time.UnixMilli(int64(msg.Timestamp))

		// Create sender info
		messageSenderID := networkid.UserID(msg.From)

		// Determine if message is from the bridged user
		isFromMe := a.IsThisUser(ctx, messageSenderID)

		sender := bridgev2.EventSender{
			Sender:   messageSenderID,
			IsFromMe: isFromMe,
		}

		if isFromMe {
			sender.SenderLogin = a.userLogin.ID
		}

		// Create message content
		msgContent := &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    msg.Message,
		}

		// Create message part
		msgPart := &bridgev2.ConvertedMessagePart{
			Type:    event.EventMessage,
			Content: msgContent,
		}

		// Create BackfillMessage
		backfillMsg := &bridgev2.BackfillMessage{
			ID:        networkid.MessageID(msg.ID),
			Timestamp: timestamp,
			Sender:    sender,
			ConvertedMessage: &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{msgPart},
			},
		}

		backfillMsgs = append(backfillMsgs, backfillMsg)
	}

	// Sort messages by timestamp in ascending order if going forward, descending if going backward
	sort.Slice(backfillMsgs, func(i, j int) bool {
		if fetchParams.Forward {
			return backfillMsgs[i].Timestamp.Before(backfillMsgs[j].Timestamp)
		}
		return backfillMsgs[i].Timestamp.After(backfillMsgs[j].Timestamp)
	})

	// *** This is the main return path after successfully fetching and processing messages ***
	return &bridgev2.FetchMessagesResponse{
		Messages:                backfillMsgs,
		HasMore:                 false, // Tinder API doesn't reliably support pagination for this yet
		Forward:                 fetchParams.Forward,
		MarkRead:                !fetchParams.Forward && !isInitialPopulation,
		AggressiveDeduplication: true,
	}, nil
}
