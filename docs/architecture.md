# Velox Architecture & Internals

This document details the internal architecture, design principles, and algorithms powering **Velox**.

---

## 1. Architectural Philosophy

Velox was engineered to address the critical bottlenecks present in legacy proxy clients:
1. **Single Self-Contained Service**: Velox runs as one systemd service on a VPS; the Xray engine, scheduler, proxy listeners and dashboard all live in one process.
2. **In-Process Core Execution**: By embedding Xray-core directly via Go API (`core.New`, `core.Dial`), Velox eliminates OS process-spawning overhead, inter-process communication (IPC) latency, and socket descriptor exhaustion.
3. **Cheap Filter, Then the Real Check**: Testing thousands of configurations is expensive, and the real check may use an account. Velox drops dead servers with a TCP connect, keeps only nodes that reach the service with a probe that uses no account, and runs the real check on the fastest few.
4. **Cryptographic Deduplication**: Subscriptions and public pools often duplicate up to 70% of configurations under different labels. Velox identifies duplicate nodes by cryptographic hash, ensuring a clean SQLite database.
5. **Zero-CGO Pure-Go Storage**: Embedded SQLite WAL engine (`modernc.org/sqlite`) enables high-concurrency read/write operations without requiring a C compiler or external dynamic libraries.

---

## 2. System Architecture Diagram

```
 ┌───────────────────────────── Velox (one process) ─────────────────────────────┐
 │                                                                                │
 │  Sources (subscription URLs)                                                   │
 │        │  every refresh interval (default 6h)                                  │
 │        ▼                                                                       │
 │  Fetch ──► Parse (VLESS/VMess/Trojan/SS/Hy2) ──► SHA-256 dedup ──► SQLite WAL  │
 │                                                     (stale configs pruned)     │
 │        │  every cycle (default 30m)                                            │
 │        ▼                                                                       │
 │  Optimizer (throwaway test engine, per profile)                                │
 │   1. TCP connect to every config (shared by the profiles in a run)             │
 │   2. filter: the profile type's probe through each node (no account)           │
 │   3. check: type's real check once per node; label it; passing = candidates    │
 │   4. best so far goes live at once; final top 10 ──► profile_nodes             │
 │        │                                                                       │
 │        ▼                                                                       │
 │  Profile listeners (one per profile, 0.0.0.0:port, SOCKS5 + HTTP, user/pass)   │
 │        │  active node = rank 1; failover to rank 2.. after repeated dial errors │
 │        ▼                                                                       │
 │  Shared serving Xray engine (one instance per node in use) ──► V2Ray node      │
 │                                                                                │
 │  Dashboard + JSON API on 127.0.0.1:18080 (reached through an SSH tunnel)       │
 └────────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Per-Profile Benchmark

Profiles are **built-in profile types**, one profile per type, created at startup. A type is code (`internal/checks`): it knows how to test a node for its service, and which settings it needs, such as a login session. Profiles cannot be added from the dashboard and there are no free-form target URLs; you edit a profile's port, user, password, Auto re-test and its type's settings (secret settings are never sent back to the browser). An enabled profile needs settings its type accepts.

| Type | Checks | Needs |
|---|---|---|
| **Antigravity** | Asks Google, with your agy (Antigravity CLI) session, through each node: which country Google places the node in, and whether that country may use the Antigravity API. Google uses its own geo-IP data, which often differs from public IP-location sites, so this is the only reliable test. | **Sign in with Google** (or paste agy's token file) and agy's OAuth client secret (`GOCSPX-…`, embedded in the agy binary). |

**Signing in (Antigravity)**: in the Profiles tab, **Edit** the profile, paste agy's client secret and press **Sign in with Google**. The browser goes to Google's sign-in page and comes back to the dashboard (through your SSH tunnel, whatever local port it uses); Velox then stores a refresh token for that account and shows "Signed in as …". This is the standard desktop OAuth flow (loopback redirect + PKCE) with agy's public desktop client and the scopes agy's own token carries, so agy itself is not needed. The code exchange goes direct from the server, or through the profile's nodes if the server cannot reach Google. **Sign out** forgets the stored session (it does not revoke it at Google; do that in your Google account's third-party access page). Pasting an agy token file still works as an alternative.

**Where the credentials come from** (none are stored in this repo; read them on the machine that has agy installed, and treat them like passwords):

- **OAuth client ID** (public, not a secret): `1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com` — the same id agy records as `AGY_CLIENT_ID`. It lives in the code (`internal/checks/antigravity.go`).
- **Client secret** (`GOCSPX-…`): embedded in the agy binary (`%LOCALAPPDATA%\agy\bin\agy.exe` on Windows, the installed `agy` binary elsewhere). The binary embeds **three** `GOCSPX-` strings; exactly one pairs with the client id above, the other two return `401 invalid_client`. Each is `GOCSPX-` followed by **exactly 28** characters — pin that length when you grep, because the surrounding bytes are also `[A-Za-z0-9_-]` and a greedy match over-reads and fails. Identify the right one by sending a `grant_type=refresh_token` request (below) with each: the one that returns `200` is it.
- **Refresh token** (the actual account credential): in agy's token file `~/.gemini/antigravity-cli/antigravity-oauth-token`, field `token.refresh_token`. Anyone holding it plus the public client id/secret can act as that Google account.
- **Access token** (short-lived, ~1 h): never stored on disk. Velox derives it on demand from the client secret + refresh token against `https://oauth2.googleapis.com/token` and keeps it in memory only, refreshing through a node about once an hour.

