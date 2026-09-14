package peers

// The reply path (spec §4.4 "Reply path") and GET /api/peers/log. A
// target Claude Code session answers a delivered message natively, into
// the helper's socket; the helper hands the raw frame to this daemon
// (handleReplyFrame). The daemon resolves the replier from the frame's
// reply address against its own live inventory, finds the return route to
// the helper's bound origin host, and forwards the reply as a fresh
// /deliver — the origin daemon then spawns its own helper for the replier
// and delivers into the origin session. The chain is symmetric.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/store"
)

// Log tail bounds for GET /api/peers/log.
const (
	defaultLogTail = 50
	maxLogTail     = 1000
)

// handleReplyFrame is the helper manager's OnFrame: every frame a
// helper's Claude Code peer wrote into the helper's socket. It runs on the
// helper's pump goroutine and never blocks it indefinitely: it waits for
// one of replyWorkerCap worker slots (back-pressure onto that one helper's
// stdout pipe is acceptable, R2-B1), gives up on Stop, and otherwise hands
// the frame to a worker goroutine joined by Module.Stop. The Add cannot
// race workers.Wait: helpers.Stop joins every pump before Wait runs.
func (m *Module) handleReplyFrame(h *helper, line string) {
	select {
	case m.replySem <- struct{}{}:
	case <-m.stopCtx.Done():
		m.logf("peers: helper %d (%s): reply frame dropped, daemon is stopping", h.pid, h.name)
		return
	}
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer func() { <-m.replySem }()
		m.forwardReply(m.stopCtx, h, line)
	}()
}

