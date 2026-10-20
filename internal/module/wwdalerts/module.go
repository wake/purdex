// Package wwdalerts delivers the wwd host alerts (spec §13.3): it reads every host's active alerts through the local wwd, sends the
// `push` ones through the push module (APNs), and gathers the `digest` ones for the daily digest.
//
// The deliverer is the daemon holding the fleet `primary` role, and only while its push module is ready; a `standby` or `off` daemon
// reads and gathers, and tells nothing (a standby tells only when the primary cannot push). Status reports a deliverer only while this
// daemon's own push module is ready, so /api/info names who really sends. Delivery is at least once. "Delivered" is recorded only when the push module says APNs took the
// push (AlertPush.Sent), never when the push was merely queued: the sender's queue is not drained when the daemon stops (#3432), and a
// person at the computer or a phone-less daemon gets no push at all. So an alert that holds is put in the push module's hands at most
// once per attemptEvery, and again after deliveredFor from its last confirmed delivery. The ledger is in this module's own database, so
// a restart neither forgets a delivery nor repeats an attempt that is only minutes old.
package wwdalerts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/alertsettings"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/fleet"
	fleetmod "github.com/wake/purdex/internal/module/fleet"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	// pollEvery is how often every host's alert list is read. An alert is not latency critical, and a poll needs no reconnect loop; the
	// `alert` event may shorten it later.
	pollEvery = 30 * time.Second
	// attemptEvery is the least time between two attempts to push the same alert that has not been confirmed delivered.
	attemptEvery = 5 * time.Minute
	// deliveredFor is how long a confirmed delivery stands for the alert: not pushed again within it unless it cleared first.
	deliveredFor = time.Hour
)

// source is the part of the wwd client the module reads; *client.Client is one.
type source interface {
	Info(ctx context.Context) (wire.Info, error)
	Pool(ctx context.Context) (wire.Pool, error)
	Alerts(ctx context.Context, host string) (wire.AlertList, bool, error)
}

// Module is the alert deliverer.
type Module struct {
	logf    func(format string, args ...any)
	now     func() time.Time
	dbPath  string // empty: wwdalerts.db in the daemon's data directory
	src     source
	store   *store
	core    *core.Core
	notify  func() (push.AlertNotifier, bool) // the push module's notifier, asked at each pass (it registers when it starts)
	deliver func() bool                       // whether this daemon is the deliverer now
	peer    func() fleet.PeerPush             // what the other fleet daemon last said about its push; nil: nothing known
	pushOn  func() (bool, error)              // the host setting wwd_alerts.push; an error is read as off

	wakeRule func() bool                // whether this daemon wakes the load watcher now (wakeRule); nil: never
	waker    func() (watch.Waker, bool) // the watch module's service, asked when a wake is made; false: not there
	wakeQ    wakeQueue                  // what waits for the one wake worker
	wakeSeen map[string]bool            // shared silence keys looked at since the daemon started (pass goroutine only)

	cancel context.CancelFunc
	wg     sync.WaitGroup

	smu            sync.Mutex // guards the status below, which /api/info reads from another goroutine
	pushState      string     // "on" or "off" as of the last look at the switch; "" before the first
	pushLogged     string     // the line logged for it last, so a state is logged when it changes and not at every look
	deliverer      bool       // whether this daemon was the deliverer at the last pass
	suppressed     int        // push-level alerts the switch held back in the last pass
	passSuppressed int        // the same, being counted in the pass that is running

	// whether the last pass could read this host's own wwd: 0 no pass yet, 1 it could, 2 it could not (AlertsReadable)
	readState atomic.Int32

	// The fallback of #3610 (spec §13.3): this host's own wwd could be read but the pool could not, and the other fleet daemon has been
	// seen unable to push for fallbackPasses passes running: this daemon then pushes the alerts of its own host (the only list it has).
	// fbStreak is counted on the pass goroutine; fallback is read by pushIfDue (same goroutine) and by Status.
	fbStreak int
	fallback atomic.Bool

	// the silence alerts' episodes (pass runs on one goroutine): which silence: keys each list held when it was last read, and the instance
	// (a time on this daemon's clock) of the failure each key is about now
	lastHeld map[string]map[string]bool // host alias -> its silence: keys
	episode  map[string]time.Time
	// the kind of each silence: key — whether the peer's host itself is gone (the words of wire.SilenceDetailHostDown) or only its wwd — as the
	// lists said this pass, and the words the push uses. A change of kind inside one failure is told again (markEpisodes).
	hostDown map[string]bool
	words    map[string]string
}

