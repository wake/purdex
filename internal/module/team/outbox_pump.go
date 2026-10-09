package teammod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
)

// The outbox pump (cross-host team spec §3.1 rule 6). One pump per side: L's commands (X3a), M's facts (X2c). It is
// written against outboxStore so both sides run the same code: the store knows its table, its path and how to apply an
// answer; the pump knows the FIFO, the backoff and the classification.
//
//   - one drain per peer HOST at a time, entries in FIFO order (the head blocks the rest of its host, never another host);
//   - the first attempt is made right after the cause committed (kick), not at the next tick;
//   - 30 s, doubling to a 10 min cap, forever (the expiry of X-U8 is the store's, not the pump's);
//   - classification follows peersmod.CallClass: done and a permanent refusal settle the entry (and go on to the next),
//     transient / unsupported / a young 401 back off and block the host's queue, an unpaired host or a 401 that lasted
//     10 minutes (Escalate401) ends the relation on this side (outboxStore.Unpaired).

const (
	pumpBackoffBase = 30 * time.Second
	pumpBackoffCap  = 10 * time.Minute
	pumpTick        = time.Second
)

// outboxEntry is the part of an outbox row the pump needs.
type outboxEntry struct {
	ID       string
	HostID   string
	Path     string
	Body     json.RawMessage // the request body; it names to_host_id (HostCaller checks it)
	Attempts int
	// Kind is the entry's kind when the peer must announce it before it is sent (a kindGate store); "" otherwise.
	Kind string
	// First401At is when the current run of 401s began (unix ms), 0 when the last attempt was not a 401.
	First401At int64
}

// outboxStore is one side's table.
type outboxStore interface {
	// Hosts lists the host ids with a pending entry.
	Hosts() ([]string, error)
	// Head is the oldest pending entry of the host and when it may be tried next (unix ms).
	Head(hostID string) (e outboxEntry, nextAt int64, ok bool, err error)
	// Attempted records a failed attempt: one more attempt, the next try time and the 401 run's start (0 clears it).
	Attempted(id string, nextAt, first401At int64) error
	// Settle marks the entry done AND applies the answer in ONE transaction (rule 4); res is a done answer or a permanent
	// refusal (ClassRefused / ClassWrongHost). An entry that is no longer pending is left alone.
	Settle(e outboxEntry, res peersmod.CallResult) error
	// Unpaired ends the relation with the host on this side (rule 6, §3.2): reason is "unpaired" or "unpaired_by_peer".
	Unpaired(hostID, reason string) error
}

// kindGate is an optional part of an outboxStore whose entries carry a Kind the receiving host has to announce before
// the entry is sent (the symmetric half of rule 7: a lead host announces the fact kinds it applies, X4a-3). Without it a
// kind the peer does not know comes back as a JSON 400, which the pump reads as a permanent refusal and settles — the fact
// would be lost. An entry whose kind is not announced is held, not settled: it backs off like any other failure, with no
// attempt limit, and goes out in the first round after the peer announces it.
type kindGate interface {
	Announces(caps ipeers.TeamCaps, kind string) bool
}

// capsTTL is how long a host's capabilities are reused by the gate (ms).
const capsTTL = 30_000

// hostCaller is *peersmod.HostCaller as the pump uses it (a test seam).
type hostCaller interface {
	Call(ctx context.Context, targetHostID, path string, body any) peersmod.CallResult
	Paired(hostID string) bool
	HostIDOf(alias string) string
	AliasOf(hostID string) string
	TeamCaps(ctx context.Context, hostID string) (ipeers.TeamCaps, error)
}

type outboxPump struct {
	name   string
	caller hostCaller
	store  outboxStore
	now    func() int64
	logf   func(string, ...any)
	ctx    context.Context
	wg     *sync.WaitGroup

	sig     chan struct{}
	caps    map[string]cachedCaps
	mu      sync.Mutex
	running map[string]bool
	// stuck remembers, per host, the last error text logged, so a host that stays down logs once per change.
	stuck map[string]string
}

