package derived

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var yieldNow = time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)

func waitingSince(age time.Duration) *wire.Waiting {
	return &wire.Waiting{Count: 1, OldestID: "j_1", OldestOrigin: "mlab", OldestSince: yieldNow.Add(-age), ObservedAt: yieldNow.Add(-2 * time.Second)}
}

var policy = YieldPolicy{After: 5 * time.Minute, Max: 30 * time.Minute}

func TestYieldPolicy_Judge(t *testing.T) {
	for name, c := range map[string]struct {
		p       YieldPolicy
		w       *wire.Waiting
		yield   bool
		over    bool
		comment string
	}{
		"nothing waiting":                 {policy, nil, false, false, ""},
		"waited a minute less than after": {policy, waitingSince(5*time.Minute - time.Second), false, false, ""},
		"waited after":                    {policy, waitingSince(5 * time.Minute), true, false, ""},
		"waited max":                      {policy, waitingSince(30 * time.Minute), true, false, ""},
		"waited past max":                 {policy, waitingSince(30*time.Minute + time.Second), false, true, ""},
		"off (after 0)":                   {YieldPolicy{Max: 30 * time.Minute}, waitingSince(10 * time.Minute), false, false, ""},
		"no max given: the default":       {YieldPolicy{After: 5 * time.Minute}, waitingSince(31 * time.Minute), false, true, ""},
		"observed long ago":               {policy, &wire.Waiting{Count: 1, OldestID: "j", OldestSince: yieldNow.Add(-10 * time.Minute), ObservedAt: yieldNow.Add(-waitingTrust - time.Second)}, false, false, ""},
		"a clock ahead (future since)":    {policy, waitingSince(-time.Minute), false, false, ""},
		"count 0":                         {policy, &wire.Waiting{Count: 0, OldestSince: yieldNow.Add(-time.Hour), ObservedAt: yieldNow}, false, false, ""},
	} {
		yield, over := c.p.Judge(c.w, yieldNow)
		if yield != c.yield || over != c.over {
			t.Errorf("%s: yield %v over %v, want %v %v", name, yield, over, c.yield, c.over)
		}
	}
}

func idleFacts() wire.Facts {
	f := wire.Facts{}
	f.Derived.Zone = wire.ZoneNormal
	f.Worker = wire.FactsWorker{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}}
	return f
}

// A heavy fast-lane run gives way to a queued job that has waited long enough; a light one does not (#3941).
func TestAdmitLocalAt_HeavyYieldsToAWaitingQueueJob(t *testing.T) {
	f := idleFacts()
	f.Worker.Waiting = waitingSince(7 * time.Minute)
	got := AdmitLocalAt(f, true, wire.Slots{}, yieldNow, policy)
	if len(got) != 1 || !strings.HasPrefix(got[0], ReasonYieldToQueue) || !strings.Contains(got[0], "j_1") || !strings.Contains(got[0], "7m") {
		t.Fatalf("heavy: %v", got)
	}
	if got := AdmitLocalAt(f, false, wire.Slots{}, yieldNow, policy); got != nil {
		t.Errorf("light: %v", got)
	}
}

func TestAdmitLocalAt_NoYieldWhen(t *testing.T) {
	for name, c := range map[string]struct {
		w *wire.Waiting
		p YieldPolicy
	}{
		"it has not waited long enough": {waitingSince(4 * time.Minute), policy},
		"it waited past the cap":        {waitingSince(31 * time.Minute), policy},
		"the policy is off":             {waitingSince(7 * time.Minute), YieldPolicy{}},
		"nothing waits":                 {nil, policy},
	} {
		f := idleFacts()
		f.Worker.Waiting = c.w
		if got := AdmitLocalAt(f, true, wire.Slots{}, yieldNow, c.p); got != nil {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// Another reason is the answer as it was; the yield is only what is added when nothing else is in the way.
func TestAdmitLocalAt_OtherReasonsComeFirstAndAlone(t *testing.T) {
	f := idleFacts()
	f.Worker.Waiting = waitingSince(7 * time.Minute)
	got := AdmitLocalAt(f, true, wire.Slots{Heavy: 1}, yieldNow, policy)
	if len(got) != 1 || got[0] != ReasonSlotsFull {
		t.Errorf("a full slot: %v", got)
	}
}

func TestAdmitLocal_IsWithoutYield(t *testing.T) {
	f := idleFacts()
	f.Worker.Waiting = waitingSince(time.Hour)
	if got := AdmitLocal(f, true, wire.Slots{}); got != nil {
		t.Errorf("%v", got)
	}
}
