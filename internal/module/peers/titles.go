package peers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// TitleStore is the peer_labels table (Peer Address v2 spec §3.3):
// *store.PeerLabelStore in production, a fake in tests. nil means "no
// store": every conversation shows an empty title and claims fail with
// store_unavailable. Addresses are unaffected either way (spec §4.5).
//
// The table keeps its v2 name on disk — renaming it would be a migration,
// and alpha carries none (v4 spec §6). Only the Go vocabulary moved.
type TitleStore interface {
	Snapshot() ([]store.PeerLabel, error)
	Claim(sessionID, title string, now time.Time) (store.PeerLabel, error)
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

// titleRows indexes a store snapshot by session id.
func titleRows(rows []store.PeerLabel) map[string]store.PeerLabel {
	out := make(map[string]store.PeerLabel, len(rows))
	for _, r := range rows {
		out[r.SessionID] = r
	}
	return out
}

// infoOf converts a (possibly absent) store row into ipeers.TitleInfo:
// absent ⇒ the zero value, which EntryRecord/applyIdentity render as no
// title at all — an empty Title with an empty TitleSource (spec §4.5).
func infoOf(row store.PeerLabel, ok bool) ipeers.TitleInfo {
	if !ok {
		return ipeers.TitleInfo{}
	}
	return ipeers.TitleInfo{Title: row.Label, Rev: row.Rev}
}

// selfResult is the outcome of one whoami/claim/release call: exactly one
// of rec (status 200) or err (any other status) is set. warn rides along
// with rec — it qualifies a success, never replaces one — and is nil
// unless the verb had something to report (spec §6.3).
type selfResult struct {
	rec    ipeers.PeerRecord
	warn   *ipeers.SelfWarning
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
//
// It hands back no registry Diagnosis, because no verb branches on one any
// more. The last reader was claim's BlockingUnknown() gate, which had to
// prove a title free before granting it; under D5 a title never has to be
// free (spec §4.2), so the diagnosis had nothing left to decide.
func (m *Module) origin(inbox string) (entries []ipeers.Entry, proxies map[int]bool, e ipeers.Entry, res selfResult, ok bool) {
	if inbox == "" {
		return nil, nil, ipeers.Entry{}, fail(http.StatusBadRequest, ipeers.ErrOriginUnknown, "origin_inbox is empty (CLAUDE_CODE_MESSAGING_SOCKET unset?)"), false
	}
	if m.titles == nil {
		return nil, nil, ipeers.Entry{}, fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "title store is not available"), false
	}
	entries, _, err := ipeers.ReadRegistry(m.registryDir, m.liveness)
	if err != nil {
		return nil, nil, ipeers.Entry{}, fail(http.StatusServiceUnavailable, ipeers.ErrNotReady, "registry read failed: "+err.Error()), false
	}
	proxies = m.proxyPIDs()
	e, found := findOriginEntry(entries, proxies, inbox)
	if !found {
		return nil, nil, ipeers.Entry{}, fail(http.StatusBadRequest, ipeers.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host"), false
	}
	return entries, proxies, e, selfResult{}, true
}

// whoami answers the caller's own current record: its address, title and
// title source, read straight from the validated registry entry and the
// title store — no write, so a failing store only degrades this to
// store_unavailable rather than reporting "no title" as fact when it
// simply could not look (spec §3.6).
func (m *Module) whoami(inbox string) selfResult {
	m.titleMu.Lock()
	defer m.titleMu.Unlock()
	_, _, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	rows, err := m.titles.Snapshot()
	if err != nil {
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "title store read failed")
	}
	snap := m.configSnapshot()
	row, has := titleRows(rows)[e.SessionID]
	// The entry origin() validated is all whoami needs: the address is
	// RefID of that entry's own sessionId, so this answer is
	// identical to the listing's by construction (spec §4.5) rather than by
	// resolving over the same population.
	return selfResult{status: http.StatusOK, rec: ipeers.EntryRecord(snap.alias, snap.hostID, e, false, infoOf(row, has))}
}

// claim gives the caller's own conversation a title. A title another live
// session already holds is granted anyway, with a title_in_use warning
// naming the other holders (spec D5): a title is a display name, nothing
// resolves or routes on it, so it has no reason to be unique. The
// serial-number convention survives as a convention, prompted at the
// moment of the collision by the warning's live_titles rather than
// enforced by a refusal.
//
// Two v2 behaviours went with that guarantee:
//
//   - the 409 label_taken refusal, now the warning above;
//   - the BlockingUnknown() gate, which made an undecodable registry file
//     a 503 because a title "could not be proven free" (spec §4.2). Under
//     D5 a title never has to be free, so the proof has nothing left to
//     establish and waiting on it would only fail a path that has nothing
//     to lose by proceeding.
//
// The path is now: title grammar → store nil → registry read error ⇒
// not_ready → origin unknown → store read error ⇒ store_unavailable →
// compute the warning over the state the write will leave behind →
// already ours ⇒ 200 no write → write (error ⇒ store_unavailable) → 200.
//
// The reserved-word refusal is gone with the grammar: a title reaches
// nothing, so "cc" and "tmux" have nothing to shadow. The wire code stays
// ErrCodeLabelInvalid because the route it answers is still
// /api/peers/self/label — the v4 rename moved the Go vocabulary and the
// peer-row JSON keys, not this route or its request body (spec §6).
func (m *Module) claim(inbox, title string) selfResult {
	if err := ipeers.ValidateTitle(title); err != nil {
		return fail(http.StatusBadRequest, ipeers.ErrCodeLabelInvalid, err.Error())
	}
	m.titleMu.Lock()
	defer m.titleMu.Unlock()
	entries, proxies, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	rows, err := m.titles.Snapshot()
	if err != nil {
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "title store read failed")
	}

	// liveEntry maps every live, non-proxy entry's session id to that
	// entry (first seen), so another holder of the title can be rendered
	// with EntryRecord from the entry already in hand, never a fresh
	// registry read.
	liveEntry := map[string]ipeers.Entry{}
	for _, le := range entries {
		if le.IsProxy || proxies[le.PID] {
			continue
		}
		if _, seen := liveEntry[le.SessionID]; !seen {
			liveEntry[le.SessionID] = le
		}
	}

	// liveTitles collects every title held by a live session — the
	// caller's own included, per spec §3.3 — for the warning body, and it
	// describes the state this claim PRODUCES, not the one it found. The
	// caller contributes `title`, whatever its row says now: by the time
	// anyone reads the warning the write below has happened, and any title
	// it held before is gone. Read off the untouched snapshot instead, the
	// envelope would announce one state in `peer` and hand over a list that
	// does not contain it — and live_titles exists precisely so an agent
	// can pick the next free serial in ONE step (spec §4.2, §8), so a stale
	// entry makes it skip a serial this very call just freed. Nothing else
	// in the snapshot moves: a claim rewrites exactly one row, the
	// caller's own.
	//
	// others are the live rows holding the requested title that are NOT the
	// caller — unaffected by the write, and still "the other holders"
	// rather than "everyone on this title". own is the caller's own row
	// when it already holds the title: the one case with no write, and the
	// one where the contribution above is the title it already had.
	//
	// The HOLDER compare runs through NormalizeTitle on both sides (v4
	// spec §6): "Purdex Tester" and "purdex  tester" are the same name to
	// the person reading them, so an exact compare would miss exactly the
	// near-duplicates the warning exists for. Only the compare normalises —
	// what is stored and shown is always the title as it was written.
	//
	// The caller's OWN row stays on an exact compare, because it decides
	// whether to write at all: normalising it would swallow a re-cased or
	// re-spaced rename of one's own title, leaving the store holding a
	// string the caller did not ask for.
	var liveTitles []string
	var others []store.PeerLabel
	var own *store.PeerLabel
	want := ipeers.NormalizeTitle(title)
	for i := range rows {
		row := &rows[i]
		if _, live := liveEntry[row.SessionID]; !live {
			continue
		}
		if row.SessionID == e.SessionID {
			if row.Label == title {
				own = row
			}
			continue
		}
		if row.Label == "" {
			continue
		}
		liveTitles = append(liveTitles, row.Label)
		if ipeers.NormalizeTitle(row.Label) == want {
			others = append(others, *row)
		}
	}
	liveTitles = append(liveTitles, title)
	sort.Strings(liveTitles)

	snap := m.configSnapshot()
	var warn *ipeers.SelfWarning
	if len(others) > 0 {
		holders := make([]ipeers.PeerRecord, 0, len(others))
		for _, o := range others {
			he := liveEntry[o.SessionID]
			holders = append(holders, ipeers.EntryRecord(snap.alias, snap.hostID, he, false, ipeers.TitleInfo{Title: o.Label, Rev: o.Rev}))
		}
		warn = &ipeers.SelfWarning{
			Code:       ipeers.WarnTitleInUse,
			Detail:     inUseDetail(title, len(others)),
			Holders:    holders,
			LiveTitles: liveTitles,
		}
	}

	var row store.PeerLabel
	if own != nil {
		// Already ours: no write, revision unchanged.
		row = *own
	} else {
		row, err = m.titles.Claim(e.SessionID, title, m.now())
		if err != nil {
			m.logf("peers: claim %q for %s: %v", title, e.SessionID, err)
			return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "title store write failed")
		}
	}
	return selfResult{
		status: http.StatusOK,
		rec:    ipeers.EntryRecord(snap.alias, snap.hostID, e, false, ipeers.TitleInfo{Title: row.Label, Rev: row.Rev}),
		warn:   warn,
	}
}

