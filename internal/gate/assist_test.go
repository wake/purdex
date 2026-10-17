package gate

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fullHost() AssistHost {
	return AssistHost{SSH: "air26", Scripts: "/s", Slots: 2, Can: []string{"vitest"}, Cannot: []string{"go vet"},
		LoadLimit: 1.5, IdleMin: 20, HoldMin: 7, On: true, Gen: 3, Op: &AssistOp{Kind: "on", ID: "op1", At: time.Unix(5, 0).UTC()},
		By: "s1", Reason: "r", At: time.Unix(9, 0).UTC()}
}

func TestAssistRoundTripKeepsEveryField(t *testing.T) {
	dir := t.TempDir()
	in := AssistFile{Version: AssistVersion, NextGen: 3, Hosts: map[string]AssistHost{"air26": fullHost()},
		Allowed: []AssistAllow{{Kind: "team", ID: "t1", Name: "x", Host: "air26", By: "b", Reason: "r", At: time.Unix(1, 0).UTC()}}}
	if _, err := EditAssist(dir, func(f *AssistFile) (bool, string, error) { *f = in; return true, "ok", nil }); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAssist(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := got.Hosts["air26"]
	if h.Slots != 2 || h.LoadLimit != 1.5 || h.IdleMin != 20 || h.HoldMin != 7 || h.Op == nil || h.Op.ID != "op1" || len(h.Can) != 1 || len(h.Cannot) != 1 || got.NextGen != 3 || got.Allowed[0].Name != "x" {
		t.Fatalf("round trip lost a field: %+v", got)
	}
}

func TestAssistMissingIsEmptyMalformedIsErrorAndNeverEdited(t *testing.T) {
	dir := t.TempDir()
	f, err := ReadAssist(dir)
	if err != nil || len(f.Hosts) != 0 {
		t.Fatalf("missing: %v %+v", err, f)
	}
	os.WriteFile(filepath.Join(dir, "assist.json"), []byte("{nope"), 0o644)
	if _, err := ReadAssist(dir); err == nil {
		t.Fatal("malformed file read without an error")
	}
	if _, err := EditAssist(dir, func(f *AssistFile) (bool, string, error) { return true, "", nil }); err == nil {
		t.Fatal("malformed file edited")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "assist.json")); string(b) != "{nope" {
		t.Fatalf("malformed file overwritten: %q", b)
	}
}

func TestAssistNewerVersionReadOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "assist.json"), []byte(`{"Version":99,"Hosts":{"a":{"SSH":"a"}}}`), 0o644)
	f, err := ReadAssist(dir)
	if err != nil || f.Hosts["a"].SSH != "a" {
		t.Fatalf("newer not read: %v", err)
	}
	if _, err := EditAssist(dir, func(f *AssistFile) (bool, string, error) { return true, "", nil }); err != ErrAssistNewer {
		t.Fatalf("edit of newer file: %v", err)
	}
}

func TestAssistConcurrentEditsBothLand(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			EditAssist(dir, func(f *AssistFile) (bool, string, error) {
				f.Allowed = append(f.Allowed, AssistAllow{Kind: "team", ID: id})
				return true, "", nil
			})
		}()
	}
	wg.Wait()
	f, _ := ReadAssist(dir)
	if len(f.Allowed) != 4 {
		t.Fatalf("lost an edit: %+v", f.Allowed)
	}
}

func TestAssistNextGenNeverReused(t *testing.T) {
	f := AssistFile{Hosts: map[string]AssistHost{}}
	g1 := f.TakeGen()
	g2 := f.TakeGen()
	if g1 != 1 || g2 != 2 || f.NextGen != 2 {
		t.Fatalf("%d %d %d", g1, g2, f.NextGen)
	}
}

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func usableFixture() (AssistFile, map[string]AssistReady, []AssistEntry) {
	f := AssistFile{Version: 1, Hosts: map[string]AssistHost{"air26": {SSH: "air26", On: true, Gen: 4}},
		Allowed: []AssistAllow{{Kind: "team", ID: "t1"}}}
	r := map[string]AssistReady{"air26": {Gen: 4, At: t0.Add(-10 * time.Second), Answer: AssistAnswer{Assist: true, Switch: "on",
		Repos: map[string]AssistRepo{"purdex": {Ready: true}, "other": {Ready: false, Why: "not cloned"}}}}}
	return f, r, []AssistEntry{{Kind: "team", ID: "t1"}}
}

