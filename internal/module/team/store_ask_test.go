package teammod

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func openHookApproval(id, sid, toolUse string, terminalOnly bool) team.Approval {
	payload, _ := json.Marshal(team.HookAskPayload{ToolUseID: toolUse, Questions: json.RawMessage(`[{"question":"q?"}]`), TerminalOnly: terminalOnly})
	return team.Approval{
		ID: id, Kind: team.KindHookAsk, HostID: "h:1",
		Origin:  team.Origin{SessionID: sid, Ref: "_abc123", PID: 10, Cwd: "/w"},
		Payload: payload, State: team.StateOpen,
		CreatedAt: 1000, DeadlineAt: team.NoExpiryAt, LeaseUntil: 31_000,
	}
}

// A hook row stores its HookDecision in grant_json and reads it back as
// Hook, never as Grant; a lead row is untouched by that.
func TestStore_HookDecisionRidesInGrantsPlace(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openHookApproval("hk-1", "sid-1", "toolu_1", false), "h"); err != nil {
		t.Fatal(err)
	}
	after, won, err := s.CloseIfOpen("hk-1", Close{State: team.StateAnsweredLocal, DecidedAt: 2000,
		DecidedBy: &team.Client{Kind: team.ClientKindTerminal, Label: team.ClientKindTerminal}, Hook: &team.HookDecision{Answers: map[string]string{"q?": "紅"}}})
	if err != nil || !won || after.State != team.StateAnsweredLocal || after.Grant != nil || after.Hook == nil || after.Hook.Answers["q?"] != "紅" {
		t.Fatalf("after=%+v hook=%+v won=%v err=%v", after, after.Hook, won, err)
	}
	if _, _, _, err := s.Create(openApproval("ld-1", "sid-1", 1000), "h"); err != nil {
		t.Fatal(err)
	}
	ld, won, err := s.CloseIfOpen("ld-1", Close{State: team.StateApproved, DecidedAt: 2000, Grant: &team.Grant{MaxMembers: 2, Roots: []string{"/w"}}})
	if err != nil || !won || ld.Hook != nil || ld.Grant == nil || ld.Grant.MaxMembers != 2 {
		t.Fatalf("lead: %+v grant=%+v won=%v err=%v", ld, ld.Grant, won, err)
	}
}

