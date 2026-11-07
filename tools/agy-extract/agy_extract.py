#!/usr/bin/env python3
"""Extract the Antigravity (agy) Google credentials Velox's Antigravity profile needs:
the OAuth *client secret* embedded in the agy binary, and the account *refresh token*
agy stored after you signed in. Paste both into the profile (client secret field +
Sign in with Google / session field), or feed them straight into a running Velox with
--into-velox.

Why this exists: Google decides Antigravity eligibility per source IP using agy's own
session, so Velox must ask Google through each node with a real agy session. The session
is two secrets that live only on a machine where agy is installed and logged in.

Run with `python -I agy_extract.py` so nothing is imported from the current directory.
Nothing here is printed unless you ask: by default it prints the two values (that is the
point of the tool); pass --json for machine output, or --into-velox to avoid printing.
"""
import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

CLIENT_ID = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
TOKEN_URL = "https://oauth2.googleapis.com/token"
# Google client secrets are exactly "GOCSPX-" + 28 chars. A looser {20,40} quantifier
# greedily grabs trailing bytes from the binary and every candidate then 401s.
SECRET_RE = re.compile(rb"GOCSPX-[A-Za-z0-9_\-]{28}")


def default_agy_paths():
    home = os.path.expanduser("~")
    local = os.environ.get("LOCALAPPDATA", os.path.join(home, "AppData", "Local"))
    return [
        os.path.join(local, "agy", "bin", "agy.exe"),            # Windows
        os.path.join(home, ".local", "share", "agy", "bin", "agy"),  # Linux-ish
        "/Applications/Antigravity.app/Contents/MacOS/agy",       # macOS (best guess)
    ]


def default_token_paths():
    home = os.path.expanduser("~")
    return [
        os.path.join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token"),
        os.path.join(home, ".gprobe", "token.json"),
    ]


def first_existing(paths):
    return next((p for p in paths if p and os.path.isfile(p)), None)


def read_refresh_token(path):
    """agy's token file ({"token":{"refresh_token":...}}), a bare {"refresh_token":...},
    or the raw token on its own line."""
    raw = open(path, "r", encoding="utf-8", errors="replace").read().strip()
    if raw.startswith("{"):
        obj = json.loads(raw)
        return obj.get("token", {}).get("refresh_token") or obj.get("refresh_token")
    return raw or None


def secret_candidates(agy_path):
    blob = open(agy_path, "rb").read()
    seen = []
    for m in SECRET_RE.findall(blob):
        s = m.decode()
        if s not in seen:
            seen.append(s)
    return seen


def post_form(url, data, timeout):
    body = urllib.parse.urlencode(data).encode()
    req = urllib.request.Request(url, data=body,
                                 headers={"Content-Type": "application/x-www-form-urlencoded"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.loads(r.read().decode() or "{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode() or "{}")


def pick_working_secret(cands, refresh, timeout):
    """Return the one secret that mints an access token with this account (HTTP 200).
    Needs to reach Google; set HTTPS_PROXY to route through a working node if direct
    access is blocked."""
    for i, sec in enumerate(cands):
        status, resp = post_form(TOKEN_URL, {
            "client_id": CLIENT_ID, "client_secret": sec,
            "refresh_token": refresh, "grant_type": "refresh_token",
        }, timeout)
        ok = status == 200 and "access_token" in resp
        print(f"  candidate #{i + 1}: HTTP {status} {'<-- works' if ok else resp.get('error', '')}",
              file=sys.stderr)
        if ok:
            return sec
    return None


def into_velox(base, profile_id, secret, refresh, timeout):
    with urllib.request.urlopen(base + "/api/profiles", timeout=timeout) as r:
        profiles = json.loads(r.read().decode())
    p = next((x for x in profiles if x["id"] == profile_id), None)
    if p is None:
        sys.exit(f"no profile id {profile_id} in {base}")
    body = json.dumps({
        "id": profile_id, "port": p["port"], "username": p["username"], "password": p["password"],
        "enabled": True, "auto_test": p.get("auto_test", False),
        "test_interval": p.get("test_interval", "30m"),
        "settings": {"client_secret": secret, "refresh_token": refresh},
    }).encode()
    req = urllib.request.Request(base + "/api/profiles", data=body,
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.status, r.read().decode()


def main():
    ap = argparse.ArgumentParser(description="Extract agy Google credentials for Velox.")
    ap.add_argument("--agy", help="path to the agy binary (default: autodetect)")
    ap.add_argument("--token", help="path to agy's oauth token file (default: autodetect)")
    ap.add_argument("--no-verify", action="store_true",
                    help="skip testing secrets against Google; print all candidates")
    ap.add_argument("--json", action="store_true", help="emit {client_secret, refresh_token} as JSON")
    ap.add_argument("--into-velox", metavar="URL",
                    help="POST the creds into a running Velox (e.g. http://127.0.0.1:18080)")
    ap.add_argument("--profile", type=int, default=2, help="Velox profile id for --into-velox (default 2)")
    ap.add_argument("--timeout", type=float, default=30.0)
    args = ap.parse_args()

    agy = args.agy or first_existing(default_agy_paths())
    if not agy:
        sys.exit("agy binary not found; pass --agy PATH (looked in %LOCALAPPDATA%\\agy\\bin\\agy.exe)")
    token_file = args.token or first_existing(default_token_paths())
    if not token_file:
        sys.exit("agy token file not found; sign in to agy first, or pass --token PATH")

    refresh = read_refresh_token(token_file)
    if not refresh:
        sys.exit(f"no refresh_token in {token_file}")
    cands = secret_candidates(agy)
    if not cands:
        sys.exit(f"no GOCSPX- secret found in {agy}")
    print(f"agy binary : {agy}\ntoken file : {token_file}\ncandidates : {len(cands)}", file=sys.stderr)

    if args.no_verify:
        if len(cands) > 1:
            print("WARNING: multiple candidates and --no-verify; the right one is unknown.", file=sys.stderr)
        secret = cands[0]
    else:
        secret = pick_working_secret(cands, refresh, args.timeout)
        if not secret:
            sys.exit("no candidate secret pairs with this account/client id "
                     "(if Google is blocked here, set HTTPS_PROXY or use --no-verify)")

    if args.into_velox:
        status, resp = into_velox(args.into_velox, args.profile, secret, refresh, args.timeout)
        print(f"velox save: HTTP {status} {resp}", file=sys.stderr)
        print("done: credentials loaded into Velox, profile enabled.", file=sys.stderr)
        return
    if args.json:
        print(json.dumps({"client_secret": secret, "refresh_token": refresh}))
    else:
        print(f"client_secret = {secret}")
        print(f"refresh_token = {refresh}")


if __name__ == "__main__":
    main()
