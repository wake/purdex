package daemon

import (
	"sync"
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
// The hint is a new event type, so it is published only while the proto 2 gate is open (spec §13.1): a proto 1 reader of /v1/events
// would refuse the type. The table is written whatever the gate says (a reader of the list is a proto 2 reader).
type AlertBook struct {
	w    AlertWriter
	host string
	b    *Broker
	open func() bool
	now  func() time.Time

	mu     sync.Mutex
	missed bool // a change was made while the gate was shut, so no hint went out for it
}

// NewAlertBook returns the book of host. open is the proto 2 gate (nil: shut); now is the clock (nil: time.Now).
func NewAlertBook(w AlertWriter, host string, b *Broker, open func() bool, now func() time.Time) *AlertBook {
	if now == nil {
		now = time.Now
	}
	return &AlertBook{w: w, host: host, b: b, open: open, now: now}
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

// Active is the keys of the alerts that are active in the table now. It is the truth about what is raised, whoever raised or cleared it.
func (k *AlertBook) Active() (map[string]bool, error) {
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

// hint announces a change. A change made while the gate is shut is remembered, and the first call after the gate opens announces it
// even when that call changed nothing, so a reader that stayed connected is not left with a list it never re-read.
func (k *AlertBook) hint(changed bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.b == nil || k.open == nil || !k.open() {
		k.missed = k.missed || changed
		return
	}
	if changed || k.missed {
		k.missed = false
		k.b.PublishAlert(k.host)
	}
}
