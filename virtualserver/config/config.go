package config

import "time"

// VSConfig holds all configuration for the VirtualServer subsystem.
// It is populated from tracker/config.go's FileConfig via ParseConfigFile.
type VSConfig struct {
	Enabled        bool
	Port           string        // e.g. ":9091"
	AdminKey       string        // bearer token for API auth
	DBPath         string        // path to vs.db
	ReconcileEvery time.Duration // reconciler tick interval
	MaxBodyBytes   int64         // max request body size (default 1 MiB)
	RequestTimeout time.Duration // HTTP handler timeout (default 30 s)
}

// DefaultVSConfig returns safe defaults for an isolated VS deployment.
func DefaultVSConfig() VSConfig {
	return VSConfig{
		Enabled:        false,
		Port:           ":9091",
		AdminKey:       "",
		DBPath:         "data/db/vs.db",
		ReconcileEvery: 15 * time.Second,
		MaxBodyBytes:   1 << 20, // 1 MiB
		RequestTimeout: 30 * time.Second,
	}
}