func newOutboxPump(name string, caller hostCaller, store outboxStore, now func() int64, logf func(string, ...any), ctx context.Context, wg *sync.WaitGroup) *outboxPump {
	return &outboxPump{name: name, caller: caller, store: store, now: now, logf: logf, ctx: ctx, wg: wg,
		sig: make(chan struct{}, 1), running: map[string]bool{}, stuck: map[string]string{}, caps: map[string]cachedCaps{}}
}

// cachedCaps is a host's capabilities and when they were read.
type cachedCaps struct {
	caps ipeers.TeamCaps
	at   int64
}

// capsOf is the host's capabilities, reused for capsTTL: a queue of facts asks once, not once per fact.
func (p *outboxPump) capsOf(hostID string) (ipeers.TeamCaps, error) {
	p.mu.Lock()
	c, ok := p.caps[hostID]
	p.mu.Unlock()
	if ok && p.now()-c.at < capsTTL {
		return c.caps, nil
	}
	caps, err := p.caller.TeamCaps(p.ctx, hostID)
	if err != nil {
		return ipeers.TeamCaps{}, err
	}
	p.mu.Lock()
	p.caps[hostID] = cachedCaps{caps: caps, at: p.now()}
	p.mu.Unlock()
	return caps, nil
}

// pumpBackoff is the wait after the n-th failed attempt (n ≥ 1): 30 s, 60 s, 120 s … capped at 10 min.
func pumpBackoff(n int) time.Duration {
	d := pumpBackoffBase
	for i := 1; i < n && d < pumpBackoffCap; i++ {
		d *= 2
	}
	if d > pumpBackoffCap {
		d = pumpBackoffCap
	}
	return d
}

// kick asks for a pass now (right after the cause committed); it never blocks.
func (p *outboxPump) kick() {
	select {
	case p.sig <- struct{}{}:
	default:
	}
}

// run is the pump's goroutine: a pass on every kick and every tick, until the context ends.
func (p *outboxPump) run() {
	defer p.wg.Done()
	ticker := time.NewTicker(pumpTick)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.sig:
		case <-ticker.C:
		}
		p.pass()
	}
}

// answerID is the id a 2xx answer carries ({id, host_id, outcome}), "" when it has none.
func answerID(body json.RawMessage) string {
	var a struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &a)
	return a.ID
}

// pass starts a drain for every host with something pending that has none running.
func (p *outboxPump) pass() {
	hosts, err := p.store.Hosts()
	if err != nil {
		p.logf("[team] %s outbox: %v", p.name, err)
		return
	}
	for _, h := range hosts {
		p.mu.Lock()
		if p.running[h] || p.ctx.Err() != nil {
			p.mu.Unlock()
			continue
		}
		p.running[h] = true
		p.mu.Unlock()
		// pass runs only on run()'s goroutine, which the wait group already counts: this Add happens while the counter is
		// positive, so it cannot race Stop's Wait (WaitGroup rule), and every drain ends before run() returns... or is waited for.
		p.wg.Add(1)
		go func(h string) {
			defer p.wg.Done()
			defer func() {
				p.mu.Lock()
				delete(p.running, h)
				p.mu.Unlock()
			}()
			p.drain(h)
		}(h)
	}
}

// drain sends the host's due entries in order until one has to wait.
func (p *outboxPump) drain(hostID string) {
	for p.ctx.Err() == nil {
		e, nextAt, ok, err := p.store.Head(hostID)
		if err != nil {
			p.logf("[team] %s outbox %s: %v", p.name, hostID, err)
			return
		}
		if !ok || nextAt > p.now() {
			return
		}
		if !p.attempt(e) {
			return
		}
	}
}

