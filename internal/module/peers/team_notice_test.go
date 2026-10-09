// internal/module/peers/team_notice_test.go
package peers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/proxyhelper/proxyhelpertest"
	"github.com/wake/purdex/internal/store"
)

// The team notice seam (cross-host team spec §4.4, plan X3d-1): the member host tells its member, as the lead, through the
// same target side as /deliver (deliverToLocalTarget), without the inbound HTTP auth and without the Peers.Deliver switch.

func (e *deliverEnv) notice(text string) TeamNotice {
	return TeamNotice{
		MsgID:      uuid.NewString(),
		LeadHostID: remoteHostID,
		Lead:       TeamNoticeLead{SessionID: senderSessionID, Ref: "_lead01", Address: "air/lead-x", PID: senderPID, ProcStart: senderProcStart},
		Target:     ipeers.WireTo{AgentSessionID: targetSessionID, PID: targetPID, ProcStart: targetProcStart},
		Text:       text,
	}
}

func noticeErr(t *testing.T, err error) *NoticeError {
	t.Helper()
	var ne *NoticeError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want a *NoticeError", err)
	}
	return ne
}

// §11 Notices: delivered with M's Peers.Deliver OFF; as the lead (helper named under the lead host's alias, reply socket
// in the frame); one audit row; and the member's reply reaches the lead.
func TestTeamNotice_DeliveredWithPeersDeliverOffAndTheReplyReachesTheLead(t *testing.T) {
	e := newDeliverEnv(t, envOpts{deliver: boolp(false)})
	forwarded := make(chan ipeers.DeliverRequest, 1)
	e.m.post = func(_ context.Context, _ *http.Client, _, _ string, req ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error) {
		forwarded <- req
		return ipeers.DeliverResponse{MsgID: req.MsgID, Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting}, nil, nil
	}
	n := e.notice("you are a member now")

	// the same message through /deliver is refused: the switch is really off
	if rr := e.post(e.hostCtx(), e.request()); rr.Code != http.StatusForbidden {
		t.Fatalf("/deliver with the switch off = %d, want 403", rr.Code)
	}
	res, err := e.m.DeliverTeamNotice(context.Background(), n)
	if err != nil || res != ipeers.ResultDelivered {
		t.Fatalf("res=%q err=%v", res, err)
	}
	w, sock := wrapperOf(t, e.recvLine())
	e.assertNoLine()
	if w.Text != n.Text || w.FromName != remoteAlias+"/lead-x" || w.FromMode != ipeers.ModePrompting {
		t.Fatalf("wrapper = %+v", w)
	}
	rows := e.rows()
	var row store.PeerMessage
	for _, r := range rows {
		if r.MsgID == n.MsgID {
			row = r
		}
	}
	if row.Direction != store.DirIn || row.FromHostID != remoteHostID || row.FromSessionID != senderSessionID ||
		row.ToHostID != localHostID || row.ToSessionID != targetSessionID || row.Result != ipeers.ResultDelivered || row.Bytes != len(n.Text) {
		t.Fatalf("audit row = %+v", row)
	}

	reply := `{"msgV":1,"msg_id":"` + uuid.NewString() + `","type":"user","priority":"next","from":"uds:` + e.targetSock + `","message":{"role":"user","content":"ok, working"}}`
	proxyhelpertest.WriteToSock(t, sock, reply)
	select {
	case req := <-forwarded:
		if req.Text != "ok, working" || req.To.AgentSessionID != senderSessionID {
			t.Fatalf("forwarded reply = %+v, want the text to the lead session %s", req, senderSessionID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the member's reply was not forwarded to the lead within 5 s")
	}
}

// The lead host must still be bound: a live peer entry carrying its host id. (Mutation: skip the binding → red.)
func TestTeamNotice_RefusedWhenTheLeadHostIsNotBound(t *testing.T) {
	cases := map[string][]config.PeerHost{
		"no entry":                  {},
		"entry for another host":    {{Alias: "air", URL: "http://air.invalid:7860", HostID: "someone-else:1", Token: "t", InboundToken: "i"}},
		"entry that never verified": {{Alias: "air", URL: "http://air.invalid:7860", HostID: "", Token: "t", InboundToken: "i"}},
	}
	for name, hosts := range cases {
		t.Run(name, func(t *testing.T) {
			e := newDeliverEnv(t, envOpts{hosts: hosts})
			if hosts == nil || len(hosts) == 0 {
				e.m.core.CfgMu.Lock()
				e.m.core.Cfg.Peers.Hosts = nil
				e.m.core.CfgMu.Unlock()
			}
			_, err := e.m.DeliverTeamNotice(context.Background(), e.notice("hi"))
			if !errors.Is(err, ErrNoticeNotBound) {
				t.Fatalf("err = %v, want ErrNoticeNotBound", err)
			}
			e.assertNoLine()
			if n := len(e.rows()); n != 0 {
				t.Fatalf("an unbound notice left %d audit rows", n)
			}
		})
	}
}

// The target is re-verified against this daemon's own inventory: live, and the pid / proc_start the row recorded.
func TestTeamNotice_TargetMustBeTheLiveProcessTheRowNames(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	for name, mut := range map[string]func(*TeamNotice){
		"another pid":        func(n *TeamNotice) { n.Target.PID++ },
		"another proc_start": func(n *TeamNotice) { n.Target.ProcStart = "Tue Sep 15 00:00:00 2026" },
		"unknown session":    func(n *TeamNotice) { n.Target.AgentSessionID = "00000000-0000-4000-8000-000000000000" },
	} {
		n := e.notice("hi")
		mut(&n)
		_, err := e.m.DeliverTeamNotice(context.Background(), n)
		ne := noticeErr(t, err)
		if ne.Code != ipeers.ErrTargetGone || ne.Retryable() {
			t.Fatalf("%s: %+v, want a permanent target_gone", name, ne)
		}
	}
	e.assertNoLine()
}

func TestTeamNotice_HostRateLimit(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	e.m.hostLimit = newHostLimiter(1, time.Minute, time.Now)
	if _, err := e.m.DeliverTeamNotice(context.Background(), e.notice("one")); err != nil {
		t.Fatal(err)
	}
	e.recvLine()
	_, err := e.m.DeliverTeamNotice(context.Background(), e.notice("two"))
	if ne := noticeErr(t, err); ne.Code != ipeers.ErrRateLimited || !ne.Retryable() {
		t.Fatalf("%+v, want a retryable rate_limited", ne)
	}
	e.assertNoLine()
}

func TestTeamNotice_RefusesAnIncompleteNotice(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	for name, mut := range map[string]func(*TeamNotice){
		"no text":         func(n *TeamNotice) { n.Text = "" },
		"huge text":       func(n *TeamNotice) { n.Text = strings.Repeat("x", maxTeamNoticeBytes+1) },
		"no msg id":       func(n *TeamNotice) { n.MsgID = "" },
		"no lead session": func(n *TeamNotice) { n.Lead.SessionID = "" },
		"no lead proc":    func(n *TeamNotice) { n.Lead.ProcStart = "" },
		"no lead pid":     func(n *TeamNotice) { n.Lead.PID = 0 },
		"no target":       func(n *TeamNotice) { n.Target.AgentSessionID = "" },
		"no lead host":    func(n *TeamNotice) { n.LeadHostID = "" },
	} {
		n := e.notice("hi")
		mut(&n)
		if _, err := e.m.DeliverTeamNotice(context.Background(), n); err == nil || errors.Is(err, ErrNoticeNotBound) {
			t.Fatalf("%s: err = %v, want a bad-notice error", name, err)
		}
	}
	e.assertNoLine()
	if n := len(e.rows()); n != 0 {
		t.Fatalf("a bad notice left %d audit rows", n)
	}
}

// The lead's address is the lead host's text: it goes through the wire address grammar like an inbound sender's. One that
// does not fit (extra slash, control character, bad suffix, too long) names the helper after the ref instead; if the ref is
// no good either, the notice is refused as bad before anything is written (codex attack).
func TestTeamNotice_LeadAddressFollowsTheWireGrammar(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	for name, address := range map[string]string{
		"extra slash":  "air/forged/name",
		"control char": "air/lead\x07x",
		"bad suffix":   "air/lead:??",
		"too long":     "air/" + strings.Repeat("a", 300),
		"newline":      "air/lead\nforged",
	} {
		n := e.notice("hi")
		n.Lead.Address = address
		if _, err := e.m.DeliverTeamNotice(context.Background(), n); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if w, _ := wrapperOf(t, e.recvLine()); w.FromName != remoteAlias+"/_lead01" {
			t.Fatalf("%s: from-name = %q, want the ref's air/_lead01", name, w.FromName)
		}
	}
	bad := e.notice("hi")
	bad.Lead.Address, bad.Lead.Ref = "air/a/b", "no good ref"
	before := len(e.rows())
	_, err := e.m.DeliverTeamNotice(context.Background(), bad)
	if ne := noticeErr(t, err); ne.Retryable() {
		t.Fatalf("%+v, want a permanent bad request", ne)
	}
	if len(e.rows()) != before {
		t.Fatal("a refused notice wrote an audit row")
	}
	e.assertNoLine()
}

// With no address the helper falls back to the lead's ref: a name that always exists.
func TestTeamNotice_HelperNameFallsBackToTheRef(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	n := e.notice("hi")
	n.Lead.Address = ""
	if _, err := e.m.DeliverTeamNotice(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if w, _ := wrapperOf(t, e.recvLine()); w.FromName != remoteAlias+"/_lead01" {
		t.Fatalf("from-name = %q, want air/_lead01", w.FromName)
	}
}
