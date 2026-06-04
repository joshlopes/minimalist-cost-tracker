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
	"strings"
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

// configDirNames are the Claude Code config directory names cost-tracker knows
// about, in priority order. Each maps to a separate "profile" so a machine that
// runs more than one Claude (e.g. a personal ~/.claude and a work ~/.claude-work)
// can track each independently.
var configDirNames = []string{".claude", ".claude-work"}

// Profile is one Claude Code config location hooks can be wired into: a logical
// label plus the directory and settings.json it resolves to.
type Profile struct {
	Name string // logical label, e.g. "default" or "work"
	Dir  string // the config directory, e.g. /home/me/.claude
	Path string // settings.json inside Dir
}

// ProfileName maps a config directory to a short logical label:
//
//	~/.claude       -> "default"
//	~/.claude-work  -> "work"
//	~/.claude-foo   -> "foo"
//
// Anything that does not follow the ".claude[-suffix]" convention falls back to
// the directory's base name with a leading dot stripped.
func ProfileName(dir string) string {
	name := strings.TrimPrefix(filepath.Base(dir), ".")
	switch {
	case name == "" || name == "claude":
		return "default"
	case strings.HasPrefix(name, "claude-"):
		return strings.TrimPrefix(name, "claude-")
	default:
		return name
	}
}

// profileForDir builds a Profile from a config directory.
func profileForDir(dir string) Profile {
	return Profile{Name: ProfileName(dir), Dir: dir, Path: filepath.Join(dir, "settings.json")}
}

// HookCommand returns the hook command string written into settings for a
// profile. The default profile keeps the bare "<bin> hook" form so installs
// made before profiles existed are matched (and never duplicated) on re-run;
// every named profile appends "--profile <name>" so its sessions are attributed
// correctly.
func HookCommand(binPath, profile string) string {
	if profile == "" || profile == "default" {
		return binPath + " hook"
	}
	return binPath + " hook --profile " + profile
}

// Path returns the single Claude Code settings.json that hooks should be written
// to by default. CLAUDE_CONFIG_DIR wins; otherwise the first existing config dir
// among ~/.claude and ~/.claude-work is used; otherwise the standard ~/.claude.
func Path() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	for _, name := range configDirNames {
		dir := filepath.Join(home, name)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return filepath.Join(dir, "settings.json"), nil
		}
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// DefaultProfile is the single profile Path() resolves to — the target used
// when install-hooks is run with no flags.
func DefaultProfile() (Profile, error) {
	p, err := Path()
	if err != nil {
		return Profile{}, err
	}
	return profileForDir(filepath.Dir(p)), nil
}

// AllProfiles returns every Claude Code config location hooks should be wired
// into. CLAUDE_CONFIG_DIR, when set, pins a single profile; otherwise every
// existing dir among ~/.claude and ~/.claude-work is returned. If none exist,
// the default (~/.claude) is returned so there is always at least one target.
func AllProfiles() ([]Profile, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return []Profile{profileForDir(d)}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var profiles []Profile
	for _, name := range configDirNames {
		dir := filepath.Join(home, name)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			profiles = append(profiles, profileForDir(dir))
		}
	}
	if len(profiles) == 0 {
		profiles = append(profiles, profileForDir(filepath.Join(home, ".claude")))
	}
	return profiles, nil
}

// InstallHooks ensures the cost-tracker hook command for the given profile is
// present in the settings file at path, running "<binPath> hook" (default
// profile) or "<binPath> hook --profile <name>". It returns the list of events
// that were newly added (empty if everything was already present).
func InstallHooks(path, binPath, profile string) ([]string, error) {
	cmd := HookCommand(binPath, profile)

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

// RemoveHooks strips every cost-tracker hook group (any whose command begins
// with "<binPath> hook", covering the default and every named profile) from the
// settings file. It returns the events it touched. Used by the uninstall path.
func RemoveHooks(path, binPath string) ([]string, error) {
	prefix := binPath + " hook"
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
			if groupHasCmdPrefix(g, prefix) {
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
	return groupMatches(group, func(c string) bool { return c == cmd })
}

// groupHasCmdPrefix reports whether a hook group contains a command starting
// with prefix — used by removal to catch the default and every named profile.
func groupHasCmdPrefix(group any, prefix string) bool {
	return groupMatches(group, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

func groupMatches(group any, match func(cmd string) bool) bool {
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
		if c, _ := hm["command"].(string); match(c) {
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
