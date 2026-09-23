package web

import (
	"context"
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

// RotationHistoryEntry documents a single auto-rotation switch.
type RotationHistoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	FromNode  string    `json:"from_node"`
	ToNode    string    `json:"to_node"`
	ToNodeID  string    `json:"to_node_id"`
	Protocol  string    `json:"protocol"`
	LatencyMS float64   `json:"latency_ms"`
	Score     float64   `json:"score"`
	Reason    string    `json:"reason"`
}

// RotationStatus summarizes current auto-rotation telemetry and state.
type RotationStatus struct {
	Enabled             bool                   `json:"enabled"`
	Interval            string                 `json:"interval"`
	IntervalSeconds     int                    `json:"interval_seconds"`
	Target              string                 `json:"target"`
	Threads             int                    `json:"threads"`
	IsBenchmarking      bool                   `json:"is_benchmarking"`
	LastRotatedAt       *time.Time             `json:"last_rotated_at,omitempty"`
	NextRotationAt      *time.Time             `json:"next_rotation_at,omitempty"`
	NextRotationSeconds int                    `json:"next_rotation_seconds"`
	ActiveNode          *model.ProxyConfig     `json:"active_node,omitempty"`
	PoolSize            int64                  `json:"pool_size"`
	WorkingInPool       int64                  `json:"working_in_pool"`
	TotalRotations      int                    `json:"total_rotations"`
	History             []RotationHistoryEntry `json:"history"`
}

// RotationManager coordinates auto-rotation pool benchmarking and seamless best-node activation.
type RotationManager struct {
	mu             sync.RWMutex
	store          *storage.Store
	pipelineCfg    *config.PipelineConfig
	scoringCfg     config.ScoringConfig
	logger         *slog.Logger
	connectProxy   func(cfg *model.ProxyConfig) error
	broadcastEvent func(event map[string]interface{})

	enabled        bool
	interval       time.Duration
	intervalStr    string
	target         string
	threads        int
	isBenchmarking bool
	lastRotatedAt  time.Time
	nextRotationAt time.Time
	activeNode     *model.ProxyConfig
	totalRotations int
	history        []RotationHistoryEntry

	stopCh    chan struct{}
	triggerCh chan struct{}
}

// NewRotationManager initializes the auto-rotation manager.
func NewRotationManager(
	store *storage.Store,
	pipelineCfg *config.PipelineConfig,
	scoringCfg config.ScoringConfig,
	logger *slog.Logger,
	connectProxy func(cfg *model.ProxyConfig) error,
	broadcastEvent func(event map[string]interface{}),
) *RotationManager {
	if logger == nil {
		logger = slog.Default()
	}

	rm := &RotationManager{
		store:          store,
		pipelineCfg:    pipelineCfg,
		scoringCfg:     scoringCfg,
		logger:         logger,
		connectProxy:   connectProxy,
		broadcastEvent: broadcastEvent,
		enabled:        false,
		interval:       15 * time.Minute,
		intervalStr:    "15m",
		target:         "https://www.google.com/generate_204",
		threads:        50,
		triggerCh:      make(chan struct{}, 1),
		history:        make([]RotationHistoryEntry, 0),
	}

	// Restore settings from SQLite if present
	if en, err := store.GetSetting("rotation_enabled", "false"); err == nil && en == "true" {
		rm.enabled = true
	}
	if iv, err := store.GetSetting("rotation_interval", "15m"); err == nil && iv != "" {
		rm.intervalStr = iv
		if dur, err := parseRotationInterval(iv); err == nil {
			rm.interval = dur
		}
	}
	if tg, err := store.GetSetting("rotation_target", ""); err == nil && tg != "" {
		rm.target = tg
	}

	return rm
}

// Start begins the background rotation scheduler loop.
func (m *RotationManager) Start(ctx context.Context) {
	m.mu.Lock()
	m.stopCh = make(chan struct{})
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			case <-m.triggerCh:
				m.runCycle()
			case <-ticker.C:
				m.mu.Lock()
				shouldRun := false
				if m.enabled && !m.isBenchmarking {
					if m.nextRotationAt.IsZero() || time.Now().After(m.nextRotationAt) {
						shouldRun = true
					}
				}
				m.mu.Unlock()

				if shouldRun {
					m.runCycle()
				}
			}
		}
	}()
}

