package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// UnattendedRoute is the U23 switch's route (GET / PUT): on the general
// chain, admin token only (D-U23-2); exported for cmd/pdx's chain test.
// No pdx command and no mod call names it (cmd/pdx's guard test): the
// App is its one caller. That is told, not enforced — the App and pdx
// share one host token (plan deviation 6).
const UnattendedRoute = "/api/team/unattended"

// handleUnattendedGet is GET /api/team/unattended?before=<ms>&limit=<n>:
// the switch and one page of what the daemon approved since its last
// switch-on, newest first (D-U23-6, decision 17).
func (m *Module) handleUnattendedGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, okB := positiveParam(q.Get("before"), q.Has("before"), 0)
	limit, okL := positiveParam(q.Get("limit"), q.Has("limit"), team.UnattendedPageDefault)
	if !okB || !okL {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "before and limit must be positive integers", nil)
		return
	}
	st, err := m.unattended.Unattended()
	if err != nil {
		m.logf("[team] unattended GET: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "the unattended switch could not be read; see the daemon log", nil)
		return
	}
	v := team.UnattendedView{UnattendedState: st}
	if err := m.fillUnattendedPage(&v, before, int(min(limit, team.UnattendedPageMax))); err != nil {
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.fillQuotas(&v)
	m.writeJSON(w, http.StatusOK, v)
}

// positiveParam reads a query value that, when present, must be a
// positive integer; absent → def.
func positiveParam(s string, present bool, def int64) (int64, bool) {
	if !present {
		return def, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// fillUnattendedPage fills v's page from team.db (before 0 = the newest).
// A page that cannot be read is logged and returned as the error, v's
// page left empty.
func (m *Module) fillUnattendedPage(v *team.UnattendedView, before int64, limit int) error {
	rows, truncated, err := m.store.ListAutoApproved(v.Since, before, limit)
	if err != nil {
		m.logf("[team] unattended list: %v", err)
		return err
	}
	v.Approved, v.Truncated = rows, truncated
	if truncated {
		v.NextBefore = rows[len(rows)-1].DecidedAt
	}
	return nil
}

// handleUnattendedPut is PUT /api/team/unattended {on, client}: the App
// turns the switch on or off (D-U23-1). The write, the switch-on sweep
// (D-U23-3) and the changed event share one createMu section, so no create
// commits open after the sweep and the events keep the writes' order. A
// row the sweep could not approve is pending in the answer; the sweeper's
// next tick approves it (decision 5). Off changes nothing else: open
// requests stay open, the list stays until the next switch-on.
func (m *Module) handleUnattendedPut(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.UnattendedPutRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	switch {
	case req.On == nil:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "on must be true or false", nil)
		return
	case strings.TrimSpace(req.Client.Kind) != "app" || strings.TrimSpace(req.Client.Label) == "":
		// The switch is the App's (D-U23-2, decision 6): the kind is the
		// caller's own claim, so this tells, not enforces.
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `client must be {"kind":"app","label":…}`, nil)
		return
	}
	client := team.Client{Kind: "app", Label: req.Client.Label, Addr: r.RemoteAddr}

	m.createMu.Lock()
	if m.stopping() {
		m.createMu.Unlock()
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	st, changed, err := m.unattended.SetUnattended(*req.On, client, m.now())
	var swept, pending int
	if err == nil && changed {
		if st.On {
			swept, pending = m.sweepUnattended("switch on")
		}
		m.broadcastUnattended("changed", st)
	}
	m.createMu.Unlock()

	if err != nil {
		m.logf("[team] unattended PUT: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "the unattended switch could not be written; see the daemon log", nil)
		return
	}
	if changed {
		m.logf("[team] unattended %s by app %q from %s (swept %d, pending %d)", onOff(st.On), client.Label, client.Addr, swept, pending)
	}
	// The write took effect: a list that cannot be read must not turn that
	// into a 500 the App could not tell from a failed write. It answers
	// 200 with approved [] and list_failed, and GETs the list.
	v := team.UnattendedView{UnattendedState: st, Swept: swept, Pending: pending}
	if m.fillUnattendedPage(&v, 0, team.UnattendedPageDefault) != nil {
		v.ListFailed = true
	}
	m.fillQuotas(&v)
	m.writeJSON(w, http.StatusOK, v)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// unattendedEvent is the HostEvent of {op, state}.
func unattendedEvent(op string, st team.UnattendedState) (core.HostEvent, error) {
	v, err := json.Marshal(team.UnattendedEventValue{Op: op, State: st})
	if err != nil {
		return core.HostEvent{}, fmt.Errorf("encode %s %s event: %w", team.UnattendedEventType, op, err)
	}
	return core.HostEvent{Type: team.UnattendedEventType, Value: string(v)}, nil
}

// broadcastUnattended queues {op, state} to every subscriber, under
// eventMu like broadcast, so it never lands between a snapshot's read and
// its send. It is strict for every subscriber (BroadcastStrict): one that
// cannot take it is closed and reconnects for the snapshot, since a
// dropped changed would leave its window showing the wrong switch with
// nothing to correct it (D-U23-6). A switch changes rarely, so the cost is
// a reconnect for a client that was already behind.
func (m *Module) broadcastUnattended(op string, st team.UnattendedState) {
	ev, err := unattendedEvent(op, st)
	if err != nil {
		m.logf("[team] %v", err)
		return
	}
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	m.core.Events.BroadcastStrict(ev)
}

// sendUnattendedSnapshot queues {op:"snapshot", state} to a new subscriber
// (D-U23-6: every window shows the switch), read and sent under eventMu as
// sendSnapshot does. A switch that cannot be read sends nothing and keeps
// the subscriber: reconnecting would not fix a stored value, and the
// approval stream on the same connection must not suffer for it; the
// App's GET gets the 500. A full buffer closes it so it reconnects.
func (m *Module) sendUnattendedSnapshot(sub *core.EventSubscriber) {
	m.eventMu.Lock()
	st, err := m.unattended.Unattended()
	var ev core.HostEvent
	if err == nil {
		ev, err = unattendedEvent("snapshot", st)
	}
	var data []byte
	if err == nil {
		data, err = json.Marshal(ev)
	}
	sent := err == nil && sub.TrySend(data)
	m.eventMu.Unlock()
	switch {
	case err != nil:
		m.logf("[team] unattended snapshot not sent: %v", err)
	case !sent:
		select {
		case <-sub.Done():
		default:
			m.logf("[team] unattended snapshot could not be queued (send buffer full); closing the connection so the client reconnects")
			m.core.Events.Remove(sub)
		}
	}
}
