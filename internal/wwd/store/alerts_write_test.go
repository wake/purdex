package store

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var at0 = time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)

func openAlerts(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func diskLow(detail string) wire.Alert {
	return wire.Alert{Key: "disk_low:/", Kind: wire.AlertDiskLow, Level: wire.AlertPush, Detail: detail}
}

// Raise: a new key is a change; the same active key again only moves "last" (the rule still holds) and is not a change.
func TestRaiseAlertNewIsAChangeRefreshIsNot(t *testing.T) {
	s := openAlerts(t)
	changed, err := s.RaiseAlert(diskLow("mlab 剩 11.2 GB"), at0)
	if err != nil || !changed {
		t.Fatalf("new: changed=%v err=%v", changed, err)
	}
	changed, err = s.RaiseAlert(diskLow("mlab 剩 11.2 GB"), at0.Add(time.Minute))
	if err != nil || changed {
		t.Fatalf("refresh: changed=%v err=%v", changed, err)
	}
	got, err := s.ActiveAlerts()
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if !got[0].Since.Equal(at0) || !got[0].Last.Equal(at0.Add(time.Minute)) || got[0].Detail != "mlab 剩 11.2 GB" {
		t.Fatalf("since stays, last moves: %+v", got[0])
	}
}

// A different level or detail on an active key is a change (the person reads the new text), and the alert keeps its start.
func TestRaiseAlertChangedTextOrLevelIsAChange(t *testing.T) {
	s := openAlerts(t)
	s.RaiseAlert(diskLow("11.2 GB"), at0)
	if changed, _ := s.RaiseAlert(diskLow("9.8 GB"), at0.Add(time.Minute)); !changed {
		t.Fatal("a new detail is a change")
	}
	up := diskLow("9.8 GB")
	up.Level = wire.AlertDigest
	if changed, _ := s.RaiseAlert(up, at0.Add(2*time.Minute)); !changed {
		t.Fatal("a new level is a change")
	}
	got, _ := s.ActiveAlerts()
	if len(got) != 1 || got[0].Detail != "9.8 GB" || got[0].Level != wire.AlertDigest || !got[0].Since.Equal(at0) {
		t.Fatalf("%+v", got)
	}
}

func TestClearAlertAndReopen(t *testing.T) {
	s := openAlerts(t)
	if changed, err := s.ClearAlert("nope", at0); err != nil || changed {
		t.Fatalf("clearing what is not there: changed=%v err=%v", changed, err)
	}
	s.RaiseAlert(diskLow("x"), at0)
	if changed, err := s.ClearAlert("disk_low:/", at0.Add(time.Hour)); err != nil || !changed {
		t.Fatalf("clear: changed=%v err=%v", changed, err)
	}
	if changed, _ := s.ClearAlert("disk_low:/", at0.Add(2*time.Hour)); changed {
		t.Fatal("clearing twice is one change")
	}
	if got, _ := s.ActiveAlerts(); len(got) != 0 {
		t.Fatalf("a cleared alert is not active: %+v", got)
	}
	// the rule holds again: a new alert with a new start, not the old one revived
	changed, err := s.RaiseAlert(diskLow("again"), at0.Add(3*time.Hour))
	if err != nil || !changed {
		t.Fatalf("reopen: changed=%v err=%v", changed, err)
	}
	got, _ := s.ActiveAlerts()
	if len(got) != 1 || !got[0].Since.Equal(at0.Add(3*time.Hour)) || got[0].Detail != "again" {
		t.Fatalf("%+v", got)
	}
	if err := got[0].Validate(); err != nil {
		t.Fatal(err)
	}
}

// A clear is never earlier than the last time the rule held (the wire contract: cleared_at >= last).
func TestClearAlertNeverPrecedesLast(t *testing.T) {
	s := openAlerts(t)
	s.RaiseAlert(diskLow("x"), at0)
	s.RaiseAlert(diskLow("x"), at0.Add(10*time.Minute))
	s.ClearAlert("disk_low:/", at0.Add(5*time.Minute)) // a clock that stepped back
	var last, cleared int64
	if err := s.DB().QueryRow(`SELECT last_ms, cleared_ms FROM alerts WHERE key = 'disk_low:/'`).Scan(&last, &cleared); err != nil || cleared < last {
		t.Fatalf("last %d cleared %d err %v", last, cleared, err)
	}
}

func TestRaiseAlertRefusesWhatIsNotAnAlertAndCutsTheDetail(t *testing.T) {
	s := openAlerts(t)
	if _, err := s.RaiseAlert(wire.Alert{Key: "", Kind: wire.AlertDiskLow, Level: wire.AlertPush}, at0); err == nil {
		t.Fatal("an empty key was accepted")
	}
	if _, err := s.RaiseAlert(wire.Alert{Key: "k", Kind: "meltdown", Level: wire.AlertPush}, at0); err == nil {
		t.Fatal("an unknown kind was accepted")
	}
	long := diskLow(strings.Repeat("剩", 100) + "\nx") // over the limit and with a newline: cut, not refused
	if _, err := s.RaiseAlert(long, at0); err != nil {
		t.Fatalf("a long detail is cut, not refused: %v", err)
	}
	got, _ := s.ActiveAlerts()
	if len(got) != 1 || len(got[0].Detail) > wire.MaxAlertDetailBytes || strings.ContainsAny(got[0].Detail, "\n") {
		t.Fatalf("%+v", got)
	}
}

// Cleared rows are history for a few days, not a log.
func TestClearedAlertsArePrunedAfterAWeek(t *testing.T) {
	s := openAlerts(t)
	s.RaiseAlert(diskLow("x"), at0)
	s.ClearAlert("disk_low:/", at0.Add(time.Minute))
	s.RaiseAlert(wire.Alert{Key: "other", Kind: wire.AlertOrphans, Level: wire.AlertDigest}, at0.Add(AlertsRetention+time.Hour))
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM alerts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v: the old cleared row goes on the next write", n, err)
	}
}

// Many rules may raise the same key at once: a read-then-write transaction must not fail with SQLITE_BUSY, and exactly one call is the
// change that created the alert.
func TestRaiseAlertSameKeyConcurrent(t *testing.T) {
	s := openAlerts(t)
	const n = 16
	var wg sync.WaitGroup
	var created atomic.Int32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			changed, err := s.RaiseAlert(diskLow("11.2 GB"), at0)
			if err != nil {
				errs <- err
				return
			}
			if changed {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent raise: %v", err)
	}
	if created.Load() != 1 {
		t.Fatalf("exactly one raise creates the alert, got %d", created.Load())
	}
}

// Raise and Clear of one key from different rules at once: neither may fail with SQLITE_BUSY.
func TestRaiseAndClearAlertConcurrent(t *testing.T) {
	s := openAlerts(t)
	if _, err := s.RaiseAlert(diskLow("11.2 GB"), at0); err != nil {
		t.Fatal(err)
	}
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := s.RaiseAlert(diskLow("11.2 GB"), at0.Add(time.Minute)); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := s.ClearAlert(diskLow("").Key, at0.Add(time.Minute)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent raise/clear: %v", err)
	}
}
