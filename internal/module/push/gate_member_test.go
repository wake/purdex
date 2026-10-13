package push

import (
	"testing"

	"github.com/wake/purdex/internal/push"
)

// Badge spec §3.1, gate: a member pushes only when the device has its tab (whatever notify_without_tab says), and a session
// the device muted gets no agent push. The rest of the gate is unchanged (TestGate_MacShouldNotifyCases).

func mdev(id string, agents map[string]push.AgentPrefs, tabs, muted []string) push.Device {
	return push.Device{DeviceID: id, Prefs: push.Prefs{Agents: agents, Tabs: tabs, Muted: muted}}
}

// Mutation gate: drop the member rule (a member treated like anyone) → the "member, no tab, notify_without_tab on" row is red.
func TestGate_MemberPushesOnlyWithATab(t *testing.T) {
	withoutTab := map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}
	cases := []struct {
		name   string
		member bool
		agents map[string]push.AgentPrefs
		tabs   []string
		want   bool
	}{
		{"member with a tab", true, nil, []string{"c1"}, true},
		{"member, no tab, notify_without_tab on", true, withoutTab, nil, false},
		{"member, another session's tab, notify_without_tab on", true, withoutTab, []string{"other"}, false},
		{"member, tab, notify_without_tab on", true, withoutTab, []string{"c1"}, true},
		{"non-member, no tab, notify_without_tab on (option 甲)", false, withoutTab, nil, true},
		{"non-member, no tab, notify_without_tab off", false, nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, _ := newTestGate()
			e := ev("waiting", "PermissionRequest")
			e.Member = c.member
			got := decide(g, e, []push.Device{mdev("d1", c.agents, c.tabs, nil)}, nil)
			if (got == 1) != c.want {
				t.Fatalf("recipients = %d, want pushed=%v", got, c.want)
			}
		})
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

// A muted member and a member with no tab: neither pushes; the two rules compose.
func TestGate_MemberAndMuted(t *testing.T) {
	g, _ := newTestGate()
	e := ev("waiting", "PermissionRequest")
	e.Member = true
	got := g.Decide(e, []push.Device{
		mdev("tab-muted", nil, []string{"c1"}, []string{"c1"}),
		mdev("tab", nil, []string{"c1"}, nil),
		mdev("no-tab", map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}, nil, nil),
	}, nil)
	if len(got) != 1 || got[0].DeviceID != "tab" {
		t.Fatalf("recipients = %+v, want only tab", got)
	}
}
