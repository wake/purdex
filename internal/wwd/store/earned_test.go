package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEarnedLabelRoundTripAndState(t *testing.T) {
	s := openTemp(t)
	at := time.UnixMilli(1_700_000_000_000).UTC()
	e := wire.EarnedLabel{Label: "task:purdex/tsc", Fingerprint: map[string]string{"node": "24.14.0"}, Snapshot: "abc", GrantedAt: at, State: wire.EarnedLabelEarned}
	if err := s.SaveEarnedLabel(e); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.EarnedLabel(e.Label)
	if err != nil || !ok || got.Fingerprint["node"] != "24.14.0" || got.Snapshot != "abc" || !got.GrantedAt.Equal(at) || got.State != wire.EarnedLabelEarned {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if ok, err := s.SetEarnedLabelState(e.Label, wire.EarnedLabelQuarantined); err != nil || !ok {
		t.Fatalf("quarantine: %v %v", ok, err)
	}
	if got, _, _ := s.EarnedLabel(e.Label); got.State != wire.EarnedLabelQuarantined {
		t.Fatalf("state %q", got.State)
	}
	if ok, err := s.SetEarnedLabelState("task:none/none", wire.EarnedLabelEarned); err != nil || ok {
		t.Fatalf("missing label: %v %v", ok, err)
	}
	if _, err := s.SetEarnedLabelState(e.Label, "bogus"); err == nil {
		t.Fatal("bogus state accepted")
	}
	all, err := s.EarnedLabels()
	if err != nil || len(all) != 1 {
		t.Fatalf("all %v %v", all, err)
	}
	if err := s.DeleteEarnedLabel(e.Label); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.EarnedLabel(e.Label); ok {
		t.Fatal("still there")
	}
	if err := s.DeleteEarnedLabel(e.Label); err != nil {
		t.Fatalf("deleting a missing label: %v", err)
	}
}

func TestEarnedLabelsListIsNotNilAndOrdered(t *testing.T) {
	s := openTemp(t)
	if got, err := s.EarnedLabels(); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty table: %v %v", got, err)
	}
	for _, l := range []string{"task:b/x", "task:a/y"} {
		if err := s.SaveEarnedLabel(wire.EarnedLabel{Label: l, State: wire.EarnedLabelEarned}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.EarnedLabels()
	if len(got) != 2 || got[0].Label != "task:a/y" {
		t.Fatalf("%+v", got)
	}
	if got[0].Fingerprint == nil {
		t.Fatal("a missing fingerprint comes back as an empty map")
	}
}

func TestEarnedLabelRefusesBadRows(t *testing.T) {
	s := openTemp(t)
	for name, e := range map[string]wire.EarnedLabel{
		"empty label":      {State: wire.EarnedLabelEarned},
		"not a task label": {Label: "os:linux", State: wire.EarnedLabelEarned},
		"blanks":           {Label: "task:a b/c", State: wire.EarnedLabelEarned},
		"over long":        {Label: "task:a/" + strings.Repeat("x", 65), State: wire.EarnedLabelEarned},
		"bad state":        {Label: "x", State: "maybe"},
	} {
		if err := s.SaveEarnedLabel(e); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// A damaged row is skipped and reported; the good rows still come back.
func TestEarnedLabelsSkipsDamagedRow(t *testing.T) {
	s := openTemp(t)
	if err := s.SaveEarnedLabel(wire.EarnedLabel{Label: "task:a/ok", State: wire.EarnedLabelEarned}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO earned_labels (label, fingerprint, snapshot, granted_ms, state) VALUES ('task:a/bad', '{', '', 0, 'earned')`); err != nil {
		t.Fatal(err)
	}
	got, err := s.EarnedLabels()
	var se *SkippedRowsError
	if !errors.As(err, &se) || se.N != 1 || len(got) != 1 || got[0].Label != "task:a/ok" {
		t.Fatalf("%+v %v", got, err)
	}
}

// A row that is not a task label (a damaged or planted one) is skipped on the way out like any other damaged row.
func TestEarnedLabelsSkipsRowsThatAreNotTaskLabels(t *testing.T) {
	s := openTemp(t)
	if _, err := s.DB().Exec(`INSERT INTO earned_labels (label, fingerprint, snapshot, granted_ms, state) VALUES ('os:linux', '{}', '', 0, 'earned')`); err != nil {
		t.Fatal(err)
	}
	got, err := s.EarnedLabels()
	var se *SkippedRowsError
	if !errors.As(err, &se) || se.N != 1 || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}