// Stop gracefully shuts down the background rotation loop.
func (m *RotationManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopCh != nil {
		close(m.stopCh)
		m.stopCh = nil
	}
}

// Configure updates auto-rotation settings and resets timer if needed.
func (m *RotationManager) Configure(enabled bool, intervalStr string, target string, threads int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if intervalStr != "" {
		dur, err := parseRotationInterval(intervalStr)
		if err != nil {
			return err
		}
		m.interval = dur
		m.intervalStr = intervalStr
	}

	if target != "" {
		m.target = target
	}
	if threads > 0 {
		m.threads = threads
	}

	prevEnabled := m.enabled
	m.enabled = enabled

	// Persist to settings
	_ = m.store.SetSetting("rotation_enabled", fmt.Sprintf("%t", enabled))
	_ = m.store.SetSetting("rotation_interval", m.intervalStr)
	_ = m.store.SetSetting("rotation_target", m.target)

	if enabled {
		if !prevEnabled || m.nextRotationAt.IsZero() {
			m.nextRotationAt = time.Now() // Run immediately on enable
		}
	} else {
		m.nextRotationAt = time.Time{}
	}

	m.logger.Info("auto-rotation configuration updated",
		slog.String("source", "system"),
		slog.Bool("enabled", enabled),
		slog.String("interval", m.intervalStr),
		slog.String("target", m.target),
	)

	return nil
}

// TriggerNow schedules an immediate rotation cycle.
func (m *RotationManager) TriggerNow() {
	select {
	case m.triggerCh <- struct{}{}:
	default:
	}
}

// runCycle runs a benchmark across all nodes in the pool and activates the best node.
func (m *RotationManager) runCycle() {
	m.mu.Lock()
	if m.isBenchmarking {
		m.mu.Unlock()
		return
	}
	m.isBenchmarking = true
	target := m.target
	threads := m.threads
	interval := m.interval
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.isBenchmarking = false
		m.nextRotationAt = time.Now().Add(interval)
		m.mu.Unlock()
		m.broadcastStatus()
	}()

	m.broadcastStatus()

	// 1. Fetch pool configs
	poolItems, err := m.store.GetRotationPoolConfigs()
	if err != nil || len(poolItems) == 0 {
		m.logger.Info("auto-rotation: pool is empty, skipping cycle",
			slog.String("source", "system"),
		)
		return
	}

	ids := make([]string, len(poolItems))
	for i, item := range poolItems {
		ids[i] = item.ID
	}

	configs, err := m.store.GetConfigsByIDs(ids)
	if err != nil || len(configs) == 0 {
		return
	}

	m.logger.Info("auto-rotation cycle started: benchmarking pool",
		slog.String("source", "system"),
		slog.Int("pool_size", len(configs)),
		slog.String("target", target),
	)

	// 2. Run pipeline benchmark
	eng := engine.NewXrayEngine(m.logger)
	defer eng.Close()

	chain, err := m.store.GetBenchmarkMethodology()
	if err != nil || chain == nil {
		chain = model.DefaultMethodologyChain()
	}

	p := pipeline.New(m.pipelineCfg, eng, []string{target}, []int{200, 204}, m.logger)
	p.SetConcurrency(threads)
	p.SetMethodology(chain)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	results := p.Run(ctx, configs)

	// 3. Process scores and identify working nodes
	sc := scorer.New(m.scoringCfg)
	type candidate struct {
		config    *model.ProxyConfig
		score     *model.Score
		latencyMS float64
	}
	var candidates []candidate

	for _, r := range results {
		_ = m.store.InsertTestResults(r.Results)
		existing, _ := m.store.GetScore(r.Config.ID)
		score := sc.Compute(r.Config.ID, r.Results, existing, chain)
		_ = m.store.UpsertScore(score)

		if !r.Failed {
			var latMS float64
			for _, tr := range r.Results {
				if tr.Success && (tr.Stage == model.StageProxy || len(chain.ActiveSteps()) == 1) {
					latMS = float64(tr.Latency.Milliseconds())
				}
			}
			candidates = append(candidates, candidate{
				config:    r.Config,
				score:     score,
				latencyMS: latMS,
			})
		}
	}

	if len(candidates) == 0 {
		m.logger.Warn("auto-rotation: all nodes in rotation pool failed test",
			slog.String("source", "system"),
			slog.Int("tested_nodes", len(configs)),
		)
		return
	}

	// 4. Select best candidate (lowest composite score, then lowest latency)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score.Composite != candidates[j].score.Composite {
			return candidates[i].score.Composite < candidates[j].score.Composite
		}
		return candidates[i].latencyMS < candidates[j].latencyMS
	})

	best := candidates[0].config
	bestScore := candidates[0].score.Composite
	bestLat := candidates[0].latencyMS

	m.mu.Lock()
	prevName := "None"
	if m.activeNode != nil {
		prevName = m.activeNode.DisplayName()
	}
	m.activeNode = best
	m.lastRotatedAt = time.Now()
	m.totalRotations++

	entry := RotationHistoryEntry{
		Timestamp: time.Now(),
		FromNode:  prevName,
		ToNode:    best.DisplayName(),
		ToNodeID:  best.ID,
		Protocol:  string(best.Protocol),
		LatencyMS: bestLat,
		Score:     bestScore,
		Reason:    fmt.Sprintf("Best composite score (%.3f, %.0fms)", bestScore, bestLat),
	}
	m.history = append([]RotationHistoryEntry{entry}, m.history...)
	if len(m.history) > 50 {
		m.history = m.history[:50]
	}
	m.mu.Unlock()

	// 5. Connect the newly selected best node
	if m.connectProxy != nil {
		if err := m.connectProxy(best); err != nil {
			m.logger.Error("auto-rotation: failed to connect best node",
				slog.String("source", "system"),
				slog.String("node_id", best.ID),
				slog.String("error", err.Error()),
			)
			return
		}
	}

	m.logger.Info("auto-rotation: switched active proxy node",
		slog.String("source", "system"),
		slog.String("from", prevName),
		slog.String("to", best.DisplayName()),
		slog.Float64("latency_ms", bestLat),
		slog.Float64("score", bestScore),
	)
}

