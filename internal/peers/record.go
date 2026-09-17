package peers

import "sort"

// SessionSummary is one tmux session as the daemon's inventory describes it.
type SessionSummary struct {
	Code, Name, Cwd, TmuxInstance string
}

// Owner is the agent module's resolved owner of a session (the most
// recently active root agent frame among the session's panes).
type Owner struct {
	AgentType, SessionID, Cwd, TmuxPaneID string
	LastSeenAt                            int64
	Status                                string // Purdex agent status of the owning frame (Task 4)
}

// AgentInfo describes the agent that owns a peer row, when known.
type AgentInfo struct {
	Type      string `json:"type"` // cc | codex | opencode | proxy
	SessionID string `json:"session_id,omitempty"`
	PeerName  string `json:"peer_name,omitempty"`
	PID       int    `json:"pid,omitempty"`
	ProcStart string `json:"proc_start,omitempty"`
	Inbox     string `json:"inbox,omitempty"`
	Status    string `json:"status,omitempty"`
	Version   string `json:"version"` // ALWAYS present; "" when unknown (spec §4.2)
}

// PeerRecord is one row of GET /api/peers: one per tmux session, plus one
// per live Claude Code registry entry that no session row consumed —
// whether or not that entry's tmux field points into a listed session
// (Peer Address v2 spec §3.4; RowKind tells the two apart).
type PeerRecord struct {
	Host    string `json:"host"`
	HostID  string `json:"host_id"`
	Address string `json:"address"`
	RowKind string `json:"row_kind"` // session | entry
	// Ref is the sessionId-derived disambiguator, "_q34psn"; "" when the row
	// has no cc agent. It is the one part of an address that cannot drift, and
	// what Resolve falls back to when a name does.
	Ref          string `json:"ref"`
	Label        string `json:"label"`        // self-declared display name; "" until one is set, and never routed on (spec §4.5)
	LabelSource  string `json:"label_source"` // user | ""
	LabelRev     int64  `json:"label_rev"`
	SessionCode  string `json:"session_code"`  // always present
	SessionName  string `json:"session_name"`  // always present
	TmuxInstance string `json:"tmux_instance"` // always present
	// TmuxName is the tmux session this row's agent is in, for display only.
	// NOTHING routes on it, and that is the point: its two provenances differ
	// in how much they can be trusted, and RowKind tells them apart.
	//
	//   - row_kind "session": the daemon's live inventory name, so it tracks a
	//     rename immediately.
	//   - row_kind "entry": no session row stands behind it, so this is Claude
	//     Code's registry field, frozen when the agent started and never
	//     refreshed. It can name a session since renamed or gone — which is
	//     exactly v3 spec §2's P1, and exactly why SessionName is NOT set here
	//     and no tier may match on this field.
	//
	// "" when the agent is not in tmux at all.
	TmuxName    string     `json:"tmux_name"`
	Cwd         string     `json:"cwd,omitempty"`
	Agent       *AgentInfo `json:"agent"` // always present, null when none
	Deliverable bool       `json:"deliverable"`
	Reason      string     `json:"reason"` // always present: "" | no_agent | not_cc | inbox_dead | proxy | ambiguous
}

// WireAddress renders r's from.address: the bare Ref, or "" when the row has
// no cc agent (Ref == "").
//
// It is ref-based rather than name- or label-based because send.go's
// wireFromRecord puts this string in the outbound from.address: a name-based
// rendering would announce the sender at an address that drifts the moment
// Claude Code re-derives the registry name, and a label-based one at an
// address nothing routes on at all. The ref is the one half of the address
// that cannot move.
func (r PeerRecord) WireAddress() string {
	return r.Ref
}

// LabelInfo is what the label store (Task 3) knows about one conversation:
// the user label ("" ⇒ the conversation has not named itself, and nothing
// is substituted for it) and the label row's revision.
type LabelInfo struct {
	Label string
	Rev   int64
}

