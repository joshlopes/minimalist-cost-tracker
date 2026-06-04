package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// openTestDB opens a fresh migrated database in a temp directory.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Migrate must be idempotent.
	if err := d.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	return d
}

// insertSession seeds a finalised session row directly.
func (d *DB) insertSession(t *testing.T, id, cwd, model string, in, out, cacheR, cacheW int, cost float64, endedAt string) {
	t.Helper()
	_, err := d.Exec(`
		INSERT INTO sessions
			(id, cwd, model, started_at, ended_at,
			 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
		VALUES (?, ?, ?, datetime('now'), ?, ?, ?, ?, ?, ?)`,
		id, cwd, model, endedAt, in, out, cacheR, cacheW, cost)
	if err != nil {
		t.Fatalf("insertSession %s: %v", id, err)
	}
}

func (d *DB) insertSkill(t *testing.T, sessionID, skill string) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO skill_events (session_id, skill_name, tool_use_id) VALUES (?, ?, ?)`,
		sessionID, skill, "tu-"+skill); err != nil {
		t.Fatalf("insertSkill: %v", err)
	}
}

func (d *DB) insertTool(t *testing.T, sessionID, tool string) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO tool_events (session_id, tool_name, tool_use_id) VALUES (?, ?, ?)`,
		sessionID, tool, "tu-"+tool); err != nil {
		t.Fatalf("insertTool: %v", err)
	}
}

func TestStats(t *testing.T) {
	d := openTestDB(t)

	empty, err := d.Stats()
	if err != nil {
		t.Fatalf("Stats (empty): %v", err)
	}
	if empty.TotalSessions != 0 || empty.TotalCostUSD != 0 || empty.AvgCostPerSession != 0 {
		t.Fatalf("expected zero stats on empty db, got %+v", empty)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	d.insertSession(t, "s1", "/a", "claude-opus-4", 100, 50, 0, 0, 2.0, now)
	d.insertSession(t, "s2", "/b", "claude-sonnet-4", 200, 80, 0, 0, 1.0, now)

	s, err := d.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.TotalSessions != 2 {
		t.Errorf("TotalSessions = %d, want 2", s.TotalSessions)
	}
	if s.TotalCostUSD != 3.0 {
		t.Errorf("TotalCostUSD = %v, want 3.0", s.TotalCostUSD)
	}
	if s.TotalInputTokens != 300 {
		t.Errorf("TotalInputTokens = %d, want 300", s.TotalInputTokens)
	}
	if s.TotalOutputTokens != 130 {
		t.Errorf("TotalOutputTokens = %d, want 130", s.TotalOutputTokens)
	}
	if s.AvgCostPerSession != 1.5 {
		t.Errorf("AvgCostPerSession = %v, want 1.5", s.AvgCostPerSession)
	}
}

func TestSessionsSortAndSkills(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	d.insertSession(t, "cheap", "/a", "claude-opus-4", 1, 1, 0, 0, 0.5, now)
	d.insertSession(t, "pricey", "/b", "claude-sonnet-4", 1, 1, 0, 0, 9.0, now)
	d.insertSkill(t, "pricey", "pr")
	d.insertSkill(t, "pricey", "deploy")

	// Default sort is cost DESC.
	rows, err := d.Sessions(0, 0, "")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d sessions, want 2", len(rows))
	}
	if rows[0].ID != "pricey" {
		t.Errorf("default sort: first = %q, want pricey", rows[0].ID)
	}
	if len(rows[0].Skills) != 2 {
		t.Errorf("pricey skills = %v, want 2 distinct", rows[0].Skills)
	}
	// Session without skills must yield an empty (non-nil) slice, not null.
	if rows[1].Skills == nil {
		t.Errorf("cheap.Skills should be non-nil empty slice")
	}

	// Limit + offset.
	page, err := d.Sessions(1, 1, "cost")
	if err != nil {
		t.Fatalf("Sessions paged: %v", err)
	}
	if len(page) != 1 || page[0].ID != "cheap" {
		t.Errorf("paged result = %+v, want [cheap]", page)
	}
}

