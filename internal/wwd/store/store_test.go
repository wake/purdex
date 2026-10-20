package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

var t0 = time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wwd.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p
}

func TestWorkerSettingsPersistAcrossReopen(t *testing.T) {
	s, p := open(t)
	if _, ok, err := s.LoadWorker(); err != nil || ok {
		t.Fatalf("fresh db: ok=%v err=%v", ok, err)
	}
	want := wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 0, Light: 3}, OwnerRule: "hid<5m"}
	if err := s.SaveWorker(want); err != nil {
		t.Fatal(err)
	}
	want2 := wire.WorkerSettings{Enabled: false, Slots: wire.Slots{Heavy: 1, Light: 2}}
	if err := s.SaveWorker(want2); err != nil { // a second save replaces, it does not add a row
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	want2.DiskMinGB = f64(wire.DefaultDiskMinGB) // saved without a floor: stored, and read back, as the default
	got, ok, err := s2.LoadWorker()
	if err != nil || !ok || !reflect.DeepEqual(got, want2) {
		t.Fatalf("got %+v ok=%v err=%v want %+v", got, ok, err, want2)
	}
}

func TestExceptionsPersistAndDelete(t *testing.T) {
	s, p := open(t)
	until := t0.Add(time.Hour)
	pid := 4242
	a := Stored{Exception: wire.Exception{Name: "app-dev-package", Until: &until, Holds: []wire.Hold{wire.HoldHeavy, wire.HoldLight}}, Since: t0}
	b := Stored{Exception: wire.Exception{Name: "dev-server", PID: &pid, Holds: []wire.Hold{}}, Since: t0.Add(time.Minute), PIDIdent: "9.000001"}
	for _, e := range []Stored{a, b} {
		if err := s.SaveException(e); err != nil {
			t.Fatal(err)
		}
	}
	// saving the same name again replaces it
	a2 := a
	a2.Exception.Holds = []wire.Hold{wire.HoldHeavy}
	if err := s.SaveException(a2); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.LoadExceptions()
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Exception.Name != "app-dev-package" || !reflect.DeepEqual(got[0].Exception.Holds, []wire.Hold{wire.HoldHeavy}) ||
		got[0].Exception.Until == nil || !got[0].Exception.Until.Equal(until) || !got[0].Since.Equal(t0) || got[0].Exception.PID != nil {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Exception.PID == nil || *got[1].Exception.PID != 4242 || got[1].Exception.Until != nil || got[1].Exception.Holds == nil || got[1].PIDIdent != "9.000001" || got[0].PIDIdent != "" {
		t.Errorf("second: %+v", got[1])
	}
	if err := s2.DeleteException("dev-server"); err != nil {
		t.Fatal(err)
	}
	if err := s2.DeleteException("dev-server"); err != nil { // deleting what is not there is not an error
		t.Fatal(err)
	}
	if got, _ := s2.LoadExceptions(); len(got) != 1 {
		t.Fatalf("%d left", len(got))
	}
}

// The host tables and the queue's live in one wwd.db with their own version
// tables: opening twice, and migrating the queue next to it, disturbs neither.
func TestSharesTheDatabaseWithTheQueue(t *testing.T) {
	s, p := open(t)
	ctx := context.Background()
	if err := queue.Migrate(ctx, s.DB()); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorker(wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := queue.Migrate(ctx, s2.DB()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s2.LoadWorker(); !ok {
		t.Fatal("worker settings lost")
	}
}

func TestRefusesADatabaseANewerWwdWrote(t *testing.T) {
	s, p := open(t)
	if _, err := s.DB().Exec(`UPDATE host_schema SET version = 99`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(p); err == nil {
		t.Fatal("a newer schema must be refused")
	}
}

func TestOpenUsesWALAndBusyTimeout(t *testing.T) {
	s, _ := open(t)
	var mode string
	if err := s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode %q %v", mode, err)
	}
	var fk, busy int
	_ = s.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	_ = s.DB().QueryRow(`PRAGMA busy_timeout`).Scan(&busy)
	if fk != 1 || busy < 1000 {
		t.Fatalf("foreign_keys=%d busy_timeout=%d", fk, busy)
	}
}
