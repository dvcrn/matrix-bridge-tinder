package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	tinder "github.com/dvcrn/go-tinder"
)

// Define a custom Flow ID for token login
const LoginFlowIDToken = "m.login.token"

type TinderConnector struct {
	// REMOVED: bridgev2.NetworkConnector // Embed the correct base connector

	Log    zerolog.Logger
	Config TinderConfig // Add Config field

	DB     *database.Database
	Bridge *bridgev2.Bridge

	M *mxmain.BridgeMain
}

var _ bridgev2.NetworkConnector = (*TinderConnector)(nil)

func (tc *TinderConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		UserLogin: func() any {
			return &TinderLoginMetadata{}
		},
		Ghost: func() any {
			return &TinderGhostMetadata{}
		},
		Portal: func() any {
			return &PortalMetadata{}
		},
		// Message, Reaction can be added if needed later
	}
}

func (tc *TinderConnector) Init(bridge *bridgev2.Bridge) {
	// Logging and DB must be accessed via tc.Bridge *after* this call
	tc.Bridge = bridge
	tc.DB = bridge.DB
	tc.Log = bridge.Log
	// Cannot log here as logger isn't available yet
	// tc.Log = bridge.Log.With().Str("component", "TinderConnector").Logger()

}

func (tc *TinderConnector) Start(ctx context.Context) error {
	tc.Log.Info().Msg("TinderConnector starting...")
	return nil
}

func (tc *TinderConnector) Stop(ctx context.Context) error {
	tc.Log.Info().Msg("TinderConnector stopping...")
	return nil
}

func (tc *TinderConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName: "Tinder",
		NetworkURL:  "",       // TODO: Add URL if applicable
		NetworkIcon: "",       // TODO: Add icon mxc uri if applicable
		NetworkID:   "tinder", // Unique ID for the network
		// BeeperBridgeType:     "tinder", // Optional: for Beeper integration
		DefaultPort:          29320, // TODO: Choose appropriate default port
		DefaultCommandPrefix: "!tinder",
	}
}

func (tc *TinderConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

func (tc *TinderConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{
		{
			ID:          LoginFlowIDToken,
			Name:        "Tinder API Token",
			Description: "Log in using your Tinder API token.",
		},
	}
}

func (tc *TinderConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	tc.Log.Info().Str("user_mxid", user.MXID.String()).Str("flowID", flowID).Msg("CreateLogin called")
	if flowID != LoginFlowIDToken {
		return nil, fmt.Errorf("unsupported login flow ID: %s", flowID)
	}
	// Return NewTinderLoginProcess
	return NewTinderLoginProcess(ctx, tc, user), nil // Pass connector and user
}

func (tc *TinderConnector) LoadUserLogin(ctx context.Context, userLogin *bridgev2.UserLogin) error {
	log := tc.Bridge.Log.With().Str("user_login_id", string(userLogin.ID)).Logger()
	log.Info().Msg("LoadUserLogin called")

	// Retrieve the stored metadata via type assertion
	meta, ok := userLogin.Metadata.(*TinderLoginMetadata)
	if !ok || meta == nil {
		log.Error().Msg("Failed to assert UserLogin.Metadata to *TinderLoginMetadata or metadata is nil")
		// Depending on desired behavior, you might want to prevent login loading here.
		// For now, we'll proceed with potentially empty/default credentials, but log an error.
		// Alternatively: return fmt.Errorf("failed to load Tinder login metadata")
		meta = &TinderLoginMetadata{} // Use empty struct to avoid nil pointer dereference below
	}

	// Validate if essential tokens are present (optional but recommended)
	if meta.AccessToken == "" || meta.DeviceID == "" {
		log.Warn().Msg("TinderLoginMetadata is missing AccessToken or DeviceID")
		// Handle this case as appropriate - maybe require re-login or return error
	}

	// Initialize the go-tinder client with credentials from metadata
	log.Debug().
		Bool("has_access_token", meta.AccessToken != "").
		Bool("has_refresh_token", meta.RefreshToken != "").
		Bool("has_device_id", meta.DeviceID != "").
		Msg("Initializing go-tinder client with stored credentials")

	tinderClient := tinder.NewClient(meta.AccessToken, meta.DeviceID)

	tokenRes, err := tinderClient.RefreshToken(meta.RefreshToken)
	if err != nil || tokenRes == nil || tokenRes.RefreshToken == "" {
		log.Error().Err(err).Msg("Failed to refresh token")
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	tinderUser, err := tinderClient.GetOwnUser()
	if err != nil {
		// This shouldn't happen if previous validation passed, but handle defensively
		tc.Log.Error().Err(err).Msg("Failed to get Tinder user profile after successful token validation")
		return fmt.Errorf("internal error: failed to fetch user profile: %w", err)
	}

	// Update metadata with new token information
	meta.RefreshToken = tokenRes.RefreshToken
	meta.AccessToken = tokenRes.AuthToken
	if tokenRes.AuthTokenTtl != nil {
		meta.AccessTokenExpires = time.Now().Add(time.Millisecond * time.Duration(tokenRes.AuthTokenTtl.Value))
	}

	userLogin.Metadata = meta

	// Save the updated metadata
	if err := tc.DB.UserLogin.Update(ctx, userLogin.UserLogin); err != nil {
		log.Error().Err(err).Msg("Failed to update user login metadata")
		return fmt.Errorf("failed to update user login metadata: %w", err)
	}

	meta.TinderUserID = tinderUser.ID
	meta.OwnuserProfile = tinderUser

	// Create the NetworkAPI adapter, passing the actual metadata
	log.Debug().Msg("Creating TinderClientAdapter")
	adapter := NewTinderClientAdapter(tc, userLogin, tinderClient, meta) // Pass actual meta

	// Store the adapter in the UserLogin object
	userLogin.Client = adapter
	log.Info().Msg("TinderClientAdapter created and stored")

	return nil // Returning success for now, despite potentially empty credentials
}

// GetBridgeInfoVersion implements bridgev2.NetworkConnector
func (tc *TinderConnector) GetBridgeInfoVersion() (int, int) {
	// TODO: Determine appropriate version numbers based on bridge features/API compatibility.
	return 1, 0 // Example major and minor version
}
