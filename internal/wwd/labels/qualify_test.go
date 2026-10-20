package labels

import (
	"slices"
	"testing"
)

func TestQualifyRoleOfAKey(t *testing.T) {
	// an alias may hold colons (wire.ValidAlias), so the key is compared, not split
	for _, host := range []string{"mserv", "rack:a", "a:task:purdex/tsc:candidate"} {
		for _, role := range []string{RoleCandidate, RoleReference} {
			if r, ok := QualifyRole(QualifyKey(host, "task:purdex/tsc", role), host, "task:purdex/tsc"); !ok || r != role {
				t.Errorf("%s %s: %q %v", host, role, r, ok)
			}
		}
	}
	key := QualifyKey("mserv", "task:purdex/tsc", RoleCandidate)
	for name, c := range map[string][2]string{"other host": {"air26", "task:purdex/tsc"}, "other label": {"mserv", "task:purdex/other"}} {
		if r, ok := QualifyRole(key, c[0], c[1]); ok {
			t.Errorf("%s: %q", name, r)
		}
	}
	for _, bad := range []string{"", "qualify:", "qualify:mserv:task:purdex/tsc:other", "wwd:mserv:task:purdex/tsc:candidate"} {
		if _, ok := QualifyRole(bad, "mserv", "task:purdex/tsc"); ok {
			t.Errorf("%q has a role", bad)
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