// New returns the module with production defaults.
func New() *Module {
	return &Module{logf: log.Printf, now: time.Now}
}

func (m *Module) Name() string { return "wwdalerts" }

// Dependencies: fleet gives the role. The push module is not listed: it is not always there, and its notifier is looked up when used.
func (m *Module) Dependencies() []string { return []string{"fleet"} }

func (m *Module) Init(c *core.Core) error {
	m.core = c
	c.Registry.Register(push.AlertsReadableKey, m) // the fleet role answer folds it into push_ready
	if m.src == nil {
		m.src = client.New(client.DefaultSocket())
	}
	if m.notify == nil {
		m.notify = func() (push.AlertNotifier, bool) {
			svc, ok := c.Registry.Get(push.AlertNotifyKey)
			if !ok {
				return nil, false
			}
			return readyNotifier(svc)
		}
	}
	if m.pushOn == nil {
		m.pushOn = func() (bool, error) {
			svc, ok := c.Registry.Get(alertsettings.Key)
			if !ok {
				return false, errors.New("the hostconfig module is not running")
			}
			r, ok := svc.(alertsettings.Reader)
			if !ok {
				return false, errors.New("the hostconfig module does not serve the setting")
			}
			s, err := r.WwdAlertsSettings()
			return s.Push, err
		}
	}
	if m.waker == nil {
		m.waker = func() (watch.Waker, bool) {
			svc, ok := c.Registry.Get(watch.WakerKey)
			if !ok {
				return nil, false
			}
			w, ok := svc.(watch.Waker)
			return w, ok
		}
	}
	if m.deliver == nil || m.peer == nil || m.wakeRule == nil {
		var rv fleet.RoleView
		if svc, ok := c.Registry.Get(fleetmod.RoleViewKey); ok {
			rv, _ = svc.(fleet.RoleView)
		}
		if m.wakeRule == nil {
			m.wakeRule = wakeRule(rv)
		}
		if m.deliver == nil {
			m.deliver = deliverRule(rv)
		}
		if pv, ok := rv.(fleet.PeerPushView); ok && m.peer == nil {
			m.peer = pv.PeerPush
		}
	}
	path := m.dbPath
	if path == "" {
		path = filepath.Join(c.Cfg.DataDir, "wwdalerts.db")
	}
	st, err := openStore(path)
	if err != nil {
		return err
	}
	m.store = st
	return nil
}

func (m *Module) RegisterRoutes(*http.ServeMux) {}

func (m *Module) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.startWake(ctx)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.loop(ctx)
	}()
	return nil
}

// Stop ends the loop and waits for it as long as ctx allows; Close closes the database after it.
func (m *Module) Stop(ctx context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	ended := make(chan struct{})
	go func() { m.wg.Wait(); close(ended) }()
	select {
	case <-ended:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Module) Close() error {
	if m.store == nil {
		return nil
	}
	ended := make(chan struct{})
	go func() { m.wg.Wait(); close(ended) }()
	select {
	case <-ended:
		return m.store.close()
	case <-time.After(2 * time.Second):
		return fmt.Errorf("wwdalerts: the loop is still running, database left open")
	}
}

// loop is the poll loop.
func (m *Module) loop(ctx context.Context) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		m.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// readyNotifier is the registered service as a notifier that can be asked now. The registry keeps a service after its module stops, so a
// notifier that says it is not ready is not asked: no attempt is spent on it.
func readyNotifier(svc any) (push.AlertNotifier, bool) {
	n, ok := svc.(push.AlertNotifier)
	if !ok {
		return nil, false
	}
	if r, ok := svc.(push.AlertReadiness); ok && !r.AlertReady() {
		return nil, false
	}
	return n, true
}

// deliverRule is who delivers (spec §13.3), before this daemon's own push module is asked (a daemon without one attempts nothing):
//   - the fleet primary delivers;
//   - a standby delivers too when the primary cannot send a phone push — it reported push_ready=false, reported nothing (an older
//     daemon), did not answer, or has not been asked yet. Better both push (the phone shows one notification per collapse id) than
//     none; fleet's election is what turns an unreachable primary into a new one, and that takeover needs no rule here;
//   - an `off` daemon, and one without a role view (no fleet module), reads and does not push.
func deliverRule(rv fleet.RoleView) func() bool {
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
				return false // no way to know what the primary can do: not a reason to push
			}
			p := v.PeerPush()
			return !p.Known || !p.Answered || p.Ready == nil || !*p.Ready
		}
		return false
	}
}

