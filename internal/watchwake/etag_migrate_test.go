package watchwake

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/watchsettings"
)

// The etag is of the normalized document, so the order of the keys a client sent does not matter, and any value that changes does.
func TestEtag_DoesNotDependOnTheOrderOfTheKeys(t *testing.T) {
	a := `{"version":1,"enabled":true,"auto_start":true,"rules":{"patrol":{"prompt":"","interval_min":{"resting":10,"present":30,"away":30,"busy":10,"calm":15},"max_gap_hours":24,"times":[],"tz":"UTC"},"disk_low":{"enabled":true,"prompt":"","below_gb":14},"wwd_alert":{"enabled":true,"prompt":"","levels":["push","digest","watch"],"kinds_off":[]},"unattended_changed":{"enabled":true,"prompt":""},"user_back":{"enabled":true,"prompt":"","away_min":30}}}`
	b := `{"rules":{"user_back":{"away_min":30,"prompt":"","enabled":true},"unattended_changed":{"prompt":"","enabled":true},"wwd_alert":{"kinds_off":[],"levels":["push","digest","watch"],"prompt":"","enabled":true},"disk_low":{"below_gb":14,"prompt":"","enabled":true},"patrol":{"tz":"UTC","times":[],"max_gap_hours":24,"interval_min":{"calm":15,"busy":10,"away":30,"present":30,"resting":10},"prompt":""}},"auto_start":true,"enabled":true,"version":1}`
	sa, ea := Normalize([]byte(a))
	sb, eb := Normalize([]byte(b))
	if len(ea)+len(eb) != 0 {
		t.Fatalf("%+v %+v", ea, eb)
	}
	if Etag(sa) != Etag(sb) {
		t.Fatalf("the same document has two etags: %s %s", Etag(sa), Etag(sb))
	}
	if e := Etag(sa); len(e) != 16 || strings.Trim(e, "0123456789abcdef") != "" {
		t.Fatalf("16 hex: %q", e)
	}
}

func TestEtag_AnyValueChangesIt(t *testing.T) {
	base := Default("UTC")
	seen := map[string]string{Etag(base): "base"}
	mutations := map[string]func(*Settings){
		"enabled":       func(s *Settings) { s.Enabled = false },
		"auto_start":    func(s *Settings) { s.AutoStart = false },
		"prompt":        func(s *Settings) { s.Rules.Patrol.Prompt = "x" },
		"interval":      func(s *Settings) { s.Rules.Patrol.IntervalMin.Calm = 16 },
		"max gap":       func(s *Settings) { s.Rules.Patrol.MaxGapHours = 25 },
		"times":         func(s *Settings) { s.Rules.Patrol.Times = []string{"09:00"} },
		"tz":            func(s *Settings) { s.Rules.Patrol.TZ = "Asia/Taipei" },
		"disk":          func(s *Settings) { s.Rules.DiskLow.BelowGB = 15 },
		"levels":        func(s *Settings) { s.Rules.WwdAlert.Levels = []string{"push"} },
		"kinds":         func(s *Settings) { s.Rules.WwdAlert.KindsOff = []string{"swap_growth"} },
		"unattended":    func(s *Settings) { s.Rules.UnattendedChanged.Enabled = false },
		"user back":     func(s *Settings) { s.Rules.UserBack.AwayMin = 31 },
		"user back off": func(s *Settings) { s.Rules.UserBack.Enabled = false },
	}
	for name, mutate := range mutations {
		s := Default("UTC")
		mutate(&s)
		e := Etag(s)
		if prev, dup := seen[e]; dup {
			t.Errorf("%s has the etag of %s", name, prev)
		}
		seen[e] = name
	}
}

// Canonical JSON is what the store keeps and what the etag is of: normalizing it again changes nothing.
func TestMarshal_IsCanonicalAndStable(t *testing.T) {
	s := Default("Asia/Taipei")
	s.Rules.Patrol.Times = []string{"09:00", "21:30"}
	raw := Marshal(s)
	back, errs := Normalize(raw)
	if len(errs) != 0 {
		t.Fatalf("%+v", errs)
	}
	if string(Marshal(back)) != string(raw) || Etag(back) != Etag(s) {
		t.Fatal("not stable")
	}
	if !strings.Contains(string(raw), `"times":["09:00","21:30"]`) || strings.Contains(string(raw), "null") {
		t.Fatalf("%s", raw)
	}
}

func watchRow() watchsettings.Settings {
	w := watchsettings.Default()
	w.Enabled = false // the patrol cannot be switched off: this must not travel
	w.AutoWake = false
	w.PatrolPresentMin = 45
	w.PatrolBusyMin = 7
	w.PatrolCalmMin = 20
	w.PatrolRestMin = 12
	w.DiskLowGB = 20
	return w
}

func TestFromWatchRow_MapsTheOldFieldsAndLeavesEnabledBehind(t *testing.T) {
	s := FromWatchRow(watchRow(), "Asia/Taipei")
	if s.AutoStart {
		t.Error("auto_wake false -> auto_start false")
	}
	p := s.Rules.Patrol
	if p.IntervalMin != (Intervals{Resting: 12, Present: 45, Away: 45, Busy: 7, Calm: 20}) {
		t.Errorf("intervals %+v (away starts as present)", p.IntervalMin)
	}
	if s.Rules.DiskLow.BelowGB != 20 || p.TZ != "Asia/Taipei" {
		t.Errorf("%+v tz=%s", s.Rules.DiskLow, p.TZ)
	}
	// watch.enabled=false must not become any kind of off: the master switch and every rule stay on, and the patrol has no switch
	if !s.Enabled || !s.Rules.DiskLow.Enabled || !s.Rules.WwdAlert.Enabled || !s.Rules.UnattendedChanged.Enabled || !s.Rules.UserBack.Enabled {
		t.Errorf("watch.enabled travelled: %+v", s)
	}
	// the rest is the defaults of the document
	d := Default("Asia/Taipei")
	if p.MaxGapHours != d.Rules.Patrol.MaxGapHours || s.Rules.UserBack.AwayMin != d.Rules.UserBack.AwayMin || len(s.Rules.WwdAlert.Levels) != 3 {
		t.Errorf("%+v", s)
	}
	if _, errs := Normalize(Marshal(s)); len(errs) != 0 {
		t.Fatalf("a migrated document is valid: %+v", errs)
	}
}

func TestFromWatchRow_AutoWakeTrueStaysTrue(t *testing.T) {
	w := watchRow()
	w.AutoWake = true
	if !FromWatchRow(w, "UTC").AutoStart {
		t.Fatal("auto_wake true -> auto_start true")
	}
}

// A value of the old row that the new document would refuse (the old ranges were 1-1440 and a floor of 1 GB) is still valid there;
// a zero or out-of-range one (a row that was never validated) is replaced by the default instead of producing a document nobody can read.
func TestFromWatchRow_AnOutOfRangeOldValueFallsBackToTheDefault(t *testing.T) {
	w := watchRow()
	w.PatrolPresentMin = 5000
	w.DiskLowGB = 0
	s := FromWatchRow(w, "UTC")
	d := Default("UTC")
	if s.Rules.Patrol.IntervalMin.Present != d.Rules.Patrol.IntervalMin.Present || s.Rules.Patrol.IntervalMin.Away != d.Rules.Patrol.IntervalMin.Present || s.Rules.DiskLow.BelowGB != d.Rules.DiskLow.BelowGB {
		t.Fatalf("%+v", s.Rules)
	}
	if _, errs := Normalize(Marshal(s)); len(errs) != 0 {
		t.Fatalf("%+v", errs)
	}
}
