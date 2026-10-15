package resourcesmod

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
)

// A host that gave neither the pressure level nor the paging counters has no zone to judge: unknown, not normal (L2b attack).
func TestSampler_NoSignalIsAnUnknownZone(t *testing.T) {
	r := newZoneRig(t)
	h := r.tick(t, zoneRaw(95, 50, 0)) // level unknown, no counters: nothing to judge by
	assert.Equal(t, "unknown", h.Zone)

	// the counters alone judge it
	total := uint64(0)
	r.tick(t, withPages(zoneRaw(40, 1, 0), total))
	total += 500
	h = r.tick(t, withPages(zoneRaw(40, 1, 0), total))
	assert.Equal(t, "warning", h.Zone, "100 pages/s, level unknown")

	// the level alone judges it
	h = r.tick(t, zoneRaw(40, 1, 2))
	assert.Equal(t, "warning", h.Zone)
	h = r.tick(t, zoneRaw(40, 1, 1))
	assert.Equal(t, "normal", h.Zone)
}

// Adding a column that is there is not an error (two openers of one database), and a real failure still is.
func TestAddColumn_ToleratesADuplicate(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE t (a INTEGER)`)
	require.NoError(t, err)
	require.NoError(t, addColumn(db, "t", "b", "INTEGER NOT NULL DEFAULT 0"))
	require.NoError(t, addColumn(db, "t", "b", "INTEGER NOT NULL DEFAULT 0"), "the second add is what was wanted")
	assert.Error(t, addColumn(db, "no_such_table", "b", "INTEGER"))
}

// A gate directory that does not exist yet is zeros (no gate has run); one that cannot be looked at is no reading.
func TestSlotCounts_UnreadableDirectoryIsNoReading(t *testing.T) {
	m := newTestModule(idleSampler(), nil)
	cfg := gate.Defaults()

	cfg.Dir = filepath.Join(t.TempDir(), "not-yet")
	m.gateConfig = func() gate.Config { return cfg }
	_, _, _, ok := m.slotCounts()
	assert.True(t, ok, "no directory: no gate has run, zeros are right")

	file := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	cfg.Dir = filepath.Join(file, "gate") // under a file: stat says ENOTDIR
	_, _, _, ok = m.slotCounts()
	assert.False(t, ok)
}
