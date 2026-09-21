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
6. Configures the `velox.service` systemd unit.

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
# Test against default targets (gstatic 204)
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

## 3. Running the Proxy

Velox provides a **mixed inbound port** (`127.0.0.1:1080` by default) supporting both **SOCKS5** and **HTTP / HTTPS CONNECT** protocols simultaneously on the same port.

### Option A: Run in Foreground (Terminal / Tmux)
```bash
# Start proxy with auto-failover
velox connect

# Start proxy and automatically configure Ubuntu desktop system proxy (GNOME)
velox connect --system

# Specify custom port
velox connect --port 2080
```

### Option B: Run as a Background Systemd Service
On headless servers or dedicated proxy hosts:

```bash
# Enable and start the service
sudo systemctl enable --now velox

# Check service status and active node logs
sudo systemctl status velox

# View live logs
sudo journalctl -u velox -f

# Restart or stop
sudo systemctl restart velox
sudo systemctl stop velox
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

The installed `velox.service` already includes `LimitNOFILE=65535` out-of-the-box.
