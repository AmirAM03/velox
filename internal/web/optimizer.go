package web

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/config"
	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/ingest"
	loggerPkg "github.com/AmirAM03/velox/internal/logger"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/parser"
	"github.com/AmirAM03/velox/internal/pipeline"
	"github.com/AmirAM03/velox/internal/storage"
)

const (
	// shortcut: only the fastest reachable configs get the costly per-profile HTTP test;
	// raise it (or make it a setting) if profiles end up with too few working nodes.
	maxHTTPCandidates = 500
	nodesPerProfile   = 10

	settingRefreshInterval = "source_refresh_interval"
	settingCycleInterval   = "cycle_interval"
	settingConfigMaxAge    = "config_max_age"
	settingLastRefreshAt   = "last_source_refresh_at"
)

// OptimizerSettings are the admin-tunable schedule values.
type OptimizerSettings struct {
	RefreshInterval string `json:"refresh_interval"` // Go duration, e.g. "6h"
	CycleInterval   string `json:"cycle_interval"`   // Go duration, e.g. "30m"
	ConfigMaxAge    string `json:"config_max_age"`   // retention, e.g. "7d"
}

// OptimizerStatus is what the dashboard shows about the background work.
type OptimizerStatus struct {
	OptimizerSettings
	Phase         string     `json:"phase"` // "idle", "refreshing", "testing"
	LastRefreshAt *time.Time `json:"last_refresh_at,omitempty"`
	LastCycleAt   *time.Time `json:"last_cycle_at,omitempty"`
	NextRefreshAt *time.Time `json:"next_refresh_at,omitempty"`
	NextCycleAt   *time.Time `json:"next_cycle_at,omitempty"`
	LastSummary   string     `json:"last_summary"`
}

// Optimizer fetches subscriptions on a schedule and, every cycle, finds the
// best nodes for each profile's target service.
type Optimizer struct {
	store       *storage.Store
	pipelineCfg *config.PipelineConfig
	profiles    *ProfileManager
	logger      *slog.Logger
	broadcast   func(map[string]interface{})
	busy        func() bool // true while a manual benchmark runs; cycles wait for it

	mu            sync.Mutex
	settings      OptimizerSettings
	refreshEvery  time.Duration
	cycleEvery    time.Duration
	maxAge        time.Duration
	phase         string
	lastRefreshAt time.Time
	lastCycleAt   time.Time
	lastSummary   string
	trigger       chan string
}

// NewOptimizer loads saved settings.
func NewOptimizer(store *storage.Store, pipelineCfg *config.PipelineConfig, profiles *ProfileManager,
	logger *slog.Logger, broadcast func(map[string]interface{}), busy func() bool) *Optimizer {
	o := &Optimizer{
		store: store, pipelineCfg: pipelineCfg, profiles: profiles, logger: logger,
		broadcast: broadcast, busy: busy, phase: "idle",
		trigger: make(chan string, 1),
	}
	get := func(k, def string) string { v, _ := store.GetSetting(k, def); return v }
	if err := o.apply(OptimizerSettings{
		RefreshInterval: get(settingRefreshInterval, "6h"),
		CycleInterval:   get(settingCycleInterval, "30m"),
		ConfigMaxAge:    get(settingConfigMaxAge, "7d"),
	}); err != nil {
		logger.Warn("invalid saved optimizer settings, using defaults", "source", "optimizer", "error", err)
		_ = o.apply(OptimizerSettings{RefreshInterval: "6h", CycleInterval: "30m", ConfigMaxAge: "7d"})
	}
	if t := get(settingLastRefreshAt, ""); t != "" {
		o.lastRefreshAt, _ = time.Parse(time.RFC3339, t)
	}
	return o
}

