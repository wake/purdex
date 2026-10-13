package push

import (
	"log"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/reads"
)

// The module's side of read sync (badge spec §7.2 "Pushes"): follow the reads module's feed, keep the reads pending for the
// registrations that asked (prefs.read_sync), ask the sender for a flush push when one has waited long enough, and drop all of
// it with the registration.

// readCarrier is what the sender needs of the pending reads: the ones to put on the push it is building, and the word that a
// push reached the phone with exactly those.
type readCarrier interface {
	Reserve(deviceID string) []reads.Read
	Sent(deviceID string, sent []reads.Read)
}

// initReadSync builds the pending-read state with the flush interval of [push] read_flush_minutes (an invalid value is
// reported and the default used).
func (m *Module) initReadSync(cfg config.Config) {
	minutes, valid := cfg.PushReadFlushMinutes()
	if !valid {
		log.Printf("[push] read_flush_minutes is outside 1-120: using %d", config.DefaultReadFlushMinutes)
	}
	m.rs.flushAfter = time.Duration(minutes) * time.Minute
}

// followReads subscribes to the reads module's feed (a seam: nil = the registry's), for the life of this run.
func (m *Module) followReads() {
	m.rs.Start()
	feed := m.readsFeed
	if feed == nil {
		svc, _ := m.core.Registry.Get(reads.FeedKey)
		f, ok := svc.(reads.Feed)
		if !ok {
			log.Printf("[push] the reads module's feed is not available: reads will not be carried on pushes")
			return
		}
		feed = f
	}
	m.unsubReads = feed.SubscribeReads(m.onRead)
}

// stopReads ends the subscription and cancels every flush timer.
func (m *Module) stopReads() {
	if m.unsubReads != nil {
		m.unsubReads()
		m.unsubReads = nil
	}
	m.rs.Stop()
}

// onRead is one accepted read of the reads module: it becomes pending for every registration that syncs reads.
func (m *Module) onRead(rd reads.Read) {
	for _, d := range m.snapshot() {
		if !d.Prefs.ReadSync {
			continue
		}
		var last time.Time
		if d.LastSentAt > 0 {
			last = time.UnixMilli(d.LastSentAt)
		}
		m.rs.Add(d.DeviceID, rd, last)
	}
}

// fireReadsFlush asks the sender for the push that carries one registration's pending reads alone (kind "reads", the
// background headers of BG-1b). The sender sends it only if the registration still syncs reads and has something pending.
func (m *Module) fireReadsFlush(deviceID string) {
	snd := m.sender.Load()
	if snd == nil {
		return
	}
	if !snd.Enqueue(Job{DeviceIDs: []string{deviceID}, SkipOpen: true, Make: func(d push.Device) (push.Content, bool) {
		if !d.Prefs.ReadSync {
			return push.Content{}, false
		}
		return push.Content{Kind: "reads", Background: true}, true
	}}) {
		log.Printf("[push] reads flush dropped: the send queue is full (the next push carries the reads)")
	}
}

// registered is called, under m.mu, when a registration is stored: one that does not (or no longer) syncs reads keeps nothing pending, and
// the phone's other registrations (an older APNs token of the same paired phone) are not in anyone's hands any more.
func (m *Module) registered(d push.Device) {
	if !d.Prefs.ReadSync {
		m.rs.Forget(d.DeviceID)
	}
	if d.OwnerDeviceID == "" {
		return
	}
	for _, other := range m.devices { // the caller holds m.mu
		if other.OwnerDeviceID == d.OwnerDeviceID && other.DeviceID != d.DeviceID {
			m.rs.Forget(other.DeviceID)
		}
	}
}
