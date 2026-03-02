package connector

import (
	"context"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gorilla/websocket"
	"maunium.net/go/mautrix/bridgev2/status"
	// Remove unused imports
	// "github.com/rs/zerolog"
	// "maunium.net/go/mautrix/bridgev2"
	// tinder "github.com/dvcrn/go-tinder"
	// pb "github.com/dvcrn/go-tinder/pb"
)

// Connect starts the background goroutines and triggers initial sync.
func (a *TinderClientAdapter) Connect(ctx context.Context) {
	a.log.Info().Msg("Initiating Tinder connection and sync...")
	go a.connectAndListenGoroutine()     // Start connection manager goroutine
	go a.handleIncomingEventsGoroutine() // Start event processor goroutine
	go a.initialSync(ctx)                // Start initial data sync in a separate goroutine
	go a.startPeriodicSyncAndRefresh()   // Start periodic sync and refresh in a separate goroutine
}

// connectAndListenGoroutine manages the websocket connection lifecycle.
func (a *TinderClientAdapter) connectAndListenGoroutine() {
	const maxRetries = 5
	retryCount := 0
	retryTimeout := time.NewTimer(time.Minute * 10) // Timer to reset retry count
	defer retryTimeout.Stop()

	go func() { // Goroutine to reset retry count periodically
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-retryTimeout.C:
				log := a.log.With().Str("subcomponent", "ws-retry-reset").Logger()
				log.Debug().Int("old_retry_count", retryCount).Msg("Resetting retry count after timeout")
				retryCount = 0
			}
		}
	}()

DialWebsocket: // Label for goto retry
	a.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting})
	log := a.log.With().Str("subcomponent", "ws-connect").Logger()

	// Check if main context is already cancelled before attempting connection
	select {
	case <-a.ctx.Done():
		log.Info().Msg("Main context cancelled, stopping connection attempts.")
		return
	default:
	}

	log.Info().Msg("Attempting to get WebSocket token...")
	wsToken, err := a.tClient.GetWsToken()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get WebSocket token")
		// Capture error to Sentry with context
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("error_type", "websocket_token_failure")
			scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
			scope.SetContext("connection", map[string]interface{}{
				"retry_count": retryCount,
				"user_id":     a.userLogin.ID,
			})
			sentry.CaptureException(err)
		})
		// TODO: Maybe retry GetWsToken with backoff?
		// For now, wait and retry the whole connection process
		time.Sleep(10 * time.Second)
		retryTimeout.Reset(time.Minute * 10)
		goto DialWebsocket
	}
	log.Info().Msg("Got WebSocket token, attempting connection...")

	// Create a context specifically for this connection attempt
	connectCtx, connectCancel := context.WithCancel(a.ctx)

	a.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected, RemoteName: a.userLogin.RemoteName})
	err = a.tClient.ConnectWebsocket(connectCtx, wsToken, a.eventChan)
	connectCancel() // Cancel context for this specific connection attempt

	if err == nil {
		// ConnectWebsocket likely exited because the context was cancelled (normal shutdown)
		log.Info().Msg("ConnectWebsocket returned nil error, likely due to context cancellation (shutdown).")
		a.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: status.BridgeStateErrorCode("tinder_ws_closed")})
		return // Exit goroutine normally
	}

	// Handle errors
	if websocket.IsUnexpectedCloseError(err) {
		log.Warn().Err(err).Msg("WebSocket closed unexpectedly, will refresh token and retry...")
		// Capture unexpected close to Sentry
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("error_type", "websocket_unexpected_close")
			scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
			scope.SetContext("connection", map[string]interface{}{
				"retry_count": retryCount,
			})
			sentry.CaptureException(err)
		})
		a.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: status.BridgeStateErrorCode("tinder_ws_unexpected_closed")})
		// Refresh token logic implicitly handled by retrying GetWsToken above
		retryTimeout.Reset(time.Minute * 10) // Reset timer as we are actively retrying
		goto DialWebsocket
	}

	// Handle other errors
	retryCount++
	log.Error().Err(err).Int("retry_count", retryCount).Msg("ConnectWebsocket failed with unknown error")
	// Capture unknown websocket errors
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("error_type", "websocket_unknown_error")
		scope.SetUser(sentry.User{ID: string(a.userLogin.ID)})
		scope.SetContext("connection", map[string]interface{}{
			"retry_count": retryCount,
			"max_retries": maxRetries,
		})
		sentry.CaptureException(err)
	})
	if retryCount > maxRetries {
		log.Error().Msg("Max retries reached, giving up on WebSocket connection.")
		// Capture max retries reached event
		sentry.CaptureMessage("WebSocket max retries reached, connection abandoned")
		// TODO: Mark user as disconnected? Notify user?
		return // Stop trying
	}

	log.Info().Dur("wait_duration", 5*time.Second).Msg("Waiting before retry...")
	time.Sleep(5 * time.Second)
	retryTimeout.Reset(time.Minute * 10) // Reset timer as we are actively retrying
	goto DialWebsocket
}
