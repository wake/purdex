package journal

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	scpLike   = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]+):(.+)$`) // [user@]host:owner/repo, git's scp form
	validHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
)

// NormalizeRemote reduces a remote URL, in any form git accepts for it (scp-like, ssh://, https://, git://, or already
// `host/owner/repo`), to `host/owner/repo`: lowercase, without scheme, user and password, port, a trailing `/` or `.git`.
// "" when it names no host and path (a local path, file://, nonsense). The result never carries credentials, so it is
// safe to log.
func NormalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	var host, path string
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		host, path = u.Hostname(), u.Path
	case scpLike.MatchString(s):
		m := scpLike.FindStringSubmatch(s)
		host, path = m[1], m[2]
	default:
		i := strings.IndexByte(s, '/')
		if i <= 0 {
			return ""
		}
		host, path = s[:i], s[i+1:]
	}
	host = strings.ToLower(host)
	path = strings.TrimSuffix(strings.Trim(strings.ToLower(path), "/"), ".git")
	if !validHost.MatchString(host) || path == "" || strings.Contains(path, "..") {
		return ""
	}
	return host + "/" + path
}

// PushAllowed reports whether a remote may be pushed: its normalised form equals a normalised entry of list. The match is
// exact (no prefixes, no wildcards), and an empty list or an entry that normalises to nothing allows nothing.
func PushAllowed(remoteURL string, list []string) bool {
	n := NormalizeRemote(remoteURL)
	if n == "" {
		return false
	}
	for _, e := range list {
		if NormalizeRemote(e) == n {
			return true
		}
	}
	return false
}
