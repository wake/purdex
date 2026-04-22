package arbmode

// loadPublishedForTest returns the mode and revision of the currently published
// modeTarget. White-box helper for concurrency tests in this package; compiled
// only under `go test` (files ending in _test.go are not part of production
// binaries — a standard Go pattern for exposing internals to tests within the
// same package).
func (m *Manager) loadPublishedForTest() (ArbMode, int64) {
	t := m.published.Load()
	if t == nil {
		return "", -1
	}
	return t.Mode, t.Revision
}
