package system

import (
	"log/slog"
)

// ProxyController controls the OS system-wide proxy settings.
type ProxyController interface {
	// Enable configures the system to route traffic through the given proxy.
	Enable(host string, httpPort, socksPort int) error

	// Disable restores the system proxy settings to direct connection.
	Disable() error
}

// NewProxyController returns the appropriate ProxyController for the current OS.
func NewProxyController(logger *slog.Logger) ProxyController {
	if logger == nil {
		logger = slog.Default()
	}
	return newOSProxy(logger)
}

type noopProxy struct {
	logger *slog.Logger
}

func (n *noopProxy) Enable(host string, httpPort, socksPort int) error {
	n.logger.Warn("system proxy configuration not supported on this OS")
	return nil
}

func (n *noopProxy) Disable() error {
	return nil
}
