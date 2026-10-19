package main

import "testing"

// `pdx test` and `pdx jobs` name the caller's session by the same reading the gate uses (#3477): nothing in the repo sets PDX_SESSION_ID, so
// reading only it left submitter.session empty on every job. Claude Code's CLAUDE_CODE_SESSION_ID is what is there.
func TestSubmitterSession(t *testing.T) {
	clear := func() {
		t.Setenv("PDX_SESSION_ID", "")
		t.Setenv("CLAUDE_CODE_SESSION_ID", "")
		t.Setenv("CLAUDE_SESSION_ID", "")
	}
	t.Run("only CLAUDE_CODE_SESSION_ID: that is the session", func(t *testing.T) {
		clear()
		t.Setenv("CLAUDE_CODE_SESSION_ID", "6d0b7ae9-d77a-4a0f-9a13-dbc8ab81a8cc")
		if got := submitterSession(); got != "6d0b7ae9-d77a-4a0f-9a13-dbc8ab81a8cc" {
			t.Fatalf("session = %q", got)
		}
	})
	t.Run("PDX_SESSION_ID wins when a script names the session", func(t *testing.T) {
		clear()
		t.Setenv("CLAUDE_CODE_SESSION_ID", "from-claude")
		t.Setenv("PDX_SESSION_ID", "named-by-the-script")
		if got := submitterSession(); got != "named-by-the-script" {
			t.Fatalf("session = %q", got)
		}
	})
	t.Run("the older CLAUDE_SESSION_ID still counts", func(t *testing.T) {
		clear()
		t.Setenv("CLAUDE_SESSION_ID", "old-name")
		if got := submitterSession(); got != "old-name" {
			t.Fatalf("session = %q", got)
		}
	})
	t.Run("none of them: empty", func(t *testing.T) {
		clear()
		if got := submitterSession(); got != "" {
			t.Fatalf("session = %q", got)
		}
	})
}
