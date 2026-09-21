//go:build darwin

package system

import "log/slog"

func newOSProxy(logger *slog.Logger) ProxyController {
	return newDarwinProxy(logger)
}
