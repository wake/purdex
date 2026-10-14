// Package fleet probes machines that have no Purdex daemon, over SSH, and judges how loaded any host is. It is a leaf
// package: it imports nothing of internal/module, so the hostconfig module can use it to check an alias.
package fleet

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// aliasRE is the shape of an SSH alias the fleet accepts (spec §5).
var aliasRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// errConfigTooBig is returned for a config file over maxConfigBytes: it is refused, never read as a prefix.
var errConfigTooBig = errors.New("ssh config: file too large")

// maxConfigBytes bounds what is read from one SSH config file.
const maxConfigBytes = 1 << 20

// Aliases lists the literal `Host` names of <home>/.ssh/config that the alias rule accepts, in order of appearance, each
// once. A pattern (`*`, `?`, `!`) is not an alias; `Match` blocks and comments yield nothing. `Include` is followed one
// level: paths are relative to ~/.ssh (`~` is the home), globs are expanded in name order, an `Include` inside an included
// file is ignored and a file that is not there is skipped. Nothing is cached: every call reads the files again, so a
// `Host` that was removed is gone at once. A missing config is no aliases, not an error.
func Aliases(home string) ([]string, error) {
	sshDir := filepath.Join(home, ".ssh")
	var out []string
	add := func(names []string) {
		for _, n := range names {
			if aliasRE.MatchString(n) && !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	main, err := readConfig(filepath.Join(sshDir, "config"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	mainDirs, err := parseConfig(main)
	if err != nil {
		return nil, err
	}
	for _, d := range mainDirs {
		switch d.keyword {
		case "host":
			add(literalHosts(d.args))
		case "include":
			for _, path := range expandIncludes(home, sshDir, d.args) {
				text, err := readConfig(path)
				if err != nil {
					if errors.Is(err, errConfigTooBig) {
						return nil, err // fail closed: ssh would read the whole file
					}
					continue // an include that cannot be read is skipped
				}
				innerDirs, err := parseConfig(text)
				if err != nil {
					return nil, err
				}
				for _, inner := range innerDirs {
					if inner.keyword == "host" { // an Include in an included file is ignored
						add(literalHosts(inner.args))
					}
				}
			}
		}
	}
	return out, nil
}

// ValidAlias reports whether alias passes the alias rule and is a literal `Host` of the SSH config as it is now.
func ValidAlias(home, alias string) bool {
	if !aliasRE.MatchString(alias) {
		return false
	}
	names, err := Aliases(home)
	return err == nil && slices.Contains(names, alias)
}

type directive struct {
	keyword string // lower case
	args    []string
}

// parseConfig splits an SSH config into its directives. Comments (a line starting with `#`, or `#` after white space) and
// blank lines are dropped; a keyword and its value may be separated by white space or `=`. A line ssh would refuse (an
// unterminated quote) is an error for the whole file: validation must not approve what ssh will not load.
func parseConfig(text string) ([]directive, error) {
	var out []directive
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexAny(line, " \t"); i >= 0 {
			if j := strings.Index(line[i:], "#"); j >= 0 {
				line = strings.TrimSpace(line[:i+j])
			}
		}
		key, rest := line, ""
		if i := strings.IndexAny(line, " \t="); i >= 0 {
			key, rest = line[:i], strings.TrimLeft(line[i:], " \t=")
		}
		args, ok := splitArgs(rest)
		if !ok {
			return nil, errors.New("ssh config: unterminated quote")
		}
		out = append(out, directive{keyword: strings.ToLower(key), args: args})
	}
	return out, nil
}

// splitArgs splits the arguments of a directive the way ssh does: white space separates, double quotes group (and are
// removed), and inside quotes a backslash escapes the next character. ok is false for an unterminated quote.
func splitArgs(s string) (args []string, ok bool) {
	var out []string
	var cur strings.Builder
	inTok, quoted := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted && c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == '"':
			quoted, inTok = !quoted, true
		case !quoted && (c == ' ' || c == '\t'):
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			cur.WriteByte(c)
			inTok = true
		}
	}
	if quoted {
		return nil, false
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out, true
}

// literalHosts drops every pattern from the arguments of a `Host` line.
func literalHosts(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.ContainsAny(a, "*?!") {
			out = append(out, a)
		}
	}
	return out
}

// expandIncludes turns the arguments of an `Include` into the files to read: relative to ~/.ssh, `~` expanded, globs in
// name order, directories left out.
func expandIncludes(home, sshDir string, args []string) []string {
	var out []string
	for _, a := range args {
		switch {
		case a == "~":
			a = home
		case strings.HasPrefix(a, "~/"):
			a = filepath.Join(home, a[2:])
		case !filepath.IsAbs(a):
			a = filepath.Join(sshDir, a)
		}
		matches, err := filepath.Glob(a)
		if err != nil {
			continue
		}
		slices.Sort(matches)
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && st.Mode().IsRegular() {
				out = append(out, m)
			}
		}
	}
	return out
}

// readConfig reads at most maxConfigBytes of a config file.
func readConfig(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err == nil && len(b) > maxConfigBytes {
		return "", errConfigTooBig
	}
	return string(b), err
}
