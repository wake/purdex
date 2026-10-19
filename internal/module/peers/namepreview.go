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

// renamePreview is how many live conversations the backfill would look at if a collection with these codes were saved now: the
// first pass's work, counted (a conversation in a relay is left to a later pass but is renamed in the end, so it is counted).
func (m *Module) renamePreview(ctx context.Context, codes []string) (int, error) {
	if m.peerNames == nil {
		return 0, errNoNameStore
	}
	entries, _, err := ipeers.ReadRegistry(m.registryDir, m.liveness)
	if err != nil {
		return 0, err
	}
	sids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.SessionID != "" {
			sids = append(sids, e.SessionID)
		}
	}
	rows, err := m.peerNames.Lookup(ctx, sids)
	if err != nil {
		return 0, err
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
	return len(backfillCandidates(entries, m.proxyPIDs(), names, hasCode)), nil
}

// handleRenamePreview is POST /api/peers/rename-preview {codes: [...]} → {would_rename: N}.
func (m *Module) handleRenamePreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Codes []string `json:"codes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, previewBodyLimit)).Decode(&req); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Codes) > previewMaxCodes {
		http.Error(w, "too many codes", http.StatusBadRequest)
		return
	}
	for _, c := range req.Codes {
		if len(c) > previewMaxCodeLen {
			http.Error(w, "a code is too long", http.StatusBadRequest)
			return
		}
	}
	n, err := m.renamePreview(r.Context(), req.Codes)
	if err != nil {
		m.logf("peers: rename preview: %v", err)
		http.Error(w, "the names are not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"would_rename": n})
}
