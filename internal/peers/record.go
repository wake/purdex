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
// per live Claude Code registry entry whose tmux field does not point into
// any listed session.
type PeerRecord struct {
	Host         string     `json:"host"`
	HostID       string     `json:"host_id"`
	Address      string     `json:"address"`
	SessionCode  string     `json:"session_code"`  // always present
	SessionName  string     `json:"session_name"`  // always present
	TmuxInstance string     `json:"tmux_instance"` // always present
	Cwd          string     `json:"cwd,omitempty"`
	Agent        *AgentInfo `json:"agent"` // always present, null when none
	Deliverable  bool       `json:"deliverable"`
	Reason       string     `json:"reason"` // always present: "" | no_agent | not_cc | inbox_dead | proxy | ambiguous
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
	ProxyPIDs     map[int]bool // empty in P1; kept so P3 needs no signature change
}

// Build joins sessions, owners and registry entries into PeerRecords. It is
// a pure function: deterministic, no I/O.
func Build(in BuildInput) []PeerRecord {
	entriesBySessionID := make(map[string][]Entry, len(in.Entries))
	for _, e := range in.Entries {
		entriesBySessionID[e.SessionID] = append(entriesBySessionID[e.SessionID], e)
	}

	sessionNames := make(map[string]bool, len(in.Sessions))
	for _, s := range in.Sessions {
		sessionNames[s.Name] = true
	}

	records := make([]PeerRecord, 0, len(in.Sessions)+len(in.Entries))

	for _, s := range in.Sessions {
		records = append(records, buildSessionRecord(in, s, entriesBySessionID))
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].SessionName != records[j].SessionName {
			return records[i].SessionName < records[j].SessionName
		}
		return records[i].SessionCode < records[j].SessionCode
	})

	outside := buildOutsideRecords(in, sessionNames)
	records = append(records, outside...)

	return records
}

// buildSessionRecord implements rules 2-4 for one tmux session.
func buildSessionRecord(in BuildInput, s SessionSummary, entriesBySessionID map[string][]Entry) PeerRecord {
	rec := PeerRecord{
		Host:         in.Alias,
		HostID:       in.HostID,
		Address:      in.Alias + "/" + s.Name,
		SessionCode:  s.Code,
		SessionName:  s.Name,
		TmuxInstance: s.TmuxInstance,
		Cwd:          s.Cwd,
	}

	if in.Unresolved[s.Code] {
		return rec // Agent nil, Reason "", Deliverable false
	}

	owner, hasOwner := in.Owners[s.Code]
	if !hasOwner {
		rec.Reason = "no_agent"
		return rec
	}

	if owner.AgentType != "cc" {
		rec.Agent = &AgentInfo{
			Type:      owner.AgentType,
			SessionID: owner.SessionID,
			Status:    owner.Status,
			Version:   "",
		}
		rec.Reason = "not_cc"
		return rec
	}

	var candidates []Entry
	for _, e := range entriesBySessionID[owner.SessionID] {
		if !in.ProxyPIDs[e.PID] {
			candidates = append(candidates, e)
		}
	}

	switch len(candidates) {
	case 0:
		rec.Agent = ownerFallbackAgent(owner)
		rec.Reason = "inbox_dead"
	case 1:
		rec.Agent = agentInfoFromEntry(candidates[0])
		rec.Deliverable = true
	default:
		var paneMatches []Entry
		for _, c := range candidates {
			if c.TmuxPaneID() == owner.TmuxPaneID {
				paneMatches = append(paneMatches, c)
			}
		}
		if len(paneMatches) == 1 {
			rec.Agent = agentInfoFromEntry(paneMatches[0])
			rec.Deliverable = true
		} else {
			rec.Agent = ownerFallbackAgent(owner)
			rec.Reason = "ambiguous"
		}
	}

	return rec
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

// outsideCandidate pairs a built outside-tmux record with the sort keys
// (rule 5: sorted by PeerName, ties by PID) that live on the source entry
// rather than the record itself.
type outsideCandidate struct {
	rec      PeerRecord
	peerName string
	pid      int
}

// buildOutsideRecords implements rule 5: every live entry whose tmux
// session name is not a listed session gets its own row, regardless of
// whether rule 4 also used it.
func buildOutsideRecords(in BuildInput, sessionNames map[string]bool) []PeerRecord {
	var candidates []outsideCandidate
	for _, e := range in.Entries {
		if sessionNames[e.TmuxSessionName()] {
			continue
		}

		agent := agentInfoFromEntry(e)
		rec := PeerRecord{
			Host:        in.Alias,
			HostID:      in.HostID,
			Address:     in.Alias + "/cc:" + e.Name,
			Cwd:         e.Cwd,
			Agent:       agent,
			Deliverable: true,
		}
		if in.ProxyPIDs[e.PID] {
			agent.Type = "proxy"
			rec.Deliverable = false
			rec.Reason = "proxy"
		}

		candidates = append(candidates, outsideCandidate{rec: rec, peerName: e.Name, pid: e.PID})
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
