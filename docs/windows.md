# Velox on Windows (x86_64 / ARM64) — User Guide

This guide walks you through installing, running, and configuring **Velox** on Windows 10, Windows 11, and Windows Server (2019/2022).

---

## 1. Quick Installation

### Option A: Download Pre-compiled Binary (Recommended)
1. Go to the [GitHub Releases](https://github.com/AmirAM03/velox/releases) page.
2. Download the latest Windows archive:
   - **64-bit Intel/AMD**: `velox-vX.Y.Z-windows-amd64.zip`
   - **64-bit ARM**: `velox-vX.Y.Z-windows-arm64.zip`
3. Extract `velox.exe` and `config.example.yaml` into your preferred directory (e.g. `C:\Tools\velox` or `C:\Program Files\Velox`).
4. (Optional) Add the folder to your user or system `PATH` so you can type `velox` from any PowerShell or Command Prompt terminal.

### Option B: PowerShell Automated One-Liner
Run PowerShell as Administrator or regular user:

```powershell
# Create folder and download latest Windows release
New-Item -ItemType Directory -Force -Path "$HOME\velox"
$release = (Invoke-RestMethod "https://api.github.com/repos/AmirAM03/velox/releases/latest")
$asset = $release.assets | Where-Object { $_.name -like "*windows-amd64.zip" }
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile "$HOME\velox\velox.zip"
Expand-Archive -Path "$HOME\velox\velox.zip" -DestinationPath "$HOME\velox" -Force
Remove-Item "$HOME\velox\velox.zip"

# Add to user PATH
[Environment]::SetEnvironmentVariable("PATH", $env:PATH + ";$HOME\velox", "User")
```

---

## 2. Launching the Web Dashboard (Recommended)

Velox includes a zero-dependency, dark-mode Web Dashboard embedded directly into `velox.exe`. No web servers, Node.js, or external files are required.

Open PowerShell or Command Prompt and run:

```powershell
velox ui
```
*Aliases: `velox dashboard`, `velox --ui`, or `velox --dashboard`.*

The dashboard automatically starts on `http://127.0.0.1:18080` and opens your default browser (Chrome, Edge, Firefox, Brave):
- **Overview Tab**: Live telemetry, engine status, and top ranked proxy nodes.
- **Configurations Tab**: Interactive searchable table across all protocols (`VLESS`, `VMess`, `Trojan`, `Shadowsocks`, `Hysteria 2`, `WireGuard`).
- **Ingest & Parse Tab**: Paste subscription links or raw configuration blocks with instant cryptographic deduplication.
- **Speed & Latency Benchmark Tab**: Multi-stage speed and delay tests with destination presets (`Google`, `Cloudflare`, `YouTube`, `GitHub`).
- **One-Click Proxy & System Toggle**: Start or stop the in-process proxy engine and switch Windows system proxy settings instantly.

To run on a custom port without auto-opening the browser:
```powershell
velox ui --port 8080 --no-open
```

---

## 3. Command Line Interface (CLI)

For command-line power users, all features are accessible via standalone CLI commands:

### Step 1: Ingest Subscriptions or Files
```powershell
# Ingest from a subscription URL
velox parse --sub "https://your-provider.com/sub/token"

# Ingest from a local text file
velox parse --file my_configs.txt

# Pipe configs via standard input
Get-Content my_configs.txt | velox parse
```

### Step 2: Test & Rank Proxy Nodes
```powershell
# Test against Google 204 endpoint (default)
velox test

# Test against custom target websites
velox test -t "https://www.google.com" -t "https://cp.cloudflare.com/generate_204"

# Test only VLESS configs with a limit of 50
velox test -p vless --limit 50
```

### Step 3: View Top-Ranked Nodes
```powershell
# List top 10 scored nodes
velox list --limit 10

# Show raw proxy connection URIs
velox list --show-uri
```

### Step 4: Connect Through the Proxy
```powershell
# Start local mixed proxy (127.0.0.1:1080)
velox connect

# Start proxy and automatically configure Windows system proxy
velox connect --system

# Specify custom inbound port
velox connect --port 2080
```

---

## 4. Windows System Proxy Integration

When started with `--system` or when toggled in the Web Dashboard, Velox automatically configures the Windows **Internet Settings (WinINet)** registry keys:
- Registry Path: `HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
- `ProxyEnable`: Set to `1`
- `ProxyServer`: Set to `http=127.0.0.1:1080;https=127.0.0.1:1080;socks=127.0.0.1:1080`
- `ProxyOverride`: Set to `<local>;*.local;127.*;10.*;172.16.*;192.168.*`

All major Windows applications (Microsoft Edge, Google Chrome, Brave, Outlook, Teams, Windows Store) immediately route their traffic through Velox.

When Velox exits (via `Ctrl+C`, the Web Dashboard disconnect button, or process termination), the Windows system proxy is cleanly restored to `0`.

---

## 5. Terminal & Development Tool Configuration

To route command-line tools in PowerShell through Velox:

### PowerShell
```powershell
$env:http_proxy  = "http://127.0.0.1:1080"
$env:https_proxy = "http://127.0.0.1:1080"
$env:all_proxy   = "socks5://127.0.0.1:1080"

# Verify connectivity
curl.exe -i https://ipinfo.io
```

### Git
```powershell
git config --global http.proxy http://127.0.0.1:1080
git config --global https.proxy http://127.0.0.1:1080
```

### Package Managers (npm / pip / cargo)
```powershell
# npm
npm config set proxy http://127.0.0.1:1080
npm config set https-proxy http://127.0.0.1:1080

# pip
pip config set global.proxy http://127.0.0.1:1080
```

---

## 6. Data Storage & Profiles

On Windows, Velox automatically stores its pure-Go SQLite WAL database and configuration files in:
```
%USERPROFILE%\.velox\velox.db
%USERPROFILE%\.velox\config.yaml
```

To specify an isolated directory (useful for portable drives or separate profiles):
```powershell
velox --data-dir "D:\VeloxData" ui
```

---

## 7. Clean Removal

Because Velox runs entirely in user-space without any Windows services or drivers:
1. Close any running `velox.exe` instances.
2. Delete the directory where you extracted `velox.exe`.
3. (Optional) Remove the user data folder:
   ```powershell
   Remove-Item -Recurse -Force "$HOME\.velox"
   ```
