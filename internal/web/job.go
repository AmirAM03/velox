package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/pipeline"
	"github.com/AmirAM03/velox/internal/scorer"
	"github.com/AmirAM03/velox/internal/storage"
)

// BenchmarkStatus represents current job state.
type BenchmarkStatus string

const (
	StatusIdle      BenchmarkStatus = "idle"
	StatusRunning   BenchmarkStatus = "running"
	StatusCompleted BenchmarkStatus = "completed"
	StatusCancelled BenchmarkStatus = "cancelled"
)

// LogEntry represents an operation log line in the UI console.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`  // "info", "success", "warn", "error"
	Source    string `json:"source"` // "ingest", "test", "proxy", "system", "dedup"
	Message   string `json:"message"`
}

// NodeResultDetail represents a single tested node streamed to the client.
type NodeResultDetail struct {
	ConfigID  string  `json:"config_id"`
	Name      string  `json:"name"`
	Protocol  string  `json:"protocol"`
	Address   string  `json:"address"`
	Port      int     `json:"port"`
	Success   bool    `json:"success"`
	LatencyMS float64 `json:"latency_ms"`
	Stage     string  `json:"stage"`
	Error     string  `json:"error,omitempty"`
}

// BenchmarkJobManager manages background pipeline tests and real-time streaming.
type BenchmarkJobManager struct {
	mu           sync.RWMutex
	status       BenchmarkStatus
	target       string
	threads      int
	total        int
	tested       int
	passed       int
	failed       int
	fastestMS    float64
	currentStage string
	startTime    time.Time
	cancelFunc   context.CancelFunc
	results      []NodeResultDetail
	logs         []LogEntry
	subscribers  map[chan []byte]struct{}
	logger       *slog.Logger
	store        *storage.Store
	pipelineCfg  *config.PipelineConfig
	scoringCfg   config.ScoringConfig
}

// NewBenchmarkJobManager creates a new job manager.
func NewBenchmarkJobManager(store *storage.Store, pipelineCfg *config.PipelineConfig, scoringCfg config.ScoringConfig, logger *slog.Logger) *BenchmarkJobManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &BenchmarkJobManager{
		status:       StatusIdle,
		subscribers:  make(map[chan []byte]struct{}),
		logger:       logger,
		store:        store,
		pipelineCfg:  pipelineCfg,
		scoringCfg:   scoringCfg,
		currentStage: "Idle",
	}
}

// Subscribe adds an SSE subscriber channel and sends initial snapshot state.
func (m *BenchmarkJobManager) Subscribe() chan []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := make(chan []byte, 100)
	m.subscribers[ch] = struct{}{}

	// Send current snapshot immediately
	snapshot := map[string]interface{}{
		"type":          "snapshot",
		"status":        m.status,
		"target":        m.target,
		"threads":       m.threads,
		"total":         m.total,
		"tested":        m.tested,
		"passed":        m.passed,
		"failed":        m.failed,
		"fastest_ms":    m.fastestMS,
		"current_stage": m.currentStage,
		"results":       m.results,
		"logs":          m.logs,
	}
	data, _ := json.Marshal(snapshot)
	ch <- []byte(fmt.Sprintf("data: %s\n\n", data))

	return ch
}

// Unsubscribe removes an SSE subscriber channel.
func (m *BenchmarkJobManager) Unsubscribe(ch chan []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.subscribers, ch)
	close(ch)
}

// broadcast sends an SSE event to all connected listeners.
func (m *BenchmarkJobManager) broadcast(event map[string]interface{}) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	msg := []byte(fmt.Sprintf("data: %s\n\n", data))

	m.mu.RLock()
	defer m.mu.RUnlock()

	for ch := range m.subscribers {
		select {
		case ch <- msg:
		default:
			// Client buffer full, skip to avoid blocking other subscribers
		}
	}
}

// Broadcast sends an SSE event to all connected listeners.
func (m *BenchmarkJobManager) Broadcast(event map[string]interface{}) {
	m.broadcast(event)
}

