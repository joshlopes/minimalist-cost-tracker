package recorder

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/lendable/minimalist-cost-tracker/internal/db"
	"github.com/lendable/minimalist-cost-tracker/internal/transcript"
)

func newRecorder(t *testing.T) (*Recorder, *db.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rec.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return New(d, "default"), d
}

func TestEnsureSessionIdempotent(t *testing.T) {
	r, d := newRecorder(t)

	if err := r.EnsureSession("s1", "/work"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	// Capture the original started_at, then ensure again — the existing row must
	// not be overwritten (INSERT OR IGNORE).
	var firstStart string
	if err := d.QueryRow(`SELECT started_at FROM sessions WHERE id = ?`, "s1").Scan(&firstStart); err != nil {
		t.Fatalf("read started_at: %v", err)
	}
	if err := r.EnsureSession("s1", "/somewhere-else"); err != nil {
		t.Fatalf("EnsureSession again: %v", err)
	}

	var count int
	var cwd, start string
	if err := d.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("session count = %d, want 1 (idempotent)", count)
	}
	if err := d.QueryRow(`SELECT cwd, started_at FROM sessions WHERE id = ?`, "s1").Scan(&cwd, &start); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if cwd != "/work" {
		t.Errorf("cwd = %q, want /work (original preserved)", cwd)
	}
	if start != firstStart {
		t.Errorf("started_at changed from %q to %q", firstStart, start)
	}
}

func TestEnsureSessionRecordsProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := New(d, "work").EnsureSession("s1", "/work"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	var profile string
	if err := d.QueryRow(`SELECT profile FROM sessions WHERE id = ?`, "s1").Scan(&profile); err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if profile != "work" {
		t.Errorf("profile = %q, want work", profile)
	}

	// An empty profile falls back to "default".
	if err := New(d, "").EnsureSession("s2", "/x"); err != nil {
		t.Fatalf("EnsureSession default: %v", err)
	}
	if err := d.QueryRow(`SELECT profile FROM sessions WHERE id = ?`, "s2").Scan(&profile); err != nil {
		t.Fatalf("read profile s2: %v", err)
	}
	if profile != "default" {
		t.Errorf("empty profile = %q, want default", profile)
	}
}

