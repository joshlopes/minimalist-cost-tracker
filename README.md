# minimalist-cost-tracker

A single Go binary that hooks into Claude Code, records token usage and cost per
session to a local SQLite database, and serves a web dashboard showing cost
breakdowns by session, skill, model, and time.

No runtime dependencies, no remote services, no build step for the frontend —
everything (including the dashboard HTML/JS) is embedded in one binary.

## Install

### curl | sh (recommended)

```sh
curl -fsSL https://raw.githubusercontent.com/joshlopes/minimalist-cost-tracker/main/install.sh | sh
```

This downloads the prebuilt binary for your OS/arch from the latest GitHub
Release (no Go toolchain, no clone), verifies its checksum, then:

1. installs `cost-tracker` into `~/.local/bin/`;
2. migrates the SQLite database;
3. wires the `PostToolUse` and `Stop` hooks into your Claude Code
   `settings.json` (auto-detected: `CLAUDE_CONFIG_DIR`, else `~/.claude`, else
   `~/.claude-work`);
4. picks a free port (starting at `7842`);
5. installs a start-on-login service (launchd on macOS, systemd-user on Linux)
   and starts the dashboard.

It finishes with the URL your dashboard is running on. The installer is
idempotent and configurable via environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `VERSION` | latest | install a specific tag, e.g. `v1.2.3` |
| `COST_TRACKER_PORT` | `7842` | preferred port (a free one is chosen if busy) |
| `COST_TRACKER_SERVICE` | `1` | set `0` to skip the login service |
| `BIN_DIR` | `~/.local/bin` | install location |
| `REPO` | `joshlopes/minimalist-cost-tracker` | release source |

### Homebrew

```sh
brew install joshlopes/minimalist-cost-tracker/cost-tracker
cost-tracker install-hooks
cost-tracker service install
```

### From source

```sh
make build                 # -> ./bin/cost-tracker
./bin/cost-tracker migrate
./bin/cost-tracker install-hooks
```

Requires Go 1.22+. Hooks take effect on your **next** Claude Code session.

## Update

```sh
cost-tracker update        # self-update to the latest GitHub release
```

`update` checks the latest release, downloads the matching binary, verifies its
checksum, and atomically replaces itself in place. Restart the dashboard (or run
`cost-tracker service install` again) to run the new version. Homebrew users
update with `brew upgrade cost-tracker` instead.

## Use

```sh
cost-tracker serve            # dashboard on http://localhost:7842
cost-tracker serve --port 9000
```

The dashboard has four tabs:

- **Overview** — total cost, sessions, tokens, and a daily-cost timeline.
- **Sessions** — sortable list; click a row for its skill/tool breakdown.
- **Skills** — cost attributed per skill (whole-session cost is attributed to
  every skill used in that session — an approximation).
- **Models** — cost share and token totals per model family.

## Commands

```
cost-tracker hook                 read a hook event from stdin and record it
cost-tracker serve [--port N]     start the dashboard (default 7842)
cost-tracker migrate              create/upgrade the database schema
cost-tracker install-hooks        wire the hooks into Claude Code settings.json
cost-tracker service install      install + start the login service (--port N)
cost-tracker service uninstall    stop and remove the login service
cost-tracker service status       show the login-service status
cost-tracker update [--repo R]    self-update to the latest GitHub release
cost-tracker version              print version
```

## Data layout

| Path | Purpose |
|---|---|
| `~/.local/bin/cost-tracker` | the binary |
| `~/.local/share/cost-tracker/tracker.db` | SQLite database (WAL mode) |
| `~/.local/share/cost-tracker/hook.log` | hook debug/error log |
| `~/.local/share/cost-tracker/service.log` | dashboard service stdout/stderr |
| `~/.claude/settings.json` *(or `~/.claude-work`)* | Claude Code hooks live here |
| `~/Library/LaunchAgents/com.cost-tracker.dashboard.plist` | macOS login service |
| `~/.config/systemd/user/cost-tracker.service` | Linux login service |

`XDG_DATA_HOME` and `CLAUDE_CONFIG_DIR` are honoured if set.

## Uninstall

```sh
cost-tracker service uninstall          # stop + remove the login service
# remove the hook entries (the two groups whose command is "<.../cost-tracker> hook"
# under hooks.PostToolUse and hooks.Stop) from your settings.json:
$EDITOR ~/.claude/settings.json
rm -f ~/.local/bin/cost-tracker
rm -rf ~/.local/share/cost-tracker
```

## Development

```sh
make build   # -> ./bin/cost-tracker
make test    # go test ./...
make vet
make fmt
make dist    # cross-compiled release tarballs + SHA256SUMS in ./dist
```

## Releasing

Releases are built by `.github/workflows/release.yml` when a `vX.Y.Z` tag is
pushed:

```sh
git tag v1.2.3
git push origin v1.2.3
```

The workflow cross-compiles `darwin/{amd64,arm64}` and `linux/{amd64,arm64}`
(pure-Go SQLite means `CGO_ENABLED=0`, so one runner does all targets), writes
`SHA256SUMS`, and publishes a GitHub Release. `install.sh` and `cost-tracker
update` consume those assets. The Homebrew formula in `HomebrewFormula/` is
bumped per release (version + the four per-platform `sha256` values).

## Notes / limitations

- Cost is computed from a hardcoded price table in `internal/pricing`; update it
  when Anthropic prices change.
- The transcript JSONL format and the `Skill` tool-input field name are not
  formally documented. The parser is deliberately lenient (unknown fields are
  ignored, malformed lines are skipped) and the hook logs the first `Skill`
  payload to `hook.log` so the field name can be confirmed against live data.
- Sessions terminated without a `Stop` event still appear, with `ended_at` null
  and any captured skill/tool events preserved.
```