// Log adds a log entry to memory, broadcasts to UI, and outputs to slog.
func (m *BenchmarkJobManager) Log(level, source, message string) {
	entry := LogEntry{
		Timestamp: time.Now().Format("15:04:05"),
		Level:     level,
		Source:    source,
		Message:   message,
	}

	m.mu.Lock()
	m.logs = append(m.logs, entry)
	if len(m.logs) > 500 {
		m.logs = m.logs[len(m.logs)-500:]
	}
	m.mu.Unlock()

	// Broadcast to web console
	m.broadcast(map[string]interface{}{
		"type":  "log",
		"entry": entry,
	})

	// CLI visibility
	switch level {
	case "error":
		m.logger.Error(message, "source", source)
	case "warn":
		m.logger.Warn(message, "source", source)
	default:
		m.logger.Info(message, "source", source)
	}
}

// BroadcastAppLog sends a persistent application log record to connected SSE clients.
func (m *BenchmarkJobManager) BroadcastAppLog(rec *storage.LogRecord) {
	if rec == nil {
		return
	}
	m.broadcast(map[string]interface{}{
		"type": "app_log",
		"log":  rec,
	})
}


// Start begins a benchmark on the given configs.
func (m *BenchmarkJobManager) Start(target string, threads int, configs []*model.ProxyConfig) error {
	m.mu.Lock()
	if m.status == StatusRunning {
		m.mu.Unlock()
		return fmt.Errorf("a benchmark is already currently running")
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.status = StatusRunning
	m.target = target
	m.threads = threads
	m.total = len(configs)
	m.tested = 0
	m.passed = 0
	m.failed = 0
	m.fastestMS = 0
	m.currentStage = "Stage 0 (DNS+TCP Reachability)"
	m.startTime = time.Now()
	m.cancelFunc = cancel
	m.results = nil
	m.mu.Unlock()

	m.Log("info", "test", fmt.Sprintf("Started benchmark on %d configs (Threads: %d, Target: %s)", len(configs), threads, target))

	m.broadcast(map[string]interface{}{
		"type":          "start",
		"target":        target,
		"threads":       threads,
		"total":         len(configs),
		"current_stage": m.currentStage,
	})

	go m.runPipeline(ctx, configs, target, threads)
	return nil
}

// Cancel stops the running benchmark.
func (m *BenchmarkJobManager) Cancel() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.status != StatusRunning || m.cancelFunc == nil {
		return false
	}

	m.cancelFunc()
	m.status = StatusCancelled
	m.currentStage = "Cancelled"
	m.Log("warn", "test", "Benchmark was cancelled by user")

	m.broadcast(map[string]interface{}{
		"type":    "cancelled",
		"message": "Benchmark cancelled by user",
	})
	return true
}