// forwardReply is one reply worker. The step order is the contract (Task
// 9): a frame that is not a user message, or does not parse, is logged
// and dropped with no row (it is not a reply); every other refusal is
// audited as a reply row carrying the code, the native msg_id and the
// helper's origin; the insert (step 7) precedes the outbound call (step
// 8), whose every outcome is recorded.
func (m *Module) forwardReply(ctx context.Context, h *helper, line string) {
	// 1. Parse; only user frames are replies.
	frame, err := ccuds.ParseFrame([]byte(strings.TrimSpace(line)))
	if err != nil {
		m.logf("peers: helper %d (%s): reply frame dropped: %v", h.pid, h.name, err)
		return
	}
	if frame.Type != "user" {
		m.logf("peers: helper %d (%s): reply frame dropped: type %q is not user", h.pid, h.name, frame.Type)
		return
	}

	snap := m.configSnapshot()
	row := store.PeerMessage{
		NativeMsgID: frame.MsgID,
		Direction:   store.DirReply,
		FromHostID:  snap.hostID,
		ToHostID:    h.key.HostID,
		ToSessionID: h.key.AgentSessionID,
	}
	// drop audits the refusal as the row itself (no msg_id exists yet for
	// a drop; one is minted so the row is well-formed) and logs it.
	drop := func(code, detail string) {
		row.MsgID = m.newMsgID()
		row.TS = m.now()
		row.Result = code
		row.Error = detail
		m.logf("peers: reply via helper %d (%s) dropped (%s): %s", h.pid, h.name, code, detail)
		if m.audit == nil {
			return
		}
		if _, err := m.audit.Insert(row); err != nil {
			m.logf("peers: reply via helper %d (%s): audit insert: %v", h.pid, h.name, err)
		}
	}

	// 2. The reply address names the replier.
	sock, ok := ccuds.FromSocket(frame.From)
	if !ok {
		drop(ipeers.ErrReplierUnknown, "frame carries no uds: reply address")
		return
	}

	// 3. A helper socket — this daemon's own (by the manager) or another
	// daemon's (a proxy row of the inventory, D9) — never replies through
	// the bridge: a helper is a relay, not a session.
	if _, own := m.helpers.FindBySock(sock); own {
		drop(ipeers.ErrProxyToProxy, "reply address is one of this daemon's helpers")
		return
	}
	env := m.localEnvelope(ctx, snap.hostID, snap.alias)
	if !env.OK {
		drop(ipeers.ErrReplierUnknown, "inventory unavailable: "+env.Error)
		return
	}
	replier, detail := findReplier(env.Peers, sock)
	if detail != "" {
		// A PARTIAL inventory (spec §4.2) with no row for the reply
		// address says nothing about the replier — its tmux session may
		// be the one whose owner lookup did not complete: not_ready, never
		// replier_unknown (a verdict). An alive-but-undecodable registry
		// file (Diagnosis.BlockingUnknown) is the same problem in a
		// stronger form and overrides even a row findReplier DID resolve
		// for this sock (found, but not cc or not deliverable): the file
		// that failed to decode could be the one that would have
		// superseded it, so its presence is not_ready too, never
		// replier_unknown — a positively-identified proxy row (a helper,
		// D9) is unaffected, since that verdict comes from a row that
		// decoded fine. The helper is kept either way.
		code := ipeers.ErrReplierUnknown
		switch {
		case replier.Agent != nil && replier.Agent.Type == "proxy":
			code = ipeers.ErrProxyToProxy
		case len(env.UnknownRegistryFiles) > 0 || (replier.Agent == nil && env.Partial):
			code, detail = ipeers.ErrNotReady, detailInventoryPartial
		}
		drop(code, detail)
		return
	}
	// 4. (found above) The replier is a live, deliverable cc session.
	row.FromSessionID = replier.Agent.SessionID

	// 5. Unwrap: Claude Code wraps its native reply in the same
	// cross-session-message envelope; the text and the harness's own
	// from-mode (D3) and hop-chain come out of it. Bare content is the
	// whole text, prompting, no hop chain.
	text, hop := frame.Message.Content, ""
	declared := ipeers.ModePrompting
	if w, ok := ccuds.Parse(frame.Message.Content); ok {
		text, hop = w.Text, w.HopChain
		if mode, err := ipeers.ValidateMode(w.FromMode); err == nil {
			declared = mode
		}
	}
	row.DeclaredMode = declared
	row.Bytes = len(text)
	if err := ipeers.ValidateText(text); err != nil {
		drop(ipeers.ValidationCode(err), err.Error())
		return
	}

	// 6. The return route: the entry for the helper's origin host, with a
	// verified outbound token.
	entry, found := returnRoute(snap.hosts, h.key.HostID)
	if !found {
		drop(ipeers.ErrNoReturnRoute, "no verified outbound route to host "+h.key.HostID)
		return
	}

	// 7. The outbound request — a fresh msg_id (D11: never the native one,
	// which is kept in native_msg_id for tracing) — validated against the
	// wire contract the origin enforces, then the audit row, written
	// before anything leaves this host.
	dreq := ipeers.DeliverRequest{
		MsgID:    m.newMsgID(),
		HopChain: hop,
		From:     wireFromRecord(snap.hostID, replier, declared),
		To:       ipeers.WireTo{AgentSessionID: h.key.AgentSessionID, PID: h.key.PID, ProcStart: h.key.ProcStart},
		Text:     text,
	}
	if err := dreq.Validate(); err != nil {
		drop(ipeers.ValidationCode(err), "outbound request invalid: "+err.Error())
		return
	}
	if m.audit == nil {
		m.logf("peers: reply %s via helper %d (%s) dropped: audit store is not available", dreq.MsgID, h.pid, h.name)
		return
	}
	row.MsgID = dreq.MsgID
	row.TS = m.now()
	id, err := m.audit.Insert(row)
	if err != nil {
		m.logf("peers: reply %s via helper %d (%s): audit insert: %v", dreq.MsgID, h.pid, h.name, err)
		return
	}

	// 8. The outbound call, under stopCtx: every outcome is recorded.
	postCtx, cancelPost := context.WithTimeout(ctx, ipeers.InterDaemonTimeout)
	defer cancelPost()
	resp, remote, err := m.post(postCtx, m.deliverClient, entry.URL, entry.Token, dreq)
	switch {
	case err != nil:
		text := boundRemoteText(err.Error())
		m.setResult(id, "", "", text)
		m.logf("peers: reply %s to %q: deliver call failed: %s", dreq.MsgID, entry.Alias, text)
	case remote != nil:
		m.setResult(id, "", remote.Error, remote.Detail)
		m.logf("peers: reply %s to %q refused by peer: %d %s: %s", dreq.MsgID, entry.Alias, remote.Status, remote.Error, remote.Detail)
		if remote.Error == ipeers.ErrTargetGone {
			// The origin session is gone (spec §4.5): its helper is reaped,
			// so a late reply can never reach a successor process.
			m.helpers.Release(h, "origin gone")
		}
	default:
		// resp.Result / resp.EffectiveMode are the origin's text, admitted
		// by postDeliver only as values this daemon itself would produce.
		errText := ""
		if resp.OneWay {
			errText = ipeers.ErrNoReturnRoute
		}
		m.setResult(id, resp.EffectiveMode, resp.Result, errText)
		m.helpers.Touch(h.key)
	}
}

