package conversations

import (
	"path/filepath"
	"strings"
)

// IsTestCwd reports whether a conversation's cwd marks it as a throwaway
// "test" conversation: a path at or under /tmp (macOS: /private/tmp). The
// path is cleaned first so "..", "//" and trailing slashes cannot smuggle a
// real directory in or out; relative paths and /var/folders never match.
// The shared case table lives in testdata/test-cwd-cases.json.
func IsTestCwd(cwd string) bool {
	if cwd == "" {
		return false
	}
	p := filepath.Clean(cwd)
	if p == "/tmp" || strings.HasPrefix(p, "/tmp/") {
		p = "/private" + p
	}
	return p == "/private/tmp" || strings.HasPrefix(p, "/private/tmp/")
}
