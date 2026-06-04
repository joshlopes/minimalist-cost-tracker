// Package pricing maps Claude model identifiers to token rates and computes
// the dollar cost of a session. Rates are hardcoded; update the table here when
// Anthropic changes prices.
package pricing

import (
	"strings"
)

// Rate holds USD cost per 1,000,000 tokens for each token class.
type Rate struct {
	InputPer1M      float64
	OutputPer1M     float64
	CacheReadPer1M  float64
	CacheWritePer1M float64
}

// Pricer resolves a (possibly versioned) model id to a Rate via family
// normalisation.
type Pricer struct {
	rates map[string]Rate
}

// New returns a Pricer loaded with the built-in rate table. Keys are family
// slugs as produced by Normalize.
func New() *Pricer {
	return &Pricer{rates: map[string]Rate{
		// Opus family — premium tier.
		"claude-opus-4": {InputPer1M: 15, OutputPer1M: 75, CacheReadPer1M: 1.5, CacheWritePer1M: 18.75},
		// Sonnet family — balanced tier (covers sonnet-4, -4-5, -4-6...).
		"claude-sonnet-4": {InputPer1M: 3, OutputPer1M: 15, CacheReadPer1M: 0.3, CacheWritePer1M: 3.75},
		// Haiku family — fast/cheap tier.
		"claude-haiku-4": {InputPer1M: 1, OutputPer1M: 5, CacheReadPer1M: 0.1, CacheWritePer1M: 1.25},
	}}
}

// Normalize collapses a versioned model id to its family slug, e.g.
// "claude-sonnet-4-6" -> "claude-sonnet-4". Unknown shapes are lower-cased and
// returned unchanged so they remain visible (rather than silently merged).
func (p *Pricer) Normalize(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	var family string
	switch {
	case strings.Contains(m, "opus"):
		family = "opus"
	case strings.Contains(m, "sonnet"):
		family = "sonnet"
	case strings.Contains(m, "haiku"):
		family = "haiku"
	default:
		return m
	}
	major := majorVersion(m, family)
	if major == "" {
		return "claude-" + family
	}
	return "claude-" + family + "-" + major
}

// majorVersion returns the first run of digits appearing after the family
// keyword, e.g. ("claude-opus-4-8", "opus") -> "4".
func majorVersion(m, family string) string {
	idx := strings.Index(m, family)
	if idx < 0 {
		return ""
	}
	rest := m[idx+len(family):]
	var b strings.Builder
	started := false
	for _, r := range rest {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			started = true
		} else if started {
			break
		}
	}
	return b.String()
}

// ModelRate returns the Rate for a model and whether it was found in the table.
func (p *Pricer) ModelRate(model string) (Rate, bool) {
	r, ok := p.rates[p.Normalize(model)]
	return r, ok
}

// CostUSD computes the total dollar cost for the given token counts. Unknown
// models yield 0 (the caller is expected to log a warning).
func (p *Pricer) CostUSD(model string, in, out, cacheRead, cacheWrite int) float64 {
	r, ok := p.ModelRate(model)
	if !ok {
		return 0
	}
	const perToken = 1.0 / 1_000_000.0
	return perToken * (float64(in)*r.InputPer1M +
		float64(out)*r.OutputPer1M +
		float64(cacheRead)*r.CacheReadPer1M +
		float64(cacheWrite)*r.CacheWritePer1M)
}