// inUseDetail is the title_in_use warning's sentence. It says "also", and
// says it about the other holders rather than about the caller, because
// the caller's own claim went through: this is a report on the company it
// is now keeping, not a report on a failure.
func inUseDetail(title string, others int) string {
	plural := ""
	if others > 1 {
		plural = "s"
	}
	return fmt.Sprintf("%q is also held by %d other live session%s", title, others, plural)
}

// release clears the caller's own title, leaving the conversation unnamed
// — its address is unaffected, because the address never came from the
// title (spec §4.5). No registry completeness requirement (v2 §3.3): a
// registry file this daemon cannot classify never blocks releasing your
// own title, only claiming one you might not be free to take.
//
// It still reads the title store before writing, and that read is still a
// gate: it fails with store_unavailable and attempts NO write, so an
// unreadable store leaves the title exactly where it was rather than
// half-releasing it. What the read no longer has to do is resolve the
// default the response should show — there is no default any more.
func (m *Module) release(inbox string) selfResult {
	m.titleMu.Lock()
	defer m.titleMu.Unlock()
	_, _, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	if _, err := m.titles.Snapshot(); err != nil {
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "title store read failed")
	}
	row, had, err := m.titles.Release(e.SessionID, m.now())
	if err != nil {
		m.logf("peers: release for %s: %v", e.SessionID, err)
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "title store write failed")
	}
	info := ipeers.TitleInfo{}
	if had {
		info.Rev = row.Rev
	}
	snap := m.configSnapshot()
	return selfResult{status: http.StatusOK, rec: ipeers.EntryRecord(snap.alias, snap.hostID, e, false, info)}
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
// ipeers.SelfResponse envelope on success (spec §6.3 — the record used to
// go out bare, with nowhere to carry a warning). Always called after
// titleMu is released — the lock covers the registry read, the store write
// and the construction of res itself, never the encode.
func writeSelfResult(w http.ResponseWriter, res selfResult) {
	if res.err != nil {
		writeWireError(w, res.status, *res.err)
		return
	}
	_ = json.NewEncoder(w).Encode(ipeers.SelfResponse{Peer: res.rec, Warning: res.warn})
}
