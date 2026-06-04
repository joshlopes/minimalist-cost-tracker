// Package recorder centralises every write to the database. The hook binary
// drives it; the serve binary never writes.
package recorder

import (
	"time"

	"github.com/lendable/minimalist-cost-tracker/internal/db"
	"github.com/lendable/minimalist-cost-tracker/internal/transcript"
)

type Recorder struct {
	db      *db.DB
	profile string
}

// New builds a recorder that attributes every session it creates to profile
// (the Claude Code config the hook was installed under). An empty profile
// defaults to "default".
func New(database *db.DB, profile string) *Recorder {
	if profile == "" {
		profile = "default"
	}
	return &Recorder{db: database, profile: profile}
}

// EnsureSession inserts the session if it does not already exist, stamping a
// started_at so partial (never-finalised) sessions still carry a timestamp, and
// recording the recorder's profile. Existing rows are left untouched.
func (r *Recorder) EnsureSession(sessionID, cwd string) error {
	_, err := r.db.Exec(
		`INSERT OR IGNORE INTO sessions (id, cwd, profile, started_at)
		 VALUES (?, ?, ?, datetime('now'))`,
		sessionID, cwd, r.profile,
	)
	return err
}

// InsertSkillEvent records one Skill invocation.
func (r *Recorder) InsertSkillEvent(sessionID, skill, toolUseID string) error {
	_, err := r.db.Exec(
		`INSERT INTO skill_events (session_id, skill_name, tool_use_id)
		 VALUES (?, ?, ?)`,
		sessionID, skill, nullIfEmpty(toolUseID),
	)
	return err
}

// InsertToolEvent records one tool invocation of any kind.
func (r *Recorder) InsertToolEvent(sessionID, toolName, toolUseID string) error {
	_, err := r.db.Exec(
		`INSERT INTO tool_events (session_id, tool_name, tool_use_id)
		 VALUES (?, ?, ?)`,
		sessionID, toolName, nullIfEmpty(toolUseID),
	)
	return err
}

// FinaliseSession applies the parsed transcript summary to a session. ended_at
// defaults to now when the transcript carried no timestamps; started_at is only
// overwritten when the summary provides one (otherwise the EnsureSession stamp
// is preserved).
func (r *Recorder) FinaliseSession(sessionID string, s transcript.SessionSummary) error {
	ended := s.EndedAt
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	var started interface{}
	if !s.StartedAt.IsZero() {
		started = s.StartedAt.UTC().Format(time.RFC3339)
	}
	_, err := r.db.Exec(`
		UPDATE sessions SET
			model              = ?,
			input_tokens       = ?,
			output_tokens      = ?,
			cache_read_tokens  = ?,
			cache_write_tokens = ?,
			cost_usd           = ?,
			ended_at           = ?,
			started_at         = COALESCE(?, started_at)
		WHERE id = ?`,
		nullIfEmpty(s.Model),
		s.InputTokens, s.OutputTokens, s.CacheReadTokens, s.CacheWriteTokens,
		s.CostUSD,
		ended.UTC().Format(time.RFC3339),
		started,
		sessionID,
	)
	return err
}

// SetTranscriptPath stores the transcript file location for a session.
func (r *Recorder) SetTranscriptPath(sessionID, path string) error {
	if path == "" {
		return nil
	}
	_, err := r.db.Exec(`UPDATE sessions SET transcript_path = ? WHERE id = ?`, path, sessionID)
	return err
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
