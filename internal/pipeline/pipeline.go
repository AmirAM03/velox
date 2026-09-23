// Package pipeline implements the multi-stage proxy config testing pipeline.
// Configs flow through stages 0-3, with each stage filtering out failures
// before advancing survivors to the next (more expensive) stage.
package pipeline

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/model"
)

// ProgressCallback is called when a config finishes a test in any stage.
type ProgressCallback func(stage model.TestStage, current, total, passed, failed int, result *model.TestResult, cfg *model.ProxyConfig)

// Pipeline orchestrates multi-stage testing of proxy configs.
type Pipeline struct {
	cfg        *config.PipelineConfig
	engine     engine.Engine
	targets    []string // Target URLs for Stage 2
	expect     []int    // Expected HTTP status codes
	logger     *slog.Logger
	onProgress  ProgressCallback
	methodology *model.BenchmarkMethodologyChain
}

// New creates a new Pipeline.
func New(cfg *config.PipelineConfig, eng engine.Engine, targets []string, expect []int, logger *slog.Logger) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	cfgCopy := *cfg
	// Ensure resilient default timeouts for real-world proxy testing
	if cfgCopy.Stage0Timeout < 2500*time.Millisecond {
		cfgCopy.Stage0Timeout = 2500 * time.Millisecond
	}
	if cfgCopy.Stage1Timeout < 3500*time.Millisecond {
		cfgCopy.Stage1Timeout = 3500 * time.Millisecond
	}
	if cfgCopy.Stage2Timeout < 6000*time.Millisecond {
		cfgCopy.Stage2Timeout = 7000 * time.Millisecond
	}
	return &Pipeline{
		cfg:     &cfgCopy,
		engine:  eng,
		targets: targets,
		expect:  expect,
		logger:  logger,
	}
}

// SetOnProgress registers a callback for live per-node test updates.
func (p *Pipeline) SetOnProgress(cb ProgressCallback) {
	p.onProgress = cb
}

// SetMethodology configures the dynamic test methodology chain to execute.
func (p *Pipeline) SetMethodology(m *model.BenchmarkMethodologyChain) {
	if m != nil && len(m.ActiveSteps()) > 0 {
		p.methodology = m
	}
}

// SetConcurrency dynamically scales worker pools across all pipeline stages.
func (p *Pipeline) SetConcurrency(threads int) {
	if threads <= 0 {
		return
	}
	p.cfg.Stage0Workers = threads * 4
	p.cfg.Stage1Workers = threads * 2
	p.cfg.Stage2Workers = threads
}

// Result holds the outcome of running a config through the pipeline.
type Result struct {
	Config      *model.ProxyConfig
	Results     []model.TestResult
	Failed      bool
	FailedStage model.TestStage
}

// Run executes the pipeline on a batch of configs.
// Each active step in the configured methodology chain is executed sequentially.
// If a step marked Necessary fails, that config is immediately disqualified and skips subsequent steps.
func (p *Pipeline) Run(ctx context.Context, configs []*model.ProxyConfig) []Result {
	results := make([]Result, len(configs))
	for i, cfg := range configs {
		results[i] = Result{Config: cfg}
	}

	chain := p.methodology
	if chain == nil || len(chain.ActiveSteps()) == 0 {
		chain = model.DefaultMethodologyChain()
	}

	activeSteps := chain.ActiveSteps()
	p.logger.Info("pipeline: executing methodology chain",
		"steps", len(activeSteps),
		"configs", len(configs),
		"scoring_mode", chain.ScoringMode,
	)

	survivors := configs
	for stepIdx, step := range activeSteps {
		if len(survivors) == 0 {
			p.logger.Info("pipeline: all configs disqualified before step", "step", step.Name, "priority", step.Priority)
			break
		}

		workers := p.cfg.Stage0Workers
		stage := model.StageDNSTCP
		switch step.Type {
		case model.MethodologyTCPPing:
			workers = p.cfg.Stage0Workers
			stage = model.StageDNSTCP
		case model.MethodologyTLSHandshake:
			workers = p.cfg.Stage1Workers
			stage = model.StageTLS
		case model.MethodologyHTTPDelay:
			workers = p.cfg.Stage2Workers
			stage = model.StageProxy
		}

		p.logger.Info("pipeline: executing step",
			"index", stepIdx+1,
			"name", step.Name,
			"type", string(step.Type),
			"necessary", step.Necessary,
			"configs", len(survivors),
		)

		survivors = p.runStep(ctx, survivors, results, step, stage, workers)
	}

	passedCount := 0
	for _, r := range results {
		if !r.Failed {
			passedCount++
		}
	}

	p.logger.Info("pipeline: complete",
		"total", len(configs),
		"passed", passedCount,
		"failed", len(configs)-passedCount,
	)

	return results
}

