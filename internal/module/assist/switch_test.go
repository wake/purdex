package assistmod

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
)

func sw(host string, on bool) map[string]any {
	return map[string]any{"host": host, "on": on, "reason": "test"}
}

func TestSwitchOnOK(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	before := r.file().Hosts["air26"].Gen
	code, body := r.do("POST", "/api/assist/switch", sw("air26", true))
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	h := r.file().Hosts["air26"]
	if !h.On || h.Op != nil || h.Gen <= before || h.By == "" || h.Reason != "test" {
		t.Fatalf("host after on: %+v", h)
	}
	if got := r.calls(); len(got) != 1 || got[0] != "assist-on --json host=air26" {
		t.Fatalf("calls = %v", got)
	}
}

func TestSwitchOnFailingLeavesItOffWithTheDetail(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.script("assist-on", `{"ok":false,"switch":"off","detail":"no launchd"}`, 0)
	code, body := r.do("POST", "/api/assist/switch", sw("air26", true))
	if code != 502 || !strings.Contains(body["detail"].(string), "no launchd") {
		t.Fatalf("%d %v", code, body)
	}
	if h := r.file().Hosts["air26"]; h.On || h.Op != nil {
		t.Fatalf("%+v", h)
	}
	r.script("assist-on", "", 2)
	if code, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); code != 502 {
		t.Fatalf("exit 2: %d", code)
	}
}

