package ccuds

import (
	"strconv"
	"strings"
)

// VerifiedCCVersion is the Claude Code version every byte layout in this
// package was measured against. A registry entry reporting a newer version
// is where a silent protocol change would first show up, so callers log it
// (once) and the selftest exists to re-verify.
const VerifiedCCVersion = "2.1.270"

// NewerThanVerified reports whether v (dotted decimal, e.g. "2.1.271") is
// strictly newer than VerifiedCCVersion. Missing trailing components count
// as 0. Anything that is not purely dotted decimal is not comparable and
// yields false.
func NewerThanVerified(v string) bool {
	got, ok := parseDotted(v)
	if !ok {
		return false
	}
	ref, _ := parseDotted(VerifiedCCVersion)
	n := max(len(got), len(ref))
	for i := 0; i < n; i++ {
		a, b := component(got, i), component(ref, i)
		if a != b {
			return a > b
		}
	}
	return false
}

func component(parts []int, i int) int {
	if i < len(parts) {
		return parts[i]
	}
	return 0
}

func parseDotted(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		if f == "" {
			return nil, false
		}
		for _, r := range f {
			if r < '0' || r > '9' {
				return nil, false
			}
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}
