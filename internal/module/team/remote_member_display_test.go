// internal/module/team/remote_member_display_test.go
package teammod

import (
	"context"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// c933b0-65: a remote member shows its tmux session NAME (what the Mac's left-hand group matches a workspace tab by) and the
// title its HOST gives it. The adopt answer carries the whole Origin.Tmux ("<session>:@<win>.%<pane>"); a row written with
// that whole string before this fix reads as the name too, without a migration.

func titledRecord(sessionID, title string) ipeers.PeerRecord {
	r := remoteRecord(sessionID, 30, "claude-sonnet-5-5", "")
	r.Title = title
	return r
}

func (f *fixture) setRowText(spawnOp, column, value string) {
	f.t.Helper()
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET `+column+` = ? WHERE spawn_op = ?`, value, spawnOp); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) cachePeers(rows ...ipeers.PeerRecord) {
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) { return rows, nil }
	f.m.readRemoteHost(context.Background(), "hostM")
}

// The write: the name and the pane are stored apart, as a local adopt does. Mutation gate: store the whole string → red.
func TestRemoteMember_AdoptAppliedStoresTheTmuxNameAndThePaneApart(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowJoining)
	out := applied("sid-ios", "_ios123")
	out.Tmux = "purdex-ios:@10.%10"
	f.settleRemote(CmdAdopt, "a1", "mk1", answerOf("a1", out))
	var name, pane string
	if err := f.m.store.db.QueryRow(`SELECT tmux_session, pane_id FROM team_members WHERE spawn_op = 'op-j'`).Scan(&name, &pane); err != nil {
		t.Fatal(err)
	}
	if name != "purdex-ios" || pane != "%10" {
		t.Fatalf("tmux_session=%q pane_id=%q, want purdex-ios and %%10", name, pane)
	}
}

// A host that reports no tmux leaves what the row already had (an empty answer never blanks a value).
func TestRemoteMember_AdoptAppliedWithoutAPane(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowJoining)
	out := applied("sid-ios", "_ios123")
	out.Tmux = ""
	f.settleRemote(CmdAdopt, "a1", "mk1", answerOf("a1", out))
	var name, pane string
	_ = f.m.store.db.QueryRow(`SELECT tmux_session, pane_id FROM team_members WHERE spawn_op = 'op-j'`).Scan(&name, &pane)
	if name != "tm-op-j" || pane != "" {
		t.Fatalf("tmux_session=%q pane_id=%q, want the row's own name and no pane", name, pane)
	}
}

// The read: a row already written with the whole string (the deployed one) reads as the name — roster and GET /api/team.
// Mutation gate: pass the stored string through → red.
func TestRemoteMember_ARowWithTheWholeOriginTmuxReadsAsTheName(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("a1", "hostM", "mk1", rowActive)
	f.setRowText("a1", "tmux_session", "purdex-ios:@10.%10")
	r, err := f.m.buildRoster()
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := rosterMemberOf(t, r, "sid-a1"); !ok || m.TmuxSession != "purdex-ios" {
		t.Fatalf("roster tmux = %q ok=%v", m.TmuxSession, ok)
	}
	if m := memberOf(t, f.leadTeamView(), "sid-a1"); m.TmuxSession != "purdex-ios" {
		t.Fatalf("GET /api/team tmux = %q", m.TmuxSession)
	}
}

// A local member's name is unchanged by the same read (it never carried a colon).
func TestRemoteMember_ALocalNameStaysAsItWas(t *testing.T) {
	if got := tmuxName("tm-0000000100"); got != "tm-0000000100" {
		t.Fatalf("tmuxName = %q", got)
	}
}

// The title is the host's (it changes, and the host is the source): the cached answer of GET /api/peers wins; with no cache
// entry the row's own value stays. Nothing is written back to the row. Mutation gate: ignore the cache → red.
func TestRemoteMember_TitleComesFromTheHostsAnswer(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("a1", "hostM", "mk1", rowActive)
	f.remoteRow("a2", "hostM", "mk2", rowActive)
	f.setRowText("a1", "title", "")
	f.setRowText("a2", "title", "row-title")
	f.cachePeers(titledRecord("sid-a1", "purdex-ios"))
	r, err := f.m.buildRoster()
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := rosterMemberOf(t, r, "sid-a1"); m.Title != "purdex-ios" {
		t.Fatalf("a1 roster title = %q, want the host's", m.Title)
	}
	if m, _ := rosterMemberOf(t, r, "sid-a2"); m.Title != "row-title" {
		t.Fatalf("a2 (not in the host's answer) roster title = %q, want the row's", m.Title)
	}
	v := f.leadTeamView()
	if m := memberOf(t, v, "sid-a1"); m.Title != "purdex-ios" {
		t.Fatalf("a1 GET /api/team title = %q", m.Title)
	}
	if m := memberOf(t, v, "sid-a2"); m.Title != "row-title" {
		t.Fatalf("a2 GET /api/team title = %q", m.Title)
	}
	var stored string
	_ = f.m.store.db.QueryRow(`SELECT title FROM team_members WHERE spawn_op = 'a1'`).Scan(&stored)
	if stored != "" {
		t.Fatalf("the row was written back with %q", stored)
	}
	// the host renamed it: the next reading is what shows
	f.cachePeers(titledRecord("sid-a1", "renamed"))
	if m := memberOf(t, f.leadTeamView(), "sid-a1"); m.Title != "renamed" {
		t.Fatalf("after a rename title = %q", m.Title)
	}
}

// A host that did not answer, or answered with an empty title, leaves the row's own value.
func TestRemoteMember_NoTitleInTheAnswerKeepsTheRows(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("a1", "hostM", "mk1", rowActive)
	f.setRowText("a1", "title", "row-title")
	f.cachePeers(titledRecord("sid-a1", ""))
	r, _ := f.m.buildRoster()
	if m, _ := rosterMemberOf(t, r, "sid-a1"); m.Title != "row-title" {
		t.Fatalf("empty title in the answer: roster title = %q", m.Title)
	}
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) { return nil, context.DeadlineExceeded }
	f.m.readRemoteHost(context.Background(), "hostM")
	if code, _, _ := call[team.TeamView](f, http.MethodGet, leadTeamURL, nil); code != http.StatusOK {
		t.Fatal(code)
	}
	if m := memberOf(t, f.leadTeamView(), "sid-a1"); m.Title != "row-title" {
		t.Fatalf("host down: title = %q", m.Title)
	}
}
