package wwdalerts

import (
	"context"
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

// wakeQueue holds what waits for the one wake worker: the first episode's line and how many came after it, folded into one note.
type wakeQueue struct {
	mu     sync.Mutex
	first  string
	have   bool
	more   int
	busy   bool // the worker is inside WakeWatcher
	signal chan struct{}
	once   sync.Once
}

func (q *wakeQueue) init() { q.once.Do(func() { q.signal = make(chan struct{}, 1) }) }

// idle says nothing waits and the worker is not inside a call.
func (q *wakeQueue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return !q.have && !q.busy
}

// enqueueWake never blocks: it leaves the episode for the worker, folded into the ones already waiting.
func (m *Module) enqueueWake(text string) {
	q := &m.wakeQ
	q.init()
	q.mu.Lock()
	if !q.have {
		q.first, q.have = text, true
	} else {
		q.more++
	}
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
			first, have, more := q.first, q.have, q.more
			q.first, q.have, q.more = "", false, 0
			q.busy = have
			q.mu.Unlock()
			if !have {
				continue
			}
			m.wakeOnce(ctx, first, more)
			q.mu.Lock()
			q.busy = false
			q.mu.Unlock()
		}
	}()
}

func (m *Module) wakeOnce(ctx context.Context, text string, more int) {
	var w watch.Waker
	ok := false
	if m.waker != nil {
		w, ok = m.waker()
	}
	if !ok {
		m.logf("wwdalerts: wake: the watch module is not running, the watcher is not told: %s", text)
		return
	}
	if more > 0 {
		text += fmt.Sprintf("；另 %d 則", more)
	}
	cctx, cancel := context.WithTimeout(ctx, wakeBudget)
	defer cancel()
	if err := w.WakeWatcher(cctx, wakeReason, text); err != nil && ctx.Err() == nil {
		m.logf("wwdalerts: wake the watcher: %v", err)
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
	return s
}

// wakeIdentity is the episode an alert stands for: its own (host, key, since), or for a silence, the failure markEpisodes decided on.
func (m *Module) wakeIdentity(host string, a wire.Alert) (ep wakeEp, about string, shared bool) {
	if sharedLedger(a) {
		return wakeEp{silenceLedger, a.Key, m.episode[a.Key]}, strings.TrimPrefix(a.Key, "silence:"), true
	}
	return wakeEp{host, a.Key, a.Since}, host, false
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
	for _, a := range alerts {
		if a.ClearedAt != nil {
			continue
		}
		ep, about, shared := m.wakeIdentity(host, a)
		var adopt int64
		if shared && !m.wakeSeen[a.Key] {
			adopt = a.Since.UnixMilli() // the first look at this key since the daemon started: the ledger may know the failure
		}
		v, err := m.store.claimWake(ep, now, wakeThrottle, adopt)
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
		if v == wakeNow {
			m.enqueueWake(wakeLine(about, a))
		}
	}
}
