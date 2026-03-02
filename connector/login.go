package connector

import (
	"context"

	// "database/sql" // Already removed by previous edit
	// "errors"       // Already removed by previous edit
	"fmt"
	"regexp"  // Add regexp import
	"strings" // Add strings import

	// Needed for UUID generation example
	"github.com/google/uuid" // For generating stable LoginID
	"github.com/rs/zerolog"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"  // Needed for database.UserLogin
	"maunium.net/go/mautrix/bridgev2/networkid" // Needed for networkid.UserLoginID
	"maunium.net/go/mautrix/bridgev2/status"    // Needed for status.RemoteProfile

	tinder "github.com/dvcrn/go-tinder"
)

// Step IDs for the Tinder token flow
const (
	LoginStepIDTinderCurl         = "tinder-curl-input"          // Renamed first step
	LoginStepIDTinderRefreshToken = "tinder-refresh-token-input" // Added step for refresh token
	LoginStepIDTinderComplete     = "tinder-complete"
)

// Ensure TinderLoginProcess implements LoginProcessUserInput
var _ bridgev2.LoginProcessUserInput = (*TinderLoginProcess)(nil)

// Helper function to extract tokens from curl command
func extractTokenAndDeviceID(input string) (string, string) {
	// Remove line breaks and extra spaces
	input = strings.ReplaceAll(input, "\n", " ")
	input = strings.ReplaceAll(input, "\r", " ")
	input = regexp.MustCompile(`\s+`).ReplaceAllString(input, " ")

	// Extract x-auth-token
	authTokenRegex := regexp.MustCompile(`X-Auth-Token: ([a-zA-Z0-9-]+)`)
	authTokenMatch := authTokenRegex.FindStringSubmatch(input)
	authToken := ""
	if len(authTokenMatch) > 1 {
		authToken = authTokenMatch[1]
	}

	// Extract persistent-device-id
	deviceIDRegex := regexp.MustCompile(`persistent-device-id: ([a-zA-Z0-9-]+)`)
	deviceIDMatch := deviceIDRegex.FindStringSubmatch(input)
	deviceID := ""
	if len(deviceIDMatch) > 1 {
		deviceID = deviceIDMatch[1]
	}

	return authToken, deviceID
}

// TinderLoginProcess handles the token-based login flow.
type TinderLoginProcess struct {
	User *bridgev2.User   // The Matrix user performing the login.
	Main *TinderConnector // Reference to the main connector.
	Log  zerolog.Logger   // Logger specific to this login process.
	// Store extracted tokens between steps
	authToken string
	deviceID  string
}

// NewTinderLoginProcess creates a new login process instance.
func NewTinderLoginProcess(ctx context.Context, main *TinderConnector, user *bridgev2.User) *TinderLoginProcess {
	log := main.Bridge.Log.With().
		Str("component", "login_process").
		Str("user_mxid", string(user.MXID)).
		Str("flow_id", LoginFlowIDToken). // Use Flow ID defined in connector.go
		Logger()

	return &TinderLoginProcess{
		User: user,
		Main: main,
		Log:  log,
	}
}

// Start returns the initial step asking for the curl command.
func (p *TinderLoginProcess) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	p.Log.Debug().Msg("Starting Tinder curl login flow")
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       LoginStepIDTinderCurl, // Start with curl input
		Instructions: "Please paste a valid curl command (e.g., from browser developer tools) to extract necessary headers.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{Type: bridgev2.LoginInputFieldTypePassword, ID: "curl_command", Name: "Curl Command"}, // Use password type to hide input
			},
		},
	}, nil
}

// SubmitUserInput handles inputs for different steps.
func (p *TinderLoginProcess) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	p.Log.Debug().Interface("input", input).Msg("SubmitUserInput called")

	stepID := p.CurrentStepID() // Need a way to get the current step ID (Assume it's managed by the framework or stored in the process struct)

	switch stepID {
	case LoginStepIDTinderCurl:
		return p.handleCurlInput(ctx, input)
	case LoginStepIDTinderRefreshToken:
		return p.handleRefreshTokenInput(ctx, input)
	default:
		return nil, fmt.Errorf("unknown login step ID: %s", stepID)
	}
}

