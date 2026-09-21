package system

import (
	"fmt"
	"log/slog"
	"os/exec"
)

type darwinProxy struct {
	logger *slog.Logger
}

func newDarwinProxy(logger *slog.Logger) ProxyController {
	return &darwinProxy{logger: logger}
}

func (d *darwinProxy) Enable(host string, httpPort, socksPort int) error {
	d.logger.Info("configuring macOS system proxy", "host", host, "http_port", httpPort, "socks_port", socksPort)

	// networksetup requires a network service name (e.g. Wi-Fi, Ethernet).
	// For now we attempt "Wi-Fi" as default interface.
	service := "Wi-Fi"
	_ = exec.Command("networksetup", "-setwebproxy", service, host, fmt.Sprintf("%d", httpPort)).Run()
	_ = exec.Command("networksetup", "-setsecurewebproxy", service, host, fmt.Sprintf("%d", httpPort)).Run()
	_ = exec.Command("networksetup", "-setsocksfirewallproxy", service, host, fmt.Sprintf("%d", socksPort)).Run()

	return nil
}

func (d *darwinProxy) Disable() error {
	d.logger.Info("restoring macOS system proxy to direct")

	service := "Wi-Fi"
	_ = exec.Command("networksetup", "-setwebproxystate", service, "off").Run()
	_ = exec.Command("networksetup", "-setsecurewebproxystate", service, "off").Run()
	_ = exec.Command("networksetup", "-setsocksfirewallproxystate", service, "off").Run()

	return nil
}
