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

// Pipeline orchestrates multi-stage testing of proxy configs.
type Pipeline struct {
	cfg    *config.PipelineConfig
	engine engine.Engine
	targets []string       // Target URLs for Stage 2
	expect  []int          // Expected HTTP status codes
	logger  *slog.Logger
}

// New creates a new Pipeline.
func New(cfg *config.PipelineConfig, eng engine.Engine, targets []string, expect []int, logger *slog.Logger) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		cfg:     cfg,
		engine:  eng,
		targets: targets,
		expect:  expect,
		logger:  logger,
	}
}

// Result holds the outcome of running a config through the pipeline.
type Result struct {
	Config  *model.ProxyConfig
	Results []model.TestResult
	Failed  bool
	FailedStage model.TestStage
}

// Run executes the pipeline on a batch of configs.
// Each stage filters out failures before the next stage runs.
// Returns results for all configs (including failures).
func (p *Pipeline) Run(ctx context.Context, configs []*model.ProxyConfig) []Result {
	results := make([]Result, len(configs))
	for i, cfg := range configs {
		results[i] = Result{Config: cfg}
	}

	// Stage 0: DNS + TCP
	p.logger.Info("pipeline: stage 0 (DNS+TCP)", "configs", len(configs))
	survivors := p.runStage(ctx, configs, results, model.StageDNSTCP, p.cfg.Stage0Workers, p.cfg.Stage0Timeout, p.testDNSTCP)

	// Stage 1: TLS Handshake
	p.logger.Info("pipeline: stage 1 (TLS)", "configs", len(survivors))
	survivors = p.runStage(ctx, survivors, results, model.StageTLS, p.cfg.Stage1Workers, p.cfg.Stage1Timeout, p.testTLS)

	// Stage 2: Proxy Functional Test
	p.logger.Info("pipeline: stage 2 (proxy)", "configs", len(survivors))
	survivors = p.runStage(ctx, survivors, results, model.StageProxy, p.cfg.Stage2Workers, p.cfg.Stage2Timeout, p.testProxy)

	p.logger.Info("pipeline: complete",
		"total", len(configs),
		"passed", len(survivors),
		"failed", len(configs)-len(survivors),
	)

	return results
}

// testFunc is the signature for a stage test function.
type testFunc func(ctx context.Context, cfg *model.ProxyConfig) model.TestResult

// runStage runs a single pipeline stage with bounded concurrency.
func (p *Pipeline) runStage(
	ctx context.Context,
	configs []*model.ProxyConfig,
	allResults []Result,
	stage model.TestStage,
	maxWorkers int,
	timeout time.Duration,
	test testFunc,
) []*model.ProxyConfig {
	if len(configs) == 0 {
		return nil
	}

	// Create a map from config ID to result index for fast lookup
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

	for _, cfg := range configs {
		wg.Add(1)
		go func(c *model.ProxyConfig) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				resultCh <- stageResult{
					config: c,
					result: model.TestResult{
						ConfigID: c.ID,
						Stage:    stage,
						Success:  false,
						Error:    "context cancelled",
						TestedAt: time.Now(),
					},
				}
				return
			}

			// Run test with timeout
			testCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			tr := test(testCtx, c)
			tr.ConfigID = c.ID
			tr.Stage = stage
			tr.TestedAt = time.Now()

			resultCh <- stageResult{config: c, result: tr}
		}(cfg)
	}

	// Close channel when all workers finish
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results
	var survivors []*model.ProxyConfig
	for sr := range resultCh {
		idx := idxMap[sr.config.ID]
		allResults[idx].Results = append(allResults[idx].Results, sr.result)

		if sr.result.Success {
			survivors = append(survivors, sr.config)
		} else {
			allResults[idx].Failed = true
			allResults[idx].FailedStage = stage
			p.logger.Debug("config failed",
				"stage", stage.String(),
				"config", sr.config.DisplayName(),
				"error", sr.result.Error,
			)
		}
	}

	return survivors
}

// testDNSTCP performs Stage 0: DNS resolution and TCP connect.
func (p *Pipeline) testDNSTCP(ctx context.Context, cfg *model.ProxyConfig) model.TestResult {
	start := time.Now()
	addr := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)

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

	// Skip TLS test for non-TLS configs
	if cfg.Security == model.SecurityNone {
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

// testProxy performs Stage 2: functional proxy test via HTTP.
func (p *Pipeline) testProxy(ctx context.Context, cfg *model.ProxyConfig) model.TestResult {
	start := time.Now()

	// Create HTTP client that dials through the proxy engine
	transport := &http.Transport{
		DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			return p.engine.Dial(dialCtx, cfg, network, addr)
		},
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   p.cfg.Stage2Timeout,
		// Don't follow redirects
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Test against each target URL
	for _, rawTarget := range p.targets {
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
		for _, expected := range p.expect {
			if resp.StatusCode == expected {
				statusOK = true
				break
			}
		}
		if !statusOK && resp.StatusCode >= 200 && resp.StatusCode < 400 {
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
