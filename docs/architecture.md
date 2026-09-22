# Velox Architecture & Internals

This document details the internal architecture, design principles, and algorithms powering **Velox**.

---

## 1. Architectural Philosophy

Velox was engineered to address the critical bottlenecks present in legacy proxy clients:
1. **Zero External Daemon Dependency**: Rather than running background system daemons, Velox operates purely in user-space with an in-process engine.
2. **In-Process Core Execution**: By embedding Xray-core directly via Go API (`core.New`, `core.Dial`), Velox eliminates OS process-spawning overhead, inter-process communication (IPC) latency, and socket descriptor exhaustion.
3. **Multi-Stage Progressive Pipeline**: Testing thousands of proxy configurations is computationally expensive. Velox organizes tests into stages of increasing cost, filtering out dead nodes before invoking resource-heavy TLS and proxy handshakes.
4. **Cryptographic Deduplication**: Subscriptions and public pools often duplicate up to 70% of configurations under different labels. Velox identifies duplicate nodes by cryptographic hash, ensuring a clean SQLite database.
5. **Zero-CGO Pure-Go Storage**: Embedded SQLite WAL engine (`modernc.org/sqlite`) enables high-concurrency read/write operations without requiring a C compiler or external dynamic libraries.

---

## 2. System Architecture Diagram

```
                             ┌──────────────────────────────────────┐
                             │       User Interfaces (UI/CLI)       │
                             │  ┌───────────────┐ ┌───────────────┐ │
                             │  │ Web Dashboard │ │ Foreground CLI│ │
                             │  │   (18080)     │ │   (Cobra)     │ │
                             │  └───────┬───────┘ └───────┬───────┘ │
                             └──────────┼─────────────────┼─────────┘
                                        │                 │
                                        ▼                 ▼
 ┌──────────────────────────────────────────────────────────────────────────────┐
 │                              Velox Core Engine                               │
 │                                                                              │
 │  ┌─────────────────┐       ┌─────────────────┐       ┌────────────────────┐  │
 │  │ Ingest & Parser │ ────► │  Deduplication  │ ────► │ Pure-Go SQLite WAL │  │
 │  │ (VLESS/VMess/   │       │ (SHA-256 Hash)  │       │ (Indexes & Schemas)│  │
 │  │  Trojan/SS/Hy2) │       └─────────────────┘       └─────────┬──────────┘  │
 │  └─────────────────┘                                           │             │
 │                                                                │             │
 │                                  ┌─────────────────────────────┘             │
 │                                  ▼                                           │
 │  ┌────────────────────────────────────────────────────────────────────────┐  │
 │  │                  Multi-Stage Concurrent Pipeline                       │  │
 │  │                                                                        │  │
 │  │  ┌───────────────────┐    ┌───────────────────┐    ┌─────────────────┐ │  │
 │  │  │ Stage 0: DNS+TCP  │───►│   Stage 1: TLS    │───►│ Stage 2: Proxy  │ │  │
 │  │  │   (2000 workers)  │    │   (500 workers)   │    │  (150 workers)  │ │  │
 │  │  └───────────────────┘    └───────────────────┘    └────────┬────────┘ │  │
 │  └─────────────────────────────────────────────────────────────┼──────────┘  │
 │                                                                │             │
 │                                  ┌─────────────────────────────┘             │
 │                                  ▼                                           │
 │  ┌───────────────────┐       ┌───────────────────┐       ┌────────────────┐  │
 │  │  Smart Scorer     │ ◄──── │ Embedded Xray-Core│ ◄──── │ Mixed Inbound  │  │
 │  │  (P95 Latency &   │       │ (In-Process Dial) │       │ (SOCKS5 + HTTP │  │
 │  │   Stability Math) │       └─────────┬─────────┘       │  127.0.0.1:1080│  │
 │  └───────────────────┘                 │                 └───────▲────────┘  │
 └────────────────────────────────────────┼─────────────────────────┼───────────┘
                                          │                         │
                                          ▼                         │
                             ┌────────────────────────┐             │
                             │  External Destination  │             │
                             │   (Google/Cloudflare)  │             │
                             └────────────────────────┘             │
                                                                    │
                             ┌──────────────────────────────────────┴───────────┐
                             │           OS System Proxy Automation             │
                             │   Windows (WinINet)  /  Linux (GNOME / KDE)      │
                             └──────────────────────────────────────────────────┘
```

---

## 3. Multi-Stage Pipeline Mechanics

When benchmarking thousands of configurations, a naive test (establishing an end-to-end proxy connection for all nodes) would cause massive thread exhaustion and take minutes. Velox filters nodes through three progressive stages:

### Stage 0: DNS Resolution & TCP Reachability
- **Concurrency**: Up to `2,000` concurrent goroutines.
- **Timeout**: `1,500 ms`.
- **Purpose**: Strips out offline servers, expired domains, and dead IP addresses in milliseconds.
- **Metric**: TCP handshake latency.

### Stage 1: TLS & REALITY Handshake Validation
- **Concurrency**: Up to `500` concurrent goroutines.
- **Timeout**: `2,500 ms`.
- **Purpose**: Validates TLS certificates, ALPN negotiation, and cryptographic configuration validity for TLS and REALITY nodes. Non-TLS nodes skip directly to Stage 2.
- **Metric**: TLS handshake latency.

