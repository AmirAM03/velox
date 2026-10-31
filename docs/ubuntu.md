# Velox on Linux (Ubuntu / Debian) — Deployment Guide

This guide walks you through installing, configuring, and running **Velox** on Ubuntu (20.04 LTS, 22.04 LTS, 24.04 LTS) and Debian-based distributions.

---

## 1. Quick Installation (One-Liner)

Install or upgrade to the latest version of Velox:

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/install.sh | sudo bash
```

This automated installer:
1. Detects your CPU architecture (`x86_64` / `arm64`).
2. Downloads the latest release from GitHub.
3. Installs the executable to `/usr/local/bin/velox`.
4. Creates the configuration template at `/etc/velox/config.yaml`.
5. Creates the data directory `/var/lib/velox`.

---

---

## 2. Launching the Web Dashboard

Velox is built as an interactive, web-first application with an embedded zero-dependency Web Dashboard. Simply run:

```bash
velox
```

The dashboard listens on `127.0.0.1:18080` only and has no login, so it is never exposed to the internet.

### Reaching the Dashboard on a VPS
Open an SSH tunnel from your own machine:
```bash
# SSH Tunnel from your local machine:
ssh -L 18080:127.0.0.1:18080 user@your-vps-ip
# Then navigate to http://127.0.0.1:18080 in your local browser
```

The Web Dashboard lets you:
- **Telemetry Overview**: Real-time proxy status, active node latency, working nodes tally, and database statistics.
- **Configurations Tab**: Interactive searchable table across all protocols (`VLESS`, `VMess`, `Trojan`, `Shadowsocks`, `Hysteria 2`, `WireGuard`).
- **Ingest & Parse Tab**: Paste subscription links or raw configuration blocks with instant cryptographic deduplication.
- **Speed & Latency Benchmark Tab**: Multi-stage speed and delay tests with destination presets (`Google`, `Cloudflare`, `YouTube`, `GitHub`) and configurable parallel threads.
- **Application Logs Tab**: Persistent SQLite WAL logging (`app_logs`) with keyword search, level/subsystem filtering, JSON attribute inspection, automated retention pruning (24h, 3d, 7d, 30d, 90d, custom), and export.
- **One-Click Proxy**: Start or stop the in-process proxy engine.

---

## 3. CLI Options & Headless Flags

Velox operates cleanly in user space without requiring any background services:

```bash
# Run on custom port
velox --port 8080

# Custom data directory
velox --data-dir /var/lib/velox

# Enable debug logging
velox --log-level debug
```

---

## 4. Routing System Traffic Through Velox

### Terminal / Shell Environment
Add to `~/.bashrc` or run in your active terminal:
```bash
export http_proxy="http://127.0.0.1:1080"
export https_proxy="http://127.0.0.1:1080"
export all_proxy="socks5://127.0.0.1:1080"
```

To verify:
```bash
curl -i https://ipinfo.io
```

### Ubuntu Desktop (GNOME)
Velox includes native GNOME `gsettings` integration when run with `--system`. You can also configure it manually in **Settings → Network → Network Proxy → Manual**:
- **HTTP Proxy**: `127.0.0.1`, Port `1080`
- **HTTPS Proxy**: `127.0.0.1`, Port `1080`
- **Socks Host**: `127.0.0.1`, Port `1080`

### Package Managers (`apt`)
To route `apt` updates through Velox:
```bash
echo 'Acquire::http::Proxy "http://127.0.0.1:1080/";' | sudo tee /etc/apt/apt.conf.d/99proxy
echo 'Acquire::https::Proxy "http://127.0.0.1:1080/";' | sudo tee -a /etc/apt/apt.conf.d/99proxy
```

---

## 5. Performance Tuning (High-Volume Testing)

For testing thousands of configs concurrently, ensure Ubuntu allows sufficient open files:

```bash
# Check current limit
ulimit -n

# Increase file descriptors temporarily
ulimit -n 65535
```

---

## 6. Uninstallation & Cleanup

To cleanly remove Velox from your Ubuntu machine:

```bash
# Standard uninstall (removes binary, systemd service, and /etc/velox)
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash
```

To perform a **deep clean** (which also removes user databases and configs in `~/.velox`):

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash -s -- --purge
```

---

## 7. Related Documentation

- [Windows User Guide](windows.md) — Setup and usage on Windows 10/11.
- [Architecture & Internals](architecture.md) — Technical deep-dive into the pipeline, scoring math, and storage.
- [Configuration Manual](configuration.md) — Detailed reference for `config.yaml`.