// BuildInput is everything the pure join needs: the tmux inventory, the
// agent module's owner resolution, the live Claude Code registry, and the
// set of pids the daemon recognises as its own proxy helpers.
type BuildInput struct {
	HostID, Alias string
	Sessions      []SessionSummary
	Owners        map[string]Owner // by session code; absent ⇒ no owner
	Unresolved    map[string]bool  // codes whose owner lookup did not run
	Entries       []Entry
	ProxyPIDs     map[int]bool         // empty in P1; kept so P3 needs no signature change
	Labels        map[string]LabelInfo // by sessionId; absent ⇒ no label, rev 0
	// LabelsUnavailable says the label snapshot in Labels could NOT be
	// read. Under v3 that costs display only: Labels feeds Label and
	// LabelRev, and nothing else. Every address is built from the registry
	// name and RefID(sessionID) — never from this store — so an
	// unreadable label store cannot make a single address wrong, late or
	// ambiguous. The rows simply render without their display names.
	//
	// (Under v2 this flag gated address correctness: a default label was
	// minted from the tmux session name and resolved over the label rows,
	// so an unreadable row could hold the very name another row was about
	// to advertise. D2 removed that input, and Build no longer branches on
	// this field at all. It is kept because it rides on the wire envelope,
	// where it still tells a consumer why the label column is blank.)
	LabelsUnavailable bool
}

// Build joins sessions, owners and registry entries into PeerRecords. It is
// a pure function: deterministic, no I/O.
func Build(in BuildInput) []PeerRecord {
	entriesBySessionID := make(map[string][]Entry, len(in.Entries))
	for _, e := range in.Entries {
		entriesBySessionID[e.SessionID] = append(entriesBySessionID[e.SessionID], e)
	}

	records := make([]PeerRecord, 0, len(in.Sessions)+len(in.Entries))
	consumed := make(map[Entry]bool)

	for _, s := range in.Sessions {
		rec, entry, ok := buildSessionRecord(in, s, entriesBySessionID, consumed)
		records = append(records, rec)
		if ok {
			consumed[entry] = true
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].SessionName != records[j].SessionName {
			return records[i].SessionName < records[j].SessionName
		}
		return records[i].SessionCode < records[j].SessionCode
	})

	entryRecords := buildEntryRecords(in, consumed)
	records = append(records, entryRecords...)

	return records
}

