// Package transcript parses a Claude Code JSONL transcript into a token/cost
// summary. Claude Code's transcript format is undocumented, so parsing is
// deliberately lenient: unknown or missing fields are ignored rather than
// treated as errors, and a malformed line never aborts the whole file.
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"time"

	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
)

// SessionSummary is the aggregate extracted from a transcript.
type SessionSummary struct {
	Model            string
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	CostUSD          float64
	StartedAt        time.Time
	EndedAt          time.Time
	SkillsObserved   []string
}

// usage mirrors the token counters Anthropic emits on assistant messages.
type usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// message is the inner Claude message object carried by a transcript line.
type message struct {
	Model   string          `json:"model"`
	Role    string          `json:"role"`
	Usage   *usage          `json:"usage"`
	Content json.RawMessage `json:"content"`
}

// line is one JSONL record. We tolerate usage appearing either nested under
// "message" or at the top level.
type line struct {
	Type      string   `json:"type"`
	Timestamp string   `json:"timestamp"`
	Model     string   `json:"model"`
	Usage     *usage   `json:"usage"`
	Message   *message `json:"message"`
}

// contentBlock is one entry of an assistant message's content array.
type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Parse reads the transcript at path, accumulates token usage across all
// assistant messages, collects skill invocations, and computes cost via pricer.
func Parse(path string, pricer *pricing.Pricer) (SessionSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionSummary{}, err
	}
	defer f.Close()

	var s SessionSummary
	skills := map[string]bool{}

	sc := bufio.NewScanner(f)
	// Transcript lines can be large (full tool outputs); raise the buffer cap.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ln line
		if err := json.Unmarshal(raw, &ln); err != nil {
			continue // skip malformed lines, keep going
		}

		u := ln.Usage
		model := ln.Model
		var content json.RawMessage
		if ln.Message != nil {
			if ln.Message.Usage != nil {
				u = ln.Message.Usage
			}
			if model == "" {
				model = ln.Message.Model
			}
			content = ln.Message.Content
		}

		if u != nil {
			s.InputTokens += u.InputTokens
			s.OutputTokens += u.OutputTokens
			s.CacheReadTokens += u.CacheReadInputTokens
			s.CacheWriteTokens += u.CacheCreationInputTokens
		}
		if s.Model == "" && model != "" {
			s.Model = model
		}

		if t := parseTime(ln.Timestamp); !t.IsZero() {
			if s.StartedAt.IsZero() || t.Before(s.StartedAt) {
				s.StartedAt = t
			}
			if t.After(s.EndedAt) {
				s.EndedAt = t
			}
		}

		for _, name := range skillsFromContent(content) {
			skills[name] = true
		}
	}
	if err := sc.Err(); err != nil {
		return s, err
	}

	for name := range skills {
		s.SkillsObserved = append(s.SkillsObserved, name)
	}
	// Fall back to file mtime when no message timestamps were present.
	if s.EndedAt.IsZero() {
		if fi, err := f.Stat(); err == nil {
			s.EndedAt = fi.ModTime().UTC()
		}
	}

	s.CostUSD = pricer.CostUSD(s.Model, s.InputTokens, s.OutputTokens, s.CacheReadTokens, s.CacheWriteTokens)
	return s, nil
}

// skillsFromContent extracts skill names from tool_use blocks where the tool is
// "Skill". The input key is tried as "skill" then "name" (see spec open
// question on the exact field name).
func skillsFromContent(content json.RawMessage) []string {
	if len(content) == 0 {
		return nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type != "tool_use" || b.Name != "Skill" {
			continue
		}
		if name := SkillNameFromInput(b.Input); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// SkillInput is the expected shape of tool_input when tool_name == "Skill".
type SkillInput struct {
	Skill string `json:"skill"`
	Name  string `json:"name"`
}

// SkillNameFromInput pulls the skill identifier out of a tool_use / tool_input
// blob, trying the "skill" key first and "name" as a fallback. Shared by the
// transcript parser and the live PostToolUse hook handler.
func SkillNameFromInput(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var si SkillInput
	if err := json.Unmarshal(input, &si); err != nil {
		return ""
	}
	if si.Skill != "" {
		return si.Skill
	}
	return si.Name
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
