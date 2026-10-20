package labels

import (
	"slices"
	"testing"
)

func TestQualifyKeyRoundTrips(t *testing.T) {
	for _, role := range []string{RoleCandidate, RoleReference} {
		c, own, r, ok := ParseQualifyKey(QualifyKey("mserv", "task:purdex/tsc", role))
		if !ok || c != "mserv" || own != "task:purdex/tsc" || r != role {
			t.Errorf("%s: %q %q %q %v", role, c, own, r, ok)
		}
	}
	for _, bad := range []string{"", "qualify:", "qualify:a", "qualify:a:b", "qualify::task:x/y:candidate", "qualify:a:task:x/y:other", "wwd:a:task:x/y:candidate"} {
		if _, _, _, ok := ParseQualifyKey(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestQualifyHalfLabels(t *testing.T) {
	task := []string{"os:darwin", "arch:arm64", "tool:node", "task:purdex/tsc"}
	if got, want := QualifyHalfLabels(task, "task:purdex/tsc", RoleCandidate, "mserv"), []string{"tool:node", "host:mserv"}; !slices.Equal(got, want) {
		t.Errorf("candidate = %v, want %v", got, want)
	}
	if got, want := QualifyHalfLabels(task, "task:purdex/tsc", RoleReference, "air26"), append(slices.Clone(task), "host:air26"); !slices.Equal(got, want) {
		t.Errorf("reference = %v, want %v", got, want)
	}
}