func TestSwitchTimeoutEndsTheProcessGroup(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.m.timeouts.on = 300 * time.Millisecond
	r.flag("assist-on", "pidfile", true)
	r.flag("assist-on", "block", true)
	start := time.Now()
	code, _ := r.do("POST", "/api/assist/switch", sw("air26", true))
	if code != 502 || time.Since(start) > 4*time.Second {
		t.Fatalf("code %d after %v", code, time.Since(start))
	}
	b, err := os.ReadFile(filepath.Join(r.ctl, "assist-on.child"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	r.waitFor("the child of the script to be ended", func() bool { return syscall.Kill(pid, 0) != nil })
	if h := r.file().Hosts["air26"]; h.On || h.Op != nil {
		t.Fatalf("%+v", h)
	}
}

func TestSwitchOffIsOnDiskBeforeItsScriptRuns(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.edit(func(f *gate.AssistFile) { h := f.Hosts["air26"]; h.On = true; f.Hosts["air26"] = h })
	r.flag("assist-off", "block", true)
	done := make(chan int, 1)
	go func() { c, _ := r.do("POST", "/api/assist/switch", sw("air26", false)); done <- c }()
	r.waitFor("the off script to start", func() bool { return r.count("assist-off") == 1 })
	h := r.file().Hosts["air26"]
	if h.On || h.Op == nil || h.Op.Kind != "off" {
		t.Fatalf("while the script runs: %+v", h)
	}
	r.flag("assist-off", "block", false)
	if c := <-done; c != 200 {
		t.Fatalf("off: %d", c)
	}
	if h := r.file().Hosts["air26"]; h.Op != nil || h.On {
		t.Fatalf("after: %+v", h)
	}
	if got := r.calls(); got[0] != "assist-off --apply --json host=air26" {
		t.Fatalf("calls = %v", got)
	}
}

func TestSwitchBusyOnTheSameHostNotTheOther(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.addHost("mserv")
	r.flag("assist-on", "block", true)
	done := make(chan int, 1)
	go func() { c, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); done <- c }()
	r.waitFor("the script to start", func() bool { return r.count("assist-on") == 1 })
	if code, body := r.do("POST", "/api/assist/switch", sw("air26", false)); code != 409 || body["error"] != "busy" {
		t.Fatalf("same host: %d %v", code, body)
	}
	r.flag("assist-on", "block", false)
	if c := <-done; c != 200 {
		t.Fatal(c)
	}
	r.flag("assist-on", "block", false)
	if code, _ := r.do("POST", "/api/assist/switch", sw("mserv", true)); code != 200 {
		t.Fatalf("other host: %d", code)
	}
}

func TestSwitchRefusals(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	if code, _ := r.do("POST", "/api/assist/switch", sw("nohost", true)); code != 404 {
		t.Fatalf("unknown host %d", code)
	}
	for _, reason := range []string{"", "a\nb", strings.Repeat("x", 201)} {
		b := sw("air26", true)
		b["reason"] = reason
		if code, _ := r.do("POST", "/api/assist/switch", b); code != 400 {
			t.Fatalf("reason %q: %d", reason, code)
		}
	}
	if r.count("assist-") != 0 {
		t.Fatal("a refused switch ran a script")
	}
}

// A daemon died with an Op in the file: the reconciler runs that script once and concludes it.
func TestReconcileRunsALeftOpOnce(t *testing.T) {
	for _, kind := range []string{"on", "off"} {
		r := newRig(t)
		r.addHost("air26")
		r.edit(func(f *gate.AssistFile) {
			h := f.Hosts["air26"]
			h.On = kind == "on"
			h.Op = &gate.AssistOp{Kind: kind, ID: "left", At: r.clock.now()}
			f.Hosts["air26"] = h
		})
		r.flag("assist-"+kind, "block", true)
		go r.m.reconcile()
		r.waitFor("the reconciler's script", func() bool { return r.count("assist-"+kind) == 1 })
		if code, body := r.do("POST", "/api/assist/switch", sw("air26", kind != "on")); code != 409 || body["error"] != "busy" {
			t.Fatalf("%s: while it runs: %d %v", kind, code, body)
		}
		r.flag("assist-"+kind, "block", false)
		r.waitFor("the Op to be cleared", func() bool { return r.file().Hosts["air26"].Op == nil })
		r.m.reconcile()
		if n := r.count("assist-" + kind); n != 1 {
			t.Fatalf("%s ran %d times", kind, n)
		}
		if h := r.file().Hosts["air26"]; h.On != (kind == "on") {
			t.Fatalf("%s: On = %v", kind, h.On)
		}
	}
}

func TestReconcileFailedOnConcludesOff(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On, h.Op = true, &gate.AssistOp{Kind: "on", ID: "left"}
		f.Hosts["air26"] = h
	})
	r.script("assist-on", "", 2)
	r.m.reconcile()
	if h := r.file().Hosts["air26"]; h.On || h.Op != nil {
		t.Fatalf("%+v", h)
	}
}

// A route's switch is still running when a poll comes: the reconciler runs nothing.
func TestReconcileLeavesARunningRouteSwitchAlone(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.flag("assist-on", "block", true)
	done := make(chan int, 1)
	go func() { c, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); done <- c }()
	r.waitFor("the script", func() bool { return r.count("assist-on") == 1 })
	r.m.reconcile()
	r.m.reconcile()
	r.flag("assist-on", "block", false)
	<-done
	if n := r.count("assist-on"); n != 1 {
		t.Fatalf("assist-on ran %d times", n)
	}
	r.m.reconcile() // and a poll between the clearing of Op and a later switch runs nothing either
	if n := r.count("assist-"); n != 1 {
		t.Fatalf("scripts ran %d times", n)
	}
}

func TestReconcileAskedOffButHostSaysOnRunsOffAgain(t *testing.T) {
	for _, sw := range []string{"on", "closing"} {
		r := newRig(t)
		r.addHost("air26")
		g := r.file().Hosts["air26"].Gen
		r.m.setReady("air26", r.readyOf(g, gateAnswer(sw)))
		r.m.reconcile()
		if n := r.count("assist-off --apply --json"); n != 1 {
			t.Fatalf("%s: off ran %d times", sw, n)
		}
		if h := r.file().Hosts["air26"]; h.On || h.Op != nil {
			t.Fatalf("%+v", h)
		}
	}
}

