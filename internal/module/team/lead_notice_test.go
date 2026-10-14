package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The seam the lease notice outbox uses when no Purdex mod takes a notice (lease v2 amendment A, P2d-2).

// A member's notice goes to its lead, from the member's inbox; a lead and an unknown session are no members and nothing is sent.
func TestLeadNoticer_MemberReachesItsLeadOthersAreNoMembers(t *testing.T) {
	f := noticeFixture(t)
	var ln team.LeadNoticer = f.m
	member, delivered := ln.NoticeLeadOf("sid-ma", "the text")
	if !member || !delivered {
		t.Fatalf("member = %v delivered = %v, want both", member, delivered)
	}
	calls := f.sender.calls()
	if len(calls) != 1 || calls[0].Text != "the text" {
		t.Fatalf("calls = %+v, want one carrying the text", calls)
	}
	for _, sid := range []string{"sid-1" /* the lead */, "sid-nobody", ""} {
		if member, delivered := ln.NoticeLeadOf(sid, "x"); member || delivered {
			t.Fatalf("%q: member = %v delivered = %v, want neither", sid, member, delivered)
		}
	}
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d sends, want the one", n)
	}
}
