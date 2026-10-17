// Package gitenv is the environment git runs in when it is asked about a repository the caller names, not the one the process
// happens to be in.
package gitenv

import "strings"

// kept are the GIT_ variables that choose where configuration is read from. They are how the owner of the process isolates git from the
// user's own configuration (GIT_CONFIG_GLOBAL=/dev/null, GIT_CONFIG_NOSYSTEM=1), so dropping them would let ~/.gitconfig back in; they
// name a file, they do not carry settings.
var kept = map[string]bool{"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_NOSYSTEM": true}

// Clean is env without the GIT_ variables, and with GIT_TERMINAL_PROMPT=0 added. The GIT_ variables tell git which repository, work tree,
// index and object store to use (a process started by a git hook carries them, and `git -C dir` does not override them), and some of
// them carry configuration that runs a command (GIT_CONFIG_COUNT / GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n, GIT_CONFIG_PARAMETERS:
// core.fsmonitor, core.sshCommand). The git commands here are local, so none of that is needed; a list of names would fall behind git,
// a prefix does not. The exception is `kept` above. What is not a GIT_ variable stays, HOME and XDG_CONFIG_HOME included: the user's own
// configuration is read unless the owner of the process isolated it. The caller adds the GIT_ variables it does want after this.
func Clean(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") && !kept[name] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_TERMINAL_PROMPT=0")
}
