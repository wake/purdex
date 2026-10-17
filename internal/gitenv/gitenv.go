// Package gitenv is the environment git runs in when it is asked about a repository the caller names, not the one the process
// happens to be in.
package gitenv

import "strings"

// Clean is env without any variable that starts with GIT_, and with GIT_TERMINAL_PROMPT=0 added. The GIT_ variables tell git which
// repository, work tree, index and object store to use (a process started by a git hook carries them, and `git -C dir` does not override
// them), and some of them add configuration that runs a command (GIT_CONFIG_COUNT / GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n,
// GIT_CONFIG_PARAMETERS: core.fsmonitor, core.sshCommand). The git commands here are local, so nothing that is dropped is needed; a
// list of names would fall behind git, a prefix does not. The caller adds the GIT_ variables it does want after this.
func Clean(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_TERMINAL_PROMPT=0")
}
