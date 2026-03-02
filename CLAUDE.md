## BridgeV2 Lifecycle & Core Operations (Tinder Bridge How-To Guide)

1.  **Initialization & Connection:**
    *   *Lifecycle:* Bridge framework initializes connector, handles user login (`connector.LoadUserLogin`), creates `UserLogin` and `TinderClientAdapter`, calls `Connect()`.
    *   ***How To:*** Implement `connector.LoadUserLogin` to initialize your `tinder.Client`, create your `TinderClientAdapter` (`NetworkAPI`), and store it on `userLogin.Client`. Implement `adapter.Connect()` to send `status.StateConnecting`, start remote connections (WebSocket), event handlers, and initial/periodic syncs. Send `status.StateConnected` upon successful connection.

2.  **Room Creation (`simplevent.ChatResync`):**
    *   *Lifecycle:* Triggered by `handleIncomingMatch` when a new match is detected (`portal.MXID == ""`). Queues `ChatResync`, framework finds no existing room, calls `GetChatInfo` to get details, creates Matrix room, ghost, sets initial state based on `GetChatInfo` result.
    *   ***How To: Create a Room:***
        *   **Identify Need:** Check if a `bridgev2.Portal` exists for the remote chat ID (e.g., `tinder.Match.ID`). If `portal.MXID == ""`, creation is needed.
        *   **Prepare Data:** Save essential linking info (`OtherUserID`, `InitialName`, `MatchCreatedDate`, full `TinderMatch`) to `portal.Metadata`. Update and save the `portal`.
        *   **Queue Event:** Call `Bridge.QueueRemoteEvent(login, &simplevent.ChatResync{...})`.
        *   **Essential Fields:** Set `EventMeta.PortalKey`, `EventMeta.CreatePortal = true`. **Crucially, `ChatInfo` should be `nil` here.** The framework will call your `GetChatInfo` implementation separately.
        *   *(Optional)*: Implement `EventMeta.PostHandleFunc` for actions after room creation (e.g., ensuring puppets joined).