// buildSessionRecord implements rules 2-4 for one tmux session. When rule 4
// resolves the session to exactly one live entry (the single candidate, or
// the unique pane-tiebreak winner), it returns that Entry with ok=true so
// Build can exclude it from the outside-tmux rows (rule 5): a consumed
// entry never also produces an entry row.
//
// consumed is the set of entries already claimed by an EARLIER session row
// (Build calls this once per session in list order, growing consumed as it
// goes). If the entry this call would otherwise select is already in
// consumed — two sessions whose owners resolved to the same SessionID, e.g.
// a pane that moved mid-request — it is NOT selected here: the row falls
// back to the owner-only agent with Deliverable=false, Reason="ambiguous",
// exactly as when no unique candidate exists at all. This guarantees an
// entry is consumed by at most one session row, ever, regardless of how
// many sessions' owners happen to name it.
func buildSessionRecord(in BuildInput, s SessionSummary, entriesBySessionID map[string][]Entry, consumed map[Entry]bool) (rec PeerRecord, consumedEntry Entry, ok bool) {
	rec = PeerRecord{
		Host:         in.Alias,
		HostID:       in.HostID,
		Address:      in.Alias + "/tmux:" + s.Name,
		RowKind:      "session",
		SessionCode:  s.Code,
		SessionName:  s.Name,
		TmuxName:     s.Name,
		TmuxInstance: s.TmuxInstance,
		Cwd:          s.Cwd,
	}

	if in.Unresolved[s.Code] {
		return rec, Entry{}, false // Agent nil, Reason "", Deliverable false
	}

	owner, hasOwner := in.Owners[s.Code]
	if !hasOwner {
		rec.Reason = "no_agent"
		return rec, Entry{}, false
	}

	if owner.AgentType != "cc" {
		rec.Agent = &AgentInfo{
			Type:      owner.AgentType,
			SessionID: owner.SessionID,
			Status:    owner.Status,
			Version:   "",
		}
		rec.Reason = "not_cc"
		return rec, Entry{}, false
	}

	// From here the owner IS a cc conversation, so every remaining branch
	// sets a cc Agent (full entry info, or the owner-only fallback) and
	// calls applyIdentity to render Ref/Label/Address (v4 spec §5.3).
	//
	// Every branch, fallbacks included, passes RefID(owner.SessionID) with no
	// special case and no population to consult: the ref is a pure function of
	// the conversation's own id, so an inbox_dead row, an ambiguous row and a
	// deliverable row all carry the same ref for the same conversation by
	// construction rather than by agreeing on a shared lookup.
	//
	// What DOES differ between them is the registry name. Only a branch that
	// pinned the row to exactly one live entry has one to pass; the fallbacks
	// pass "", which is why their addresses take the ref form (§5.2).

	var candidates []Entry
	for _, e := range entriesBySessionID[owner.SessionID] {
		if !(e.IsProxy || in.ProxyPIDs[e.PID]) {
			candidates = append(candidates, e)
		}
	}

	switch len(candidates) {
	case 0:
		rec.Agent = ownerFallbackAgent(owner)
		rec.Reason = "inbox_dead"
		applyIdentity(&rec, in.Alias, in.Labels[owner.SessionID], RefID(owner.SessionID), "")
		return rec, Entry{}, false
	case 1:
		if consumed[candidates[0]] {
			rec.Agent = ownerFallbackAgent(owner)
			rec.Reason = "ambiguous"
			applyIdentity(&rec, in.Alias, in.Labels[owner.SessionID], RefID(owner.SessionID), "")
			return rec, Entry{}, false
		}
		rec.Agent = agentInfoFromEntry(candidates[0])
		rec.Deliverable = true
		applyIdentity(&rec, in.Alias, in.Labels[owner.SessionID], RefID(owner.SessionID), candidates[0].Name)
		return rec, candidates[0], true
	default:
		var paneMatches []Entry
		for _, c := range candidates {
			if c.TmuxPaneID() == owner.TmuxPaneID {
				paneMatches = append(paneMatches, c)
			}
		}
		if len(paneMatches) == 1 {
			if consumed[paneMatches[0]] {
				rec.Agent = ownerFallbackAgent(owner)
				rec.Reason = "ambiguous"
				applyIdentity(&rec, in.Alias, in.Labels[owner.SessionID], RefID(owner.SessionID), "")
				return rec, Entry{}, false
			}
			rec.Agent = agentInfoFromEntry(paneMatches[0])
			rec.Deliverable = true
			applyIdentity(&rec, in.Alias, in.Labels[owner.SessionID], RefID(owner.SessionID), paneMatches[0].Name)
			return rec, paneMatches[0], true
		}
		rec.Agent = ownerFallbackAgent(owner)
		rec.Reason = "ambiguous"
		applyIdentity(&rec, in.Alias, in.Labels[owner.SessionID], RefID(owner.SessionID), "")
		return rec, Entry{}, false
	}
}

// applyIdentity fills Ref/Label/LabelSource/LabelRev/Address/Reason for a row
// whose agent is a cc conversation. It is the single writer of those fields,
// which is what makes the spec's invariant table checkable in one place.
//
// The address has two forms and RoutableName picks between them (v4 §5.2): a
// routable registry name gives "<host>/<name>", anything else gives
// "<host>/<ref>" and says why in Reason. A row is never left holding an
// address that cannot be typed back in.
//
// ccName is the registry name. The owner-fallback callers pass "" — no live
// entry stands behind those rows, so they have no name to offer, and the ref
// form matches their being unreachable anyway. "" is not a Reason-worthy
// event for them, so only a non-empty unroutable name sets one.
//
// info.Rev is carried straight through: it is still the LABEL's revision. It
// does not imply an address change.
func applyIdentity(rec *PeerRecord, alias string, info LabelInfo, ref, ccName string) {
	rec.Ref = ref
	if info.Label != "" {
		rec.Label, rec.LabelSource = info.Label, LabelSourceUser
	} else {
		rec.Label, rec.LabelSource = "", ""
	}
	rec.LabelRev = info.Rev
	if RoutableName(ccName) {
		rec.Address = alias + "/" + ccName
		return
	}
	rec.Address = alias + "/" + ref
	if ccName != "" && rec.Reason == "" {
		rec.Reason = "name_unroutable"
	}
}

