package store

import (
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func diskMin(t *testing.T, s *Store) float64 {
	t.Helper()
	ws, ok, err := s.LoadWorker()
	if err != nil || !ok || ws.DiskMinGB == nil {
		t.Fatalf("LoadWorker: %+v ok=%v err=%v", ws, ok, err)
	}
	return *ws.DiskMinGB
}

func f64(v float64) *float64 { return &v }

// The v8 file (what 762/763 run) has a worker_settings row with no disk floor: opening it adds the column, the row gets the default 10
// (the gate's disk_min_gb, which the host has been under until now), and a second open changes nothing.
func TestOpenMigratesV8ToV9(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorker(wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 3}, OwnerRule: "hid<5m"}); err != nil {
		t.Fatal(err)
	}
	// turn it into the v8 file: no disk_min_gb column, version 8
	for _, q := range []string{`ALTER TABLE worker_settings DROP COLUMN disk_min_gb`, `UPDATE host_schema SET version = 8`} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	for round := 1; round <= 2; round++ {
		s, err := Open(p)
		if err != nil {
			t.Fatalf("open %d: %v", round, err)
		}
		if !tableCols(t, s, "worker_settings")["disk_min_gb"] {
			t.Errorf("open %d: no disk_min_gb column", round)
		}
		ws, ok, err := s.LoadWorker()
		if err != nil || !ok || ws.Slots != (wire.Slots{Heavy: 2, Light: 3}) || ws.OwnerRule != "hid<5m" || !ws.Enabled {
			t.Fatalf("open %d: the row changed: %+v ok=%v err=%v", round, ws, ok, err)
		}
		if got := diskMin(t, s); got != 10 {
			t.Errorf("open %d: the old row got disk_min_gb %v, want 10", round, got)
		}
		var v int
		if err := s.DB().QueryRow(`SELECT version FROM host_schema`).Scan(&v); err != nil || v != schemaVersion {
			t.Errorf("open %d: version %d %v", round, v, err)
		}
		s.Close()
	}
}

// A file made by this wwd has the column from the start.
func TestAFreshFileHasTheDiskFloorColumn(t *testing.T) {
	s, _ := open(t)
	if !tableCols(t, s, "worker_settings")["disk_min_gb"] {
		t.Fatal("a new wwd.db has no disk_min_gb column")
	}
}

// A saved floor, including 0 (off), survives a reopen; settings saved without one (nil) are stored as the default.
func TestTheDiskFloorIsSavedAndReloaded(t *testing.T) {
	s, p := open(t)
	for _, c := range []struct {
		in   *float64
		want float64
	}{{f64(25.5), 25.5}, {f64(0), 0}, {nil, wire.DefaultDiskMinGB}} {
		if err := s.SaveWorker(wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: c.in}); err != nil {
			t.Fatal(err)
		}
		s.Close()
		var err error
		if s, err = Open(p); err != nil {
			t.Fatal(err)
		}
		if got := diskMin(t, s); got != c.want {
			t.Errorf("saved %v: loaded %v, want %v", c.in, got, c.want)
		}
	}
	s.Close()
}

// What an older wwd does with a v9 file is the existing rule: a newer schema is refused, so the way back is the backup taken before the
// upgrade. This pins the number that rule compares.
func TestSchemaVersionIsNine(t *testing.T) {
	if schemaVersion != 9 {
		t.Fatalf("schemaVersion = %d: a step needs its own case in migrate and its own test", schemaVersion)
	}
}
