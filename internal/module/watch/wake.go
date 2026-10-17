package watchmod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/watch"
)

// pathNote is the peer route a host tells another host's watcher a wake note on (an older daemon answers 404: no note, an error).
const pathNote = "/api/peers/watch/note"

// noteBody is the request of pathNote.
type noteBody struct {
	ToHostID string `json:"to_host_id"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail,omitempty"`
}

// WakeWatcher finds the watcher (waking or starting one as auto_wake allows, spec §12.3) and tells it why, in one note. An error means
// the note did not reach a watcher: none is awake and none could be woken, or the host that has it did not take the note.
func (m *Module) WakeWatcher(ctx context.Context, reason, detail string) error {
	if err := watch.ValidateWake(reason, detail); err != nil {
		return err
	}
	s, err := m.readSettings()
	if err != nil {
		return err
	}
	where, err := m.loc.Current(ctx, s.Targets, s.AutoWake)
	if err != nil {
		return err
	}
	text := watch.WakeText(reason, detail)
	if m.isSelf(where.Alias) {
		own := m.hs().Get()
		if own.State != watch.HolderAwake {
			return errNoWatcherHere
		}
		return m.notifyWatcher(ctx, own, text)
	}
	if m.caller == nil {
		return errors.New("no host caller")
	}
	res := m.caller.Call(ctx, where.HostID, pathNote, noteBody{ToHostID: where.HostID, Reason: reason, Detail: detail})
	if res.Class != peersmod.ClassDone {
		return fmt.Errorf("%s: %s %s %s", where.Alias, res.Class, res.Code, res.Detail)
	}
	return nil
}

// handlePeerNote serves POST /api/peers/watch/note: a paired host tells this host's watcher a wake note. The order is the holder
// route's (the binding of the host principal, the rate limit and the body cap, then to_host_id).
func (m *Module) handlePeerNote(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ours string
	_, _, berr := peersmod.BindHostPrincipal(r, "watch note", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ours, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[watch] note refused (%s): %s", berr.Code, berr.Detail)
		m.writeErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	var req noteBody
	switch st := peersmod.AdmitDecode(w, r, m.limit, entry.HostID, maxSessionBody, &req); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeErr(w, st, ipeers.ErrBadRequest, "body over 8 KiB")
		return
	default:
		m.writeErr(w, st, ipeers.ErrBadRequest, "invalid JSON body")
		return
	}
	if req.ToHostID != ours {
		m.writeErr(w, http.StatusConflict, "wrong_host", "this request is addressed to another host")
		return
	}
	if err := watch.ValidateWake(req.Reason, req.Detail); err != nil {
		m.writeErr(w, http.StatusBadRequest, "bad_note", err.Error())
		return
	}
	m.fresh(r.Context())
	own := m.hs().Get()
	if own.State != watch.HolderAwake {
		m.writeErr(w, http.StatusConflict, "no_watcher", errNoWatcherHere.Error())
		return
	}
	if err := m.notifyWatcher(r.Context(), own, watch.WakeText(req.Reason, req.Detail)); err != nil {
		m.logf("[watch] wake note: %v", err)
		m.writeErr(w, http.StatusBadGateway, "not_delivered", "the watcher could not be told")
		return
	}
	m.writeJSON(w, http.StatusOK, json.RawMessage(`{"told":true}`))
}
