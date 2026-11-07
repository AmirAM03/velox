# Velox on a Linux VPS

Velox runs as a systemd service on a VPS. Users connect to the VPS on per-service ports; Velox forwards each connection through the best V2Ray node it found for that service.

---

## 1. Install

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/install.sh | sudo bash
```

The installer:
- installs `/usr/local/bin/velox`,
- creates the `velox` system user and `/var/lib/velox` (database),
- writes `/etc/velox/config.yaml` (kept on upgrades),
- installs and starts the `velox` systemd service (restarts on failure, starts on boot).

Options (pass after `bash -s --` when piping):

| Option | Meaning |
| :--- | :--- |
| `--binary PATH` | Install a binary you built yourself instead of the latest GitHub release. |
| `--port N` | Dashboard port on `127.0.0.1` (default `18080`). |

Upgrading is the same command; the service restarts on the new binary and keeps its data.

---

## 2. Open the dashboard

The dashboard listens on `127.0.0.1` only and has no login. From your own machine:

```bash
ssh -L 18080:127.0.0.1:18080 user@your-vps
```

Then browse to `http://127.0.0.1:18080`.

---

## 3. Set it up

1. **Sources** tab: add subscription URLs. Velox fetches them right away and then on the schedule (default every 6 hours), deduplicates every config and drops configs that no subscription has listed for 7 days.
   If the server cannot reach a subscription (blocked or throttled), paste its content (URI lines or the base64 body) into **Paste configs** instead; it goes through the same parsing and deduplication.
2. **Profiles** tab: the built-in profiles are already there, disabled. Press **Edit** on one:
   - **Port**, **Username / Password**: required; every profile port is reachable from the internet.
   - The type's settings. For **Antigravity**: paste agy's OAuth client secret (`GOCSPX-…`), then press **Sign in with Google** and sign in with the Google account to use; you come back to the dashboard signed in. (Alternative: paste the whole `~/.gemini/antigravity-cli/antigravity-oauth-token` file of a logged-in agy as the session.) Secrets are stored in `velox.db` and never shown again; leave a secret empty to keep it.
   - Tick **Enabled** and save.
3. Open each profile port in the firewall:
   ```bash
   sudo ufw allow 20001/tcp
   ```
4. Copy the profile URL (SOCKS5 or HTTP button) and give it to users, replacing `SERVER_IP` with the VPS address:
   ```
   socks5://user:pass@SERVER_IP:20001
   ```

Every test cycle (default 30 minutes) re-ranks the nodes of each profile that has **Auto** re-test on. Turn **Auto** off to pin a profile to its current nodes; its **Test now** button runs the same test for that profile alone whenever you press it. **Re-test all now** tests every enabled profile, and **Fetch all now** fetches subscriptions, then re-tests the Auto profiles. The schedule can be changed in the Profiles tab.

---

## 4. Operate

```bash
systemctl status velox          # service state
journalctl -u velox -f          # live logs
sudo systemctl restart velox    # restart
```

The **Application Logs** tab shows the same logs with search and filters.

Configuration file: `/etc/velox/config.yaml` (see the [Configuration Manual](configuration.md)). Restart the service after editing it.

---

## 5. Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/AmirAM03/velox/main/uninstall.sh | sudo bash
```

This stops and removes the service, binary, `/etc/velox`, `/var/lib/velox` (including the database) and the `velox` user.
