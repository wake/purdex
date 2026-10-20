package push

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/notifylabel"
)

// notify-title spec §1, §3.4: the title is built per device from the daemon's label and that device's own host name.

func labelled(label notifylabel.Label) agent.NotifyEvent {
	ev := nev("c1", "PdxStop", "idle", stopDetail("done"))
	ev.Event.NotifyLabel = &label
	return ev
}

func titleOf(t *testing.T, payload string) string {
	t.Helper()
	var p struct {
		Aps struct {
			Alert struct {
				Title string `json:"title"`
			} `json:"alert"`
		} `json:"aps"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	return p.Aps.Alert.Title
}

// A named session in a group: both devices are titled by the person and the group, whatever they call the host.
// A named session with no group: each device's own name for the host is in its own title.
func TestAgentTitle_PerDeviceHostName(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.device(tokB, "zh-TW", "mlab-zh", tabsOf("c1"))
	e.feed.emit(labelled(notifylabel.Label{Name: "Mei", Group: "Purdex A team"}))
	got := map[string]bool{}
	for _, c := range e.waitSends(t, 2) {
		got[titleOf(t, c.Payload)] = true
	}
	if len(got) != 1 || !got["Mei — Purdex A team"] {
		t.Fatalf("titles = %v", got)
	}
	e.clock.advance(time.Minute)
	e.feed.emit(labelled(notifylabel.Label{Name: "Mei"}))
	got = map[string]bool{}
	for _, c := range e.waitSends(t, 4)[2:] {
		got[titleOf(t, c.Payload)] = true
	}
	if len(got) != 2 || !got["Mei — mlab: dev"] || !got["Mei — mlab-zh：dev"] {
		t.Fatalf("titles = %v", got)
	}
}

// A frame with no label (an older daemon path, a cold cache, a session with no persona) is titled as it always was.
func TestAgentTitle_NoLabelIsToday(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("done")))
	if got := titleOf(t, e.waitSends(t, 1)[0].Payload); got != "mlab: dev" {
		t.Fatalf("title = %q", got)
	}
}

// Only aps.alert.title differs from the push of the same event with no label: the purdex block keeps its shape.
func TestAgentTitle_OnlyTheAlertTitleChanges(t *testing.T) {
	strip := func(payload string) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatal(err)
		}
		delete(m["aps"].(map[string]any)["alert"].(map[string]any), "title")
		return m
	}
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	plain := nev("c1", "PdxStop", "idle", stopDetail("done"))
	e.feed.emit(plain)
	e.clock.advance(time.Minute)
	e.feed.emit(labelled(notifylabel.Label{Name: "Mei", Group: "Purdex A team"}))
	calls := e.waitSends(t, 2)
	if titleOf(t, calls[0].Payload) == titleOf(t, calls[1].Payload) {
		t.Fatal("the label did not change the title")
	}
	if !reflect.DeepEqual(strip(calls[0].Payload), strip(calls[1].Payload)) {
		t.Fatalf("payloads differ beyond the title:\n%s\n%s", calls[0].Payload, calls[1].Payload)
	}
	if !strings.Contains(calls[1].Payload, `"session_title":"dev"`) {
		t.Fatalf("the purdex block lost the tmux name: %s", calls[1].Payload)
	}
}
