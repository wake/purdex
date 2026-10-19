package daemon

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

type fakeWriter struct {
	raised  []wire.Alert
	cleared []string
	changed bool // what Raise and Clear answer
	err     error
}

func (f *fakeWriter) RaiseAlert(a wire.Alert, at time.Time) (bool, error) {
	f.raised = append(f.raised, a)
	return f.changed, f.err
}

func (f *fakeWriter) ClearAlert(key string, at time.Time) (bool, error) {
	f.cleared = append(f.cleared, key)
	return f.changed, f.err
}

func alertHint(t *testing.T, ch <-chan wire.Event) (wire.AlertEvent, bool) {
	t.Helper()
	select {
	case e := <-ch:
		if e.Type != wire.EventAlert {
			t.Fatalf("event %q", e.Type)
		}
		v, err := e.DecodeData()
		if err != nil {
			t.Fatal(err)
		}
		return v.(wire.AlertEvent), true
	case <-time.After(50 * time.Millisecond):
		return wire.AlertEvent{}, false
	}
}

// Spec §13.3: the alert event is only a hint that a host's list changed; it is a new event type, so it is sent only while the proto 2
// gate is open (a proto 1 reader of /v1/events would reject the type). The table is written either way.
func TestAlertBookHintsOnlyWhenTheGateIsOpenAndSomethingChanged(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe(false)
	defer cancel()
	w := &fakeWriter{changed: true}
	var open atomic.Bool
	book := NewAlertBook(w, "mlab", b, open.Load, nil)
	a := wire.Alert{Key: "disk_low:/", Kind: wire.AlertDiskLow, Level: wire.AlertPush}

	if err := book.Raise(a); err != nil {
		t.Fatal(err)
	}
	if _, got := alertHint(t, ch); got {
		t.Fatal("a hint went out with the gate shut")
	}
	if len(w.raised) != 1 {
		t.Fatal("the table is written whatever the gate says")
	}

	open.Store(true)
	if err := book.Raise(a); err != nil {
		t.Fatal(err)
	}
	if h, got := alertHint(t, ch); !got || h.Host != "mlab" {
		t.Fatalf("hint %+v %v", h, got)
	}

	w.changed = false // the rule still holds, nothing a reader would notice
	if err := book.Raise(a); err != nil {
		t.Fatal(err)
	}
	if _, got := alertHint(t, ch); got {
		t.Fatal("a refresh is not a change")
	}

	w.changed = true
	if err := book.Clear("disk_low:/"); err != nil {
		t.Fatal(err)
	}
	if _, got := alertHint(t, ch); !got {
		t.Fatal("a clear is a change")
	}
	if len(w.cleared) != 1 || w.cleared[0] != "disk_low:/" {
		t.Fatalf("%v", w.cleared)
	}
}

func TestAlertBookWriteFailureIsReturnedAndSaysNothing(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe(false)
	defer cancel()
	w := &fakeWriter{changed: true, err: errors.New("database is locked")}
	book := NewAlertBook(w, "mlab", b, func() bool { return true }, nil)
	if err := book.Raise(wire.Alert{Key: "k", Kind: wire.AlertThrash, Level: wire.AlertDigest}); err == nil {
		t.Fatal("the failure was swallowed")
	}
	if err := book.Clear("k"); err == nil {
		t.Fatal("the failure was swallowed")
	}
	if _, got := alertHint(t, ch); got {
		t.Fatal("a failed write must not announce a change")
	}
}

// No gate function is a shut gate.
func TestAlertBookWithoutAGateNeverHints(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe(false)
	defer cancel()
	book := NewAlertBook(&fakeWriter{changed: true}, "mlab", b, nil, nil)
	_ = book.Raise(wire.Alert{Key: "k", Kind: wire.AlertThrash, Level: wire.AlertDigest})
	if _, got := alertHint(t, ch); got {
		t.Fatal("hint without a gate")
	}
}

// The hint is not cached for a late subscriber (it reads the list when it starts), and it reaches TCP subscribers too: an alert is not a
// local-only thing like an ad-hoc job.
func TestAlertHintIsNotReplayedAndReachesPeers(t *testing.T) {
	b := NewBroker()
	peerCh, cancelPeer := b.Subscribe(true)
	defer cancelPeer()
	b.PublishAlert("mlab")
	if h, got := alertHint(t, peerCh); !got || h.Host != "mlab" {
		t.Fatalf("a peer stream must get the hint: %+v %v", h, got)
	}
	late, cancelLate := b.Subscribe(false)
	defer cancelLate()
	select {
	case e := <-late:
		t.Fatalf("a new stream started with a cached %q event", e.Type)
	case <-time.After(30 * time.Millisecond):
	}
}

// A change made while the gate was shut is never announced; once the gate opens, the next call (even an unchanged refresh) sends the
// hint once, so a reader that stayed connected re-reads the list.
func TestAlertBookHintsOnceAfterTheGateOpensForAMissedChange(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe(false)
	defer cancel()
	w := &fakeWriter{changed: true}
	var open atomic.Bool
	book := NewAlertBook(w, "mlab", b, open.Load, nil)
	a := wire.Alert{Key: "disk_low:/", Kind: wire.AlertDiskLow, Level: wire.AlertPush}
	book.Raise(a) // gate shut: missed
	open.Store(true)
	w.changed = false // the same alert again: nothing new in the table
	book.Raise(a)
	if _, got := alertHint(t, ch); !got {
		t.Fatal("the missed change is announced once the gate is open")
	}
	book.Raise(a)
	if _, got := alertHint(t, ch); got {
		t.Fatal("announced only once")
	}
}
