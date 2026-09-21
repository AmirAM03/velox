package scorer

import (
	"math"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	"github.com/AmirAM03/velox/internal/model"
)

func TestScorer_Compute_Empty(t *testing.T) {
	sc := New(config.DefaultConfig().Scoring)
	score := sc.Compute("cfg-1", nil, nil)
	if score.Composite != math.MaxFloat64 {
		t.Errorf("expected MaxFloat64 for empty results, got %v", score.Composite)
	}
	if score.TestCount != 0 {
		t.Errorf("expected 0 test count, got %d", score.TestCount)
	}
}

func TestScorer_Compute_SuccessVsFailure(t *testing.T) {
	cfg := config.DefaultConfig().Scoring
	sc := New(cfg)

	// Perfect config: 100% success, 50ms latency
	perfectResults := []model.TestResult{
		{Success: true, Latency: 50 * time.Millisecond},
		{Success: true, Latency: 60 * time.Millisecond},
		{Success: true, Latency: 40 * time.Millisecond},
	}
	perfScore := sc.Compute("cfg-good", perfectResults, nil)

	// Failing config: 100% failure
	failedResults := []model.TestResult{
		{Success: false, Latency: 3000 * time.Millisecond},
		{Success: false, Latency: 3000 * time.Millisecond},
	}
	failScore := sc.Compute("cfg-bad", failedResults, nil)

	if perfScore.Composite >= failScore.Composite {
		t.Errorf("expected good score (%f) to be lower than bad score (%f)",
			perfScore.Composite, failScore.Composite)
	}

	if perfScore.SuccessScore != 0.0 {
		t.Errorf("expected 0.0 success score for 100%% success, got %f", perfScore.SuccessScore)
	}
	if failScore.SuccessScore != 1.0 {
		t.Errorf("expected 1.0 success score for 100%% failure, got %f", failScore.SuccessScore)
	}
}

func TestScorer_StabilityPenalty(t *testing.T) {
	// Alternating results (flaky)
	flakyResults := []model.TestResult{
		{Success: true},
		{Success: false},
		{Success: true},
		{Success: false},
	}
	penalty := computeStabilityPenalty(flakyResults, 0, 0.8)
	if penalty <= 0 {
		t.Errorf("expected positive stability penalty for flaky config, got %f", penalty)
	}

	// Consistently successful results
	stableResults := []model.TestResult{
		{Success: true},
		{Success: true},
		{Success: true},
		{Success: true},
	}
	stablePenalty := computeStabilityPenalty(stableResults, 0, 0.8)
	if stablePenalty != 0 {
		t.Errorf("expected 0 penalty for perfectly stable config, got %f", stablePenalty)
	}
}

func TestScorer_ExistingAccumulation(t *testing.T) {
	cfg := config.DefaultConfig().Scoring
	sc := New(cfg)

	existing := &model.Score{
		ConfigID:         "cfg-1",
		TestCount:        5,
		StabilityPenalty: 0.5,
	}

	newResults := []model.TestResult{
		{Success: true, Latency: 100 * time.Millisecond},
	}

	score := sc.Compute("cfg-1", newResults, existing)
	if score.TestCount != 6 {
		t.Errorf("expected test count to be 6, got %d", score.TestCount)
	}
}
