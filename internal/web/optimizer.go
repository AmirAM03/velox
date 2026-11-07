package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/bench"
	"github.com/AmirAM03/velox/internal/checks"
	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/ingest"
	loggerPkg "github.com/AmirAM03/velox/internal/logger"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/parser"
	"github.com/AmirAM03/velox/internal/storage"
)

const (
	nodesPerProfile = 10

	settingRefreshInterval = "source_refresh_interval"
	settingConfigMaxAge    = "config_max_age"
	settingLastRefreshAt   = "last_source_refresh_at"
)

// OptimizerSettings are the product-wide subscription options. Per-profile test
// scheduling is not here: each profile owns its own on its card.
type OptimizerSettings struct {
	RefreshInterval string `json:"refresh_interval"` // how often to fetch subscriptions, e.g. "6h"
	ConfigMaxAge    string `json:"config_max_age"`   // config retention, e.g. "7d"
}

// OptimizerStatus is what the dashboard shows about the background work.
type OptimizerStatus struct {
	OptimizerSettings
	Phase         string     `json:"phase"` // "idle", "refreshing", "testing"
	LastRefreshAt *time.Time `json:"last_refresh_at,omitempty"`
	NextRefreshAt *time.Time `json:"next_refresh_at,omitempty"`
	LastSummary   string     `json:"last_summary"`
	// Progress of profiles under test right now, by profile ID.
	Progress map[int64]string `json:"progress,omitempty"`
}

// Optimizer fetches subscriptions on a schedule and, every cycle, finds the
// best nodes for each profile's target service.
type Optimizer struct {
	store     *storage.Store
	profiles  *ProfileManager
	logger    *slog.Logger
	broadcast func(any)

	mu            sync.Mutex
	settings      OptimizerSettings
	refreshEvery  time.Duration
	maxAge        time.Duration
	phase         string
	lastRefreshAt time.Time
	lastSummary   string
	progress      map[int64]string
	lastBroadcast time.Time

	// Queued requests, merged so none is lost; wake tells the loop to look.
	pendRefresh bool           // fetch subscriptions
	pendIDs     map[int64]bool // test these profiles
	wake        chan struct{}
}

// NewOptimizer loads saved settings.
func NewOptimizer(store *storage.Store, profiles *ProfileManager, logger *slog.Logger, broadcast func(any)) *Optimizer {
	o := &Optimizer{
		store: store, profiles: profiles, logger: logger, broadcast: broadcast, phase: "idle",
		progress: map[int64]string{}, pendIDs: map[int64]bool{}, wake: make(chan struct{}, 1),
	}
	get := func(k, def string) string { v, _ := store.GetSetting(k, def); return v }
	if err := o.apply(OptimizerSettings{
		RefreshInterval: get(settingRefreshInterval, "6h"),
		ConfigMaxAge:    get(settingConfigMaxAge, "7d"),
	}); err != nil {
		logger.Warn("invalid saved optimizer settings, using defaults", "source", "optimizer", "error", err)
		_ = o.apply(OptimizerSettings{RefreshInterval: "6h", ConfigMaxAge: "7d"})
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
	maxAge, err := loggerPkg.ParseRetention(s.ConfigMaxAge)
	if err != nil || maxAge < 24*time.Hour {
		return fmt.Errorf("config max age must be at least 1d (e.g. 7d)")
	}
	o.settings, o.refreshEvery, o.maxAge = s, refresh, maxAge
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
		settingConfigMaxAge:    s.ConfigMaxAge,
	} {
		if err := o.store.SetSetting(k, v); err != nil {
			return fmt.Errorf("save %s: %w", k, err)
		}
	}
	return nil
}

// TriggerRefresh queues a subscription fetch. It is for automatic callers (a new
// source), which queue behind any live run; the manual endpoint uses TryTriggerRefresh.
func (o *Optimizer) TriggerRefresh() {
	o.mu.Lock()
	o.pendRefresh = true
	o.mu.Unlock()
	o.signal()
}

// TriggerProfile queues one profile's test. For automatic callers (a saved profile).
func (o *Optimizer) TriggerProfile(id int64) {
	o.mu.Lock()
	o.pendIDs[id] = true
	o.mu.Unlock()
	o.signal()
}

// busyLocked reports a run in progress or already queued; the caller holds o.mu.
func (o *Optimizer) busyLocked() bool {
	return o.phase != "idle" || o.pendRefresh || len(o.pendIDs) > 0
}

// errBusy is returned when a new run is asked for while one is running or queued.
var errBusy = fmt.Errorf("a benchmark is already running; wait for it to finish before starting another")

