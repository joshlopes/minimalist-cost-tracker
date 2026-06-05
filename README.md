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
5. **asks** whether to run the dashboard as a start-on-login service (launchd on
   macOS, systemd-user on Linux). The dashboard is optional — hooks record cost
   data either way, and you can start it any time with `cost-tracker serve`.

If you answer yes it finishes with the URL your dashboard is running on. The
installer is idempotent and configurable via environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `VERSION` | latest | install a specific tag, e.g. `v1.2.3` |
| `COST_TRACKER_PORT` | `7842` | preferred port (a free one is chosen if busy) |
| `COST_TRACKER_SERVICE` | _ask_ | `1` runs the dashboard server, `0` skips it; set it to install unattended without the prompt (no terminal → defaults to `1`) |
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
cost-tracker update --check # only report whether a newer release exists
```

`update` checks the latest release, downloads the matching binary, verifies its
checksum, and atomically replaces itself in place. Restart the dashboard (or run
`cost-tracker service install` again) to run the new version. Homebrew users
update with `brew upgrade cost-tracker` instead.

The running dashboard also checks GitHub for newer releases in the background
(on start, then every few hours) and shows an **update-available banner** at the
top of the page when one is found, so you don't have to remember to check. The
same data is available at `/api/version`
(`{"current","latest","update_available"}`).

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
cost-tracker hook [--profile P]   read a hook event from stdin and record it
cost-tracker serve [--port N]     start the dashboard (default 7842)
cost-tracker migrate              create/upgrade the database schema
cost-tracker install-hooks [--all] wire the hooks into Claude Code settings.json
                                  (--all = every profile; --settings P = a specific one)
cost-tracker service install      install + start the login service (--port N)
cost-tracker service uninstall    stop and remove the login service
cost-tracker service start        start the installed service
cost-tracker service stop         stop it (keeps the definition; restarts on login)
cost-tracker service restart      restart it (e.g. to pick up a new binary)
cost-tracker service status       show the login-service status
cost-tracker update [--repo R]    self-update to the latest GitHub release
                                  (--check = report only, don't update)
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

## FAQ

### Where does it install the hooks?

Into a Claude Code `settings.json`, under the `hooks.PostToolUse` and `hooks.Stop`
keys. By default a single location is chosen, in this order:

1. `$CLAUDE_CONFIG_DIR/settings.json`, if that variable is set;
2. otherwise the first existing of `~/.claude` then `~/.claude-work`;
3. otherwise `~/.claude/settings.json` (created if missing).

So if you have both `~/.claude` and `~/.claude-work`, a plain install lands in
`~/.claude` — the first match. Check where it went with:

```sh
grep -l 'cost-tracker hook' ~/.claude/settings.json ~/.claude-work/settings.json 2>/dev/null
```

### I run more than one Claude (e.g. personal + work). How do I track both?

Wire the hooks into **every** profile:

```sh
cost-tracker install-hooks --all      # ~/.claude and ~/.claude-work
```

or target one explicitly:

```sh
cost-tracker install-hooks --settings ~/.claude-work/settings.json
```

Each profile gets a distinct hook command — the default profile keeps the bare
`cost-tracker hook` (so existing installs aren't disturbed), and named profiles
get `cost-tracker hook --profile <name>`. The profile label is derived from the
config directory: `~/.claude` → `default`, `~/.claude-work` → `work`,
`~/.claude-<x>` → `<x>`. Re-running is idempotent and safe.

### Can I see costs per profile?

Yes. Every session is stamped with the profile that recorded it. When more than
one profile has data, the dashboard shows a **Profile** selector in the header
that filters every tab; the Sessions table also gains a Profile column. The same
filter is available on the API via `?profile=<name>` (e.g.
`/api/stats?profile=work`), and `/api/profiles` lists the known profiles.

### Will adding profiles double-count or duplicate hooks?

No. Hooks are de-duplicated by exact command string, so re-running `install-hooks`
(with or without `--all`) never adds a second copy. An upgrade from an
older single-profile install keeps working unchanged — its bare `cost-tracker
hook` command is recognised as the `default` profile. Existing databases are
migrated automatically (the `profile` column back-fills to `default`).

## Development

```sh
make build   # -> ./bin/cost-tracker
make test    # go test ./...
make vet
make fmt
make dist    # cross-compiled release tarballs + SHA256SUMS in ./dist
```

## Releasing

Releases are automated with [release-please](https://github.com/googleapis/release-please)
and driven by the conventional-commit history — no manual tagging. The version
bump follows semver: `fix:` → patch, `feat:` → minor, `feat!:` / `BREAKING
CHANGE:` → major.

1. Land commits on `main` using conventional-commit messages.
2. release-please keeps a standing **"chore(main): release X.Y.Z"** PR open,
   maintaining the next version and `CHANGELOG.md` from those commits.
3. **Merging that PR is the approval step** — it creates the `vX.Y.Z` tag and a
   GitHub Release.

The same `.github/workflows/release.yml` then runs its `build` job, which
cross-compiles `darwin/{amd64,arm64}` and `linux/{amd64,arm64}` (pure-Go SQLite
means `CGO_ENABLED=0`, so one runner does all targets), writes `SHA256SUMS`,
attaches the tarballs to the release, and commits the bumped Homebrew formula
(version + the four per-platform `sha256` values). `install.sh` and
`cost-tracker update` consume those assets.

> The build is folded into the release-please workflow on purpose: a tag created
> with the default `GITHUB_TOKEN` does not trigger a separate `push: tags`
> workflow. To force a specific version, add `Release-As: 1.2.3` to a commit body.

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
