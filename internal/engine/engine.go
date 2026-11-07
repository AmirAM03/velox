// Package engine provides an abstraction layer over proxy cores (Xray-core).
// The Engine interface allows swapping the underlying core implementation
// without changing the rest of the application.
package engine

import (
	"context"
	"net"

	"github.com/AmirAM03/velox/internal/model"
)

// Engine is the interface for proxy core operations.
// It abstracts the underlying proxy engine (Xray-core, sing-box, etc.)
// so the benchmark and proxy server don't depend on a specific implementation.
type Engine interface {
	// Dial creates a proxied connection through the given config.
	// The returned net.Conn tunnels traffic through the proxy server.
	// The context controls the dial timeout.
	Dial(ctx context.Context, config *model.ProxyConfig, network, addr string) (net.Conn, error)

	// Close releases all resources held by the engine.
	Close() error
}
