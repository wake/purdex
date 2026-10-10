package ccuds

import "testing"

// VerifiedCCVersion is the byte-layout measure and the version the proxy helpers impersonate: it stays where the layouts
// were measured. SelftestPassedCCVersion is how far a passing `pdx msg selftest` has been seen to work; only the warning
// threshold follows it (#2387).
func TestVerifiedCCVersion(t *testing.T) {
	if VerifiedCCVersion != "2.1.270" {
		t.Fatalf("VerifiedCCVersion = %q", VerifiedCCVersion)
	}
	if SelftestPassedCCVersion != "2.1.296" {
		t.Fatalf("SelftestPassedCCVersion = %q", SelftestPassedCCVersion)
	}
}

func TestNewerThanVerified(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"2.1.296", false},
		{"2.1.294", false},
		{"2.1.270", false},
		{"2.1.269", false},
		{"2.0.999", false},
		{"1.9.9", false},
		{"2.1.297", true},
		{"2.2.0", true},
		{"2.2", true},
		{"3", true},
		{"2.1.296.1", true},
		{"2.1.296.0", false},
		{"2.1", false},
		{"", false},
		{"abc", false},
		{"2.1.296-beta", false},
		{"v2.1.297", false},
		{"2..1", false},
		{"2.1.296 ", false},
	}
	for _, c := range cases {
		if got := NewerThanVerified(c.v); got != c.want {
			t.Errorf("NewerThanVerified(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}
