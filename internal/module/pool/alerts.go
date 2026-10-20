package poolmod

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// EventAlertsChanged is the host event the App hears when a host's alert list changed (wwd spec §13.3): {host}. It is a notice: the App
// re-reads GET /api/pool/hosts/{alias}/alerts. A daemon that sends it announces pool.alerts.v1, and the App then stops polling the alerts.
const EventAlertsChanged = "alerts.changed"

// defaultAlertsEvery is how often every host's alert list is read. wwd's `alert` event reaches this daemon only for the local host (spec
// §13.1: the peers' TCP streams do not carry it), so another host's alerts are as new as the last of these reads.
const defaultAlertsEvery = 30 * time.Second

// PoolAlert is one alert as the App reads it: wwd's Alert, and for a silence which one it is (wire.SilenceKind), which wwd's own answer
// does not say. Purdex's own type: wire.Alert and what wwd answers are unchanged.
type PoolAlert struct {
	wire.Alert
	Silence string `json:"silence,omitempty"`
}

// PoolHostAlerts is GET /api/pool/hosts/{alias}/alerts. Known is false when that host's wwd predates the route (no alerts yet).
type PoolHostAlerts struct {
	Host   string      `json:"host"`
	Known  bool        `json:"known"`
	Alerts []PoolAlert `json:"alerts"`
}

// poolAlertsOf is the App's view of a host's alerts: each alert with its silence kind, never null.
func poolAlertsOf(in []wire.Alert) []PoolAlert {
	out := make([]PoolAlert, 0, len(in))
	for _, a := range in {
		out = append(out, PoolAlert{Alert: a, Silence: wire.SilenceKind(a)})
	}
	return out
}

// handleAlerts is a host's alerts, asked of the local wwd with that host (spec §13.3). A host whose wwd predates the route is "not known"
// (known: false), not an error; the errors are the history read's.
func (m *Module) handleAlerts(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	if !validAlias(alias) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the host alias is empty, a dot name (. or ..), too long or holds a control character")
		return
	}
	m.serve(w, r, "alerts|"+alias, func(ctx context.Context) (any, *apiError) {
		list, known, err := m.client().Alerts(ctx, alias)
		if err != nil {
			return nil, historyError(err)
		}
		return PoolHostAlerts{Host: alias, Known: known, Alerts: poolAlertsOf(list.Alerts)}, nil
	})
}

// alertsPrint is what a host's alerts look like to the watch: what the App would see changed. `last` moves at every evaluation of a rule that
// still holds, so it is left out: a change of it is not news, as the time wwd last looked at a peer is not for pool.changed.
func alertsPrint(list []wire.Alert) string {
	type row struct {
		Key       string
		Kind      wire.AlertKind
		Level     wire.AlertLevel
		Since     time.Time
		ClearedAt *time.Time
		Detail    string
	}
	rows := make([]row, 0, len(list))
	for _, a := range list {
		rows = append(rows, row{a.Key, a.Kind, a.Level, a.Since, a.ClearedAt, a.Detail})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return ""
	}
	return string(b)
}

// setHosts remembers the aliases of the local wwd's pool, this host first (wwd lists it first), for the alerts watch to read.
func (m *Module) setHosts(p wire.Pool) {
	hosts := make([]string, 0, len(p.Peers))
	for _, peer := range p.Peers {
		hosts = append(hosts, peer.Info.Alias)
	}
	m.mu.Lock()
	m.hosts = hosts
	m.mu.Unlock()
}

func (m *Module) poolHosts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.hosts...)
}

// watchAlerts reads every host's alert list every m.alertsEvery until ctx ends and announces alerts.changed for a host whose list differs
// from the last look. A host's first look is only its baseline. The local host's alert event (kick) shortens its own wait: only that host
// is read then. When the local wwd cannot be read every host looked at before is announced once (the answer of the route is an error now),
// and the first read after it is back announces every one of them that is there: the App stopped polling when it saw pool.alerts.v1 and its
// connection to this daemon did not drop with wwd's. A peer that cannot be read keeps its last look and is not announced (the local
// wwd's own silence alert says it); wwd being absent is logged when it starts and when it changes, not at every read.
func (m *Module) watchAlerts(ctx context.Context) {
	known := map[string]string{} // host alias -> alertsPrint when last read
	away := false
	last := ""
	localOnly := false
	for {
		res, err := m.alertsPass(ctx, known, away, localOnly)
		switch res {
		case passOK:
			away, last = false, ""
		case passAway:
			if !away {
				away = true
				for alias := range known {
					m.notifyKind(EventAlertsChanged, alias)
				}
			}
			if s := err.Error(); s != last {
				last = s
				m.logf("pool: alerts: reading the local wwd: %v", err)
			}
		case passCanceled:
			return
		case passNoPool: // the pool watch has not read yet: look again soon, so that the baseline is not a whole turn away
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.pollEvery):
			}
			continue
		}
		localOnly = false
		select {
		case <-ctx.Done():
			return
		case <-m.alertKick:
			localOnly = true
		case <-time.After(m.alertsEvery):
		}
	}
}

type passResult int

const (
	passOK passResult = iota
	passAway
	passCanceled
	passNoPool // the pool has not been read yet: there is no one to ask about
)

// alertsPass reads the hosts' alerts once (only the local host's when localOnly) and announces what changed. recovered is true for the
// first pass after the local wwd was away: every host that was known and is there now is announced.
func (m *Module) alertsPass(ctx context.Context, known map[string]string, recovered, localOnly bool) (passResult, error) {
	hosts := m.poolHosts()
	if len(hosts) == 0 {
		return passNoPool, nil
	}
	now := map[string]string{}
	for i, alias := range hosts {
		if localOnly && i > 0 {
			break
		}
		ask := alias
		if i == 0 {
			ask = "" // this host itself: the local wwd answers for it
		}
		list, _, err := m.client().Alerts(ctx, ask)
		if err != nil {
			if ctx.Err() != nil {
				return passCanceled, nil
			}
			if i == 0 {
				return passAway, err
			}
			continue // a peer that could not be read: its last look stands
		}
		now[alias] = alertsPrint(list.Alerts)
	}
	for alias, fp := range now {
		if old, had := known[alias]; had && (recovered || old != fp) {
			m.notifyKind(EventAlertsChanged, alias)
		}
		known[alias] = fp
	}
	if !localOnly {
		still := map[string]bool{}
		for _, a := range hosts {
			still[a] = true
		}
		for alias := range known {
			if !still[alias] { // left the pool
				delete(known, alias)
				m.notifyKind(EventAlertsChanged, alias)
			}
		}
	}
	return passOK, nil
}

// watchAlertEvents keeps the local wwd's event stream open until ctx ends and turns its `alert` events into a kick of watchAlerts, which
// reads the local host's list at once instead of at its next turn. The stream does not replay: a change while it was down is found by the
// next regular read. wwd being absent is retried every m.pollEvery, quietly.
func (m *Module) watchAlertEvents(ctx context.Context) {
	for ctx.Err() == nil {
		_ = m.client().Watch(ctx, nil, func(ev wire.Event) bool {
			if ev.Type == wire.EventAlert {
				select {
				case m.alertKick <- struct{}{}:
				default: // a kick is already waiting: it covers this one
				}
			}
			return false
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.pollEvery):
		}
	}
}
