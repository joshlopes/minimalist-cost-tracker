package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lendable/minimalist-cost-tracker/internal/db"
	"github.com/lendable/minimalist-cost-tracker/internal/hook"
	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
	"github.com/lendable/minimalist-cost-tracker/internal/recorder"
	"github.com/lendable/minimalist-cost-tracker/internal/transcript"
)

func setup(t *testing.T) http.Handler {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	rec := recorder.New(d, "default")
	pricer := pricing.New()
	// Seed: one finalised sonnet session that used the "pr" skill.
	for _, p := range []string{
		`{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/tmp/a","tool_name":"Skill","tool_use_id":"t1","tool_input":{"skill":"pr"}}`,
		`{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash"}`,
	} {
		if err := hook.Handle(strings.NewReader(p), rec, pricer); err != nil {
			t.Fatal(err)
		}
	}
	// Finalise s1 directly via the recorder with a known summary.
	const model = "claude-sonnet-4-6"
	summary := transcript.SessionSummary{
		Model:        model,
		InputTokens:  1000,
		OutputTokens: 500,
		CostUSD:      pricer.CostUSD(model, 1000, 500, 0, 0),
		StartedAt:    time.Now().UTC(),
		EndedAt:      time.Now().UTC(),
	}
	if err := rec.FinaliseSession("s1", summary); err != nil {
		t.Fatal(err)
	}

	return New(d, pricer, 0).Handler()
}

func get(t *testing.T, h http.Handler, path string, out any) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	res := rr.Result()
	if out != nil && res.StatusCode == http.StatusOK {
		if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("%s Content-Type = %q, want json", path, ct)
		}
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("%s decode: %v", path, err)
		}
	}
	return res
}

func TestAPIEndpoints(t *testing.T) {
	h := setup(t)

	var stats db.StatsRow
	get(t, h, "/api/stats", &stats)
	if stats.TotalSessions != 1 || stats.TotalCostUSD <= 0 {
		t.Errorf("stats = %+v", stats)
	}

	var sessions []db.SessionRow
	get(t, h, "/api/sessions?sort=cost", &sessions)
	if len(sessions) != 1 || len(sessions[0].Skills) != 1 || sessions[0].Skills[0] != "pr" {
		t.Errorf("sessions = %+v", sessions)
	}

	var skills []db.SkillStatRow
	get(t, h, "/api/skills", &skills)
	if len(skills) != 1 || skills[0].SkillName != "pr" || skills[0].TotalCostUSD <= 0 {
		t.Errorf("skills = %+v", skills)
	}

	var models []db.ModelStatRow
	get(t, h, "/api/models", &models)
	if len(models) != 1 || models[0].Model != "claude-sonnet-4" {
		t.Errorf("models = %+v (want normalised family claude-sonnet-4)", models)
	}

	var timeline []db.DayBucketRow
	get(t, h, "/api/timeline?days=30", &timeline)
	if len(timeline) != 1 {
		t.Errorf("timeline = %+v, want 1 bucket", timeline)
	}

	var detail db.SessionDetail
	get(t, h, "/api/sessions/s1", &detail)
	if detail.ID != "s1" || len(detail.SkillEvents) != 1 {
		t.Errorf("detail = %+v", detail)
	}
}

func TestIndexAndNotFound(t *testing.T) {
	h := setup(t)

	res := get(t, h, "/", nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("/ status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("/ Content-Type = %q", ct)
	}

	if res := get(t, h, "/nope", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("/nope status = %d, want 404", res.StatusCode)
	}
	if res := get(t, h, "/api/sessions/does-not-exist", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown session status = %d, want 404", res.StatusCode)
	}
}
