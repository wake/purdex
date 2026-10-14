package peers

// The remote half of "which pool names are in use" (name pool spec §4): an
// in-process snapshot of the pool names every other configured host's live
// rows hold, filled by every OK answer fetchHostResult gets and refreshed in
// the background, so the naming path (a 1 s budget) never waits on another
// host.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
)

const (
	// remoteNamesRefresh is how often the background refresher asks every
	// configured host for its rows.
	remoteNamesRefresh = 30 * time.Second
	// remoteNamesTTL is how long a host that stops answering keeps holding the
	// names of its last answer; after it, it holds nothing.
	remoteNamesTTL = 5 * time.Minute
)

// remoteHolder is one pool name held on a host and when its conversation's
// process started. fromRow says the start was the row's own proc_start; false
// means this daemon first saw the holder at start, and ident (the holder's ref
// and pid) says whose first sighting that is: a different holder taking the
// name starts a new one.
type remoteHolder struct {
	start   time.Time
	fromRow bool
	ident   string
}

type hostNames struct {
	readAt time.Time
	ticket uint64 // the request this set answers (remoteNameSnapshot.ticket)
	names  map[string]remoteHolder
}

// remoteNameSnapshot is safe for concurrent use.
type remoteNameSnapshot struct {
	mu    sync.Mutex
	hosts map[string]*hostNames
	seq   uint64 // last ticket handed out
}

func newRemoteNameSnapshot() *remoteNameSnapshot {
	return &remoteNameSnapshot{hosts: map[string]*hostNames{}}
}

// record replaces hostID's set with the pool names rows hold, read at now.
// rows must already have been through normalizeRemoteRows: a remote is
// attacker-controlled, and only a name that function left on a row — routable,
// on a name-addressable row, of a v5 daemon — is one this host would route by.
// A row holds a name only while it carries a live cc process (a dead or
// sleeping conversation holds nothing, spec decision 3). A pre-v5 daemon has
// no pool, so its answer holds nothing.
//
// A holder's process start is its row's proc_start; when that is missing or
// unparsable, the time this daemon first saw the holder (kept while the name
// stays in the answers). Two rows holding one name keep the earlier start.
func (s *remoteNameSnapshot) record(hostID string, rows []ipeers.PeerRecord, addressVersion int, now time.Time) {
	s.recordTicket(hostID, rows, addressVersion, s.ticket(), now)
}

// ticket numbers a request about to be sent: strictly increasing, so two
// requests are never equal however coarse the clock.
func (s *remoteNameSnapshot) ticket() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return s.seq
}

// recordTicket is record for the answer to the request that drew ticket. Fetches
// of one host run concurrently (the refresher and every scope=all), so answers
// are applied in the order their requests were sent, not the order they
// finished: one to an older request than the stored set answers is dropped, or
// a slow old answer would hand a name back that a newer one showed held.
func (s *remoteNameSnapshot) recordTicket(hostID string, rows []ipeers.PeerRecord, addressVersion int, ticket uint64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var prev map[string]remoteHolder
	if h := s.hosts[hostID]; h != nil {
		if ticket < h.ticket {
			return
		}
		prev = h.names
	}
	names := map[string]remoteHolder{}
	if addressVersion >= ipeers.AddressVersionV5 {
		for _, rec := range rows {
			if !ipeers.NameAddressable(rec) || rec.Agent == nil || rec.Agent.Type != "cc" || rec.Agent.PID == 0 {
				continue
			}
			name, ok := ipeers.PoolNameOf(rec.Name)
			if !ok || !ipeers.RoutableName(rec.Name) {
				continue
			}
			ident := fmt.Sprintf("%s/%d", rec.Ref, rec.Agent.PID)
			h := remoteHolder{start: now, ident: ident}
			if t, err := ipeers.ParseProcStart(rec.Agent.ProcStart); err == nil {
				h = remoteHolder{start: t, fromRow: true, ident: ident}
			} else if p, seen := prev[name]; seen && !p.fromRow && p.ident == ident {
				h = p
			}
			if cur, dup := names[name]; dup && !h.start.Before(cur.start) {
				continue
			}
			names[name] = h
		}
	}
	s.hosts[hostID] = &hostNames{readAt: now, ticket: ticket, names: names}
}

// held is every pool name some other host holds, with the earliest process
// start among its holders. A host not read within remoteNamesTTL holds
// nothing.
func (s *remoteNameSnapshot) held(now time.Time) map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]time.Time{}
	for _, h := range s.hosts {
		if now.Sub(h.readAt) >= remoteNamesTTL {
			continue
		}
		for name, holder := range h.names {
			if cur, ok := out[name]; !ok || holder.start.Before(cur) {
				out[name] = holder.start
			}
		}
	}
	return out
}

// prune forgets every host whose id is not in keep (a host removed from the
// config).
func (s *remoteNameSnapshot) prune(keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.hosts {
		if !keep[id] {
			delete(s.hosts, id)
		}
	}
}

// noteRemoteNames files one OK answer's normalized rows. No-op without a pool.
func (m *Module) noteRemoteNames(hostID string, rows []ipeers.PeerRecord, addressVersion int, ticket uint64) {
	if m.remoteNames == nil {
		return
	}
	m.remoteNames.recordTicket(hostID, rows, addressVersion, ticket, m.now())
}

// startNamePoolRefresher starts the background refresher: every
// remoteNamesRefresh (m.namePoolRefresh when set) it fetches every configured
// host through fetchHostResult, which files the answers. It is joined by Stop
// through reapWG, after stopCtx is cancelled. No-op without a pool.
func (m *Module) startNamePoolRefresher() {
	if m.remoteNames == nil || m.core == nil {
		return
	}
	every := m.namePoolRefresh
	if every <= 0 {
		every = remoteNamesRefresh
	}
	// Until the first pass over the other hosts is done, naming a conversation
	// with a pool name waits for it (awaitPoolReady): a daemon that has just
	// started must not pick blind to what the other hosts hold. The barrier is
	// made here, before Start returns and the first request is served.
	ready := make(chan struct{})
	m.poolReady = ready
	m.reapWG.Add(1)
	go func() {
		defer m.reapWG.Done()
		m.refreshRemoteNames(m.stopCtx)
		close(ready)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-m.stopCtx.Done():
				return
			case <-t.C:
				m.refreshRemoteNames(m.stopCtx)
			}
		}
	}()
}

func (m *Module) refreshRemoteNames(ctx context.Context) {
	hosts := m.configSnapshot().hosts
	keep := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		keep[h.HostID] = true
	}
	m.remoteNames.prune(keep)
	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(h config.PeerHost) {
			defer wg.Done()
			m.fetchHostResult(ctx, h)
		}(h)
	}
	wg.Wait()
}
