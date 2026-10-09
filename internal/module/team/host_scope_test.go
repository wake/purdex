package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// X3b-1c: the readers and writers of `state = 'active'` that mean "a session on THIS host" are scoped to this host's rows,
// so a remote member row (host_id ≠ local; cross-host spec §4.2) never looks like a local session. One test per decision of
// the table in the PR; each is red when its scope is dropped.

func hostScopeFixture(t *testing.T) *fixture {
	t.Helper()
	f, fc := remoteFixture(t)
	_ = fc
	f.remoteRow("abc12", "hostM", "mk1", rowActive) // a remote member (session sid-abc12, ref _rabc12)
	return f
}

// roster: the lead's roster lists local members only until X5 shows remote ones with their host; seats still count them.
func TestHostScope_RosterListsOnlyLocalMembers(t *testing.T) {
	f := hostScopeFixture(t)
	seedMember(t, f.m.store, "op-local", uid(1), "sid-local", f.clock.Load())
	r := f.getRoster()
	var n int
	for _, tm := range r.Teams {
		for _, mem := range tm.Members {
			n++
			if mem.SessionID == "sid-abc12" {
				t.Fatal("a remote member was listed as if it were a local session")
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d members listed, want the one local member", n)
	}
	if used, _ := seatsTaken(f.m.store.db, uid(1), ""); used != 2 {
		t.Fatalf("seats = %d, want 2 (the remote member still holds one)", used)
	}
}

// last turn: a turn end is a LOCAL session's; a remote member's session id is never recorded from here.
func TestHostScope_ALastTurnIsNotRecordedForARemoteRow(t *testing.T) {
	f := hostScopeFixture(t)
	if w, err := f.m.store.SetLastTurn("sid-abc12", "did things", 5, 1); err != nil || w != lastTurnNone {
		t.Fatalf("remote row: %v %v, want lastTurnNone", w, err)
	}
	seedMember(t, f.m.store, "op-local", uid(1), "sid-local", f.clock.Load())
	if w, err := f.m.store.SetLastTurn("sid-local", "did things", 5, 1); err != nil || w == lastTurnNone {
		t.Fatalf("local row: %v %v, want a write", w, err)
	}
}

// local mutators: release / kill / gone marks of the local paths never touch a remote row, even given its keys.
func TestHostScope_LocalMarksNeverTouchARemoteRow(t *testing.T) {
	f := hostScopeFixture(t)
	st := f.m.store
	if ok, _ := st.ReleaseMember("abc12", "sid-abc12", 1); ok {
		t.Fatal("ReleaseMember released a remote row")
	}
	if ok, _ := st.MarkMemberKilled("abc12", "sid-abc12", 1); ok {
		t.Fatal("MarkMemberKilled killed a remote row")
	}
	if ok, _ := st.MarkMemberGone("abc12", "sid-abc12", 1); ok {
		t.Fatal("MarkMemberGone marked a remote row gone")
	}
	if s, _ := f.memberRowState("abc12"); s != rowActive {
		t.Fatalf("row = %s", s)
	}
}

// member relay: only a local row can be relayed (a cross-host member relay is out of scope); the gate at the store too.
func TestHostScope_AMemberRelayOfARemoteRowIsRefused(t *testing.T) {
	f := hostScopeFixture(t)
	code, _, ae := f.createRelay("00000000-0000-4000-8000-0000000000aa", "/tmp/10.sock", "air26/"+remoteRef)
	if code != http.StatusConflict || ae.Error != team.ErrRelayUnsupported {
		t.Fatalf("relay of a remote member = %d %s, want 409 relay_unsupported", code, ae.Error)
	}
	// the store's own gate: the op's session is not an active LOCAL member
	_, _, err := f.m.store.CreateMemberRelayOp(team.RelayOp{ID: "op-x", Kind: team.RelayKindMember, HostID: "h:1", SessionID: "sid-abc12", Ref: remoteRef, TeamID: uid(1), HandoffPath: "/x", CreatedAt: 1, UpdatedAt: 1}, nil)
	if err == nil {
		t.Fatal("the store accepted a member relay op for a remote row")
	}
}

// view: a remote row's address is its host's alias and its ref, never resolved through this host's registry.
func TestHostScope_ARemoteRowsAddressIsItsHostsAlias(t *testing.T) {
	f := hostScopeFixture(t)
	code, mem, _, _ := f.release("air26/" + remoteRef) // answers the row's view
	if code != http.StatusOK || mem.Address != "air26/"+remoteRef {
		t.Fatalf("view = %d address %q, want air26/%s", code, mem.Address, remoteRef)
	}
}

// codex attack: the caller gates (task / report / compacted callers) look a LOCAL session up; a remote row that happens to
// carry the same session id is nobody's caller identity. Mutation gate: drop the scope in ActiveMemberInLiveTeam → red.
func TestHostScope_ACallerGateNeverResolvesToARemoteRow(t *testing.T) {
	f := hostScopeFixture(t)
	if _, _, ok, err := f.m.store.ActiveMemberInLiveTeam("sid-abc12"); err != nil || ok {
		t.Fatalf("a remote row answered as a member by session id: ok=%v err=%v", ok, err)
	}
	seedMember(t, f.m.store, "op-local", uid(1), "sid-local", f.clock.Load())
	if _, _, ok, _ := f.m.store.ActiveMemberInLiveTeam("sid-local"); !ok {
		t.Fatal("a local member was not found")
	}
}

// codex attack: a remote row's view never reads this host's usage / quota for its session id, and an unresolved alias never
// falls back to the local alias.
func TestHostScope_ARemoteViewUsesNoLocalReaders(t *testing.T) {
	f, fc := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.usage.setPct("sid-abc12", 99) // a LOCAL reading under the same session id
	rows, _ := f.m.store.MembersOf(uid(1))
	var mr memberRow
	for _, r := range rows {
		if r.SpawnOp == "abc12" {
			mr = r
		}
	}
	v := f.m.memberView(mr)
	if v.Context != nil {
		t.Fatalf("a remote view took this host's reading: %+v", v.Context)
	}
	if v.Address != "air26/"+remoteRef {
		t.Fatalf("address = %q", v.Address)
	}
	fc.aliases = map[string]string{} // the peer entry is gone
	if v := f.m.memberView(mr); v.Address != "hostM/"+remoteRef {
		t.Fatalf("unresolved alias: address = %q, want the host id form", v.Address)
	}
}
