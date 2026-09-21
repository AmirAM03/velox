package engine

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"

	"github.com/AmirAM03/velox/internal/model"
)

// XrayEngine implements Engine using embedded Xray-core.
type XrayEngine struct {
	logger *slog.Logger
	mu     sync.RWMutex
	pool   map[string]*core.Instance // cache of running instances by config ID
}

// NewXrayEngine creates a new Xray-core backed engine.
func NewXrayEngine(logger *slog.Logger) *XrayEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &XrayEngine{
		logger: logger,
		pool:   make(map[string]*core.Instance),
	}
}

// Ensure XrayEngine implements Engine.
var _ Engine = (*XrayEngine)(nil)

// getInstance gets or creates an Xray core instance for the given config.
func (e *XrayEngine) getInstance(cfg *model.ProxyConfig) (*core.Instance, error) {
	e.mu.RLock()
	inst, ok := e.pool[cfg.ID]
	e.mu.RUnlock()
	if ok {
		return inst, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring lock
	if inst, ok := e.pool[cfg.ID]; ok {
		return inst, nil
	}

	rawJSON, err := BuildConfigJSON(cfg)
	if err != nil {
		return nil, fmt.Errorf("build config json: %w", err)
	}

	xrayCfg, err := core.LoadConfig("json", rawJSON)
	if err != nil {
		return nil, fmt.Errorf("load xray config: %w", err)
	}

	inst, err = core.New(xrayCfg)
	if err != nil {
		return nil, fmt.Errorf("create xray instance: %w", err)
	}

	if err := inst.Start(); err != nil {
		inst.Close()
		return nil, fmt.Errorf("start xray instance: %w", err)
	}

	e.pool[cfg.ID] = inst
	return inst, nil
}

// Dial creates a proxied connection through the given config.
func (e *XrayEngine) Dial(ctx context.Context, cfg *model.ProxyConfig, network, addr string) (net.Conn, error) {
	inst, err := e.getInstance(cfg)
	if err != nil {
		return nil, err
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid destination address %q: %w", addr, err)
	}

	port, err := net.LookupPort(network, portStr)
	if err != nil {
		return nil, fmt.Errorf("lookup port %q: %w", portStr, err)
	}

	dest := xnet.Destination{
		Network: xnet.Network_TCP,
		Address: xnet.ParseAddress(host),
		Port:    xnet.Port(port),
	}
	if network == "udp" {
		dest.Network = xnet.Network_UDP
	}

	return core.Dial(ctx, inst, dest)
}

// TestTLSHandshake tests TLS/REALITY handshake through the proxy.
func (e *XrayEngine) TestTLSHandshake(ctx context.Context, cfg *model.ProxyConfig) error {
	// For REALITY or TLS handshake verification, we attempt a dial to the configured SNI
	// or server address on port 443 (or cfg.Port) with a brief deadline.
	sni := cfg.SNI
	if sni == "" {
		sni = cfg.Address
	}
	targetAddr := fmt.Sprintf("%s:%d", sni, cfg.Port)
	conn, err := e.Dial(ctx, cfg, "tcp", targetAddr)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// Close closes all running Xray instances.
func (e *XrayEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var firstErr error
	for id, inst := range e.pool {
		if err := inst.Close(); err != nil && firstErr == nil {
			firstErr = err
			e.logger.Warn("error closing xray instance", "config_id", id, "error", err)
		}
	}
	e.pool = make(map[string]*core.Instance)
	return firstErr
}
