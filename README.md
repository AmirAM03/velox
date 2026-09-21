# Velox

**High-performance V2Ray proxy config engine & interactive web dashboard.** Ingest, test, score, and connect through thousands of proxy configurations at blazing speed.

> *Velox* — Latin for *swift*.

[![Release](https://img.shields.io/github/v/release/AmirAM03/velox?style=flat-square)](https://github.com/AmirAM03/velox/releases)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL_2.0-blue.svg?style=flat-square)](LICENSE)

---

## 🖥️ Interactive Web Dashboard

Velox includes a zero-dependency, modern dark-mode **Web Dashboard** embedded directly inside the binary. Launch it anytime with:

```bash
velox ui
# or:
velox dashboard
# or with flag:
velox --ui
```

Opens automatically at `http://127.0.0.1:18080`:
- **Real-Time Telemetry**: Active proxy node status, connection health, and database metrics.
- **Config Explorer**: Interactive search and protocol-filtered table (VLESS, VMess, Trojan, SS, WireGuard) with latency tags.
- **Instant Ingestion**: Paste subscription links or bulk configuration text with automatic cryptographic deduplication.
- **Live Benchmarks**: Trigger multi-stage speed & latency tests against custom target endpoints with real-time progress.
- **One-Click Proxy & System Toggle**: Connect or disconnect the in-process proxy engine and switch system proxy settings seamlessly.

---

## ⚡ Quick Start

### Linux (Ubuntu / Debian / Arch / RHEL)
Install the latest release with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/install.sh | sudo bash
```

See the [Ubuntu Deployment Guide](docs/ubuntu.md) for detailed configuration and system integration.

### Windows (amd64 / arm64)
1. Download the latest `velox-vX.Y.Z-windows-amd64.zip` from [GitHub Releases](https://github.com/AmirAM03/velox/releases).
2. Extract `velox.exe` to any folder in your PATH (e.g. `C:\tools\velox` or `C:\Windows\System32`).
3. Run `velox.exe ui` in PowerShell or Command Prompt to launch the Web Dashboard!

---

## 🚀 CLI Usage

Velox operates completely in user-space without any background daemon or service requirement:

```bash
# 1. Ingest configs from subscription URLs or files
velox parse --sub "https://example.com/sub/token"
velox parse --file my_configs.txt

# 2. Test and rank configs against custom targets
velox test --target "https://www.google.com" --target "https://cloudflare.com"

# 3. List top-ranked nodes
velox list

# 4. Connect through the fastest proxy (mixed SOCKS5/HTTP on 127.0.0.1:1080)
velox connect

# 5. Connect and set OS system proxy automatically
velox connect --system

# 6. Deduplicate database records
velox dedup
```

---

## Features

- 🖥️ **Embedded Web Dashboard** — Zero external frontend files or web servers; fully embedded in Go binary with modern glassmorphic UI.
- 🚀 **High-Volume Ingestion** — VMess, VLESS (REALITY, Vision), Trojan, Shadowsocks, WireGuard.
- 🧹 **Automatic Sanitization & Deduplication** — Strips comments (`#`, `//`), normalizes CRLF, trims BOM, and enforces strict SHA256-based deduplication across subscriptions.
- 🔬 **Multi-Stage Pipeline** — DNS+TCP (2000 workers) → TLS/REALITY (500 workers) → In-process Proxy test (150 workers).
- 🎯 **Custom Target Delay Testing** — Probe against specific destination URLs to find the fastest proxy for your workload.
- 📊 **Smart Weighted Scoring** — Evaluates P95 latency, success rate, stability penalty, and recency.
- 🔄 **Warm Pool & Auto-Failover** — Health checks switch seamlessly to the next best node if connection drops.
- ⚡ **Embedded Xray-Core** — Runs in-process via `core.New` and `core.Dial`, eliminating external process overhead and socket exhaustion.
- 🌐 **Mixed SOCKS5 + HTTP Inbound** — Listens on `127.0.0.1:1080` and handles both protocols simultaneously.
- 🗄️ **Pure-Go SQLite WAL** — High-concurrency persistence with zero CGO dependencies.
- 🪟 **Cross-Platform** — Native support for Linux (`amd64`, `arm64`) and Windows (`amd64`, `arm64`) with automatic system proxy controls.

---

## 🗑️ Clean Uninstallation (Linux)

To cleanly remove Velox:

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash
```

To perform a **deep clean** (also purging all user configs and databases in `~/.velox`):
```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash -s -- --purge
```

---

## 🛠️ Building from Source

Requires Go 1.23+:

```bash
git clone https://github.com/AmirAM03/velox.git
cd velox
go build -ldflags "-s -w -X main.version=dev" -o velox .
```

---

## License

MPL-2.0 (Mozilla Public License Version 2.0)
