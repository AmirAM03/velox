// Package engine provides an abstraction layer over proxy cores (Xray-core).
// The Engine interface allows swapping the underlying core implementation
// without changing the rest of the application.
package engine

import (
	"context"
	"io"
	"net"

	"github.com/AmirAM03/velox/internal/model"
)

// Engine is the interface for proxy core operations.
// It abstracts the underlying proxy engine (Xray-core, sing-box, etc.)
// so the pipeline and proxy server don't depend on a specific implementation.
type Engine interface {
	// Dial creates a proxied connection through the given config.
	// The returned net.Conn tunnels traffic through the proxy server.
	// The context controls the dial timeout.
	Dial(ctx context.Context, config *model.ProxyConfig, network, addr string) (net.Conn, error)

	// TestTLSHandshake performs a TLS handshake through the proxy config's
	// server without sending application data. Used for Stage 1 validation.
	TestTLSHandshake(ctx context.Context, config *model.ProxyConfig) error

	// Close releases all resources held by the engine.
	Close() error
}

// DialFunc is a function type matching the Engine.Dial signature,
// useful for creating http.Transport instances.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// HTTPClient returns a DialFunc bound to a specific config, suitable for
// use as an http.Transport.DialContext.
func HTTPDialer(e Engine, config *model.ProxyConfig) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return e.Dial(ctx, config, network, addr)
	}
}

// Closer is a helper to close a resource, logging any error.
type Closer struct {
	closers []io.Closer
}

// Add registers a closer.
func (c *Closer) Add(closer io.Closer) {
	c.closers = append(c.closers, closer)
}

// CloseAll closes all registered closers in reverse order.
func (c *Closer) CloseAll() error {
	var firstErr error
	for i := len(c.closers) - 1; i >= 0; i-- {
		if err := c.closers[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
