package watchmod

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/watch"
)

// Reading the events of another host (host-load-control spec §12.3: `pdx watch log --host | --all`): the daemon asks the paired host
// through POST /api/peers/watch/log, which answers with its own events only, and puts the answers together, newest first. A host that
// cannot be asked is named in the answer next to the events of the others.

// pathLog is the peer route a host asks another host's events on.
const pathLog = "/api/peers/watch/log"

const logDefaultLimit = 500

// logQuery is what is asked of a host's events; the host that holds them applies it.
type logQuery struct {
	Since     time.Time
	Kind      watch.Kind
	Unhandled bool
	Limit     int
}

// hostLog is what became of asking one host.
type hostLog struct {
	Host  string `json:"host"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// peerLogBody is the request of pathLog.
type peerLogBody struct {
	ToHostID  string     `json:"to_host_id"`
	Since     *time.Time `json:"since,omitempty"`
	Kind      watch.Kind `json:"kind,omitempty"`
	Unhandled bool       `json:"unhandled,omitempty"`
	Limit     int        `json:"limit,omitempty"`
}

// peerLogResponse is the answer of pathLog.
type peerLogResponse struct {
	Events []watch.EventView `json:"events"`
	HostID string            `json:"host_id"`
}

// eventViews are this host's events that match q, newest first.
func (m *Module) eventViews(q logQuery) ([]watch.EventView, error) {
	since := m.now().Add(-recordKeep)
	if q.Since.After(since) {
		since = q.Since
	}
	recs, err := watch.ReadLog(m.dlv.logDir, since, q.Kind)
	if err != nil {
		return nil, err
	}
	acks := m.acks.All()
	limit := q.Limit
	if limit <= 0 || limit > eventsMaxLimit {
		limit = eventsMaxLimit
	}
	out := make([]watch.EventView, 0, min(limit, len(recs)))
	for i := len(recs) - 1; i >= 0 && len(out) < limit; i-- {
		var a *watch.Ack
		if x, ok := acks[recs[i].ID]; ok {
			a = &x
		}
		if q.Unhandled && a != nil {
			continue
		}
		out = append(out, watch.ViewOf(recs[i], a))
	}
	return out, nil
}

func validKind(k watch.Kind) bool { return k == "" || watch.KnownKind(k) }

// handlePeerLog serves POST /api/peers/watch/log: a paired host reads this host's events. Its own events only; nothing is asked of any
// other host.
func (m *Module) handlePeerLog(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ours string
	_, _, berr := peersmod.BindHostPrincipal(r, "watch log", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ours, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[watch] log refused (%s): %s", berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	var req peerLogBody
	switch st := peersmod.AdmitDecode(w, r, m.limit, entry.HostID, maxHolderBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over 4 KiB")
		return
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return
	}
	if req.ToHostID != ours {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return
	}
	if !validKind(req.Kind) || req.Limit < 0 || req.Limit > eventsMaxLimit {
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, "kind or limit is not valid")
		return
	}
	q := logQuery{Kind: req.Kind, Unhandled: req.Unhandled, Limit: req.Limit}
	if req.Since != nil {
		q.Since = *req.Since
	}
	evs, err := m.eventViews(q)
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}
	m.writeJSON(w, http.StatusOK, peerLogResponse{Events: evs, HostID: ours})
}

// askLog reads the events of a paired host through the peer route.
func (m *Module) askLog(r *http.Request, entry config.PeerHost, q logQuery) ([]watch.EventView, string) {
	if m.caller == nil || entry.HostID == "" {
		return nil, "unreachable"
	}
	body := peerLogBody{ToHostID: entry.HostID, Kind: q.Kind, Unhandled: q.Unhandled, Limit: q.Limit}
	if !q.Since.IsZero() {
		body.Since = &q.Since
	}
	res := m.caller.Call(r.Context(), entry.HostID, pathLog, body)
	switch res.Class {
	case peersmod.ClassDone:
		var out peerLogResponse
		if err := json.Unmarshal(res.Body, &out); err != nil {
			return nil, "refused: an answer that cannot be read"
		}
		return out.Events, ""
	case peersmod.ClassUnsupported:
		return nil, "an older daemon: it has no event log route"
	case peersmod.ClassRefused:
		return nil, "refused: " + res.Detail
	}
	return nil, "unreachable: " + res.Detail
}

// parseLogQuery reads the filters of GET /api/watch/log; why is "" when they are fine.
func parseLogQuery(v url.Values, now time.Time) (q logQuery, why string) {
	q.Kind = watch.Kind(v.Get("kind"))
	if !validKind(q.Kind) {
		return q, "kind is not a kind of event"
	}
	q.Unhandled = v.Get("unhandled") == "1" || v.Get("unhandled") == "true"
	if s := v.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return q, "since must be an RFC 3339 time"
		}
		q.Since = t
	}
	q.Limit = logDefaultLimit
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > eventsMaxLimit {
			return q, "limit must be 1 to 500"
		}
		q.Limit = n
	}
	return q, ""
}

// handleLog serves GET /api/watch/log?host=<alias>|all: this host's events by default, another paired host's by its alias, or every
// host's, newest first, with what became of asking each. It is the `pdx watch log` of a host that is not this one.
func (m *Module) handleLog(w http.ResponseWriter, r *http.Request) {
	q, why := parseLogQuery(r.URL.Query(), m.now())
	if why != "" {
		m.writeErr(w, http.StatusBadRequest, "bad_request", why)
		return
	}
	host := r.URL.Query().Get("host")
	m.core.CfgMu.RLock()
	ownAlias := m.core.Cfg.PeerAlias()
	peers := append([]config.PeerHost(nil), m.core.Cfg.Peers.Hosts...)
	m.core.CfgMu.RUnlock()

	wantSelf := host == "" || host == "all" || m.isSelf(host)
	var ask []config.PeerHost
	switch {
	case host == "all":
		ask = peers
	case host != "" && !m.isSelf(host):
		entry, _, ok := m.peerEntry(host)
		if !ok {
			m.writeErr(w, http.StatusBadRequest, "bad_request", "no paired host "+host)
			return
		}
		ask = []config.PeerHost{entry}
	}
	var events []watch.EventView
	var hosts []hostLog
	if wantSelf {
		evs, err := m.eventViews(q)
		if err != nil {
			hosts = append(hosts, hostLog{Host: ownAlias, Error: err.Error()})
		} else {
			events = append(events, evs...)
			hosts = append(hosts, hostLog{Host: ownAlias, OK: true})
		}
	}
	for _, e := range ask {
		evs, errText := m.askLog(r, e, q)
		if errText != "" {
			hosts = append(hosts, hostLog{Host: e.Alias, Error: errText})
			continue
		}
		events = append(events, evs...)
		hosts = append(hosts, hostLog{Host: e.Alias, OK: true})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if len(events) > q.Limit {
		events = events[:q.Limit]
	}
	if events == nil {
		events = []watch.EventView{}
	}
	m.writeJSON(w, http.StatusOK, map[string]any{"events": events, "hosts": hosts})
}