func TestInsertEvents(t *testing.T) {
	r, d := newRecorder(t)
	if err := r.EnsureSession("s1", "/work"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := r.InsertSkillEvent("s1", "pr", "tu-1"); err != nil {
		t.Fatalf("InsertSkillEvent: %v", err)
	}
	if err := r.InsertToolEvent("s1", "Bash", "tu-2"); err != nil {
		t.Fatalf("InsertToolEvent: %v", err)
	}
	// Empty tool_use_id must be stored as NULL, not "".
	if err := r.InsertToolEvent("s1", "Read", ""); err != nil {
		t.Fatalf("InsertToolEvent empty id: %v", err)
	}

	detail, err := d.SessionByID("s1")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if len(detail.SkillEvents) != 1 || detail.SkillEvents[0].Name != "pr" {
		t.Errorf("skill events = %+v, want one named pr", detail.SkillEvents)
	}
	if len(detail.ToolEvents) != 2 {
		t.Errorf("tool events = %d, want 2", len(detail.ToolEvents))
	}

	var nullCount int
	if err := d.QueryRow(`SELECT COUNT(*) FROM tool_events WHERE tool_use_id IS NULL`).Scan(&nullCount); err != nil {
		t.Fatalf("null count: %v", err)
	}
	if nullCount != 1 {
		t.Errorf("tool_use_id NULL rows = %d, want 1", nullCount)
	}
}

func TestFinaliseSession(t *testing.T) {
	r, d := newRecorder(t)
	if err := r.EnsureSession("s1", "/work"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	started := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 6, 1, 11, 30, 0, 0, time.UTC)
	summary := transcript.SessionSummary{
		Model:            "claude-opus-4",
		InputTokens:      1000,
		OutputTokens:     500,
		CacheReadTokens:  200,
		CacheWriteTokens: 100,
		CostUSD:          1.23,
		StartedAt:        started,
		EndedAt:          ended,
	}
	if err := r.FinaliseSession("s1", summary); err != nil {
		t.Fatalf("FinaliseSession: %v", err)
	}

	row, err := d.Sessions(10, 0, "cost", "")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(row) != 1 {
		t.Fatalf("got %d sessions, want 1", len(row))
	}
	s := row[0]
	if s.Model != "claude-opus-4" {
		t.Errorf("Model = %q, want claude-opus-4", s.Model)
	}
	if s.InputTokens != 1000 || s.OutputTokens != 500 {
		t.Errorf("tokens = %d/%d, want 1000/500", s.InputTokens, s.OutputTokens)
	}
	if s.CacheReadTokens != 200 || s.CacheWriteTokens != 100 {
		t.Errorf("cache tokens = %d/%d, want 200/100", s.CacheReadTokens, s.CacheWriteTokens)
	}
	if s.CostUSD != 1.23 {
		t.Errorf("CostUSD = %v, want 1.23", s.CostUSD)
	}
	if s.EndedAt == nil {
		t.Errorf("EndedAt should be set after finalise")
	}
}

// A session that only ever got EnsureSession (no Stop/finalise) must still be
// listed, with a null ended_at and its skill events preserved.
func TestUnfinalisedSessionVisible(t *testing.T) {
	r, d := newRecorder(t)
	if err := r.EnsureSession("partial", "/work"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := r.InsertSkillEvent("partial", "pr", "tu-1"); err != nil {
		t.Fatalf("InsertSkillEvent: %v", err)
	}

	rows, err := d.Sessions(10, 0, "date", "")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d sessions, want 1", len(rows))
	}
	if rows[0].EndedAt != nil {
		t.Errorf("EndedAt = %v, want nil for unfinalised session", *rows[0].EndedAt)
	}
	if len(rows[0].Skills) != 1 {
		t.Errorf("skills = %v, want [pr] preserved", rows[0].Skills)
	}
}

func TestFinaliseDefaultsEndedAtWhenZero(t *testing.T) {
	r, d := newRecorder(t)
	if err := r.EnsureSession("s1", "/work"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	// Zero timestamps: ended_at must default to now, started_at must be preserved.
	if err := r.FinaliseSession("s1", transcript.SessionSummary{Model: "claude-sonnet-4", CostUSD: 0.1}); err != nil {
		t.Fatalf("FinaliseSession: %v", err)
	}
	var ended sql.NullString
	if err := d.QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, "s1").Scan(&ended); err != nil {
		t.Fatalf("read ended_at: %v", err)
	}
	if !ended.Valid || ended.String == "" {
		t.Errorf("ended_at should default to now, got %+v", ended)
	}
}

func TestSetTranscriptPath(t *testing.T) {
	r, d := newRecorder(t)
	if err := r.EnsureSession("s1", "/work"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := r.SetTranscriptPath("s1", "/tmp/transcript.jsonl"); err != nil {
		t.Fatalf("SetTranscriptPath: %v", err)
	}
	var path sql.NullString
	if err := d.QueryRow(`SELECT transcript_path FROM sessions WHERE id = ?`, "s1").Scan(&path); err != nil {
		t.Fatalf("read transcript_path: %v", err)
	}
	if path.String != "/tmp/transcript.jsonl" {
		t.Errorf("transcript_path = %q, want /tmp/transcript.jsonl", path.String)
	}

	// Empty path is a no-op (must not clobber the stored value).
	if err := r.SetTranscriptPath("s1", ""); err != nil {
		t.Fatalf("SetTranscriptPath empty: %v", err)
	}
	if err := d.QueryRow(`SELECT transcript_path FROM sessions WHERE id = ?`, "s1").Scan(&path); err != nil {
		t.Fatalf("re-read transcript_path: %v", err)
	}
	if path.String != "/tmp/transcript.jsonl" {
		t.Errorf("transcript_path clobbered by empty set: %q", path.String)
	}
}