func TestStore_OpenByToolUse_TerminalOnly_NonHook(t *testing.T) {
	s := openTestStore(t)
	for _, a := range []team.Approval{
		openHookApproval("hk-1", "sid-1", "toolu_1", false),
		openHookApproval("hk-2", "sid-1", "toolu_2", true),
		openHookApproval("hk-3", "sid-2", "toolu_1", true),
		openApproval("ld-1", "sid-1", 1000),
	} {
		if _, _, _, err := s.Create(a, "h"); err != nil {
			t.Fatal(err)
		}
	}
	a, found, err := s.OpenByToolUse("sid-1", "toolu_1")
	if err != nil || !found || a.ID != "hk-1" {
		t.Fatalf("by tool use: %+v found=%v err=%v", a, found, err)
	}
	if _, found, _ := s.OpenByToolUse("sid-1", "toolu_9"); found {
		t.Fatal("unknown tool use must not be found")
	}
	ro, err := s.OpenTerminalOnlyBySession("sid-1")
	if err != nil || len(ro) != 1 || ro[0].ID != "hk-2" {
		t.Fatalf("terminal_only of sid-1 = %+v err=%v", ro, err)
	}
	nh, err := s.ListOpenNonHook()
	if err != nil || len(nh) != 1 || nh[0].ID != "ld-1" {
		t.Fatalf("non-hook = %+v err=%v", nh, err)
	}
	if _, _, err := s.CloseIfOpen("hk-1", Close{State: team.StateDismissed, DecidedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OpenByToolUse("sid-1", "toolu_1"); found {
		t.Fatal("a closed row is not open by tool use")
	}
}

// OverrideIfApproved moves approved → terminal_override only (spec §6.6 step 5).
func TestStore_OverrideIfApprovedOnlyFromApproved(t *testing.T) {
	s := openTestStore(t)
	for _, id := range []string{"hk-1", "hk-2"} {
		if _, _, _, err := s.Create(openHookApproval(id, "sid-1", id, false), "h"); err != nil {
			t.Fatal(err)
		}
	}
	remote := &team.Client{Kind: "app", Label: "phone"}
	if _, _, err := s.CloseIfOpen("hk-1", Close{State: team.StateApproved, DecidedAt: 2, DecidedBy: remote, Hook: &team.HookDecision{Answers: map[string]string{"q?": "藍"}}}); err != nil {
		t.Fatal(err)
	}
	over, won, err := s.OverrideIfApproved("hk-1", 3, &team.HookDecision{Answers: map[string]string{"q?": "紅"}})
	if err != nil || !won || over.State != team.StateTerminalOverride || over.Hook.Answers["q?"] != "紅" || over.DecidedBy.Kind != team.ClientKindTerminal || over.DecidedAt != 3 {
		t.Fatalf("override: %+v hook=%+v by=%+v won=%v err=%v", over, over.Hook, over.DecidedBy, won, err)
	}
	if _, won, err := s.OverrideIfApproved("hk-1", 4, nil); err != nil || won {
		t.Fatalf("a second override must lose: won=%v err=%v", won, err)
	}
	if _, won, err := s.OverrideIfApproved("hk-2", 4, nil); err != nil || won {
		t.Fatalf("an open row cannot be overridden: won=%v err=%v", won, err)
	}
	if _, _, err := s.CloseIfOpen("hk-2", Close{State: team.StateDismissed, DecidedAt: 5}); err != nil {
		t.Fatal(err)
	}
	if _, won, _ := s.OverrideIfApproved("hk-2", 6, nil); won {
		t.Fatal("a dismissed row cannot be overridden")
	}
	if _, _, _, err := s.Create(openApproval("ld-1", "sid-1", 1000), "h"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CloseIfOpen("ld-1", Close{State: team.StateApproved, DecidedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if _, won, _ := s.OverrideIfApproved("ld-1", 7, nil); won {
		t.Fatal("a lead row is never overridden")
	}
}

// Mutation gate (spec §15): a hook_ask row closes through the same CAS as a
// lead request — a remote approve and a terminal answered_local racing on
// one row leave exactly one winner. Drop state='open' from closeWhere ⇒ red.
func TestStore_HookRowCloseExactlyOneWinner(t *testing.T) {
	for round := 0; round < 25; round++ {
		s := openTestStore(t)
		if _, _, _, err := s.Create(openHookApproval("hk-1", "sid-1", "toolu_1", false), "h"); err != nil {
			t.Fatal(err)
		}
		closes := []Close{
			{State: team.StateApproved, DecidedAt: 2, DecidedBy: &team.Client{Kind: "app", Label: "phone"}, Hook: &team.HookDecision{Answers: map[string]string{"q?": "藍"}}},
			{State: team.StateAnsweredLocal, DecidedAt: 2, DecidedBy: &team.Client{Kind: team.ClientKindTerminal, Label: team.ClientKindTerminal}, Hook: &team.HookDecision{Answers: map[string]string{"q?": "紅"}}},
			{State: team.StateDismissed, DecidedAt: 2},
		}
		var wg sync.WaitGroup
		won := make([]bool, len(closes))
		for i, c := range closes {
			wg.Add(1)
			go func(i int, c Close) {
				defer wg.Done()
				_, w, err := s.CloseIfOpen("hk-1", c)
				if err != nil {
					t.Errorf("close %d: %v", i, err)
				}
				won[i] = w
			}(i, c)
		}
		wg.Wait()
		n := 0
		for _, w := range won {
			if w {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("round %d: %d winners, want exactly 1 (%v)", round, n, won)
		}
		s.Close()
	}
}
