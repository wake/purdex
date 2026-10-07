package peers

import (
	"strings"
	"testing"
)

func TestVirtualName(t *testing.T) {
	long100 := strings.Repeat("a", 100)
	// 60 letters then a '-' at index 60: cut to 61 the base ends in '-',
	// which must be trimmed before the suffix goes on.
	dashAtCut := strings.Repeat("b", 60) + "-cdef"
	cases := []struct {
		base, ref, want string
		ok              bool
	}{
		{"purdex-54", "_k3m9qz", "purdex-54-k3", true},
		{"a", "_k3m9qz", "a-k3", true},
		// Every virtual name carries a '-', so none can be 6 base36 digits:
		// "q3-k3m" style collisions with a ref are impossible.
		{"q3m", "_k3m9qz", "q3m-k3", true},
		// A base that is itself ref-shaped (unroutable as a name) still makes
		// a routable virtual name.
		{"q34psn", "_k3m9qz", "q34psn-k3", true},
		{long100, "_k3m9qz", strings.Repeat("a", 61) + "-k3", true},
		{dashAtCut, "_k3m9qz", strings.Repeat("b", 60) + "-k3", true},
		{strings.Repeat("c", 61), "_0a0000", strings.Repeat("c", 61) + "-0a", true},
		// Shape failures: checked on the base before any truncation.
		{"", "_k3m9qz", "", false},
		{"-ab", "_k3m9qz", "", false},
		{"Purdex", "_k3m9qz", "", false},
		{"a b", "_k3m9qz", "", false},
		{"a_b", "_k3m9qz", "", false},
		{"a/b", "_k3m9qz", "", false},
		{"專案", "_k3m9qz", "", false},
		{strings.Repeat("a", 100) + "_", "_k3m9qz", "", false},
		// The ref must be a ref.
		{"purdex", "k3m9qz", "", false},
		{"purdex", "", "", false},
		{"purdex", "_K3M9QZ", "", false},
	}
	for _, c := range cases {
		got, ok := VirtualName(c.base, c.ref)
		if got != c.want || ok != c.ok {
			t.Errorf("VirtualName(%q, %q) = %q, %v; want %q, %v", c.base, c.ref, got, ok, c.want, c.ok)
		}
		if ok && (!RoutableName(got) || len(got) > 64) {
			t.Errorf("VirtualName(%q, %q) = %q is not routable", c.base, c.ref, got)
		}
	}
}

func TestNormalizeBase(t *testing.T) {
	cases := map[string]string{
		"purdex":          "purdex",
		"My Project.v2":   "my-project-v2",
		"--a__b--":        "a-b",
		"專案":              "",
		"work/專案-x":       "work-x",
		"UPPER_case":      "upper-case",
		"":                "",
		"a   b":           "a-b",
		"agent-a8125ffea": "agent-a8125ffea",
	}
	for in, want := range cases {
		if got := NormalizeBase(in); got != want {
			t.Errorf("NormalizeBase(%q) = %q, want %q", in, got, want)
		}
	}
}
