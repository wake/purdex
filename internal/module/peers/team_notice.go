// internal/module/peers/team_notice.go
package peers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// TeamNoticeKey is the registry key of the team notice seam.
const TeamNoticeKey = "peers.team-notice"

// maxTeamNoticeBytes bounds a notice's text: M's own fixed templates are far smaller.
const maxTeamNoticeBytes = 4096

// TeamNoticeLead is the lead as the member host recorded it from the lead host's commands (cross-host team spec §5.1).
type TeamNoticeLead struct {
	SessionID string
	Ref       string // "_xxxxxx"
	Address   string // "<its alias>/<name>"; only the part after the first "/" names the helper
	PID       int
	ProcStart string
}

// TeamNotice is one message M's daemon delivers to one of its own sessions AS the remote lead (spec §4.4): the text is
// M's own fixed template, never the lead host's free text.
type TeamNotice struct {
	MsgID      string
	LeadHostID string
	Lead       TeamNoticeLead
	Target     ipeers.WireTo // the member's session, pid and proc_start as the row recorded them
	Text       string
}

// TeamNoticeDeliverer is the narrow seam the team module delivers notices through. It skips only what is about the
// inbound HTTP request — the host principal's authentication and the Peers.Deliver switch (a notice is part of the
// membership the user consented to with AllowTeam) — and keeps the rest of /deliver's target side: the lead host must
// still be bound, the target is re-verified against this daemon's inventory, the pair and host limits apply, the lead's
// reply-capable helper is built from the recorded tuple, and one audit row is written.
type TeamNoticeDeliverer interface {
	// DeliverTeamNotice returns the delivery result (delivered | delivery_uncertain) or an error: ErrNoticeNotBound, or a
	// *NoticeError.
	DeliverTeamNotice(ctx context.Context, n TeamNotice) (string, error)
}

// ErrNoticeNotBound: no live peer entry carries the lead host's id (never paired, unpaired, or its alias re-created
// for another host). Nothing was written.
var ErrNoticeNotBound = errors.New("the lead host is not a bound peer")

// NoticeError is a refused notice: the wire code /deliver would have used and whether trying again can help.
type NoticeError struct {
	Code      string
	Detail    string
	retryable bool
}

func (e *NoticeError) Error() string { return e.Code + ": " + e.Detail }

// Retryable: a later attempt may succeed (the daemon was busy, stopping, a helper was starting). A permanent one —
// target_gone, bad_request — means this notice cannot be delivered as recorded.
func (e *NoticeError) Retryable() bool { return e.retryable }

var _ TeamNoticeDeliverer = (*Module)(nil)

// DeliverTeamNotice implements TeamNoticeDeliverer: the binding check, the sender built from the recorded lead tuple, then
// deliverToLocalTarget — the very target side /deliver runs.
func (m *Module) DeliverTeamNotice(ctx context.Context, n TeamNotice) (string, error) {
	switch {
	case n.MsgID == "" || n.LeadHostID == "" || n.Lead.SessionID == "" || n.Lead.PID <= 0 || n.Lead.ProcStart == "" ||
		n.Target.AgentSessionID == "" || n.Text == "" || len(n.Text) > maxTeamNoticeBytes:
		return "", &NoticeError{Code: ipeers.ErrBadRequest, Detail: "incomplete team notice"}
	case m.stopCtx.Err() != nil:
		return "", &NoticeError{Code: ipeers.ErrNotReady, Detail: "daemon is stopping", retryable: true}
	}
	// The lead's address is the lead host's text, recorded from its command: it is held to the wire address grammar an
	// inbound sender's is. It names the helper under the lead host's alias in THIS daemon's config, after the part that
	// follows the lead host's own alias; one that does not fit (or none) names it after the ref.
	address := n.Lead.Address
	if i := strings.Index(address, "/"); i >= 0 {
		address = address[i+1:]
	}
	if address == "" || ipeers.ValidateWireAddress(address) != nil {
		address = n.Lead.Ref
	}
	from := ipeers.WireFrom{
		HostID: n.LeadHostID, AgentSessionID: n.Lead.SessionID, PID: n.Lead.PID, ProcStart: n.Lead.ProcStart,
		DeclaredMode: ipeers.ModePrompting, Address: address,
	}
	// Everything else goes through the validation /deliver applies to its request, before anything is written.
	if err := (ipeers.DeliverRequest{MsgID: n.MsgID, From: from, To: n.Target, Text: n.Text}).Validate(); err != nil {
		return "", &NoticeError{Code: ipeers.ValidationCode(err), Detail: err.Error()}
	}
	snap, ok := m.snapshotForLeadHost(n.LeadHostID)
	if !ok {
		return "", ErrNoticeNotBound
	}
	if !m.hostLimit.Allow(n.LeadHostID) {
		return "", &NoticeError{Code: ipeers.ErrRateLimited, Detail: "host rate limit exceeded", retryable: true}
	}
	if m.audit == nil {
		return "", &NoticeError{Code: ipeers.ErrAuditUnavailable, Detail: "audit store is not available", retryable: true}
	}
	id, err := m.audit.Insert(store.PeerMessage{
		MsgID:         n.MsgID,
		Direction:     store.DirIn,
		TS:            m.now(),
		FromHostID:    n.LeadHostID,
		FromSessionID: n.Lead.SessionID,
		ToHostID:      snap.localHostID,
		ToSessionID:   n.Target.AgentSessionID,
		DeclaredMode:  ipeers.ModePrompting,
		EffectiveMode: ipeers.ModePrompting,
		Bytes:         len(n.Text),
	})
	if err != nil {
		m.logf("peers: team notice %s from %q: audit insert: %v", n.MsgID, snap.entry.Alias, err)
		return "", &NoticeError{Code: ipeers.ErrAuditUnavailable, Detail: "audit insert failed", retryable: true}
	}

	result, errText, ref := m.deliverToLocalTarget(ctx, snap, localDelivery{
		msgID: n.MsgID, text: n.Text, to: n.Target, senderAlias: snap.entry.Alias, from: from,
		effective: ipeers.ModePrompting,
		oneWay:    snap.entry.Token == "", // D12: no verified route back to the lead
	})
	if ref != nil {
		if ref.clientGone {
			m.setResult(id, "", resultClientGone, "")
			return "", &NoticeError{Code: ipeers.ErrNotReady, Detail: "cancelled while the helper was starting", retryable: true}
		}
		m.setResult(id, "", ref.code, ref.auditDetail)
		m.logf("peers: team notice %s from %q refused (%s): %s", n.MsgID, snap.entry.Alias, ref.code, ref.auditDetail)
		return "", &NoticeError{Code: ref.code, Detail: ref.wireDetail, retryable: ref.status >= http.StatusInternalServerError || ref.status == http.StatusTooManyRequests}
	}
	m.setResult(id, "", result, errText)
	return result, nil
}

// snapshotForLeadHost is the one config read a notice makes: the live entry carrying hostID (never an unverified one),
// by host id and not by alias, with this daemon's own identity.
func (m *Module) snapshotForLeadHost(hostID string) (deliverSnapshot, bool) {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	s := deliverSnapshot{deliver: m.core.Cfg.Peers.Deliver, localHostID: m.core.Cfg.HostID, localAlias: m.core.Cfg.PeerAlias()}
	if hostID == "" {
		return s, false
	}
	for _, h := range m.core.Cfg.Peers.Hosts {
		if h.HostID == hostID {
			s.entry, s.found = h, true
			return s, true
		}
	}
	return s, false
}
