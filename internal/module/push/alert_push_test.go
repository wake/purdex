package push

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
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

// A caller that read the sender just before Stop and enqueues after it must be told "not done": nothing will ever send that job, and
// done=true would make the alert delivery module mark the alert delivered for good.
func TestAlertPush_StoppedSenderIsNotDone(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	snd := te.mod.sender.Load()
	snd.Stop()
	if snd.Enqueue(Job{DeviceIDs: []string{"d"}}) {
		t.Fatal("a stopped sender accepted a job")
	}
}

// Sent is the only proof of delivery: it is called once, after APNs took the push on at least one device. NotifyAlert's true only
// says the job was queued (or deliberately skipped), so the delivery ledger must not record on it.
func TestAlertPush_SentCalledOnlyAfterApnsTookIt(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	te.register(tokB, "en", "mlab-en")
	sent := make(chan struct{}, 4)
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: "alert:mlab:x", Title: "t", Host: "mlab", Sent: func() { sent <- struct{}{} }}) {
		t.Fatal("not done")
	}
	te.waitSends(t, 2)
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("Sent not called after the pushes were accepted")
	}
	select {
	case <-sent:
		t.Fatal("Sent called more than once for one alert")
	case <-time.After(100 * time.Millisecond):
	}
}

// A push APNs refuses is not delivered, and a skipped one (the person is at the computer) is not either: Sent stays uncalled.
func TestAlertPush_SentNotCalledWhenRefusedOrSkipped(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	te.apns.mu.Lock()
	te.apns.script = []apns.Result{{Class: apns.Rejected, Status: 400, Err: "BadCollapseId"}}
	te.apns.mu.Unlock()
	called := make(chan struct{}, 2)
	sent := func() { called <- struct{}{} }
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: "alert:mlab:x", Title: "t", Host: "mlab", Sent: sent}) {
		t.Fatal("queued is done")
	}
	te.waitSends(t, 1)
	te.mod.presence = presenceFake{at: func() bool { return true }}
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: "alert:mlab:y", Title: "t", Host: "mlab", Sent: sent}) {
		t.Fatal("skipped is done")
	}
	select {
	case <-called:
		t.Fatal("Sent called for a refused or skipped push")
	case <-time.After(300 * time.Millisecond):
	}
}

// The registry keeps the notifier after the module stops, so readiness is its own question: true while the sender is up, false after Stop.
func TestAlertPush_ReadyOnlyWhileTheSenderIsUp(t *testing.T) {
	te := newTriggerEnv(t, nil)
	r, ok := te.mod.alertNotifier().(push.AlertReadiness)
	if !ok {
		t.Fatal("the push module's notifier does not say whether it is ready")
	}
	if !r.AlertReady() {
		t.Fatal("not ready after Start")
	}
	if err := te.mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.AlertReady() {
		t.Fatal("still ready after Stop")
	}
}

// StillWanted is asked by the sender just before each device's push, not only when the push was queued: a push that waited in the queue
// while its caller was told to stop (the wwd_alerts.push switch) is not sent, and then Sent is not called either.
func TestAlertPush_NotSentWhenNoLongerWanted(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	var wanted atomic.Bool
	sent := make(chan struct{}, 2)
	wanted.Store(false)
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: "alert:mlab:x", Title: "t", Host: "mlab",
		StillWanted: wanted.Load, Sent: func() { sent <- struct{}{} }}) {
		t.Fatal("queued is done")
	}
	te.noSends(t)
	select {
	case <-sent:
		t.Fatal("Sent called for a push that was not sent")
	default:
	}
	wanted.Store(true)
	if !te.mod.alertNotifier().NotifyAlert(push.AlertPush{CollapseID: "alert:mlab:y", Title: "t", Host: "mlab", StillWanted: wanted.Load}) {
		t.Fatal("not done")
	}
	te.waitSends(t, 1)
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