// GetStatus returns the current status and metrics of the rotation manager.
func (m *RotationManager) GetStatus() RotationStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total, working, _ := m.store.GetRotationPoolStats()

	status := RotationStatus{
		Enabled:             m.enabled,
		Interval:            m.intervalStr,
		IntervalSeconds:     int(m.interval.Seconds()),
		Target:              m.target,
		Threads:             m.threads,
		IsBenchmarking:      m.isBenchmarking,
		ActiveNode:          m.activeNode,
		PoolSize:            total,
		WorkingInPool:       working,
		TotalRotations:      m.totalRotations,
		History:             m.history,
		NextRotationSeconds: 0,
	}

	if !m.lastRotatedAt.IsZero() {
		t := m.lastRotatedAt
		status.LastRotatedAt = &t
	}
	if !m.nextRotationAt.IsZero() {
		t := m.nextRotationAt
		status.NextRotationAt = &t
		rem := int(time.Until(t).Seconds())
		if rem < 0 {
			rem = 0
		}
		status.NextRotationSeconds = rem
	}

	return status
}

// SetActiveNode updates the currently active node reference.
func (m *RotationManager) SetActiveNode(cfg *model.ProxyConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeNode = cfg
}

func (m *RotationManager) broadcastStatus() {
	if m.broadcastEvent != nil {
		m.broadcastEvent(map[string]interface{}{
			"type":   "rotation_status",
			"status": m.GetStatus(),
		})
	}
}

// parseRotationInterval converts string interval (e.g. "2m", "5m", "15m", "1h") into time.Duration.
func parseRotationInterval(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 15 * time.Minute, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid interval format %q (use e.g. 5m, 15m, 1h): %w", s, err)
	}
	if d < 30*time.Second {
		return 0, fmt.Errorf("rotation interval must be at least 30 seconds")
	}
	return d, nil
}
