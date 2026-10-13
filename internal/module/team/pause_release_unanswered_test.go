// internal/module/team/pause_release_unanswered_test.go — a person's release of a session that holds relay_unanswered (RT-2a2, T18).
package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A switch-off and the reconciliation leave a relay_unanswered row alone. Mutation gate: drop `cause IN ('quota','lead')` from
// ResumeScope.where → the row goes (red).
func TestResumeScope_OnlyQuotaAndLeadAreResumable(t *testing.T) {
	f := newFixture(t)
	f.unanswered("sid-1")
	f.seedPause("sid-2", team.PauseCauseQuota, team.PauseRoleSolo, "_bbbbbb", "t", 1)
	for _, sc := range []ResumeScope{{All: true}, {OnSinceNot: 99}, {Release: "sid-1"}} {
		if _, err := f.m.store.ResumePauses(sc, f.clock.Load(), nil, func() string { return "tok" }); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.pauses()["sid-1/relay_unanswered"]; !ok {
			t.Fatalf("scope %+v took the relay_unanswered row", sc)
		}
	}
}

// Releasing a session that holds only relay_unanswered denies its card (by the request's client); the card's close takes the row
// and tells the session the denial. Mutation gate: skip the deny → the row stays and the release does nothing (red).
func TestRelease_OfAnUnansweredSessionDeniesItsCard(t *testing.T) {
	f := newFixture(t)
	card := f.unanswered("sid-1")
	code, raw := f.releasePaused("sid-1", appRelease())
	if code != http.StatusOK {
		t.Fatalf("release: %d %s", code, raw)
	}
	a := f.row(card.RequestID)
	if a.State != team.StateDenied || a.DecidedBy == nil || a.DecidedBy.Kind != "app" {
		t.Fatalf("card = %s by %+v, want denied by the app client", a.State, a.DecidedBy)
	}
	if len(f.pauses()) != 0 {
		t.Fatalf("pauses = %+v, want none", f.pauses())
	}
	notices := f.noticesOf("sid-1")
	if len(notices) == 0 || notices[len(notices)-1] != team.PauseResumedUnansweredDenied {
		t.Fatalf("notices = %q, want the denial last", notices)
	}
	for _, o := range f.outbox() {
		if o.Kind == team.PauseOutboxReask {
			t.Fatalf("a reask control was written: %+v", o)
		}
	}
}

// relay_unanswered + quota: the card is denied, then the quota row is released WITHOUT a reask (the person chose not to relay).
// Mutation gate: release it with the reask → a reask control / token is written (red).
func TestRelease_UnansweredPlusQuotaEndsBothWithNoReask(t *testing.T) {
	f := newFixture(t)
	f.modHello("sid-1", "5")
	card := f.unanswered("sid-1")
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, "_aaaaaa", "t", 1)
	code, raw := f.releasePaused("sid-1", appRelease())
	if code != http.StatusOK {
		t.Fatalf("release: %d %s", code, raw)
	}
	if f.row(card.RequestID).State != team.StateDenied {
		t.Fatal("the card was not denied")
	}
	if len(f.pauses()) != 0 {
		t.Fatalf("pauses = %+v, want none", f.pauses())
	}
	for _, o := range f.outbox() {
		if o.Kind == team.PauseOutboxReask {
			t.Fatalf("a reask control was written: %+v", o)
		}
	}
	notices := f.noticesOf("sid-1")
	if last := notices[len(notices)-1]; last != team.PauseResumedUnansweredDenied {
		t.Fatalf("last notice = %q, want the denial text", last)
	}
}

// A quota-only session is released as before (the reask is offered). Mutation gate: apply NoReask to every release → no reask (red).
func TestRelease_QuotaOnlyStillReasks(t *testing.T) {
	f := newFixture(t)
	f.modHello("sid-1", "5")
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, "_aaaaaa", "t", 1)
	if code, raw := f.releasePaused("sid-1", appRelease()); code != http.StatusOK {
		t.Fatalf("release: %d %s", code, raw)
	}
	var reask bool
	for _, o := range f.outbox() {
		reask = reask || o.Kind == team.PauseOutboxReask
	}
	if !reask {
		t.Fatal("a quota-only release lost its reask")
	}
}
