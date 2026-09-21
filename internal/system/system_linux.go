//go:build linux

package system

import "log/slog"

func newOSProxy(logger *slog.Logger) ProxyController {
	return newLinuxProxy(logger)
}
