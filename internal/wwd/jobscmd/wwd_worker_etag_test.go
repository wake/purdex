package jobscmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

func workerFake() *fakeAPI {
	return &fakeAPI{worker: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, OwnerRule: "hid<3m"}}
}

func TestWorkerShowsTheSettingsAndTheirEtag(t *testing.T) {
	f := workerFake()
	code, out, _ := runWWD(f, "worker")
	etag := f.worker.Etag()
	for _, want := range []string{"worker on", "slots heavy 1 light 2", "owner_rule hid<3m", "etag " + etag} {
		if code != ExitOK || !strings.Contains(out, want) {
			t.Fatalf("show lacks %q (code %d): %q", want, code, out)
		}
	}
	if f.set != nil || f.askedHost != "" {
		t.Fatalf("showing wrote %+v / asked %q", f.set, f.askedHost)
	}
	// another host: asked of the local wwd by name, and the answer says whose it is
	code, out, _ = runWWD(f, "worker", "--host", "air26")
	if code != ExitOK || f.askedHost != "air26" || !strings.Contains(out, "host air26") || f.set != nil {
		t.Fatalf("--host: code %d asked %q out %q", code, f.askedHost, out)
	}
	// a wwd from before the etag answers none: the line says so rather than print an empty etag to pass to --expect
	old := &fakeAPI{worker: f.worker}
	old.noEtag = true
	if code, out, _ := runWWD(old, "worker"); code != ExitOK || !strings.Contains(out, "predates") {
		t.Fatalf("no etag: code %d out %q", code, out)
	}
}

func TestWorkerWriteWithExpectAndHost(t *testing.T) {
	f := workerFake()
	etag := f.worker.Etag()
	code, out, _ := runWWD(f, "worker", "on", "--heavy", "3", "--host", "air26", "--expect", etag)
	if code != ExitOK || f.askedHost != "air26" || f.expect != etag || f.set == nil || f.set.Slots.Heavy != 3 || f.set.Slots.Light != 2 || f.set.OwnerRule != "hid<3m" {
		t.Fatalf("code %d host %q expect %q set %+v", code, f.askedHost, f.expect, f.set)
	}
	// the answer is the read-back: the new settings and the etag to use next
	if !strings.Contains(out, "slots heavy 3 light 2") || !strings.Contains(out, "etag "+f.set.Etag()) || !strings.Contains(out, "host air26") {
		t.Fatalf("read-back: %q", out)
	}
	// --expect=<etag> spells the same
	f.set, f.expect = nil, ""
	if code, _, _ := runWWD(f, "worker", "off", "--expect="+etag); code != ExitOK || f.expect != etag || f.set == nil || f.set.Enabled {
		t.Fatalf("--expect=: code %d expect %q set %+v", code, f.expect, f.set)
	}
}

func TestWorkerWriteWithoutExpectIsAllowedHereAndOnAHost(t *testing.T) {
	f := workerFake()
	if code, _, _ := runWWD(f, "worker", "off"); code != ExitOK || f.expect != "" || f.set == nil {
		t.Fatalf("local: code %d expect %q set %+v", code, f.expect, f.set)
	}
	f.set = nil
	if code, _, _ := runWWD(f, "worker", "off", "--host", "mserv"); code != ExitOK || f.expect != "" || f.askedHost != "mserv" || f.set == nil {
		t.Fatalf("host: code %d expect %q host %q set %+v", code, f.expect, f.askedHost, f.set)
	}
}

func TestWorkerWriteRefusedForAStaleEtagIsExit7AndShowsWhatIsThere(t *testing.T) {
	f := workerFake()
	now := wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 5, Light: 2}, OwnerRule: "hid<3m"}
	body, _ := json.Marshal(wire.WorkerEtagMismatch{Reason: wire.WorkerEtagMismatchReason, Etag: now.Etag(), Worker: now.View()})
	f.setErr = &client.StatusError{Code: 409, Body: string(body)}
	code, out, errOut := runWWD(f, "worker", "on", "--heavy", "2", "--expect", "stale")
	if code != ExitConflict || code == ExitNoWWD || out != "" {
		t.Fatalf("code %d out %q", code, out)
	}
	for _, want := range []string{"nothing was changed", "slots heavy 5 light 2", "etag " + now.Etag()} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q: %q", want, errOut)
		}
	}
	// a 409 that is not the etag refusal is an ordinary failure, not a conflict
	f.setErr = &client.StatusError{Code: 409, Body: "something else"}
	if code, _, _ := runWWD(f, "worker", "on", "--expect", "x"); code != ExitFailed {
		t.Errorf("other 409: code %d", code)
	}
}

func TestWorkerWriteToAnOlderWWDIsNotRetriedWithoutExpect(t *testing.T) {
	f := workerFake()
	f.setErr = &client.StatusError{Code: 400, Body: `{"error":"json: unknown field \"expect_etag\""}`}
	code, out, errOut := runWWD(f, "worker", "on", "--host", "air19", "--expect", "abc")
	if code != ExitUsage || out != "" || !strings.Contains(errOut, "does not know --expect") || !strings.Contains(errOut, "Nothing was changed") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if f.set != nil || f.sets != 1 {
		t.Fatalf("asked %d times, set %+v: it must be asked once and never without the etag", f.sets, f.set)
	}
}

func TestWorkerFlagsAreChecked(t *testing.T) {
	f := workerFake()
	for _, args := range [][]string{
		{"worker", "--heavy", "2"},         // no on/off: it only shows
		{"worker", "--expect", "abc"},      // an etag with nothing to write
		{"worker", "on", "--expect="},      // an empty etag
		{"worker", "on", "--host", "a\nb"}, // not an alias
		{"worker", "on", "off"},            // two actions
		{"worker", "show", "--host", "x"},  // not on or off
	} {
		f.set = nil
		if code, _, _ := runWWD(f, args...); code != ExitUsage || f.set != nil {
			t.Errorf("%v: code %d set %+v", args, code, f.set)
		}
	}
}