// ownerFallbackAgent builds the reduced AgentInfo used when a cc owner's
// session cannot be pinned to exactly one live registry entry
// (inbox_dead / ambiguous).
func ownerFallbackAgent(owner Owner) *AgentInfo {
	return &AgentInfo{
		Type:      "cc",
		SessionID: owner.SessionID,
		Status:    owner.Status,
		Version:   "",
	}
}

// agentInfoFromEntry builds the full AgentInfo for a resolved live entry.
func agentInfoFromEntry(e Entry) *AgentInfo {
	return &AgentInfo{
		Type:      "cc",
		SessionID: e.SessionID,
		PeerName:  e.Name,
		PID:       e.PID,
		ProcStart: e.ProcStart,
		Inbox:     e.Inbox,
		Status:    e.Status,
		Version:   e.Version,
	}
}

// EntryRecord is the row of one live registry entry that no session row
// consumed (Peer Address v2 spec §3.4). Task 7 also uses it to answer
// whoami/claim/release straight from the validated entry and label row, so
// the address it renders must be identical to the listing's: a proxy entry
// keeps the unresolvable "alias/cc:<name>" form (unchanged from rule 5); a
// live cc entry gets the same address applyIdentity gives a session row,
// derived from the entry's own registry name and sessionId.
//
// It takes no population argument: the ref is RefID(e.SessionID) and the name
// is the entry's own, so a self route answering from one validated entry and
// the listing building from the whole registry cannot disagree about the
// caller's own address (v4 spec §5.3). That agreement used to require passing
// the same resolved default-label map to both.
func EntryRecord(alias, hostID string, e Entry, proxy bool, info LabelInfo) PeerRecord {
	agent := agentInfoFromEntry(e)
	rec := PeerRecord{
		Host: alias, HostID: hostID, RowKind: "entry",
		TmuxName: e.TmuxSessionName(),
		Cwd:      e.Cwd, Agent: agent, Deliverable: true,
	}
	if proxy {
		agent.Type = "proxy"
		agent.Status = "proxy"
		rec.Address = alias + "/cc:" + e.Name
		rec.Deliverable = false
		rec.Reason = "proxy"
		return rec
	}
	applyIdentity(&rec, alias, info, RefID(e.SessionID), e.Name)
	return rec
}

// entryCandidate pairs a built entry record with the sort keys (sorted by
// PeerName, ties by PID) that live on the source entry rather than the
// record itself.
type entryCandidate struct {
	rec      PeerRecord
	peerName string
	pid      int
}

// buildEntryRecords implements the entry-row rule (Peer Address v2 spec
// §3.4, plan delta over rule 5): EVERY live entry no session row already
// consumed (as the single candidate, or the unique pane-tiebreak winner,
// for some session) gets its own row — whether or not its tmux field
// points into a listed session; a proxy entry's row is EntryRecord's
// unresolvable "cc:<name>" form. A consumed entry never also produces an
// entry row, so each entry appears exactly once across the whole output.
func buildEntryRecords(in BuildInput, consumed map[Entry]bool) []PeerRecord {
	var candidates []entryCandidate
	for _, e := range in.Entries {
		if consumed[e] {
			continue
		}
		rec := EntryRecord(in.Alias, in.HostID, e, e.IsProxy || in.ProxyPIDs[e.PID], in.Labels[e.SessionID])
		candidates = append(candidates, entryCandidate{rec: rec, peerName: e.Name, pid: e.PID})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].peerName != candidates[j].peerName {
			return candidates[i].peerName < candidates[j].peerName
		}
		return candidates[i].pid < candidates[j].pid
	})

	out := make([]PeerRecord, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.rec)
	}
	return out
}