// TryTriggerRefresh queues a manual subscription fetch, or refuses if a run is
// already running or queued, so a second run never starts on a live one.
func (o *Optimizer) TryTriggerRefresh() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.busyLocked() {
		return errBusy
	}
	o.pendRefresh = true
	o.signal() // signal never takes o.mu, so it is safe to call while holding it
	return nil
}

// TryTriggerProfile queues one profile's test, or refuses when a run is active or queued.
func (o *Optimizer) TryTriggerProfile(id int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.busyLocked() {
		return errBusy
	}
	o.pendIDs[id] = true
	o.signal()
	return nil
}

func (o *Optimizer) signal() {
	select {
	case o.wake <- struct{}{}:
	default: // the loop is already due to look at the queue
	}
}

// Start runs the scheduler until ctx is done. Scheduled work (fetch when due,
// test when due) covers only auto-test profiles; queued requests run as asked.
func (o *Optimizer) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			o.mu.Lock()
			refresh := o.pendRefresh || time.Since(o.lastRefreshAt) >= o.refreshEvery
			ids := o.pendIDs
			o.pendRefresh, o.pendIDs = false, map[int64]bool{}
			o.mu.Unlock()

			for id := range o.dueProfiles() { // each profile's own auto-test interval
				ids[id] = true
			}
			if refresh || len(ids) > 0 {
				o.run(ctx, refresh, ids)
			}

			select {
			case <-ctx.Done():
				return
			case <-o.wake:
			case <-ticker.C:
			}
		}
	}()
}

// run optionally fetches subscriptions, then tests the profiles in ids.
func (o *Optimizer) run(ctx context.Context, refresh bool, ids map[int64]bool) {
	if refresh {
		o.setPhase("refreshing")
		o.refreshSources(ctx)
	}
	summary := "subscriptions updated"
	if len(ids) > 0 {
		o.setPhase("testing")
		summary = o.cycle(ctx, func(p storage.Profile) bool { return ids[p.ID] })
	}
	o.mu.Lock()
	o.lastSummary = summary
	o.phase = "idle"
	o.mu.Unlock()
	o.broadcastStatus()
}

// dueProfiles returns the enabled auto-test profiles whose own interval has
// elapsed since their last test (from the saved funnel), so each profile's
// scheduling is driven only by its own card setting, never a global cycle.
func (o *Optimizer) dueProfiles() map[int64]bool {
	out := map[int64]bool{}
	profiles, err := o.store.ListProfiles()
	if err != nil {
		return out
	}
	now := time.Now()
	for _, p := range profiles {
		if !p.Enabled || !p.AutoTest {
			continue
		}
		iv, err := time.ParseDuration(p.TestInterval)
		if err != nil || iv < time.Minute {
			iv = 30 * time.Minute
		}
		last := time.Time{}
		if s := LoadProfileStats(o.store, p.ID); s != nil {
			last = s.At
		}
		if now.Sub(last) >= iv {
			out[p.ID] = true
		}
	}
	return out
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
	return o.ingest(raw, src.URL)
}

// Import stores configs pasted by the admin. Like fetched configs, they age out
// after the max age unless a profile uses them. Profiles pick them up on their own
// auto-test schedule or when the user presses Test now.
func (o *Optimizer) Import(content string) (count, inserted int, err error) {
	count, inserted, err = o.ingest(ingest.DecodeSubscription(content), "paste")
	if err == nil {
		o.logger.Info("pasted configs imported", "source", "optimizer", "configs", count, "new", inserted)
	}
	return count, inserted, err
}

// ingest parses a subscription body, deduplicates it and upserts the configs.
func (o *Optimizer) ingest(raw, source string) (count, inserted int, err error) {
	cfgs, _, _ := parser.ParseManyDetailed(ingest.CleanRawContent(raw))
	if len(cfgs) == 0 {
		return 0, 0, fmt.Errorf("no valid configs found")
	}
	for _, c := range cfgs {
		c.Source = source
	}
	inserted, _, err = o.store.UpsertConfigs(cfgs)
	return len(cfgs), inserted, err
}

