package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
)

// The rename preview (issue #3520). Before the first persona is saved the App tells the person how many conversations on the host
// will take a persona name. That number used to be derived in the App from the tmux session rows, but the backfill walks every live
// registry entry, so a conversation with no session row (a headless one) was renamed and not counted. The daemon now counts, with
// the judgement the backfill uses (backfillCandidates).
//
// It is a dry run that takes the codes of the collection about to be saved, not a field of GET /api/hostconfig/personas: the
// answer depends on that collection (a name that already ends in one of its codes is not renamed), which a GET cannot know, and
// the hostconfig module holds no registry or names. It reads and writes nothing.

const (
	// previewMaxCodes and previewMaxCodeLen bound the body: a collection holds at most this many personas, a code this many bytes.
	previewMaxCodes   = 2000
	previewMaxCodeLen = 64
	previewBodyLimit  = 1 << 20
)

// renamePreview counts, for a collection whose codes are codes and whose active (pickable) codes are pickable: candidates, the live
// conversations the backfill would look at, and wouldRename, how many of them it can actually rename — no more than there are
// pickable names no live conversation holds, because the pool runs out and the rest keep their own names. A conversation in a relay
// is left to a later pass but is renamed in the end, so it is counted. Names held by another host's snapshot or by a reservation
// are not known here; they can only make the real number smaller.
func (m *Module) renamePreview(ctx context.Context, codes, pickable []string) (candidates, wouldRename int, err error) {
	if m.peerNames == nil {
		return 0, 0, errNoNameStore
	}
	entries, _, err := ipeers.ReadRegistry(m.registryDir, m.liveness)
	if err != nil {
		return 0, 0, err
	}
	sids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.SessionID != "" {
			sids = append(sids, e.SessionID)
		}
	}
	rows, err := m.peerNames.Lookup(ctx, sids)
	if err != nil {
		return 0, 0, err
	}
	names := make(map[string]string, len(entries))
	for _, e := range entries {
		if r, ok := rows[nameSID(e.SessionID)]; ok {
			names[e.SessionID] = r.Name
		}
	}
	set := make(map[string]bool, len(codes))
	for _, c := range codes {
		set[c] = true
	}
	// the last segment of the name, as PoolNameOf reads it
	hasCode := func(name string) bool {
		i := strings.LastIndexByte(name, '-')
		return i >= 0 && set[name[i+1:]]
	}
	cands := backfillCandidates(entries, m.proxyPIDs(), names, hasCode)
	// the pickable names a live conversation already holds (its name ends in one) are not free
	pick := make(map[string]bool, len(pickable))
	for _, c := range pickable {
		pick[c] = true
	}
	held := map[string]bool{}
	for _, name := range names {
		if i := strings.LastIndexByte(name, '-'); i >= 0 && pick[name[i+1:]] {
			held[name[i+1:]] = true
		}
	}
	free := len(pick) - len(held)
	return len(cands), min(len(cands), free), nil
}

// handleRenamePreview is POST /api/peers/rename-preview {codes, pickable?} → {would_rename, candidates}.
func (m *Module) handleRenamePreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Codes []string `json:"codes"`
		// Pickable are the active codes (the names the picker may give out); absent means every code is, present and empty none is.
		Pickable *[]string `json:"pickable"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, previewBodyLimit)).Decode(&req); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}
	pickable := req.Codes
	if req.Pickable != nil {
		pickable = *req.Pickable
	}
	if len(req.Codes) > previewMaxCodes || len(pickable) > previewMaxCodes {
		http.Error(w, "too many codes", http.StatusBadRequest)
		return
	}
	for _, c := range append(append([]string(nil), req.Codes...), pickable...) {
		if len(c) > previewMaxCodeLen {
			http.Error(w, "a code is too long", http.StatusBadRequest)
			return
		}
	}
	cands, would, err := m.renamePreview(r.Context(), req.Codes, pickable)
	if err != nil {
		m.logf("peers: rename preview: %v", err)
		http.Error(w, "the names are not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"would_rename": would, "candidates": cands})
}