const (
	readYes = 1
	readNo  = 2
)

// AlertsReadable says whether the last pass could read this host's own wwd (push.AlertsReadable): true until the first pass has run. A
// daemon whose wwd is down reads no alert, from any host, so it must not stand as the deliverer; the fleet role answer says so
// through push_ready and the standby takes over.
func (m *Module) AlertsReadable() bool { return m.readState.Load() != readNo }

// fallbackPasses is how many passes in a row the other fleet daemon must be seen unable to push before this one pushes its own host's
// alerts without the pool (#3610): a standby that restarts for a moment does not make the primary take over.
const fallbackPasses = 2

// peerCannotPush says the other fleet daemon has been seen unable to push: it answered that its push is not ready, or it could not be
// reached (a daemon that is not there sends nothing). Nothing known yet, or an answer that carries no push_ready (an older daemon), is not
// a confirmation.
func (m *Module) peerCannotPush() bool {
	if m.peer == nil {
		return false
	}
	p := m.peer()
	return p.Known && (!p.Answered || (p.Ready != nil && !*p.Ready))
}

// updateFallback is called once per pass, before anything is acted on. localOnly: this pass read the list of this host's own wwd and
// nothing else (the pool could not be read). The fallback is on after fallbackPasses such passes in a row with the other daemon seen
// unable to push, and off at the first pass that is not.
func (m *Module) updateFallback(localOnly bool) {
	if !localOnly || !m.peerCannotPush() {
		m.fbStreak = 0
		m.fallback.Store(false)
		return
	}
	m.fbStreak++
	on := m.fbStreak >= fallbackPasses
	if on != m.fallback.Load() {
		if on {
			m.logf("wwdalerts: the pool cannot be read and the other daemon cannot push: pushing this host's own alerts")
		} else {
			m.logf("wwdalerts: the fallback of this host's own alerts ended")
		}
	}
	m.fallback.Store(on)
}

