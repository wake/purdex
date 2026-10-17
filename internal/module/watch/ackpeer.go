package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/watch"
)

// Marking the events of another host handled (host-load-control spec §12.3): the watcher is on one host and the events it deals with
// are on several, and an event is handled where it was recorded. The daemon that is asked to ack an id that is another host's sends
// it to that host (POST /api/peers/watch/ack); that host handles only the events it recorded and never passes anything on, so no
// chain of forwards can form. The answer says what became of each id, the ones that could not be reached included.

// pathAck is the peer route a host sends an ack to the host that recorded the events.
const pathAck = "/api/peers/watch/ack"

// ackPeerBody is the request of pathAck: the ack, and who gave the order (an address, as the sender resolved it).
type ackPeerBody struct {
	ToHostID string   `json:"to_host_id"`
	IDs      []string `json:"ids,omitempty"`
	All      bool     `json:"all,omitempty"`
	Note     string   `json:"note"`
	By       string   `json:"by,omitempty"`
	// CountOnly asks for the host's pending count and nothing else (no ids, no note): the ack's answer lists every target's count.
	CountOnly bool `json:"count_only,omitempty"`
}

// ackPeerResponse is the answer: the ack's, with the host that answered named (the caller checks it).
type ackPeerResponse struct {
	ackResponse
	HostID string `json:"host_id"`
}

const maxByLen = 128

// hostPart is the host name in a host id: what the ids of that host's events start with.
func hostPart(hostID string) string {
	name, _, _ := strings.Cut(hostID, ":")
	return name
}

// ownerOf is the host name an event id belongs to (the id is host + "-" + a ULID, and a host name may hold a dash).
func ownerOf(id string) string {
	if i := strings.LastIndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return ""
}

// peersByHostName are the paired hosts whose events' ids start with name.
func (m *Module) peersByHostName(name string) []config.PeerHost {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	var out []config.PeerHost
	for _, h := range m.core.Cfg.Peers.Hosts {
		if hostPart(h.HostID) == name {
			out = append(out, h)
		}
	}
	return out
}

// forwardAck sends an ack to the host that recorded the events and returns what became of each id. A host that cannot be reached, has
// no such route or refuses is reported for each id (or once for an "all"), never hidden.
//
// unhandled is the count that host reported after the ack (what is left on it), or -1 when it did not report one.
func (m *Module) forwardAck(ctx context.Context, entry config.PeerHost, ids []string, all bool, note, by string) (results []watch.AckResult, unhandled int) {
	failed := func(outcome watch.AckOutcome) ([]watch.AckResult, int) {
		if all {
			return []watch.AckResult{{ID: "*@" + entry.Alias, Outcome: outcome}}, -1
		}
		out := make([]watch.AckResult, len(ids))
		for i, id := range ids {
			out[i] = watch.AckResult{ID: id, Outcome: outcome}
		}
		return out, -1
	}
	if m.caller == nil || entry.HostID == "" {
		return failed(watch.AckUnreachable)
	}
	res := m.caller.Call(ctx, entry.HostID, pathAck, ackPeerBody{ToHostID: entry.HostID, IDs: ids, All: all, Note: note, By: by})
	switch res.Class {
	case peersmod.ClassDone:
		var out ackPeerResponse
		if err := json.Unmarshal(res.Body, &out); err != nil {
			return failed(watch.AckRefused)
		}
		if out.Unhandled < 0 {
			out.Unhandled = -1
		}
		return sanitizeAnswer(out.Results, ids, all, hostPart(entry.HostID)), out.Unhandled
	case peersmod.ClassUnsupported:
		return failed(watch.AckUnsupported)
	case peersmod.ClassRefused:
		return failed(watch.AckRefused)
	}
	return failed(watch.AckUnreachable)
}

// knownOutcome says whether o is something an ack can come to.
func knownOutcome(o watch.AckOutcome) bool {
	switch o {
	case watch.AckHandled, watch.AckAlready, watch.AckNotFound:
		return true
	}
	return false
}

const maxAllResults = 1000

