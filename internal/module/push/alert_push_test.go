package push

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/push"
)

// The alert delivery module asks the push module for a phone push in process (WWD-8a-1b). The module publishes itself as the
// AlertNotifier when it is ready, and the push reaches every device with kind "alert" and the alert's collapse id.
func TestAlertPush_RegisteredAndReachesEveryDevice(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	te.register(tokB, "en", "mlab-en")
	svc, ok := te.mod.core.Registry.Get(push.AlertNotifyKey)
	if !ok {
		t.Fatal("the push module did not publish its AlertNotifier")
	}
	n, ok := svc.(push.AlertNotifier)
	if !ok {
		t.Fatalf("%T is not an AlertNotifier", svc)
	}
	if !n.NotifyAlert(push.AlertPush{CollapseID: "alert:mserv:disk_low:/", Title: "mserv 磁碟剩 9.8 GB", Host: "mserv"}) {
		t.Fatal("NotifyAlert not done")
	}
	for _, c := range te.waitSends(t, 2) {
		if c.H.CollapseID != "alert:mserv:disk_low:/" || !strings.Contains(c.Payload, "mserv 磁碟剩 9.8 GB") ||
			!strings.Contains(c.Payload, `"kind":"alert"`) || !strings.Contains(c.Payload, `"host_id"`) {
			t.Fatalf("call = %+v", c)
		}
	}
}

// A person at the computer needs no phone push: the alert is done (not retried) and nothing is sent.
func TestAlertPush_NotSentWhenAtComputer(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	te.mod.presence = presenceFake{at: func() bool { return true }}
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: "alert:mlab:x", Title: "t", Host: "mlab"}) {
		t.Fatal("at the computer is done, not a retry")
	}
	te.noSends(t)
}

// With no device there is nobody to tell: done. With the push side not up, not done (the caller retries).
func TestAlertPush_NoDeviceIsDoneNotStartedIsNot(t *testing.T) {
	te := newTriggerEnv(t, nil)
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: "alert:mlab:x", Title: "t", Host: "mlab"}) {
		t.Fatal("no device is done")
	}
	te.mod.sender.Store(nil)
	if te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: "alert:mlab:x", Title: "t", Host: "mlab"}) {
		t.Fatal("a push side that is not up is not done")
	}
}