// pass reads every host's active alerts once and acts on them.
func (m *Module) pass(ctx context.Context) {
	// the status: whether this daemon delivers and what the switch says, looked at once per pass even when no alert exists
	m.smu.Lock()
	m.passSuppressed = 0
	m.smu.Unlock()
	m.pushEnabled()
	defer func() {
		// what is reported is who really sends: the rule names this daemon and its own push module can be asked (#3573). The rule itself
		// is left alone: a standby still takes over for a primary whose push it cannot see.
		// and whose wwd it can read: a daemon that cannot read its own wwd hands delivery over (AlertsReadable, #3605), so it is not the sender (#3631)
		deliverer := (m.deliver() && m.AlertsReadable()) || m.fallback.Load()
		if deliverer {
			_, deliverer = m.notify()
		}
		m.smu.Lock()
		m.deliverer, m.suppressed = deliverer, m.passSuppressed
		m.smu.Unlock()
	}()
	hosts, whole, err := m.hosts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.logf("wwdalerts: hosts: %v", err)
			m.readState.Store(readNo)
			m.updateFallback(false)
		}
		return
	}
	// every list is read before any is acted on, so a silence alert is judged on all of them at once
	var lists []gotList
	localOK := false // the list of this host's own wwd was read
	for _, h := range hosts {
		if ctx.Err() != nil {
			return
		}
		list, known, err := m.src.Alerts(ctx, h.ask)
		if h.ask == "" && ctx.Err() == nil && err == nil && known {
			localOK = true
		}
		if h.ask == "" && ctx.Err() == nil { // the list of this host's own wwd; one it does not serve (404) cannot be read either
			if err != nil || !known || !whole { // without the pool the peers' lists cannot be enumerated: most of what there is to deliver
				m.readState.Store(readNo)
			} else {
				m.readState.Store(readYes)
			}
		}
		if err != nil || !known {
			// An unreadable host is not a cleared one: its ledger is kept, so the alerts it still has are not told a second time when it
			// answers again.
			if err != nil && ctx.Err() == nil {
				m.logf("wwdalerts: %s: alerts: %v", h.alias, err)
			}
			continue
		}
		lists = append(lists, gotList{h.alias, list.Alerts})
	}
	m.updateFallback(localOK && !whole)
	present := map[string]bool{} // the hosts that are in the pool now
	for _, h := range hosts {
		present[h.alias] = true
	}
	m.markEpisodes(lists2(lists), present, whole)
	for _, l := range lists {
		m.handle(l.alias, l.alerts)
	}
	if err := m.store.prune(m.now()); err != nil {
		m.logf("wwdalerts: prune: %v", err)
	}
	m.dropGoneSilences()
	if err := m.store.pruneWake(m.now()); err != nil {
		m.logf("wwdalerts: prune wake ledger: %v", err)
	}
}