// findReplier picks the inventory row whose agent inbox is exactly sock.
// A proxy row (another daemon's helper, D9, or this daemon's own hidden by
// pid) is returned with a detail so the caller can name it proxy_to_proxy;
// any other non-deliverable row, or no row at all, is replier_unknown.
func findReplier(records []ipeers.PeerRecord, sock string) (ipeers.PeerRecord, string) {
	for _, rec := range records {
		a := rec.Agent
		if a == nil || a.Inbox != sock {
			continue
		}
		switch {
		case a.Type == "proxy":
			return rec, "reply address is a peer-proxy helper"
		case a.Type != "cc":
			return rec, "reply address is not a Claude Code session"
		case !rec.Deliverable:
			detail := "replier is not deliverable"
			if rec.Reason != "" {
				detail += ": " + rec.Reason
			}
			return rec, detail
		default:
			return rec, ""
		}
	}
	return ipeers.PeerRecord{}, "no live Claude Code session listens on the reply address"
}

// returnRoute is the configured entry for hostID that carries a verified
// outbound token; false when there is none (no_return_route, D12).
func returnRoute(hosts []config.PeerHost, hostID string) (config.PeerHost, bool) {
	for _, h := range hosts {
		if h.HostID == hostID && h.Token != "" {
			return h, true
		}
	}
	return config.PeerHost{}, false
}

// logTimeLayout is RFC 3339 with milliseconds: how ipeers.LogEntry.TS is
// rendered.
const logTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// toLogEntry renders one audit row as its GET /api/peers/log wire shape
// (ipeers.LogEntry, shared with the CLI).
func toLogEntry(p store.PeerMessage) ipeers.LogEntry {
	return ipeers.LogEntry{
		ID:            p.ID,
		MsgID:         p.MsgID,
		NativeMsgID:   p.NativeMsgID,
		Direction:     p.Direction,
		TS:            p.TS.UTC().Format(logTimeLayout),
		FromHostID:    p.FromHostID,
		FromSessionID: p.FromSessionID,
		ToHostID:      p.ToHostID,
		ToSessionID:   p.ToSessionID,
		DeclaredMode:  p.DeclaredMode,
		EffectiveMode: p.EffectiveMode,
		Bytes:         p.Bytes,
		Result:        p.Result,
		Error:         p.Error,
	}
}

// parseTail reads the tail query parameter: "" ⇒ defaultLogTail; a
// non-negative decimal integer, clamped to maxLogTail; anything else is
// an error.
func parseTail(raw string) (int, error) {
	if raw == "" {
		return defaultLogTail, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, strconv.ErrRange
	}
	if n > maxLogTail {
		n = maxLogTail
	}
	return n, nil
}

// handlePeersLog serves GET /api/peers/log?tail=N (admin only — the
// auth layer's HostRoutePolicy already refuses host principals here;
// enforced again in depth): the newest N audit rows, oldest first.
func (m *Module) handlePeersLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	n, err := parseTail(r.URL.Query().Get("tail"))
	if err != nil {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrBadRequest, Detail: "tail must be a non-negative integer"})
		return
	}
	if m.audit == nil {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrAuditUnavailable, Detail: "audit store is not available"})
		return
	}
	rows, err := m.audit.Tail(n)
	if err != nil {
		m.logf("peers: log: tail %d: %v", n, err)
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrAuditUnavailable, Detail: "audit read failed"})
		return
	}
	out := ipeers.LogResponse{Messages: make([]ipeers.LogEntry, 0, len(rows))}
	for _, p := range rows {
		out.Messages = append(out.Messages, toLogEntry(p))
	}
	_ = json.NewEncoder(w).Encode(out)
}
