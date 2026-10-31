package web

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/AmirAM03/velox/internal/engine"
	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/proxy"
	"github.com/AmirAM03/velox/internal/storage"
)

// ProfileStatus is a profile plus its live listener state, as shown in the dashboard.
type ProfileStatus struct {
	storage.Profile
	Running    bool                  `json:"running"`
	Error      string                `json:"error,omitempty"`
	ActiveNode *model.ProxyConfig    `json:"active_node,omitempty"`
	Nodes      []storage.ProfileNode `json:"nodes"`
}

type profileRuntime struct {
	profile  storage.Profile
	server   *proxy.Server
	selector *proxy.Selector
}

// ProfileManager runs one authenticated SOCKS5/HTTP listener per enabled profile.
// All listeners share one Xray engine, so a node used by two profiles runs once.
type ProfileManager struct {
	mu         sync.Mutex
	store      *storage.Store
	listenAddr string
	failLimit  int
	engine     *engine.XrayEngine
	logger     *slog.Logger
	running    map[int64]*profileRuntime
	errors     map[int64]string
	prevKeep   map[string]bool
}

// NewProfileManager creates a manager. Call Sync to start listeners.
func NewProfileManager(store *storage.Store, listenAddr string, failLimit int, logger *slog.Logger) *ProfileManager {
	return &ProfileManager{
		store:      store,
		listenAddr: listenAddr,
		failLimit:  failLimit,
		engine:     engine.NewXrayEngine(logger),
		logger:     logger,
		running:    map[int64]*profileRuntime{},
		errors:     map[int64]string{},
		prevKeep:   map[string]bool{},
	}
}

// Sync makes the running listeners match the profiles stored in the database.
func (m *ProfileManager) Sync() error {
	profiles, err := m.store.ListProfiles()
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	wanted := map[int64]storage.Profile{}
	for _, p := range profiles {
		if p.Enabled {
			wanted[p.ID] = p
		}
	}

	// Stop listeners that were removed, disabled, or changed port/credentials.
	for id, rt := range m.running {
		p, ok := wanted[id]
		if ok && p.Port == rt.profile.Port && p.Username == rt.profile.Username && p.Password == rt.profile.Password {
			rt.profile = p // name/target changes need no restart
			continue
		}
		rt.server.Stop()
		delete(m.running, id)
		m.logger.Info("profile listener stopped", "source", "profiles", "profile", rt.profile.Name, "port", rt.profile.Port)
	}

	m.errors = map[int64]string{}
	for id, p := range wanted {
		if _, ok := m.running[id]; ok {
			continue
		}
		sel := proxy.NewSelector(nil, 0, m.failLimit, m.logger)
		if _, cfgs, err := m.store.ListProfileNodes(id); err == nil {
			sel.SetPool(cfgs) // serve last cycle's nodes right away after a restart
		}
		srv := proxy.NewServer(m.listenAddr, p.Port, p.Username, p.Password, m.engine, sel, m.logger)
		if err := srv.Start(); err != nil {
			m.errors[id] = err.Error()
			m.logger.Error("profile listener failed", "source", "profiles", "profile", p.Name, "port", p.Port, "error", err)
			continue
		}
		m.running[id] = &profileRuntime{profile: p, server: srv, selector: sel}
		m.logger.Info("profile listener started", "source", "profiles", "profile", p.Name, "addr", fmt.Sprintf("%s:%d", m.listenAddr, p.Port))
	}
	return nil
}

// SetNodes hands a profile its new ranked nodes (best first).
func (m *ProfileManager) SetNodes(profileID int64, cfgs []*model.ProxyConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt := m.running[profileID]; rt != nil {
		rt.selector.SetPool(cfgs)
	}
}

// ReleaseUnused closes Xray instances no profile uses any more. Nodes from the
// previous call are kept one extra cycle so open user connections are not cut
// the moment a node drops out of the ranking.
func (m *ProfileManager) ReleaseUnused() {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := map[string]bool{}
	for _, rt := range m.running {
		for _, c := range rt.selector.WarmPool() {
			keep[c.ID] = true
		}
	}
	all := map[string]bool{}
	for id := range keep {
		all[id] = true
	}
	for id := range m.prevKeep {
		all[id] = true
	}
	m.engine.Retain(all)
	m.prevKeep = keep
}

// Status returns every stored profile with its live state.
func (m *ProfileManager) Status() ([]ProfileStatus, error) {
	profiles, err := m.store.ListProfiles()
	if err != nil {
		return nil, err
	}
	out := make([]ProfileStatus, 0, len(profiles))
	for _, p := range profiles {
		st := ProfileStatus{Profile: p, Nodes: []storage.ProfileNode{}}
		if nodes, _, err := m.store.ListProfileNodes(p.ID); err == nil && nodes != nil {
			st.Nodes = nodes
		}
		m.mu.Lock()
		if rt := m.running[p.ID]; rt != nil {
			st.Running = true
			st.ActiveNode = rt.selector.Active()
		}
		st.Error = m.errors[p.ID]
		m.mu.Unlock()
		out = append(out, st)
	}
	return out, nil
}

// Close stops all listeners and the shared engine.
func (m *ProfileManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rt := range m.running {
		rt.server.Stop()
		delete(m.running, id)
	}
	m.engine.Close()
}