// attempt makes one call for the head entry and acts on its class; true means the entry was settled and the next one
// may go at once.
func (p *outboxPump) attempt(e outboxEntry) bool {
	if g, ok := p.store.(kindGate); ok && e.Kind != "" {
		caps, err := p.capsOf(e.HostID)
		var se *peersmod.CapsStatusError
		switch {
		case err != nil && !p.caller.Paired(e.HostID):
			p.unpaired(e.HostID, "unpaired") // the same verdict Call would have reached
			return false
		case errors.As(err, &se) && se.Code == http.StatusUnauthorized:
			p.onUnauthorized(e) // a 401 for ten minutes is unpaired_by_peer here as on a send
			return false
		case err != nil:
			p.backoff(e, 0, "its capabilities are unavailable: "+err.Error())
			return false
		case !g.Announces(caps, e.Kind):
			p.backoff(e, 0, "the host does not announce "+e.Kind+" yet; held")
			return false
		}
	}
	res := p.caller.Call(p.ctx, e.HostID, e.Path, e.Body)
	switch res.Class {
	case peersmod.ClassDone, peersmod.ClassRefused, peersmod.ClassWrongHost:
		// A done answer must be THIS entry's: HostCaller proved the host, not the command. Another id is a broken peer, not an
		// outcome to apply (and never to mark this entry done with).
		if res.Class == peersmod.ClassDone && answerID(res.Body) != e.ID {
			p.backoff(e, 0, "its answer names another command")
			return false
		}
		// done, or the peer's permanent refusal (a wrong_host too, rule 1): the outcome is applied with the entry
		if err := p.store.Settle(e, res); err != nil {
			// a local failure (the database, the outcome): the peer already has the command, so it is not sent again every
			// second — it backs off like any failure, and the stored outcome comes back when it is
			p.logf("[team] %s outbox %s (%s): settle: %v", p.name, e.ID, e.HostID, err)
			p.backoff(e, 0, "settling its answer failed")
			return false
		}
		p.mu.Lock()
		delete(p.stuck, e.HostID)
		p.mu.Unlock()
		return true
	case peersmod.ClassUnpaired:
		p.unpaired(e.HostID, "unpaired")
		return false
	case peersmod.ClassUnauthorized:
		p.onUnauthorized(e)
		return false
	default: // transient, unsupported (the route is missing: nothing else could apply either), anything unexpected
		p.backoff(e, 0, string(res.Class)+" "+res.Code)
		return false
	}
}

// onUnauthorized is the 401 rule: a run of 401s that lasts UnpairedByPeerAfter ends the relation on this side.
func (p *outboxPump) onUnauthorized(e outboxEntry) {
	now := p.now()
	first := e.First401At
	if first == 0 {
		first = now
	}
	if peersmod.Escalate401(time.UnixMilli(first), time.UnixMilli(now)) == peersmod.ClassUnpairedByPeer {
		p.unpaired(e.HostID, "unpaired_by_peer")
		return
	}
	// the next look is never later than the end of the 10 minutes, so the escalation is on time (not at the next
	// doubling step after it)
	p.backoffUntil(e, first, first+peersmod.UnpairedByPeerAfter.Milliseconds(), "401 (the peer does not know our token yet)")
}

func (p *outboxPump) unpaired(hostID, reason string) {
	p.logf("[team] %s outbox: host %s %s; ending the relation on this side", p.name, hostID, reason)
	if err := p.store.Unpaired(hostID, reason); err != nil {
		p.logf("[team] %s outbox: unpair %s: %v", p.name, hostID, err)
	}
}

func (p *outboxPump) backoff(e outboxEntry, first401 int64, why string) {
	p.backoffUntil(e, first401, 0, why)
}

// backoffUntil is backoff with a ceiling on the next try time (0: none).
func (p *outboxPump) backoffUntil(e outboxEntry, first401, ceil int64, why string) {
	next := p.now() + pumpBackoff(e.Attempts+1).Milliseconds()
	if ceil > 0 && next > ceil {
		next = ceil
	}
	if err := p.store.Attempted(e.ID, next, first401); err != nil {
		p.logf("[team] %s outbox %s: %v", p.name, e.ID, err)
		return
	}
	p.mu.Lock()
	changed := p.stuck[e.HostID] != why
	p.stuck[e.HostID] = why
	p.mu.Unlock()
	if changed {
		p.logf("[team] %s outbox: host %s: %s; trying again in %s", p.name, e.HostID, why, pumpBackoff(e.Attempts+1))
	}
}