// cycle tests the picked profiles: one TCP pass over all configs (shared by every
// profile), then for each profile the filter and measure phases against its target.
func (o *Optimizer) cycle(ctx context.Context, pick func(storage.Profile) bool) string {
	profiles, err := o.store.ListProfiles()
	if err != nil {
		return "error: " + err.Error()
	}
	var active []storage.Profile
	for _, p := range profiles {
		if p.Enabled && pick(p) {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return "no profiles to test"
	}
	configs, err := o.store.ListConfigsFiltered("", 0)
	if err != nil || len(configs) == 0 {
		return "no configs; add a subscription source"
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	eng := engine.NewXrayEngine(o.logger) // throwaway test engine; never the serving one
	defer eng.Close()
	defer func() {
		for _, p := range active {
			o.setProgress(p.ID, "")
		}
	}()

	start := time.Now()
	for _, p := range active {
		o.setProgress(p.ID, fmt.Sprintf("checking %d servers", len(configs)))
	}
	reachable := bench.Reachable(ctx, configs)
	o.logger.Info("reachability done", "source", "optimizer", "configs", len(configs), "reachable", len(reachable))

	var parts []string
	for _, prof := range active {
		if ctx.Err() != nil {
			break
		}
		parts = append(parts, o.testProfile(ctx, eng, prof, len(configs), reachable))
		o.setProgress(prof.ID, "")
	}
	o.profiles.ReleaseUnused()

	return fmt.Sprintf("%d configs, %d reachable; %s (%s)", len(configs), len(reachable),
		joinOr(parts, "no profiles tested"), time.Since(start).Round(time.Second))
}

// ProfileStats is the funnel of a profile's last test, shown on its card. It is kept
// in the settings table, so it survives restarts.
type ProfileStats struct {
	At        time.Time      `json:"at"`
	Configs   int            `json:"configs"`   // stored configs
	Reachable int            `json:"reachable"` // accepted a TCP connection
	Reached   int            `json:"reached"`   // passed the type's probe (reach the service)
	Checked   int            `json:"checked"`   // got the type's real check
	Passed    int            `json:"passed"`    // passed it: the profile's candidates
	BestMS    float64        `json:"best_ms,omitempty"`
	Passing   map[string]int `json:"passing,omitempty"` // candidates by label, e.g. country
	Failing   map[string]int `json:"failing,omitempty"` // checked but failed, by label ("" = no answer)
	Outcome   string         `json:"outcome"`
}

func statsKey(profileID int64) string { return fmt.Sprintf("profile_stats_%d", profileID) }

// LoadProfileStats returns a profile's last test funnel, or nil before its first test.
func LoadProfileStats(store *storage.Store, profileID int64) *ProfileStats {
	raw, err := store.GetSetting(statsKey(profileID), "")
	var st ProfileStats
	if err != nil || raw == "" || json.Unmarshal([]byte(raw), &st) != nil {
		return nil
	}
	return &st
}

// testProfile runs one profile's flow and keeps its funnel. While it runs, the
// profile serves the best candidate found so far, with the previous candidates
// behind it as fallbacks. A test that finds no candidate keeps the previous ones.
func (o *Optimizer) testProfile(ctx context.Context, eng *engine.XrayEngine, prof storage.Profile, configs int, reachable []*model.ProxyConfig) string {
	st := &ProfileStats{At: time.Now(), Configs: configs, Reachable: len(reachable), Passing: map[string]int{}, Failing: map[string]int{}}
	st.Outcome = o.runProfile(ctx, eng, prof, reachable, st)
	if b, err := json.Marshal(st); err == nil {
		if err := o.store.SetSetting(statsKey(prof.ID), string(b)); err != nil {
			o.logger.Warn("save profile stats failed", "source", "optimizer", "profile", prof.Name, "error", err)
		}
	}
	return prof.Name + ": " + st.Outcome
}

// runProfile is the flow every profile type shares, with the type's own probe and check:
//  1. filter: the type's cheap probe (no account) through every reachable node;
//  2. check: the type's real check once per node, fastest probe first; each node is
//     labeled (e.g. with the country the service saw) and passing ones become candidates;
//  3. rank: candidates by the check's response time; the best ones serve the port.
func (o *Optimizer) runProfile(ctx context.Context, eng *engine.XrayEngine, prof storage.Profile, reachable []*model.ProxyConfig, st *ProfileStats) string {
	kind, ok := checks.Get(prof.Kind)
	if !ok {
		return "unknown profile type " + prof.Kind
	}
	check, err := kind.New(prof.Settings)
	if err != nil {
		return "not configured: " + err.Error()
	}
	prev := o.profiles.Nodes(prof.ID)

	probes := bench.Filter(ctx, eng, check, reachable, func(bench.Probe) {
		st.Reached++
		o.setProgress(prof.ID, fmt.Sprintf("step 1/2 filter: %d of %d reachable configs reach %s", st.Reached, len(reachable), kind.Name))
	})
	if len(probes) == 0 {
		o.logger.Warn("no node reaches the service; keeping previous candidates", "source", "optimizer", "profile", prof.Name)
		return fmt.Sprintf("no node reaches %s; kept previous candidates", kind.Name)
	}
	probeCfgs := make([]*model.ProxyConfig, len(probes))
	for i, p := range probes {
		probeCfgs[i] = p.Config
	}

	// A refused session fails every check the same way: stop at the first one.
	sessionErr := func() error {
		if se, ok := check.(interface{ SessionError() error }); ok {
			return se.SessionError()
		}
		return nil
	}
	vctx, stop := context.WithCancel(ctx)
	defer stop()
	var passed []bench.Result
	results := bench.Verify(vctx, eng, check, probeCfgs, func(r bench.Result) {
		if sessionErr() != nil {
			stop()
			return
		}
		st.Checked++
		if r.OK {
			st.Passed++
			st.Passing[r.Label]++
			passed = append(passed, r)
			bench.SortResults(passed)
			o.profiles.SetNodes(prof.ID, livePool(passed, prev))
		} else {
			st.Failing[r.Label]++
		}
		text := fmt.Sprintf("step 2/2 check: %d of %d checked, %d passed", st.Checked, min(len(probes), bench.VerifyMax), st.Passed)
		if len(passed) > 0 {
			text += fmt.Sprintf(", best %d ms", passed[0].Latency.Milliseconds())
		}
		o.setProgress(prof.ID, text)
	})
	if err := sessionErr(); err != nil {
		o.logger.Error("profile session refused", "source", "optimizer", "profile", prof.Name, "error", err)
		return "session error: " + err.Error()
	}
	if len(passed) == 0 {
		o.logger.Warn("no node passed the check; keeping previous candidates", "source", "optimizer", "profile", prof.Name)
		return fmt.Sprintf("%d checked, none passed; kept previous candidates", st.Checked)
	}

	now := time.Now()
	nodes := make([]storage.ProfileNode, 0, len(results))
	var serve []*model.ProxyConfig
	for i, r := range results { // passing first, best first
		n := storage.ProfileNode{ConfigID: r.Config.ID, OK: r.OK, LatencyMS: ms(r.Latency), Label: r.Label, Error: r.Err, Response: r.Response, TestedAt: now}
		if r.OK {
			n.Rank = i + 1
			if len(serve) < nodesPerProfile {
				serve = append(serve, r.Config)
			}
		}
		nodes = append(nodes, n)
	}
	if err := o.store.ReplaceProfileNodes(prof.ID, nodes); err != nil {
		o.logger.Error("save profile results failed", "source", "optimizer", "profile", prof.Name, "error", err)
	}
	o.profiles.SetNodes(prof.ID, serve)
	st.BestMS = nodes[0].LatencyMS
	o.logger.Info("profile updated", "source", "optimizer", "profile", prof.Name, "best", serve[0].DisplayName(),
		"latency_ms", nodes[0].LatencyMS, "label", nodes[0].Label, "candidates", st.Passed)
	return fmt.Sprintf("%d candidates of %d checked, best %.0f ms", st.Passed, st.Checked, st.BestMS)
}

// livePool is the candidates found so far (best first) followed by the previous
// ones not checked yet, so failover still has somewhere to go mid-test.
func livePool(done []bench.Result, fallback []*model.ProxyConfig) []*model.ProxyConfig {
	pool := make([]*model.ProxyConfig, 0, nodesPerProfile)
	seen := map[string]bool{}
	for _, m := range done {
		if len(pool) == nodesPerProfile {
			return pool
		}
		pool = append(pool, m.Config)
		seen[m.Config.ID] = true
	}
	for _, c := range fallback {
		if len(pool) == nodesPerProfile {
			break
		}
		if !seen[c.ID] {
			pool = append(pool, c)
		}
	}
	return pool
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// setProgress records what a profile's test is doing; "" clears it. Broadcasts
// are limited to one a second so a fast test cannot flood the dashboard.
func (o *Optimizer) setProgress(id int64, text string) {
	o.mu.Lock()
	if text == "" {
		delete(o.progress, id)
	} else {
		o.progress[id] = text
	}
	due := time.Since(o.lastBroadcast) >= time.Second
	if due {
		o.lastBroadcast = time.Now()
	}
	o.mu.Unlock()
	if due {
		o.broadcastStatus()
	}
}

func joinOr(parts []string, empty string) string {
	if len(parts) == 0 {
		return empty
	}
	return strings.Join(parts, "; ")
}

// Status reports the schedule and the outcome of the last cycle.
func (o *Optimizer) Status() OptimizerStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	st := OptimizerStatus{OptimizerSettings: o.settings, Phase: o.phase, LastSummary: o.lastSummary,
		Progress: maps.Clone(o.progress)}
	ptr := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	st.LastRefreshAt = ptr(o.lastRefreshAt)
	if !o.lastRefreshAt.IsZero() {
		st.NextRefreshAt = ptr(o.lastRefreshAt.Add(o.refreshEvery))
	}
	return st
}

func (o *Optimizer) broadcastStatus() {
	if o.broadcast != nil {
		o.broadcast(map[string]interface{}{"type": "optimizer_status", "status": o.Status()})
	}
}
