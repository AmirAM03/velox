# Velox Configuration Guide

Velox is designed to run out-of-the-box with sensible zero-configuration defaults. The file only holds process-level settings; sources, profiles (built-in types) and the test schedule are managed in the dashboard. The benchmark limits are fixed constants (see the [architecture guide](architecture.md#3-per-profile-benchmark)).

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
# Profile Proxy Listeners (ports, users and sessions are set per profile in the dashboard)
# ------------------------------------------------------------------------------
proxy:
  listen_addr: "0.0.0.0"        # Address every profile port binds to
  fail_threshold: 3             # Consecutive dial failures before failing over to the next-best node

# ------------------------------------------------------------------------------
# Diagnostic & Profiling Settings
# ------------------------------------------------------------------------------
debug:
  pprof_enabled: false          # Enable Go pprof profiling HTTP server
  pprof_addr: "127.0.0.1:6060"  # Profiling endpoint address
```

---

## 3. CLI Overrides

Any configuration file option can be overridden directly on the command line:

```bash
# Override data directory
velox --data-dir /mnt/storage/velox

# Override log level
velox --log-level debug

# Dashboard port (always on 127.0.0.1)
velox --port 18090
```
