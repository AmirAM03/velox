#!/usr/bin/env bash
# Velox Deep Clean Uninstaller for Linux (Ubuntu / Debian / RHEL / Arch)
# https://github.com/AmirAM03/velox

set -e

INSTALL_BIN="/usr/local/bin/velox"
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
    error "This script must be run as root. Please run with sudo: curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash"
fi

PURGE_USER_DATA=false
for arg in "$@"; do
    case "$arg" in
        --purge|-p|--all)
            PURGE_USER_DATA=true
            ;;
    esac
done

echo -e "\n${YELLOW}════════════════════════════════════════════════════════════${NC}"
echo -e "${YELLOW}               Velox Deep Clean Uninstaller               ${NC}"
echo -e "${YELLOW}════════════════════════════════════════════════════════════${NC}\n"

# 1. Stop and disable systemd service
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet velox.service 2>/dev/null; then
        info "Stopping running velox.service..."
        systemctl stop velox.service || true
    fi

    if systemctl is-enabled --quiet velox.service 2>/dev/null; then
        info "Disabling velox.service..."
        systemctl disable velox.service || true
    fi

    if [ -f "$SERVICE_FILE" ]; then
        info "Removing systemd service unit: $SERVICE_FILE"
        rm -f "$SERVICE_FILE"
        systemctl daemon-reload || true
        systemctl reset-failed || true
        success "Removed systemd service"
    fi
fi

# 2. Reset any OS system proxy that might have been configured
info "Checking and resetting system proxy settings..."
if command -v gsettings >/dev/null 2>&1; then
    gsettings set org.gnome.system.proxy mode 'none' 2>/dev/null || true
fi

for kwrite in kwriteconfig6 kwriteconfig5; do
    if command -v "$kwrite" >/dev/null 2>&1; then
        "$kwrite" --file kioslaurc --group "Proxy Settings" --key ProxyType 0 2>/dev/null || true
    fi
done

# Remove APT proxy configuration if added
if [ -f "/etc/apt/apt.conf.d/99proxy" ]; then
    if grep -qi "1080" "/etc/apt/apt.conf.d/99proxy" 2>/dev/null; then
        info "Removing APT proxy configuration: /etc/apt/apt.conf.d/99proxy"
        rm -f "/etc/apt/apt.conf.d/99proxy"
    fi
fi

# 3. Remove binary
if [ -f "$INSTALL_BIN" ]; then
    info "Removing executable: $INSTALL_BIN"
    rm -f "$INSTALL_BIN"
    success "Removed binary"
else
    warn "Executable $INSTALL_BIN was not found"
fi

# 4. Remove system configuration directory
if [ -d "$CONFIG_DIR" ]; then
    info "Removing configuration directory: $CONFIG_DIR"
    rm -rf "$CONFIG_DIR"
    success "Removed $CONFIG_DIR"
fi

# 5. Remove system data directory (SQLite database and state)
if [ -d "$DATA_DIR" ]; then
    info "Removing system data directory: $DATA_DIR"
    rm -rf "$DATA_DIR"
    success "Removed $DATA_DIR"
fi

# 6. Deep clean user directories if requested or present in home
if [ "$PURGE_USER_DATA" = true ]; then
    info "Purging user-level directories..."

    # Current user / sudo user home
    TARGET_HOME="${HOME}"
    if [ -n "${SUDO_USER}" ] && [ "${SUDO_USER}" != "root" ]; then
        TARGET_HOME=$(getent passwd "${SUDO_USER}" | cut -d: -f6)
    fi

    if [ -n "$TARGET_HOME" ] && [ -d "$TARGET_HOME" ]; then
        rm -rf "$TARGET_HOME/.velox"
        rm -rf "$TARGET_HOME/.config/velox"
        info "Removed $TARGET_HOME/.velox and $TARGET_HOME/.config/velox"
    fi

    # Also clean /root/.velox if running as root
    rm -rf /root/.velox /root/.config/velox 2>/dev/null || true
    success "Purged all user configs and databases"
fi

echo -e "\n${GREEN}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}       Velox has been completely uninstalled!       ${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════════════${NC}\n"

if [ "$PURGE_USER_DATA" = false ]; then
    echo -e "Note: User configuration directories (~/.velox) were preserved."
    echo -e "To also purge user-level data, run:"
    echo -e "  ${CYAN}curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash -s -- --purge${NC}\n"
fi
