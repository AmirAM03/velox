//go:build !windows && !linux && !darwin

package system

import "log/slog"

func newOSProxy(logger *slog.Logger) ProxyController {
	return &noopProxy{logger: logger}
}