// markEpisodes decides, from the lists read this pass, which failure each silence:<host> key is about. Every wwd that loses a peer raises
// the same key on its own list, so it is one failure however many noticed it. It goes on while some list that held it before still holds
// it — a list that could not be read this pass keeps what it held, so a blip in reading is not the end. When no list holds it
// continuously (it cleared everywhere, or another wwd noticed afresh after the first one's alert was gone) a new failure begins, told at
// once: a duplicate is preferred to a failure not told (spec §13.3). Only this daemon's own observations and clock are compared, never a
// wwd's Since against this daemon's time.
func (m *Module) markEpisodes(lists map[string][]wire.Alert, inPool map[string]bool, poolWhole bool) {
	first := m.lastHeld == nil
	if first {
		m.lastHeld = map[string]map[string]bool{}
		m.episode = map[string]time.Time{}
		m.hostDown = map[string]bool{}
	}
	m.words = map[string]string{}
	if poolWhole {
		// a host that is not in the pool any more takes what it held with it: coming back it is a new observer, not a continuation
		for h := range m.lastHeld {
			if !inPool[h] {
				delete(m.lastHeld, h)
			}
		}
	}
	now := m.now()
	nowHeld := map[string]map[string]bool{}
	earliest := map[string]time.Time{} // key -> the earliest Since on the lists (a wwd's clock; used only for the first look after a restart)
	// The lists are read in the order of the hosts' names, so which words a push uses does not depend on which list came first; when the
	// wwds disagree (one reaches the peer's SSH port, another does not) the host-down words win.
	hostNames := make([]string, 0, len(lists))
	for host := range lists {
		hostNames = append(hostNames, host)
	}
	sort.Strings(hostNames)
	nowHostDown := map[string]bool{}
	for _, host := range hostNames {
		alerts := lists[host]
		held := map[string]bool{}
		for _, a := range alerts {
			if a.ClearedAt == nil && sharedLedger(a) {
				held[a.Key] = true
				down := wire.SilenceHostDown(a.Detail)
				if _, seen := m.words[a.Key]; !seen || (down && !nowHostDown[a.Key]) {
					m.words[a.Key] = a.Detail
				}
				if down {
					nowHostDown[a.Key] = true
				}
				if t, ok := earliest[a.Key]; !ok || a.Since.Before(t) {
					earliest[a.Key] = a.Since
				}
			}
		}
		nowHeld[host] = held
	}
	// A list that is read and no longer holds what it held is a clear: the failure ended, at least where that wwd saw it. What a list that
	// could not be read held about the same key is then old news, and is dropped, so the failure coming back is told as a new one (a
	// duplicate is preferred to a miss) and not hidden by a hold nobody can check.
	for h, held := range nowHeld {
		for k := range m.lastHeld[h] {
			if held[k] {
				continue
			}
			for u, uheld := range m.lastHeld {
				if _, read := nowHeld[u]; !read {
					delete(uheld, k)
				}
			}
		}
	}
	keys := map[string]bool{}
	for _, held := range nowHeld {
		for k := range held {
			keys[k] = true
		}
	}
	for k := range keys {
		continuing := false
		for h, held := range nowHeld {
			if held[k] && m.lastHeld[h][k] {
				continuing = true
			}
		}
		// a list that could not be read this pass keeps what it held (a blip in reading, or the pool unreadable): it is not a clear
		for h, held := range m.lastHeld {
			if _, read := nowHeld[h]; !read && held[k] {
				continuing = true
			}
		}
		// The kind of the silence (the peer's wwd only, or its host) is part of what was told: the same failure that changes kind is told
		// again, in the new words. A restart of this daemon forgets the kind it last told (the ledger holds the failure, not its words).
		prev, had := m.hostDown[k]
		if !had { // the first look at it since this daemon started: the ledger knows the kind that was told
			prev, had = m.store.toldKind(silenceLedger, k)
		}
		kindChanged := had && prev != nowHostDown[k]
		m.hostDown[k] = nowHostDown[k]
		if continuing {
			if kindChanged {
				m.episode[k] = now
			}
			continue
		}
		// The first look after a start has no history of its own. The ledger knows the failure it last told: the same one while a list's
		// alert was raised no later than that push (a wwd's clock against this one's, used only here); one raised after it began while
		// this daemon was down, and is told.
		_, known := m.episode[k]
		if row, had := m.store.delivery(silenceLedger, k); had && !known && !earliest[k].After(time.UnixMilli(row.Attempt)) {
			m.episode[k] = time.UnixMilli(row.Since)
		} else {
			m.episode[k] = now
		}
		if kindChanged { // the failure goes on, as another kind (the ledger's kind, after a restart, counts as what was told)
			m.episode[k] = now
		}
	}
	// a key no list holds has cleared; its kind is compared with the ledger's (what was last told) if it comes back
	for k := range m.hostDown {
		if !keys[k] {
			delete(m.hostDown, k)
		}
	}
	for h, held := range nowHeld {
		m.lastHeld[h] = held
	}
}

func lists2(lists []gotList) map[string][]wire.Alert {
	out := make(map[string][]wire.Alert, len(lists))
	for _, l := range lists {
		out[l.alias] = l.alerts
	}
	return out
}

// gotList is one host's active alerts as read this pass.
type gotList struct {
	alias  string
	alerts []wire.Alert
}

type hostRef struct{ alias, ask string }

// hosts lists the local host (asked with "") and every peer of its pool. whole is false when the pool could not be read, so the list is
// only the local host.
func (m *Module) hosts(ctx context.Context) (out []hostRef, whole bool, err error) {
	info, err := m.src.Info(ctx)
	if err != nil {
		return nil, false, err
	}
	out = []hostRef{{alias: info.Alias, ask: ""}}
	pool, err := m.src.Pool(ctx)
	if err != nil {
		return out, false, nil // the pool unreadable: the local host's alerts are still read
	}
	for _, p := range pool.Peers {
		if p.Info.Alias != "" && p.Info.Alias != info.Alias {
			out = append(out, hostRef{alias: p.Info.Alias, ask: p.Info.Alias})
		}
	}
	return out, true, nil
}

