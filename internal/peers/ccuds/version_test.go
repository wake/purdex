package ccuds

import "testing"

func TestVerifiedCCVersion(t *testing.T) {
	if VerifiedCCVersion != "2.1.270" {
		t.Fatalf("VerifiedCCVersion = %q", VerifiedCCVersion)
	}
}

func TestNewerThanVerified(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"2.1.270", false},
		{"2.1.269", false},
		{"2.0.999", false},
		{"1.9.9", false},
		{"2.1.271", true},
		{"2.2.0", true},
		{"2.2", true},
		{"3", true},
		{"2.1.270.1", true},
		{"2.1.270.0", false},
		{"2.1", false},
		{"", false},
		{"abc", false},
		{"2.1.270-beta", false},
		{"v2.1.271", false},
		{"2..1", false},
		{"2.1.270 ", false},
	}
	for _, c := range cases {
		if got := NewerThanVerified(c.v); got != c.want {
			t.Errorf("NewerThanVerified(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}
