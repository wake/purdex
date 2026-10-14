//go:build !unix

package fleet

// LocalDisk is not available off Unix.
func LocalDisk() (float64, bool) { return 0, false }
