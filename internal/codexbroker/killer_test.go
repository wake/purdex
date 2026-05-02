package codexbroker

import (
	"testing"
	"time"
)

// TestVerifyIdentity_Match — fakeLister returns matching pid+lstart+cmdline
// → ok=true.
func TestVerifyIdentity_Match(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{
		Key:    "k1",
		PID:    4321,
		Lstart: lstart,
	}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  lstart,
		Cmdline: "node /opt/codex/dist/app-server-broker.mjs serve --cwd /tmp/x",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if !ok {
		t.Errorf("expected ok=true on full match")
	}
}

// TestVerifyIdentity_LstartMismatch — pid same, lstart differs by >1s →
// ok=false.
func TestVerifyIdentity_LstartMismatch(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  lstart.Add(5 * time.Second), // 5s drift
		Cmdline: "node /opt/codex/dist/app-server-broker.mjs",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if ok {
		t.Errorf("expected ok=false on lstart drift")
	}
}

// TestVerifyIdentity_PidGone — fakeLister returns no matching pid → ok=false.
func TestVerifyIdentity_PidGone(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: time.Now()}
	lister := NewFakeProcessLister([]RawProcess{{PID: 9999, Cmdline: "other"}})
	ok, _ := VerifyIdentity(rec, lister)
	if ok {
		t.Errorf("expected ok=false on pid gone")
	}
}

// TestVerifyIdentity_CmdlineMismatch — pid+lstart match but cmdline isn't
// a broker → ok=false (PID-reuse defence).
func TestVerifyIdentity_CmdlineMismatch(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  lstart,
		Cmdline: "/usr/bin/zsh -i",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if ok {
		t.Errorf("expected ok=false on cmdline mismatch")
	}
}

// TestVerifyIdentity_LstartTolerance_1s — lstart drift within ±1s is
// accepted (round-trip via ps formatting can shift seconds).
func TestVerifyIdentity_LstartTolerance_1s(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  lstart.Add(900 * time.Millisecond),
		Cmdline: "node app-server-broker.mjs",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if !ok {
		t.Errorf("expected ok=true within 1s tolerance")
	}
}

// Smoke for KillSequence struct compile.
func TestKillSequence_StructCompiles(t *testing.T) {
	ks := &KillSequence{
		Rec: BrokerRecord{Key: "k1"},
	}
	if ks.Rec.Key != "k1" {
		t.Errorf("rec lost")
	}
	// KillResult zero-value should be usable.
	var res KillResult
	_ = res.GracefulOk
}
