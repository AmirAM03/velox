# agy-extract — Antigravity credentials for Velox

Velox's **Antigravity** profile asks Google, through each candidate node, whether that
node's IP may use the Antigravity (agy) API. To do that it needs a real agy session —
two secrets that exist only on a machine where the agy app is installed and signed in:

| Secret | What it is | Where it lives |
|---|---|---|
| **client secret** (`GOCSPX-…`) | agy's OAuth client secret | embedded inside the `agy` binary |
| **refresh token** | your signed-in account's session | agy's oauth token file on disk |

This tool pulls both out and hands them to you (or loads them straight into a running
Velox), so you never have to dig them out by hand on each new deployment.

## Usage

```bash
# Print the two values to paste into the profile (client-secret field + Sign-in/session field)
python -I agy_extract.py

# Machine-readable
python -I agy_extract.py --json

# Load straight into a running Velox profile (no printing); profile id defaults to 2
python -I agy_extract.py --into-velox http://127.0.0.1:18080 --profile 2
```

Run it on a machine where agy is installed **and you have signed in to agy at least once**.
Use `python -I` so nothing is imported from the current directory.

### Options

- `--agy PATH` — the agy binary. Default autodetect: `%LOCALAPPDATA%\agy\bin\agy.exe`
  (Windows), `~/.local/share/agy/bin/agy` (Linux).
- `--token PATH` — agy's oauth token file. Default autodetect:
  `~/.gemini/antigravity-cli/antigravity-oauth-token`, then `~/.gprobe/token.json`.
  **agy sometimes moves this file between versions** — if autodetect fails, pass the file
  that contains your `refresh_token` here.
- `--no-verify` — skip testing secrets against Google and print the first candidate.
  Only safe when the binary yields exactly one candidate.
- `--into-velox URL` / `--profile ID` — POST the creds into a running Velox and enable the
  profile, instead of printing them.

## How it works

1. **client secret** — scans the agy binary for `GOCSPX-` strings. Google client secrets
   are **exactly `GOCSPX-` + 28 chars**; a looser pattern over-captures trailing bytes and
   every candidate then fails with `401 invalid_client`. The binary usually holds a few
   `GOCSPX-` strings; only one pairs with agy's client id.
2. **refresh token** — reads it from agy's oauth token file (`{"token":{"refresh_token":…}}`,
   a bare `{"refresh_token":…}`, or the raw token).
3. **verify** — mints an access token at `https://oauth2.googleapis.com/token` with each
   candidate secret + your refresh token; the one that returns `200` is the right secret.
   If Google is blocked from where you run this, set `HTTPS_PROXY` to a working node, or use
   `--no-verify` when there is a single candidate.

## Security

These are account credentials — treat the refresh token like a password. The tool prints
them only because that is its purpose; prefer `--into-velox` on shared machines so nothing
lands in your shell history. Never commit the extracted values or a Velox data directory.
