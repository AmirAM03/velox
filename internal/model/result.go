package model

import "time"

// TestStage identifies which pipeline stage produced a result.
type TestStage int

const (
	StageDNSTCP    TestStage = 0 // DNS resolution + TCP connect
	StageTLS       TestStage = 1 // TLS/REALITY handshake
	StageProxy     TestStage = 2 // Functional proxy test (HTTP through proxy)
	StageQuality   TestStage = 3 // Deep quality test (throughput, jitter, stability)
)

// String returns a human-readable name for the test stage.
func (s TestStage) String() string {
	switch s {
	case StageDNSTCP:
		return "dns+tcp"
	case StageTLS:
		return "tls"
	case StageProxy:
		return "proxy"
	case StageQuality:
		return "quality"
	default:
		return "unknown"
	}
}

// TestResult captures the outcome of testing a single config through one stage.
type TestResult struct {
	ConfigID  string        `json:"config_id"`
	Stage     TestStage     `json:"stage"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	Latency   time.Duration `json:"latency"`          // Total time for this stage
	TTFB      time.Duration `json:"ttfb,omitempty"`   // Time to first byte (Stage 2+)
	TestedAt  time.Time     `json:"tested_at"`
	TargetURL string        `json:"target_url,omitempty"` // The URL tested against (Stage 2)
}

// QualityResult captures deep quality metrics from Stage 3.
type QualityResult struct {
	ConfigID     string        `json:"config_id"`
	Throughput   float64       `json:"throughput_mbps"`    // Download throughput in Mbps
	LatencyP50   time.Duration `json:"latency_p50"`
	LatencyP95   time.Duration `json:"latency_p95"`
	LatencyP99   time.Duration `json:"latency_p99"`
	Jitter       time.Duration `json:"jitter"`             // Standard deviation of latencies
	DNSResolved  bool          `json:"dns_resolved"`       // UDP DNS through proxy works
	Rounds       int           `json:"rounds"`             // Number of test rounds completed
	SuccessRate  float64       `json:"success_rate"`       // Success ratio across rounds (0.0-1.0)
	TestedAt     time.Time     `json:"tested_at"`
}

// Score is the composite score for a proxy config, combining test results
// and historical performance.
type Score struct {
	ConfigID        string    `json:"config_id"`
	Composite       float64   `json:"composite"`        // Final weighted score (lower is better)
	LatencyScore    float64   `json:"latency_score"`    // Normalized latency component
	SuccessScore    float64   `json:"success_score"`    // Success rate component (0.0-1.0)
	ThroughputScore float64   `json:"throughput_score"` // Normalized throughput component
	StabilityPenalty float64  `json:"stability_penalty"` // Penalty for flaky behavior
	RecencyBonus    float64   `json:"recency_bonus"`    // Bonus for recent tests
	TestCount       int       `json:"test_count"`       // Total number of tests run
	LastTestedAt    time.Time `json:"last_tested_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
