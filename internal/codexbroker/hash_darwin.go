//go:build darwin

package codexbroker

import (
	"golang.org/x/sys/unix"
)

// _PC_CASE_SENSITIVE is the pathconf(2) selector for case sensitivity on
// macOS. Defined in <sys/unistd.h> as 11. golang.org/x/sys/unix exposes
// the Pathconf wrapper but not this constant; we hard-code it here.
const _PC_CASE_SENSITIVE = 11

// IsCaseInsensitiveVolume returns true on macOS APFS / HFS+ case-preserving
// + case-insensitive volumes. Default-true on darwin when pathconf fails
// (the overwhelming majority of macOS installations use case-insensitive
// boot volumes; defaulting that direction matches user expectation).
func IsCaseInsensitiveVolume(path string) bool {
	if path == "" {
		return true
	}
	// _PC_CASE_SENSITIVE returns 0 on case-insensitive volumes, 1 on
	// case-sensitive. On error (-1) we fall back to "case-insensitive"
	// which is the macOS default boot volume behaviour.
	v, err := unix.Pathconf(path, _PC_CASE_SENSITIVE)
	if err != nil {
		return true
	}
	return v == 0
}
