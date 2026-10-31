#!/usr/bin/env bash
# Velox Installation Script for Linux (Ubuntu / Debian / RHEL / Arch)
# https://github.com/AmirAM03/velox

set -e

REPO="AmirAM03/velox"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/velox"
DATA_DIR="/var/lib/velox"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info() {
    echo -e "${CYAN}[INFO]${NC} $1"
}

success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
    exit 1
}

# Require root/sudo
if [ "$(id -u)" -ne 0 ]; then
    error "This script must be run as root. Please run with sudo: curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sudo bash"
fi

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)
        TARGET_ARCH="amd64"
        ;;
    aarch64|arm64)
        TARGET_ARCH="arm64"
        ;;
    *)
        error "Unsupported architecture: $ARCH. Velox currently supports x86_64 and arm64."
        ;;
esac

info "Detected architecture: $TARGET_ARCH"

# Optional: --binary PATH installs a locally built binary instead of a GitHub release.
LOCAL_BINARY=""
DASHBOARD_PORT=18080
while [ $# -gt 0 ]; do
    case "$1" in
        --binary) LOCAL_BINARY="$2"; shift 2 ;;
        --port) DASHBOARD_PORT="$2"; shift 2 ;;
        *) error "Unknown option: $1 (supported: --binary PATH, --port N)" ;;
    esac
done

# Temporary directory for download
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

if [ -n "$LOCAL_BINARY" ]; then
    [ -f "$LOCAL_BINARY" ] || error "Binary not found: $LOCAL_BINARY"
    LATEST_TAG="local build"
    cp "$LOCAL_BINARY" "$TMP_DIR/velox"
else
    info "Fetching latest release version from GitHub..."
    LATEST_TAG=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    [ -n "$LATEST_TAG" ] || error "Could not resolve the latest release from GitHub. Use --binary PATH to install a local build."

    info "Installing Velox $LATEST_TAG ($TARGET_ARCH)..."
    ARCHIVE_NAME="velox-${LATEST_TAG}-linux-${TARGET_ARCH}.tar.gz"
    DOWNLOAD_URL="https://github.com/$REPO/releases/download/${LATEST_TAG}/${ARCHIVE_NAME}"

    info "Downloading from $DOWNLOAD_URL ..."
    curl -fsSL -o "$TMP_DIR/$ARCHIVE_NAME" "$DOWNLOAD_URL" || error "Failed to download $DOWNLOAD_URL"
    tar -xzf "$TMP_DIR/$ARCHIVE_NAME" -C "$TMP_DIR"
fi

# Install binary
install -m 755 "$TMP_DIR/velox" "$INSTALL_DIR/velox"
success "Installed binary to $INSTALL_DIR/velox"

# Dedicated unprivileged service user owning the data directory
if ! id -u velox >/dev/null 2>&1; then
    useradd --system --no-create-home --home-dir "$DATA_DIR" --shell /usr/sbin/nologin velox
    info "Created system user 'velox'"
fi
mkdir -p "$DATA_DIR"
chown -R velox:velox "$DATA_DIR"
chmod 750 "$DATA_DIR"

# Create config directory and default configuration
mkdir -p "$CONFIG_DIR"
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    if [ -f "$TMP_DIR/config.example.yaml" ]; then
        cp "$TMP_DIR/config.example.yaml" "$CONFIG_DIR/config.yaml"
    else
        curl -fsSL -o "$CONFIG_DIR/config.yaml" "https://raw.githubusercontent.com/$REPO/main/config.example.yaml" || true
    fi
    chmod 644 "$CONFIG_DIR/config.yaml"
    info "Created default configuration at $CONFIG_DIR/config.yaml"
else
    info "Existing configuration at $CONFIG_DIR/config.yaml preserved"
fi

# systemd service: starts on boot, restarts on crash, upgrades in place
SERVICE_FILE="/etc/systemd/system/velox.service"
if command -v systemctl >/dev/null 2>&1; then
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Velox V2Ray connector and dashboard
After=network-online.target
Wants=network-online.target

[Service]
User=velox
Group=velox
ExecStart=$INSTALL_DIR/velox --config $CONFIG_DIR/config.yaml --data-dir $DATA_DIR --port $DASHBOARD_PORT
Restart=on-failure
RestartSec=5
# Benchmarks open thousands of sockets at once.
LimitNOFILE=1048576
# Lets profiles use ports below 1024 without running as root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$DATA_DIR

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable velox.service >/dev/null 2>&1
    systemctl restart velox.service
    success "Velox service installed and started (systemctl status velox)"
else
    warn "systemd not found; start Velox manually: sudo -u velox $INSTALL_DIR/velox --config $CONFIG_DIR/config.yaml --data-dir $DATA_DIR"
fi

echo -e "\n${GREEN}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}       Velox ${LATEST_TAG} installed successfully!${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}\n"

echo -e "Next steps:\n"
echo -e "  1. From your own machine, open an SSH tunnel to the dashboard:"
echo -e "     ${CYAN}ssh -L ${DASHBOARD_PORT}:127.0.0.1:${DASHBOARD_PORT} user@this-server${NC}"
echo -e "     then browse to ${GREEN}http://127.0.0.1:${DASHBOARD_PORT}${NC}\n"
echo -e "  2. Sources tab: add your subscription URLs."
echo -e "  3. Profiles tab: create one profile per target service (port + username + password)."
echo -e "  4. Open each profile port in your firewall, e.g.: ${CYAN}ufw allow 20001/tcp${NC}\n"
echo -e "Logs: ${CYAN}journalctl -u velox -f${NC}\n"