func TestUsable(t *testing.T) {
	f, r, e := usableFixture()
	if ok, why := Usable(f, r, t0, "air26", e, "purdex"); !ok {
		t.Fatalf("should be usable: %s", why)
	}
	type fx struct {
		f *AssistFile
		r map[string]AssistReady
		e *[]AssistEntry
		n *time.Time
	}
	setReady := func(x fx, fn func(*AssistReady)) { v := x.r["air26"]; fn(&v); x.r["air26"] = v }
	cases := []struct {
		name string
		mut  func(fx)
		repo string
		why  string
	}{
		{"asked off", func(x fx) { h := x.f.Hosts["air26"]; h.On = false; x.f.Hosts["air26"] = h }, "purdex", "off"},
		{"op pending", func(x fx) { h := x.f.Hosts["air26"]; h.Op = &AssistOp{Kind: "on"}; x.f.Hosts["air26"] = h }, "purdex", "switch"},
		{"old gen", func(x fx) { setReady(x, func(v *AssistReady) { v.Gen = 3 }) }, "purdex", "no check"},
		{"stale", func(x fx) { *x.n = t0.Add(81 * time.Second) }, "purdex", "old"},
		{"host says off", func(x fx) { setReady(x, func(v *AssistReady) { v.Answer.Switch = "off" }) }, "purdex", "the host says off"},
		{"closing", func(x fx) { setReady(x, func(v *AssistReady) { v.Answer.Switch = "closing" }) }, "purdex", "closing"},
		{"repo not ready", func(x fx) {}, "other", "not cloned"},
		{"repo unknown", func(x fx) {}, "zzz", "not prepared"},
		{"not allowed", func(x fx) { *x.e = []AssistEntry{{Kind: "team", ID: "t2"}} }, "purdex", "not allowed"},
		{"allowed for another host", func(x fx) { x.f.Allowed[0].Host = "mserv" }, "purdex", "not allowed"},
		{"future check", func(x fx) { setReady(x, func(v *AssistReady) { v.At = t0.Add(time.Hour) }) }, "purdex", "too old"},
		{"assist missing", func(x fx) { setReady(x, func(v *AssistReady) { v.Answer.Assist = false }) }, "purdex", "not installed"},
		{"check failed", func(x fx) { setReady(x, func(v *AssistReady) { v.Err = "check failed: boom" }) }, "purdex", "check failed: boom"},
	}
	for _, c := range cases {
		f, r, e := usableFixture()
		now := t0
		c.mut(fx{&f, r, &e, &now})
		ok, why := Usable(f, r, now, "air26", e, c.repo)
		if ok || !strings.Contains(why, c.why) {
			t.Errorf("%s: ok=%v why=%q want %q", c.name, ok, why, c.why)
		}
	}
	if ok, _ := Usable(f, r, t0.Add(80*time.Second), "air26", e, "purdex"); !ok {
		t.Error("exactly 90 s old is still fresh")
	}
	if ok, _ := Usable(f, r, t0, "nohost", e, "purdex"); ok {
		t.Error("unknown host usable")
	}
}

func TestReadyFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if m, err := ReadReady(dir); err != nil || len(m) != 0 {
		t.Fatalf("%v %v", m, err)
	}
	in := map[string]AssistReady{"a": {Gen: 2, At: t0, Answer: AssistAnswer{Host: "a", Switch: "on"}}}
	if err := WriteReady(dir, in); err != nil {
		t.Fatal(err)
	}
	m, _ := ReadReady(dir)
	if m["a"].Gen != 2 || m["a"].Answer.Switch != "on" {
		t.Fatalf("%+v", m)
	}
}

func TestRepoFromURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:wake/purdex.git":        "purdex",
		"https://github.com/wake/purdex":        "purdex",
		"ssh://git@lab.protype.tw:9079/a/b.git": "b",
		"/srv/repos/thing.git/":                 "thing",
	}
	for in, want := range cases {
		if got := RepoFromURL(in); got != want {
			t.Errorf("%q → %q want %q", in, got, want)
		}
	}
}

func TestAssistReadRaisesNextGenToTheHighestHostGen(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "assist.json"), []byte(`{"Version":1,"Hosts":{"a":{"Gen":5}}}`), 0o644)
	f, err := ReadAssist(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g := f.TakeGen(); g != 6 {
		t.Fatalf("TakeGen = %d", g)
	}
}

// The script prints its own ssh alias as the host; that is not the key of assist.json and must not have to match it.
func TestUsableDoesNotRequireTheAnswerHostToEqualTheKey(t *testing.T) {
	f, r, e := usableFixture()
	v := r["air26"]
	v.Answer.Host = "air26-lan"
	r["air26"] = v
	if ok, why := Usable(f, r, t0, "air26", e, "purdex"); !ok {
		t.Fatal(why)
	}
}
