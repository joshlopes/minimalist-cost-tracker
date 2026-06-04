# minimalist-cost-tracker

A single Go binary that hooks into Claude Code, records token usage and cost per
session to a local SQLite database, and serves a web dashboard showing cost
breakdowns by session, skill, model, and time.

No runtime dependencies, no remote services, no build step for the frontend —
everything (including the dashboard HTML/JS) is embedded in one binary.

## Install

```sh
./setup.sh
```

`setup.sh` (idempotent — safe to re-run):

1. Builds `cost-tracker` into `~/.local/bin/`.
2. Creates `~/.local/share/cost-tracker/` and runs the schema migration.
3. Wires `PostToolUse` and `Stop` hooks into `~/.claude-work/settings.json`
   (de-duplicated by command string, written atomically).

Requires Go 1.22+ and `python3` (used only to patch `settings.json`). If
`~/.local/bin` is not on your `PATH`, add it.

Hooks take effect on your **next** Claude Code session.

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
cost-tracker hook              read a hook event from stdin and record it
cost-tracker serve [--port N]  start the dashboard (default 7842)
cost-tracker migrate           create/upgrade the database schema
cost-tracker version           print version
```

## Data layout

| Path | Purpose |
|---|---|
| `~/.local/bin/cost-tracker` | the binary |
| `~/.local/share/cost-tracker/tracker.db` | SQLite database (WAL mode) |
| `~/.local/share/cost-tracker/hook.log` | hook debug/error log |
| `~/.claude-work/settings.json` | Claude Code hooks live here |

`XDG_DATA_HOME` and `CLAUDE_CONFIG_DIR` are honoured if set.

## Uninstall

```sh
# 1. remove the hook entries from settings.json (the two groups whose command
#    is "<.../cost-tracker> hook" under hooks.PostToolUse and hooks.Stop)
$EDITOR ~/.claude-work/settings.json

# 2. delete the binary and data
rm -f ~/.local/bin/cost-tracker
rm -rf ~/.local/share/cost-tracker
```

## Development

```sh
make build   # -> ./bin/cost-tracker
make test    # go test ./...
make vet
make fmt
```

## Notes / limitations

- Cost is computed from a hardcoded price table in `internal/pricing`; update it
  when Anthropic prices change.
- The transcript JSONL format and the `Skill` tool-input field name are not
  formally documented. The parser is deliberately lenient (unknown fields are
  ignored, malformed lines are skipped) and the hook logs the first `Skill`
  payload to `hook.log` so the field name can be confirmed against live data.
- Sessions terminated without a `Stop` event still appear, with `ended_at` null
  and any captured skill/tool events preserved.