// sanitizeAnswer takes from a host's answer only what that host may say. For named ids: the outcome of each id that was sent, once (the
// first stands), in the order they were sent; one that is missing or has an outcome that does not exist is refused, and an id that
// was not sent is ignored. For "all": the events of that host (their ids start with its name), once each, with outcomes that exist.
func sanitizeAnswer(got []watch.AckResult, asked []string, all bool, owner string) []watch.AckResult {
	if all {
		seen := map[string]bool{}
		var out []watch.AckResult
		for _, x := range got {
			if seen[x.ID] || ownerOf(x.ID) != owner || !eventIDPattern.MatchString(x.ID) || !knownOutcome(x.Outcome) || len(out) >= maxAllResults {
				continue
			}
			seen[x.ID] = true
			out = append(out, x)
		}
		return out
	}
	first := map[string]watch.AckOutcome{}
	for _, x := range got {
		if _, dup := first[x.ID]; !dup {
			first[x.ID] = x.Outcome
		}
	}
	out := make([]watch.AckResult, len(asked))
	for i, id := range asked {
		o, ok := first[id]
		if !ok || !knownOutcome(o) {
			o = watch.AckRefused
		}
		out[i] = watch.AckResult{ID: id, Outcome: o}
	}
	return out
}

// handlePeerAck serves POST /api/peers/watch/ack: a paired host marks events of this host handled. The order is the other watch
// routes': the binding of the host principal, the rate limit and the body cap, then to_host_id. Only the events this host recorded
// are handled; an id of another host is not found, and nothing is passed on.
func (m *Module) handlePeerAck(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ours string
	_, _, berr := peersmod.BindHostPrincipal(r, "watch ack", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ours, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[watch] ack refused (%s): %s", berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	var req ackPeerBody
	switch st := peersmod.AdmitDecode(w, r, m.limit, entry.HostID, maxAckBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over 64 KiB")
		return
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return
	}
	if req.ToHostID != ours {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return
	}
	if req.CountOnly {
		m.writeJSON(w, http.StatusOK, ackPeerResponse{ackResponse: ackResponse{Unhandled: m.unhandledOrMinus()}, HostID: ours})
		return
	}
	if why := validAck(req.IDs, req.All, req.Note); why != "" {
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, why)
		return
	}
	if len(req.By) > maxByLen || watch.HasControl(req.By) {
		m.writeErr(w, http.StatusBadRequest, ipeers.ErrBadRequest, "by must be one short line")
		return
	}
	// who the order came from: the host this call was verified to come from, whatever the body says
	resp, status, err := m.ackLocal(dedupeIDs(req.IDs), req.All, watch.Ack{Note: req.Note, By: req.By, FromHost: hostPart(entry.HostID)})
	if err != nil {
		m.writeErr(w, status, "ack_failed", err.Error())
		return
	}
	m.writeJSON(w, http.StatusOK, ackPeerResponse{ackResponse: resp, HostID: ours})
}

// dedupeIDs is ids with each id once, in the order it first appears: an id named twice is one event, and one answer.
func dedupeIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// validAck checks the part of an ack both routes share: the note, and either ids or all.
func validAck(ids []string, all bool, note string) string {
	if err := watch.ValidateNote(note); err != nil {
		return err.Error()
	}
	if (len(ids) == 0) == !all || len(ids) > maxAckIDs {
		return "name 1 to 500 ids, or ask for all"
	}
	for _, id := range ids {
		if !eventIDPattern.MatchString(id) {
			return "an id has characters it may not have"
		}
	}
	return ""
}

// askLeft is how many events are pending on the paired host named alias (-1: it did not say: unreachable, an older daemon that does not
// know count_only, or an answer that does not read).
func (m *Module) askLeft(ctx context.Context, alias string) int {
	entry, _, ok := m.peerEntry(alias)
	if !ok || m.caller == nil || entry.HostID == "" {
		return -1
	}
	res := m.caller.Call(ctx, entry.HostID, pathAck, ackPeerBody{ToHostID: entry.HostID, CountOnly: true})
	if res.Class != peersmod.ClassDone {
		return -1
	}
	var out ackPeerResponse
	if json.Unmarshal(res.Body, &out) != nil || out.Unhandled < 0 {
		return -1
	}
	return out.Unhandled
}
