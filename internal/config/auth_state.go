package config

// AdminAuthState is the per-request auth state the /api middleware reads.
// It is a snapshot taken lazily on every request (via ConfigManager.AdminAuthState)
// so runtime enable/disable from the settings endpoints applies without a
// restart.
type AdminAuthState struct {
	Enabled   bool
	KeyDigest string // SHA-256 hex of the raw key; "" when no key exists
}

// AdminAuthState returns the current external-API auth state from the live
// config snapshot.
func (cm *ConfigManager) AdminAuthState() AdminAuthState {
	c := cm.Config()
	return AdminAuthState{Enabled: c.Server.APIEndpoint.Enabled, KeyDigest: c.Server.APIEndpoint.KeyDigest}
}

// SetAPIEndpointState swaps the live snapshot's API-endpoint state; used by the
// settings endpoints after both DB and YAML are persisted (mirrors the Store
// pattern in UpdateSettings).
func (cm *ConfigManager) SetAPIEndpointState(enabled bool, keyDigest string) {
	c := *cm.Config()
	c.Server.APIEndpoint.Enabled = enabled
	c.Server.APIEndpoint.KeyDigest = keyDigest
	cm.Store(&c)
}
