package push

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
)

// Member spec R1 (push spec §5.2 rule 4a): a team member's waiting and idle are never pushed, tab or not; its errors follow
// the rules of any session (5-6a, the 60 s debounce). A session the device muted gets no agent push.

func mdev(id string, agents map[string]push.AgentPrefs, tabs, muted []string) push.Device {
	return push.Device{DeviceID: id, Prefs: push.Prefs{Agents: agents, Tabs: tabs, Muted: muted}}
}

// Mutation gate: drop rule 4a → the member waiting / idle rows are red. Keep a member tab clause in DeviceWants → the
// member error "notify_without_tab on, no tab" row is red.
func TestGate_MemberWaitingAndIdleAreNeverPushedErrorsFollowTheNormalRules(t *testing.T) {
	withoutTab := map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}
	typeOff := map[string]push.AgentPrefs{"cc": {Enabled: bptr(false)}}
	cases := []struct {
		name   string
		member bool
		status string
		agents map[string]push.AgentPrefs
		tabs   []string
		want   bool
	}{
		{"member waiting, tab", true, "waiting", nil, []string{"c1"}, false},
		{"member waiting, no tab, notify_without_tab on", true, "waiting", withoutTab, nil, false},
		{"member idle, tab", true, "idle", nil, []string{"c1"}, false},
		{"member idle, no tab, notify_without_tab on", true, "idle", withoutTab, nil, false},
		{"member error, tab", true, "error", nil, []string{"c1"}, true},
		{"member error, no tab, notify_without_tab on", true, "error", withoutTab, nil, true},
		{"member error, no tab, notify_without_tab off", true, "error", nil, nil, false},
		{"member error, type switched off, no tab", true, "error", typeOff, nil, false},
		{"member error, type switched off, tab", true, "error", typeOff, []string{"c1"}, false},
		{"non-member waiting, tab", false, "waiting", nil, []string{"c1"}, true},
		{"non-member waiting, no tab, notify_without_tab on", false, "waiting", withoutTab, nil, true},
		{"non-member waiting, no tab, notify_without_tab off", false, "waiting", nil, nil, false},
		{"non-member idle, type switched off", false, "idle", typeOff, []string{"c1"}, false},
		{"non-member error, no tab, notify_without_tab on", false, "error", withoutTab, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, _ := newTestGate()
			name := "PermissionRequest"
			if c.status == "idle" {
				name = "Stop"
			}
			e := ev(c.status, name)
			e.ErrorString = "boom"
			e.Member = c.member
			got := decide(g, e, []push.Device{mdev("d1", c.agents, c.tabs, nil)}, nil)
			if (got == 1) != c.want {
				t.Fatalf("recipients = %d, want pushed=%v", got, c.want)
			}
		})
	}
}

// Rule 4a is per event, not per device: no device gets a member's waiting event, whatever its prefs.
func TestGate_MemberWaitingReachesNoDevice(t *testing.T) {
	g, _ := newTestGate()
	e := ev("waiting", "PermissionRequest")
	e.Member = true
	got := g.Decide(e, []push.Device{
		mdev("tab", nil, []string{"c1"}, nil),
		mdev("no-tab", map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}, nil, nil),
	}, nil)
	if len(got) != 0 {
		t.Fatalf("recipients = %+v, want none", got)
	}
}

// The error debounce still applies to a member's error: the second identical one inside 60 s is silent.
// Mutation gate: skip the debounce for a member → red.
func TestGate_MemberErrorStillPassesTheDebounce(t *testing.T) {
	g, clock := newTestGate()
	e := ev("error", "StopFailure")
	e.ErrorString = "rate_limit"
	e.Member = true
	devs := []push.Device{mdev("d1", nil, []string{"c1"}, nil)}
	if decide(g, e, devs, nil) != 1 {
		t.Fatal("the first member error must push")
	}
	clock.advance(10 * time.Second)
	e.BroadcastTs++
	if decide(g, e, devs, nil) != 0 {
		t.Fatal("the same member error inside 60 s must be debounced")
	}
}

// Mutation gate: ignore prefs.muted → red.
func TestGate_MutedSessionGetsNoAgentPush(t *testing.T) {
	withoutTab := map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}
	g, _ := newTestGate()
	devs := []push.Device{
		mdev("muted-tab", nil, []string{"c1"}, []string{"c1"}),          // a tab and muted: muted wins
		mdev("muted-notab", withoutTab, nil, []string{"c1"}),            // notify_without_tab and muted: muted wins
		mdev("other-muted", withoutTab, []string{"c1"}, []string{"c2"}), // another session muted: pushed
		mdev("plain", withoutTab, []string{"c1"}, nil),
	}
	got := g.Decide(ev("waiting", "PermissionRequest"), devs, nil)
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.DeviceID] = true
	}
	if len(ids) != 2 || !ids["other-muted"] || !ids["plain"] {
		t.Fatalf("recipients = %v, want other-muted and plain only", ids)
	}
}

// Mute is per phone: a device that did not mute still gets it, and the muted one does not.
func TestGate_MuteIsPerDevice(t *testing.T) {
	g, _ := newTestGate()
	got := g.Decide(ev("waiting", "PermissionRequest"), []push.Device{
		mdev("a", nil, []string{"c1"}, []string{"c1"}),
		mdev("b", nil, []string{"c1"}, nil),
	}, nil)
	if len(got) != 1 || got[0].DeviceID != "b" {
		t.Fatalf("recipients = %+v, want only b", got)
	}
}

// A muted member's error: the muted device gets nothing, the others follow the normal tab rule (the two rules compose).
func TestGate_MemberErrorAndMuted(t *testing.T) {
	g, _ := newTestGate()
	e := ev("error", "StopFailure")
	e.ErrorString = "boom"
	e.Member = true
	got := g.Decide(e, []push.Device{
		mdev("tab-muted", nil, []string{"c1"}, []string{"c1"}),
		mdev("tab", nil, []string{"c1"}, nil),
		mdev("no-tab", map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}, nil, nil),
	}, nil)
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.DeviceID] = true
	}
	if len(ids) != 2 || !ids["tab"] || !ids["no-tab"] {
		t.Fatalf("recipients = %+v, want tab and no-tab", got)
	}
}