### Stage 2: Functional In-Process Proxy Test (Real Delay)
- **Concurrency**: Up to `150` concurrent goroutines.
- **Timeout**: `5,000 ms`.
- **Purpose**: Establishes an in-process proxy tunnel through `core.Dial` and sends an HTTP GET request to real-world target URLs (e.g. `https://www.google.com/generate_204` or `https://cp.cloudflare.com/generate_204`).
- **Metric**: **Time to First Byte (TTFB)** — measures the true network round-trip latency through the proxy node.
- **Status Evaluation**: Accepts standard `204 No Content`, `200 OK`, as well as standard HTTP `3xx` redirects (such as Google or Cloudflare's `301/302`), confirming end-to-end data delivery.

---

## 4. In-Process Core vs. Daemon Architecture

| Feature | Legacy Daemon / Subprocess (v2rayN, Clash) | Velox In-Process Architecture |
| :--- | :--- | :--- |
| **Execution Model** | Spawns separate `xray.exe` or `sing-box` background processes | Direct in-memory invocation via `core.New` & `core.Dial` |
| **System Overhead** | Process table bloat, IPC latency, zombie process risk | Single lightweight OS process, shared memory |
| **File Descriptors** | Heavy socket churn across local loopback connections | Direct memory pipe without loopback proxy hop |
| **Service Dependency**| Requires systemd service or Windows Service installation | Zero background daemons; clean user-initiated execution |
| **Port Conflicts** | Requires separate management ports (e.g. 10085 API, 1080 proxy)| Unified internal routing |

---

## 5. Cryptographic Deduplication

Velox eliminates duplicate proxy nodes across subscription feeds using SHA-256 canonical hashing:

1. **Normalization**:
   - Hostnames and protocols are downcased.
   - Default ports (e.g. 443 for TLS) are normalized.
   - Query parameters are sorted alphabetically to prevent permutation duplicates (`?sni=a&fp=b` vs `?fp=b&sni=a`).
   - Remark fragments (`#MyProxyName`) are decoupled from identity calculation.
2. **Hash Generation**:
   A deterministic byte sequence is generated from:
   $$\text{Hash} = \text{SHA256}(\text{Protocol} \parallel \text{Address} \parallel \text{Port} \parallel \text{UUID/Auth} \parallel \text{Network} \parallel \text{Security} \parallel \text{SNI} \parallel \text{Path/Opts})$$
3. **Database Upsert**:
   The calculated hash serves as the primary unique key in SQLite:
   ```sql
   INSERT INTO configs (...) VALUES (...)
   ON CONFLICT(id) DO UPDATE SET updated_at = CURRENT_TIMESTAMP;
   ```

---

## 6. Smart Composite Scoring Formula

Nodes that pass all testing stages are evaluated using a multi-factor weighted scoring algorithm where **lower scores represent faster, more reliable proxies**:

$$\text{Composite} = W_{\text{lat}} \cdot S_{\text{lat}} + W_{\text{succ}} \cdot S_{\text{succ}} + W_{\text{stab}} \cdot S_{\text{stab}} + W_{\text{rec}} \cdot S_{\text{rec}}$$

### Weights (Default Configuration)
- $W_{\text{lat}} = 0.30$ (P95 Latency)
- $W_{\text{succ}} = 0.30$ (Success Rate)
- $W_{\text{stab}} = 0.15$ (Stability Penalty)
- $W_{\text{rec}} = 0.10$ (Recency Bonus)
- $W_{\text{tp}} = 0.15$ (Throughput Capacity)

### Component Definitions
1. **Latency Score ($S_{\text{lat}}$)**:
   Calculates the 95th percentile (P95) latency across recent test samples:
   $$S_{\text{lat}} = \min\left(\frac{\text{Latency}_{\text{P95}}\text{ in ms}}{10000}, 1.0\right)$$
2. **Success Score ($S_{\text{succ}}$)**:
   $$S_{\text{succ}} = 1.0 - \frac{\text{Successful Tests}}{\text{Total Tests}}$$
3. **Stability Penalty ($S_{\text{stab}}$)**:
   Measures state flips (success-to-failure or failure-to-success transitions) to penalize jittery or unstable nodes:
   $$S_{\text{stab}} = \text{PreviousPenalty} \cdot \alpha + \text{TransitionRate} \cdot (1 - \alpha)$$
4. **Recency Factor ($S_{\text{rec}}$)**:
   Penalizes scores as tests grow stale over a 7-day decay horizon:
   $$S_{\text{rec}} = \min\left(\frac{\text{Hours Since Last Test}}{168}, 1.0\right)$$

---

## 7. Storage Engine (SQLite WAL)

Velox uses embedded SQLite compiled purely in Go via `modernc.org/sqlite`:
- **WAL Mode (Write-Ahead Logging)**: Enabled via `PRAGMA journal_mode=WAL;` to allow simultaneous non-blocking concurrent readers during active background writes.
- **Synchronous Normal**: `PRAGMA synchronous=NORMAL;` maximizes disk I/O throughput while guaranteeing database integrity.
- **Covering Indexes**:
  - `idx_configs_protocol`: Accelerates protocol filtering (`VLESS`, `VMess`, `Trojan`, `SS`).
  - `idx_scores_composite`: Enables instant sub-millisecond retrieval of top-ranked proxies.
  - `idx_test_results_config`: Fast aggregation for scoring pipelines.
