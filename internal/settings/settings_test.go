package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const bin = "/opt/cost-tracker"

func read(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func TestInstallHooksCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")

	added, err := InstallHooks(path, bin)
	if err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if len(added) != 2 {
		t.Fatalf("expected 2 events added, got %v", added)
	}

	data := read(t, path)
	hooks, ok := data["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks missing or wrong type: %#v", data)
	}
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Error("PostToolUse not written")
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Error("Stop not written")
	}
}

func TestInstallHooksIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	if _, err := InstallHooks(path, bin); err != nil {
		t.Fatalf("first install: %v", err)
	}
	added, err := InstallHooks(path, bin)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("re-run added hooks again: %v", added)
	}

	// Exactly one group per event survives.
	hooks := read(t, path)["hooks"].(map[string]any)
	if groups, _ := hooks["PostToolUse"].([]any); len(groups) != 1 {
		t.Errorf("PostToolUse has %d groups, want 1", len(groups))
	}
}

func TestInstallHooksPreservesExistingKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := `{"model":"opus","hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"other"}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallHooks(path, bin); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	data := read(t, path)
	if data["model"] != "opus" {
		t.Errorf("top-level key clobbered: %#v", data["model"])
	}
	hooks := data["hooks"].(map[string]any)
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("existing PreToolUse hook removed")
	}
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Error("PostToolUse not added alongside existing hooks")
	}
}

func TestInstallHooksRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHooks(path, bin); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestRemoveHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := InstallHooks(path, bin); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveHooks(path, bin)
	if err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 events removed, got %v", removed)
	}
	hooks, _ := read(t, path)["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"]; ok {
		t.Error("PostToolUse not removed")
	}
}

func TestPathHonoursEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "settings.json"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}
