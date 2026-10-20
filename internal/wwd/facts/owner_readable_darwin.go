//go:build darwin

package facts

// OwnerReadable says whether this host can read its owner (macOS with the IOKit frameworks loadable): the same condition under which
// SystemSourcesWith gives facts an owner. A host that can read its owner can have an owner rule, whatever [owner] enabled says about the
// rule it starts with; the worker settings accept an owner_rule only where this is true.
func OwnerReadable() bool {
	_, err := DarwinOwnerSources()
	return err == nil
}
