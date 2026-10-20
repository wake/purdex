package wwdalerts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Waking the load watcher (S3-1): every alert episode the wake ledger has not seen wakes the watcher once, whatever its level (push,
// digest, and the watch level that comes later), so the watcher does not have to patrol. The wake is separate from the push: it does
// not depend on APNs or on the push switch.

const (
	// wakeThrottle is the least time between two wakes about the same (host, key); an episode inside it is recorded and not made up.
	wakeThrottle = 10 * time.Minute
	// wakeBudget bounds one WakeWatcher call: it covers the watcher's registration window (a start may take that long to show up).
	wakeBudget = watch.RegisterWithin + watch.StartGrace + 10*time.Second
	// wakeReason is the reason the watcher is told.
	wakeReason = "wwd_alert"
	// wakeDetailMax bounds the detail of one alert in the note, in runes.
	wakeDetailMax = 120
	// wakeLineMax bounds one alert line in bytes, leaving room in the 500 of a note for the folded count.
	wakeLineMax = 400
	// wakeAdoptWithin is how recently a shared silence must have been seen holding for a restarted daemon to take it as the same failure.
	wakeAdoptWithin = 10 * time.Minute
)

// wakeKindNames is what the note calls each alert kind (a wire.Alert has no title). A kind not here is shown by its own name.
var wakeKindNames = map[wire.AlertKind]string{
	wire.AlertDiskLow:        "磁碟空間不足",
	wire.AlertDiskFalling:    "磁碟空間快速下降",
	wire.AlertPressureHeld:   "記憶體壓力持續",
	wire.AlertMservService:   "mserv 服務異常",
	wire.AlertWorkerCrash:    "worker 反覆當機",
	wire.AlertSilence:        "主機失聯",
	wire.AlertThrash:         "換頁過量",
	wire.AlertResidentGrowth: "常駐記憶體成長",
	wire.AlertOrphans:        "孤兒行程",
}

func wakeKindName(k wire.AlertKind) string {
	if n, ok := wakeKindNames[k]; ok {
		return n
	}
	return string(k)
}

// wakeRule is who wakes the watcher, apart from deliverRule (the push module's state is not a part of it):
//   - the fleet primary always wakes;
//   - a standby wakes only when the primary is known not to: it did not answer, or it answered that it cannot read the alerts or push
//     (push_ready=false). Nothing observed yet (!Known), or an older daemon's answer without push_ready, is not a reason: at start both
//     daemons would wake. The known cost: a primary that reads alerts but cannot push has both wake the watcher (once, not missed);
//   - an off daemon, and one without a role view, never wakes.
func wakeRule(rv fleet.RoleView) func() bool {
	return func() bool {
		if rv == nil {
			return false
		}
		switch rv.State().Role {
		case fleet.RolePrimary:
			return true
		case fleet.RoleStandby:
			v, ok := rv.(fleet.PeerPushView)
			if !ok {
				return false
			}
			p := v.PeerPush()
			return (p.Known && !p.Answered) || (p.Answered && p.Ready != nil && !*p.Ready)
		}
		return false
	}
}

// wakeBackoff is how long a failed call waits before the next try: 2 minutes after the first failure, doubling, at most 15.
func wakeBackoff(failures int) time.Duration {
	d := 2 * time.Minute
	for i := 1; i < failures && d < 15*time.Minute; i++ {
		d *= 2
	}
	if d > 15*time.Minute {
		d = 15 * time.Minute
	}
	return d
}

// wakeItem is one episode waiting for the worker, with its line of the note.
type wakeItem struct {
	ep   wakeEp
	line string
}

// wakeQueue holds what waits for the one wake worker: the episodes, folded into one note when the worker takes them. flight holds every
// episode that is waiting or being called, so one episode never has two wakes (a wake may start the watcher's session).
type wakeQueue struct {
	mu     sync.Mutex
	items  []wakeItem
	flight map[wakeEp]bool
	busy   bool // the worker is inside WakeWatcher
	signal chan struct{}
	once   sync.Once
}

func (q *wakeQueue) init() { q.once.Do(func() { q.signal = make(chan struct{}, 1) }) }

// idle says nothing waits and the worker is not inside a call.
func (q *wakeQueue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) == 0 && !q.busy
}

// enqueueWake never blocks: it leaves the episode for the worker, folded into the ones already waiting. An episode already waiting or in
// a call is not added again.
func (m *Module) enqueueWake(ep wakeEp, line string) {
	q := &m.wakeQ
	q.init()
	q.mu.Lock()
	if q.flight == nil {
		q.flight = map[wakeEp]bool{}
	}
	if q.flight[ep] {
		q.mu.Unlock()
		return
	}
	q.flight[ep] = true
	q.items = append(q.items, wakeItem{ep, line})
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// startWake runs the one wake worker, ended with ctx and joined by Stop.
func (m *Module) startWake(ctx context.Context) {
	q := &m.wakeQ
	q.init()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-q.signal:
			}
			q.mu.Lock()
			items := q.items
			q.items = nil
			q.busy = len(items) > 0
			q.mu.Unlock()
			if len(items) == 0 {
				continue
			}
			m.wakeOnce(ctx, items)
			q.mu.Lock()
			for _, it := range items {
				delete(q.flight, it.ep)
			}
			q.busy = false
			q.mu.Unlock()
		}
	}()
}

