package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/AmirAM03/velox/internal/engine"
)

// HealthChecker periodically checks the health of the active proxy config
// and triggers automatic failover when consecutive failures occur.
type HealthChecker struct {
	selector *Selector
	engine   engine.Engine
	targets  []string
	expect   []int
	interval time.Duration
	logger   *slog.Logger
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewHealthChecker creates a new HealthChecker.
func NewHealthChecker(
	sel *Selector,
	eng engine.Engine,
	targets []string,
	expect []int,
	interval time.Duration,
	logger *slog.Logger,
) *HealthChecker {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if len(targets) == 0 {
		targets = []string{"https://www.gstatic.com/generate_204"}
	}
	if len(expect) == 0 {
		expect = []int{200, 204}
	}

	return &HealthChecker{
		selector: sel,
		engine:   eng,
		targets:  targets,
		expect:   expect,
		interval: interval,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the background health-checking loop.
func (h *HealthChecker) Start(ctx context.Context) {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()

		// Run an initial check after a brief grace period
		select {
		case <-time.After(2 * time.Second):
			h.checkActive(ctx)
		case <-h.stopCh:
			return
		case <-ctx.Done():
			return
		}

		ticker := time.NewTicker(h.interval)
		poolRefreshTicker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		defer poolRefreshTicker.Stop()

		for {
			select {
			case <-ticker.C:
				h.checkActive(ctx)
			case <-poolRefreshTicker.C:
				if err := h.selector.RefreshWarmPool(); err != nil {
					h.logger.Debug("periodic warm pool refresh", "error", err)
				}
			case <-h.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop stops the health checking loop and waits for the goroutine to finish.
func (h *HealthChecker) Stop() {
	close(h.stopCh)
	h.wg.Wait()
}

// checkActive performs a live HTTP probe through the active proxy config.
func (h *HealthChecker) checkActive(ctx context.Context) {
	active := h.selector.Active()
	if active == nil {
		return
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
				return h.engine.Dial(dialCtx, active, network, addr)
			},
			DisableKeepAlives: true,
		},
		Timeout: 10 * time.Second,
	}

	var lastErr error
	for _, target := range h.targets {
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "Velox-HealthCheck/1.0")

		resp, err := client.Do(req)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()

		for _, code := range h.expect {
			if resp.StatusCode == code {
				// Success!
				h.selector.RecordSuccess()
				return
			}
		}
		lastErr = fmt.Errorf("unexpected status %d from %s", resp.StatusCode, target)
	}

	// All targets failed
	reason := "health check failed"
	if lastErr != nil {
		reason = lastErr.Error()
	}
	h.selector.RecordFailure(reason)
}
