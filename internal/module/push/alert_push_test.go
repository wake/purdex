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

// The payload names the host the alert is about (alert_host); host_id stays the host that delivers, so a tap opens a Purdex that is
// reachable. A collapse id longer than APNs' 64 bytes (a 200-byte alert key) is replaced by a stable short one, so the push is not
// refused with BadCollapseId; the same alert gets the same id, and a short id is sent as it is.
func TestAlertPush_AlertHostAndLongCollapseID(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	long := "alert:mserv:disk_low:/" + strings.Repeat("a", 190)
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: long, Title: "t", Host: "mserv"}) {
		t.Fatal("not done")
	}
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: long, Title: "t", Host: "mserv"}) {
		t.Fatal("not done")
	}
	calls := te.waitSends(t, 2)
	if id := calls[0].H.CollapseID; len(id) == 0 || len(id) > 64 || id != calls[1].H.CollapseID || id == long {
		t.Fatalf("collapse ids = %q, %q", calls[0].H.CollapseID, calls[1].H.CollapseID)
	}
	if !strings.Contains(calls[0].Payload, `"alert_host":"mserv"`) || !strings.Contains(calls[0].Payload, `"host_id"`) {
		t.Fatalf("payload = %s", calls[0].Payload)
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
