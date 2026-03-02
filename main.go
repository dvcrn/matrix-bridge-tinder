package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	// Import the main bridge helper package
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
	// mxmain handles logging setup internally.

	// Import our Tinder connector package
	"github.com/dvcrn/matrix-tinder/connector"
	"github.com/getsentry/sentry-go"
)

// Build time variables (optional but good practice)
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	// Create the network connector instance.
	// mxmain will handle logger injection during Init.
	connector := &connector.TinderConnector{}

	// Create and configure the BridgeMain helper.
	m := mxmain.BridgeMain{
		Name:        "tinder",
		Description: "Matrix <-> Tinder Bridge",
		Version:     "2.0.0",
		URL:         "https://github.com/dvcrn/matrix-tinder",
		Connector:   connector,

		// PostInit hook to initialize Sentry after config is loaded
		PostInit: func() {
			// The network config should be loaded by this point
			log.Println("----")
			log.Printf("Sentry DSN from connector.Config: %s\n", connector.Config.SentryDSN)
			b, _ := json.MarshalIndent(connector.Config, "", "  ")
			fmt.Println(string(b))
			log.Println("----")
			// Check if Sentry DSN is configured
			if connector.Config.SentryDSN != "" {
				err := sentry.Init(sentry.ClientOptions{
					Dsn:              connector.Config.SentryDSN,
					Environment:      "production",
					Release:          "matrix-tinder", // TODO: add proper release
					AttachStacktrace: true,
					SampleRate:       1.0,
					BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
						// You can filter or modify events here if needed
						return event
					},
				})
				if err != nil {
					log.Printf("Failed to initialize Sentry: %v", err)
				} else {
					log.Printf("Sentry initialized successfully with DSN: %s", connector.Config.SentryDSN)
				}
			} else {
				log.Printf("Sentry DSN not configured, skipping Sentry initialization")
			}
		},
	}

	connector.M = &m

	// Initialize version info and run the bridge.
	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()

	// Flush any buffered Sentry events before exit
	if connector.Config.SentryDSN != "" {
		sentry.Flush(2 * time.Second)
	}
}
