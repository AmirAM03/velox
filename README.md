# Velox

<div align="center">

**High-performance V2Ray proxy engine & interactive embedded web dashboard.**  
*Ingest, sanitize, deduplicate, benchmark, score, and route thousands of proxy nodes at speed.*

[![Release](https://img.shields.io/github/v/release/AmirAM03/velox?style=flat-square&color=06b6d4)](https://github.com/AmirAM03/velox/releases)
[![Go Version](https://img.shields.io/badge/Go-1.23%2B-blue?style=flat-square)](https://go.dev)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL_2.0-indigo.svg?style=flat-square)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Linux%20%7C%20Windows%20%7C%20macOS-emerald.svg?style=flat-square)](https://github.com/AmirAM03/velox/releases)

</div>

---

## 📑 Table of Contents

- [Overview & Architecture](#-overview--architecture)
- [🖥️ Interactive Web Dashboard](#️-interactive-web-dashboard)
- [⚡ Quick Start](#-quick-start)
  - [Linux (Ubuntu / Debian / Arch / RHEL)](#linux-ubuntu--debian--arch--rhel)
  - [Windows (amd64 / arm64)](#windows-amd64--arm64)
- [🚀 CLI Command Reference](#-cli-command-reference)
- [🌐 Supported Protocols & Transports](#-supported-protocols--transports)
- [🎯 Real-World Delay Benchmarking](#-real-world-delay-benchmarking)
- [🪟 Multi-Platform System Proxy](#-multi-platform-system-proxy)
- [📚 In-Depth Documentation](#-in-depth-documentation)
- [🛠️ Building from Source](#️-building-from-source)
- [🗑️ Uninstallation](#️-uninstallation)
- [License](#license)

---

## ⚡ Overview & Architecture

Velox is built from the ground up for power users, developers, and researchers managing large pools of proxy configurations. Unlike traditional GUI clients that wrap external background daemons, Velox embeds **Xray-core** directly in-process via Go API calls (`core.New` and `core.Dial`), resulting in:

- **Zero Background Daemons**: Runs strictly in user-space when invoked; zero hidden background processes or system services.
- **Microsecond In-Process Routing**: Direct memory piping without loopback proxy hop overhead or socket descriptor exhaustion.
- **Cryptographic Deduplication**: Automatic SHA-256 canonical hashing eliminates identical configurations across multiple subscriptions.
- **Multi-Stage Progressive Pipeline**: Filters dead nodes across 3 concurrent stages before running expensive real delay HTTP requests.
- **Pure-Go SQLite WAL**: High-concurrency local persistence with zero CGO dependencies.

```
 [ Subscriptions / Files / Stdin ]
                 │
                 ▼
 ┌───────────────────────────────┐
 │   Ingest & Cryptographic      │ ──► Canonical SHA-256 Deduplication
 │   Sanitization Pipeline       │
 └───────────────┬───────────────┘
                 ▼
 ┌───────────────────────────────┐
 │    Pure-Go SQLite WAL DB      │ ──► Fast Indexed Queries & Scoring
 └───────────────┬───────────────┘
                 │
                 ▼
 ┌───────────────────────────────┐
 │  3-Stage Concurrent Pipeline  │
 │  • S0: DNS+TCP (2000 workers) │
 │  • S1: TLS/REALITY (500 wkr)  │
 │  • S2: In-Process HTTP (150)  │ ──► Real Delay (TTFB) against Google / Cloudflare
 └───────────────┬───────────────┘
                 ▼
 ┌───────────────────────────────┐
 │   In-Process Xray Engine      │ ──► Mixed SOCKS5 + HTTP Inbound (127.0.0.1:1080)
 │   & Embedded Web Dashboard    │ ──► Zero-Dependency Modern Dark SPA (18080)
 └───────────────────────────────┘
```

---

## 🖥️ Interactive Web Dashboard

Velox embeds a zero-dependency, modern dark-mode single-page application directly inside the compiled binary. Launch it anytime:

```bash
velox ui
# Aliases:
velox dashboard
velox --ui
```

Automatically opens `http://127.0.0.1:18080` in your default browser:

| Feature Tab | Capabilities |
| :--- | :--- |
| **Telemetry Overview** | Real-time proxy status, active node latency, working nodes tally, and database statistics. |
| **Config Explorer** | Searchable & protocol-filtered table (`VLESS`, `Trojan`, `VMess`, `Shadowsocks`, `Hysteria2`) with ping tags and one-click URI clipboard copy. |
| **Ingest & Parse** | Dual-input ingestion for subscription URLs and raw configuration paste with cryptographic deduplication. |
| **Live Benchmarks** | Multi-stage speed & latency tests against destination presets (`Google`, `Cloudflare`, `YouTube`, `GitHub`) with per-node delay tables. |
| **Proxy Control** | One-click connect / disconnect for the in-process proxy engine (`127.0.0.1:1080`) and one-click OS system proxy toggle. |

To run on a custom port without auto-launching a browser:
```bash
velox ui --port 8080 --no-open
```

---

## ⚡ Quick Start

### Linux (Ubuntu / Debian / Arch / RHEL)

Install or upgrade to the latest release with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/install.sh | sudo bash
```

See the [Linux Deployment Guide](docs/ubuntu.md) for distribution setup and environment variable configuration.

### Windows (amd64 / arm64)

1. Download the latest `velox-vX.Y.Z-windows-amd64.zip` from [GitHub Releases](https://github.com/AmirAM03/velox/releases).
2. Extract `velox.exe` to any folder in your PATH (e.g. `C:\Tools\velox`).
3. Run `velox.exe ui` in PowerShell or Command Prompt to launch the Web Dashboard!

See the [Windows User Guide](docs/windows.md) for PowerShell one-liner and system proxy integration.

---

## 🚀 CLI Command Reference

Velox operates cleanly in the foreground without requiring any background services:

### 1. Ingest Configurations
```bash
# Ingest from one or more subscription URLs
velox parse --sub "https://example.com/sub/token"

# Ingest from multiple subscriptions
velox parse \
  --sub "https://sub1.com/link" \
  --sub "https://sub2.com/link"

# Ingest from local file or standard input
velox parse --file my_configs.txt
cat my_configs.txt | velox parse
```

### 2. Run Real Delay Benchmarks
```bash
# Benchmark against default target (Google 204)
velox test

# Benchmark against custom destination URLs
velox test --target "https://www.google.com" --target "https://cloudflare.com"

# Filter by protocol or limit concurrency
velox test --protocol vless --limit 100
```

### 3. Inspect Ranked Configurations
```bash
# Display top-ranked nodes based on composite score
velox list --limit 10

# Display raw proxy connection URIs
velox list --show-uri

# Filter table by protocol
velox list --protocol trojan
```

### 4. Connect Through Proxy
```bash
# Start in-process proxy engine (Mixed SOCKS5 + HTTP on 127.0.0.1:1080)
velox connect

# Start proxy and automatically enable OS system proxy
velox connect --system

# Specify custom inbound port
velox connect --port 2080
```

### 5. Database Deduplication
```bash
# Scan database and prune duplicate configs by cryptographic hash
velox dedup
```

---

## 🌐 Supported Protocols & Transports

| Protocol | Transports Supported | Security / TLS | In-Process Engine |
| :--- | :--- | :--- | :---: |
| **VLESS** | TCP, WebSocket, gRPC, HTTPUpgrade, SplitHTTP | TLS, REALITY (Vision), None | ✅ Embedded Xray-Core |
| **Trojan** | TCP, WebSocket, gRPC | TLS | ✅ Embedded Xray-Core |
| **VMess** | TCP, WebSocket, gRPC, HTTPUpgrade | TLS, None | ✅ Embedded Xray-Core |
| **Shadowsocks** | TCP, UDP | AEAD (AES-128/256-GCM, Chacha20) | ✅ Embedded Xray-Core |
| **Hysteria 2** | UDP (QUIC) | TLS, Obfs (Salamander) | ✅ Parser & DB Storage |
| **WireGuard** | UDP | Kernel / Userspace | ✅ Parser & DB Storage |

---

## 🎯 Real-World Delay Benchmarking

Velox avoids misleading ICMP ping tests by measuring the **Time to First Byte (TTFB)** of real HTTP requests routed through each proxy node. The Web Dashboard includes one-click presets:

| Destination Preset | Target URL | Use Case |
| :--- | :--- | :--- |
| **🌐 Google (204)** | `https://www.google.com/generate_204` | Fast lightweight HTTP 204 connectivity test *(Default)* |
| **🔍 Google Web** | `https://www.google.com` | Standard search engine connectivity & redirect test |
| **⚡ Cloudflare (204)**| `https://cp.cloudflare.com/generate_204` | Global Anycast edge delay check |
| **☁️ CF Trace** | `https://www.cloudflare.com/cdn-cgi/trace` | Diagnostic trace verifying client IP and data center |
| **📺 YouTube** | `https://www.youtube.com/generate_204` | Media CDN and video streaming reachability |
| **🐙 GitHub** | `https://github.com` | Developer tools and code repository access |
| **📶 GStatic** | `https://www.gstatic.com/generate_204` | Android and Chrome connectivity check |

---

## 🪟 Multi-Platform System Proxy

When invoked with `--system` or when toggling the switch in the Web Dashboard, Velox automatically configures OS-level proxy settings without manual user intervention:

- **Windows**: Updates WinINet registry keys (`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`). System proxy is automatically restored when Velox disconnects.
- **Linux (GNOME)**: Configures GNOME desktop settings via `gsettings set org.gnome.system.proxy mode 'manual'`.
- **Linux (KDE)**: Updates KDE desktop settings via `kwriteconfig6` / `kwriteconfig5`.
- **Terminal Shells**: Export standard environment variables:
  ```bash
  export http_proxy="http://127.0.0.1:1080"
  export https_proxy="http://127.0.0.1:1080"
  export all_proxy="socks5://127.0.0.1:1080"
  ```

---

## 📚 In-Depth Documentation

For advanced deployments, performance tuning, and technical specifications:

- 🐧 [**Linux / Ubuntu Deployment Guide**](docs/ubuntu.md) — One-liner install, APT proxy, and shell integration.
- 🪟 [**Windows User Guide**](docs/windows.md) — Windows installation, WinINet registry automation, and PowerShell tips.
- 🔬 [**Architecture & Internals**](docs/architecture.md) — Pipeline mechanics, in-process core, and scoring math.
- ⚙️ [**Configuration Manual**](docs/configuration.md) — Full `config.yaml` schema, tuning profiles, and overrides.

---

## 🛠️ Building from Source

Requires Go 1.23+:

```bash
git clone https://github.com/AmirAM03/velox.git
cd velox
go build -ldflags "-s -w -X main.version=dev" -o velox ./cmd/velox
```

---

## 🗑️ Uninstallation

To cleanly remove Velox and reset system proxy settings:

```bash
# Standard uninstall
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash

# Deep clean (purges databases and configs in ~/.velox)
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash -s -- --purge
```

---

## License

[Mozilla Public License Version 2.0 (MPL-2.0)](LICENSE)
