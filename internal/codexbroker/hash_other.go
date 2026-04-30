//go:build !darwin && !linux

package codexbroker

// IsCaseInsensitiveVolume returns false on non-Darwin / non-Linux platforms
// (Windows, FreeBSD, etc.). P1 does not support fine-grained detection on
// these platforms; this is documented as a P1 limitation in the plan.
func IsCaseInsensitiveVolume(path string) bool {
	return false
}
