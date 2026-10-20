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
// the baseline (what is there already is not news). When wwd goes away every host is announced once (the answer of /api/pool/hosts is an
// error now), and the first read after it is back announces every host there was or is: the App stopped polling when it saw
// pool.events.v1 and its connection to this daemon did not drop with wwd's, so without these notices it would keep an old pool for as long
// as nothing else changed. wwd being absent is retried quietly: a failure is logged when it starts and when it changes, not at every poll.
func (m *Module) watch(ctx context.Context) {
	known := map[string]string{} // host alias -> what its entry looked like when last read
	based, away := false, false
	last := ""
	for {
		p, err := m.client().PoolWithLiveness(ctx) // the entry compared is the one GET /api/pool/hosts gives, liveness included
		switch {
		case err == nil:
			last = ""
			m.setSelf(&p)
			m.setHosts(p)
			m.compare(known, based, away, p)
			based, away = true, false
		case ctx.Err() != nil:
			return
		default:
			m.setSelf(nil)
			if based && !away {
				away = true
				for alias := range known {
					m.notify(alias)
				}
			}
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
// answer is different for this host now". Before the baseline nothing is announced; with all, every host in the answer or in the last look
// is (wwd was away).
func (m *Module) compare(known map[string]string, based, all bool, p wire.Pool) {
	now := map[string]string{}
	for _, h := range hostsOf(p).Hosts {
		h.LivenessAt = nil // when the peer was last looked at moves at every poll; a change of the entry is a change of what it says
		b, err := json.Marshal(h)
		if err != nil || !wire.ValidAlias(h.Info.Alias) {
			continue
		}
		now[h.Info.Alias] = string(b)
	}
	for alias, fp := range now {
		if old, had := known[alias]; based && (all || !had || old != fp) {
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
// window joins the notice already waiting). A notice is let through only here, under mu and while watching: it is counted in m.sending
// at once, so Stop, which waits for the count to reach 0, cannot return before it is emitted, and a timer callback that Stop (or a Stop and
// a Start) overtook finds its entry gone and does nothing.
func (m *Module) notify(host string) { m.notifyKind(EventPoolChanged, host) }

// noticeKey keys the merge window of a notice: pool.changed by the bare host (its first and only kind), the others by kind and host.
func noticeKey(kind, host string) string {
	if kind == EventPoolChanged {
		return host
	}
	return kind + "|" + host
}

// notifyKind is notify for any of the notices this module sends (pool.changed, alerts.changed): each kind has its own window per host.
func (m *Module) notifyKind(kind, host string) {
	key := noticeKey(kind, host)
	m.mu.Lock()
	if !m.watching {
		m.mu.Unlock()
		return
	}
	if _, waiting := m.pending[key]; waiting {
		m.mu.Unlock()
		return
	}
	now := m.now()
	wait := m.mergeWithin - now.Sub(m.sentAt[key])
	if wait <= 0 {
		m.sentAt[key] = now
		m.sending++
		m.mu.Unlock()
		m.emitNotice(kind, host)
		return
	}
	defer m.mu.Unlock()
	var t *time.Timer
	t = time.AfterFunc(wait, func() {
		m.mu.Lock()
		if m.pending[key] != t { // Stop took it back
			m.mu.Unlock()
			return
		}
		delete(m.pending, key)
		m.sentAt[key] = m.now()
		m.sending++
		m.mu.Unlock()
		m.emitNotice(kind, host)
	})
	m.pending[key] = t
}

// emitNotice broadcasts the notice of kind for host (non-blocking: a client that cannot keep up misses a notice and re-reads on its next one)
// and takes the notice off the count Stop waits on. The caller has counted it.
func (m *Module) emitNotice(kind, host string) {
	defer func() {
		m.mu.Lock()
		m.sending--
		m.sendIdle.Broadcast()
		m.mu.Unlock()
	}()
	b, err := json.Marshal(wire.PoolChanged{Host: host}) // {host}, the same body for every kind
	if err != nil {
		return
	}
	ev := core.HostEvent{Type: kind, Value: string(b)}
	m.mu.Lock()
	emit, c := m.emit, m.core
	m.mu.Unlock()
	switch {
	case emit != nil:
		emit(ev)
	case c != nil && c.Events != nil:
		c.Events.BroadcastEvent(ev)
	}
}
