package peers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// LabelStore is the peer_labels table (Peer Address v2 spec §3.3):
// *store.PeerLabelStore in production, a fake in tests. nil means "no
// store": every conversation has its default label and claims fail with
// store_unavailable.
type LabelStore interface {
	Snapshot() ([]store.PeerLabel, error)
	Claim(sessionID, label string, now time.Time) (store.PeerLabel, error)
	Release(sessionID string, now time.Time) (store.PeerLabel, bool, error)
}

// findOriginEntry attributes inbox to a live, non-proxy registry entry
// (entry attribution, spec §3.6 — not send.go's deliverable-row rule): the
// caller must name a registry entry directly, unlike /send's origin
// resolution which may fall through to a tmux-session match.
func findOriginEntry(entries []ipeers.Entry, proxyPIDs map[int]bool, inbox string) (ipeers.Entry, bool) {
	for _, e := range entries {
		if e.Inbox == inbox && !e.IsProxy && !proxyPIDs[e.PID] {
			return e, true
		}
	}
	return ipeers.Entry{}, false
}

// proxyPIDs is the helper manager's pid set, or empty without a manager
// (a test module built without Init, or one still starting up).
func (m *Module) proxyPIDs() map[int]bool {
	if m.helpers == nil {
		return map[int]bool{}
	}
	return m.helpers.ProxyPIDs()
}

// labelRows indexes a store snapshot by session id.
func labelRows(rows []store.PeerLabel) map[string]store.PeerLabel {
	out := make(map[string]store.PeerLabel, len(rows))
	for _, r := range rows {
		out[r.SessionID] = r
	}
	return out
}

// labelInfos indexes a store snapshot the way ResolveDefaultLabels reads
// it — the same shape labelSnapshot() hands Build (module.go), so the self
// routes resolve defaults over exactly the data the listing does and the
// two can never disagree about a caller's own address (spec §3.2).
func labelInfos(rows []store.PeerLabel) map[string]ipeers.LabelInfo {
	out := make(map[string]ipeers.LabelInfo, len(rows))
	for _, r := range rows {
		out[r.SessionID] = ipeers.LabelInfo{Label: r.Label, Rev: r.Rev}
	}
	return out
}

// infoOf converts a (possibly absent) store row into ipeers.LabelInfo:
// absent ⇒ the zero value, which EntryRecord/applyLabel render as the
// default label.
func infoOf(row store.PeerLabel, ok bool) ipeers.LabelInfo {
	if !ok {
		return ipeers.LabelInfo{}
	}
	return ipeers.LabelInfo{Label: row.Label, Rev: row.Rev}
}

// selfResult is the outcome of one whoami/claim/release call: exactly one
// of rec (status 200) or err (any other status) is set.
type selfResult struct {
	rec    ipeers.PeerRecord
	err    *ipeers.APIError
	status int
}

// fail builds a selfResult carrying an APIError.
func fail(status int, code, detail string) selfResult {
	return selfResult{status: status, err: &ipeers.APIError{Error: code, Detail: detail}}
}

