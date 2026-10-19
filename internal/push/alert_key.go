package push

// AlertNotifyKey is the service-registry key under which the push module publishes its AlertNotifier: the module that delivers the
// wwd host alerts (WWD-8a, spec §13) asks, in process, for one phone push per alert.
const AlertNotifyKey = "push.alert-notifier"

// AlertPush is one phone push of a host alert. CollapseID is alert:<host>:<key>, so a repeat of the same alert replaces the earlier
// one on the phone.
type AlertPush struct {
	CollapseID string
	Title      string // the alert's text, already in the person's language
	Host       string
	// Sent is the only proof of delivery. It is called at most once, on the sender's goroutine, after APNs accepted the push on at
	// least one device. It is never called for a push that was skipped, refused, dropped on a full queue, or lost when the sender
	// stopped with the job still queued — so a delivery ledger records on Sent, never on NotifyAlert's answer. nil = not wanted.
	Sent func()
	// StillWanted, when set, is asked by the sender just before each device's push goes out, not only when the push is queued: false
	// drops it (and Sent is then not called for it). It lets a caller that was told to stop (the wwd_alerts.push switch) take back what
	// still waits in the queue; a request already on its way to APNs cannot be recalled. nil = always wanted.
	StillWanted func() bool
	// Recalled is called once when StillWanted took the push back before any request to APNs was made, so nothing was sent and the
	// caller can undo what it recorded for the attempt. Not called once a request went out (a retry taken back after that does not
	// undo the attempt). nil = none.
	Recalled func()
}

// AlertReadiness is the second face of the registered AlertNotifier: whether it can queue a push right now. The registry has no way to
// remove a service, so the notifier stays registered after the push module stops; this tells the difference. A notifier that does not
// have it is taken as not ready.
type AlertReadiness interface {
	AlertReady() bool
}

// AlertNotifier is how the alert delivery module asks for a phone push, in process. NotifyAlert says done when the push was queued or
// deliberately not sent (a person at the computer needs no phone push; no device means nobody to tell), and false when the push
// side is not up or its queue is full: the caller keeps the alert undelivered and tries again. "done" is not "delivered": see Sent.
type AlertNotifier interface {
	NotifyAlert(p AlertPush) bool
}
