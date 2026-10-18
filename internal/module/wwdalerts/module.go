// Package wwdalerts delivers the wwd host alerts (spec §13.3): it reads every host's active alerts through the local wwd, sends the
// `push` ones through the push module (APNs), and gathers the `digest` ones for the daily digest.
//
// The deliverer is the daemon holding the fleet `primary` role, and only while its push module is ready; a `standby` or `off` daemon
// reads and gathers, and tells nothing. Delivery is at least once. "Delivered" is recorded only when the push module says APNs took the
// push (AlertPush.Sent), never when the push was merely queued: the sender's queue is not drained when the daemon stops (#3432), and a
// person at the computer or a phone-less daemon gets no push at all. So an alert that holds is put in the push module's hands at most
// once per attemptEvery, and again after deliveredFor from its last confirmed delivery. The ledger is in this module's own database, so
// a restart neither forgets a delivery nor repeats an attempt that is only minutes old.
package wwdalerts

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

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

	cancel context.CancelFunc
	wg     sync.WaitGroup
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
			n, ok := svc.(push.AlertNotifier)
			return n, ok
		}
	}
	if m.deliver == nil {
		var rv fleet.RoleView
		if svc, ok := c.Registry.Get(fleetmod.RoleViewKey); ok {
			rv, _ = svc.(fleet.RoleView)
		}
		m.deliver = primaryOnly(rv)
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

// primaryOnly is the deliverer rule: this daemon holds the fleet primary role. Without a role view (no fleet module) nobody delivers.
func primaryOnly(rv fleet.RoleView) func() bool {
	return func() bool { return rv != nil && rv.State().Role == fleet.RolePrimary }
}

// pass reads every host's active alerts once and acts on them.
func (m *Module) pass(ctx context.Context) {
	hosts, err := m.hosts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.logf("wwdalerts: hosts: %v", err)
		}
		return
	}
	for _, h := range hosts {
		if ctx.Err() != nil {
			return
		}
		list, known, err := m.src.Alerts(ctx, h.ask)
		if err != nil || !known {
			// An unreadable host is not a cleared one: its ledger is kept, so the alerts it still has are not told a second time when it
			// answers again.
			if err != nil && ctx.Err() == nil {
				m.logf("wwdalerts: %s: alerts: %v", h.alias, err)
			}
			continue
		}
		m.handle(h.alias, list.Alerts)
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
	ntf, ok := m.notify()
	if !ok {
		return // the push side is not up: no attempt is spent
	}
	if d, ok := m.store.delivery(host, a.Key); ok && d.Since == a.Since.UnixMilli() {
		if d.Sent > 0 && now.Sub(time.UnixMilli(d.Sent)) < deliveredFor {
			return
		}
		if d.Attempt > 0 && now.Sub(time.UnixMilli(d.Attempt)) < attemptEvery {
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
	ntf.NotifyAlert(push.AlertPush{
		CollapseID: "alert:" + host + ":" + a.Key,
		Title:      title(host, a),
		Host:       host,
		Sent: func() {
			if err := m.store.markSent(host, key, since, m.now()); err != nil {
				m.logf("wwdalerts: record delivery %s/%s: %v", host, key, err)
			}
		},
	})
}

// title is the line the person reads: the alert's own text, which names the host, or the host and the rule when it has none.
func title(host string, a wire.Alert) string {
	if a.Detail != "" {
		return "[wwd] " + a.Detail
	}
	return fmt.Sprintf("[wwd] %s %s", host, a.Kind)
}
