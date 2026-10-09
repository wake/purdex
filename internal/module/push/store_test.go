package push

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/push"
)

func dev(token string) push.Device {
	return push.Device{
		DeviceID: push.DeviceID(token), Token: token, BundleID: push.BundleID, Env: "sandbox", Platform: "ios",
		DeviceName: "iPhone", HostLabel: "mlab", Locale: "zh-TW",
		Prefs: push.Prefs{Tabs: []string{"c1"}},
	}
}

var (
	tokA = strings.Repeat("a1", 32)
	tokB = strings.Repeat("b2", 32)
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStore_UpsertTwiceKeepsOneRowAndReplacesFields(t *testing.T) {
	s := newStore(t)
	s.now = func() int64 { return 1000 }
	first, err := s.Upsert(dev(tokA))
	if err != nil {
		t.Fatal(err)
	}
	if first.CreatedAt != 1000 || first.UpdatedAt != 1000 {
		t.Fatalf("first = %+v", first)
	}
	s.now = func() int64 { return 2000 }
	d := dev(tokA)
	d.DeviceName, d.Locale, d.Prefs = "Renamed", "en", push.Prefs{Tabs: []string{"x", "y"}}
	second, err := s.Upsert(d)
	if err != nil {
		t.Fatal(err)
	}
	if second.CreatedAt != 1000 || second.UpdatedAt != 2000 || second.DeviceName != "Renamed" || second.Locale != "en" || len(second.Prefs.Tabs) != 2 {
		t.Fatalf("second = %+v", second)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Token != tokA || list[0].DeviceID != push.DeviceID(tokA) {
		t.Fatalf("list = %+v err %v", list, err)
	}
}

func TestStore_ListIsOrderedByCreation(t *testing.T) {
	s := newStore(t)
	s.now = func() int64 { return 1 }
	s.Upsert(dev(tokB))
	s.now = func() int64 { return 2 }
	s.Upsert(dev(tokA))
	list, _ := s.List()
	if len(list) != 2 || list[0].Token != tokB || list[1].Token != tokA {
		t.Fatalf("order = %v / %v", list[0].Token[:4], list[1].Token[:4])
	}
}

func TestStore_DeleteByIDIsIdempotent(t *testing.T) {
	s := newStore(t)
	s.Upsert(dev(tokA))
	gone, err := s.DeleteByID(push.DeviceID(tokA))
	if err != nil || !gone {
		t.Fatalf("gone = %v err %v", gone, err)
	}
	gone, err = s.DeleteByID(push.DeviceID(tokA))
	if err != nil || gone {
		t.Fatalf("second delete: gone = %v err %v", gone, err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("list = %+v", list)
	}
}

func TestStore_MarkSentAndMarkError(t *testing.T) {
	s := newStore(t)
	s.Upsert(dev(tokA))
	id := push.DeviceID(tokA)
	if err := s.MarkError(id, "BadDeviceToken"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSent(id, 5000); err != nil {
		t.Fatal(err)
	}
	d := mustOne(t, s)
	if d.LastSentAt != 5000 || d.LastError != "" { // a success clears the last error
		t.Fatalf("after sent: %+v", d)
	}
	if err := s.MarkError(id, "Unregistered"); err != nil {
		t.Fatal(err)
	}
	if d := mustOne(t, s); d.LastError != "Unregistered" || d.LastSentAt != 5000 {
		t.Fatalf("after error: %+v", d)
	}
	// marking a device that is gone is not an error
	if err := s.MarkSent("deadbeefdeadbeef", 1); err != nil {
		t.Fatal(err)
	}
}

// A re-registration keeps the send history of the device.
func TestStore_UpsertKeepsLastSentAndError(t *testing.T) {
	s := newStore(t)
	s.Upsert(dev(tokA))
	s.MarkError(push.DeviceID(tokA), "boom")
	s.MarkSent(push.DeviceID(tokA), 9)
	s.MarkError(push.DeviceID(tokA), "later")
	s.Upsert(dev(tokA))
	if d := mustOne(t, s); d.LastSentAt != 9 || d.LastError != "later" {
		t.Fatalf("history lost: %+v", d)
	}
}

func TestStore_SurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(dev(tokA)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if list, _ := s.List(); len(list) != 1 || list[0].Prefs.Tabs[0] != "c1" {
		t.Fatalf("list after reopen = %+v", list)
	}
}

func mustOne(t *testing.T, s *Store) push.Device {
	t.Helper()
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v err %v", list, err)
	}
	return list[0]
}