func TestReconcileAskedOffAndHostOffRunsNothing(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.m.setReady("air26", r.readyOf(r.file().Hosts["air26"].Gen, gateAnswer("off")))
	r.m.reconcile()
	if r.count("assist-") != 0 {
		t.Fatalf("ran %v", r.calls())
	}
}

// A check of an older generation than the one that went with the off does not make the daemon end anything again.
func TestReconcileIgnoresACheckOfAnOlderGen(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.m.setReady("air26", r.readyOf(r.file().Hosts["air26"].Gen-1, gateAnswer("on")))
	r.m.reconcile()
	if r.count("assist-") != 0 {
		t.Fatalf("ran %v", r.calls())
	}
}

func TestReconcileAskedOnButHostOffIsNotUsableAndRunsNothing(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.edit(func(f *gate.AssistFile) {
		h := f.Hosts["air26"]
		h.On = true
		f.Hosts["air26"] = h
		f.Allowed = []gate.AssistAllow{{Kind: "team", ID: "t1"}}
	})
	r.m.setReady("air26", r.readyOf(r.file().Hosts["air26"].Gen, gateAnswerRepo("off", "purdex")))
	r.m.reconcile()
	if r.count("assist-") != 0 {
		t.Fatalf("ran %v", r.calls())
	}
	ok, why := gate.Usable(r.file(), r.m.readyCopy(), r.clock.now(), "air26", []gate.AssistEntry{{Kind: "team", ID: "t1"}}, "purdex")
	if ok || why != "the host says off" {
		t.Fatalf("%v %q", ok, why)
	}
}

// Stop ends a script in flight (its process group) instead of waiting for the script's timeout.
func TestStopEndsARunningCheck(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.m.timeouts.check = time.Minute
	r.m.pollEvery = time.Hour
	r.flag("assist-check", "block", true)
	if err := r.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the check to start", func() bool { return r.count("assist-check") == 1 })
	done := make(chan struct{})
	go func() { r.m.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop waited for the script")
	}
	r.flag("assist-check", "block", false)
}

// A script that says ok about the wrong state is a failure, not a switch.
func TestSwitchAnswerForTheWrongStateFails(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.script("assist-on", `{"ok":true,"switch":"off","detail":"hm"}`, 0)
	if code, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); code != 502 {
		t.Fatalf("%d", code)
	}
	if h := r.file().Hosts["air26"]; h.On || h.Op != nil {
		t.Fatalf("%+v", h)
	}
}

// A script that says no on exit 1 gives its reason in the JSON detail.
func TestSwitchExit1DetailIsKept(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.script("assist-off", `{"ok":false,"switch":"on","detail":"still running jobs"}`, 1)
	code, body := r.do("POST", "/api/assist/switch", sw("air26", false))
	if code != 502 || !strings.Contains(body["detail"].(string), "still running jobs") {
		t.Fatalf("%d %v", code, body)
	}
}

// What a script leaves running in its process group is ended when the script is.
func TestScriptLeftoversAreEnded(t *testing.T) {
	r := newRig(t)
	r.addHost("air26")
	r.flag("assist-on", "pidfile", true) // the fake script starts a `sleep 1000 &` and exits
	if code, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); code != 200 {
		t.Fatal(code)
	}
	b, err := os.ReadFile(filepath.Join(r.ctl, "assist-on.child"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	r.waitFor("the leftover to be ended", func() bool { return syscall.Kill(pid, 0) != nil })
}

func TestSwitchOkWithoutAStateFails(t *testing.T) {
	for _, out := range []string{`{"ok":true}`, `{"ok":true,"switch":""}`} {
		r := newRig(t)
		r.addHost("air26")
		r.script("assist-on", out, 0)
		if code, _ := r.do("POST", "/api/assist/switch", sw("air26", true)); code != 502 {
			t.Fatalf("%s: %d", out, code)
		}
	}
}
