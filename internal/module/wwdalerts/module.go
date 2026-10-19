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
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/alertsettings"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/fleet"
	fleetmod "github.com/wake/purdex/internal/module/fleet"
	"github.com/wake/purdex/internal/push"
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
	pushOn  func() (bool, error)              // the host setting wwd_alerts.push; an error is read as off

	cancel context.CancelFunc
	wg     sync.WaitGroup

	smu            sync.Mutex // guards the status below, which /api/info reads from another goroutine
	pushState      string     // "on" or "off" as of the last look at the switch; "" before the first
	pushLogged     string     // the line logged for it last, so a state is logged when it changes and not at every look
	deliverer      bool       // whether this daemon was the deliverer at the last pass
	suppressed     int        // push-level alerts the switch held back in the last pass
	passSuppressed int        // the same, being counted in the pass that is running
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
	if m.deliver == nil {
		var rv fleet.RoleView
		if svc, ok := c.Registry.Get(fleetmod.RoleViewKey); ok {
			rv, _ = svc.(fleet.RoleView)
		}
		m.deliver = deliverRule(rv)
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
		deliverer := m.deliver()
		if deliverer {
			_, deliverer = m.notify()
		}
		m.smu.Lock()
		m.deliverer, m.suppressed = deliverer, m.passSuppressed
		m.smu.Unlock()
	}()
	hosts, err := m.hosts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.logf("wwdalerts: hosts: %v", err)
		}
		return
	}
	allRead := true
	silent := map[string]bool{} // the silence:<host> keys active on any list read this pass
	for _, h := range hosts {
		if ctx.Err() != nil {
			return
		}
		list, known, err := m.src.Alerts(ctx, h.ask)
		if err != nil || !known {
			allRead = false
			// An unreadable host is not a cleared one: its ledger is kept, so the alerts it still has are not told a second time when it
			// answers again.
			if err != nil && ctx.Err() == nil {
				m.logf("wwdalerts: %s: alerts: %v", h.alias, err)
			}
			continue
		}
		m.handle(h.alias, list.Alerts)
		for _, a := range list.Alerts {
			if a.ClearedAt == nil && sharedLedger(a) {
				silent[a.Key] = true
			}
		}
	}
	// a silence alert is one failure however many wwds noticed it: its ledger row goes when no list holds it any more — judged only when
	// every list was read, since one that could not be read may still hold it
	if allRead {
		keys := make([]string, 0, len(silent))
		for k := range silent {
			keys = append(keys, k)
		}
		if err := m.store.forgetExcept(silenceLedger, keys); err != nil {
			m.logf("wwdalerts: ledger %s: %v", silenceLedger, err)
		}
	}
	if err := m.store.prune(m.now()); err != nil {
		m.logf("wwdalerts: prune: %v", err)
	}
}

type hostRef struct{ alias, ask string }

// hosts lists the local host (asked with "") and every peer of its pool.
func (m *Module) hosts(ctx context.Context) ([]hostRef, error) {
	info, err := m.src.Info(ctx)
	if err != nil {
		return nil, err
	}
	out := []hostRef{{alias: info.Alias, ask: ""}}
	pool, err := m.src.Pool(ctx)
	if err != nil {
		return out, nil // the pool unreadable: the local host's alerts are still read
	}
	for _, p := range pool.Peers {
		if p.Info.Alias != "" && p.Info.Alias != info.Alias {
			out = append(out, hostRef{alias: p.Info.Alias, ask: p.Info.Alias})
		}
	}
	return out, nil
}

// handle acts on one host's active alerts: gathers the digest ones, pushes the push ones that are due, and forgets the ledger rows of
// alerts that are gone.
func (m *Module) handle(host string, alerts []wire.Alert) {
	now := m.now()
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
		}
	}
	if err := m.store.forgetExcept(host, keys); err != nil {
		m.logf("wwdalerts: ledger %s: %v", host, err)
	}
}

// pushIfDue puts a push-level alert in the push module's hands when this daemon is the deliverer and the alert is due.
func (m *Module) pushIfDue(host string, a wire.Alert, now time.Time) {
	if !m.deliver() {
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
	// failure, not one per observer (spec §13.3: a duplicate only during a failover or a partition, and those are separate deliverers)
	about := host
	if sharedLedger(a) {
		about, host = strings.TrimPrefix(a.Key, "silence:"), silenceLedger
	}
	prev, hadRow := m.store.delivery(host, a.Key)
	// the observers raise it at slightly different times, so for the shared row the row's existence is the episode, not its start time
	if hadRow && (prev.Since == a.Since.UnixMilli() || host == silenceLedger) {
		if prev.Sent > 0 && within(now, prev.Sent, deliveredFor) {
			return
		}
		if prev.Attempt > 0 && within(now, prev.Attempt, attemptEvery) {
			return
		}
	}
	// the attempt is written before the push module is asked, so a restart in the middle cannot ask again at once
	if err := m.store.beginAttempt(host, a.Key, a.Since, now); err != nil {
		m.logf("wwdalerts: ledger %s/%s: %v", host, a.Key, err)
		return
	}
	since := a.Since
	key := a.Key
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
		},
	})
	// The push module can stop between the readiness look and the call: it then says false, nothing was queued, and the attempt written
	// above must not hold the alert back for the interval. A false from a module that is still up (its queue is full) keeps the attempt.
	if !queued {
		if r, ok := ntf.(push.AlertReadiness); ok && !r.AlertReady() {
			if err := m.store.restoreDelivery(host, a.Key, prev, hadRow, a.Since, now); err != nil {
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
