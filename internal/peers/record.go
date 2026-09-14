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
	Host         string     `json:"host"`
	HostID       string     `json:"host_id"`
	Address      string     `json:"address"`
	RowKind      string     `json:"row_kind"`     // session | entry
	Label        string     `json:"label"`        // "" when the row has no cc agent
	LabelSource  string     `json:"label_source"` // user | default | ""
	LabelRev     int64      `json:"label_rev"`
	Suffix       string     `json:"suffix"`        // "" when the row has no cc agent
	SessionCode  string     `json:"session_code"`  // always present
	SessionName  string     `json:"session_name"`  // always present
	TmuxInstance string     `json:"tmux_instance"` // always present
	Cwd          string     `json:"cwd,omitempty"`
	Agent        *AgentInfo `json:"agent"` // always present, null when none
	Deliverable  bool       `json:"deliverable"`
	Reason       string     `json:"reason"` // always present: "" | no_agent | not_cc | inbox_dead | proxy | ambiguous
}

// LabelInfo is what the label store (Task 3) knows about one conversation:
// the user label ("" ⇒ the default label applies) and the label row's
// revision.
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
	Labels        map[string]LabelInfo // by sessionId; absent ⇒ default label, rev 0
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
// entry never also produces a cc: row.
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
	// calls applyLabel to render Label/Suffix/Address (spec §3.4): the
	// fallback branches (inbox_dead / ambiguous, no entry chosen) derive
	// the suffix from the SESSION's own tmux name, never a candidate's,
	// since no entry was chosen for the row.
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
		applyLabel(&rec, in.Alias, in.Labels[owner.SessionID], owner.SessionID, s.Name, "")
		return rec, Entry{}, false
	case 1:
		if consumed[candidates[0]] {
			rec.Agent = ownerFallbackAgent(owner)
			rec.Reason = "ambiguous"
			applyLabel(&rec, in.Alias, in.Labels[owner.SessionID], owner.SessionID, s.Name, "")
			return rec, Entry{}, false
		}
		rec.Agent = agentInfoFromEntry(candidates[0])
		rec.Deliverable = true
		applyLabel(&rec, in.Alias, in.Labels[owner.SessionID], owner.SessionID, candidates[0].TmuxSessionName(), candidates[0].Name)
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
				applyLabel(&rec, in.Alias, in.Labels[owner.SessionID], owner.SessionID, s.Name, "")
				return rec, Entry{}, false
			}
			rec.Agent = agentInfoFromEntry(paneMatches[0])
			rec.Deliverable = true
			applyLabel(&rec, in.Alias, in.Labels[owner.SessionID], owner.SessionID, paneMatches[0].TmuxSessionName(), paneMatches[0].Name)
			return rec, paneMatches[0], true
		}
		rec.Agent = ownerFallbackAgent(owner)
		rec.Reason = "ambiguous"
		applyLabel(&rec, in.Alias, in.Labels[owner.SessionID], owner.SessionID, s.Name, "")
		return rec, Entry{}, false
	}
}

// applyLabel fills Label/LabelSource/LabelRev/Suffix/Address for a row
// whose agent is a cc conversation sid (session rows and entry rows
// alike): info.Label ("" ⇒ default) decides Label/LabelSource, info.Rev
// is carried straight through, and Suffix/Address are rendered from
// tmuxName/ccName (spec §3.1/§3.4) — the plan-recorded delta that the
// suffix always comes from the row's OWN registry tmux field (or, for the
// owner-fallback session row with no entry, the session's own name), never
// from Resolve's session argument.
func applyLabel(rec *PeerRecord, alias string, info LabelInfo, sid, tmuxName, ccName string) {
	if info.Label != "" {
		rec.Label, rec.LabelSource = info.Label, LabelSourceUser
	} else {
		rec.Label, rec.LabelSource = DefaultLabel(sid), LabelSourceDefault
	}
	rec.LabelRev = info.Rev
	rec.Suffix = Suffix(tmuxName, ccName)
	rec.Address = alias + "/" + rec.Label + ":" + rec.Suffix
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
// live cc entry gets the same label+suffix address applyLabel gives a
// session row, derived from the entry's own tmux field.
func EntryRecord(alias, hostID string, e Entry, proxy bool, info LabelInfo) PeerRecord {
	agent := agentInfoFromEntry(e)
	rec := PeerRecord{
		Host: alias, HostID: hostID, RowKind: "entry",
		Cwd: e.Cwd, Agent: agent, Deliverable: true,
	}
	if proxy {
		agent.Type = "proxy"
		agent.Status = "proxy"
		rec.Address = alias + "/cc:" + e.Name
		rec.Deliverable = false
		rec.Reason = "proxy"
		return rec
	}
	applyLabel(&rec, alias, info, e.SessionID, e.TmuxSessionName(), e.Name)
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
