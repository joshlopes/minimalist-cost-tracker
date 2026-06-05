#!/bin/sh
#
# install.sh — one-shot installer for the Claude Code cost-tracker.
#
#   curl -fsSL https://raw.githubusercontent.com/joshlopes/minimalist-cost-tracker/main/install.sh | sh
#
# It downloads the right prebuilt binary from GitHub Releases (no Go toolchain,
# no clone), verifies its checksum, migrates the database, wires the Claude
# Code hooks, picks a free port, optionally installs a start-on-login service,
# and starts the dashboard.
#
# Tunable via environment (all optional):
#   REPO=owner/name          release source (default joshlopes/minimalist-cost-tracker)
#   VERSION=v1.2.3           install a specific tag instead of latest
#   COST_TRACKER_PORT=7842   preferred dashboard port (a free one is chosen if busy)
#   COST_TRACKER_SERVICE=1   install a login service (default: 1, set 0 to skip)
#   BIN_DIR=$HOME/.local/bin install location
#
# POSIX sh; safe to re-run.
set -eu

REPO="${REPO:-joshlopes/minimalist-cost-tracker}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
BIN_PATH="$BIN_DIR/cost-tracker"
WANT_SERVICE="${COST_TRACKER_SERVICE:-1}"
PREFERRED_PORT="${COST_TRACKER_PORT:-7842}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33mwarn:\033[0m %s\n' "$1" >&2; }
fail() { printf '\033[1;31merror:\033[0m %s\n' "$1" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"; }

# --- 0. tools -------------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  DL="curl -fsSL"
  DLO="curl -fsSL -o"
elif command -v wget >/dev/null 2>&1; then
  DL="wget -qO-"
  DLO="wget -qO"
else
  fail "need curl or wget"
fi
need tar
need uname

# --- 1. detect platform ---------------------------------------------------
os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Darwin) GOOS=darwin ;;
  Linux)  GOOS=linux ;;
  *) fail "unsupported OS: $os (build from source: https://github.com/$REPO)" ;;
esac
case "$arch" in
  x86_64|amd64) GOARCH=amd64 ;;
  arm64|aarch64) GOARCH=arm64 ;;
  *) fail "unsupported architecture: $arch" ;;
esac
ASSET="cost-tracker_${GOOS}_${GOARCH}.tar.gz"
info "Platform: ${GOOS}/${GOARCH}"

# --- 2. resolve version ---------------------------------------------------
if [ -n "${VERSION:-}" ]; then
  TAG="$VERSION"
else
  info "Resolving latest release of $REPO"
  TAG="$($DL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -n1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
  [ -n "$TAG" ] || fail "could not determine latest release (set VERSION=vX.Y.Z, or check that $REPO has a published release)"
fi
info "Version: $TAG"

BASE="https://github.com/$REPO/releases/download/$TAG"

# --- 3. download + verify -------------------------------------------------
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

info "Downloading $ASSET"
$DLO "$TMP/$ASSET" "$BASE/$ASSET" || fail "download failed: $BASE/$ASSET"

if $DLO "$TMP/SHA256SUMS" "$BASE/SHA256SUMS" 2>/dev/null; then
  expected="$(grep " ${ASSET}\$" "$TMP/SHA256SUMS" | awk '{print $1}' | head -n1)"
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
      actual="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
    else
      actual=""
      warn "no sha256 tool; skipping checksum verification"
    fi
    if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
      fail "checksum mismatch for $ASSET (expected $expected, got $actual)"
    fi
    [ -n "$actual" ] && info "Checksum OK"
  fi
else
  warn "SHA256SUMS not found for $TAG; skipping checksum verification"
fi

# --- 4. install binary ----------------------------------------------------
mkdir -p "$BIN_DIR"
tar -xzf "$TMP/$ASSET" -C "$TMP"
[ -f "$TMP/cost-tracker" ] || fail "archive did not contain a cost-tracker binary"
chmod +x "$TMP/cost-tracker"
mv "$TMP/cost-tracker" "$BIN_PATH"
info "Installed $BIN_PATH ($("$BIN_PATH" version))"

# --- 5. migrate database --------------------------------------------------
info "Migrating database"
"$BIN_PATH" migrate >/dev/null

# --- 6. wire Claude Code hooks --------------------------------------------
info "Wiring Claude Code hooks"
"$BIN_PATH" install-hooks

# --- 7. choose a free port ------------------------------------------------
# A previous run's service is still holding its port. Stop it first so a
# re-install reclaims the same preferred port instead of drifting to a new one
# (which would leave the old dashboard running alongside the new one).
if "$BIN_PATH" service status >/dev/null 2>&1; then
  info "Stopping previous dashboard service"
  "$BIN_PATH" service uninstall >/dev/null 2>&1 || true
fi

PORT="$("$BIN_PATH" free-port --start "$PREFERRED_PORT")"
if [ "$PORT" != "$PREFERRED_PORT" ]; then
  warn "port $PREFERRED_PORT was busy; using $PORT instead"
fi

# --- 8. start the dashboard -----------------------------------------------
RUNNING=0
if [ "$WANT_SERVICE" = "1" ]; then
  if "$BIN_PATH" service install --port "$PORT" >/dev/null 2>&1; then
    info "Installed login service (auto-starts on boot)"
    RUNNING=1
  else
    warn "could not install a login service; starting in the background instead"
  fi
fi

if [ "$RUNNING" != "1" ]; then
  # No service (or it failed): start a detached background dashboard.
  nohup "$BIN_PATH" serve --port "$PORT" >/dev/null 2>&1 &
  RUNNING=1
fi

# Give it a moment to bind, then confirm it answers.
URL="http://localhost:$PORT"
i=0
while [ "$i" -lt 20 ]; do
  if $DL "$URL/api/stats" >/dev/null 2>&1; then
    break
  fi
  i=$((i + 1))
  sleep 0.25
done

# --- 9. PATH note + final message -----------------------------------------
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) warn "$BIN_DIR is not on your PATH — add: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

echo
info "Hooks successfully installed. Your dashboard is running now on $URL"
echo "  (Hooks take effect on your next Claude Code session.)"
echo "  Update later with:  cost-tracker update"
