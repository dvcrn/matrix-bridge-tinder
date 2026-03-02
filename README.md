# matrix-tinder

A Matrix bridge that connects Tinder chats to Matrix.

It syncs Tinder conversations into Matrix rooms and bridges messages in both directions:

- Tinder -> Matrix
- Matrix -> Tinder

## Disclaimer

Using automation or unofficial integrations with Tinder may violate Tinder's terms and can get your account restricted or banned.

Use this at your own risk.

## Scope

This project is intentionally limited to chat bridging.

Supported:

- Bridge existing Tinder chat conversations into Matrix.
- Send and receive messages between Matrix and Tinder.

Not supported:

- Swiping / like / pass actions.
- Match-finding automation.
- Any botting behavior beyond message bridging.

## How It Works

- Uses `mautrix-go` `bridgev2` for the Matrix bridge framework.
- Uses `go-tinder` for Tinder API communication.
- Maintains login/session metadata and bridge state in local SQLite.

## Quick Start

1. Create local config files from examples:
   - `config.example.yaml` -> `config.yaml`
   - `registration.example.yaml` -> `registration.yaml`
2. Generate/update registration:

```bash
go run . -g -c config.yaml -r registration.yaml
```

3. Run the bridge:

```bash
go run main.go -r registration.yaml -c config.yaml
```

## Authentication

This bridge uses a token-based login flow from the Tinder web app.

You need to:

1. Open Tinder Web in your browser.
2. Capture and copy a request as a `curl` command.
3. Provide that `curl` command to the bridge login flow.
4. Provide the Tinder refresh token when prompted.

The bridge extracts required auth values (such as `X-Auth-Token` and device ID) from that curl command.

Note: this project is for Tinder, not Bumble.

## Local Data and Secrets

This repo is configured to ignore local runtime and secret files, including:

- `config.yaml`
- `registration.yaml`
- `*.db`, `*db-shm`, `*db-wal`
- `logs/`

Keep credentials local and do not commit generated config or runtime artifacts.
