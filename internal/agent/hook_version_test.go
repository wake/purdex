package agent

import "testing"

func TestExtractHookAgentVersion(t *testing.T) {
	cases := map[string]string{
		"codex-cli 0.121.0":     "0.121.0",
		"2.1.114 (Claude Code)": "2.1.114",
		"v1.2.3-beta.1 release": "1.2.3",
		"no version here":       "",
	}

	for input, want := range cases {
		if got := ExtractHookAgentVersion(input); got != want {
			t.Fatalf("ExtractHookAgentVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompareHookAgentVersions(t *testing.T) {
	cases := []struct {
		a    string
		b    string
		want int
	}{
		{a: "0.121.0", b: "0.121.0", want: 0},
		{a: "0.122.0", b: "0.121.0", want: 1},
		{a: "0.121.0", b: "0.122.0", want: -1},
		{a: "2.1.114", b: "2.1.99", want: 1},
		{a: "2.1", b: "2.1.0", want: 0},
		{a: "", b: "2.1.0", want: -1},
	}

	for _, tc := range cases {
		if got := CompareHookAgentVersions(tc.a, tc.b); got != tc.want {
			t.Fatalf("CompareHookAgentVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
