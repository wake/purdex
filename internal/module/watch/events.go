package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

// ackResponse is the answer: what became of each id, and the count after. Unhandled keeps its meaning (what is left on the host the ack
// was about: this host, or the one named by --host), so a CLI that predates Hosts reads it as it always did. Hosts is what is left on
// this host and on every other target, and Remaining their sum, or -1 when one of them did not say; a peer's answer carries only its
// own Unhandled.
type ackResponse struct {
	Results   []watch.AckResult `json:"results"`
	Handled   int               `json:"handled"`
	Unhandled int               `json:"unhandled"`
	Hosts     []HostLeft        `json:"hosts,omitempty"`
	Remaining *int              `json:"remaining,omitempty"`
}

// HostLeft is how many events are still pending on a host (-1: the host did not say).
type HostLeft struct {
	Host      string `json:"host"`
	Unhandled int    `json:"unhandled"`
}

// countBudget bounds the questions about the other hosts' pending counts together, and ackDeadline is how long after the request began
// they may still be asked: the CLI waits 10 s, and an ack that went through must not be reported as failed because the extra count
// was slow, so a count that cannot be had in time is "unknown".
const (
	countBudget = 2 * time.Second
	ackDeadline = 7 * time.Second
)

// leftEverywhere is the pending count of this host and of every other target: the ones learned from the ack itself (known, by alias) and
// the others asked side by side. A host that cannot be asked is -1, never 0.
func (m *Module) leftEverywhere(ctx context.Context, began time.Time, own int, known map[string]int) ([]HostLeft, int) {
	m.core.CfgMu.RLock()
	ownAlias := m.core.Cfg.PeerAlias()
	m.core.CfgMu.RUnlock()
	hosts := []HostLeft{{Host: ownAlias, Unhandled: own}}
	others := m.otherTargets()
	counts := make([]int, len(others))
	budget := min(countBudget, ackDeadline-time.Since(began))
	var wg sync.WaitGroup
	for i, alias := range others {
		if n, ok := known[alias]; ok {
			counts[i] = n
			continue
		}
		if budget <= 0 { // the ack itself took the time: no more questions
			counts[i] = -1
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			counts[i] = m.askLeft(cctx, alias)
		}()
	}
	wg.Wait()
	total := own
	for i, alias := range others {
		hosts = append(hosts, HostLeft{Host: alias, Unhandled: counts[i]})
		if counts[i] < 0 || total < 0 {
			total = -1
		} else {
			total += counts[i]
		}
	}
	return hosts, total
}

func (m *Module) handleAck(w http.ResponseWriter, r *http.Request) {
	began := time.Now()
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
	req.IDs = dedupeIDs(req.IDs)
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
			resp.Hosts, resp.Remaining = m.leftEverywhere(ctx, began, resp.Unhandled, nil)
			m.writeJSON(w, http.StatusOK, resp)
			return
		}
		entry, _, ok := m.peerEntry(req.Host)
		if !ok {
			m.writeErr(w, http.StatusBadRequest, "bad_request", "no paired host "+req.Host)
			return
		}
		res, left := m.forwardAck(ctx, entry, nil, true, req.Note, by)
		hosts, total := m.leftEverywhere(ctx, began, m.unhandledOrMinus(), map[string]int{entry.Alias: left})
		m.writeJSON(w, http.StatusOK, ackResponse{Results: res, Handled: countHandled(res), Unhandled: left, Hosts: hosts, Remaining: &total}) // Unhandled: what is left on that host
		return
	}

	// ids: those of this host here, the others to the host that recorded them, the rest not found
	byID := map[string]watch.AckResult{}
	var local []string
	known := map[string]int{}        // alias -> what is left there, as the ack's own answer said
	foreign := map[string][]string{} // host name -> ids
	for _, id := range req.IDs {
		o := ownerOf(id)
		peers := m.peersByHostName(o)
		candidates := len(peers)
		if o == own {
			candidates++
		}
		switch {
		case candidates > 1: // two hosts go by that name: the id does not say which; neither is guessed at
			byID[id] = watch.AckResult{ID: id, Outcome: watch.AckAmbiguous}
		case o == own:
			local = append(local, id)
		case candidates == 1:
			foreign[o] = append(foreign[o], id)
		default:
			byID[id] = watch.AckResult{ID: id, Outcome: watch.AckNotFound}
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
		entry := m.peersByHostName(name)[0]
		res, left := m.forwardAck(ctx, entry, ids, false, req.Note, by)
		known[entry.Alias] = left
		for _, x := range res {
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
	hosts, total := m.leftEverywhere(ctx, began, m.unhandledOrMinus(), known)
	m.writeJSON(w, http.StatusOK, ackResponse{Results: res, Handled: countHandled(res), Unhandled: m.unhandledOrZero(), Hosts: hosts, Remaining: &total})
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

// unhandledOrMinus is this host's pending count, -1 when it cannot be read.
func (m *Module) unhandledOrMinus() int {
	n, err := m.unhandledCount()
	if err != nil {
		return -1
	}
	return n
}

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
