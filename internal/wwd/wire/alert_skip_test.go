package wire

import (
	"errors"
	"testing"
	"time"
)

func TestTheQueueKindsAreValidAndTheirSpellingsAreContract(t *testing.T) {
	at := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	want := map[AlertKind]string{
		AlertQueueStalled: "queue_stalled", AlertJobRequeued: "job_requeued", AlertQueueBacklog: "queue_backlog", AlertQueueUnclaimable: "queue_unclaimable",
	}
	for k, s := range want {
		if string(k) != s {
			t.Errorf("%q is spelt %q", s, k)
		}
		if !k.Valid() {
			t.Errorf("%s is not valid", k)
		}
		if err := (Alert{Key: s, Kind: k, Level: AlertWatch, Since: at, Last: at}).Validate(); err != nil {
			t.Errorf("%s at level watch: %v", s, err)
		}
	}
}

func TestValidateNamesAnUnknownKindOrLevelAndNothingElseAsUnknown(t *testing.T) {
	at := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	ok := Alert{Key: "k", Kind: AlertThrash, Level: AlertPush, Since: at, Last: at}
	bad := ok
	bad.Kind = "meltdown"
	if err := bad.Validate(); !errors.Is(err, ErrUnknownAlertKind) || !IsUnknownAlert(err) {
		t.Errorf("unknown kind: %v", err)
	}
	bad = ok
	bad.Level = "urgent"
	if err := bad.Validate(); !errors.Is(err, ErrUnknownAlertLevel) || !IsUnknownAlert(err) {
		t.Errorf("unknown level: %v", err)
	}
	for name, a := range map[string]Alert{
		"no key":      {Kind: AlertThrash, Level: AlertPush, Since: at, Last: at},
		"no since":    {Key: "k", Kind: AlertThrash, Level: AlertPush, Last: at},
		"last first":  {Key: "k", Kind: AlertThrash, Level: AlertPush, Since: at, Last: at.Add(-time.Hour)},
		"bad detail":  {Key: "k", Kind: AlertThrash, Level: AlertPush, Since: at, Last: at, Detail: "a\x07"},
		"no key, too": {Kind: "meltdown", Level: AlertPush, Since: at, Last: at}, // the key is checked first
	} {
		if err := a.Validate(); err == nil || IsUnknownAlert(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseAlertKeepsWhatItCouldReadOfAnUnknownOne(t *testing.T) {
	a, err := ParseAlert([]byte(`{"key":"k","kind":"meltdown","level":"push","since":"2026-10-19T04:00:00Z","last":"2026-10-19T04:00:00Z","cleared_at":null,"newer":1}`))
	if !IsUnknownAlert(err) || a.Key != "k" || a.Kind != "meltdown" {
		t.Fatalf("%+v %v", a, err)
	}
	for name, raw := range map[string]string{
		"not an object": `"x"`, "a wrong type": `{"key":5}`, "trailing": `{"key":"k"} {}`, "empty": ``,
	} {
		if _, err := ParseAlert([]byte(raw)); err == nil || IsUnknownAlert(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAlertSkipsSaysEachKindOrLevelOnce(t *testing.T) {
	var s AlertSkips
	kind := Alert{Key: "k", Kind: "meltdown"}
	level := Alert{Key: "k", Kind: AlertThrash, Level: "urgent"}
	if what, first := s.First(kind, ErrUnknownAlertKind); !first || what != `kind "meltdown"` {
		t.Errorf("%q %v", what, first)
	}
	if _, first := s.First(kind, ErrUnknownAlertKind); first {
		t.Error("said twice")
	}
	if what, first := s.First(level, ErrUnknownAlertLevel); !first || what != `level "urgent"` {
		t.Errorf("%q %v", what, first)
	}
	if _, first := s.First(Alert{Kind: "other"}, ErrUnknownAlertKind); !first {
		t.Error("another kind is said")
	}
	if _, first := s.First(kind, errors.New("something else")); first {
		t.Error("an error that is not an unknown kind is not for this")
	}
}
