package gate

import "testing"

// The session a slot, a queue entry, a decision and a wwd job are recorded for: PDX_SESSION_ID (a script that names the session on
// purpose), else CLAUDE_CODE_SESSION_ID (#2945, what Claude Code sets), else the older CLAUDE_SESSION_ID. One reading, used by the gate and
// by `pdx test` / `pdx jobs` (#3477).
func TestSessionFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, c := range map[string]struct {
		env  map[string]string
		want string
	}{
		"the Claude Code name":             {map[string]string{"CLAUDE_CODE_SESSION_ID": "new"}, "new"},
		"the older name":                   {map[string]string{"CLAUDE_SESSION_ID": "old"}, "old"},
		"both: the new one wins":           {map[string]string{"CLAUDE_CODE_SESSION_ID": "new", "CLAUDE_SESSION_ID": "old"}, "new"},
		"an empty new one falls back":      {map[string]string{"CLAUDE_CODE_SESSION_ID": "", "CLAUDE_SESSION_ID": "old"}, "old"},
		"the explicit one wins over both":  {map[string]string{"PDX_SESSION_ID": "named", "CLAUDE_CODE_SESSION_ID": "new", "CLAUDE_SESSION_ID": "old"}, "named"},
		"the explicit one alone":           {map[string]string{"PDX_SESSION_ID": "named"}, "named"},
		"an empty explicit one falls back": {map[string]string{"PDX_SESSION_ID": "", "CLAUDE_CODE_SESSION_ID": "new"}, "new"},
		"neither":                          {map[string]string{}, ""},
		"all three empty":                  {map[string]string{"PDX_SESSION_ID": "", "CLAUDE_CODE_SESSION_ID": "", "CLAUDE_SESSION_ID": ""}, ""},
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