// handleCurlInput extracts tokens from curl command and proceeds to refresh token step.
func (p *TinderLoginProcess) handleCurlInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	curlCommand, ok := input["curl_command"]
	if !ok || curlCommand == "" {
		return nil, fmt.Errorf("missing curl command input")
	}

	// Extract tokens
	authToken, deviceID := extractTokenAndDeviceID(curlCommand)
	if authToken == "" || deviceID == "" {
		p.Log.Warn().Str("curl_input", curlCommand).Msg("Could not extract X-Auth-Token or persistent-device-id from curl command")
		// Return to the same step with an error message
		return &bridgev2.LoginStep{
			Type:         bridgev2.LoginStepTypeUserInput,
			StepID:       LoginStepIDTinderCurl,
			Instructions: "Extraction failed. Please paste a valid curl command containing 'X-Auth-Token: ...' and 'persistent-device-id: ...' headers.",
			UserInputParams: &bridgev2.LoginUserInputParams{
				Fields: []bridgev2.LoginInputDataField{
					{Type: bridgev2.LoginInputFieldTypePassword, ID: "curl_command", Name: "Curl Command"},
				},
			},
		}, fmt.Errorf("could not extract required headers from curl command")
	}

	p.Log.Info().Msg("Extracted Auth Token and Device ID from curl command")
	p.authToken = authToken // Store for next step
	p.deviceID = deviceID

	// Proceed to ask for refresh token
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       LoginStepIDTinderRefreshToken,
		Instructions: "Tokens extracted successfully. Now, please enter your Tinder Refresh Token.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{Type: bridgev2.LoginInputFieldTypeToken, ID: "tinder_refresh_token", Name: "Tinder Refresh Token"},
			},
		},
	}, nil
}

