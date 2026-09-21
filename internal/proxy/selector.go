// Package proxy implements the local mixed SOCKS5+HTTP proxy server,
// config selection, and automatic health-checking failover.
package proxy

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/AmirAM03/velox/internal/model"
	"github.com/AmirAM03/velox/internal/storage"
)

// Selector manages the active proxy config and the warm pool of top configs.
type Selector struct {
	mu           sync.RWMutex
	store        *storage.Store
	warmPoolSize int
	active       *model.ProxyConfig
	warmPool     []*model.ProxyConfig
	failCount    int
	failLimit    int
	logger       *slog.Logger
	onSwitch     func(oldCfg, newCfg *model.ProxyConfig)
}

// NewSelector creates a new config Selector.
func NewSelector(store *storage.Store, warmPoolSize, failLimit int, logger *slog.Logger) *Selector {
	if logger == nil {
		logger = slog.Default()
	}
	if warmPoolSize <= 0 {
		warmPoolSize = 5
	}
	if failLimit <= 0 {
		failLimit = 2
	}

	return &Selector{
		store:        store,
		warmPoolSize: warmPoolSize,
		failLimit:    failLimit,
		logger:       logger,
	}
}

// RefreshWarmPool reloads the top configs from the database into the warm pool.
func (s *Selector) RefreshWarmPool() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	configs, err := s.store.ListConfigs(s.warmPoolSize)
	if err != nil {
		return fmt.Errorf("list top configs: %w", err)
	}

	if len(configs) == 0 {
		return fmt.Errorf("no configs available in database")
	}

	s.warmPool = configs

	// If no active config is selected, choose the top config
	if s.active == nil && len(configs) > 0 {
		s.active = configs[0]
		s.failCount = 0
		s.logger.Info("selected initial active config",
			"id", s.active.ID,
			"name", s.active.DisplayName(),
			"protocol", s.active.Protocol,
		)
	}

	return nil
}

// Active returns the current active proxy config.
func (s *Selector) Active() *model.ProxyConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// SelectExplicitly sets the active config by ID or explicit config.
func (s *Selector) SelectExplicitly(cfg *model.ProxyConfig) {
	s.mu.Lock()
	old := s.active
	s.active = cfg
	s.failCount = 0
	cb := s.onSwitch
	s.mu.Unlock()

	s.logger.Info("manually switched active config",
		"name", cfg.DisplayName(),
		"protocol", cfg.Protocol,
	)

	if cb != nil {
		cb(old, cfg)
	}
}

// RecordSuccess resets the consecutive failure counter for the active config.
func (s *Selector) RecordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCount = 0
}

// RecordFailure increments the failure count and triggers failover if limit is reached.
// Returns true if a failover occurred.
func (s *Selector) RecordFailure(reason string) bool {
	s.mu.Lock()
	s.failCount++
	currentFailures := s.failCount
	activeName := ""
	if s.active != nil {
		activeName = s.active.DisplayName()
	}
	s.logger.Warn("active config health check failure",
		"config", activeName,
		"failures", currentFailures,
		"threshold", s.failLimit,
		"reason", reason,
	)

	if currentFailures < s.failLimit {
		s.mu.Unlock()
		return false
	}

	// Trigger failover to next best in warm pool
	old := s.active
	var next *model.ProxyConfig
	for _, c := range s.warmPool {
		if old == nil || c.ID != old.ID {
			next = c
			break
		}
	}

	if next == nil {
		s.logger.Error("failover failed: no alternate config available in warm pool")
		s.mu.Unlock()
		return false
	}

	s.active = next
	s.failCount = 0
	cb := s.onSwitch
	s.mu.Unlock()

	s.logger.Warn("⚡ AUTOMATIC FAILOVER TRIGGERED",
		"from", old.DisplayName(),
		"to", next.DisplayName(),
		"consecutive_failures", currentFailures,
	)

	if cb != nil {
		cb(old, next)
	}
	return true
}

// SetOnSwitchCallback sets a callback to be called whenever active config changes.
func (s *Selector) SetOnSwitchCallback(cb func(oldCfg, newCfg *model.ProxyConfig)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSwitch = cb
}

// WarmPool returns a copy of current warm pool configs.
func (s *Selector) WarmPool() []*model.ProxyConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.ProxyConfig, len(s.warmPool))
	copy(out, s.warmPool)
	return out
}
