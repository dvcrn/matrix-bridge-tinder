package connector

import (
	"context"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	tinder "github.com/dvcrn/go-tinder"
	pb "github.com/dvcrn/go-tinder/pb"
	// ptr "go.mau.fi/util/ptr" // Keep commented if causing persistent errors
)

// Ensure TinderClientAdapter implements NetworkAPI
var _ bridgev2.NetworkAPI = (*TinderClientAdapter)(nil)

// TinderClientAdapter implements bridgev2.NetworkAPI for a specific logged-in user.
type TinderClientAdapter struct {
	log       zerolog.Logger
	connector *TinderConnector
	userLogin *bridgev2.UserLogin
	tClient   *tinder.Client
	meta      *TinderLoginMetadata
	ctx       context.Context
	cancel    context.CancelFunc
	wsConn    *websocket.Conn     // Keep for potential direct writes?
	eventChan chan *pb.ClientData // Added event channel

	locks map[networkid.PortalID]*sync.Mutex
}

// NewTinderClientAdapter creates a new NetworkAPI instance for a user.
func NewTinderClientAdapter(
	connector *TinderConnector,
	userLogin *bridgev2.UserLogin,
	tinderClient *tinder.Client,
	metadata *TinderLoginMetadata,
) *TinderClientAdapter {
	log := connector.Bridge.Log.With().
		Str("component", "network_api").
		Str("user_login_id", string(userLogin.ID)).
		Logger()
	ctx, cancel := context.WithCancel(context.Background())

	return &TinderClientAdapter{
		log:       log,
		connector: connector,
		userLogin: userLogin,
		tClient:   tinderClient,
		meta:      metadata,
		ctx:       ctx,
		cancel:    cancel,
		eventChan: make(chan *pb.ClientData), // Initialize channel
		locks:     make(map[networkid.PortalID]*sync.Mutex),
	}
}

// helpers for lock/unlock
func (a *TinderClientAdapter) lock(portalID networkid.PortalID) {
	if _, exists := a.locks[portalID]; !exists {
		a.locks[portalID] = &sync.Mutex{}
	}
	a.locks[portalID].Lock()
}

func (a *TinderClientAdapter) unlock(portalID networkid.PortalID) {
	a.locks[portalID].Unlock()
}

// --- bridgev2.NetworkAPI Implementation ---

// Connect starts the background goroutines and triggers initial sync.
// Moved to connection.go

// initialSync performs the first data fetch after connection.
// Moved to handletinder.go

// connectAndListenGoroutine manages the websocket connection lifecycle.
// Moved to connection.go

// handleIncomingEventsGoroutine triggers sync on Nudge.
// Moved to handletinder.go

// syncTinderUpdatesSince fetches and processes updates since a given time.
// Moved to handletinder.go

// handleIncomingTinderEvent is NO LONGER the primary dispatcher.
// Moved to handletinder.go (temporarily)

// handleIncomingMessage bridges a Tinder message to Matrix.
// Moved to handletinder.go

// handleIncomingMatch bridges a new Tinder match to Matrix.
// Moved to handletinder.go

// backfillMessages fetches recent messages for a match and bridges them.
// Moved to handletinder.go

// Disconnect closes the connection to Tinder.
func (a *TinderClientAdapter) Disconnect() {
	a.log.Info().Msg("Disconnect called, cancelling context...")
	a.cancel() // Cancel the context to signal goroutines to stop
	// WebSocket connection closing is handled by the listener goroutine's defer

	// a.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.State, Error: status.BridgeStateErrorCode("tinder_ws_unexpected_closed")})
}

// LogoutRemote performs logout actions on the Tinder side if possible.
func (a *TinderClientAdapter) LogoutRemote(ctx context.Context) {
	a.log.Info().Msg("LogoutRemote called (no specific Tinder action implemented yet)")
	// TODO: Call Tinder API to invalidate token/session if possible
}

// IsThisUser checks if a network user ID corresponds to this logged-in user.
func (a *TinderClientAdapter) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	fmt.Println("is this user called with ", string(userID))
	return string(userID) == string(a.meta.TinderUserID)
}

// IsLoggedIn checks the connection status.
func (a *TinderClientAdapter) IsLoggedIn() bool {
	// TODO: Implement actual check based on websocket connection status or recent activity
	return true // Placeholder
}

// GetUserInfo fetches info about a remote user (Tinder profile).
// Moved to handlematrix.go

// GetChatInfo fetches info about a remote chat (Tinder match).
// Moved to handlematrix.go

// GetCapabilities returns features supported for a specific chat/portal.
// Moved to handlematrix.go

// HandleMatrixMessage sends a message from Matrix to Tinder.
// Moved to handlematrix.go