// handleRefreshTokenInput validates refresh token and completes login.
func (p *TinderLoginProcess) handleRefreshTokenInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	refreshToken, ok := input["tinder_refresh_token"]
	if !ok || refreshToken == "" {
		return nil, fmt.Errorf("missing tinder_refresh_token input")
	}

	p.Log.Debug().
		Bool("has_refresh_token", refreshToken != "").
		Bool("has_access_token", p.authToken != "").
		Bool("has_device_id", p.deviceID != "").
		Msg("Received Tinder refresh token")

	// Use stored authToken and deviceID with the new refresh token for validation
	tempClient := tinder.NewClient(p.authToken, p.deviceID)
	refreshResponse, err := tempClient.RefreshToken(refreshToken)
	if err != nil || refreshResponse == nil || refreshResponse.RefreshToken == "" {
		p.Log.Warn().Err(err).Msg("Tinder refresh token validation failed")
		return &bridgev2.LoginStep{
			Type:            bridgev2.LoginStepTypeUserInput,
			StepID:          LoginStepIDTinderRefreshToken,
			Instructions:    fmt.Sprintf("Refresh token validation failed: %v. Please enter a valid refresh token.", err),
			UserInputParams: &bridgev2.LoginUserInputParams{Fields: []bridgev2.LoginInputDataField{{Type: bridgev2.LoginInputFieldTypeToken, ID: "tinder_refresh_token", Name: "Tinder Refresh Token"}}},
		}, fmt.Errorf("invalid Tinder Refresh Token: %w", err)
	}
	p.Log.Info().Msg("Tinder Refresh Token validated successfully")

	// --- Credentials Validated - Prepare data for NewLogin ---
	// Fetch user profile again to get ID and Name consistently
	tinderUser, err := tempClient.GetOwnUser()
	if err != nil {
		// This shouldn't happen if previous validation passed, but handle defensively
		p.Log.Error().Err(err).Msg("Failed to get Tinder user profile after successful token validation")
		p.Log.Debug().Msgf("Error details when fetching user profile: %v", err)

		return &bridgev2.LoginStep{
			Type:            bridgev2.LoginStepTypeUserInput,
			StepID:          LoginStepIDTinderRefreshToken,
			Instructions:    fmt.Sprintf("Failed to fetch user profile after refresh token validation: %v.", err),
			UserInputParams: &bridgev2.LoginUserInputParams{Fields: []bridgev2.LoginInputDataField{{Type: bridgev2.LoginInputFieldTypeToken, ID: "tinder_refresh_token", Name: "Tinder Refresh Token"}}},
		}, fmt.Errorf("internal error: failed to fetch user profile: %w", err)
	}

	metadata := &TinderLoginMetadata{
		AccessToken:  p.authToken,  // Use stored token
		RefreshToken: refreshToken, // Use validated refresh token
		DeviceID:     p.deviceID,   // Use stored device ID
		TinderUserID: tinderUser.ID,
	}

	// Generate stable UserLoginID
	// IMPORTANT: Replace this with a *unique* and *persistent* UUID for this bridge instance.
	// You can generate one using `uuidgen` on Linux/macOS.
	namespace := uuid.MustParse("0195ffb8-18f0-7474-bef9-8e012e050a9d") // <-- Replace with your generated UUID
	loginIDStr := uuid.NewSHA1(namespace, []byte(tinderUser.ID)).String()
	loginID := networkid.UserLoginID(loginIDStr)
	p.Log = p.Log.With().Str("login_id", string(loginID)).Logger()

	// Prepare the database.UserLogin data
	dbUserLogin := &database.UserLogin{
		ID:         loginID,
		RemoteName: tinderUser.Name,
		RemoteProfile: status.RemoteProfile{
			Name: tinderUser.Name,
		},
		Metadata: metadata,
	}

	// Add detailed logging before the call
	p.Log.Debug().Interface("db_user_login_data", dbUserLogin).Msg("Attempting User.NewLogin with DeleteOnConflict=true")

	// Call NewLogin with DeleteOnConflict: true to handle insert or replace
	ul, err := p.User.NewLogin(ctx, dbUserLogin, &bridgev2.NewLoginParams{DeleteOnConflict: true})

	// Add detailed logging after the call
	if err != nil {
		// Log the error *and* the returned (potentially nil) ul object
		p.Log.Error().Err(err).Interface("returned_ul_on_error", ul).Msg("User.NewLogin failed")
		// If this fails with UNIQUE constraint, it points to an issue in NewLogin or DB state
		p.Log.Error().Err(err).Msg("Failed to create/update user login entry in database using NewLogin(DeleteOnConflict=true)")
		return nil, fmt.Errorf("failed to save user login: %w", err)
	}
	p.Log.Info().Interface("returned_ul_on_success", ul).Msg("User.NewLogin successful")
	p.Log.Info().Msg("Successfully created/updated user login in database")

	// Return Complete Step
	// LoginCompleteParams expects *bridgev2.UserLogin, which ul now is.
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       LoginStepIDTinderComplete,
		Instructions: fmt.Sprintf("Successfully logged into Tinder as '%s'", tinderUser.Name),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: ul.ID,
			UserLogin:   ul,
		},
	}, nil
}

// CurrentStepID needs to be implemented. How does the framework track the current step?
// Option 1: Store it in the TinderLoginProcess struct
// Option 2: The framework provides it (unlikely based on interface)
// Assuming Option 1 for now - needs modification
func (p *TinderLoginProcess) CurrentStepID() string {
	// Placeholder - Needs proper implementation
	// Maybe check which fields (authToken, deviceID) are set?
	if p.authToken == "" || p.deviceID == "" {
		return LoginStepIDTinderCurl
	}
	return LoginStepIDTinderRefreshToken
}

// Cancel handles cancellation.
func (p *TinderLoginProcess) Cancel() {
	p.Log.Info().Msg("Login process cancelled")
}

// Logout (optional implementation if needed by LoginProcess interface)
func (p *TinderLoginProcess) Logout(ctx context.Context) error {
	p.Log.Info().Msg("Logout called (no specific Tinder action implemented yet)")
	return nil
}
