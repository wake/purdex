package main

import (
	"os"
	"os/exec"
)

// tmuxFallbackPaths are where tmux is installed when it is not on PATH: Homebrew on Apple Silicon and Intel, MacPorts,
// the system one. A var so tests can point it elsewhere.
var tmuxFallbackPaths = []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/opt/local/bin/tmux", "/usr/bin/tmux"}

// tmuxExecutable is the tmux the CLI-side helpers (the agent hook, the statusline proxy) run. A pane an ssh login without
// a login shell started has PATH=/usr/bin:/bin - `zsh -ic` does not read .zprofile, where Homebrew puts itself on PATH -
// so a bare `tmux` was "not found" there, the hook sent no session name, and the session never appeared (#2123). PATH
// still wins when it has tmux; otherwise the usual install locations; otherwise the bare name, so a missing tmux fails
// exactly as before.
func tmuxExecutable() string {
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	for _, p := range tmuxFallbackPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return "tmux"
}
