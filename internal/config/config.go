// Package config handles application configuration using Viper.
// Configuration is layered: defaults → config file → env vars → CLI flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config holds all application configuration.
type Config struct {
	// General
	DataDir  string `mapstructure:"data_dir"`  // Data directory for SQLite, logs, etc.
	LogLevel string `mapstructure:"log_level"` // Log level: debug, info, warn, error
	LogJSON  bool   `mapstructure:"log_json"`  // Output logs in JSON format

	// Pipeline
	Pipeline PipelineConfig `mapstructure:"pipeline"`

	// Proxy
	Proxy ProxyConfig `mapstructure:"proxy"`

	// Test targets
	Targets TargetConfig `mapstructure:"targets"`

	// Scoring
	Scoring ScoringConfig `mapstructure:"scoring"`

	// Debug
	Debug DebugConfig `mapstructure:"debug"`
}

// PipelineConfig controls the multi-stage testing pipeline.
type PipelineConfig struct {
	// Per-stage concurrency limits
	Stage0Workers int `mapstructure:"stage0_workers"` // DNS+TCP probe workers
	Stage1Workers int `mapstructure:"stage1_workers"` // TLS handshake workers
	Stage2Workers int `mapstructure:"stage2_workers"` // Proxy functional test workers
	Stage3Workers int `mapstructure:"stage3_workers"` // Deep quality test workers

	// Timeouts
	Stage0Timeout time.Duration `mapstructure:"stage0_timeout"` // DNS+TCP timeout
	Stage1Timeout time.Duration `mapstructure:"stage1_timeout"` // TLS handshake timeout
	Stage2Timeout time.Duration `mapstructure:"stage2_timeout"` // Proxy test timeout
	Stage3Timeout time.Duration `mapstructure:"stage3_timeout"` // Quality test timeout

	// Adaptive concurrency
	AdaptiveConcurrency bool    `mapstructure:"adaptive_concurrency"` // Enable AIMD controller
	TimeoutRateHigh     float64 `mapstructure:"timeout_rate_high"`    // Reduce workers above this rate
	TimeoutRateLow      float64 `mapstructure:"timeout_rate_low"`     // Increase workers below this rate

	// Rate limiting
	PerHostRateLimit int           `mapstructure:"per_host_rate_limit"` // Max concurrent tests per host
	PerHostCooldown  time.Duration `mapstructure:"per_host_cooldown"`  // Cooldown between tests to same host

	// Quality test settings
	QualityRounds int `mapstructure:"quality_rounds"` // Number of rounds for Stage 3
}

// ProxyConfig controls the local proxy server.
type ProxyConfig struct {
	ListenAddr   string `mapstructure:"listen_addr"`    // Listen address (default: 127.0.0.1)
	SOCKSPort    int    `mapstructure:"socks_port"`     // SOCKS5 port
	HTTPPort     int    `mapstructure:"http_port"`      // HTTP proxy port
	MixedPort    int    `mapstructure:"mixed_port"`     // Mixed SOCKS5+HTTP port (preferred)
	WarmPoolSize int    `mapstructure:"warm_pool_size"` // Number of configs kept warm
	HealthInterval time.Duration `mapstructure:"health_interval"` // Health check interval
	FailThreshold  int           `mapstructure:"fail_threshold"`  // Consecutive failures before failover
}

// TargetConfig holds test target URLs and settings.
type TargetConfig struct {
	// URLs to test against in Stage 2.
	// Each URL is tested via HTTP through the proxy. The first successful
	// response determines "functional." Multiple URLs give broader coverage.
	URLs []string `mapstructure:"urls"`

	// ExpectStatus is the expected HTTP status code (default: 200, 204).
	ExpectStatus []int `mapstructure:"expect_status"`

	// SpeedTestURL is used in Stage 3 for throughput measurement.
	SpeedTestURL string `mapstructure:"speed_test_url"`

	// DNSURL is used in Stage 3 to test DNS resolution through the proxy.
	DNSTestHost string `mapstructure:"dns_test_host"`
}

// ScoringConfig controls the composite scoring model.
type ScoringConfig struct {
	WeightLatency   float64 `mapstructure:"weight_latency"`   // Weight for P95 latency
	WeightSuccess   float64 `mapstructure:"weight_success"`   // Weight for success rate
	WeightThroughput float64 `mapstructure:"weight_throughput"` // Weight for throughput
	WeightStability float64 `mapstructure:"weight_stability"` // Weight for stability penalty
	WeightRecency   float64 `mapstructure:"weight_recency"`   // Weight for recency bonus
	StabilityDecay  float64 `mapstructure:"stability_decay"`  // Exponential decay rate for flakiness
}

// DebugConfig controls debugging features.
type DebugConfig struct {
	PprofEnabled bool   `mapstructure:"pprof_enabled"` // Enable pprof HTTP endpoint
	PprofAddr    string `mapstructure:"pprof_addr"`    // pprof listen address
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".velox")

	return &Config{
		DataDir:  dataDir,
		LogLevel: "info",
		LogJSON:  false,
		Pipeline: PipelineConfig{
			Stage0Workers: 2000,
			Stage1Workers: 500,
			Stage2Workers: 150,
			Stage3Workers: 20,
			Stage0Timeout: 3 * time.Second,
			Stage1Timeout: 5 * time.Second,
			Stage2Timeout: 10 * time.Second,
			Stage3Timeout: 30 * time.Second,
			AdaptiveConcurrency: true,
			TimeoutRateHigh:     0.15,
			TimeoutRateLow:      0.05,
			PerHostRateLimit:    5,
			PerHostCooldown:     500 * time.Millisecond,
			QualityRounds:       5,
		},
		Proxy: ProxyConfig{
			ListenAddr:     "127.0.0.1",
			MixedPort:      1080,
			WarmPoolSize:   5,
			HealthInterval: 30 * time.Second,
			FailThreshold:  2,
		},
		Targets: TargetConfig{
			URLs:         []string{"https://www.gstatic.com/generate_204"},
			ExpectStatus: []int{200, 204},
			SpeedTestURL: "https://speed.cloudflare.com/__down?bytes=10485760",
			DNSTestHost:  "google.com",
		},
		Scoring: ScoringConfig{
			WeightLatency:   0.30,
			WeightSuccess:   0.30,
			WeightThroughput: 0.15,
			WeightStability: 0.15,
			WeightRecency:   0.10,
			StabilityDecay:  0.8,
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

// Validate checks the config for obvious errors.
func (c *Config) Validate() error {
	if c.Pipeline.Stage0Workers <= 0 {
		return fmt.Errorf("stage0_workers must be > 0")
	}
	if len(c.Targets.URLs) == 0 {
		return fmt.Errorf("at least one target URL is required")
	}
	if c.Proxy.MixedPort <= 0 || c.Proxy.MixedPort > 65535 {
		return fmt.Errorf("mixed_port must be 1-65535")
	}
	return nil
}
