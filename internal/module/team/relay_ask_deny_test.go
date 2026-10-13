package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// POST /api/team/relays/deny (RT-2b T21, S-12): the lead declines a member's open ask.

func (f *fixture) deny(inbox, target string) (int, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/team/relays/deny", team.RelayDenyRequest{OriginInbox: inbox, Target: target})
	var ae team.APIError
	if code != http.StatusOK {
		ae = decodeErr(f.t, body)
	}
	return code, ae
}

// Mutation gate: do not close the ask → it stays open (red).
func TestRelayDeny_ClosesTheOpenAskAsWithdrawnDenied(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	f.openAsk(rid(600))
	if code, ae := f.deny("/tmp/10.sock", "_mem001"); code != http.StatusOK {
		t.Fatalf("deny: %d %+v", code, ae)
	}
	a := f.ask(rid(600))
	if a.State != team.RelayAskWithdrawn || a.Reason != team.RelayAskWithdrawDenied || a.ClosedAt == 0 {
		t.Fatalf("ask = %+v, want withdrawn / denied", a)
	}
	// a later `pdx relay _ref` still works
	if code, _, ae := f.createRelay(rid(601), "/tmp/10.sock", "_mem001"); code != http.StatusCreated {
		t.Fatalf("relay after a deny: %d %+v", code, ae)
	}
}

// Mutation gate: skip the caller / member check → a stranger's deny works (red).
func TestRelayDeny_Refusals(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	f.openAsk(rid(610))
	if code, ae := f.deny("/tmp/10.sock", "_zzzzzz"); code != http.StatusConflict || ae.Error != team.ErrNotYourMember {
		t.Fatalf("not your member: %d %+v", code, ae)
	}
	if code, ae := f.deny("/tmp/nobody.sock", "_mem001"); code == http.StatusOK {
		t.Fatalf("an unknown caller denied: %d %+v", code, ae)
	}
	if a := f.ask(rid(610)); a.State != team.RelayAskOpen {
		t.Fatalf("a refused deny closed the ask: %+v", a)
	}
}

func TestRelayDeny_NoOpenAskIs409(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	if code, ae := f.deny("/tmp/10.sock", "_mem001"); code != http.StatusConflict || ae.Error != team.ErrNoOpenAsk {
		t.Fatalf("no ask: %d %+v", code, ae)
	}
}

// S-8: a member of another host cannot be denied yet. Mutation gate: drop the remote check → the mirror is closed (red).
func TestRelayDeny_ARemoteMemberIsRelayUnsupported(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(620), 71, 300))
	if code, ae := f.deny("/tmp/10.sock", "_rabc12"); code != http.StatusConflict || ae.Error != team.ErrRelayUnsupported {
		t.Fatalf("remote deny: %d %+v", code, ae)
	}
	if a := f.ask(rid(620)); a.State != team.RelayAskOpen {
		t.Fatalf("mirror = %+v, want open", a)
	}
}