// wakeOnce makes the one call for the episodes taken together. Success marks all of them told; a failure (no watch module, an error,
// a timeout) leaves all of them pending with a backoff. A call ended by the module stopping is not a failure.
func (m *Module) wakeOnce(ctx context.Context, items []wakeItem) {
	eps := make([]wakeEp, len(items))
	for i, it := range items {
		eps[i] = it.ep
	}
	text := items[0].line
	if len(items) > 1 {
		text += fmt.Sprintf("；另 %d 則", len(items)-1)
	}
	err := errors.New("the watch module is not running")
	if m.waker != nil {
		if w, ok := m.waker(); ok {
			cctx, cancel := context.WithTimeout(ctx, wakeBudget)
			err = w.WakeWatcher(cctx, wakeReason, text)
			cancel()
		}
	}
	if ctx.Err() != nil {
		return // stopping: still pending, tried again after a restart
	}
	if err == nil {
		if e := m.store.markWoken(eps, m.now()); e != nil {
			m.logf("wwdalerts: wake ledger: %v", e)
		}
		return
	}
	m.logf("wwdalerts: wake the watcher (%s): %v; tried again later", text, err)
	if e := m.store.markWakeFailed(eps, m.now()); e != nil {
		m.logf("wwdalerts: wake ledger: %v", e)
	}
}

// wakeLine is the note about one alert: "<host>：<kind>｜<detail>".
func wakeLine(about string, a wire.Alert) string {
	s := about + "：" + wakeKindName(a.Kind)
	if d := watch.Plain(strings.TrimSpace(a.Detail)); d != "" {
		r := []rune(d)
		if len(r) > wakeDetailMax {
			r = append(r[:wakeDetailMax], '…')
		}
		s += "｜" + string(r)
	}
	for len(s) > wakeLineMax {
		r := []rune(s)
		s = string(r[:len(r)-1])
	}
	return s
}

// mkEp builds an episode identity; the time is normalised to the millisecond the ledger keeps, so it compares (as a map key) equal to
// the one read back from the ledger.
func mkEp(host, key string, since time.Time) wakeEp {
	return wakeEp{host, key, time.UnixMilli(since.UnixMilli())}
}

// wakeIdentity is the episode an alert stands for: its own (host, key, since), or for a silence, the failure markEpisodes decided on.
func (m *Module) wakeIdentity(host string, a wire.Alert) (ep wakeEp, about string, shared bool) {
	if sharedLedger(a) {
		return mkEp(silenceLedger, a.Key, m.episode[a.Key]), strings.TrimPrefix(a.Key, "silence:"), true
	}
	return mkEp(host, a.Key, a.Since), host, false
}

// wakeAlerts is called once per list read. The first look at a host after the wake ledger exists (the first deployment) records what
// stands as already told and only logs it; a host's Since is its own clock's, never compared with this one's. After that every new
// episode is recorded before anything is asked, and queued for the wake when this daemon is the one to wake.
func (m *Module) wakeAlerts(host string, alerts []wire.Alert, now time.Time) {
	if !m.store.hostSeeded(host) {
		var eps []wakeEp
		for _, a := range alerts {
			if a.ClearedAt == nil {
				ep, _, _ := m.wakeIdentity(host, a)
				eps = append(eps, ep)
			}
		}
		if err := m.store.seedWake(host, eps, now); err != nil {
			m.logf("wwdalerts: wake ledger %s: %v", host, err)
		} else if len(eps) > 0 {
			m.logf("wwdalerts: wake ledger started for %s: %d standing alerts recorded, not woken for", host, len(eps))
		}
		return
	}
	if m.wakeRule == nil || !m.wakeRule() {
		return // not this daemon's to wake: nothing is recorded, so it wakes for what stands when it becomes so
	}
	standing := map[wakeEp]bool{}
	for _, a := range alerts {
		if a.ClearedAt != nil {
			continue
		}
		ep, about, shared := m.wakeIdentity(host, a)
		standing[ep] = true
		var adopt time.Duration
		if shared && !m.wakeSeen[a.Key] {
			adopt = wakeAdoptWithin // the first look at this key since the daemon started: the ledger may know the failure
		}
		_, err := m.store.claimWake(ep, now, wakeThrottle, adopt)
		if shared {
			if m.wakeSeen == nil {
				m.wakeSeen = map[string]bool{}
			}
			m.wakeSeen[a.Key] = true
		}
		if err != nil {
			m.logf("wwdalerts: wake ledger %s/%s: %v", ep.host, ep.key, err)
			continue
		}
		// a new episode, or one whose call failed and whose backoff is over (also after a restart)
		if m.store.pendingDue(ep, now) {
			m.enqueueWake(ep, wakeLine(about, a))
		}
	}
	m.dropGone(host, standing)
}

// dropGone gives up on this host's pending episodes that no longer stand: the alert cleared, so there is nothing to tell. Silence
// episodes are shared by all hosts and are judged in dropGoneSilences.
func (m *Module) dropGone(host string, standing map[wakeEp]bool) {
	eps, err := m.store.pendingOf(host)
	if err != nil {
		m.logf("wwdalerts: wake ledger %s: %v", host, err)
		return
	}
	for _, e := range eps {
		if !standing[e] {
			m.logf("wwdalerts: wake: %s/%s cleared before the watcher was told, given up", e.host, e.key)
			if err := m.store.dropPending(e); err != nil {
				m.logf("wwdalerts: wake ledger %s: %v", host, err)
			}
		}
	}
}

// dropGoneSilences gives up on pending silence episodes no list holds now (or that are another failure by now).
func (m *Module) dropGoneSilences() {
	if m.wakeRule == nil || !m.wakeRule() {
		return
	}
	held := map[string]bool{}
	for _, keys := range m.lastHeld {
		for k := range keys {
			held[k] = true
		}
	}
	standing := map[wakeEp]bool{}
	for k := range held {
		standing[mkEp(silenceLedger, k, m.episode[k])] = true
	}
	m.dropGone(silenceLedger, standing)
}
