// Package scorer computes composite scores for proxy configs based on
// test results, historical performance, and stability metrics.
package scorer

import (
	"math"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	"github.com/AmirAM03/velox/internal/model"
)

// Scorer computes and updates composite scores for proxy configs.
type Scorer struct {
	cfg config.ScoringConfig
}

// New creates a new Scorer with the given configuration.
func New(cfg config.ScoringConfig) *Scorer {
	return &Scorer{cfg: cfg}
}

// Compute calculates a composite score from test results.
// Lower scores are better.
func (s *Scorer) Compute(configID string, results []model.TestResult, existing *model.Score) *model.Score {
	now := time.Now()

	score := &model.Score{
		ConfigID:     configID,
		TestCount:    len(results),
		LastTestedAt: now,
		UpdatedAt:    now,
	}

	if existing != nil {
		score.TestCount += existing.TestCount
	}

	if len(results) == 0 {
		score.Composite = math.MaxFloat64
		return score
	}

	// Calculate success rate
	successCount := 0
	var totalLatency time.Duration
	var latencies []time.Duration

	for _, r := range results {
		if r.Success {
			successCount++
			totalLatency += r.Latency
			latencies = append(latencies, r.Latency)
		}
	}

	successRate := float64(successCount) / float64(len(results))
	score.SuccessScore = 1.0 - successRate // Lower is better

	// Latency score (P95-ish: use 95th percentile if enough samples, else max)
	if len(latencies) > 0 {
		sortDurations(latencies)
		p95Idx := int(float64(len(latencies)) * 0.95)
		if p95Idx >= len(latencies) {
			p95Idx = len(latencies) - 1
		}
		p95 := latencies[p95Idx]
		// Normalize: 0ms = 0.0, 10s+ = 1.0
		score.LatencyScore = math.Min(float64(p95.Milliseconds())/10000.0, 1.0)
	} else {
		score.LatencyScore = 1.0 // No successful latencies = worst score
	}

	// Stability penalty
	if existing != nil {
		score.StabilityPenalty = computeStabilityPenalty(results, existing.StabilityPenalty, s.cfg.StabilityDecay)
	}

	// Recency bonus (fresher tests = lower score component)
	hoursSinceTest := time.Since(now).Hours()
	score.RecencyBonus = math.Min(hoursSinceTest/168.0, 1.0) // Decays over 1 week

	// Composite score (weighted sum, lower is better)
	score.Composite = s.cfg.WeightLatency*score.LatencyScore +
		s.cfg.WeightSuccess*score.SuccessScore +
		s.cfg.WeightStability*score.StabilityPenalty +
		s.cfg.WeightRecency*score.RecencyBonus

	return score
}

// computeStabilityPenalty calculates a penalty for flaky configs.
// A config that alternates between success and failure gets a higher penalty.
func computeStabilityPenalty(results []model.TestResult, prevPenalty float64, decay float64) float64 {
	if len(results) < 2 {
		return prevPenalty * decay
	}

	// Count state transitions (success→fail or fail→success)
	transitions := 0
	for i := 1; i < len(results); i++ {
		if results[i].Success != results[i-1].Success {
			transitions++
		}
	}

	// Transition rate (0 = perfectly stable, 1 = every test flips)
	transitionRate := float64(transitions) / float64(len(results)-1)

	// Blend with previous penalty using exponential decay
	return prevPenalty*decay + transitionRate*(1-decay)
}

// sortDurations sorts a slice of durations in ascending order (insertion sort
// since slices are typically small).
func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		key := d[i]
		j := i - 1
		for j >= 0 && d[j] > key {
			d[j+1] = d[j]
			j--
		}
		d[j+1] = key
	}
}
