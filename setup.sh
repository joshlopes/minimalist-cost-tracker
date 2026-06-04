#!/usr/bin/env bash
#
# setup.sh — build cost-tracker from this checkout and wire it up. This is the
# from-source path; end users without the repo should instead run install.sh:
#
#   curl -fsSL https://raw.githubusercontent.com/joshlopes/minimalist-cost-tracker/main/install.sh | sh
#
# Hook-wiring, port selection, and the login service all live in the binary
# (`install-hooks`, `free-port`, `service`), so this script is just: build,
# migrate, wire, start. Safe to re-run.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
BIN_PATH="$BIN_DIR/cost-tracker"
WANT_SERVICE="${COST_TRACKER_SERVICE:-1}"
PREFERRED_PORT="${COST_TRACKER_PORT:-7842}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33mwarn:\033[0m %s\n' "$1" >&2; }
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
info "Building cost-tracker -> $BIN_PATH"
( cd "$REPO_DIR" && make build >/dev/null )
cp "$REPO_DIR/bin/cost-tracker" "$BIN_PATH"

# --- 3. migrate + wire hooks (logic lives in the binary) ------------------
info "Migrating database"
"$BIN_PATH" migrate >/dev/null
info "Wiring Claude Code hooks"
"$BIN_PATH" install-hooks

# --- 4. pick a port + start -----------------------------------------------
PORT="$("$BIN_PATH" free-port --start "$PREFERRED_PORT")"
[ "$PORT" = "$PREFERRED_PORT" ] || warn "port $PREFERRED_PORT busy; using $PORT"

if [ "$WANT_SERVICE" = "1" ] && "$BIN_PATH" service install --port "$PORT" >/dev/null 2>&1; then
  info "Installed login service"
else
  [ "$WANT_SERVICE" = "1" ] && warn "could not install login service; start manually with: cost-tracker serve"
fi

info "Done."
echo
echo "  Hooks successfully installed. Start/Open the dashboard on http://localhost:$PORT"
echo "  (Hooks take effect on your next Claude Code session.)"
if ! printf '%s' ":$PATH:" | grep -q ":$BIN_DIR:"; then
  echo
  echo "  NOTE: $BIN_DIR is not on your PATH. Add it, e.g.:"
  echo "        export PATH=\"\$HOME/.local/bin:\$PATH\""
fi
