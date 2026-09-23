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

func TestScorer_Compute_MethodologyChain(t *testing.T) {
	cfg := config.DefaultConfig().Scoring
	sc := New(cfg)

	chain := &model.BenchmarkMethodologyChain{
		ScoringMode: model.ScoringModePrimary,
		Steps: []model.TestStepConfig{
			{
				ID:        "step-ping",
				Type:      model.MethodologyTCPPing,
				Enabled:   true,
				Necessary: true,
				Priority:  1,
				Weight:    0.2,
			},
			{
				ID:        "step-http",
				Type:      model.MethodologyHTTPDelay,
				Enabled:   true,
				Necessary: true,
				Priority:  2,
				Weight:    0.8,
				IsPrimary: true,
			},
		},
	}

	// 1. Necessary Ping failed -> Composite must be 999999 (disqualified)
	pingFailedResults := []model.TestResult{
		{StepID: "step-ping", Stage: model.StageDNSTCP, Success: false, Latency: 500 * time.Millisecond},
	}
	failScore := sc.Compute("cfg-fail", pingFailedResults, nil, chain)
	if failScore.Composite < 999999 || failScore.SuccessScore != 1.0 {
		t.Errorf("expected failure score 999999 and success_score 1.0, got composite=%f, success_score=%f",
			failScore.Composite, failScore.SuccessScore)
	}

	// 2. Both passed -> Score is determined by primary test (step-http, 250ms)
	bothPassedResults := []model.TestResult{
		{StepID: "step-ping", Stage: model.StageDNSTCP, Success: true, Latency: 30 * time.Millisecond},
		{StepID: "step-http", Stage: model.StageProxy, Success: true, Latency: 250 * time.Millisecond},
	}
	passScore := sc.Compute("cfg-pass", bothPassedResults, nil, chain)
	if passScore.Composite >= 999999 || passScore.SuccessScore != 0.0 {
		t.Errorf("expected success score < 999999 and success_score 0.0, got composite=%f, success_score=%f",
			passScore.Composite, passScore.SuccessScore)
	}
	// LatencyScore should be 250ms / 10000ms = 0.025
	expectedLatScore := 250.0 / 10000.0
	if math.Abs(passScore.LatencyScore-expectedLatScore) > 0.001 {
		t.Errorf("expected latency score ~%f, got %f", expectedLatScore, passScore.LatencyScore)
	}

	// 3. Weighted Average mode
	chainWeighted := &model.BenchmarkMethodologyChain{
		ScoringMode: model.ScoringModeWeighted,
		Steps: []model.TestStepConfig{
			{
				ID:        "step-ping",
				Type:      model.MethodologyTCPPing,
				Enabled:   true,
				Necessary: true,
				Priority:  1,
				Weight:    0.5,
			},
			{
				ID:        "step-http",
				Type:      model.MethodologyHTTPDelay,
				Enabled:   true,
				Necessary: true,
				Priority:  2,
				Weight:    0.5,
			},
		},
	}
	weightedScore := sc.Compute("cfg-weighted", bothPassedResults, nil, chainWeighted)
	// (30ms * 0.5 + 250ms * 0.5) / 1.0 = 140ms -> 140/10000 = 0.014
	expectedWeightedLatScore := 140.0 / 10000.0
	if math.Abs(weightedScore.LatencyScore-expectedWeightedLatScore) > 0.001 {
		t.Errorf("expected weighted latency score ~%f, got %f", expectedWeightedLatScore, weightedScore.LatencyScore)
	}
}
