package system

import (
	"fmt"
	"log/slog"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

type windowsProxy struct {
	logger *slog.Logger
}

func newWindowsProxy(logger *slog.Logger) ProxyController {
	return &windowsProxy{logger: logger}
}

const (
	internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

func (w *windowsProxy) Enable(host string, httpPort, socksPort int) error {
	w.logger.Info("configuring Windows system proxy", "host", host, "http_port", httpPort, "socks_port", socksPort)

	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry: %w", err)
	}
	defer key.Close()

	proxyServer := fmt.Sprintf("http=%s:%d;https=%s:%d;socks=%s:%d", host, httpPort, host, httpPort, host, socksPort)
	if httpPort == socksPort {
		proxyServer = fmt.Sprintf("%s:%d", host, httpPort)
	}

	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return fmt.Errorf("set ProxyEnable: %w", err)
	}

	if err := key.SetStringValue("ProxyServer", proxyServer); err != nil {
		return fmt.Errorf("set ProxyServer: %w", err)
	}

	if err := key.SetStringValue("ProxyOverride", "<local>;localhost;127.*;10.*;172.16.*;192.168.*"); err != nil {
		w.logger.Debug("set ProxyOverride failed", "error", err)
	}

	w.notifyWinINET()
	return nil
}

func (w *windowsProxy) Disable() error {
	w.logger.Info("restoring Windows system proxy to direct")

	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry: %w", err)
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
		return fmt.Errorf("set ProxyEnable: %w", err)
	}

	w.notifyWinINET()
	return nil
}

// notifyWinINET notifies the Windows Internet subsystem of proxy changes.
func (w *windowsProxy) notifyWinINET() {
	wininet := syscall.NewLazyDLL("wininet.dll")
	setOption := wininet.NewProc("InternetSetOptionW")

	_, _, _ = setOption.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	_, _, _ = setOption.Call(0, uintptr(internetOptionRefresh), 0, 0)
}
