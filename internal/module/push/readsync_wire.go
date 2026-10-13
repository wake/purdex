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

// onRead is one accepted read of the reads module: it becomes pending for every registration that syncs reads and has not
// been superseded. The choice and the recording happen under m.mu, the lock every removal path holds when it forgets a
// registration, so a registration that is removed (or stops syncing) at that moment is never handed a pending list afterwards.
func (m *Module) onRead(rd reads.Read) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.devices {
		if !d.Prefs.ReadSync || m.supersededLocked(d) {
			continue
		}
		var last time.Time
		if d.LastSentAt > 0 {
			last = time.UnixMilli(d.LastSentAt)
		}
		m.rs.Add(d.DeviceID, rd, last)
	}
}

// supersededLocked: a newer registration of the same paired phone, bundle and environment exists — the APNs token rotated,
// and d is the dead one (APNs will say so on the next push; until then nothing should be kept for it). A registration of the
// same phone in another environment is not a replacement. Newer is the later registration of this run, else the later
// update; if even that is equal the order is unknown and nobody is superseded. Under m.mu.
func (m *Module) supersededLocked(d push.Device) bool {
	if d.OwnerDeviceID == "" {
		return false
	}
	for _, o := range m.devices {
		if o.DeviceID == d.DeviceID || o.OwnerDeviceID != d.OwnerDeviceID || o.Env != d.Env || o.BundleID != d.BundleID {
			continue
		}
		switch {
		case m.regSeq[o.DeviceID] != m.regSeq[d.DeviceID]:
			if m.regSeq[o.DeviceID] > m.regSeq[d.DeviceID] {
				return true
			}
		case o.UpdatedAt > d.UpdatedAt:
			return true
		}
		// equal on both: the order is unknown (a restart and the same millisecond), and a device id, being a hash of the token,
		// says nothing about it — neither supersedes the other.
	}
	return false
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

// registered is called, under m.mu, when a registration is stored: one that does not (or no longer) syncs reads keeps nothing
// pending, and the older registrations it supersedes (the same paired phone's earlier token for this bundle and environment)
// give up theirs.
func (m *Module) registered(d push.Device) {
	if !d.Prefs.ReadSync {
		m.rs.Forget(d.DeviceID)
	}
	for _, o := range m.devices {
		if o.DeviceID != d.DeviceID && m.supersededLocked(o) {
			m.rs.Forget(o.DeviceID)
		}
	}
}
