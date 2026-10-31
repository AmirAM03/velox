# Velox

<div align="center">

**Self-hosted V2Ray jump server: subscriptions in, the best node per target service out.**  
*Fetches, deduplicates and benchmarks thousands of free configs, then serves each target service on its own authenticated port.*

[![Release](https://img.shields.io/github/v/release/AmirAM03/velox?style=flat-square&color=06b6d4)](https://github.com/AmirAM03/velox/releases)
[![Go Version](https://img.shields.io/badge/Go-1.23%2B-blue?style=flat-square)](https://go.dev)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL_2.0-indigo.svg?style=flat-square)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Linux%20%7C%20Windows%20%7C%20macOS-emerald.svg?style=flat-square)](https://github.com/AmirAM03/velox/releases)

</div>

---

## 📑 Table of Contents

- [Overview & Architecture](#-overview--architecture)
- [🖥️ Web Dashboard](#️-web-dashboard)
- [⚡ Quick Start](#-quick-start)
- [🚀 CLI Command Reference](#-cli-command-reference)
- [🌐 Supported Protocols & Transports](#-supported-protocols--transports)
- [🎯 Real-World Delay Benchmarking](#-real-world-delay-benchmarking)
- [📚 In-Depth Documentation](#-in-depth-documentation)
- [🛠️ Building from Source](#️-building-from-source)
- [🗑️ Uninstallation](#️-uninstallation)
- [License](#license)

---

## ⚡ Overview & Architecture

Velox runs on a Linux VPS as a **jump server**. It pulls V2Ray configs from the subscription links you give it, tests them against the services your users need, and exposes one authenticated proxy port per service. Users connect to the VPS; the VPS forwards each connection through the best node it found for that service.

- **Subscriptions only**: you add subscription URLs; Velox fetches them on a schedule, parses and deduplicates every config (SHA-256 canonical identity) and drops configs that no subscription lists any more.
- **Profiles**: each profile is a target service (e.g. `https://www.youtube.com`) plus a public port, username and password. Users point any SOCKS5 or HTTP client at `VPS_IP:port`.
- **Per-service ranking**: every cycle, cheap TCP/TLS checks run once over all configs; only the fastest 500 survivors get the real HTTP test, once per profile target. Each profile keeps its 10 best nodes and fails over to the next one when the active node stops answering.
- **Embedded Xray-core**: no external binaries; one shared engine serves all profiles.
- **Pure-Go SQLite (WAL)**: single static binary, no CGO.

```
 Users ──► VPS_IP:20001 (profile "YouTube", user/pass) ──┐
 Users ──► VPS_IP:20002 (profile "Telegram", user/pass) ─┤
                                                         ▼
                                       ┌──────────────────────────────┐
  Subscription URLs ──► fetch, parse,  │  Velox (systemd service)     │
  (every 6h)            deduplicate ──►│  • per-profile best nodes    │──► best V2Ray node ──► target service
                                       │  • shared Xray engine        │
                                       │  • dashboard on 127.0.0.1    │
                                       └──────────────────────────────┘
                     every 30m: TCP/TLS once for all configs ──► HTTP test per profile target ──► top 10 per profile
```

---

## 🖥️ Web Dashboard

All management happens in the embedded dashboard. It listens on `127.0.0.1:18080` only and has no login, so it is never exposed to the internet. Reach it through an SSH tunnel:

```bash
ssh -L 18080:127.0.0.1:18080 user@your-vps   # then open http://127.0.0.1:18080 locally
```

| Tab | What it does |
| :--- | :--- |
| **Dashboard** | Config counts, protocol mix, profile and optimizer status. |
| **Profiles** | Create profiles (name, target URL, port, username, password), see each one's active node and latency, copy ready-to-use `socks5://` / `http://` URLs, set the test and fetch schedule, re-test on demand. |
| **Sources** | Add, disable or delete subscription URLs; see last fetch time, config count and errors; fetch all now. |
| **Configs** | Search, filter and sort every stored config. |
| **Benchmark** | Edit the test methodology (TCP/TLS/HTTP steps, timeouts) used by the optimizer, and run manual benchmarks. |
| **Application Logs** | Persistent logs with search, level/subsystem filters, retention and export. |

---

## ⚡ Quick Start

### Linux VPS (Ubuntu / Debian / RHEL / Arch)

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/install.sh | sudo bash
```

This installs the binary, creates a `velox` system user and starts a hardened `velox` systemd service. Then:

1. Open the SSH tunnel above and browse to `http://127.0.0.1:18080`.
2. **Sources** tab: add your subscription URLs.
3. **Profiles** tab: add one profile per target service.
4. Open each profile port in the firewall, e.g. `sudo ufw allow 20001/tcp`.
5. Give users `socks5://user:pass@VPS_IP:20001` (copy it from the Profiles tab).

To install a binary you built yourself: `sudo bash install.sh --binary ./velox`. Logs: `journalctl -u velox -f`.

See the [Linux Deployment Guide](docs/ubuntu.md) for details.

---

## 📋 Flags & Options Reference

Velox operates cleanly in user space without requiring any background daemons or services:

```bash
# Launch on default port (18080)
velox

# Specify custom port
velox --port 8080

# Specify custom data directory for SQLite database
velox --data-dir /path/to/data

# Enable debug logging in console
velox --log-level debug

# Output console logs in JSON format
velox --log-json
```

---

## 🪵 Persistent Application Logging & Retention Policy

Velox stores all engine, pipeline, ingestion, proxy, storage, web, and system events directly in the local SQLite database (`app_logs` table) in high-performance WAL mode:

- **No Scattered Log Files**: All logs reside inside `velox.db` alongside configs and benchmark scores.
- **Configurable Retention Window**: Select `24 Hours`, `3 Days`, `7 Days` (default), `30 Days`, `90 Days`, or custom retention in the Web UI.
- **Automated Background Pruning**: An automated hourly worker evaluates the retention policy, cleans expired logs, and reclaims space.
- **Live SSE Streaming**: New log entries stream live into the dashboard table in real time with attribute inspection drawers.
- **One-Click Export & Vacuum**: Export logs as structured JSON / NDJSON or clear and vacuum the database anytime.

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

## 🎯 Configurable Test Methodology & Execution Chain

Benchmarking in Velox is fully transparent and user-defined rather than a fixed black-box flow:

- **Sequential Stage Chaining**: Assemble any custom sequence of `TCP Port Ping`, `TLS / REALITY Handshake`, and `HTTP Delay Toward URL`.
- **Early Disqualification ("Necessary" Filter)**: Any stage can be marked as **Necessary (Hard Filter)**. If a proxy node fails a necessary test, it is immediately eliminated from the survivor pool, saving network I/O by skipping subsequent expensive stages.
- **Normalized Composite Score**: Whether running a single test (e.g. direct HTTP delay) or an ordered sequence (TCP Ping + Delay), Velox assigns a single normalized score per node (`primary_test` or `weighted_average` mode).
- **One-Click Presets**:
  - `Standard (3-Stage)`: Ping (Necessary) → TLS Handshake → HTTP Real Delay (Necessary & Primary).
  - `Direct URL Only`: Pure direct HTTP delay toward target endpoint.
  - `TCP Ping Only`: Fast TCP reachability check.
  - `Deep Verification`: Strict verification with weighted latency blending.

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

## 📚 In-Depth Documentation

For advanced deployments, performance tuning, and technical specifications:

- 🐧 [**Linux VPS Deployment Guide**](docs/ubuntu.md) — Install, dashboard tunnel, sources, profiles and operations.
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

To cleanly remove Velox:

```bash
# Standard uninstall
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash

# Deep clean (purges databases and configs in ~/.velox)
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash -s -- --purge
```

---

## License

[Mozilla Public License Version 2.0 (MPL-2.0)](LICENSE)