So a full setup needs two values pasted into the profile: the **client secret** and, as the session, either a **Sign in with Google** or the pasted token file. Velox stores both in `velox.db` and never shows them again.

To pull both out of an installed, signed-in agy automatically (and optionally load them straight into a running Velox), use `tools/agy-extract/` — it encodes the `GOCSPX-`+28 length rule and the "which secret pairs with the client id" test described above. See its README.

One flow runs every type. It runs on the schedule (profiles with **Auto** on), after new configs arrive, and whenever **Test now** is pressed:

1. **Reachability** (shared by all profiles in a run): a TCP connect to every stored config, 512 at a time, 2 s each.
2. **Filter**: the type's cheap probe through each reachable node, 128 at a time, 6 s each, at most 4 minutes. A probe never uses an account. Antigravity: any answer from Google's API over a verified TLS connection.
3. **Check**: the type's real check, **once per node**, fastest probe first, 6 nodes at a time. Each checked config is **labeled** and, if it passes, becomes a **candidate**; the response time of the check is recorded. Antigravity: with your Google session, Google's eligibility answer (`aicode fetchConfig`): 200 = eligible, labeled with the country Google places the node in (`fetchUserInfo`); 403 `LOCATION_NOT_ELIGIBLE` = rejected, labeled with Google's detected country. The OAuth access token is refreshed through a node about once an hour and kept in memory only. A run stops after 30 candidates or 150 checked nodes (or 6 minutes).
4. **Rank and serve**: candidates are ranked by the response time recorded in step 3. Every new candidate that beats the others serves the port at once, with the previous candidates behind it as fallbacks; after the run the best 10 serve. All checked configs, labeled, are saved and listed on the card under **Checked configs** (candidates, or everything with the reason a config was rejected); each row shows the country Google detected, whether it is eligible, and the raw JSON response Google returned.

If nothing passes, or the session is refused, the profile keeps its previous nodes and the Profiles tab shows why, e.g. `rejected HK 20, IR 3` or `session error: …`.

The limits are constants in `internal/bench/bench.go`.

**Account note**: the real check sends your session through up to 150 different IPs per run. The caps keep this bounded, but Google may still treat it as unusual account activity; a separate Google account is the safer choice.

**Adding a service** = one new `checks.Kind` (fields + a `bench.Check` with `Probe` and `Round`) in `internal/checks`, added to `checks.Kinds`.

---

## 4. In-Process Core vs. Daemon Architecture

| Feature | Legacy Daemon / Subprocess (v2rayN, Clash) | Velox In-Process Architecture |
| :--- | :--- | :--- |
| **Execution Model** | Spawns separate `xray.exe` or `sing-box` background processes | Direct in-memory invocation via `core.New` & `core.Dial` |
| **System Overhead** | Process table bloat, IPC latency, zombie process risk | Single lightweight OS process, shared memory |
| **File Descriptors** | Heavy socket churn across local loopback connections | Direct memory pipe without loopback proxy hop |
| **Service Model**| One external core process per running config | One `velox` systemd service; all nodes run inside it |
| **Ports** | Separate management and proxy ports per core | One port per profile, all served by the shared engine |

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

## 6. Storage Engine (SQLite WAL)

Velox uses embedded SQLite compiled purely in Go via `modernc.org/sqlite`:
- **WAL Mode (Write-Ahead Logging)**: Enabled via `PRAGMA journal_mode=WAL;` to allow simultaneous non-blocking concurrent readers during active background writes.
- **Synchronous Normal**: `PRAGMA synchronous=NORMAL;` maximizes disk I/O throughput while guaranteeing database integrity.
- **Indexes**: `idx_configs_protocol` (protocol filter) and `idx_configs_address`.
- **Tables**: `configs` (deduplicated nodes), `sources`, `profiles`, `profile_nodes` (each profile's saved ranking), `settings`, `app_logs`.
