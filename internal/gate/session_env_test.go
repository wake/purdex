package gate

import "testing"

// The session a slot, a queue entry, a decision and a wwd job are recorded for comes from CLAUDE_CODE_SESSION_ID (#2945), then the older
// CLAUDE_SESSION_ID. `pdx test` and `pdx jobs` read the session through this same function (#3477).
func TestSessionFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"the Claude Code name":        {map[string]string{"CLAUDE_CODE_SESSION_ID": "new"}, "new"},
		"the older name":              {map[string]string{"CLAUDE_SESSION_ID": "old"}, "old"},
		"both: the new one wins":      {map[string]string{"CLAUDE_CODE_SESSION_ID": "new", "CLAUDE_SESSION_ID": "old"}, "new"},
		"an empty new one falls back": {map[string]string{"CLAUDE_CODE_SESSION_ID": "", "CLAUDE_SESSION_ID": "old"}, "old"},
		"PDX_SESSION_ID is not read":  {map[string]string{"PDX_SESSION_ID": "named"}, ""},
		"neither":                     {map[string]string{}, ""},
	} {
		if got := SessionFromEnv(env(c.env)); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// New reads the real environment through it.
func TestNew_TakesTheSessionFromTheEnvironment(t *testing.T) {
	t.Setenv("PDX_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "from-cc")
	t.Setenv("CLAUDE_SESSION_ID", "from-old")
	if g := New(Defaults()); g.Session != "from-cc" {
		t.Fatalf("Session = %q", g.Session)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if g := New(Defaults()); g.Session != "from-old" {
		t.Fatalf("Session = %q", g.Session)
	}
}

// A PDX_SESSION_ID left in a person's shell must not take the place of the real Claude session of the commands that inherit it: the gate
// records its slots, queue entries and decisions by the Claude session only (#3477 review).
func TestNew_IgnoresPDXSessionID(t *testing.T) {
	t.Setenv("PDX_SESSION_ID", "left-over-in-the-shell")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "the-real-session")
	t.Setenv("CLAUDE_SESSION_ID", "")
	if g := New(Defaults()); g.Session != "the-real-session" {
		t.Fatalf("Session = %q, the gate must not take PDX_SESSION_ID", g.Session)
	}
}
