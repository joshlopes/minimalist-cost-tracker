#!/usr/bin/env bash
#
# setup.sh — build the cost-tracker binary, migrate its database, and wire the
# Claude Code hooks. Safe to re-run: builds are overwritten, the migration is
# idempotent, and hook entries are de-duplicated by command string.
#
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/.local/bin"
BIN_PATH="$BIN_DIR/cost-tracker"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/cost-tracker"
SETTINGS="${CLAUDE_CONFIG_DIR:-$HOME/.claude-work}/settings.json"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
fail() { printf '\033[1;31merror:\033[0m %s\n' "$1" >&2; exit 1; }

# --- 1. check Go >= 1.22 --------------------------------------------------
command -v go >/dev/null 2>&1 || fail "Go is not installed (need 1.22+). See https://go.dev/dl/"
GO_VER="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
GO_MAJOR="${GO_VER%%.*}"
GO_REST="${GO_VER#*.}"
GO_MINOR="${GO_REST%%.*}"
if [ "${GO_MAJOR:-0}" -lt 1 ] || { [ "${GO_MAJOR:-0}" -eq 1 ] && [ "${GO_MINOR:-0}" -lt 22 ]; }; then
  fail "Go $GO_VER found, but 1.22+ is required."
fi
info "Go $GO_VER OK"

# --- 2. build the binary --------------------------------------------------
mkdir -p "$BIN_DIR"
VERSION="$(git -C "$REPO_DIR" describe --tags --always 2>/dev/null || echo dev)"
info "Building cost-tracker $VERSION -> $BIN_PATH"
( cd "$REPO_DIR" && go build -ldflags "-X main.version=$VERSION" -o "$BIN_PATH" ./cmd/cost-tracker )

# --- 3. data dir + migrate ------------------------------------------------
mkdir -p "$DATA_DIR"
info "Migrating database in $DATA_DIR"
"$BIN_PATH" migrate

# --- 4. patch Claude Code settings.json (idempotent) ----------------------
command -v python3 >/dev/null 2>&1 || fail "python3 is required to patch $SETTINGS"
mkdir -p "$(dirname "$SETTINGS")"
[ -f "$SETTINGS" ] || echo '{}' > "$SETTINGS"
info "Wiring hooks into $SETTINGS"
python3 - "$SETTINGS" "$BIN_PATH" <<'PY'
import json, os, sys, tempfile

settings_path, bin_path = sys.argv[1], sys.argv[2]
cmd = f"{bin_path} hook"

with open(settings_path) as f:
    try:
        data = json.load(f)
    except json.JSONDecodeError as e:
        sys.exit(f"{settings_path} is not valid JSON ({e}); refusing to overwrite. Fix it and re-run.")

if not isinstance(data, dict):
    sys.exit(f"{settings_path} top-level value is not an object; refusing to overwrite.")

hooks = data.setdefault("hooks", {})

def already_present(event):
    for group in hooks.get(event, []):
        for h in group.get("hooks", []):
            if isinstance(h, dict) and h.get("command") == cmd:
                return True
    return False

def add_hook(event, matcher=None):
    arr = hooks.setdefault(event, [])
    if already_present(event):
        return False
    group = {"hooks": [{"type": "command", "command": cmd}]}
    if matcher is not None:
        group["matcher"] = matcher
    arr.append(group)
    return True

added = []
if add_hook("PostToolUse", ".*"):
    added.append("PostToolUse")
if add_hook("Stop"):
    added.append("Stop")

# atomic write
d = os.path.dirname(os.path.abspath(settings_path)) or "."
fd, tmp = tempfile.mkstemp(dir=d, prefix=".cost-tracker-", suffix=".json")
try:
    with os.fdopen(fd, "w") as f:
        json.dump(data, f, indent=2)
        f.write("\n")
    os.replace(tmp, settings_path)
except Exception:
    os.unlink(tmp)
    raise

print("added hooks: " + (", ".join(added) if added else "none (already present)"))
PY

info "Done."
echo
echo "  Start the dashboard:  cost-tracker serve"
echo "  Then open:            http://localhost:7842"
echo
echo "  (Hooks take effect on your next Claude Code session.)"
if ! printf '%s' ":$PATH:" | grep -q ":$BIN_DIR:"; then
  echo
  echo "  NOTE: $BIN_DIR is not on your PATH. Add it, e.g.:"
  echo "        export PATH=\"\$HOME/.local/bin:\$PATH\""
fi
