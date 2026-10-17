package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/watch"
)

// The events of this host and their handled state (host-load-control spec §12.3, §12.6; watcher-area spec §8 A3): GET /api/watch/events
// and /api/watch/unhandled read the record and the ack log; POST /api/watch/ack marks events handled (admin only: a device token is
// refused); watch.events tells the clients, once per recorded event and once per ack operation.

// EventsEventType is the host event the badge and the events list follow: {host_id, unhandled, cause}.
const EventsEventType = "watch.events"

const (
	eventsDefaultLimit = 100
	eventsMaxLimit     = 500
	maxAckIDs          = 500
	maxAckBody         = 64 << 10
	recordKeep         = 30 * 24 * time.Hour // the log's keep: the window every read looks at
)

// EventsPayload is the value of watch.events.
type EventsPayload struct {
	HostID    string `json:"host_id"`
	Unhandled int    `json:"unhandled"`
	Cause     string `json:"cause"` // recorded | acked
}

// The causes of watch.events.
const (
	CauseRecorded = "recorded"
	CauseAcked    = "acked"
)

// records are this host's events of the last 30 days, oldest first.
func (m *Module) records() ([]watch.Record, error) {
	return watch.ReadLog(m.dlv.logDir, m.now().Add(-recordKeep), "")
}

// unhandledCount is how many of this host's events are pending.
func (m *Module) unhandledCount() (int, error) {
	recs, err := m.records()
	if err != nil {
		return 0, err
	}
	return watch.CountUnhandled(recs, m.acks.All()), nil
}

// broadcast sends a host event to the clients (a test replaces emit).
func (m *Module) broadcast(ev core.HostEvent) {
	switch {
	case m.emit != nil:
		m.emit(ev)
	case m.core != nil && m.core.Events != nil:
		m.core.Events.BroadcastEvent(ev)
	}
}

// emitEvents sends watch.events with the count as it is now.
//
// The count and the send are under one lock: a count read before a newer one is sent would be sent after it, and the badge would
// settle on the older number.
func (m *Module) emitEvents(cause string) {
	m.evMu.Lock()
	defer m.evMu.Unlock()
	n, err := m.unhandledCount()
	if err != nil {
		m.logf("[watch] unhandled count for watch.events (tried again shortly): %v", err)
		m.retryEvents(cause)
		return
	}
	m.core.CfgMu.RLock()
	hostID := m.core.Cfg.HostID
	m.core.CfgMu.RUnlock()
	b, _ := json.Marshal(EventsPayload{HostID: hostID, Unhandled: n, Cause: cause})
	m.broadcast(core.HostEvent{Type: EventsEventType, Value: string(b)})
}

// hostName is the host part of this host's id: what the ids of its events start with.
func (m *Module) hostName() string {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	name, _, _ := strings.Cut(m.core.Cfg.HostID, ":")
	return name
}

func (m *Module) handleUnhandled(w http.ResponseWriter, r *http.Request) {
	n, err := m.unhandledCount()
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}
	m.writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

func (m *Module) handleEvents(w http.ResponseWriter, r *http.Request) {
	limit := eventsDefaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > eventsMaxLimit {
			m.writeErr(w, http.StatusBadRequest, "bad_request", "limit must be 1 to 500")
			return
		}
		limit = n
	}
	since := m.now().Add(-recordKeep)
	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			m.writeErr(w, http.StatusBadRequest, "bad_request", "since must be an RFC 3339 time")
			return
		}
		if t.After(since) {
			since = t
		}
	}
	recs, err := watch.ReadLog(m.dlv.logDir, since, "")
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}
	acks := m.acks.All()
	out := make([]watch.EventView, 0, min(limit, len(recs)))
	for i := len(recs) - 1; i >= 0 && len(out) < limit; i-- { // newest first
		var a *watch.Ack
		if x, ok := acks[recs[i].ID]; ok {
			a = &x
		}
		out = append(out, watch.ViewOf(recs[i], a))
	}
	m.writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

// ackBody is the request of POST /api/watch/ack.
type ackBody struct {
	IDs         []string `json:"ids"`
	All         bool     `json:"all"`
	Host        string   `json:"host,omitempty"` // with all: the paired host whose pending events are meant (default: this one)
	Note        string   `json:"note"`
	OriginInbox string   `json:"origin_inbox,omitempty"` // the caller's own session, so the ack says who gave the order
}

// ackResponse is the answer: what became of each id, and the count after.
type ackResponse struct {
	Results   []watch.AckResult `json:"results"`
	Handled   int               `json:"handled"`
	Unhandled int               `json:"unhandled"`
}

