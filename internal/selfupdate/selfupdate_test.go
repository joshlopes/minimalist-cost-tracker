package selfupdate

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v1.2.0", "v1.3.0", true},
		{"1.2.0", "v1.2.1", true},
		{"v1.2.0", "v2.0.0", true},
		{"v1.2.0", "v1.2.0", false},     // equal
		{"v1.3.0", "v1.2.9", false},     // current ahead
		{"v1.2", "v1.2.1", true},        // missing patch defaults to 0
		{"v1.2.0", "v1.2.0-rc1", false}, // pre-release suffix ignored, equal core
		{"dev", "v1.2.0", false},        // dev build is never "outdated"
		{"v1.2.0", "", false},           // no latest known
		{"", "v1.2.0", false},           // no current known
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}
