// Package config handles application configuration using Viper.
// Configuration is layered: defaults → config file → env vars → CLI flags.
package config

import (
	"os"
	"path/filepath"
)

// Config holds all application configuration.
type Config struct {
	// General
	DataDir  string `mapstructure:"data_dir"`  // Data directory for SQLite, logs, etc.
	LogLevel string `mapstructure:"log_level"` // Log level: debug, info, warn, error
	LogJSON  bool   `mapstructure:"log_json"`  // Output logs in JSON format

	// Proxy
	Proxy ProxyConfig `mapstructure:"proxy"`

	// Debug
	Debug DebugConfig `mapstructure:"debug"`
}

// ProxyConfig controls the per-profile proxy listeners (ports are set per profile in the dashboard).
type ProxyConfig struct {
	ListenAddr    string `mapstructure:"listen_addr"`    // Address profile ports bind to (default: 0.0.0.0)
	FailThreshold int    `mapstructure:"fail_threshold"` // Consecutive dial failures before failing over to the next node
}

// DebugConfig controls debugging features.
type DebugConfig struct {
	PprofEnabled bool   `mapstructure:"pprof_enabled"` // Enable pprof HTTP endpoint
	PprofAddr    string `mapstructure:"pprof_addr"`    // pprof listen address
}

// DefaultDataDir returns the default data directory for Velox.
func DefaultDataDir() string {
	homeDir, err := os.UserHomeDir()
	if err == nil && homeDir != "" {
		return filepath.Join(homeDir, ".velox")
	}
	return "/var/lib/velox"
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		DataDir:  DefaultDataDir(),
		LogLevel: "info",
		LogJSON:  false,
		Proxy: ProxyConfig{
			ListenAddr:    "0.0.0.0",
			FailThreshold: 3,
		},
		Debug: DebugConfig{
			PprofEnabled: true,
			PprofAddr:    "127.0.0.1:6060",
		},
	}
}

// DBPath returns the full path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "velox.db")
}
