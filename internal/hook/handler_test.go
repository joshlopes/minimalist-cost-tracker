package hook_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lendable/minimalist-cost-tracker/internal/db"
	"github.com/lendable/minimalist-cost-tracker/internal/hook"
	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
	"github.com/lendable/minimalist-cost-tracker/internal/recorder"
)

func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tracker.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func feed(t *testing.T, rec *recorder.Recorder, payload string) {
	t.Helper()
	if err := hook.Handle(strings.NewReader(payload), rec, pricing.New()); err != nil {
		t.Fatalf("Handle returned error (should always be nil): %v", err)
	}
}

func TestPostToolUseSkillRecordsEvents(t *testing.T) {
	d := newTestDB(t)
	rec := recorder.New(d, "default")

	feed(t, rec, `{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/tmp/proj","tool_name":"Skill","tool_use_id":"tu1","tool_input":{"skill":"pr"}}`)

	skills, err := d.Skills("")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].SkillName != "pr" || skills[0].UsageCount != 1 {
		t.Fatalf("skills = %+v, want one 'pr' usage", skills)
	}

	// A Skill PostToolUse also records a tool_event for "Skill".
	detail, err := d.SessionByID("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.SkillEvents) != 1 {
		t.Errorf("skill_events = %d, want 1", len(detail.SkillEvents))
	}
	if len(detail.ToolEvents) != 1 || detail.ToolEvents[0].Name != "Skill" {
		t.Errorf("tool_events = %+v, want one 'Skill'", detail.ToolEvents)
	}
}

func TestPostToolUseNonSkillRecordsToolOnly(t *testing.T) {
	d := newTestDB(t)
	rec := recorder.New(d, "default")

	feed(t, rec, `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash","tool_use_id":"tu2","tool_input":{"command":"ls"}}`)

	skills, _ := d.Skills("")
	if len(skills) != 0 {
		t.Errorf("expected no skill events, got %+v", skills)
	}
	detail, _ := d.SessionByID("s1")
	if len(detail.ToolEvents) != 1 || detail.ToolEvents[0].Name != "Bash" {
		t.Errorf("tool_events = %+v, want one 'Bash'", detail.ToolEvents)
	}
}

func TestPartialSessionPreservedWithoutStop(t *testing.T) {
	d := newTestDB(t)
	rec := recorder.New(d, "default")

	feed(t, rec, `{"hook_event_name":"PostToolUse","session_id":"partial","tool_name":"Skill","tool_input":{"skill":"reflect"}}`)

	detail, err := d.SessionByID("partial")
	if err != nil {
		t.Fatal(err)
	}
	if detail.EndedAt != nil {
		t.Errorf("ended_at = %v, want nil for a session with no Stop", *detail.EndedAt)
	}
	if len(detail.SkillEvents) != 1 {
		t.Errorf("skill events should be preserved, got %d", len(detail.SkillEvents))
	}
}

func TestStopFinalisesSession(t *testing.T) {
	d := newTestDB(t)
	rec := recorder.New(d, "default")

	transcriptPath := filepath.Join(t.TempDir(), "t.jsonl")
	body := `{"type":"assistant","timestamp":"2026-06-04T10:00:05Z","message":{"role":"assistant","model":"claude-opus-4-8","usage":{"input_tokens":1000,"output_tokens":500}}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	feed(t, rec, `{"hook_event_name":"PostToolUse","session_id":"s2","tool_name":"Read"}`)
	feed(t, rec, `{"hook_event_name":"Stop","session_id":"s2","transcript_path":"`+transcriptPath+`"}`)

	detail, err := d.SessionByID("s2")
	if err != nil {
		t.Fatal(err)
	}
	if detail.EndedAt == nil {
		t.Error("ended_at should be set after Stop")
	}
	if detail.Model != "claude-opus-4-8" {
		t.Errorf("model = %q, want claude-opus-4-8", detail.Model)
	}
	if detail.InputTokens != 1000 || detail.OutputTokens != 500 {
		t.Errorf("tokens = (%d,%d), want (1000,500)", detail.InputTokens, detail.OutputTokens)
	}
	if detail.CostUSD <= 0 {
		t.Errorf("cost = %v, want > 0", detail.CostUSD)
	}
}

func TestHandleGarbageNeverErrors(t *testing.T) {
	d := newTestDB(t)
	rec := recorder.New(d, "default")
	// Malformed JSON, empty input, missing session id — all must be swallowed.
	for _, p := range []string{``, `not json`, `{}`, `{"hook_event_name":"PostToolUse"}`} {
		if err := hook.Handle(strings.NewReader(p), rec, pricing.New()); err != nil {
			t.Errorf("Handle(%q) returned %v, want nil", p, err)
		}
	}
}
