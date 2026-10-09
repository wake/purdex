// Package claudeenv names the environment variables a Claude Code process
// sets to say WHICH SESSION it is — the session id, its messaging socket and
// token, its pid, its entrypoint — and removes them from where they do not
// belong (#2122).
//
// The daemon is sometimes started, or its tmux server is, from inside a Claude
// Code session. Every process the daemon then starts (a tmux server, a new
// tmux session, a Nexen worker's `claude -p`, a build) inherits that one
// session's identity: a `pdx msg send` in a new shell is attributed to the
// leaked messaging socket, and a fresh `claude` believes it is a child session.
// The daemon is no Claude Code session, so it carries none of these.
//
// Configuration variables a person sets on purpose (CLAUDE_CONFIG_DIR,
// CLAUDE_CODE_PLUGIN_DIRS, CLAUDE_CODE_DISABLE_TERMINAL_TITLE, CLAUDE_CODE_USE_*,
// tokens) are NOT session identity and are kept.
package claudeenv

import (
	"os"
	"strings"
)

// SessionVars are the variables a Claude Code session sets for itself (the
// eleven seen leaking on 2026-10-09, #2122). Exact names: tmux's
// set-environment needs them.
var SessionVars = []string{
	"CLAUDECODE",
	"CLAUDE_PID",
	"CLAUDE_EFFORT",
	"CLAUDE_PLUGIN_DATA",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
}

// sessionPrefixes catch the members of the same families a later Claude Code
// adds (CLAUDE_CODE_SESSION_*, CLAUDE_CODE_MESSAGING_*), for the process
// environment, where the names can be enumerated.
var sessionPrefixes = []string{"CLAUDE_CODE_SESSION_", "CLAUDE_CODE_MESSAGING_"}

// IsSessionVar says whether name is a session-identity variable.
func IsSessionVar(name string) bool {
	for _, v := range SessionVars {
		if name == v {
			return true
		}
	}
	for _, p := range sessionPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Filter is env ("NAME=value" entries) without the session-identity
// variables, in order, and the names it dropped (names only: values are
// tokens and are never kept or logged).
func Filter(env []string) (kept, removed []string) {
	kept = make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if IsSessionVar(name) {
			removed = append(removed, name)
			continue
		}
		kept = append(kept, kv)
	}
	return kept, removed
}

// ScrubProcess removes the session-identity variables from this process's
// environment and returns the names it removed. Every process started after
// this inherits a clean environment.
func ScrubProcess() []string {
	_, removed := Filter(os.Environ())
	for _, name := range removed {
		_ = os.Unsetenv(name)
	}
	return removed
}

// TmuxGlobalUnsetArgs is a tmux command list that removes SessionVars from the
// server's global environment (a server started from a Claude Code session
// holds them there, and every session created on it inherits them):
//
//	start-server ; set-environment -g -u A ; set-environment -g -u B ; …
//
// start-server first, so the list works when no server is running yet (a
// bare set-environment would fail with "no server running" and end the list);
// it is ";"-joined to the command that follows, so one tmux invocation does
// both. set-environment -u of a variable that is not set is not an error.
func TmuxGlobalUnsetArgs() []string {
	args := []string{"start-server"}
	for _, v := range SessionVars {
		args = append(args, ";", "set-environment", "-g", "-u", v)
	}
	return append(args, ";")
}
