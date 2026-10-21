package watchwake

import (
	"encoding/json"
	"strings"
	"testing"
)

// goodDoc is Default() as the JSON a client sends: every field present (a PUT replaces the whole document).
func goodDoc(t *testing.T) map[string]any {
	t.Helper()
	b, err := json.Marshal(Default("Asia/Taipei"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// dig walks a path of object keys and returns the object holding the last key.
func dig(t *testing.T, m map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := m
	for _, p := range path {
		next, ok := cur[p].(map[string]any)
		if !ok {
			t.Fatalf("no object at %s", p)
		}
		cur = next
	}
	return cur
}

func normalize(t *testing.T, m map[string]any) (Settings, []FieldError) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return Normalize(b)
}

func wantOne(t *testing.T, errs []FieldError, path, code string) {
	t.Helper()
	if len(errs) != 1 || errs[0].Path != path || errs[0].Code != code || errs[0].Message == "" {
		t.Fatalf("want one %s at %s, got %+v", code, path, errs)
	}
}

func TestNormalize_TheDefaultIsValidAndRoundTrips(t *testing.T) {
	s, errs := normalize(t, goodDoc(t))
	if len(errs) != 0 {
		t.Fatalf("%+v", errs)
	}
	if s.Version != Version || !s.Enabled || !s.AutoStart || s.Rules.Patrol.MaxGapHours != 24 || s.Rules.Patrol.TZ != "Asia/Taipei" {
		t.Fatalf("%+v", s)
	}
	if s.Rules.Patrol.Times == nil || s.Rules.WwdAlert.KindsOff == nil || s.Rules.WwdAlert.Levels == nil {
		t.Fatal("lists are never nil: they are [] on the wire")
	}
}

func TestNormalize_Defaults(t *testing.T) {
	d := Default("UTC")
	p := d.Rules.Patrol
	if p.IntervalMin != (Intervals{Resting: 10, Present: 30, Away: 30, Busy: 10, Calm: 15}) || p.MaxGapHours != 24 || len(p.Times) != 0 {
		t.Fatalf("%+v", p)
	}
	if d.Rules.DiskLow.BelowGB != 14 || d.Rules.UserBack.AwayMin != 30 || len(d.Rules.WwdAlert.Levels) != 3 {
		t.Fatalf("%+v", d.Rules)
	}
}

func TestNormalize_MissingFieldNamesThePath(t *testing.T) {
	m := goodDoc(t)
	delete(dig(t, m, "rules", "patrol"), "max_gap_hours")
	_, errs := normalize(t, m)
	wantOne(t, errs, "rules.patrol.max_gap_hours", "missing_field")

	m = goodDoc(t)
	delete(m, "rules")
	_, errs = normalize(t, m)
	wantOne(t, errs, "rules", "missing_field")
}

func TestNormalize_UnknownFieldIncludingPatrolEnabled(t *testing.T) {
	m := goodDoc(t)
	dig(t, m, "rules", "patrol")["enabled"] = false // the patrol has no switch: it cannot be removed
	_, errs := normalize(t, m)
	wantOne(t, errs, "rules.patrol.enabled", "unknown_field")

	m = goodDoc(t)
	m["extra"] = 1
	_, errs = normalize(t, m)
	wantOne(t, errs, "extra", "unknown_field")
}

func TestNormalize_BadType(t *testing.T) {
	m := goodDoc(t)
	m["enabled"] = "yes"
	_, errs := normalize(t, m)
	wantOne(t, errs, "enabled", "bad_type")

	m = goodDoc(t)
	dig(t, m, "rules", "disk_low")["below_gb"] = 14.5 // an integer field
	_, errs = normalize(t, m)
	wantOne(t, errs, "rules.disk_low.below_gb", "bad_type")

	m = goodDoc(t)
	dig(t, m, "rules", "patrol")["times"] = "09:00"
	_, errs = normalize(t, m)
	wantOne(t, errs, "rules.patrol.times", "bad_type")

	_, errs = Normalize([]byte(`[1]`))
	wantOne(t, errs, "", "bad_type")
	_, errs = Normalize([]byte(`{`))
	wantOne(t, errs, "", "bad_type")
}

func TestNormalize_Ranges(t *testing.T) {
	cases := []struct {
		path  []string
		field string
		value int
		ok    bool
	}{
		{[]string{"rules", "patrol", "interval_min"}, "present", 0, true}, // 0 = only the max gap
		{[]string{"rules", "patrol", "interval_min"}, "present", 1, true},
		{[]string{"rules", "patrol", "interval_min"}, "present", 1440, true},
		{[]string{"rules", "patrol", "interval_min"}, "present", 1441, false},
		{[]string{"rules", "patrol", "interval_min"}, "away", -1, false},
		{[]string{"rules", "patrol"}, "max_gap_hours", 0, false}, // the patrol cannot be switched off by a gap of 0
		{[]string{"rules", "patrol"}, "max_gap_hours", 1, true},
		{[]string{"rules", "patrol"}, "max_gap_hours", 72, true},
		{[]string{"rules", "patrol"}, "max_gap_hours", 73, false},
		{[]string{"rules", "disk_low"}, "below_gb", 0, false},
		{[]string{"rules", "disk_low"}, "below_gb", 1, true},
		{[]string{"rules", "disk_low"}, "below_gb", 100000, true},
		{[]string{"rules", "disk_low"}, "below_gb", 100001, false},
		{[]string{"rules", "user_back"}, "away_min", 4, false},
		{[]string{"rules", "user_back"}, "away_min", 5, true},
		{[]string{"rules", "user_back"}, "away_min", 1440, true},
		{[]string{"rules", "user_back"}, "away_min", 1441, false},
	}
	for _, c := range cases {
		m := goodDoc(t)
		dig(t, m, c.path...)[c.field] = c.value
		_, errs := normalize(t, m)
		if c.ok && len(errs) != 0 {
			t.Errorf("%v.%s=%d: %+v", c.path, c.field, c.value, errs)
		}
		if !c.ok {
			wantOne(t, errs, strings.Join(c.path, ".")+"."+c.field, "out_of_range")
		}
	}
}

func TestNormalize_TimesAreSortedAndChecked(t *testing.T) {
	m := goodDoc(t)
	dig(t, m, "rules", "patrol")["times"] = []any{"21:30", "09:00", "00:00", "23:59"}
	s, errs := normalize(t, m)
	if len(errs) != 0 {
		t.Fatalf("%+v", errs)
	}
	if got := strings.Join(s.Rules.Patrol.Times, ","); got != "00:00,09:00,21:30,23:59" {
		t.Fatalf("sorted: %s", got)
	}

	for _, bad := range []string{"9:00", "24:00", "09:60", "09:00:00", "0900", "", "ab:cd", " 09:00"} {
		m := goodDoc(t)
		dig(t, m, "rules", "patrol")["times"] = []any{"08:00", bad}
		_, errs := normalize(t, m)
		wantOne(t, errs, "rules.patrol.times[1]", "bad_time")
	}

	m = goodDoc(t)
	dig(t, m, "rules", "patrol")["times"] = []any{"09:00", "10:00", "09:00"}
	_, errs = normalize(t, m)
	wantOne(t, errs, "rules.patrol.times[2]", "duplicate_time")

	m = goodDoc(t)
	var many []any
	for h := 0; h < 25; h++ {
		many = append(many, strings.Repeat("0", 0)+twoDigits(h%24)+":"+twoDigits(h/24*30))
	}
	dig(t, m, "rules", "patrol")["times"] = many // 25 entries: 24 is the limit
	_, errs = normalize(t, m)
	if len(errs) == 0 || errs[0].Path != "rules.patrol.times" || errs[0].Code != "too_many" {
		t.Fatalf("%+v", errs)
	}
}

func twoDigits(n int) string {
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}

func TestNormalize_Tz(t *testing.T) {
	for _, tz := range []string{"Asia/Taipei", "America/New_York", "UTC"} {
		m := goodDoc(t)
		dig(t, m, "rules", "patrol")["tz"] = tz
		if _, errs := normalize(t, m); len(errs) != 0 {
			t.Errorf("%s: %+v", tz, errs)
		}
	}
	for _, tz := range []string{"", "Mars/Olympus", "Local", "../etc/passwd"} {
		m := goodDoc(t)
		dig(t, m, "rules", "patrol")["tz"] = tz
		_, errs := normalize(t, m)
		wantOne(t, errs, "rules.patrol.tz", "bad_tz")
	}
}

func TestNormalize_Prompt(t *testing.T) {
	m := goodDoc(t)
	dig(t, m, "rules", "patrol")["prompt"] = "第一行\n\t第二行 with tab"
	if _, errs := normalize(t, m); len(errs) != 0 {
		t.Fatalf("multi-line with tab is fine: %+v", errs)
	}
	m = goodDoc(t)
	dig(t, m, "rules", "patrol")["prompt"] = strings.Repeat("a", 2000)
	if _, errs := normalize(t, m); len(errs) != 0 {
		t.Fatalf("2000 bytes is the limit: %+v", errs)
	}
	m = goodDoc(t)
	dig(t, m, "rules", "disk_low")["prompt"] = strings.Repeat("a", 2001)
	_, errs := normalize(t, m)
	wantOne(t, errs, "rules.disk_low.prompt", "prompt_too_long")
	m = goodDoc(t)
	dig(t, m, "rules", "user_back")["prompt"] = strings.Repeat("好", 667) // 2001 bytes
	_, errs = normalize(t, m)
	wantOne(t, errs, "rules.user_back.prompt", "prompt_too_long")

	for _, bad := range []string{"a\x00b", "a\rb", "a\x1bb", "a b", "a b", "a\x7fb"} {
		m := goodDoc(t)
		dig(t, m, "rules", "unattended_changed")["prompt"] = bad
		_, errs := normalize(t, m)
		wantOne(t, errs, "rules.unattended_changed.prompt", "prompt_bad_char")
	}
}

func TestNormalize_LevelsAndKinds(t *testing.T) {
	m := goodDoc(t)
	dig(t, m, "rules", "wwd_alert")["levels"] = []any{"watch", "push"}
	s, errs := normalize(t, m)
	if len(errs) != 0 || strings.Join(s.Rules.WwdAlert.Levels, ",") != "push,watch" {
		t.Fatalf("a subset is fine and is kept in the canonical order: %+v %+v", s.Rules.WwdAlert.Levels, errs)
	}
	m = goodDoc(t)
	dig(t, m, "rules", "wwd_alert")["levels"] = []any{}
	if s, errs := normalize(t, m); len(errs) != 0 || s.Rules.WwdAlert.Levels == nil || len(s.Rules.WwdAlert.Levels) != 0 {
		t.Fatalf("an empty list is fine: %+v", errs)
	}
	for _, bad := range [][]any{{"loud"}, {"push", "push"}, {"Push"}} {
		m := goodDoc(t)
		dig(t, m, "rules", "wwd_alert")["levels"] = bad
		_, errs := normalize(t, m)
		if len(errs) != 1 || errs[0].Code != "bad_level" || !strings.HasPrefix(errs[0].Path, "rules.wwd_alert.levels[") {
			t.Fatalf("%v: %+v", bad, errs)
		}
	}

	m = goodDoc(t)
	dig(t, m, "rules", "wwd_alert")["kinds_off"] = []any{"swap_growth", "silence:air26", "a-b_c"} // not on any list: an unknown kind is kept
	if s, errs := normalize(t, m); len(errs) != 0 || len(s.Rules.WwdAlert.KindsOff) != 3 {
		t.Fatalf("%+v %+v", s.Rules.WwdAlert.KindsOff, errs)
	}
	for _, bad := range []string{"", "Upper", "1abc", "has space", strings.Repeat("a", 65)} {
		m := goodDoc(t)
		dig(t, m, "rules", "wwd_alert")["kinds_off"] = []any{bad}
		_, errs := normalize(t, m)
		wantOne(t, errs, "rules.wwd_alert.kinds_off[0]", "bad_kind")
	}
	m = goodDoc(t)
	dig(t, m, "rules", "wwd_alert")["kinds_off"] = []any{"a_b", "a_b"}
	_, errs = normalize(t, m)
	wantOne(t, errs, "rules.wwd_alert.kinds_off[1]", "bad_kind")
	m = goodDoc(t)
	var many []any
	for i := 0; i < 33; i++ {
		many = append(many, "k"+twoDigits(i))
	}
	dig(t, m, "rules", "wwd_alert")["kinds_off"] = many
	_, errs = normalize(t, m)
	if len(errs) != 1 || errs[0].Path != "rules.wwd_alert.kinds_off" || errs[0].Code != "too_many" {
		t.Fatalf("%+v", errs)
	}
}

func TestNormalize_VersionOtherThanOneIsBadVersion(t *testing.T) {
	for _, v := range []any{0, 2, "1", 1.5} {
		m := goodDoc(t)
		m["version"] = v
		_, errs := normalize(t, m)
		if len(errs) != 1 || errs[0].Path != "version" || errs[0].Code != "bad_version" {
			t.Fatalf("%v: %+v", v, errs)
		}
	}
}

// One answer lists every problem, in a stable order.
func TestNormalize_CollectsEveryError(t *testing.T) {
	m := goodDoc(t)
	m["enabled"] = 1
	dig(t, m, "rules", "patrol")["max_gap_hours"] = 0
	dig(t, m, "rules", "patrol")["times"] = []any{"9:00", "10:00", "10:00"}
	dig(t, m, "rules", "disk_low")["below_gb"] = 0
	dig(t, m, "rules", "patrol")["bogus"] = true
	_, errs := normalize(t, m)
	got := map[string]string{}
	for _, e := range errs {
		got[e.Path] = e.Code
	}
	want := map[string]string{
		"enabled":                    "bad_type",
		"rules.patrol.max_gap_hours": "out_of_range",
		"rules.patrol.times[0]":      "bad_time",
		"rules.patrol.times[2]":      "duplicate_time",
		"rules.disk_low.below_gb":    "out_of_range",
		"rules.patrol.bogus":         "unknown_field",
	}
	if len(got) != len(want) || len(errs) != len(want) {
		t.Fatalf("got %+v", errs)
	}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("%s: %q, want %q", p, got[p], c)
		}
	}
	_, again := normalize(t, m)
	for i := range errs {
		if errs[i] != again[i] {
			t.Fatalf("the order is not stable: %+v vs %+v", errs, again)
		}
	}
}

func TestPeekVersion(t *testing.T) {
	if v, ok := PeekVersion([]byte(`{"version":7,"x":1}`)); !ok || v != 7 {
		t.Fatalf("%d %v", v, ok)
	}
	for _, raw := range []string{`{}`, `{"version":"1"}`, `[]`, `x`, `{"version":1.5}`} {
		if _, ok := PeekVersion([]byte(raw)); ok {
			t.Errorf("%s", raw)
		}
	}
}
