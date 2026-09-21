# Recommended stack

## Core decision: don't write the protocols yourself
Use **Xray-core** or **sing-box** as the data plane. VLESS/REALITY, VMess, Trojan, Shadowsocks, Hysteria2 and TUIC are moving targets, and re-implementing them is a maintenance trap. Your value is in the orchestration layer: parsing, testing, scoring, and sharing.

## Language: Go
Both cores are Go libraries, so you can embed them in-process instead of shelling out. That is the single biggest performance lever for high-volume testing.

- **Embedded vs. subprocess:** spawning one core process per config, each with its own local port, tops out at hundreds of configs before memory, ports, and process startup dominate. With an embedded core you build an instance containing only an outbound, no inbound and no listening port, and dial through it directly (e.g. `core.Dial` in Xray, or the outbound manager in sing-box). Thousands of configs can be tested concurrently in one process.
- **Concurrency:** goroutines, `context` timeouts, and semaphores fit a test pipeline well. `errgroup` and worker pools give you bounded parallelism with clean cancellation.
- **Deployment:** one static binary per OS, easy cross-compilation.

**Alternative:** Rust (tokio) for the orchestrator, spawning the core as a sidecar. It's a great language, but you lose in-process embedding, and Rust protocol implementations for REALITY/XTLS aren't mature enough to rely on. Python is fine for a prototype but won't hold up at high volume.

## Licensing (decide early)
Xray-core is MPL-2.0 (file-level copyleft, easy to embed). sing-box is GPL-3.0 with an additional clause, so embedding it makes your program GPL. Xray is the safer default unless you need sing-box's broader protocol set or its TUN stack.

## Pipeline architecture
1. **Ingest:** URI schemes (`vmess://`, `vless://`, `trojan://`, `ss://`, `hy2://`, `tuic://`), base64 subscriptions, Clash YAML, and raw Xray/sing-box JSON.
2. **Normalize and dedupe:** convert everything to one canonical struct and hash the normalized fields, since many "different" configs are the same server.
3. **Stage 0 (cheap filters):** DNS resolve, TCP connect, and TLS/REALITY handshake to the server. This eliminates most dead configs in milliseconds.
4. **Stage 1 (real proxy test):** dial through the outbound to your custom targets (HTTP 204 endpoints, specific IP:ports, TLS handshakes), measuring connect time, TTFB, and status.
5. **Stage 2 (deep test for survivors only):** throughput on a small download, UDP/DNS through the proxy, jitter over several rounds, and stability.
6. **Score and store:** a weighted score plus history, so flaky configs get penalized over time.

Use adaptive concurrency (raise the worker count until timeout rate climbs), per-host rate limits, and a raised file-descriptor limit.

## Storage
**SQLite (WAL mode)** via `modernc.org/sqlite` (pure Go, no CGO). It's simple and fast enough for millions of rows. Add DuckDB later only if you want heavy analytics. Redis is unnecessary for a single-machine tool.

## Sharing the proxy
- A local **mixed inbound** (SOCKS5 + HTTP) on localhost, bound to a chosen port.
- Your own **selector layer**: keep the best N configs warm and hot-swap or fail over when health checks fail. Xray's balancer and observatory can do part of this, but a custom layer gives you control over scoring.
- **System integration:** per-OS system proxy setters (Windows registry/WinINET, macOS `networksetup`, Linux gsettings), and optionally **TUN mode** for system-wide capture (sing-box's TUN is the more mature option).

## Interfaces and tooling
- **Structure:** core as a library, a daemon on top, and a local gRPC or HTTP API. Then the CLI (`cobra`) and any GUI are thin clients.
- **GUI later:** Wails (Go + web frontend) if you go Go, or a local web UI served by the daemon.
- **Observability:** `slog` or `zerolog`, Prometheus metrics, and `pprof` from day one.
- **Reliability:** Go's native fuzzing on the parsers (they will receive garbage), golden-file tests, panic recovery per worker, and graceful shutdown.

## Risks to plan for
- Embedding cores ties you to their internal APIs, which change between versions. Pin versions and wrap the core behind your own interface so it's swappable.
- A massive test run can look like a scan to your ISP or the servers. Keep rate limits configurable.

Two questions would sharpen the next step: which platforms are you targeting (Windows, Linux, macOS, Android)? And do you want a GUI soon, or is CLI plus daemon fine for the first milestone? Once you answer, I can sketch the project layout and the canonical config struct.