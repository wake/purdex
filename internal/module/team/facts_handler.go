// internal/module/team/facts_handler.go
package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// FactsRoute is the cross-host team facts route (spec §6.3): a member host's daemon POSTs one TeamFact.
const FactsRoute = "/api/peers/team/facts"

// maxFactBody is the body cap of a fact (§3.1 rule 8).
const maxFactBody = 64 << 10

// handleTeamFact serves POST /api/peers/team/facts. The order is the contract (spec §6.1, as the commands route): the
// binding of the host principal to its live entry, the per-host rate limit and the body cap BEFORE the body is decoded,
// then shape, to_host_id and kind; then the store decides in one transaction (idempotency by id and content, the row
// change, the log entry). This version applies `ended`, `registered` and `spawn_failed`; the reserved moved is 400
// unsupported_kind (not stored).
func (m *Module) handleTeamFact(w http.ResponseWriter, r *http.Request) {
	var entry config.PeerHost
	var ourHostID string
	principal, _, berr := peersmod.BindHostPrincipal(r, "team facts", func(alias string) (config.PeerHost, bool) {
		var ok bool
		entry, ourHostID, ok = m.peerEntry(alias)
		return entry, ok
	})
	if berr != nil {
		m.logf("[team] fact refused (%s): %s", berr.Code, berr.Detail)
		m.writeCommandErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}
	if m.stopCtx.Err() != nil {
		m.writeCommandErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping")
		return
	}
	var raw json.RawMessage
	switch st := peersmod.AdmitDecode(w, r, m.factLimit, entry.HostID, maxFactBody, &raw); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeCommandErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeCommandErr(w, st, team.ErrCommandBadRequest, "body over 64 KiB")
		return
	default:
		m.writeCommandErr(w, st, team.ErrCommandBadRequest, "invalid JSON body")
		return
	}
	var fact team.TeamFact
	if err := json.Unmarshal(raw, &fact); err != nil {
		m.writeCommandErr(w, http.StatusBadRequest, team.ErrCommandBadRequest, "the body is not a fact")
		return
	}
	if !uuidV4.MatchString(fact.ID) || fact.Kind == "" || fact.TeamID == "" {
		m.writeCommandErr(w, http.StatusBadRequest, team.ErrCommandBadRequest, "id (a UUID v4), kind and team_id are required")
		return
	}
	// wrong_host is a decision of this host about an identified fact (valid id, bound sender): it is stored with the fact's
	// content hash, so the same copy later gets the same answer and another body under the id is id_conflict (rule 3).
	// unsupported_kind is NOT stored: a kind this version does not know may be one a later version applies, and the member
	// host drops a fact on a permanent refusal — a stored refusal would make a resend after the upgrade lose a fact that is
	// true. (Rule 3 says refusals are stored; this is the one deliberate exception, and the member host's own gate on the
	// kinds announced in fact_kinds keeps the case to a stale capability cache.) validateFact (below) stays unstored too.
	var refusalPlan *CommandResult
	if fact.ToHostID != ourHostID {
		r := refusal(http.StatusConflict, team.ErrCommandWrongHost, "this fact is addressed to another host")
		refusalPlan = &r
	} else if !factKindApplied(fact.Kind) {
		m.writeCommandErr(w, http.StatusBadRequest, team.ErrCommandUnsupportedKind, "this host does not apply "+boundText(fact.Kind)+" facts")
		return
	}
	// The binding is the entry's as of now, not as of the bind: the alias may have been re-created for another host.
	fresh, _, ok := m.peerEntry(principal.Alias)
	if !ok || fresh.HostID != entry.HostID {
		m.writeCommandErr(w, http.StatusForbidden, ipeers.ErrHostUnverified, "host entry no longer matches the authenticated host")
		return
	}
	res, err := m.store.ApplyTeamFact(FactPlan{FromHostID: entry.HostID, Body: raw, Now: m.now(), Refusal: refusalPlan, Invalid: validateFact(fact)})
	switch {
	case errors.Is(err, ErrCommandIDConflict):
		m.writeCommandErr(w, http.StatusConflict, team.ErrCommandIDConflict, "the id is already used by a different fact")
		return
	case err != nil:
		m.logf("[team] fact %s %s: %v", fact.Kind, fact.ID, err)
		m.writeCommandErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log")
		return
	}
	if res.Status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.Status)
		_, _ = w.Write(res.Body)
		return
	}
	if !res.Replayed {
		m.rosterChanged() // a seat may have been freed
		if fact.Kind == team.FactRegistered || fact.Kind == team.FactSpawnFailed {
			m.wake(fact.MK) // a `pdx spawn --host` POST is waiting on this op
		}
	}
	m.writeJSON(w, http.StatusOK, team.TeamFactAnswer{ID: fact.ID, HostID: ourHostID, Outcome: res.Body})
}

// factKindApplied: the fact kinds this version applies (the same list the inventory announces as fact_kinds).
func factKindApplied(kind string) bool {
	switch kind {
	case team.FactEnded, team.FactRegistered, team.FactSpawnFailed:
		return true
	}
	return false
}

// validateFact checks that every string is bounded and free of control characters (they end up in rows) and the fields
// each kind needs. "" means valid.
func validateFact(f team.TeamFact) string {
	for _, s := range []string{f.ID, f.Kind, f.ToHostID, f.TeamID, f.MK, f.Reason} {
		if len(s) > maxCommandField || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return "a field is over 256 bytes, not UTF-8, or holds a control character"
		}
	}
	for _, s := range []string{f.MemberSession, f.Ref, f.ProcStart, f.Pane, f.Title} {
		if len(s) > maxCommandField || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return "a field is over 256 bytes, not UTF-8, or holds a control character"
		}
	}
	switch f.Kind {
	case team.FactEnded, team.FactRegistered, team.FactSpawnFailed:
		if f.MK == "" {
			return f.Kind + ": mk is required"
		}
	}
	switch f.Kind {
	case team.FactRegistered:
		if f.MemberSession == "" || f.Ref == "" || f.PID <= 0 || f.ProcStart == "" {
			return "registered: member_session_id, ref, pid and proc_start are required"
		}
	case team.FactSpawnFailed:
		if !spawnReasons[f.Reason] {
			return "spawn_failed: reason is not a known spawn reason"
		}
	}
	return ""
}