func (m *BenchmarkJobManager) runPipeline(ctx context.Context, configs []*model.ProxyConfig, target string, threads int) {
	eng := engine.NewXrayEngine(m.logger)
	defer eng.Close()

	chain, err := m.store.GetBenchmarkMethodology()
	if err != nil || chain == nil {
		chain = model.DefaultMethodologyChain()
	}

	// If a custom target was supplied in run options, apply it to HTTP delay steps
	if strings.TrimSpace(target) != "" {
		for i := range chain.Steps {
			if chain.Steps[i].Type == model.MethodologyHTTPDelay {
				chain.Steps[i].TargetURL = target
			}
		}
	}

	p := pipeline.New(m.pipelineCfg, eng, []string{target}, []int{200, 204}, m.logger)
	p.SetConcurrency(threads)
	p.SetMethodology(chain)

	activeSteps := chain.ActiveSteps()
	var lastStepID string
	if len(activeSteps) > 0 {
		lastStepID = activeSteps[len(activeSteps)-1].ID
	}

	// Attach live progress callback
	p.SetOnProgress(func(stage model.TestStage, current, total, stagePassed, stageFailed int, result *model.TestResult, cfg *model.ProxyConfig) {
		m.mu.Lock()
		stageName := stage.String()
		switch stage {
		case model.StageDNSTCP:
			m.currentStage = fmt.Sprintf("TCP Reachability (%d/%d)", current, total)
		case model.StageTLS:
			m.currentStage = fmt.Sprintf("TLS Handshake (%d/%d)", current, total)
		case model.StageProxy:
			m.currentStage = fmt.Sprintf("HTTP Delay (%d/%d)", current, total)
		default:
			m.currentStage = fmt.Sprintf("Testing (%d/%d)", current, total)
		}
		m.mu.Unlock()

		// Stream individual proxy test completion when it finishes its last active step or when it fails
		isFinished := !result.Success || (result.StepID != "" && result.StepID == lastStepID) || stage == model.StageProxy || len(activeSteps) <= 1
		if isFinished {
			latMS := float64(result.Latency.Milliseconds())
			name := cfg.DisplayName()
			if name == "" {
				name = cfg.Address
			}

			detail := NodeResultDetail{
				ConfigID:  cfg.ID,
				Name:      name,
				Protocol:  string(cfg.Protocol),
				Address:   cfg.Address,
				Port:      cfg.Port,
				Success:   result.Success,
				LatencyMS: latMS,
				Stage:     stageName,
				Error:     result.Error,
			}

			m.mu.Lock()
			if result.Success {
				m.passed++
				if m.fastestMS == 0 || (latMS > 0 && latMS < m.fastestMS) {
					m.fastestMS = latMS
				}
			} else {
				m.failed++
			}
			m.tested++
			m.results = append(m.results, detail)

			curTested := m.tested
			curPassed := m.passed
			curFailed := m.failed
			curFastest := m.fastestMS
			curStage := m.currentStage
			m.mu.Unlock()

			m.broadcast(map[string]interface{}{
				"type":          "node_result",
				"detail":        detail,
				"tested":        curTested,
				"passed":        curPassed,
				"failed":        curFailed,
				"fastest_ms":    curFastest,
				"current_stage": curStage,
			})
		}
	})

	results := p.Run(ctx, configs)

	// Process and store results
	sc := scorer.New(m.scoringCfg)
	var finalPassed, finalFailed int
	var fastestMS float64

	for _, r := range results {
		if !r.Failed {
			finalPassed++
			for _, tr := range r.Results {
				if tr.Success && (tr.Stage == model.StageProxy || len(activeSteps) == 1) {
					latMS := float64(tr.Latency.Milliseconds())
					if fastestMS == 0 || (latMS > 0 && latMS < fastestMS) {
						fastestMS = latMS
					}
				}
			}
		} else {
			finalFailed++
		}

		if err := m.store.InsertTestResults(r.Results); err != nil {
			m.logger.Warn("failed to store test results", "error", err)
		}
		existing, _ := m.store.GetScore(r.Config.ID)
		score := sc.Compute(r.Config.ID, r.Results, existing, chain)
		_ = m.store.UpsertScore(score)
	}

	m.mu.Lock()
	if m.status != StatusCancelled {
		m.status = StatusCompleted
		m.currentStage = "Benchmark Completed"
	}
	m.passed = finalPassed
	m.failed = finalFailed
	m.fastestMS = fastestMS
	duration := time.Since(m.startTime).Seconds()

	// Sort final details in memory so snapshot has best first
	sort.Slice(m.results, func(i, j int) bool {
		if m.results[i].Success != m.results[j].Success {
			return m.results[i].Success
		}
		if m.results[i].Success && m.results[j].Success {
			return m.results[i].LatencyMS < m.results[j].LatencyMS
		}
		return false
	})
	m.mu.Unlock()

	m.Log("success", "test", fmt.Sprintf("Benchmark finished in %.1fs: %d passed, %d failed (Fastest: %.0f ms)", duration, finalPassed, finalFailed, fastestMS))

	m.broadcast(map[string]interface{}{
		"type":          "complete",
		"total":         len(configs),
		"passed":        finalPassed,
		"failed":        finalFailed,
		"fastest_ms":    fastestMS,
		"duration_s":    duration,
		"current_stage": "Completed",
	})
}