// apply validates and sets the schedule. Callers hold no lock (it is only called before Start or under Configure).
func (o *Optimizer) apply(s OptimizerSettings) error {
	refresh, err := time.ParseDuration(s.RefreshInterval)
	if err != nil || refresh < 10*time.Minute {
		return fmt.Errorf("refresh interval must be a duration of at least 10m (e.g. 6h)")
	}
	cycle, err := time.ParseDuration(s.CycleInterval)
	if err != nil || cycle < 5*time.Minute {
		return fmt.Errorf("cycle interval must be a duration of at least 5m (e.g. 30m)")
	}
	maxAge, err := loggerPkg.ParseRetention(s.ConfigMaxAge)
	if err != nil || maxAge < 24*time.Hour {
		return fmt.Errorf("config max age must be at least 1d (e.g. 7d)")
	}
	o.settings, o.refreshEvery, o.cycleEvery, o.maxAge = s, refresh, cycle, maxAge
	return nil
}

// Configure validates, saves and applies new settings.
func (o *Optimizer) Configure(s OptimizerSettings) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.apply(s); err != nil {
		return err
	}
	for k, v := range map[string]string{
		settingRefreshInterval: s.RefreshInterval,
		settingCycleInterval:   s.CycleInterval,
		settingConfigMaxAge:    s.ConfigMaxAge,
	} {
		if err := o.store.SetSetting(k, v); err != nil {
			return fmt.Errorf("save %s: %w", k, err)
		}
	}
	return nil
}

// Trigger asks the loop to run "refresh" (fetch sources, then test) or "cycle" (test only).
// A request is dropped if one is already waiting.
func (o *Optimizer) Trigger(kind string) {
	select {
	case o.trigger <- kind:
	default:
	}
}

// Start runs the scheduler until ctx is done. The first test cycle runs right
// away; sources are fetched right away only if the last refresh is overdue.
func (o *Optimizer) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			o.mu.Lock()
			refreshDue := time.Since(o.lastRefreshAt) >= o.refreshEvery
			cycleDue := time.Since(o.lastCycleAt) >= o.cycleEvery
			o.mu.Unlock()

			switch {
			case refreshDue:
				o.run(ctx, true)
			case cycleDue && !o.busy():
				o.run(ctx, false)
			}

			select {
			case <-ctx.Done():
				return
			case kind := <-o.trigger:
				o.run(ctx, kind == "refresh")
			case <-ticker.C:
			}
		}
	}()
}

func (o *Optimizer) run(ctx context.Context, refresh bool) {
	if refresh {
		o.setPhase("refreshing")
		o.refreshSources(ctx)
	}
	o.setPhase("testing")
	summary := o.cycle(ctx)
	o.mu.Lock()
	o.lastCycleAt = time.Now()
	o.lastSummary = summary
	o.phase = "idle"
	o.mu.Unlock()
	o.broadcastStatus()
}

func (o *Optimizer) setPhase(p string) {
	o.mu.Lock()
	o.phase = p
	o.mu.Unlock()
	o.broadcastStatus()
}

// refreshSources fetches every enabled source, stores the parsed configs
// (deduplicated by canonical ID) and drops configs no source lists any more.
func (o *Optimizer) refreshSources(ctx context.Context) {
	sources, err := o.store.ListSources()
	if err != nil {
		o.logger.Error("list sources failed", "source", "optimizer", "error", err)
		return
	}
	var total, inserted int
	for _, src := range sources {
		if !src.Enabled || ctx.Err() != nil {
			continue
		}
		count, ins, err := o.fetchSource(src)
		_ = o.store.RecordSourceFetch(src.ID, count, err)
		if err != nil {
			o.logger.Warn("source fetch failed", "source", "optimizer", "url", src.URL, "error", err)
			continue
		}
		total += count
		inserted += ins
		o.logger.Info("source fetched", "source", "optimizer", "url", src.URL, "configs", count, "new", ins)
	}

	pruned, err := o.store.PruneStaleConfigs(o.maxAge)
	if err != nil {
		o.logger.Error("prune stale configs failed", "source", "optimizer", "error", err)
	}
	now := time.Now()
	_ = o.store.SetSetting(settingLastRefreshAt, now.Format(time.RFC3339))
	o.mu.Lock()
	o.lastRefreshAt = now
	o.mu.Unlock()
	o.logger.Info("sources refreshed", "source", "optimizer", "configs", total, "new", inserted, "pruned_stale", pruned)
}

