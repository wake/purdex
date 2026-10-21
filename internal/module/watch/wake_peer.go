package watchmod

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
)

// The peer routes of the wake settings (spec section 4), served by every host. They are POST because HostCaller only POSTs, and each is
// addressed by to_host_id like the holder route. get and put are for the source (another host answers 503 not_source inside the envelope),
// changed is for the hosts that follow, wake-suppressed is for the source.

const (
	maxWakeSmallBody = 4 << 10
	maxWakeReport    = 64 << 10
)

func peerHostIDs(hosts []config.PeerHost) []string {
	var ids []string
	for _, h := range hosts {
		if h.HostID != "" {
			ids = append(ids, h.HostID)
		}
	}
	return ids
}

// admitWakePeer is the front of every peer route here, in the order the team routes have it: the host principal bound to its live entry, the
// per-host rate limit and the body cap before the body is decoded, then to_host_id. It writes the refusal itself and returns false.
func (m *Module) admitWakePeer(w http.ResponseWriter, r *http.Request, what string, maxBody int64, req any, to func() string) (ours string, sender config.PeerHost, ok bool) {
	var entry config.PeerHost
	_, _, berr := peersmod.BindHostPrincipal(r, what, func(alias string) (config.PeerHost, bool) {
		var found bool
		entry, ours, found = m.peerEntry(alias)
		return entry, found
	})
	if berr != nil {
		m.logf("[watch] %s refused (%s): %s", what, berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return "", config.PeerHost{}, false
	}
	switch st := peersmod.AdmitDecode(w, r, m.limit, entry.HostID, maxBody, req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return "", config.PeerHost{}, false
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over the limit")
		return "", config.PeerHost{}, false
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return "", config.PeerHost{}, false
	}
	if to() != ours {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return "", config.PeerHost{}, false
	}
	return ours, entry, true
}

// writeEnvelope answers a get or put: the inner status and body inside a 200 that names this host (HostCaller checks the name).
func (m *Module) writeEnvelope(w http.ResponseWriter, ours string, reply wakeReply) {
	body, err := json.Marshal(reply.Body)
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, "encode", "the answer could not be encoded")
		return
	}
	m.writeJSON(w, http.StatusOK, wakeEnvelope{HostID: ours, Status: reply.Status, Body: body})
}

// handlePeerWakeGet: a host asks for the source's document.
func (m *Module) handlePeerWakeGet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToHostID string `json:"to_host_id"`
	}
	ours, _, ok := m.admitWakePeer(w, r, "wake settings read", maxWakeSmallBody, &req, func() string { return req.ToHostID })
	if !ok {
		return
	}
	source, isSource := m.wakeSourceAlias()
	if !isSource {
		m.writeEnvelope(w, ours, wakeReply{http.StatusServiceUnavailable, map[string]string{"error": "not_source", "source": source}})
		return
	}
	d, err := m.readWakeSource()
	if err != nil {
		m.writeEnvelope(w, ours, m.wakeReadReply(err))
		return
	}
	m.writeEnvelope(w, ours, wakeReply{http.StatusOK, m.wakeView(d, source)})
}

// handlePeerWakePut: a host passes a PUT of its admin on to the source. The source's own checks decide; what it answers goes back whole.
func (m *Module) handlePeerWakePut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToHostID   string          `json:"to_host_id"`
		Settings   json.RawMessage `json:"settings"`
		ExpectEtag string          `json:"expect_etag"`
	}
	ours, _, ok := m.admitWakePeer(w, r, "wake settings write", maxWakePut+maxWakeSmallBody, &req, func() string { return req.ToHostID })
	if !ok {
		return
	}
	if len(req.Settings) == 0 {
		m.writeEnvelope(w, ours, replyCode(http.StatusBadRequest, "bad_request"))
		return
	}
	m.writeEnvelope(w, ours, m.putWakeSource(wakePutBody{Settings: req.Settings, ExpectEtag: req.ExpectEtag}))
}

// handlePeerWakeChanged: the source says the document has a new revision. A host that follows reads it now; the source itself ignores it. The
// revision in the notice is only a hint (the read decides), so a wrong one costs one read.
func (m *Module) handlePeerWakeChanged(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToHostID string `json:"to_host_id"`
		Revision int64  `json:"revision"`
	}
	ours, _, ok := m.admitWakePeer(w, r, "wake settings notice", maxWakeSmallBody, &req, func() string { return req.ToHostID })
	if !ok {
		return
	}
	if _, isSource := m.wakeSourceAlias(); !isSource {
		m.requestWakePoll()
	}
	m.writeJSON(w, http.StatusOK, map[string]string{"host_id": ours})
}

// handlePeerWakeSuppressed: a host reports wake-ups its settings stopped. The source keeps the last 50 of all hosts; any other host takes
// nothing (the report was meant for the source).
func (m *Module) handlePeerWakeSuppressed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToHostID string           `json:"to_host_id"`
		Entries  []wakeSuppressed `json:"entries"`
	}
	ours, sender, ok := m.admitWakePeer(w, r, "wake suppressed report", maxWakeReport, &req, func() string { return req.ToHostID })
	if !ok {
		return
	}
	if len(req.Entries) > maxSuppressedPerReport {
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, "too many entries")
		return
	}
	if _, isSource := m.wakeSourceAlias(); isSource {
		now := m.now()
		var keep []wakeSuppressed
		for _, e := range req.Entries {
			if validSuppressed(e, now) {
				e.Host = sender.Alias // a report is about the host that sent it, whoever the entry says it is about
				keep = append(keep, e)
			}
		}
		m.suppressed.add(keep...)
	}
	m.writeJSON(w, http.StatusOK, map[string]string{"host_id": ours})
}
