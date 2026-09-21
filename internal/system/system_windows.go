//go:build windows

package system

import "log/slog"

func newOSProxy(logger *slog.Logger) ProxyController {
	return newWindowsProxy(logger)
}