// runStep runs a single pipeline methodology step with bounded concurrency.
func (p *Pipeline) runStep(
	ctx context.Context,
	configs []*model.ProxyConfig,
	allResults []Result,
	step model.TestStepConfig,
	stage model.TestStage,
	maxWorkers int,
) []*model.ProxyConfig {
	if len(configs) == 0 {
		return nil
	}

	idxMap := make(map[string]int)
	for i, r := range allResults {
		idxMap[r.Config.ID] = i
	}

	type stageResult struct {
		config *model.ProxyConfig
		result model.TestResult
	}

	resultCh := make(chan stageResult, len(configs))
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	timeout := step.TimeoutDuration()

	for _, cfg := range configs {
		wg.Add(1)
		go func(c *model.ProxyConfig) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				resultCh <- stageResult{
					config: c,
					result: model.TestResult{
						ConfigID: c.ID,
						StepID:   step.ID,
						Stage:    stage,
						Success:  false,
						Error:    "context cancelled",
						TestedAt: time.Now(),
					},
				}
				return
			}

			testCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			var tr model.TestResult
			switch step.Type {
			case model.MethodologyTCPPing:
				tr = p.testDNSTCP(testCtx, c)
			case model.MethodologyTLSHandshake:
				tr = p.testTLS(testCtx, c)
			case model.MethodologyHTTPDelay:
				tr = p.testProxyForStep(testCtx, c, step)
			default:
				tr = p.testDNSTCP(testCtx, c)
			}

			tr.ConfigID = c.ID
			tr.StepID = step.ID
			tr.Stage = stage
			tr.TestedAt = time.Now()

			resultCh <- stageResult{config: c, result: tr}
		}(cfg)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var survivors []*model.ProxyConfig
	var stagePassed, stageFailed int
	for sr := range resultCh {
		idx := idxMap[sr.config.ID]
		allResults[idx].Results = append(allResults[idx].Results, sr.result)

		if sr.result.Success {
			stagePassed++
			survivors = append(survivors, sr.config)
		} else {
			stageFailed++
			if step.Necessary {
				// Hard filter: disqualify config from subsequent steps
				allResults[idx].Failed = true
				allResults[idx].FailedStage = stage
				p.logger.Debug("config disqualified at necessary step",
					"step", step.Name,
					"config", sr.config.DisplayName(),
					"error", sr.result.Error,
				)
			} else {
				// Non-necessary step: config continues in survivors
				survivors = append(survivors, sr.config)
				p.logger.Debug("config failed optional step",
					"step", step.Name,
					"config", sr.config.DisplayName(),
					"error", sr.result.Error,
				)
			}
		}

		if p.onProgress != nil {
			p.onProgress(stage, stagePassed+stageFailed, len(configs), stagePassed, stageFailed, &sr.result, sr.config)
		}
	}

	return survivors
}

// testDNSTCP performs Stage 0: DNS resolution and TCP/UDP connect.
func (p *Pipeline) testDNSTCP(ctx context.Context, cfg *model.ProxyConfig) model.TestResult {
	start := time.Now()
	addr := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)

	if cfg.Network == model.NetworkUDP {
		raddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return model.TestResult{
				Success: false,
				Error:   fmt.Sprintf("udp resolve: %v", err),
				Latency: time.Since(start),
			}
		}
		conn, err := net.DialUDP("udp", nil, raddr)
		latency := time.Since(start)
		if err != nil {
			return model.TestResult{
				Success: false,
				Error:   fmt.Sprintf("udp dial: %v", err),
				Latency: latency,
			}
		}
		conn.Close()
		return model.TestResult{
			Success: true,
			Latency: latency,
		}
	}

	// TCP dial includes DNS resolution
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	latency := time.Since(start)

	if err != nil {
		return model.TestResult{
			Success: false,
			Error:   fmt.Sprintf("tcp dial: %v", err),
			Latency: latency,
		}
	}
	conn.Close()

	return model.TestResult{
		Success: true,
		Latency: latency,
	}
}

