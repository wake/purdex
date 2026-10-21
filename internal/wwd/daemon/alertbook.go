package daemon

import (
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// AlertWriter writes the host's alerts (store.Store): the table behind GET /v1/alerts. Each call says whether something a reader
// would notice changed.
type AlertWriter interface {
	RaiseAlert(a wire.Alert, at time.Time) (changed bool, err error)
	ClearAlert(key string, at time.Time) (changed bool, err error)
	ActiveAlerts() ([]wire.Alert, error)
}

// AlertBook is what the alert rules (spec §13.3) call: it keeps the host's alerts in the table and tells the event stream when the list
// changed. The `alert` event is only a hint — a reader that gets it, or that starts or reconnects, reads GET /v1/alerts again — so a
// dropped stream loses nothing.
//
// Alerts are not behind the proto 2 writer gate (spec §13.1): the table is written and the hint goes out whatever the pool reports. The
// hint reaches the socket stream only (Broker.PublishAlert), so no older reader is ever shown the new event type.
type AlertBook struct {
	w    AlertWriter
	host string
	b    *Broker
	now  func() time.Time
}

// NewAlertBook returns the book of host. b may be nil (no hints); now is the clock (nil: time.Now).
func NewAlertBook(w AlertWriter, host string, b *Broker, now func() time.Time) *AlertBook {
	if now == nil {
		now = time.Now
	}
	return &AlertBook{w: w, host: host, b: b, now: now}
}

// Raise records that a rule holds now. The same alert again only refreshes it; a new one, or one whose level or text changed, is
// announced.
func (k *AlertBook) Raise(a wire.Alert) error {
	changed, err := k.w.RaiseAlert(a, k.now())
	if err != nil {
		return err
	}
	k.hint(changed)
	return nil
}

// Active is the keys of the alerts that are active in the table now. It is the truth about what is raised, whoever raised or cleared it —
// including a row of a kind or level this build does not know (S3-3a: the list a reader is shown leaves it out, this does not), so that a
// rule never takes its own raised alert for one that is not.
func (k *AlertBook) Active() (map[string]bool, error) {
	if kw, ok := k.w.(interface{ ActiveAlertKeys() ([]string, error) }); ok {
		keys, err := kw.ActiveAlertKeys()
		if err != nil {
			return nil, err
		}
		out := make(map[string]bool, len(keys))
		for _, key := range keys {
			out[key] = true
		}
		return out, nil
	}
	as, err := k.w.ActiveAlerts()
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(as))
	for _, a := range as {
		out[a.Key] = true
	}
	return out, nil
}

// Clear records that the rule with this key no longer holds.
func (k *AlertBook) Clear(key string) error {
	changed, err := k.w.ClearAlert(key, k.now())
	if err != nil {
		return err
	}
	k.hint(changed)
	return nil
}

// hint announces a change.
func (k *AlertBook) hint(changed bool) {
	if changed && k.b != nil {
		k.b.PublishAlert(k.host)
	}
}
