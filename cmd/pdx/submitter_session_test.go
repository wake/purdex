package main

import (
	"strings"
	"testing"
)

// `pdx test` and `pdx jobs` name the caller's session as the gate does, with one addition of their own (#3477): a script may name the
// session on purpose with PDX_SESSION_ID. Nothing in the repo sets it, so reading only it left submitter.session empty on every job;
// Claude Code's CLAUDE_CODE_SESSION_ID is what is there. A value that does not look like a session id is not one (it is kept as the job's
// identity for good, and the completion notice finds the conversation by it): it counts as not set.
func TestSubmitterSession(t *testing.T) {
	const real = "6d0b7ae9-d77a-4a0f-9a13-dbc8ab81a8cc"
	set := func(t *testing.T, pdx, cc, old string) {
		t.Helper()
		t.Setenv("PDX_SESSION_ID", pdx)
		t.Setenv("CLAUDE_CODE_SESSION_ID", cc)
		t.Setenv("CLAUDE_SESSION_ID", old)
	}
	for name, c := range map[string]struct {
		pdx, cc, old string
		want         string
	}{
		"only CLAUDE_CODE_SESSION_ID: that is the session": {"", real, "", real},
		"PDX_SESSION_ID wins when a script names it":       {"named-by-script", real, "", "named-by-script"},
		"the older CLAUDE_SESSION_ID still counts":         {"", "", "old-name-ok", "old-name-ok"},
		"none of them: empty":                              {"", "", "", ""},
		"only spaces is not a session":                     {"", "        ", "", ""},
		"a newline in it is not a session":                 {"", "abc\ndefghij", "", ""},
		"a control character is not a session":             {"", "abcdefgh\x1b[31m", "", ""},
		"too short is not a session":                       {"", "s1", "", ""},
		"too long is not a session":                        {"", strings.Repeat("a", 65), "", ""},
		"an invalid PDX_SESSION_ID falls to Claude's":      {"bad id!", real, "", real},
		"an invalid Claude one is empty, never the junk":   {"", "bad id!", "", ""},
		"an invalid Claude Code id falls to the older one": {"", "bad id!", "old-name-ok", "old-name-ok"},
	} {
		set(t, c.pdx, c.cc, c.old)
		if got := submitterSession(); got != c.want {
			t.Errorf("%s: session = %q, want %q", name, got, c.want)
		}
	}
}
