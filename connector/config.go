package connector

import (
	_ "embed"
	"go.mau.fi/util/configupgrade"
)

//go:embed example-config.yaml
var ExampleConfig string

// TinderConfig holds network-specific configuration options.
// Define fields here if needed later (e.g., API endpoints, specific features).
type TinderConfig struct {
	// ExampleField string `yaml:"example_field"`
	SentryDSN string `yaml:"sentry_dsn"`
}

func upgradeConfig(helper configupgrade.Helper) {
	// Copy existing config values to preserve user settings
	helper.Copy(configupgrade.Str|configupgrade.Null, "sentry_dsn")
}

// GetConfig returns the default config content, a pointer to the config struct, and an upgrader.
func (tc *TinderConnector) GetConfig() (string, any, configupgrade.Upgrader) {
	// Return embedded example config, pointer to connector's Config field, and upgrader to preserve existing values.
	// The framework will use this default if the config file is missing,
	// and unmarshal the actual config file into tc.Config if it exists.
	return ExampleConfig, &tc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}