func (o *Optimizer) fetchSource(src storage.Source) (count, inserted int, err error) {
	raw, err := ingest.FetchSubscription(src.URL, 0)
	if err != nil {
		return 0, 0, err
	}
	cfgs, _, _ := parser.ParseManyDetailed(ingest.CleanRawContent(raw))
	if len(cfgs) == 0 {
		return 0, 0, fmt.Errorf("no valid configs in response")
	}
	for _, c := range cfgs {
		c.Source = src.URL
	}
	inserted, _, err = o.store.UpsertConfigs(cfgs)
	return len(cfgs), inserted, err
}

// cycle runs the reachability steps once over all configs, then the HTTP step
// once per profile against that profile's target, and hands each profile its
// best nodes. Profiles where nothing passes keep their previous nodes.
func (o *Optimizer) cycle(ctx context.Context) string {
	profiles, err := o.store.ListProfiles()
	if err != nil {
		return "error: " + err.Error()
	}
	var active []storage.Profile
	for _, p := range profiles {
		if p.Enabled {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return "no enabled profiles"
	}
	configs, err := o.store.ListConfigsFiltered("", 0)
	if err != nil || len(configs) == 0 {
		return "no configs; add a subscription source"
	}

	chain, err := o.store.GetBenchmarkMethodology()
	if err != nil || chain == nil {
		chain = model.DefaultMethodologyChain()
	}
	baseSteps, httpStep := splitChain(chain)

	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	eng := engine.NewXrayEngine(o.logger) // throwaway test engine; never the serving one
	defer eng.Close()

	start := time.Now()
	candidates := configs
	if len(baseSteps) > 0 {
		p := pipeline.New(o.pipelineCfg, eng, nil, nil, o.logger)
		p.SetMethodology(&model.BenchmarkMethodologyChain{ScoringMode: chain.ScoringMode, Steps: baseSteps})
		candidates = reachable(p.Run(ctx, configs), baseSteps[len(baseSteps)-1].ID)
	}
	o.logger.Info("reachability done", "source", "optimizer", "configs", len(configs), "reachable", len(candidates))

	var parts []string
	for _, prof := range active {
		if ctx.Err() != nil {
			break
		}
		step := httpStep
		step.TargetURL = prof.TargetURL
		step.ExpectCodes = nil // any 2xx/3xx: arbitrary services answer with redirects or 200s
		p := pipeline.New(o.pipelineCfg, eng, nil, nil, o.logger)
		p.SetMethodology(&model.BenchmarkMethodologyChain{ScoringMode: chain.ScoringMode, Steps: []model.TestStepConfig{step}})

		nodes, cfgs := rankProfileNodes(p.Run(ctx, candidates))
		if len(nodes) == 0 {
			o.logger.Warn("no node reached profile target; keeping previous nodes", "source", "optimizer", "profile", prof.Name, "target", prof.TargetURL)
			parts = append(parts, fmt.Sprintf("%s: 0 working", prof.Name))
			continue
		}
		if err := o.store.ReplaceProfileNodes(prof.ID, nodes); err != nil {
			o.logger.Error("save profile nodes failed", "source", "optimizer", "profile", prof.Name, "error", err)
			continue
		}
		o.profiles.SetNodes(prof.ID, cfgs)
		o.logger.Info("profile updated", "source", "optimizer", "profile", prof.Name,
			"best", cfgs[0].DisplayName(), "latency_ms", nodes[0].LatencyMS)
		parts = append(parts, fmt.Sprintf("%s: %.0f ms", prof.Name, nodes[0].LatencyMS))
	}
	o.profiles.ReleaseUnused()

	return fmt.Sprintf("%d configs, %d reachable, %s (%s)", len(configs), len(candidates),
		joinOr(parts, "no profiles tested"), time.Since(start).Round(time.Second))
}

// splitChain separates the admin's methodology into the shared reachability
// steps and the HTTP step that is re-run per profile target.
func splitChain(chain *model.BenchmarkMethodologyChain) (base []model.TestStepConfig, http model.TestStepConfig) {
	found := false
	for _, s := range chain.ActiveSteps() {
		if s.Type == model.MethodologyHTTPDelay {
			if !found {
				http, found = s, true
			}
			continue
		}
		s.Necessary = true // only configs passing every base step reach the HTTP test
		base = append(base, s)
	}
	if !found {
		for _, s := range model.DefaultMethodologyChain().Steps {
			if s.Type == model.MethodologyHTTPDelay {
				http = s
			}
		}
	}
	http.Necessary = true
	return base, http
}

// reachable returns configs that passed the last base step, fastest first, capped.
func reachable(results []pipeline.Result, lastStepID string) []*model.ProxyConfig {
	type c struct {
		cfg *model.ProxyConfig
		lat time.Duration
	}
	var ok []c
	for _, r := range results {
		var total time.Duration
		passed := false
		for _, tr := range r.Results {
			total += tr.Latency
			if tr.StepID == lastStepID && tr.Success {
				passed = true
			}
		}
		if passed && !r.Failed {
			ok = append(ok, c{r.Config, total})
		}
	}
	sort.Slice(ok, func(i, j int) bool { return ok[i].lat < ok[j].lat })
	if len(ok) > maxHTTPCandidates {
		ok = ok[:maxHTTPCandidates]
	}
	out := make([]*model.ProxyConfig, len(ok))
	for i := range ok {
		out[i] = ok[i].cfg
	}
	return out
}

// rankProfileNodes keeps configs whose HTTP test passed, fastest first, top N.
func rankProfileNodes(results []pipeline.Result) ([]storage.ProfileNode, []*model.ProxyConfig) {
	var nodes []storage.ProfileNode
	byID := map[string]*model.ProxyConfig{}
	for _, r := range results {
		if r.Failed || len(r.Results) == 0 || !r.Results[len(r.Results)-1].Success {
			continue
		}
		tr := r.Results[len(r.Results)-1]
		nodes = append(nodes, storage.ProfileNode{
			ConfigID: r.Config.ID, LatencyMS: float64(tr.Latency.Microseconds()) / 1000, TestedAt: tr.TestedAt,
		})
		byID[r.Config.ID] = r.Config
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].LatencyMS < nodes[j].LatencyMS })
	if len(nodes) > nodesPerProfile {
		nodes = nodes[:nodesPerProfile]
	}
	cfgs := make([]*model.ProxyConfig, len(nodes))
	for i := range nodes {
		nodes[i].Rank = i + 1
		cfgs[i] = byID[nodes[i].ConfigID]
	}
	return nodes, cfgs
}

func joinOr(parts []string, empty string) string {
	if len(parts) == 0 {
		return empty
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "; " + p
	}
	return out
}

// Status reports the schedule and the outcome of the last cycle.
func (o *Optimizer) Status() OptimizerStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	st := OptimizerStatus{OptimizerSettings: o.settings, Phase: o.phase, LastSummary: o.lastSummary}
	ptr := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	st.LastRefreshAt = ptr(o.lastRefreshAt)
	st.LastCycleAt = ptr(o.lastCycleAt)
	if !o.lastRefreshAt.IsZero() {
		st.NextRefreshAt = ptr(o.lastRefreshAt.Add(o.refreshEvery))
	}
	if !o.lastCycleAt.IsZero() {
		st.NextCycleAt = ptr(o.lastCycleAt.Add(o.cycleEvery))
	}
	return st
}

func (o *Optimizer) broadcastStatus() {
	if o.broadcast != nil {
		o.broadcast(map[string]interface{}{"type": "optimizer_status", "status": o.Status()})
	}
}
