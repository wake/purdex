//go:build !darwin

package facts

// OwnerReadable: only macOS has an owner source, so no other host can have an owner rule.
func OwnerReadable() bool { return false }