// testTLS performs Stage 1: TLS/REALITY handshake.
func (p *Pipeline) testTLS(ctx context.Context, cfg *model.ProxyConfig) model.TestResult {
	start := time.Now()

	// Skip TLS test for non-TLS configs (only test explicit TLS and REALITY)
	if cfg.Security != model.SecurityTLS && cfg.Security != model.SecurityREALITY {
		return model.TestResult{
			Success: true,
			Latency: time.Since(start),
		}
	}

	// For REALITY configs, we use the engine's TestTLSHandshake
	// because REALITY requires the proxy core to negotiate.
	if cfg.Security == model.SecurityREALITY {
		err := p.engine.TestTLSHandshake(ctx, cfg)
		latency := time.Since(start)
		if err != nil {
			return model.TestResult{
				Success: false,
				Error:   fmt.Sprintf("reality handshake: %v", err),
				Latency: latency,
			}
		}
		return model.TestResult{
			Success: true,
			Latency: latency,
		}
	}

	// Standard TLS handshake
	addr := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)
	sni := cfg.SNI
	if sni == "" {
		sni = cfg.Address
	}

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return model.TestResult{
			Success: false,
			Error:   fmt.Sprintf("tcp dial for tls: %v", err),
			Latency: time.Since(start),
		}
	}
	defer conn.Close()

	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: cfg.AllowInsecure,
	})

	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return model.TestResult{
			Success: false,
			Error:   fmt.Sprintf("tls handshake: %v", err),
			Latency: time.Since(start),
		}
	}
	tlsConn.Close()

	return model.TestResult{
		Success: true,
		Latency: time.Since(start),
	}
}

// testProxy performs Stage 2: functional proxy test via HTTP using default targets.
func (p *Pipeline) testProxy(ctx context.Context, cfg *model.ProxyConfig) model.TestResult {
	return p.testProxyForStep(ctx, cfg, model.TestStepConfig{
		Type:      model.MethodologyHTTPDelay,
		TimeoutMS: int(p.cfg.Stage2Timeout.Milliseconds()),
	})
}

// testProxyForStep performs functional proxy test via HTTP honoring step-specific targets and status expectations.
func (p *Pipeline) testProxyForStep(ctx context.Context, cfg *model.ProxyConfig, step model.TestStepConfig) model.TestResult {
	start := time.Now()

	timeout := step.TimeoutDuration()
	if timeout <= 0 {
		timeout = p.cfg.Stage2Timeout
	}

	// Create HTTP client that dials through the proxy engine
	transport := &http.Transport{
		DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			return p.engine.Dial(dialCtx, cfg, network, addr)
		},
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// Don't follow redirects
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	targets := p.targets
	if strings.TrimSpace(step.TargetURL) != "" {
		targets = []string{step.TargetURL}
	}

	expectCodes := p.expect
	if len(step.ExpectCodes) > 0 {
		expectCodes = step.ExpectCodes
	}

	// Test against each target URL
	for _, rawTarget := range targets {
		targetURL := strings.TrimSpace(rawTarget)
		if targetURL == "" {
			continue
		}
		if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
			targetURL = "https://" + targetURL
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Velox/1.0")

		ttfbStart := time.Now()
		resp, err := client.Do(req)
		ttfb := time.Since(ttfbStart)

		if err != nil {
			return model.TestResult{
				Success:   false,
				Error:     fmt.Sprintf("http request to %s: %v", targetURL, err),
				Latency:   ttfb,
				TTFB:      ttfb,
				TargetURL: targetURL,
			}
		}
		resp.Body.Close()

		// Check status code: matching explicit expect list or any valid HTTP 2xx/3xx
		statusOK := false
		for _, expected := range expectCodes {
			if resp.StatusCode == expected {
				statusOK = true
				break
			}
		}
		if !statusOK && len(expectCodes) == 0 && resp.StatusCode >= 200 && resp.StatusCode < 400 {
			statusOK = true
		}

		if !statusOK {
			return model.TestResult{
				Success:   false,
				Error:     fmt.Sprintf("unexpected status %d from %s", resp.StatusCode, targetURL),
				Latency:   ttfb,
				TTFB:      ttfb,
				TargetURL: targetURL,
			}
		}

		// First successful target is enough
		return model.TestResult{
			Success:   true,
			Latency:   ttfb,
			TTFB:      ttfb,
			TargetURL: targetURL,
		}
	}

	return model.TestResult{
		Success: false,
		Error:   "no target URLs configured",
		Latency: time.Since(start),
	}
}
