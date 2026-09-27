// internal/tmux/noserver.go
package tmux

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	errConnectingPrefix = "error connecting to "
	enoentReason        = "No such file or directory"
)

// IsNoServer reports whether a tmux client's stderr says there is no server
// to talk to — either the socket is stale ("no server running on <path>") or
// it does not exist ("error connecting to <path> (No such file or directory)").
//
// tmux prints the first on ECONNREFUSED and the second, with strerror(errno),
// for any other connect failure — which is what a host that has just rebooted
// (/tmp wiped) produces. Recognising only the first left every session-list
// read failing forever after a reboot (#1473). Other connect failures
// (Permission denied, File name too long, ...) are real faults and stay
// errors.
//
// Pure apart from one os.Stat; it never runs tmux.
func IsNoServer(stderr string) bool {
	if strings.Contains(stderr, "no server running") {
		return true
	}
	for _, line := range strings.Split(stderr, "\n") {
		if isAbsentSocketLine(strings.TrimRight(line, "\r")) {
			return true
		}
	}
	return false
}

// isAbsentSocketLine matches one "error connecting to <path> (<reason>)" line
// whose socket is absent (spec D1). The path is split at the last " (" since
// a path may itself contain " (".
func isAbsentSocketLine(line string) bool {
	rest, ok := strings.CutPrefix(line, errConnectingPrefix)
	if !ok || !strings.HasSuffix(rest, ")") {
		return false
	}
	sep := strings.LastIndex(rest, " (")
	if sep < 0 {
		return false
	}
	path, reason := rest[:sep], rest[sep+2:len(rest)-1]
	if reason == enoentReason {
		return true
	}
	// A localised strerror hides ENOENT from the reason check; ask the
	// filesystem instead. Stat (not Lstat) so a dangling symlink counts as
	// absent. Relative paths are never stat'ed: the daemon's cwd is not
	// the base tmux resolved them against. Any stat error other than
	// "not exist" (EACCES, ...) proves nothing, so it is not a match.
	if !filepath.IsAbs(path) {
		return false
	}
	_, err := os.Stat(path)
	return err != nil && errors.Is(err, fs.ErrNotExist)
}