// handle acts on one host's active alerts: gathers the digest ones, pushes the push ones that are due, and forgets the ledger rows of
// alerts that are gone.
func (m *Module) handle(host string, alerts []wire.Alert) {
	now := m.now()
	m.wakeAlerts(host, alerts, now) // every level wakes the watcher, whether or not it is pushed
	keys := make([]string, 0, len(alerts))
	for _, a := range alerts {
		if a.ClearedAt != nil {
			continue
		}
		keys = append(keys, a.Key)
		switch a.Level {
		case wire.AlertDigest:
			if err := m.store.noteDigest(host, a, now); err != nil {
				m.logf("wwdalerts: digest %s/%s: %v", host, a.Key, err)
			}
		case wire.AlertPush:
			m.pushIfDue(host, a, now)
		case wire.AlertWatch:
			// for the watcher alone: neither pushed nor gathered here (the wake of the watcher is its own path)
		default:
			// a level of a newer wwd: told to nobody, rather than guessed to be a push
		}
	}
	if err := m.store.forgetExcept(host, keys); err != nil {
		m.logf("wwdalerts: ledger %s: %v", host, err)
	}
}

// pushIfDue puts a push-level alert in the push module's hands when this daemon is the deliverer and the alert is due.
func (m *Module) pushIfDue(host string, a wire.Alert, now time.Time) {
	// A daemon that says its push is not ready (AlertsReadable) has handed delivery to the standby: pushing the lists it did read as well
	// would send the same shared alert from both.
	// Without the pool the only list there is is this host's own, and the fallback (updateFallback) decides whether it is pushed: the other
	// daemon cannot push either, so nobody else would tell it (#3610).
	if m.AlertsReadable() {
		if !m.deliver() {
			return
		}
	} else if !m.fallback.Load() {
		return
	}
	// The switch is read at every push (hostconfig keeps it cached), so turning it off stops the next push, not the next restart. Off
	// touches nothing: no attempt, no ledger row, nothing recorded as sent — the alerts that still hold are pushed when it is turned on.
	if !m.pushEnabled() {
		m.smu.Lock()
		m.passSuppressed++
		m.smu.Unlock()
		return
	}
	ntf, ok := m.notify()
	if !ok {
		return // the push side is not up: no attempt is spent
	}
	// a silence alert ("silence:mserv") is the same failure on every wwd's list that noticed it: one ledger row and one collapse id for the
	// failure, not one per observer (spec §13.3: a duplicate only during a failover or a partition, and those are separate deliverers);
	// which failure it is, markEpisodes decided
	about := host
	since := a.Since
	if sharedLedger(a) {
		about, host = strings.TrimPrefix(a.Key, "silence:"), silenceLedger
		since = m.episode[a.Key]
		if w := m.words[a.Key]; w != "" {
			a.Detail = w // the words of the failure as the lists agree on them, not of whichever list this alert came from
		}
	}
	prev, hadRow := m.store.delivery(host, a.Key)
	if hadRow && prev.Since == since.UnixMilli() {
		if prev.Sent > 0 && within(now, prev.Sent, deliveredFor) {
			return
		}
		if prev.Attempt > 0 && within(now, prev.Attempt, attemptEvery) {
			return
		}
	}
	// the attempt is written before the push module is asked, so a restart in the middle cannot ask again at once
	if err := m.store.beginAttempt(host, a.Key, since, now); err != nil {
		m.logf("wwdalerts: ledger %s/%s: %v", host, a.Key, err)
		return
	}
	key := a.Key
	// the kind of a silence that is being told (read now: the next pass may change it); it is kept in the ledger only once the push is taken,
	// so a push taken back, or never queued, leaves the kind that was told before
	shared, hostDown := sharedLedger(a), m.hostDown[a.Key]
	queued := ntf.NotifyAlert(push.AlertPush{
		CollapseID: collapseID(host, a.Key),
		Title:      title(about, a),
		Host:       about,
		// the switch is asked again just before the push goes out: turning it off also takes back what still waits in the queue
		StillWanted: m.pushEnabled,
		// nothing was sent: the attempt written above goes back, so turning the switch on again is not held back for the interval
		Recalled: func() {
			if err := m.store.restoreDelivery(host, key, prev, hadRow, since, now); err != nil {
				m.logf("wwdalerts: ledger %s/%s: %v", host, key, err)
			}
		},
		Sent: func() {
			if err := m.store.markSent(host, key, since, m.now()); err != nil {
				m.logf("wwdalerts: record delivery %s/%s: %v", host, key, err)
			}
			if shared {
				if err := m.store.setKind(host, key, since, hostDown); err != nil {
					m.logf("wwdalerts: record kind %s/%s: %v", host, key, err)
				}
			}
		},
	})
	// The push module can stop between the readiness look and the call: it then says false, nothing was queued, and the attempt written
	// above must not hold the alert back for the interval. A false from a module that is still up (its queue is full) keeps the attempt.
	if !queued {
		if r, ok := ntf.(push.AlertReadiness); ok && !r.AlertReady() {
			if err := m.store.restoreDelivery(host, a.Key, prev, hadRow, since, now); err != nil {
				m.logf("wwdalerts: ledger %s/%s: %v", host, a.Key, err)
			}
		}
	}
}