func TestSessionByID(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	d.insertSession(t, "s1", "/a", "claude-opus-4", 10, 5, 0, 0, 1.0, now)
	d.insertSkill(t, "s1", "pr")
	d.insertTool(t, "s1", "Bash")
	d.insertTool(t, "s1", "Read")

	detail, err := d.SessionByID("s1")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if detail.ID != "s1" {
		t.Errorf("ID = %q, want s1", detail.ID)
	}
	if len(detail.SkillEvents) != 1 {
		t.Errorf("SkillEvents = %d, want 1", len(detail.SkillEvents))
	}
	if len(detail.ToolEvents) != 2 {
		t.Errorf("ToolEvents = %d, want 2", len(detail.ToolEvents))
	}

	if _, err := d.SessionByID("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("SessionByID(missing) err = %v, want sql.ErrNoRows", err)
	}
}

func TestSkillsAttribution(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	d.insertSession(t, "s1", "/a", "claude-opus-4", 0, 0, 0, 0, 10.0, now)
	// Same skill used twice in one session must not double-count the session cost.
	d.insertSkill(t, "s1", "pr")
	d.insertSkill(t, "s1", "pr")
	d.insertSession(t, "s2", "/b", "claude-sonnet-4", 0, 0, 0, 0, 4.0, now)
	d.insertSkill(t, "s2", "pr")

	skills, err := d.Skills()
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}
	pr := skills[0]
	if pr.SkillName != "pr" {
		t.Errorf("SkillName = %q, want pr", pr.SkillName)
	}
	if pr.UsageCount != 3 {
		t.Errorf("UsageCount = %d, want 3", pr.UsageCount)
	}
	if pr.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", pr.SessionCount)
	}
	if pr.TotalCostUSD != 14.0 {
		t.Errorf("TotalCostUSD = %v, want 14.0 (per-session, not per-use)", pr.TotalCostUSD)
	}
	if pr.AvgCostUSD != 7.0 {
		t.Errorf("AvgCostUSD = %v, want 7.0", pr.AvgCostUSD)
	}
}

func TestModels(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC().Format(time.RFC3339)
	d.insertSession(t, "s1", "/a", "claude-opus-4", 100, 10, 0, 0, 5.0, now)
	d.insertSession(t, "s2", "/b", "claude-opus-4", 200, 20, 0, 0, 3.0, now)
	d.insertSession(t, "s3", "/c", "", 50, 5, 0, 0, 1.0, now) // empty model → "unknown"

	models, err := d.Models()
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	byName := map[string]ModelStatRow{}
	for _, m := range models {
		byName[m.Model] = m
	}
	opus, ok := byName["claude-opus-4"]
	if !ok {
		t.Fatalf("missing claude-opus-4 in %+v", models)
	}
	if opus.SessionCount != 2 || opus.TotalCostUSD != 8.0 || opus.TotalInput != 300 {
		t.Errorf("opus = %+v, want 2 sessions / 8.0 / 300 input", opus)
	}
	if _, ok := byName["unknown"]; !ok {
		t.Errorf("empty model should be grouped as 'unknown'; got %+v", models)
	}
	// Ordered by total cost DESC: opus (8.0) first.
	if models[0].Model != "claude-opus-4" {
		t.Errorf("first model = %q, want claude-opus-4", models[0].Model)
	}
}

func TestTimeline(t *testing.T) {
	d := openTestDB(t)
	today := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().UTC().AddDate(0, 0, -100).Format(time.RFC3339)
	d.insertSession(t, "recent1", "/a", "claude-opus-4", 0, 0, 0, 0, 2.0, today)
	d.insertSession(t, "recent2", "/b", "claude-opus-4", 0, 0, 0, 0, 3.0, today)
	d.insertSession(t, "ancient", "/c", "claude-opus-4", 0, 0, 0, 0, 9.0, old)

	buckets, err := d.Timeline(30)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("got %d buckets in 30d window, want 1 (ancient excluded)", len(buckets))
	}
	b := buckets[0]
	if b.Sessions != 2 {
		t.Errorf("today sessions = %d, want 2", b.Sessions)
	}
	if b.CostUSD != 5.0 {
		t.Errorf("today cost = %v, want 5.0", b.CostUSD)
	}
}
