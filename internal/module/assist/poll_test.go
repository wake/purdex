package assistmod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
)

func TestPollerRecordsChecksUnderTheGenAndReadinessFailures(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On = true
		f.Hosts["air26"] = h
		f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "t1"}}
	})
	entries := []gate.AssistEntry{{Kind: "team", ID: "t1"}}
	usable := func() (bool, string) {
		return gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", entries, "purdex")
	}
	// good JSON
	r.script("assist-check", checkOut("on", map[string]bool{"purdex": true, "other": false}), 0)
	r.m.pass()
	if ok, why := usable(); !ok {
		t.Fatalf("good check: %s", why)
	}
	if ok, why := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", entries, "other"); ok || !strings.Contains(why, "not cloned") {
		t.Fatalf("repo not ready: %v %q", ok, why)
	}
	if got := r.calls(); got[len(got)-1] != "assist-check --json host=air26" {
		t.Fatalf("%v", got)
	}
	// persisted for other readers
	if m, _ := gate.ReadReady(r.dir); m["air26"].Gen != r.file().Hosts["air26"].Gen {
		t.Fatalf("assist-ready.json: %+v", m)
	}
	// a check older than 90 s
	r.clock.add(91 * time.Second)
	if ok, _ := usable(); ok {
		t.Fatal("a 91 s old check counts")
	}
	r.clock.add(-91 * time.Second)
	// bad JSON, exit 2, timeout
	for name, set := range map[string]func(){
		"bad JSON": func() { r.script("assist-check", "not json", 0) },
		"exit 2":   func() { r.script("assist-check", "", 2) },
		"timeout": func() {
			r.script("assist-check", "", 0)
			r.m.timeouts.check = 200 * time.Millisecond
			r.flag("assist-check", "block", true)
		},
	} {
		set()
		r.m.pass()
		if ok, why := usable(); ok || !strings.HasPrefix(why, "check failed:") {
			t.Errorf("%s: %v %q", name, ok, why)
		}
		r.flag("assist-check", "block", false)
		r.m.timeouts.check = 5 * time.Second
	}
}

func TestPollerChecksOnlyHostsAskedOnBusyOrNotSayingOff(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.script("assist-check", checkOut("off", nil), 0)
	r.m.pass() // never checked: once
	r.m.pass() // says off at the current gen: not again
	if n := r.count("assist-check"); n != 1 {
		t.Fatalf("an off host was checked %d times", n)
	}
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On = true
		h.Gen = f.TakeGen()
		f.Hosts["air26"] = h
	})
	r.m.pass() // asked on: every pass
	r.m.pass()
	if n := r.count("assist-check"); n != 3 {
		t.Fatalf("an on host was checked %d times in all", n)
	}
}

// A check begun for an older generation is recorded under it and does not count for the new one.

func TestStaleGenCheckDoesNotCountAfterASwitch(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.liveTeam("t1", "alpha", "", "l1")
	r.do("POST", "/api/assist/allow", allowReq("team", "t1"))
	r.script("assist-check", checkOut("on", map[string]bool{"purdex": true}), 0)
	r.m.pass()
	old := r.m.readyCopy()["air26"].Gen
	if code, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); code != 200 {
		t.Fatal(code)
	}
	entries := []gate.AssistEntry{{Kind: "team", ID: "t1"}}
	if ok, _ := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", entries, "purdex"); ok || r.file().Hosts["air26"].Gen == old {
		t.Fatal("usable on the check of an older generation")
	}
	r.m.pass()
	if ok, why := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", entries, "purdex"); !ok {
		t.Fatalf("after the new check: %s", why)
	}
}

func TestMalformedFileSendsNothing(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	os.WriteFile(filepath.Join(r.dir, "assist.json"), []byte("{broken"), 0o644)
	if code, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); code == 200 {
		t.Fatal("switch on a broken file")
	}
	r.m.pass()
	if r.count("assist-") != 0 {
		t.Fatalf("ran %v", r.calls())
	}
}

func TestNewerVersionRefusesSwitch(t *testing.T) {
	r := newRig(t)
	os.WriteFile(filepath.Join(r.dir, "assist.json"), []byte(`{"Version":99,"Hosts":{"air26":{"SSH":"air26","Scripts":"/x"}}}`), 0o644)
	if code, b := r.do("POST", "/api/assist/switch", sw("air26", true)); code != 409 || b["error"] != "assist_version" {
		t.Fatalf("%d %v", code, b)
	}
}

// An answer for another host than the one asked is no check of this one.
func TestCheckAnswerForAnotherHostIsRefused(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On = true
		f.Hosts["air26"] = h
		f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "t1"}}
	})
	r.script("assist-check", strings.Replace(checkOut("on", map[string]bool{"purdex": true}), `"air26"`, `"mserv"`, 1), 0)
	r.m.pass()
	ok, why := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", []gate.AssistEntry{{Kind: "team", ID: "t1"}}, "purdex")
	if ok || !strings.HasPrefix(why, "check failed:") {
		t.Fatalf("%v %q", ok, why)
	}
}