// within reports whether the stored time at (unix ms) is less than window before now. A time in the future (the clock stepped back after
// it was written, and the ledger survives restarts) is not within: it is spent, so a stepped-back clock cannot stretch a window.
func within(now time.Time, at int64, window time.Duration) bool {
	d := now.Sub(time.UnixMilli(at))
	return d >= 0 && d < window
}

// silenceLedger stands in for the host in the ledger and the collapse id of the silence alerts about a wwd (key silence:<alias>), which
// every wwd that lost the peer raises on its own list.
const silenceLedger = "(silence)"

// sharedLedger says whether the alert is one of those.
func sharedLedger(a wire.Alert) bool {
	return a.Kind == wire.AlertSilence && strings.HasPrefix(a.Key, "silence:")
}

// collapseID is the id a phone uses to show one notification per alert: alert:<host>:<key>. A host name may hold a colon, and then
// two different alerts could read the same joined, so that form is used only when the host has none and is otherwise a hash of the
// host and key with the host's length in front.
func collapseID(host, key string) string {
	if !strings.Contains(host, ":") {
		return "alert:" + host + ":" + key
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", len(host), host, key)))
	return "alert:" + hex.EncodeToString(sum[:16])
}

// pushEnabled reads the switch wwd_alerts.push. Off, unreadable and "no hostconfig here" are all off. The state is remembered for Status,
// and logged when it changes (an unreadable switch: when the reason changes), not at every look.
func (m *Module) pushEnabled() bool {
	on, err := false, error(nil)
	if m.pushOn != nil {
		on, err = m.pushOn()
	} else {
		err = errors.New("the setting is not wired")
	}
	var line string
	switch {
	case err != nil:
		on, line = false, "wwdalerts: push is off, the wwd_alerts.push setting cannot be read: "+err.Error()
	case on:
		line = "wwdalerts: push is on (wwd_alerts.push)"
	default:
		line = "wwdalerts: push is off (wwd_alerts.push is false): alerts are written and gathered, no new push is sent to the phone (one already on its way to APNs cannot be recalled)"
	}
	m.smu.Lock()
	m.pushState = map[bool]string{true: "on", false: "off"}[on]
	changed := m.pushLogged != line
	m.pushLogged = line
	m.smu.Unlock()
	if changed {
		m.logf("%s", line)
	}
	return on
}

// Status is what /api/info reports for the module: whether pushes to the phone are on, whether this daemon is the deliverer, and how
// many push-level alerts the switch held back in the last pass.
func (m *Module) Status() map[string]any {
	m.smu.Lock()
	defer m.smu.Unlock()
	state := m.pushState
	if state == "" {
		state = "off" // not looked at yet: off is what it is until a pass says otherwise
	}
	return map[string]any{"push": state, "deliverer": m.deliverer, "suppressed": m.suppressed}
}

// title is the line the person reads: the alert's own text, which names the host, or the host and the rule when it has none.
func title(host string, a wire.Alert) string {
	if a.Detail != "" {
		return "[wwd] " + a.Detail
	}
	return fmt.Sprintf("[wwd] %s %s", host, a.Kind)
}
