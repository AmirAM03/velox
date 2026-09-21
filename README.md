# Velox

**High-performance V2Ray proxy config engine.** Parse, test, score, and connect through thousands of proxy configurations at speed.

> *Velox* — Latin for *swift*.

## Features

- 🚀 **High-volume config parsing** — VMess, VLESS, Trojan, Shadowsocks, Hysteria2, TUIC, WireGuard
- 🔬 **Multi-stage testing pipeline** — DNS → TCP → TLS → Proxy → Quality
- 🎯 **Configurable target testing** — Test against your own URLs for real-world performance
- 📊 **Smart scoring** — Weighted composite scores with history and stability tracking
- 🔄 **Automatic failover** — Health checks with warm pool and hot-swap
- ⚡ **Embedded Xray-core** — In-process proxy engine, no subprocess overhead
- 🗄️ **SQLite storage** — Persistent config and score history

## Quick Start

```bash
# Parse configs from a subscription URL
velox parse --sub "https://example.com/sub"

# Test all parsed configs against a target URL
velox test --target "https://www.google.com"

# List configs ranked by score
velox list

# Connect through the best config
velox connect
```

## Building

```bash
make build
```

## License

MPL-2.0 (due to Xray-core dependency)
