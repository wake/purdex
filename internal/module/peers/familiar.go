package peers

// FamiliarSource is what the picking rules know of the other hosts' history (name pool v2 spec §6.1): the codes each other host
// published in its envelope (names.familiar) as having been a lead or a lead's member there.
//
// RemoteFamiliar returns the union of those sets over the other hosts, this host not included (its own history is read from
// the store), or nil when no other host has published one: a daemon older than names.familiar publishes nothing and counts as
// an empty set. It is called with the pool lock held, so it must not take that lock or do any I/O: it answers from what the
// snapshot refresh recorded, under a lock of its own that is held briefly. The caller never changes the returned map.
type FamiliarSource interface {
	RemoteFamiliar() map[string]bool
}

// WithFamiliarSource wires the other hosts' familiar sets; nil (the default) means none.
func (m *Module) WithFamiliarSource(f FamiliarSource) *Module {
	m.familiar = f
	return m
}
