package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

type fakeWriter struct {
	raised  []wire.Alert
	cleared []string
	changed bool // what Raise and Clear answer
	err     error
	active  []wire.Alert // what ActiveAlerts answers
}

func (f *fakeWriter) RaiseAlert(a wire.Alert, at time.Time) (bool, error) {
	f.raised = append(f.raised, a)
	return f.changed, f.err
}

func (f *fakeWriter) ClearAlert(key string, at time.Time) (bool, error) {
	f.cleared = append(f.cleared, key)
	return f.changed, f.err
}

func (f *fakeWriter) ActiveAlerts() ([]wire.Alert, error) { return f.active, f.err }

// Active is the keys of what the table holds active, read fresh each time.
func TestAlertBook_ActiveIsWhatTheTableHolds(t *testing.T) {
	w := &fakeWriter{active: []wire.Alert{{Key: "a"}, {Key: "b"}}}
	book := NewAlertBook(w, "mlab", nil, nil)
	got, err := book.Active()
	if err != nil || len(got) != 2 || !got["a"] || !got["b"] {
		t.Fatalf("%v %v", got, err)
	}
	w.err = errors.New("locked")
	if _, err := book.Active(); err == nil {
		t.Fatal("an unreadable table must be an error, not an empty set")
	}
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

// Spec §13.3: the alert event is only a hint that a host's list changed. Alerts are not behind the proto 2 gate (§13.1): the table is
// written and the hint goes out whenever something a reader would notice changed, and not for a refresh.
func TestAlertBookHintsWhenSomethingChanged(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe(false)
	defer cancel()
	w := &fakeWriter{changed: true}
	book := NewAlertBook(w, "mlab", b, nil)
	a := wire.Alert{Key: "disk_low:/", Kind: wire.AlertDiskLow, Level: wire.AlertPush}

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
	if len(w.raised) != 2 || len(w.cleared) != 1 || w.cleared[0] != "disk_low:/" {
		t.Fatalf("raised %v cleared %v", w.raised, w.cleared)
	}
}

func TestAlertBookWriteFailureIsReturnedAndSaysNothing(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe(false)
	defer cancel()
	w := &fakeWriter{changed: true, err: errors.New("database is locked")}
	book := NewAlertBook(w, "mlab", b, nil)
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

// No broker: the table is still written, nothing is announced.
func TestAlertBookWithoutABrokerStillWrites(t *testing.T) {
	w := &fakeWriter{changed: true}
	book := NewAlertBook(w, "mlab", nil, nil)
	if err := book.Raise(wire.Alert{Key: "k", Kind: wire.AlertThrash, Level: wire.AlertDigest}); err != nil || len(w.raised) != 1 {
		t.Fatalf("%v %v", err, w.raised)
	}
}

// The hint goes to the socket stream only: a peer's TCP stream never carries it (an older peer's decoder would refuse the new type), and
// it is not cached for a late subscriber (who reads the list when it starts).
func TestAlertHintIsSocketOnlyAndNotReplayed(t *testing.T) {
	b := NewBroker()
	peerCh, cancelPeer := b.Subscribe(true)
	defer cancelPeer()
	localCh, cancelLocal := b.Subscribe(false)
	defer cancelLocal()
	b.PublishAlert("mlab")
	if h, got := alertHint(t, localCh); !got || h.Host != "mlab" {
		t.Fatalf("the socket stream must get the hint: %+v %v", h, got)
	}
	select {
	case e := <-peerCh:
		t.Fatalf("a peer stream was sent a %q event", e.Type)
	case <-time.After(30 * time.Millisecond):
	}
	late, cancelLate := b.Subscribe(false)
	defer cancelLate()
	select {
	case e := <-late:
		t.Fatalf("a new stream started with a cached %q event", e.Type)
	case <-time.After(30 * time.Millisecond):
	}
}
