// Package hook handles a single Claude Code hook event read from stdin. It is
// invoked once per tool call and once on session stop. Handle never returns a
// fatal error to the caller's exit code path — every problem is logged and nil
// is returned so the hook always exits 0 and never disrupts Claude Code.
package hook

import (
	"encoding/json"
	"io"
	"log"

	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
	"github.com/lendable/minimalist-cost-tracker/internal/recorder"
	"github.com/lendable/minimalist-cost-tracker/internal/transcript"
)

// HookPayload is the JSON Claude Code writes to the hook's stdin. Fields are a
// superset across event types; only some are populated per event.
type HookPayload struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	TranscriptPath string          `json:"transcript_path"`
	Reason         string          `json:"reason"`
}

// loggedSkillSample ensures we only dump a raw Skill payload once per process,
// for the spec's "confirm the tool_input field name" debugging step.
var loggedSkillSample bool

// Handle parses one hook payload and routes it to the recorder. It always
// returns nil; callers should ignore the (always-nil) error and exit 0.
func Handle(r io.Reader, rec *recorder.Recorder, pricer *pricing.Pricer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		log.Printf("hook: read stdin: %v", err)
		return nil
	}
	if len(data) == 0 {
		return nil
	}

	var p HookPayload
	if err := json.Unmarshal(data, &p); err != nil {
		log.Printf("hook: unmarshal payload: %v; raw=%s", err, truncate(data))
		return nil
	}
	if p.SessionID == "" {
		log.Printf("hook: missing session_id; raw=%s", truncate(data))
		return nil
	}

	switch p.HookEventName {
	case "Stop":
		handleStop(p, rec, pricer)
	case "PostToolUse":
		handlePostToolUse(p, rec)
	default:
		// EnsureSession anyway so we never miss a session id.
		logErr("ensure session (unknown event)", rec.EnsureSession(p.SessionID, p.Cwd))
	}
	return nil
}

func handlePostToolUse(p HookPayload, rec *recorder.Recorder) {
	logErr("ensure session", rec.EnsureSession(p.SessionID, p.Cwd))

	if p.ToolName == "Skill" {
		if !loggedSkillSample {
			loggedSkillSample = true
			log.Printf("hook: first Skill tool_input sample (confirm field name): %s", truncate(p.ToolInput))
		}
		skill := transcript.SkillNameFromInput(p.ToolInput)
		if skill == "" {
			skill = "unknown"
			log.Printf("hook: could not extract skill name from tool_input: %s", truncate(p.ToolInput))
		}
		logErr("insert skill event", rec.InsertSkillEvent(p.SessionID, skill, p.ToolUseID))
	}

	if p.ToolName != "" {
		logErr("insert tool event", rec.InsertToolEvent(p.SessionID, p.ToolName, p.ToolUseID))
	}
}

func handleStop(p HookPayload, rec *recorder.Recorder, pricer *pricing.Pricer) {
	logErr("ensure session", rec.EnsureSession(p.SessionID, p.Cwd))
	if p.TranscriptPath == "" {
		log.Printf("hook: Stop event without transcript_path for session %s", p.SessionID)
		return
	}
	logErr("set transcript path", rec.SetTranscriptPath(p.SessionID, p.TranscriptPath))

	summary, err := transcript.Parse(p.TranscriptPath, pricer)
	if err != nil {
		log.Printf("hook: parse transcript %q: %v", p.TranscriptPath, err)
		return
	}
	logErr("finalise session", rec.FinaliseSession(p.SessionID, summary))
}

func logErr(what string, err error) {
	if err != nil {
		log.Printf("hook: %s: %v", what, err)
	}
}

func truncate(b []byte) string {
	const max = 1024
	if len(b) > max {
		return string(b[:max]) + "...(truncated)"
	}
	return string(b)
}
