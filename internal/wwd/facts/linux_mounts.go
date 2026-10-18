package facts

import (
	"strconv"
	"strings"
)

// ParseMountPoints reads the mount points (field 5) out of /proc/self/mountinfo.
// The kernel writes a space, tab, newline or backslash in a path as an octal
// escape (\040).
func ParseMountPoints(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		out = append(out, unescapeMount(f[4]))
	}
	return out
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// MountOf is the mount point of the file system holding path: the longest mount
// point that is path or a parent of it ("/" when none matches).
func MountOf(points []string, path string) string {
	best := "/"
	for _, m := range points {
		if m == "/" || (path != m && !strings.HasPrefix(path, m+"/")) {
			continue
		}
		if len(m) > len(best) {
			best = m
		}
	}
	return best
}
