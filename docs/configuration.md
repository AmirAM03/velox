# Velox Configuration Guide

Velox is designed to run out-of-the-box with sensible zero-configuration defaults. However, every aspect of the pipeline, proxy engine, scoring algorithms, and targets can be customized via `config.yaml`.

---

## 1. Configuration File Locations

Velox searches for `config.yaml` in the following order:
1. **Explicit Flag**: `--config /path/to/config.yaml`
2. **User Home Directory**:
   - **Linux / macOS**: `~/.velox/config.yaml`
   - **Windows**: `%USERPROFILE%\.velox\config.yaml` (e.g. `C:\Users\username\.velox\config.yaml`)
3. **System Directory (Linux)**: `/etc/velox/config.yaml`

---

## 2. Complete Annotated `config.yaml`

```yaml
# ==============================================================================
# Velox Configuration File
# https://github.com/AmirAM03/velox
# ==============================================================================

# Data directory for SQLite database storage (default: ~/.velox or /var/lib/velox)
data_dir: ""

# Log verbosity level: debug, info, warn, error
log_level: "info"

# ------------------------------------------------------------------------------
# Multi-Stage Testing Pipeline Settings
# ------------------------------------------------------------------------------
pipeline:
  # Stage 0: DNS Resolution & TCP Ping
  stage0_workers: 2000          # Max concurrent workers for TCP dial
  stage0_timeout: "1.5s"        # TCP connect timeout

  # Stage 1: TLS / REALITY Handshake
  stage1_workers: 500           # Max concurrent workers for TLS handshake
  stage1_timeout: "2.5s"        # TLS handshake timeout

  # Stage 2: Functional In-Process Proxy Test (Real Delay)
  stage2_workers: 150           # Max concurrent workers for proxy HTTP requests
  stage2_timeout: "5.0s"        # HTTP request timeout through the proxy

  # Adaptive concurrency throttling based on network error rates
  adaptive_concurrency: true
  timeout_rate_high: 0.15       # Reduce workers if >15% requests timeout
  timeout_rate_low: 0.05        # Increase workers if <5% requests timeout

  # Per-host rate limiting (prevents overwhelming single CDN/edge IPs)
  per_host_rate_limit: 5        # Max concurrent connections per target host IP
  per_host_cooldown: "500ms"    # Cooldown between batches to same host

# ------------------------------------------------------------------------------
# Local Mixed Inbound Proxy Settings
# ------------------------------------------------------------------------------
proxy:
  listen_addr: "127.0.0.1"      # Local bind IP address (use 0.0.0.0 for LAN sharing)
  mixed_port: 1080              # Inbound port handling SOCKS5 and HTTP simultaneously
  warm_pool_size: 5             # Number of top scored configs kept warm in memory
  health_interval: "30s"        # Health check frequency on the active proxy node
  fail_threshold: 2             # Consecutive failures before triggering auto-failover

# ------------------------------------------------------------------------------
# Target URLs for Real Delay & Delay Benchmarking
# ------------------------------------------------------------------------------
targets:
  # Destination URLs probed during Stage 2 testing
  urls:
    - "https://www.google.com/generate_204"
    - "https://cp.cloudflare.com/generate_204"
    - "https://www.gstatic.com/generate_204"

  # Expected HTTP status codes considered successful
  expect_status:
    - 200
    - 204

  # Optional payload URL used for throughput benchmarking
  speed_test_url: "https://speed.cloudflare.com/__down?bytes=10485760"

  # Hostname used for proxy DNS leak / resolution testing
  dns_test_host: "google.com"

# ------------------------------------------------------------------------------
# Smart Composite Scoring Weights (Weights must sum to 1.0)
# ------------------------------------------------------------------------------
scoring:
  weight_latency: 0.30          # Weight assigned to P95 latency
  weight_success: 0.30          # Weight assigned to test success rate
  weight_throughput: 0.15       # Weight assigned to throughput
  weight_stability: 0.15        # Weight assigned to connection stability
  weight_recency: 0.10          # Weight assigned to test recency
  stability_decay: 0.80         # Exponential decay factor for stability penalty

# ------------------------------------------------------------------------------
# Diagnostic & Profiling Settings
# ------------------------------------------------------------------------------
debug:
  pprof_enabled: false          # Enable Go pprof profiling HTTP server
  pprof_addr: "127.0.0.1:6060"  # Profiling endpoint address
```

---

## 3. Recommended Optimization Profiles

### Profile A: Low-Resource VPS (512MB RAM / 1 vCPU)
When running Velox on a budget cloud instance or low-memory virtual machine:

```yaml
pipeline:
  stage0_workers: 200
  stage1_workers: 50
  stage2_workers: 20
  adaptive_concurrency: true

proxy:
  warm_pool_size: 2
  health_interval: "60s"
```

### Profile B: High-Performance Desktop / Server (Multi-Core / Gigabit)
For scanning large subscription pools (10,000+ nodes) in seconds:

```yaml
pipeline:
  stage0_workers: 4000
  stage0_timeout: "1.0s"
  stage1_workers: 1000
  stage1_timeout: "2.0s"
  stage2_workers: 300
  stage2_timeout: "3.5s"

proxy:
  warm_pool_size: 10
  health_interval: "15s"
```

---

## 4. CLI Overrides

Any configuration file option can be overridden directly on the command line:

```bash
# Override data directory
velox --data-dir /mnt/storage/velox ui

# Override log level
velox --log-level debug test

# Override target URLs on test command
velox test --target "https://www.google.com" --target "https://cloudflare.com"

# Override port on connect command
velox connect --port 2080 --system
```
