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

## 2. Basic Workflow

### Step 1: Ingest Proxy Configurations
Ingest configs from subscription URLs, local files, or standard input:

```bash
# Ingest from subscription URLs
velox parse --sub "https://your-provider.com/sub/token"

# Ingest multiple subscriptions
velox parse \
  --sub "https://sub1.com/link" \
  --sub "https://sub2.com/link"

# Ingest from file or stdin
velox parse --file configs.txt
cat configs.txt | velox parse
```

*Velox automatically cleans `#` and `//` comments, normalizes CRLF line endings, and deduplicates identical proxies by cryptographic hash.*

### Step 2: Test & Rank Configs
Run the multi-stage testing pipeline against user-defined target URLs:

```bash
# Test against default target (Google 204)
velox test

# Test against custom target addresses
velox test --target "https://www.google.com" --target "https://cloudflare.com"

# Filter by protocol or limit count
velox test --protocol vless --limit 100
```

### Step 3: Inspect Ranked Nodes
```bash
# View top scored configs
velox list

# Show raw connection URIs
velox list --show-uri
```

---

## 3. Running Velox

### Option A: Interactive Web Dashboard (Recommended)
Launch the built-in, zero-dependency modern Web Dashboard in your browser:

```bash
velox ui
# or:
velox dashboard
# or with flags:
velox --ui --port 18080
```

The Web Dashboard lets you:
- Explore and search all parsed configurations with real-time protocol badges and latency tags.
- Ingest subscription URLs or raw configuration blocks with instant deduplication.
- Trigger multi-stage speed benchmarks and latency tests against custom endpoints.
- Connect or disconnect the in-process proxy engine with a single click.
- Toggle system proxy routing directly from the interface.

### Option B: Run Proxy in CLI Foreground
Velox provides a **mixed inbound port** (`127.0.0.1:1080` by default) supporting both **SOCKS5** and **HTTP / HTTPS CONNECT** protocols simultaneously on the same port:

```bash
# Start proxy with auto-failover in foreground
velox connect

# Start proxy and automatically configure Ubuntu desktop system proxy (GNOME)
velox connect --system

# Specify custom port
velox connect --port 2080
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

