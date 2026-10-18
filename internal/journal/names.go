package journal

import (
	"regexp"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
)

// DefaultPersonName is what the journal calls the person when `[journal] person_name` is not set.
const DefaultPersonName = "使用者"

// DisplayName is how the journal writes a session: the display name of its pool name when the virtual name ends in one
// (purdex-3f-adam -> Adam; purdex-3f-hiro -> the persona's name, retired or not), else the virtual name itself (an old
// purdex-88-b8 stays as it is). Any `<host>/` prefix is removed first, so an address and a bare name give the same result.
func DisplayName(virtualName string) string {
	name := strings.TrimSpace(virtualName)
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if pool, ok := ipeers.PoolNameOf(name); ok {
		if display, ok := ipeers.PoolDisplayName(pool); ok {
			return display
		}
	}
	return name
}

// RewritePeerPrefixes rewrites the addresses in the bracketed prefix that opens a line of a turn's text
// (`[mlab/purdex-3f-adam → mlab/purdex-88-b8] ...`, `[report merged mlab/purdex-3f-adam] ...`) to display names. Only
// that prefix changes: the rest of the line, a bracket later in it, and a prefix without an address (`[pdx team]`) are
// left alone. It is for the journal job's copy of the text; the turn job keeps the original.
func RewritePeerPrefixes(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "[") {
			continue
		}
		end := strings.IndexByte(l, ']')
		if end < 0 {
			continue
		}
		inner := l[1:end]
		if sides := strings.Split(inner, " → "); len(sides) == 2 {
			inner = rewriteSide(sides[0]) + " → " + rewriteSide(sides[1])
		} else {
			words := strings.Split(inner, " ")
			for j, w := range words {
				// outside an arrow only an address whose name is a pool name is surely a session
				if isAddress(w) {
					if _, ok := ipeers.PoolNameOf(w[strings.IndexByte(w, '/')+1:]); ok {
						words[j] = DisplayName(w)
					}
				}
			}
			inner = strings.Join(words, " ")
		}
		lines[i] = "[" + inner + l[end:]
	}
	return strings.Join(lines, "\n")
}

// rewriteSide renames one side of `A → B` when it is a single token (an address or a name); anything with a space is
// not an address and stays.
func rewriteSide(s string) string {
	if s == "" || strings.ContainsAny(s, " \t") || (strings.Contains(s, "/") && !isAddress(s)) {
		return s
	}
	return DisplayName(s)
}

// addressPattern is a peer address as pdx writes it: one host alias, one slash, a virtual name or a ref. A branch name
// with two parts (feature/login) still matches, which is why outside an arrow only pool names are rewritten; a URL or
// a path with more slashes does not match.
var addressPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}/([a-z0-9][a-z0-9-]{1,63}|_[0-9a-z]{6})$`)

func isAddress(s string) bool { return addressPattern.MatchString(s) }
