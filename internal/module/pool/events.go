package poolmod

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/wwd/wire"
)

// EventPoolChanged is the host event the App hears when a host's entry in the pool changed (wwd spec §10): {host}. It is a notice: the
// App re-reads GET /api/pool/hosts. Every client of the host events gets it. A daemon that sends it announces pool.events.v1, and the
// App then stops polling the hosts.
const EventPoolChanged = "pool.changed"

const (
	// defaultPollEvery is how often the local wwd's pool is read to see what changed. wwd samples a host every 5 s (wire.SampleIntervalS), so
	// there is nothing newer to find sooner, and one reader here serves every App connected to this daemon.
	defaultPollEvery = 5 * time.Second
	// defaultMergeWithin is the least time between two notices for one host: changes inside it are merged into one notice sent when it ends.
	defaultMergeWithin = 2 * time.Second
)

// watch reads the local wwd's pool every m.pollEvery until ctx ends and announces the hosts whose entry changed. The first read is only
// the baseline (what is there already is not news), and so is the first read after wwd was away: the App re-reads when its own
// connection to this daemon comes back, and a daemon that has just seen wwd again has nothing to compare with. wwd being absent is
// retried quietly: a failure is logged when it starts and when it changes, not at every poll.
func (m *Module) watch(ctx context.Context) {
	known := map[string]string{} // host alias -> what its entry looked like when last read
	based := false
	last := ""
	for {
		p, err := m.client().Pool(ctx)
		switch {
		case err == nil:
			last = ""
			m.compare(known, based, p)
			based = true
		case ctx.Err() != nil:
			return
		default:
			based = false
			if s := err.Error(); s != last {
				last = s
				m.logf("pool: events: reading the pool: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.pollEvery):
		}
	}
}

// compare notes how each host's entry looks now, announces the hosts that differ from the last look (a host that came or went is a
// change of it), and forgets the hosts that are gone. The entry is the one GET /api/pool/hosts gives, so the notice is exactly "that
// answer is different for this host now".
func (m *Module) compare(known map[string]string, based bool, p wire.Pool) {
	now := map[string]string{}
	for _, h := range hostsOf(p).Hosts {
		b, err := json.Marshal(h)
		if err != nil || !wire.ValidAlias(h.Info.Alias) {
			continue
		}
		now[h.Info.Alias] = string(b)
	}
	for alias, fp := range now {
		if old, had := known[alias]; based && (!had || old != fp) {
			m.notify(alias)
		}
		known[alias] = fp
	}
	for alias := range known {
		if _, still := now[alias]; !still {
			delete(known, alias)
			if based {
				m.notify(alias)
			}
		}
	}
}

// notify announces host, at once if its last notice is mergeWithin or more ago, else when that time is up (a second change inside the
// window joins the notice already waiting).
func (m *Module) notify(host string) {
	m.mu.Lock()
	if !m.watching {
		m.mu.Unlock()
		return
	}
	if _, waiting := m.pending[host]; waiting {
		m.mu.Unlock()
		return
	}
	now := m.now()
	wait := m.mergeWithin - now.Sub(m.sentAt[host])
	if wait <= 0 {
		m.sentAt[host] = now
		m.mu.Unlock()
		m.send(host)
		return
	}
	defer m.mu.Unlock()
	var t *time.Timer
	t = time.AfterFunc(wait, func() {
		m.mu.Lock()
		if m.pending[host] != t { // Stop took it back
			m.mu.Unlock()
			return
		}
		delete(m.pending, host)
		m.sentAt[host] = m.now()
		m.mu.Unlock()
		m.send(host)
	})
	m.pending[host] = t
}

// send broadcasts pool.changed for host (non-blocking: a client that cannot keep up misses a notice and re-reads on its next one).
func (m *Module) send(host string) {
	b, err := json.Marshal(wire.PoolChanged{Host: host})
	if err != nil {
		return
	}
	ev := core.HostEvent{Type: EventPoolChanged, Value: string(b)}
	// Stop waits for the sends that have begun (sendMu) and a send that begins after it sees watching false, so nothing goes out once Stop
	// has returned, also from a notice that was already past its window when Stop came
	m.sendMu.RLock()
	defer m.sendMu.RUnlock()
	m.mu.Lock()
	if !m.watching {
		m.mu.Unlock()
		return
	}
	emit, c := m.emit, m.core
	m.mu.Unlock()
	switch {
	case emit != nil:
		emit(ev)
	case c != nil && c.Events != nil:
		c.Events.BroadcastEvent(ev)
	}
}