func (m *Module) handleAck(w http.ResponseWriter, r *http.Request) {
	if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice || !devices.IsAdmin(r.Context()) {
		m.writeErr(w, http.StatusForbidden, "forbidden", "only the admin token marks events handled")
		return
	}
	var req ackBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAckBody)).Decode(&req); err != nil {
		m.writeErr(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := watch.ValidateNote(req.Note); err != nil {
		m.writeErr(w, http.StatusBadRequest, "bad_note", err.Error())
		return
	}
	if why := validAck(req.IDs, req.All, req.Note); why != "" {
		m.writeErr(w, http.StatusBadRequest, "bad_request", why)
		return
	}
	if req.Host != "" && !req.All {
		m.writeErr(w, http.StatusBadRequest, "bad_request", "host is for all: name the ids of that host's events instead")
		return
	}
	by := m.whoIs(req.OriginInbox)
	own := m.hostName()
	ack := watch.Ack{Note: req.Note, By: by}
	ctx := r.Context()

	// all, on this host or on the paired host named
	if req.All {
		if req.Host == "" || m.isSelf(req.Host) {
			resp, status, err := m.ackLocal(nil, true, ack)
			if err != nil {
				m.writeErr(w, status, "ack_failed", err.Error())
				return
			}
			m.writeJSON(w, http.StatusOK, resp)
			return
		}
		entry, _, ok := m.peerEntry(req.Host)
		if !ok {
			m.writeErr(w, http.StatusBadRequest, "bad_request", "no paired host "+req.Host)
			return
		}
		res := m.forwardAck(ctx, entry, nil, true, req.Note, by)
		m.writeJSON(w, http.StatusOK, ackResponse{Results: res, Handled: countHandled(res), Unhandled: m.unhandledOrZero()})
		return
	}

	// ids: those of this host here, the others to the host that recorded them, the rest not found
	byID := map[string]watch.AckResult{}
	var local []string
	foreign := map[string][]string{} // host name -> ids
	for _, id := range req.IDs {
		switch o := ownerOf(id); {
		case o == own:
			local = append(local, id)
		default:
			if _, ok := m.peerByHostName(o); ok {
				foreign[o] = append(foreign[o], id)
			} else {
				byID[id] = watch.AckResult{ID: id, Outcome: watch.AckNotFound}
			}
		}
	}
	if len(local) > 0 {
		resp, status, err := m.ackLocal(local, false, ack)
		if err != nil {
			m.writeErr(w, status, "ack_failed", err.Error())
			return
		}
		for _, x := range resp.Results {
			byID[x.ID] = x
		}
	}
	for name, ids := range foreign {
		entry, _ := m.peerByHostName(name)
		for _, x := range m.forwardAck(ctx, entry, ids, false, req.Note, by) {
			byID[x.ID] = x
		}
	}
	res := make([]watch.AckResult, 0, len(req.IDs))
	for _, id := range req.IDs {
		x, ok := byID[id]
		if !ok { // an id the other host's answer did not mention: reported, not assumed
			x = watch.AckResult{ID: id, Outcome: watch.AckRefused}
		}
		res = append(res, x)
	}
	m.writeJSON(w, http.StatusOK, ackResponse{Results: res, Handled: countHandled(res), Unhandled: m.unhandledOrZero()})
}

func countHandled(res []watch.AckResult) int {
	n := 0
	for _, x := range res {
		if x.Outcome == watch.AckHandled {
			n++
		}
	}
	return n
}

func (m *Module) unhandledOrZero() int { n, _ := m.unhandledCount(); return n }

// ackLocal marks this host's events handled: the ids named (those that are not this host's events come back not_found), or every
// pending one. watch.events goes out once when anything became handled.
func (m *Module) ackLocal(ids []string, all bool, a watch.Ack) (ackResponse, int, error) {
	recs, err := m.records()
	if err != nil {
		return ackResponse{}, http.StatusInternalServerError, err
	}
	have := make(map[string]bool, len(recs))
	for _, r := range recs {
		have[r.ID] = true
	}
	if all {
		acks := m.acks.All()
		for _, r := range recs {
			if _, ok := acks[r.ID]; !ok {
				ids = append(ids, r.ID)
			}
		}
	}
	res, err := m.acks.Ack(ids, func(id string) bool { return have[id] }, a)
	if err != nil {
		return ackResponse{}, http.StatusInternalServerError, err
	}
	handled := 0
	handled = countHandled(res)
	if handled > 0 {
		m.emitEvents(CauseAcked)
	}
	n, _ := m.unhandledCount()
	return ackResponse{Results: res, Handled: handled, Unhandled: n}, http.StatusOK, nil
}

// whoIs is the address of the session the caller says it is (its CLAUDE_CODE_MESSAGING_SOCKET), or "" when it cannot be told.
func (m *Module) whoIs(inbox string) string {
	if inbox == "" || m.core == nil {
		return ""
	}
	svc, ok := m.core.Registry.Get(peers.OriginResolverKey)
	if !ok {
		return ""
	}
	res, ok := svc.(interface {
		ResolveOrigin(inbox string) (team.Origin, bool, error)
	})
	if !ok {
		return ""
	}
	o, found, err := res.ResolveOrigin(inbox)
	if err != nil || !found {
		return ""
	}
	if o.Address != "" {
		return watch.Plain(o.Address)
	}
	return watch.Plain(o.Ref)
}

// retryEvents sends watch.events again a little later, once, for a count that could not be read: the state changed, so the clients
// must hear of it. A retry that fails again asks for the next one.
func (m *Module) retryEvents(cause string) {
	m.evRetryMu.Lock()
	if m.evRetrying {
		m.evRetryMu.Unlock()
		return
	}
	m.evRetrying = true
	m.evRetryMu.Unlock()
	after := m.evRetryAfter
	if after <= 0 {
		after = evRetryDefault
	}
	time.AfterFunc(after, func() {
		m.evRetryMu.Lock()
		m.evRetrying = false
		m.evRetryMu.Unlock()
		if _, done, ok := m.enter(context.Background()); ok {
			defer done()
			m.emitEvents(cause)
		}
	})
}

const evRetryDefault = 5 * time.Second
