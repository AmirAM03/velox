# V2Ray Engine — Revised Implementation Plan

## Overview

A high-performance Go application that ingests large volumes of V2Ray/proxy configs from multiple sources, validates and tests them through a multi-stage pipeline against user-defined target addresses, scores and ranks them, and exposes the best working configs as a local system proxy with automatic failover.

> [!NOTE]
> This plan is a **revised and expanded** version of your [PlanV1.md](file:///d:/Projects/v2ray%20engine/PlanV1.md). Your original plan is architecturally strong. The revisions below refine tech choices, add missing details, restructure the pipeline, and lay out a concrete project structure for development.

---

## Analysis of PlanV1 — What's Confirmed ✅

Your original plan gets the big decisions right:

| Decision | Verdict |
|---|---|
| **Go as the language** | ✅ Confirmed — best choice for embedding Xray-core in-process |
| **Xray-core over sing-box** | ✅ Confirmed — MPL-2.0 is licensing-friendly, `core.Dial` API is proven, VLESS/REALITY/XTLS support is best-in-class |
| **Embedded core (not subprocess)** | ✅ Confirmed — the single biggest perf lever; avoids port exhaustion and process overhead |
| **Multi-stage pipeline** | ✅ Confirmed — cheap-first filtering is essential at volume |
| **SQLite WAL mode** | ✅ Confirmed — `modernc.org/sqlite` is production-ready in 2026, no CGO needed |
| **Cobra for CLI** | ✅ Confirmed — industry standard, integrates with Viper for config management |
| **Wails for future GUI** | ✅ Confirmed — natural fit for a Go backend, lightweight |
| **Core-behind-interface pattern** | ✅ Confirmed — critical for version pinning and future core swaps |

---

## Key Revisions & Additions 🔄

### 1. API Layer: ConnectRPC instead of gRPC

Your plan mentions "gRPC or HTTP API". **Recommendation: ConnectRPC**.

- **Why**: ConnectRPC is wire-compatible with gRPC but works over standard HTTP/1.1+HTTP/2 without needing Envoy or gRPC-Web proxies. You can debug with `curl`. It generates idiomatic Go code, and any future gRPC client still works against it.
- **Impact**: The CLI and GUI become thin ConnectRPC clients. A future web UI can call the same API natively from the browser with no proxy.

### 2. Logging: `slog` (stdlib) over `zerolog`

Your plan offers `slog` or `zerolog`. **Recommendation: `slog`**.

- **Why**: Go 1.21+ `slog` is now the standard structured logging interface. Zero external dependency. The `slog.Handler` interface allows plugging in `zerolog` or any backend later without changing call sites. Start with stdlib, add a fancy handler only if needed.

### 3. Config Management: Viper + YAML config file

Not mentioned in PlanV1. Add **Viper** (pairs naturally with Cobra) for layered configuration:
- Default values → config file (`~/.v2ray-engine/config.yaml`) → environment variables → CLI flags.
- Manages settings like concurrency limits, test targets, proxy port, rate limits, database path.

### 4. Pipeline Refinement: 4-Stage with Adaptive Concurrency

Your 3-stage pipeline is mostly right but needs restructuring:

| Stage | Name | What | Concurrency | Kill Criterion |
|---|---|---|---|---|
| **0** | DNS + TCP Probe | Resolve DNS, TCP SYN to `host:port` | Very high (2000+) | Timeout > 3s |
| **1** | TLS/Protocol Handshake | TLS handshake + REALITY/XTLS negotiation where applicable | High (500+) | Handshake failure or timeout > 5s |
| **2** | Proxy Functional Test | `core.Dial` through outbound → HTTP request to user's custom targets (204 endpoint, IP check, specific URLs) | Medium (100-200) | HTTP error, wrong IP, timeout > 10s |
| **3** | Deep Quality Test | Throughput (small download), jitter (5 rounds), UDP/DNS resolution, latency percentiles (P50/P95/P99) | Low (10-30) | Below quality threshold |

> [!IMPORTANT]
> **Stage 1 (TLS handshake) is separated from Stage 0** because DNS+TCP is nearly free (milliseconds), while TLS/REALITY handshakes are more expensive. Splitting them prevents wasting TLS handshakes on TCP-dead servers.

**Adaptive concurrency**: Start at a baseline worker count. If timeout rate exceeds a configurable threshold (e.g., 15%), reduce workers. If it drops below 5%, increase. Use a simple AIMD (Additive Increase Multiplicative Decrease) controller.

### 5. Scoring Model Additions

Your plan mentions "weighted score plus history." Expand this:

- **Composite score** = `w1*latency_p95 + w2*success_rate + w3*throughput + w4*stability_penalty + w5*recency_bonus`
- **Stability penalty**: exponential decay on flaky configs (success → fail → success pattern)
- **Recency bonus**: configs tested more recently get a small boost
- **Geographic affinity**: optional tag for configs that route well to specific regions
- **All weights configurable** via Viper config

### 6. Additional Protocol Support

Your ingest list is good. Add:
- **WireGuard** configs (Xray-core supports WireGuard outbound)
- **Clash Meta YAML** (distinct from standard Clash YAML — different fields for REALITY/VLESS)

### 7. System Proxy Integration — Platform Details

| Platform | Method | Notes |
|---|---|---|
| Windows | WinINET `InternetSetOption` + Registry `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings` | Must notify WinINET of changes via `InternetSetOptionW` |
| macOS | `networksetup -setwebproxy` / `-setsocksfirewallproxy` | Per-interface, need to detect active interface |
| Linux | `gsettings` (GNOME), `kwriteconfig5` (KDE), env vars (`http_proxy`, `https_proxy`) | Fragmented, env vars as fallback |

### 8. Health Check & Failover Loop

Not deeply specified in PlanV1. Add a **continuous health loop**:

1. Keep top-N configs (configurable, default 5) as "warm pool"
2. Run health checks every 30s (configurable) against the active config
3. If active config fails 2 consecutive checks → switch to next-best in warm pool
4. Background re-test warm pool configs every 5 minutes
5. Periodically promote/demote configs between warm pool and cold storage based on score

---

## Proposed Project Structure

```
v2ray-engine/
├── cmd/
│   └── v2ray-engine/          # CLI entrypoint (Cobra)
│       └── main.go
├── internal/
│   ├── config/                # Viper config loading & validation
│   │   └── config.go
│   ├── parser/                # Config parsing & normalization
│   │   ├── parser.go          # Unified parser interface
│   │   ├── vmess.go
│   │   ├── vless.go
│   │   ├── trojan.go
│   │   ├── shadowsocks.go
│   │   ├── hysteria2.go
│   │   ├── tuic.go
│   │   ├── wireguard.go
│   │   ├── subscription.go    # Base64 subscription decoder
│   │   ├── clash.go           # Clash/Clash Meta YAML
│   │   └── xray_json.go       # Raw Xray JSON configs
│   ├── model/                 # Canonical data structures
│   │   ├── proxy.go           # Canonical ProxyConfig struct
│   │   ├── result.go          # TestResult, Score structs
│   │   └── hash.go            # Deduplication hash logic
│   ├── core/                  # Xray-core abstraction layer
│   │   ├── engine.go          # Interface: Engine (Dial, BuildOutbound)
│   │   ├── xray.go            # Xray-core implementation
│   │   └── builder.go         # ProxyConfig → Xray outbound config
│   ├── pipeline/              # Multi-stage test pipeline
│   │   ├── pipeline.go        # Orchestrator
│   │   ├── stage_dns_tcp.go   # Stage 0: DNS + TCP
│   │   ├── stage_tls.go       # Stage 1: TLS/REALITY handshake
│   │   ├── stage_proxy.go     # Stage 2: Functional proxy test
│   │   ├── stage_quality.go   # Stage 3: Deep quality test
│   │   ├── worker.go          # Worker pool + adaptive concurrency
│   │   └── ratelimit.go       # Per-host rate limiter
│   ├── scorer/                # Scoring & ranking
│   │   ├── scorer.go          # Score computation
│   │   └── history.go         # Historical penalty/bonus
│   ├── storage/               # Database layer
│   │   ├── sqlite.go          # SQLite WAL implementation
│   │   ├── migrations.go      # Schema migrations
│   │   └── queries.go         # Prepared queries
│   ├── proxy/                 # Local proxy server
│   │   ├── server.go          # Mixed SOCKS5+HTTP inbound
│   │   ├── selector.go        # Config selector & hot-swap
│   │   └── health.go          # Health check & failover
│   ├── system/                # OS integration
│   │   ├── proxy_windows.go   # Windows system proxy
│   │   ├── proxy_darwin.go    # macOS system proxy
│   │   └── proxy_linux.go     # Linux system proxy
│   └── api/                   # Daemon API
│       ├── proto/             # Protobuf definitions
│       │   └── engine.proto
│       ├── server.go          # ConnectRPC server
│       └── handlers.go        # API handlers
├── proto/                     # Top-level proto definitions (for buf)
│   ├── buf.yaml
│   └── engine/
│       └── v1/
│           └── engine.proto
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## Technology Stack Summary

| Component | Choice | Rationale |
|---|---|---|
| Language | **Go 1.22+** | In-process core embedding, goroutines, single binary |
| Proxy Core | **Xray-core** (pinned version) | MPL-2.0, `core.Dial`, VLESS/REALITY/XTLS |
| CLI | **Cobra + Viper** | Industry standard, layered config |
| API | **ConnectRPC + Protobuf** | gRPC-compatible, curl-debuggable, browser-native |
| Database | **SQLite WAL** via `modernc.org/sqlite` | Pure Go, no CGO, production-ready |
| Logging | **`slog`** (stdlib) | Zero dependency, pluggable handler |
| Metrics | **Prometheus** (`prometheus/client_golang`) | Standard observability |
| Profiling | **`net/http/pprof`** | Built-in, zero overhead when idle |
| Testing | **Go native** + fuzzing | Built-in fuzzing for parsers, golden files |
| Build | **Makefile + goreleaser** | Cross-compilation, release automation |
| Future GUI | **Wails v3** | Go-native, lightweight, WebView-based |

---

## Development Phases

### Phase 1 — Foundation (Milestone 1: CLI + Parse + Test)
> Goal: Parse configs, run the pipeline, output results to stdout/SQLite.

1. Project scaffolding (`go mod init`, directory structure, Makefile)
2. Canonical `ProxyConfig` struct and dedup hash
3. Parsers for top-3 formats: `vmess://`, `vless://`, base64 subscriptions
4. Xray-core abstraction layer (`Engine` interface + Xray implementation)
5. Pipeline stages 0-2 (DNS+TCP, TLS handshake, proxy functional test)
6. Basic scoring (latency + success rate)
7. SQLite storage layer with migrations
8. Cobra CLI: `parse`, `test`, `list` commands
9. Basic `slog` logging + `pprof` endpoint

### Phase 2 — Proxy & Reliability
> Goal: Working local proxy with health checks and failover.

1. Mixed SOCKS5+HTTP inbound proxy server
2. Config selector with warm pool
3. Health check loop + automatic failover
4. System proxy integration (Windows first, then macOS/Linux)
5. Pipeline Stage 3 (deep quality testing)
6. Adaptive concurrency controller
7. Per-host rate limiting
8. Cobra CLI: `connect`, `status`, `disconnect` commands

### Phase 3 — Daemon & API
> Goal: Long-running daemon with ConnectRPC API.

1. Daemon mode (background process with PID file)
2. ConnectRPC API (proto definitions, server, handlers)
3. CLI becomes a thin ConnectRPC client
4. Full scoring model with history and decay
5. Additional parsers (Trojan, SS, Hysteria2, TUIC, WireGuard, Clash Meta)
6. Prometheus metrics endpoint
7. Graceful shutdown with connection draining

### Phase 4 — Polish & GUI
> Goal: Production-grade reliability and optional GUI.

1. Go fuzzing for all parsers
2. Golden-file tests for parser edge cases
3. Integration tests with mock servers
4. Goreleaser for multi-platform builds
5. Wails GUI (dashboard: config list, scores, active proxy, logs)
6. TUN mode investigation (sing-box TUN as optional sidecar)

---

## Open Questions

> [!IMPORTANT]
> **Q1: Target Platforms** — Which platforms are you targeting for the initial release? Windows only, or Windows + Linux + macOS from the start? This affects the system proxy integration priority.

> [!IMPORTANT]
> **Q2: Custom Test Targets** — What are your primary test targets? A 204 endpoint you control? Specific websites? IP-check services? This determines how Stage 2 is configured.

> [!IMPORTANT]
> **Q3: Config Sources** — Where do configs come from? Manual paste, subscription URLs, Telegram channels, file import, or all of the above? This affects the ingest layer design.

> [!IMPORTANT]
> **Q4: CLI-first or Daemon-first?** — Is a CLI that runs in the foreground sufficient for Phase 1-2, with daemon mode coming in Phase 3? Or do you need background operation immediately?

> [!IMPORTANT]
> **Q5: Naming** — Do you have a name for the project, or should we use `v2ray-engine` as the working name? This affects the Go module path (`github.com/<you>/<name>`).

---

## Verification Plan

### Automated Tests
- `go test ./...` — unit tests for all packages
- `go test -fuzz ./internal/parser/...` — fuzzing on all parsers
- Golden-file tests for parser output against known good/bad configs
- Integration tests: spin up Xray-core instance, test full pipeline locally

### Manual Verification
- Parse 1000+ configs from a real subscription and verify dedup rate
- Run pipeline against live configs and verify scoring accuracy
- Connect through local proxy and verify traffic routing
- Test failover by killing the active config's upstream
- Verify system proxy setting/unsetting on target OS

---

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Xray-core API breakage between versions | Pin version, wrap behind `Engine` interface, version-gate imports |
| Mass testing looks like a network scan | Per-host rate limits, configurable concurrency, backoff on errors |
| SQLite write contention under load | WAL mode, single-writer pattern, batch inserts |
| Port exhaustion during testing | No listening ports needed — `core.Dial` goes outbound-only |
| Config parsing edge cases / malformed URIs | Fuzzing, permissive parsing with validation, skip-and-log strategy |
| Xray-core memory leaks on instance creation | Instance pooling, explicit `Close()` lifecycle, pprof heap monitoring |
