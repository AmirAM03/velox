package system

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

type linuxProxy struct {
	logger *slog.Logger
}

func newLinuxProxy(logger *slog.Logger) ProxyController {
	return &linuxProxy{logger: logger}
}

func (l *linuxProxy) Enable(host string, httpPort, socksPort int) error {
	l.logger.Info("configuring Linux system proxy", "host", host, "http_port", httpPort, "socks_port", socksPort)

	// Check if gsettings exists (GNOME / Ubuntu default)
	if _, err := exec.LookPath("gsettings"); err == nil {
		commands := [][]string{
			{"set", "org.gnome.system.proxy", "mode", "manual"},
			{"set", "org.gnome.system.proxy.http", "host", host},
			{"set", "org.gnome.system.proxy.http", "port", fmt.Sprintf("%d", httpPort)},
			{"set", "org.gnome.system.proxy.https", "host", host},
			{"set", "org.gnome.system.proxy.https", "port", fmt.Sprintf("%d", httpPort)},
			{"set", "org.gnome.system.proxy.socks", "host", host},
			{"set", "org.gnome.system.proxy.socks", "port", fmt.Sprintf("%d", socksPort)},
		}

		for _, args := range commands {
			cmd := exec.Command("gsettings", args...)
			if err := cmd.Run(); err != nil {
				l.logger.Debug("gsettings command failed", "args", args, "error", err)
			}
		}
	}

	// Check for KDE (kwriteconfig5 / kwriteconfig6)
	kwrite := "kwriteconfig5"
	if _, err := exec.LookPath("kwriteconfig6"); err == nil {
		kwrite = "kwriteconfig6"
	}
	if _, err := exec.LookPath(kwrite); err == nil {
		kdeCmds := [][]string{
			{"--file", "kioslaurc", "--group", "Proxy Settings", "--key", "ProxyType", "1"},
			{"--file", "kioslaurc", "--group", "Proxy Settings", "--key", "httpProxy", fmt.Sprintf("http://%s:%d", host, httpPort)},
			{"--file", "kioslaurc", "--group", "Proxy Settings", "--key", "httpsProxy", fmt.Sprintf("http://%s:%d", host, httpPort)},
			{"--file", "kioslaurc", "--group", "Proxy Settings", "--key", "socksProxy", fmt.Sprintf("socks://%s:%d", host, socksPort)},
		}
		for _, args := range kdeCmds {
			_ = exec.Command(kwrite, args...).Run()
		}
	}

	// Print shell environment hint for terminal users
	fmt.Printf("\n💡 To route shell/terminal commands through Velox proxy, run:\n")
	fmt.Printf("   export http_proxy=\"http://%s:%d\"\n", host, httpPort)
	fmt.Printf("   export https_proxy=\"http://%s:%d\"\n", host, httpPort)
	fmt.Printf("   export all_proxy=\"socks5://%s:%d\"\n\n", host, socksPort)

	return nil
}

func (l *linuxProxy) Disable() error {
	l.logger.Info("restoring Linux system proxy to direct")

	if _, err := exec.LookPath("gsettings"); err == nil {
		_ = exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "none").Run()
	}

	kwrite := "kwriteconfig5"
	if _, err := exec.LookPath("kwriteconfig6"); err == nil {
		kwrite = "kwriteconfig6"
	}
	if _, err := exec.LookPath(kwrite); err == nil {
		_ = exec.Command(kwrite, "--file", "kioslaurc", "--group", "Proxy Settings", "--key", "ProxyType", "0").Run()
	}

	_ = os.Unsetenv("http_proxy")
	_ = os.Unsetenv("https_proxy")
	_ = os.Unsetenv("all_proxy")
	return nil
}
