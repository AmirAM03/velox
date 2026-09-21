#!/usr/bin/env bash
# Velox Installation Script for Linux (Ubuntu / Debian / RHEL / Arch)
# https://github.com/AmirAM03/velox

set -e

REPO="AmirAM03/velox"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/velox"
DATA_DIR="/var/lib/velox"
SERVICE_FILE="/etc/systemd/system/velox.service"

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

# Detect latest version
info "Fetching latest release version from GitHub..."
LATEST_TAG=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_TAG" ]; then
    warn "Could not resolve latest release tag via GitHub API, falling back to v0.1.0"
    LATEST_TAG="v0.1.0"
fi

info "Installing Velox $LATEST_TAG ($TARGET_ARCH)..."

# Temporary directory for download
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

ARCHIVE_NAME="velox-${LATEST_TAG}-linux-${TARGET_ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/${LATEST_TAG}/${ARCHIVE_NAME}"

info "Downloading from $DOWNLOAD_URL ..."
curl -fsSL -o "$TMP_DIR/$ARCHIVE_NAME" "$DOWNLOAD_URL" || error "Failed to download $DOWNLOAD_URL"

# Extract archive
info "Extracting archive..."
tar -xzf "$TMP_DIR/$ARCHIVE_NAME" -C "$TMP_DIR"

# Install binary
install -m 755 "$TMP_DIR/velox" "$INSTALL_DIR/velox"
success "Installed binary to $INSTALL_DIR/velox"

# Create data directory
mkdir -p "$DATA_DIR"
chmod 755 "$DATA_DIR"

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

# Install systemd service if systemd is active
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    if [ -f "$TMP_DIR/velox.service" ]; then
        cp "$TMP_DIR/velox.service" "$SERVICE_FILE"
    else
        curl -fsSL -o "$SERVICE_FILE" "https://raw.githubusercontent.com/$REPO/main/velox.service" || true
    fi
    chmod 644 "$SERVICE_FILE"
    systemctl daemon-reload
    success "Installed systemd service at $SERVICE_FILE"
fi

echo -e "\n${GREEN}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}       Velox ${LATEST_TAG} installed successfully!${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}\n"

echo -e "Quick Usage Guide:\n"
echo -e "  1. Ingest subscription configs:"
echo -e "     ${CYAN}velox parse --sub \"https://your-subscription-url\"${NC}\n"
echo -e "  2. Test configs against custom targets:"
echo -e "     ${CYAN}velox test --target \"https://www.google.com\"${NC}\n"
echo -e "  3. List top-ranked configs:"
echo -e "     ${CYAN}velox list${NC}\n"
echo -e "  4. Start local proxy in foreground:"
echo -e "     ${CYAN}velox connect --system${NC}\n"
echo -e "  5. Or run continuously as background systemd service:"
echo -e "     ${CYAN}sudo systemctl enable --now velox${NC}"
echo -e "     ${CYAN}sudo systemctl status velox${NC}\n"
echo -e "Proxy listen address: ${GREEN}127.0.0.1:1080${NC} (Mixed SOCKS5 / HTTP)\n"
