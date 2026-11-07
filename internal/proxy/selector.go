// Package proxy implements the per-profile SOCKS5+HTTP proxy server and the
// selection of the node it forwards through.
package proxy

import (
	"log/slog"
	"sync"

	"github.com/AmirAM03/velox/internal/model"
)

// Selector holds a profile's ranked nodes (best first) and the one in use.
type Selector struct {
	mu        sync.RWMutex
	active    *model.ProxyConfig
	pool      []*model.ProxyConfig
	failCount int
	failLimit int
	logger    *slog.Logger
}

// NewSelector creates an empty Selector that fails over after failLimit consecutive failures.
func NewSelector(failLimit int, logger *slog.Logger) *Selector {
	if logger == nil {
		logger = slog.Default()
	}
	if failLimit <= 0 {
		failLimit = 2
	}
	return &Selector{failLimit: failLimit, logger: logger}
}

// Active returns the node in use, or nil when the profile has none.
func (s *Selector) Active() *model.ProxyConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// Pool returns a copy of the ranked nodes, best first.
func (s *Selector) Pool() []*model.ProxyConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*model.ProxyConfig(nil), s.pool...)
}

// SetPool replaces the ranked nodes (best first) and makes the best one active.
func (s *Selector) SetPool(configs []*model.ProxyConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pool = configs
	s.failCount = 0
	s.active = nil
	if len(configs) > 0 {
		s.active = configs[0]
	}
}

// SetActive makes the pool node with id the active one and resets the failure
// counter. It reports whether id was in the pool.
func (s *Selector) SetActive(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.pool {
		if c.ID == id {
			s.active = c
			s.failCount = 0
			return true
		}
	}
	return false
}

// RecordSuccess resets the consecutive failure counter.
func (s *Selector) RecordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCount = 0
}

// RecordFailure counts a failure of the active node and, at the limit, moves to
// the next node in rank order (wrapping around). It reports whether it switched.
func (s *Selector) RecordFailure(reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCount++
	if s.failCount < s.failLimit || len(s.pool) < 2 {
		return false
	}
	next := s.pool[0]
	for i, c := range s.pool {
		if s.active != nil && c.ID == s.active.ID {
			next = s.pool[(i+1)%len(s.pool)]
			break
		}
	}
	s.logger.Warn("node failing, switching to the next-ranked one", "source", "proxy",
		"from", s.active.DisplayName(), "to", next.DisplayName(), "failures", s.failCount, "reason", reason)
	s.active = next
	s.failCount = 0
	return true
}