// origin reads the registry and attributes inbox to a live, non-proxy
// entry. Shared by whoami/claim/release — the one registry read and origin
// check every verb makes before doing its own thing.
func (m *Module) origin(inbox string) (entries []ipeers.Entry, diag ipeers.Diagnosis, proxies map[int]bool, e ipeers.Entry, res selfResult, ok bool) {
	if inbox == "" {
		return nil, ipeers.Diagnosis{}, nil, ipeers.Entry{}, fail(http.StatusBadRequest, ipeers.ErrOriginUnknown, "origin_inbox is empty (CLAUDE_CODE_MESSAGING_SOCKET unset?)"), false
	}
	if m.labels == nil {
		return nil, ipeers.Diagnosis{}, nil, ipeers.Entry{}, fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store is not available"), false
	}
	entries, diag, err := ipeers.ReadRegistryDiag(m.registryDir, m.liveness)
	if err != nil {
		return nil, diag, nil, ipeers.Entry{}, fail(http.StatusServiceUnavailable, ipeers.ErrNotReady, "registry read failed: "+err.Error()), false
	}
	proxies = m.proxyPIDs()
	e, found := findOriginEntry(entries, proxies, inbox)
	if !found {
		return nil, diag, nil, ipeers.Entry{}, fail(http.StatusBadRequest, ipeers.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host"), false
	}
	return entries, diag, proxies, e, selfResult{}, true
}

// whoami answers the caller's own current record: its address, label and
// label source, read straight from the validated registry entry and the
// label store — no write, so a failing store only degrades this to
// store_unavailable, never a default label presented as fact (spec §3.6:
// "never print a default label as if it were the truth").
func (m *Module) whoami(inbox string) selfResult {
	m.labelMu.Lock()
	defer m.labelMu.Unlock()
	entries, _, proxies, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	rows, err := m.labels.Snapshot()
	if err != nil {
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store read failed")
	}
	snap := m.configSnapshot()
	row, has := labelRows(rows)[e.SessionID]
	// The entries and proxy set origin() already read ARE the population
	// (spec §3.2) — whoami reads nothing else, and resolving over them is
	// what makes its answer identical to the listing's.
	defaults := ipeers.ResolveDefaultLabels(entries, proxies, labelInfos(rows))
	return selfResult{status: http.StatusOK, rec: ipeers.EntryRecord(snap.alias, snap.hostID, e, false, infoOf(row, has), defaults)}
}

// claim gives the caller's own conversation label, evicting a dead
// session's hold on it but refusing a live one's (spec §3.3's claim
// matrix, restated in Task 7's brief): label rule → reserved → store nil
// → registry read error ⇒ not_ready → origin unknown → blocking unknown
// files ⇒ not_ready with skipped → store read error ⇒ store_unavailable →
// taken (live holder ≠ self) ⇒ 409 with holder + live_labels → already
// ours ⇒ 200 no write → write (error ⇒ store_unavailable) → 200.
func (m *Module) claim(inbox, label string) selfResult {
	if err := ipeers.ValidateUserLabel(label); err != nil {
		code := ipeers.ErrCodeLabelInvalid
		if errors.Is(err, ipeers.ErrLabelReserved) {
			code = ipeers.ErrCodeLabelReserved
		}
		return fail(http.StatusBadRequest, code, err.Error())
	}
	m.labelMu.Lock()
	defer m.labelMu.Unlock()
	entries, diag, proxies, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	if blocking := diag.BlockingUnknown(); len(blocking) > 0 {
		r := fail(http.StatusServiceUnavailable, ipeers.ErrNotReady, "registry has unreadable files for live processes; a label cannot be proven free")
		r.err.Skipped = blocking
		return r
	}
	rows, err := m.labels.Snapshot()
	if err != nil {
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store read failed")
	}

	// liveEntry maps every live, non-proxy entry's session id to that
	// entry (first seen), so a taken label's holder can be rendered with
	// EntryRecord from the entry already in hand, never a fresh registry
	// read.
	liveEntry := map[string]ipeers.Entry{}
	for _, le := range entries {
		if le.IsProxy || proxies[le.PID] {
			continue
		}
		if _, seen := liveEntry[le.SessionID]; !seen {
			liveEntry[le.SessionID] = le
		}
	}

	// liveLabels collects every label held by a live session — the
	// caller's own included, per spec §3.3 — for the label_taken body;
	// holder is the row (if any) that already holds the requested label.
	var liveLabels []string
	var holder *store.PeerLabel
	for i := range rows {
		row := &rows[i]
		if row.Label == "" {
			continue
		}
		if _, live := liveEntry[row.SessionID]; !live {
			continue
		}
		liveLabels = append(liveLabels, row.Label)
		if row.Label == label {
			holder = row
		}
	}
	sort.Strings(liveLabels)

	snap := m.configSnapshot()
	// One map for both records below, over the same population whoami and
	// the listing use (spec §3.4).
	defaults := ipeers.ResolveDefaultLabels(entries, proxies, labelInfos(rows))

	if holder != nil && holder.SessionID != e.SessionID {
		he := liveEntry[holder.SessionID]
		hrec := ipeers.EntryRecord(snap.alias, snap.hostID, he, false, ipeers.LabelInfo{Label: holder.Label, Rev: holder.Rev}, defaults)
		r := fail(http.StatusConflict, ipeers.ErrLabelTaken, fmt.Sprintf("%q is held by a live session", label))
		r.err.Holder, r.err.LiveLabels = &hrec, liveLabels
		return r
	}

	var row store.PeerLabel
	if holder != nil {
		// Already ours: no write, revision unchanged.
		row = *holder
	} else {
		row, err = m.labels.Claim(e.SessionID, label, m.now())
		if err != nil {
			m.logf("peers: claim %q for %s: %v", label, e.SessionID, err)
			return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store write failed")
		}
	}
	return selfResult{status: http.StatusOK, rec: ipeers.EntryRecord(snap.alias, snap.hostID, e, false, ipeers.LabelInfo{Label: row.Label, Rev: row.Rev}, defaults)}
}

// release clears the caller's own label, reverting it to the default. No
// registry completeness requirement (v2 §3.3): a registry file this daemon
// cannot classify never blocks releasing your own label, only claiming
// one you might not be free to take.
//
// It does read the label store first, though (spec §3.4): the record it
// returns must show the default the listing will show, and a default can
// only be resolved over the label rows. That read is a gate — it fails
// with store_unavailable and attempts NO write — so a release never hands
// back a default the daemon could not vouch for, and an unreadable store
// leaves the label exactly where it was.
// Resolving over the pre-write snapshot is safe precisely because of
// spec §3.3 rule 3's "other": the caller's own about-to-be-released label
// does not compete with its own candidate.
func (m *Module) release(inbox string) selfResult {
	m.labelMu.Lock()
	defer m.labelMu.Unlock()
	entries, _, proxies, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	rows, err := m.labels.Snapshot()
	if err != nil {
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store read failed")
	}
	defaults := ipeers.ResolveDefaultLabels(entries, proxies, labelInfos(rows))
	row, had, err := m.labels.Release(e.SessionID, m.now())
	if err != nil {
		m.logf("peers: release for %s: %v", e.SessionID, err)
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store write failed")
	}
	info := ipeers.LabelInfo{}
	if had {
		info.Rev = row.Rev
	}
	snap := m.configSnapshot()
	return selfResult{status: http.StatusOK, rec: ipeers.EntryRecord(snap.alias, snap.hostID, e, false, info, defaults)}
}

// handleSelf serves POST /api/peers/self: whoami.
func (m *Module) handleSelf(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	var req ipeers.SelfRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrBadRequest, Detail: "invalid JSON body"})
		return
	}
	writeSelfResult(w, m.whoami(req.OriginInbox))
}

// handleClaimLabel serves PUT /api/peers/self/label: claim.
func (m *Module) handleClaimLabel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	var req ipeers.ClaimLabelRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrBadRequest, Detail: "invalid JSON body"})
		return
	}
	writeSelfResult(w, m.claim(req.OriginInbox, req.Label))
}

// handleReleaseLabel serves DELETE /api/peers/self/label: release.
func (m *Module) handleReleaseLabel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	var req ipeers.SelfRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrBadRequest, Detail: "invalid JSON body"})
		return
	}
	writeSelfResult(w, m.release(req.OriginInbox))
}

// writeSelfResult encodes a selfResult: the error body on failure, the
// PeerRecord on success. Always called after labelMu is released — the
// lock covers the registry read, the store write and the construction of
// res itself, never the encode.
func writeSelfResult(w http.ResponseWriter, res selfResult) {
	if res.err != nil {
		writeWireError(w, res.status, *res.err)
		return
	}
	_ = json.NewEncoder(w).Encode(res.rec)
}
