package journal

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	scpLike   = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]+):(.+)$`) // [user@]host:owner/repo, git's scp form
	validHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
)

// NormalizeRemote reduces a remote URL, in a form git treats as a network address or a local repository (scp-like, ssh://,
// https://, http://, git://, an absolute path, file://), to `host/owner/repo`: lowercase, without scheme, user and password, port, a trailing `/` or `.git`.
// A local remote (an absolute path or file://) becomes `file:<cleaned path>`, which only an entry spelling that path
// matches. "" when it names no host and path (a relative path, nonsense). The result never carries credentials, so it is
// safe to log.
func NormalizeRemote(raw string) string { return normalizeRemote(raw, false) }

// normalizeEntry is NormalizeRemote for an allow-list entry, which may also be spelled `host/owner/repo` as it is
// written in config.toml. A remote must not be: git reads such a string as a path relative to the working directory.
func normalizeEntry(raw string) string { return normalizeRemote(raw, true) }

func normalizeRemote(raw string, entry bool) string {
	s := strings.TrimSpace(raw)
	var host, path string
	switch {
	case s == "", strings.ContainsAny(s, " \t\r\n"), strings.Contains(s, "::"): // "::" is git's remote-helper syntax (ext::, fd::)
		return ""
	case strings.HasPrefix(s, "/"):
		return localRemote(s)
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		switch strings.ToLower(u.Scheme) {
		case "ssh", "https", "http", "git":
			host, path = u.Hostname(), u.Path
		case "file":
			if u.Host != "" && u.Host != "localhost" {
				return ""
			}
			return localRemote(u.Path)
		default: // a remote helper (ext://, evil://) is not a host, whatever its path says
			return ""
		}
	case scpLike.MatchString(s):
		m := scpLike.FindStringSubmatch(s)
		host, path = m[1], m[2]
	case entry:
		i := strings.IndexByte(s, '/')
		if i <= 0 {
			return ""
		}
		host, path = s[:i], s[i+1:]
	default: // no scheme, not scp-like, not absolute: git takes it for a relative path
		return ""
	}
	host = strings.ToLower(host)
	path = strings.TrimSuffix(strings.Trim(strings.ToLower(path), "/"), ".git")
	if !validHost.MatchString(host) || path == "" || strings.Contains(path, "..") {
		return ""
	}
	return host + "/" + path
}

// localRemote is the normal form of a local repository path.
func localRemote(path string) string {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || path == string(filepath.Separator) {
		return ""
	}
	return "file:" + path
}

// PushAllowed reports whether a remote may be pushed: its normalised form equals a normalised entry of list. The match is
// exact (no prefixes, no wildcards), and an empty list or an entry that normalises to nothing allows nothing.
func PushAllowed(remoteURL string, list []string) bool {
	n := NormalizeRemote(remoteURL)
	if n == "" {
		return false
	}
	for _, e := range list {
		if normalizeEntry(e) == n {
			return true
		}
	}
	return false
}
