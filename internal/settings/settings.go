// Package settings wires the cost-tracker hooks into a Claude Code
// settings.json. It is the Go port of the python3 snippet setup.sh used to
// rely on, so the curl-able installer needs no interpreter beyond the binary
// itself. All edits are idempotent (de-duplicated by command string) and
// written atomically.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The hook events cost-tracker registers for, and the matcher (if any) each
// group carries. PostToolUse fires per tool call; Stop fires when a session
// ends.
var hookEvents = []struct {
	event   string
	matcher string // empty => no matcher key
}{
	{"PostToolUse", ".*"},
	{"Stop", ""},
}

// Path returns the Claude Code settings.json that hooks should be written to.
// CLAUDE_CONFIG_DIR wins; otherwise the first existing config dir among
// ~/.claude and ~/.claude-work is used; otherwise the standard ~/.claude.
func Path() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	for _, name := range []string{".claude", ".claude-work"} {
		dir := filepath.Join(home, name)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return filepath.Join(dir, "settings.json"), nil
		}
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// InstallHooks ensures the cost-tracker hook command is present in the
// settings file at path, running "<binPath> hook". It returns the list of
// events that were newly added (empty if everything was already present).
func InstallHooks(path, binPath string) ([]string, error) {
	cmd := binPath + " hook"

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	data, err := load(path)
	if err != nil {
		return nil, err
	}

	hooks, err := mapAt(data, "hooks")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var added []string
	for _, he := range hookEvents {
		if addHook(hooks, he.event, he.matcher, cmd) {
			added = append(added, he.event)
		}
	}

	if len(added) == 0 {
		return nil, nil
	}
	if err := writeAtomic(path, data); err != nil {
		return nil, err
	}
	return added, nil
}

// RemoveHooks strips every cost-tracker hook group (any whose command ends in
// " hook" and points at binPath) from the settings file. It returns the events
// it touched. Used by the uninstall path.
func RemoveHooks(path, binPath string) ([]string, error) {
	cmd := binPath + " hook"
	data, err := load(path)
	if err != nil {
		return nil, err
	}
	hooks, ok := data["hooks"].(map[string]any)
	if !ok {
		return nil, nil
	}

	var removed []string
	for _, he := range hookEvents {
		groups, ok := hooks[he.event].([]any)
		if !ok {
			continue
		}
		kept := groups[:0]
		changed := false
		for _, g := range groups {
			if groupHasCmd(g, cmd) {
				changed = true
				continue
			}
			kept = append(kept, g)
		}
		if changed {
			removed = append(removed, he.event)
			if len(kept) == 0 {
				delete(hooks, he.event)
			} else {
				hooks[he.event] = kept
			}
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if err := writeAtomic(path, data); err != nil {
		return nil, err
	}
	return removed, nil
}

// load reads and parses path, treating a missing file as an empty object. It
// refuses to proceed on invalid JSON or a non-object root so a hand-edited
// settings file is never clobbered.
func load(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v); refusing to overwrite, fix it and re-run", path, err)
	}
	return data, nil
}

// mapAt returns data[key] as a map, creating it if absent. It errors if the
// key exists but is not an object.
func mapAt(data map[string]any, key string) (map[string]any, error) {
	v, ok := data[key]
	if !ok || v == nil {
		m := map[string]any{}
		data[key] = m
		return m, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%q is not an object", key)
	}
	return m, nil
}

// addHook appends a hook group for event unless an identical command is
// already registered. Returns true if it added one.
func addHook(hooks map[string]any, event, matcher, cmd string) bool {
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		if groupHasCmd(g, cmd) {
			return false
		}
	}
	group := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": cmd},
		},
	}
	if matcher != "" {
		group["matcher"] = matcher
	}
	hooks[event] = append(groups, group)
	return true
}

// groupHasCmd reports whether a hook group contains a command equal to cmd.
func groupHasCmd(group any, cmd string) bool {
	gm, ok := group.(map[string]any)
	if !ok {
		return false
	}
	inner, ok := gm["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if c, _ := hm["command"].(string); c == cmd {
			return true
		}
	}
	return false
}

// writeAtomic marshals data to path via a temp file + rename, so a crash mid
// write cannot leave a half-written settings.json.
func writeAtomic(path string, data map[string]any) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".cost-tracker-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