3.  **Providing Room Info (`GetChatInfo`):**
    *   *Lifecycle:* Framework calls this method when it needs the desired state for a Matrix room, particularly during the processing of a `ChatResync` with `CreatePortal=true`, but potentially at other times.
    *   ***How To: Define Room State:***
        *   **Implement API Method:** Implement `GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error)` on your `TinderClientAdapter`.
        *   **Fetch Remote Info:** Get the `OtherUserID` from the portal, fetch the corresponding Tinder user profile (`tClient.GetUser`).
        *   **Construct ChatInfo:** Create and return a complete `*bridgev2.ChatInfo` struct:
            *   Set `Name`, `Topic`, `Type` (e.g., `database.RoomTypeDM`).
            *   Create `Members` (`*bridgev2.ChatMemberList`) including `OtherUserID`, `MemberMap` (with the ghost ID and the bridged user's Tinder ID), and standard DM `PowerLevels`.
            *   Create `Avatar` (`*bridgev2.Avatar`) by finding the appropriate photo URL and ID from the fetched profile. Set `Avatar.ID` and provide the `Avatar.Get` function closure to fetch the image bytes via HTTP when the framework requires it.

4.  **Room State Updates (e.g., Locking/Unlocking) (`simplevent.ChatInfoChange`):**
    *   *Lifecycle:* Triggered when only specific state changes are needed (e.g., making room read-only on unmatch). Queues `ChatInfoChange`, framework sends updated state event to Matrix.
    *   ***How To: Update Room State:***
        *   **Identify Need:** Determine which state needs changing (e.g., Power Levels for locking in `handleTinderUnmatch`).
        *   **Prepare Data:** Create a `*bridgev2.PowerLevelOverrides` struct with the desired changes. Create a `*simplevent.ChatInfoChange` struct.
        *   **Populate Event:** Set `EventMeta.Type = bridgev2.RemoteEventChatInfoChange`, `EventMeta.PortalKey`. Set `Changes.PowerLevels` to your prepared overrides struct.
        *   **Queue Event:** Call `Bridge.QueueRemoteEvent(login, yourChatInfoChangeEvent)`.

5.  **Incoming Messages (Remote -> Matrix):**
    *   *Lifecycle:* Remote event triggers message handler (`handleIncomingMessage`). Handler wraps message, queues it. Framework calls `ConvertMessage`, then sends converted message to Matrix.
    *   ***How To: Handle Incoming Messages:***
        *   **Implement Handler:** Create `handleIncomingMessage(*tinder.Message)` triggered by your sync logic.
        *   **Wrap Message:** Create `TinderRemoteMessage` struct implementing `bridgev2.RemoteMessage`. Embed `simplevent.EventMeta`, store native `*tinder.Message`, `login`, `Sender` (network ID), `IsFromMe`. Implement `GetPortalKey()` (returns `networkid.PortalKey{ID: networkid.PortalID(Msg.MatchID)}`).
        *   **Queue Event:** Call `Bridge.QueueRemoteEvent(login, yourTinderRemoteMessage)`.
        *   **Implement Conversion:** Implement `TinderRemoteMessage.ConvertMessage()`. Return `*bridgev2.ConvertedMessage` containing `Parts` (usually one `*bridgev2.ConvertedMessagePart` with `Type: event.EventMessage` and `Content: &event.MessageEventContent{MsgType: event.MsgText, Body: msg.Message}}`).

6.  **Bot Messages (Bridge -> Matrix):**
    *   *Lifecycle:* Bridge needs to send notification/status messages directly to Matrix rooms (e.g., unmatch notifications, reinstatement alerts).
    *   ***How To: Send Bot Messages:***
        *   **Create Message Content:** Create `&event.MessageEventContent` with appropriate `MsgType` (usually `event.MsgNotice` for notifications) and `Body`.
        *   **Send via Bot:** Call `Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Parsed: content}, nil)`.
        *   **Error Handling:** Handle errors gracefully - notification failures shouldn't stop critical operations.
        *   **Example:** See unmatch/reinstatement notifications in `handleTinderUnmatch` and `handleUserReinstatement`.

7.  **Outgoing Messages (Matrix -> Remote):**
    *   *Lifecycle:* User sends message in Matrix room. Framework calls `HandleMatrixMessage`. Adapter sends message to remote API, returns remote message details. Framework stores details.
    *   ***How To: Handle Outgoing Messages:***
        *   **Implement API Method:** Implement `HandleMatrixMessage(*bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error)` on your `TinderClientAdapter`.
        *   **Process:** Get `portal` and `event` from `MatrixMessage`. Parse `event.Content.AsMessage()`. Get `matchID = portal.ID`, `otherUserID = portal.OtherUserID`. Call `tClient.SendMessage`.
        *   **Return Response:** Return `*bridgev2.MatrixMessageResponse` containing `DB: &database.Message{...}` with the remote message `ID` (from `tClient.SendMessage` response), `SenderID` (bridged user's Tinder ID), `Timestamp`, etc.

8.  **Backfill (Populating History):**
    *   *Lifecycle:* Framework calls `FetchMessages`. Adapter fetches remote history, converts messages, returns them. Framework sends messages to Matrix.
    *   ***How To: Implement Backfill:***
        *   **Implement API Method:** Implement `FetchMessages(bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error)` on your adapter (ensure it implements `BackfillingNetworkAPI`).
        *   **Locking:** Use `a.lock(portal.ID)` / `defer a.unlock(portal.ID)`.
        *   **(Optional) Handle Initial State:** Check `DB.Message.GetLastNInPortal`. If count is 0, create initial welcome/photo messages as `*bridgev2.BackfillMessage` using data from `portal.Metadata` and `portal.AvatarMXC`.
        *   **Fetch Remote History:** Call `tClient.GetMessages(matchID, batchSize)`.
        *   **Convert Messages:** Convert each `*tinder.Message` (and any prepended initial messages) into a `*bridgev2.BackfillMessage`. Key fields: `ID` (remote `msg.ID`), `Timestamp`, `Sender` (`bridgev2.EventSender`), and `ConvertedMessage` (containing Matrix event `Parts`).
        *   **Return Response:** Return `*bridgev2.FetchMessagesResponse` containing the slice of `*bridgev2.BackfillMessage`s and `HasMore` (likely `false` for Tinder `GetMessages`).

9.  **Updating Bridge State:**
    *   *Lifecycle:* Called at various points (connect, disconnect, errors) to inform the user of the connection status.
    *   ***How To: Send State Updates:***
        *   Use `a.userLogin.BridgeState.Send(status.BridgeState{StateEvent: status.DesiredState, ...})`.
        *   Common `StateEvent` values (from `maunium.net/go/mautrix/bridgev2/status`):
            *   `StateStarting`: Bridge process starting.
            *   `StateUnconfigured`: Bridge needs configuration (e.g., login).
            *   `StateRunning`: General operational state (less specific than Connected).
            *   `StateConnecting`: Before attempting remote connection.
            *   `StateBackfilling`: When actively backfilling messages.
            *   `StateConnected`: After successful remote connection (e.g., WebSocket open).
            *   `StateTransientDisconnect`: On temporary connection loss (before retry).
            *   `StateBadCredentials`: Authentication failed.
            *   `StateUnknownError`: An unspecified error occurred.
            *   `StateLoggedOut`: User logged out or session invalidated.
            *   `StateBridgeUnreachable`: Homeserver cannot reach bridge (less common).
        *   Optionally include `Error`, `ErrorCode`, `Message`, `RemoteProfile` fields for more detail. # CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands
- Run bridge locally: `go run main.go -r registration.yaml -c config.yaml`
- Build binary: `go build -o matrix-tinder .`
- Build Docker image: `just docker-build` (requires `docker login` if pushing)
- Run tests: `go test ./...`
- Install dependencies: `go mod download`

## Testing
- Run all tests: `go test ./...`
- Run specific test file: `go test ./connector/login_test.go`
- Run with verbose output: `go test -v ./...`

## Code Style
- Go 1.23+ project using standard Go conventions
- Uses mautrix-go/bridgev2 framework for Matrix bridge functionality
- Uses github.com/dvcrn/go-tinder for Tinder API integration
- Error handling via standard Go error patterns
- Logging via zerolog with structured logging
- Database operations via bridgev2's built-in database abstraction

## Architecture Overview

### Core Components

1. **Main Entry Point** (`main.go`)
   - Initializes the bridge using `mxmain.BridgeMain`
   - Creates TinderConnector instance
   - Handles command-line flags and configuration

2. **TinderConnector** (`connector/connector.go`)
   - Implements `bridgev2.NetworkConnector` interface
   - Manages bridge lifecycle (Init, Start, Stop)
   - Handles login flows and user authentication
   - Creates TinderClientAdapter instances for logged-in users

3. **TinderClientAdapter** (`connector/client.go`)
   - Implements `bridgev2.NetworkAPI` interface
   - Manages WebSocket connection to Tinder
   - Handles incoming/outgoing messages
   - Manages room synchronization and backfilling

4. **Connection Management** (`connector/connection.go`)
   - WebSocket connection handling
   - Automatic reconnection logic
   - Event routing from Tinder to Matrix

5. **Message Handling**
   - `handlematrix.go`: Processes Matrix → Tinder messages
   - `handletinder.go`: Processes Tinder → Matrix events (matches, messages, unmatches)

6. **Data Types** (`connector/types.go`)
   - Bridge metadata structures
   - Message conversion types
   - Portal and ghost metadata

### Key Workflows

1. **User Login**
   - User provides Tinder refresh token
   - Bridge validates and refreshes token
   - Stores credentials in database
   - Establishes WebSocket connection

2. **Message Flow**
   - Tinder → Matrix: WebSocket events → handleIncomingMessage → QueueRemoteEvent
   - Matrix → Tinder: HandleMatrixMessage → tClient.SendMessage

3. **Room Management**
   - New matches create Matrix rooms via ChatResync events
   - GetChatInfo provides room metadata (name, avatar, members)
   - Unmatches lock rooms via ChatInfoChange events

4. **Backfilling**
   - FetchMessages retrieves message history
   - Prepends welcome/photo messages for new rooms
   - Converts Tinder messages to Matrix events

## Configuration

### Key Files
- `config.yaml`: Main bridge configuration (local, ignored; start from `config.example.yaml`)
- `registration.yaml`: Appservice registration (local, ignored; start from `registration.example.yaml`)
- `tinder.db`: SQLite database for bridge state (local, ignored)
- `connector/example-config.yaml`: Default network-specific config template

### Network-Specific Configuration
The bridge uses a two-layer configuration system:
1. **Main bridge config** (`config.yaml`): Contains standard bridge settings (homeserver, database, etc.)
2. **Network-specific config** (under `network:` section): Contains Tinder-specific settings

#### How Network Config Works
1. The `TinderConnector.GetConfig()` method returns:
   - Default config template from embedded `example-config.yaml`
   - Pointer to `TinderConfig` struct to unmarshal into
   - Config upgrader function to preserve existing values

2. The config upgrader (`upgradeConfig`) uses `helper.Copy()` to preserve user settings when the config is loaded/saved

3. To add new config options:
   - Add field to `TinderConfig` struct with yaml tag
   - Add default value to `example-config.yaml`
   - Add `helper.Copy()` line in `upgradeConfig` function

### Important Config Sections
- `network.sentry_dsn`: Sentry DSN for error tracking (optional)
- `bridge.command_prefix`: Command prefix for bot commands
- `database.uri`: Database connection string
- `homeserver.address`: Matrix homeserver URL
- `appservice.id`: Unique appservice ID
- `appservice.bot.username`: Bridge bot username
- `logging.min_level`: Log verbosity level

### Running Without Config Updates
Use `--no-update` flag to prevent the bridge from saving config changes to disk:
```bash
./matrix-tinder -r registration.yaml -c config.yaml --no-update
```

## Docker Deployment
- Uses multi-stage build for efficiency
- Requires GitHub token for private dependencies
- Runs via goreman process manager
- Data volume mounted at `/data`

## Development Tips
- The bridge uses bridgev2's portal locking mechanism - always acquire locks before modifying portals
- State updates (StateConnecting, StateConnected, etc.) inform users of connection status
- WebSocket reconnection has 10-second backoff on failures
- Initial sync fetches recent matches and last 10 minutes of updates
- Periodic sync runs every 5 minutes to catch missed events

## Common Tasks

### Adding New Tinder Event Types
1. Update WebSocket event handling in `handleIncomingEventsGoroutine`
2. Add handler function in `handletinder.go`
3. Update event routing logic

### Modifying Room Creation
1. Update `handleIncomingMatch` for match processing
2. Modify `GetChatInfo` for room metadata
3. Adjust initial message creation in `FetchMessages`

### Debugging Connection Issues
1. Check logs for WebSocket connection status
2. Verify token refresh in `LoadUserLogin`
3. Monitor state updates via BridgeState.Send calls

### Configuring Sentry Error Tracking
1. Add your Sentry DSN to `config.yaml` under `network.sentry_dsn`
2. The bridge will automatically initialize Sentry on startup if DSN is provided
3. Errors are captured with stack traces and release information
4. Sentry events are flushed on bridge shutdown

#### Error Types Captured
- **WebSocket Connection Errors** (in goroutines):
  - `websocket_token_failure`: Failed to get WebSocket token
  - `websocket_unexpected_close`: WebSocket closed unexpectedly
  - `websocket_unknown_error`: Other WebSocket connection failures
  - Max retries reached event
- **Sync Errors** (in goroutines):
  - `initial_sync_failure`: Failed to fetch initial matches
  - `sync_updates_failure`: Failed to get Tinder updates
- **Token Refresh Errors** (in goroutines):
  - `periodic_token_refresh_failure`: Failed to refresh token during periodic sync

All errors include relevant context such as user ID, retry counts, and error-specific metadata. Sentry captures are only added at the outermost layer (goroutines and top-level handlers) where errors cannot be propagated further.
