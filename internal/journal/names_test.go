package journal

import "testing"

func TestDisplayName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"purdex-3f-adam", "Adam"},
		{"mlab/purdex-3f-adam", "Adam"},
		{"purdex-88-b8", "purdex-88-b8"},
		{"mlab/purdex-88-b8", "purdex-88-b8"},
		{"a/b/purdex-3f-adam", "Adam"},
		{"mlab/", ""},
		{"", ""},
		{"adam", "adam"}, // no dash: not a virtual name with a pool suffix
		{"mlab/_q34psn", "_q34psn"},
	} {
		if got := DisplayName(c.in); got != c.want {
			t.Errorf("DisplayName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRewritePeerPrefixes(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"[mlab/purdex-3f-adam → mlab/purdex-88-b8] 請查 mlab/purdex-3f-adam", "[Adam → purdex-88-b8] 請查 mlab/purdex-3f-adam"},
		{"[report merged mlab/purdex-3f-adam] 好了", "[report merged Adam] 好了"},
		{"[report merged T-3] ok", "[report merged T-3] ok"},
		{"[pdx team] member 接力完成：a → b", "[pdx team] member 接力完成：a → b"},
		{"intro [mlab/purdex-3f-adam → x] not a prefix", "intro [mlab/purdex-3f-adam → x] not a prefix"},
		{"[a b → c] x", "[a b → c] x"},
		{"[mlab/purdex-3f-adam → mlab/purdex-88-b8] one\n[mlab/purdex-88-b8 → mlab/purdex-3f-adam] two", "[Adam → purdex-88-b8] one\n[purdex-88-b8 → Adam] two"},
		{"[unclosed mlab/purdex-3f-adam", "[unclosed mlab/purdex-3f-adam"},
		{"", ""},
	} {
		if got := RewritePeerPrefixes(c.in); got != c.want {
			t.Errorf("RewritePeerPrefixes(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}
