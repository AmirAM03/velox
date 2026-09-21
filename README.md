# Velox

**High-performance V2Ray proxy config engine.** Ingest, test, score, and connect through thousands of proxy configurations at speed.

> *Velox* — Latin for *swift*.

[![Release](https://img.shields.io/github/v/release/AmirAM03/velox?style=flat-square)](https://github.com/AmirAM03/velox/releases)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL_2.0-blue.svg?style=flat-square)](LICENSE)

---

## ⚡ Quick Install (Linux / Ubuntu)

Install Velox and systemd service on Ubuntu with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/install.sh | sudo bash
```

See the [Ubuntu Deployment Guide](docs/ubuntu.md) for full setup and tuning instructions.

---

## Features

- 🚀 **High-Volume Ingestion** — VMess, VLESS (REALITY, Vision), Trojan, Shadowsocks, WireGuard.
- 🧹 **Automatic Sanitization** — Strips comments (`#`, `//`), normalizes CRLF, trims BOM.
- 🔬 **Multi-Stage Pipeline** — DNS+TCP (2000 workers) → TLS/REALITY (500 workers) → In-process Proxy test (150 workers).
- 🎯 **Custom Target Delay Testing** — Probe against your own specific destination URLs to find the fastest proxy for your workload.
- 📊 **Smart Weighted Scoring** — Evaluates P95 latency, success rate, stability penalty, and recency.
- 🔄 **Warm Pool & Auto-Failover** — Continuous background health checks switch seamlessly to the next best node if failures occur.
- ⚡ **Embedded Xray-Core** — Runs in-process via `core.New` and `core.Dial`, eliminating external process overhead and socket exhaustion.
- 🌐 **Mixed SOCKS5 + HTTP Inbound** — Listens on `127.0.0.1:1080` and handles both protocols simultaneously.
- 🗄️ **Pure-Go SQLite WAL** — High-concurrency persistence with zero CGO dependencies.
- 🐧 **Linux System Integration** — GNOME `gsettings`, KDE `kwriteconfig`, systemd service, and environment variable exports.

---

## CLI Usage

```bash
# 1. Parse configs from one or more subscription URLs
velox parse --sub "https://example.com/sub"

# 2. Test configs against your custom target URLs
velox test --target "https://www.google.com" --target "https://cloudflare.com"

# 3. List top-ranked nodes
velox list

# 4. Connect through the fastest proxy (mixed SOCKS5/HTTP on 127.0.0.1:1080)
velox connect

# Run with system-wide proxy enabled
velox connect --system
```

---

## Running as a Background Service (Ubuntu)

```bash
# Start and enable on boot
sudo systemctl enable --now velox

# Check status
sudo systemctl status velox

# Live log output
sudo journalctl -u velox -f
```

---

## Building from Source

Requires Go 1.23+:

```bash
git clone https://github.com/AmirAM03/velox.git
cd velox
make build
```

---

## License

MPL-2.0 (Mozilla Public License Version 2.0)
