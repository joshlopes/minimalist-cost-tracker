// Package db owns the SQLite connection, schema migration, and all read
// queries used by the dashboard. Writes live in the recorder package.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// DB wraps *sql.DB so we can hang typed query methods off it.
type DB struct {
	*sql.DB
}

// Open opens (creating if needed) the SQLite database at path with WAL mode
// and a busy timeout, so the hook writer and the serve reader can coexist.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
		path,
	)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &DB{sqlDB}, nil
}

// Migrate executes the embedded schema. It is idempotent (every statement is
// CREATE ... IF NOT EXISTS), so it is safe to run on every startup.
func (d *DB) Migrate() error {
	if _, err := d.Exec(schemaSQL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// ---- read models (JSON-tagged so the web layer can marshal them directly) ----

type StatsRow struct {
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalSessions     int     `json:"total_sessions"`
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
	AvgCostPerSession float64 `json:"avg_cost_per_session"`
}

type SessionRow struct {
	ID               string   `json:"id"`
	Cwd              string   `json:"cwd"`
	Model            string   `json:"model"`
	StartedAt        *string  `json:"started_at"`
	EndedAt          *string  `json:"ended_at"`
	InputTokens      int      `json:"input_tokens"`
	OutputTokens     int      `json:"output_tokens"`
	CacheReadTokens  int      `json:"cache_read_tokens"`
	CacheWriteTokens int      `json:"cache_write_tokens"`
	CostUSD          float64  `json:"cost_usd"`
	Skills           []string `json:"skills"`
}

type EventRow struct {
	Name       string `json:"name"`
	ToolUseID  string `json:"tool_use_id"`
	OccurredAt string `json:"occurred_at"`
}

type SessionDetail struct {
	SessionRow
	SkillEvents []EventRow `json:"skill_events"`
	ToolEvents  []EventRow `json:"tool_events"`
}

type SkillStatRow struct {
	SkillName    string  `json:"skill_name"`
	UsageCount   int     `json:"usage_count"`
	SessionCount int     `json:"session_count"`
	AvgCostUSD   float64 `json:"avg_cost_usd"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

type ModelStatRow struct {
	Model        string  `json:"model"`
	SessionCount int     `json:"session_count"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	TotalInput   int     `json:"total_input_tokens"`
	TotalOutput  int     `json:"total_output_tokens"`
}

type DayBucketRow struct {
	Date     string  `json:"date"`
	CostUSD  float64 `json:"cost_usd"`
	Sessions int     `json:"sessions"`
}

// Stats returns aggregate totals across all sessions.
func (d *DB) Stats() (StatsRow, error) {
	var s StatsRow
	row := d.QueryRow(`
		SELECT
			COALESCE(SUM(cost_usd), 0),
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0)
		FROM sessions`)
	if err := row.Scan(&s.TotalCostUSD, &s.TotalSessions, &s.TotalInputTokens, &s.TotalOutputTokens); err != nil {
		return s, err
	}
	if s.TotalSessions > 0 {
		s.AvgCostPerSession = s.TotalCostUSD / float64(s.TotalSessions)
	}
	return s, nil
}

// sessionSortClause whitelists the ORDER BY to avoid SQL injection via the
// sort query parameter.
func sessionSortClause(sortBy string) string {
	switch sortBy {
	case "date":
		return "COALESCE(ended_at, started_at) DESC"
	case "cost":
		fallthrough
	default:
		return "cost_usd DESC"
	}
}

// Sessions returns a page of sessions with their distinct skill names.
func (d *DB) Sessions(limit, offset int, sortBy string) ([]SessionRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := fmt.Sprintf(`
		SELECT s.id, s.cwd, s.model, s.started_at, s.ended_at,
		       s.input_tokens, s.output_tokens, s.cache_read_tokens,
		       s.cache_write_tokens, s.cost_usd,
		       (SELECT GROUP_CONCAT(DISTINCT skill_name)
		          FROM skill_events WHERE session_id = s.id) AS skills
		FROM sessions s
		ORDER BY %s
		LIMIT ? OFFSET ?`, sessionSortClause(sortBy))
	rows, err := d.Query(q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionRow
	for rows.Next() {
		r, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanSessionRow scans the common session columns (+ a concatenated skills
// column) shared by Sessions and SessionByID.
func scanSessionRow(rows *sql.Rows) (SessionRow, error) {
	var (
		r      SessionRow
		cwd    sql.NullString
		model  sql.NullString
		start  sql.NullString
		end    sql.NullString
		skills sql.NullString
	)
	if err := rows.Scan(&r.ID, &cwd, &model, &start, &end,
		&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens,
		&r.CacheWriteTokens, &r.CostUSD, &skills); err != nil {
		return r, err
	}
	r.Cwd = cwd.String
	r.Model = model.String
	r.StartedAt = nullToPtr(start)
	r.EndedAt = nullToPtr(end)
	r.Skills = splitConcat(skills)
	return r, nil
}

// SessionByID returns one session plus its skill and tool event lists.
func (d *DB) SessionByID(id string) (SessionDetail, error) {
	var detail SessionDetail
	rows, err := d.Query(`
		SELECT s.id, s.cwd, s.model, s.started_at, s.ended_at,
		       s.input_tokens, s.output_tokens, s.cache_read_tokens,
		       s.cache_write_tokens, s.cost_usd,
		       (SELECT GROUP_CONCAT(DISTINCT skill_name)
		          FROM skill_events WHERE session_id = s.id) AS skills
		FROM sessions s WHERE s.id = ?`, id)
	if err != nil {
		return detail, err
	}
	found := false
	if rows.Next() {
		found = true
		detail.SessionRow, err = scanSessionRow(rows)
	}
	rows.Close()
	if err != nil {
		return detail, err
	}
	if !found {
		return detail, sql.ErrNoRows
	}

	if detail.SkillEvents, err = d.events(`
		SELECT skill_name, tool_use_id, occurred_at
		FROM skill_events WHERE session_id = ? ORDER BY occurred_at`, id); err != nil {
		return detail, err
	}
	if detail.ToolEvents, err = d.events(`
		SELECT tool_name, tool_use_id, occurred_at
		FROM tool_events WHERE session_id = ? ORDER BY occurred_at`, id); err != nil {
		return detail, err
	}
	return detail, nil
}

func (d *DB) events(query, id string) ([]EventRow, error) {
	rows, err := d.Query(query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventRow{}
	for rows.Next() {
		var (
			e         EventRow
			toolUseID sql.NullString
		)
		if err := rows.Scan(&e.Name, &toolUseID, &e.OccurredAt); err != nil {
			return nil, err
		}
		e.ToolUseID = toolUseID.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// Skills aggregates per skill, attributing whole-session cost to every skill
// that appeared in that session (the session-level approximation documented in
// the spec). Cost is summed over distinct sessions, not raw events, so a skill
// used twice in one session does not double-count that session's cost.
func (d *DB) Skills() ([]SkillStatRow, error) {
	rows, err := d.Query(`
		SELECT skill_name,
		       SUM(uses)        AS usage_count,
		       COUNT(*)         AS session_count,
		       AVG(cost_usd)    AS avg_cost,
		       SUM(cost_usd)    AS total_cost
		FROM (
			SELECT se.skill_name, se.session_id,
			       COUNT(*)   AS uses,
			       s.cost_usd AS cost_usd
			FROM skill_events se
			JOIN sessions s ON s.id = se.session_id
			GROUP BY se.skill_name, se.session_id
		)
		GROUP BY skill_name
		ORDER BY total_cost DESC, usage_count DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkillStatRow{}
	for rows.Next() {
		var r SkillStatRow
		if err := rows.Scan(&r.SkillName, &r.UsageCount, &r.SessionCount, &r.AvgCostUSD, &r.TotalCostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Models aggregates per raw model string. Family normalisation (for the
// breakdown chart) is applied by the web layer, which holds the pricer.
func (d *DB) Models() ([]ModelStatRow, error) {
	rows, err := d.Query(`
		SELECT COALESCE(NULLIF(model, ''), 'unknown') AS model,
		       COUNT(*)                       AS session_count,
		       COALESCE(SUM(cost_usd), 0)     AS total_cost,
		       COALESCE(SUM(input_tokens), 0) AS total_input,
		       COALESCE(SUM(output_tokens), 0) AS total_output
		FROM sessions
		GROUP BY model
		ORDER BY total_cost DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelStatRow{}
	for rows.Next() {
		var r ModelStatRow
		if err := rows.Scan(&r.Model, &r.SessionCount, &r.TotalCostUSD, &r.TotalInput, &r.TotalOutput); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Timeline returns daily cost buckets for the trailing window of days.
func (d *DB) Timeline(days int) ([]DayBucketRow, error) {
	if days <= 0 {
		days = 30
	}
	modifier := fmt.Sprintf("-%d days", days)
	rows, err := d.Query(`
		SELECT date(COALESCE(ended_at, started_at)) AS day,
		       COALESCE(SUM(cost_usd), 0)           AS cost,
		       COUNT(*)                             AS sessions
		FROM sessions
		WHERE COALESCE(ended_at, started_at) IS NOT NULL
		  AND date(COALESCE(ended_at, started_at)) >= date('now', ?)
		GROUP BY day
		ORDER BY day`, modifier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayBucketRow{}
	for rows.Next() {
		var r DayBucketRow
		if err := rows.Scan(&r.Date, &r.CostUSD, &r.Sessions); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullToPtr(ns sql.NullString) *string {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	s := ns.String
	return &s
}

func splitConcat(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return []string{}
	}
	return strings.Split(ns.String, ",")
}
