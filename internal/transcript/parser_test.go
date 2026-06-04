package transcript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
)

// A small synthetic transcript modelled on Claude Code's JSONL: assistant
// messages carry model + usage, and tool_use content blocks carry Skill calls.
const sample = `{"type":"user","timestamp":"2026-06-04T10:00:00Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","timestamp":"2026-06-04T10:00:05Z","message":{"role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":50,"cache_creation_input_tokens":10},"content":[{"type":"text","text":"ok"},{"type":"tool_use","name":"Skill","input":{"skill":"pr"}}]}}
{"type":"assistant","timestamp":"2026-06-04T10:00:09Z","message":{"role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":500,"output_tokens":100},"content":[{"type":"tool_use","name":"Skill","input":{"name":"deploy"}}]}}
garbage line that should be skipped
{"type":"assistant","timestamp":"2026-06-04T10:00:12Z","message":{"role":"assistant","model":"claude-sonnet-4-6","usage":{"input_tokens":300,"output_tokens":40},"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
`

func writeTranscript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParse(t *testing.T) {
	path := writeTranscript(t, sample)
	s, err := Parse(path, pricing.New())
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q", s.Model)
	}
	if s.InputTokens != 1800 {
		t.Errorf("input = %d, want 1800", s.InputTokens)
	}
	if s.OutputTokens != 340 {
		t.Errorf("output = %d, want 340", s.OutputTokens)
	}
	if s.CacheReadTokens != 50 || s.CacheWriteTokens != 10 {
		t.Errorf("cache = (%d,%d), want (50,10)", s.CacheReadTokens, s.CacheWriteTokens)
	}
	if s.CostUSD <= 0 {
		t.Errorf("cost = %v, want > 0", s.CostUSD)
	}
	// Both "skill" and "name" keys should be picked up.
	want := map[string]bool{"pr": true, "deploy": true}
	if len(s.SkillsObserved) != 2 {
		t.Fatalf("skills = %v, want pr+deploy", s.SkillsObserved)
	}
	for _, k := range s.SkillsObserved {
		if !want[k] {
			t.Errorf("unexpected skill %q", k)
		}
	}
	if s.StartedAt.IsZero() || s.EndedAt.IsZero() || !s.EndedAt.After(s.StartedAt) {
		t.Errorf("timestamps: start=%v end=%v", s.StartedAt, s.EndedAt)
	}
}

func TestParseMissingFileErrors(t *testing.T) {
	if _, err := Parse("/no/such/transcript.jsonl", pricing.New()); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseEmptyFile(t *testing.T) {
	path := writeTranscript(t, "")
	s, err := Parse(path, pricing.New())
	if err != nil {
		t.Fatal(err)
	}
	if s.InputTokens != 0 || s.CostUSD != 0 {
		t.Errorf("empty transcript should yield zero totals, got %+v", s)
	}
	// EndedAt should fall back to file mtime (non-zero).
	if s.EndedAt.IsZero() {
		t.Error("expected EndedAt to fall back to file mtime")
	}
}
