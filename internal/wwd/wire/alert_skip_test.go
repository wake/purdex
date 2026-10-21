package wire

import (
	"errors"
	"fmt"
	"strings"
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
		// an alert that is of a later release and broken in some other way is a broken alert (codex R1): the kind and level come last
		"unknown kind, bad detail":       {Key: "k", Kind: "meltdown", Level: AlertPush, Since: at, Last: at, Detail: "a\x07"},
		"unknown kind, no since":         {Key: "k", Kind: "meltdown", Level: AlertPush, Last: at},
		"unknown kind, last first":       {Key: "k", Kind: "meltdown", Level: AlertPush, Since: at, Last: at.Add(-time.Hour)},
		"unknown kind, cleared early":    {Key: "k", Kind: "meltdown", Level: AlertPush, Since: at, Last: at, ClearedAt: &[]time.Time{at.Add(-time.Hour)}[0]},
		"unknown level, bad detail":      {Key: "k", Kind: AlertThrash, Level: "urgent", Since: at, Last: at, Detail: "a\x07"},
		"unknown level, last first":      {Key: "k", Kind: AlertThrash, Level: "urgent", Since: at, Last: at.Add(-time.Hour)},
		"unknown kind and level, no key": {Kind: "meltdown", Level: "urgent", Since: at, Last: at},
		// a kind or level that is missing, or is not a short line of text, is not a name of a later release (codex R2)
		"no kind":                    {Key: "k", Level: AlertPush, Since: at, Last: at},
		"no level":                   {Key: "k", Kind: AlertThrash, Since: at, Last: at},
		"an unknown kind, no level":  {Key: "k", Kind: "meltdown", Since: at, Last: at},
		"an unknown level, no kind":  {Key: "k", Level: "urgent", Since: at, Last: at},
		"a kind too long":            {Key: "k", Kind: AlertKind(strings.Repeat("x", MaxAlertNameBytes+1)), Level: AlertPush, Since: at, Last: at},
		"a level too long":           {Key: "k", Kind: AlertThrash, Level: AlertLevel(strings.Repeat("x", MaxAlertNameBytes+1)), Since: at, Last: at},
		"a kind with a control char": {Key: "k", Kind: "melt\x07down", Level: AlertPush, Since: at, Last: at},
		"a level that is not UTF-8":  {Key: "k", Kind: AlertThrash, Level: "ur\xffgent", Since: at, Last: at},
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

// A peer that sends a different unknown name at every read cannot grow the reader or its log without end (codex R2).
func TestAlertSkipsIsBounded(t *testing.T) {
	var s AlertSkips
	said := 0
	for i := 0; i < 10*MaxAlertSkips; i++ {
		if _, first := s.First(Alert{Key: "k", Kind: AlertKind(fmt.Sprintf("kind_%d", i))}, ErrUnknownAlertKind); first {
			said++
		}
	}
	if said != MaxAlertSkips+1 {
		t.Fatalf("said %d times, want one for each of the first %d and one that says there are more", said, MaxAlertSkips)
	}
	if len(s.seen) != MaxAlertSkips {
		t.Fatalf("remembers %d names, want %d", len(s.seen), MaxAlertSkips)
	}
	// one that is remembered is still known, and is not said again
	if _, first := s.First(Alert{Key: "k", Kind: "kind_0"}, ErrUnknownAlertKind); first {
		t.Error("a remembered name was said again")
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
