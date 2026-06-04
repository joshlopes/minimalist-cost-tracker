package pricing

import (
	"math"
	"testing"
)

func TestNormalize(t *testing.T) {
	p := New()
	cases := map[string]string{
		"claude-opus-4-8":   "claude-opus-4",
		"claude-sonnet-4-6": "claude-sonnet-4",
		"claude-sonnet-4-5": "claude-sonnet-4",
		"claude-haiku-4-5":  "claude-haiku-4",
		"claude-opus-4":     "claude-opus-4",
		"CLAUDE-SONNET-4-6": "claude-sonnet-4",
		"gpt-4o":            "gpt-4o", // unknown family returned lower-cased
		"":                  "",
	}
	for in, want := range cases {
		if got := p.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelRate(t *testing.T) {
	p := New()
	if _, ok := p.ModelRate("claude-sonnet-4-6"); !ok {
		t.Error("expected sonnet-4-6 to resolve to a known rate")
	}
	if _, ok := p.ModelRate("some-unknown-model"); ok {
		t.Error("expected unknown model to be unresolved")
	}
}

func TestCostUSD(t *testing.T) {
	p := New()
	// sonnet rates: in 3, out 15, cacheRead 0.30, cacheWrite 3.75 per 1M.
	// 1M input + 1M output = 3 + 15 = 18.
	got := p.CostUSD("claude-sonnet-4-6", 1_000_000, 1_000_000, 0, 0)
	if math.Abs(got-18.0) > 1e-9 {
		t.Errorf("CostUSD = %v, want 18.0", got)
	}
	// cache tokens: 1M cacheRead + 1M cacheWrite = 0.30 + 3.75 = 4.05.
	got = p.CostUSD("claude-sonnet-4-6", 0, 0, 1_000_000, 1_000_000)
	if math.Abs(got-4.05) > 1e-9 {
		t.Errorf("CostUSD(cache) = %v, want 4.05", got)
	}
	// unknown model => 0.
	if got := p.CostUSD("unknown", 1_000_000, 1_000_000, 0, 0); got != 0 {
		t.Errorf("CostUSD(unknown) = %v, want 0", got)
	}
}
