# Velox

<div align="center">

**Self-hosted V2Ray jump server: subscriptions in, the best node per service out.**  
*Fetches, deduplicates and checks thousands of free configs against each service, then serves each service on its own authenticated port.*

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
- [🎯 How Profiles Are Benchmarked](#-how-profiles-are-benchmarked)
- [📚 In-Depth Documentation](#-in-depth-documentation)
- [🛠️ Building from Source](#️-building-from-source)
- [🗑️ Uninstallation](#️-uninstallation)
- [License](#license)

---

## ⚡ Overview & Architecture

Velox runs on a Linux VPS as a **jump server**. It pulls V2Ray configs from the subscription links you give it, tests them against the services your users need, and exposes one authenticated proxy port per service. Users connect to the VPS; the VPS forwards each connection through the best node it found for that service.

- **Subscriptions only**: you add subscription URLs; Velox fetches them on a schedule, parses and deduplicates every config (SHA-256 canonical identity) and drops configs that no subscription lists any more.
- **Profiles**: each profile is a built-in service check (first one: **Antigravity**, Google's own location and eligibility verdict) plus a public port, username and password. Users point any SOCKS5 or HTTP client at `VPS_IP:port`.
- **Per-service checks**: each profile type filters all configs with a cheap probe, then runs its real check (e.g. with a login session) on the fastest survivors. The best node found so far is live while the test still runs. Each profile keeps its 10 best nodes and fails over to the next one when the active node stops answering.
- **Embedded Xray-core**: no external binaries; one shared engine serves all profiles.
- **Pure-Go SQLite (WAL)**: single static binary, no CGO.

```
 Users ──► VPS_IP:20001 (profile "Antigravity", user/pass) ┐
 Users ──► VPS_IP:20002 (future profile types)  ──────────┤
                                                         ▼
                                       ┌──────────────────────────────┐
  Subscription URLs ──► fetch, parse,  │  Velox (systemd service)     │
  (every 6h)            deduplicate ──►│  • per-profile best nodes    │──► best V2Ray node ──► the service
                                       │  • shared Xray engine        │
                                       │  • dashboard on 127.0.0.1    │
                                       └──────────────────────────────┘
                     per profile: TCP check ──► type's probe ──► type's real check (session) ──► top 10, best live first
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
| **Profiles** | One card per built-in profile type: the last test's funnel (configs → reachable → reach the service → checked → candidates, best delay), labels (e.g. countries), live progress, active node, **Checked configs** list, settings and account sign-in, copy ready-to-use `socks5://` / `http://` URLs, turn scheduled re-testing on or off per profile, **Test now** per profile, set the test and fetch schedule. |
| **Sources** | Add, disable or delete subscription URLs; see last fetch time, config count and errors; fetch all now. |
| **Configs** | Search, filter and sort every stored config. |
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
3. **Profiles** tab: **Edit** a profile, fill in its settings (Antigravity: your agy session) and enable it.
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

Velox stores all engine, optimizer, ingestion, proxy, storage, web, and system events directly in the local SQLite database (`app_logs` table) in high-performance WAL mode:

- **No Scattered Log Files**: All logs reside inside `velox.db` alongside configs and profiles.
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

## 🎯 How Profiles Are Benchmarked

Profiles are **built-in profile types**, one profile per type, created at startup. A type is code (`internal/checks`): it knows how to test a node for its service, and which settings it needs, such as a login session. Profiles cannot be added from the dashboard and there are no free-form target URLs; you edit a profile's port, user, password, Auto re-test and its type's settings (secret settings are never sent back to the browser). An enabled profile needs settings its type accepts.

| Type | Checks | Needs |
|---|---|---|
| **Antigravity** | Asks Google, with your agy (Antigravity CLI) session, through each node: which country Google places the node in, and whether that country may use the Antigravity API. Google uses its own geo-IP data, which often differs from public IP-location sites, so this is the only reliable test. | **Sign in with Google** (or paste agy's token file) and agy's OAuth client secret (`GOCSPX-…`, embedded in the agy binary). |

**Signing in (Antigravity)**: in the Profiles tab, **Edit** the profile, paste agy's client secret and press **Sign in with Google**. The browser goes to Google's sign-in page and comes back to the dashboard (through your SSH tunnel, whatever local port it uses); Velox then stores a refresh token for that account and shows "Signed in as …". This is the standard desktop OAuth flow (loopback redirect + PKCE) with agy's public desktop client and the scopes agy's own token carries, so agy itself is not needed. The code exchange goes direct from the server, or through the profile's nodes if the server cannot reach Google. **Sign out** forgets the stored session (it does not revoke it at Google; do that in your Google account's third-party access page). Pasting an agy token file still works as an alternative.

One flow runs every type. It runs on the schedule (profiles with **Auto** on), after new configs arrive, and whenever **Test now** is pressed:

1. **Reachability** (shared by all profiles in a run): a TCP connect to every stored config, 512 at a time, 2 s each.
2. **Filter**: the type's cheap probe through each reachable node, 128 at a time, 6 s each, at most 4 minutes. A probe never uses an account. Antigravity: any answer from Google's API over a verified TLS connection.
3. **Check**: the type's real check, **once per node**, fastest probe first, 6 nodes at a time. Each checked config is **labeled** and, if it passes, becomes a **candidate**; the response time of the check is recorded. Antigravity: with your Google session, Google's eligibility answer (`aicode fetchConfig`): 200 = eligible, labeled with the country Google places the node in (`fetchUserInfo`); 403 `LOCATION_NOT_ELIGIBLE` = rejected, labeled with Google's detected country. The OAuth access token is refreshed through a node about once an hour and kept in memory only. A run stops after 30 candidates or 150 checked nodes (or 6 minutes).
4. **Rank and serve**: candidates are ranked by the response time recorded in step 3. Every new candidate that beats the others serves the port at once, with the previous candidates behind it as fallbacks; after the run the best 10 serve. All checked configs, labeled, are saved and listed on the card under **Checked configs** (candidates, or everything with the reason a config was rejected); each row shows the country Google detected, whether it is eligible, and the raw JSON response Google returned.

If nothing passes, or the session is refused, the profile keeps its previous nodes and the Profiles tab shows why, e.g. `rejected HK 20, IR 3` or `session error: …`.

**Account note**: the real check sends your session through up to 150 different IPs per run. The caps keep this bounded, but Google may still treat it as unusual account activity; a separate Google account is the safer choice.

**Adding a service** = one new `checks.Kind` (fields + a `bench.Check` with `Probe` and `Round`) in `internal/checks`, added to `checks.Kinds`.

---

## 📚 In-Depth Documentation

For advanced deployments, performance tuning, and technical specifications:

- 🐧 [**Linux VPS Deployment Guide**](docs/ubuntu.md) — Install, dashboard tunnel, sources, profiles and operations.
- 🔬 [**Architecture & Internals**](docs/architecture.md) — Benchmark flow, in-process core, deduplication and storage.
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
