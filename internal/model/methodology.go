package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TestMethodologyType defines the available testing methodologies in Velox.
type TestMethodologyType string

const (
	MethodologyTCPPing      TestMethodologyType = "tcp_ping"
	MethodologyTLSHandshake TestMethodologyType = "tls_handshake"
	MethodologyHTTPDelay    TestMethodologyType = "http_delay"
)

const (
	ScoringModePrimary  = "primary_test"
	ScoringModeWeighted = "weighted_average"
)

// TestStepConfig defines a single test step in the sequential benchmarking chain.
type TestStepConfig struct {
	ID          string              `json:"id"`
	Type        TestMethodologyType `json:"type"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Enabled     bool                `json:"enabled"`
	Necessary   bool                `json:"necessary"`  // If true and test fails, config is marked failed & skips subsequent steps
	Priority    int                 `json:"priority"`   // Sequence execution order (1, 2, 3...)
	Weight      float64             `json:"weight"`     // Scoring weight (0.0 to 1.0)
	IsPrimary   bool                `json:"is_primary"` // If true, provides the primary score in primary_test scoring mode

	// Step Parameters
	TimeoutMS   int      `json:"timeout_ms"`
	TargetURL   string   `json:"target_url,omitempty"`   // Destination URL for http_delay
	ExpectCodes []int    `json:"expect_codes,omitempty"` // Expected HTTP status codes (e.g. 200, 204)
}

// TimeoutDuration returns the configured timeout as time.Duration.
func (s TestStepConfig) TimeoutDuration() time.Duration {
	if s.TimeoutMS <= 0 {
		switch s.Type {
		case MethodologyTCPPing:
			return 2500 * time.Millisecond
		case MethodologyTLSHandshake:
			return 3500 * time.Millisecond
		case MethodologyHTTPDelay:
			return 7000 * time.Millisecond
		default:
			return 5000 * time.Millisecond
		}
	}
	return time.Duration(s.TimeoutMS) * time.Millisecond
}

// BenchmarkMethodologyChain defines the ordered chain of test steps and scoring strategy.
type BenchmarkMethodologyChain struct {
	ScoringMode string           `json:"scoring_mode"` // "primary_test" or "weighted_average"
	Steps       []TestStepConfig `json:"steps"`
}

// DefaultMethodologyChain returns the default standard testing chain.
func DefaultMethodologyChain() *BenchmarkMethodologyChain {
	return &BenchmarkMethodologyChain{
		ScoringMode: "primary_test",
		Steps: []TestStepConfig{
			{
				ID:          "step_tcp_ping",
				Type:        MethodologyTCPPing,
				Name:        "TCP Port Ping",
				Description: "Fast socket connect to proxy host:port. Filters out dead hosts instantly.",
				Enabled:     true,
				Necessary:   true,
				Priority:    1,
				Weight:      0.2,
				IsPrimary:   false,
				TimeoutMS:   2500,
			},
			{
				ID:          "step_tls_handshake",
				Type:        MethodologyTLSHandshake,
				Name:        "TLS / REALITY Handshake",
				Description: "Validates cryptographic TLS/REALITY negotiation and SNI parameters.",
				Enabled:     true,
				Necessary:   false,
				Priority:    2,
				Weight:      0.2,
				IsPrimary:   false,
				TimeoutMS:   3500,
			},
			{
				ID:          "step_http_delay",
				Type:        MethodologyHTTPDelay,
				Name:        "HTTP URL Real Delay",
				Description: "In-process proxy HTTP request measuring true Time-To-First-Byte (TTFB).",
				Enabled:     true,
				Necessary:   true,
				Priority:    3,
				Weight:      0.6,
				IsPrimary:   true,
				TimeoutMS:   7000,
				TargetURL:   "https://www.google.com/generate_204",
				ExpectCodes: []int{200, 204},
			},
		},
	}
}

// ActiveSteps returns enabled steps sorted by execution Priority.
func (c *BenchmarkMethodologyChain) ActiveSteps() []TestStepConfig {
	if c == nil || len(c.Steps) == 0 {
		return DefaultMethodologyChain().ActiveSteps()
	}

	var active []TestStepConfig
	for _, s := range c.Steps {
		if s.Enabled {
			active = append(active, s)
		}
	}

	sort.SliceStable(active, func(i, j int) bool {
		return active[i].Priority < active[j].Priority
	})

	return active
}

// GetPrimaryStep returns the step marked as primary, or the last enabled step as fallback.
func (c *BenchmarkMethodologyChain) GetPrimaryStep() *TestStepConfig {
	active := c.ActiveSteps()
	if len(active) == 0 {
		return nil
	}
	for i := range active {
		if active[i].IsPrimary {
			return &active[i]
		}
	}
	// Fallback: last active step (usually HTTP delay)
	return &active[len(active)-1]
}

// Validate checks the chain configuration for consistency.
func (c *BenchmarkMethodologyChain) Validate() error {
	if c == nil || len(c.Steps) == 0 {
		return fmt.Errorf("methodology chain cannot be empty")
	}

	activeCount := 0
	for _, s := range c.Steps {
		if s.Enabled {
			activeCount++
			if s.TimeoutMS < 500 && s.TimeoutMS != 0 {
				return fmt.Errorf("step %q timeout must be at least 500ms", s.Name)
			}
			if s.Type == MethodologyHTTPDelay {
				target := strings.TrimSpace(s.TargetURL)
				if target == "" {
					return fmt.Errorf("step %q requires a target URL", s.Name)
				}
			}
		}
	}

	if activeCount == 0 {
		return fmt.Errorf("at least one test step must be enabled")
	}

	if c.ScoringMode != "primary_test" && c.ScoringMode != "weighted_average" {
		c.ScoringMode = "primary_test"
	}

	return nil
}

// Clone creates a deep copy of the methodology chain.
func (c *BenchmarkMethodologyChain) Clone() *BenchmarkMethodologyChain {
	if c == nil {
		return DefaultMethodologyChain()
	}
	data, _ := json.Marshal(c)
	var clone BenchmarkMethodologyChain
	_ = json.Unmarshal(data, &clone)
	return &clone
}

// ParseMethodologyChain parses a JSON string into a BenchmarkMethodologyChain.
func ParseMethodologyChain(data string) (*BenchmarkMethodologyChain, error) {
	var chain BenchmarkMethodologyChain
	if err := json.Unmarshal([]byte(data), &chain); err != nil {
		return nil, fmt.Errorf("unmarshal methodology chain: %w", err)
	}
	return &chain, nil
}
