package peers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/personas"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/team"
)

// The backfill (name pool v2 spec §6.4): once personas exist, a live conversation whose name does not end in a persona code
// gets one, by the rule of its role; the old name stays as an alias; the session is told.

// fakeTeam answers the seat reader and the relay guard of the team module.
type fakeTeam struct {
	seats    map[string]team.Seat
	inRelay  map[string]bool
	seatErr  error
	relayErr error
}

func (f *fakeTeam) SeatOf(sid string) (team.Seat, error) {
	if f.seatErr != nil {
		return team.Seat{}, f.seatErr
	}
	if s, ok := f.seats[sid]; ok {
		return s, nil
	}
	return team.Seat{Role: team.SeatNone}, nil
}

func (f *fakeTeam) InRelay(sid string) (bool, error) { return f.inRelay[sid], f.relayErr }

type sentNotice struct{ req ipeers.SendRequest }

// backfillEnv is rulesEnv (personas hiro lead:true, anna, mia) with a core that holds the fake team module and a notice sender
// that records.
type backfillEnv struct {
	*poolEnv
	team    *fakeTeam
	mu      sync.Mutex
	notices []ipeers.SendRequest
	noticeE error
}

func newBackfillEnv(t *testing.T) *backfillEnv {
	t.Helper()
	e := &backfillEnv{poolEnv: rulesEnv(t), team: &fakeTeam{seats: map[string]team.Seat{}, inRelay: map[string]bool{}}}
	c := newTestCoreWithHosts(t, "host-mlab", "mlab", nil)
	c.Registry.Register(team.SeatReaderKey, e.team)
	c.Registry.Register(team.RelayGuardKey, e.team)
	e.m.core = c
	e.m.noticeSend = func(_ context.Context, req ipeers.SendRequest) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.notices = append(e.notices, req)
		return e.noticeE
	}
	return e
}

// live starts sid with the stored name and a messaging inbox, and returns its registry entry.
func (e *backfillEnv) live(sid, name string) ipeers.Entry {
	e.t.Helper()
	en := e.start(sid, tEarly)
	en.Inbox = "/tmp/cc-socks/" + sid[len(sid)-1:] + ".sock"
	e.assign(sid, name, store.PeerNameSourcePool)
	return en
}

func (e *backfillEnv) refName(sid string) string { return "purdex-54-" + ipeers.RefID(sid)[1:3] }

// backfill runs one naming pass over entries and returns the pass's names; the notices it sent are waited for.
func (e *backfillEnv) backfill(entries ...ipeers.Entry) map[string]string {
	e.t.Helper()
	var cands []nameCandidate
	for _, en := range entries {
		cands = append(cands, nameCandidate{sid: en.SessionID, ref: ipeers.RefID(en.SessionID), registryName: en.Name})
	}
	names := e.m.resolveNames(context.Background(), cands)
	e.m.backfillPersonaNames(context.Background(), entries, nil, names, "mlab")
	e.m.workers.Wait()
	return names
}

func (e *backfillEnv) sent() []ipeers.SendRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ipeers.SendRequest(nil), e.notices...)
}

func TestBackfill_ASessionWithoutAPersonaCodeGetsOneByRule3AndIsTold(t *testing.T) {
	e := newBackfillEnv(t)
	a := e.live(poolSidA, e.refName(poolSidA))
	names := e.backfill(a)

	assert.Equal(t, "purdex-54-anna", names[poolSidA], "hiro is lead:true, anna the first newcomer")
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA))
	al, err := e.meta.PeerNames().Aliases(context.Background())
	require.NoError(t, err)
	require.Len(t, al, 1)
	assert.Equal(t, e.refName(poolSidA), al[0].Alias, "the old name stays routable")
	assert.Equal(t, store.PeerNameSourcePool, storedRow(t, e.meta, poolSidA).Source)

	got := e.sent()
	require.Len(t, got, 1)
	assert.Equal(t, "mlab/"+ipeers.RefID(poolSidA), got[0].To, "to the session itself, by ref")
	assert.Equal(t, a.Inbox, got[0].OriginInbox, "from its own inbox")
	assert.Contains(t, got[0].Text, "[pdx team]")
	assert.Contains(t, got[0].Text, "mlab/purdex-54-anna")
	assert.Contains(t, got[0].Text, "mlab/"+e.refName(poolSidA))
}

func TestBackfill_ALivePersonaNameIsLeftAlone_ActiveOrRetired(t *testing.T) {
	e := newBackfillEnv(t)
	e.m.bindPersonas(&fakePersonas{set: personas.Set{Items: []personas.Persona{leadP("hiro"), pp("anna", "Anna", true), pp("mia", "Mia", false)}}})
	a := e.live(poolSidA, "purdex-54-mia")  // active
	b := e.live(poolSidB, "purdex-54-anna") // retired
	e.backfill(a, b)
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA))
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidB))
	assert.Empty(t, e.sent())
}

func TestBackfill_ABuiltInNameIsReplacedOnceThereArePersonas(t *testing.T) {
	e := newBackfillEnv(t)
	a := e.live(poolSidA, "purdex-54-kenji")
	e.backfill(a)
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA))
}

func TestBackfill_WithNoPersonasNothingHappens(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	e := newBackfillEnv(t)
	e.m.bindPersonas(&fakePersonas{}) // the collection is empty now
	a := e.live(poolSidA, e.refName(poolSidA))
	e.backfill(a)
	assert.Equal(t, e.refName(poolSidA), e.storedName(poolSidA))
	assert.Empty(t, e.sent())
}

func TestBackfill_ASessionInARelayWaitsUntilTheRelayEnds(t *testing.T) {
	e := newBackfillEnv(t)
	a := e.live(poolSidA, e.refName(poolSidA))
	e.team.inRelay[poolSidA] = true
	e.backfill(a)
	assert.Equal(t, e.refName(poolSidA), e.storedName(poolSidA), "skipped")
	assert.Empty(t, e.sent())

	e.team.inRelay[poolSidA] = false
	e.backfill(a)
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA), "renamed on the next pass")
}

func TestBackfill_AnUnreadableTeamStoreSkipsTheSessionThisPass(t *testing.T) {
	e := newBackfillEnv(t)
	a := e.live(poolSidA, e.refName(poolSidA))
	e.team.seatErr = errors.New("team.db down")
	e.backfill(a)
	assert.Equal(t, e.refName(poolSidA), e.storedName(poolSidA), "the role is unknown, so it is not guessed")
	assert.True(t, e.logged("team.db down"))

	e.team.seatErr = nil
	e.team.relayErr = errors.New("relay read down")
	e.backfill(a)
	assert.Equal(t, e.refName(poolSidA), e.storedName(poolSidA), "a relay that cannot be ruled out is a relay")
}

func TestBackfill_WithoutTheTeamModuleEverySessionTakesRule3(t *testing.T) {
	e := newBackfillEnv(t)
	e.m.core = newTestCoreWithHosts(t, "host-mlab", "mlab", nil) // no seat reader, no relay guard registered
	a := e.live(poolSidA, e.refName(poolSidA))
	e.backfill(a)
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA))
}

func TestBackfill_ALeadTakesTheFirstOfItsCandidatesOtherThanItsOwnName(t *testing.T) {
	e := newBackfillEnv(t)
	a := e.live(poolSidA, "purdex-54-kenji")
	e.team.seats[poolSidA] = team.Seat{Role: team.SeatLead, TeamID: "t1"}
	e.backfill(a)
	// rule 1: keep (kenji), then former leads, then lead:true personas, then the others: so hiro
	assert.Equal(t, "purdex-54-hiro", e.storedName(poolSidA))
}

func TestBackfill_ALeadWithNothingFreeKeepsItsName(t *testing.T) {
	e := newBackfillEnv(t)
	e.holdRemote(tEarly, "hiro", "anna", "mia")
	a := e.live(poolSidA, "purdex-54-kenji")
	e.team.seats[poolSidA] = team.Seat{Role: team.SeatLead, TeamID: "t1"}
	e.backfill(a)
	assert.Equal(t, "purdex-54-kenji", e.storedName(poolSidA))
	assert.Empty(t, e.sent())
}

func TestBackfill_AMemberTakesAFreeNameItWorkedWithItsLeadBefore(t *testing.T) {
	e := newBackfillEnv(t)
	ctx := context.Background()
	lead := e.live(poolSidA, "purdex-54-hiro") // a persona-named lead
	mem := e.live(poolSidB, e.refName(poolSidB))
	e.team.seats[poolSidA] = team.Seat{Role: team.SeatLead, TeamID: "t1"}
	e.team.seats[poolSidB] = team.Seat{Role: team.SeatMember, TeamID: "t1", LeadSessionID: poolSidA}
	require.NoError(t, e.meta.NameHistory().RecordPair(ctx, "hiro", "mia", 5))
	e.backfill(lead, mem)
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidB), "rule 2 step 1: paired with hiro, though anna is first in the pool")
}

// The leads of the pass are named first, whatever the session ids: a member whose id sorts before its lead's still reads the
// lead's new persona code (codex R1 on NP-3c-3).
func TestBackfill_LeadsAreNamedBeforeTheirMembersWhateverTheSessionIds(t *testing.T) {
	e := newBackfillEnv(t)
	ctx := context.Background()
	mem := e.live(poolSidA, e.refName(poolSidA)) // sorts first
	lead := e.live(poolSidB, e.refName(poolSidB))
	e.team.seats[poolSidB] = team.Seat{Role: team.SeatLead, TeamID: "t1"}
	e.team.seats[poolSidA] = team.Seat{Role: team.SeatMember, TeamID: "t1", LeadSessionID: poolSidB}
	require.NoError(t, e.meta.NameHistory().RecordPair(ctx, "hiro", "mia", 5))
	e.backfill(mem, lead)
	assert.Equal(t, "purdex-54-hiro", e.storedName(poolSidB), "the lead takes the first lead:true candidate")
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA), "the member follows the lead's new code, not rule 3")
}

// A collection that is being applied is seen whole or not at all (codex R2 on NP-3c-3): a name that the new collection makes a
// persona code is not an old name, however the pass is timed against the publish.
func TestBackfill_ACollectionBeingAppliedIsSeenWholeByTheScan(t *testing.T) {
	e := newBackfillEnv(t)
	f := &fakePersonas{set: personas.Set{Items: []personas.Persona{leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", false)}}}
	e.m.bindPersonas(f)
	a := e.live(poolSidA, "purdex-54-zoe") // zoe is no persona yet

	inGap, release := make(chan struct{}), make(chan struct{})
	e.m.afterPoolPublished = func() { close(inGap); <-release }
	changed := make(chan struct{})
	go func() {
		f.change(leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", false), pp("zoe", "Zoe", false))
		close(changed)
	}()
	<-inGap // the new pool (zoe recognised) is out, the new code set is not yet

	done := make(chan struct{})
	go func() {
		e.backfill(a)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the backfill ran while the collection was half applied")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	<-changed
	<-done
	assert.Equal(t, "purdex-54-zoe", e.storedName(poolSidA), "zoe is a persona by the time the pass looks")
}

// The candidates are checked again at the rename: a collection that gives the name a persona code between the scan and the write
// leaves the session alone.
func TestBackfill_ACandidateIsCheckedAgainAtTheRename(t *testing.T) {
	e := newBackfillEnv(t)
	f := &fakePersonas{set: personas.Set{Items: []personas.Persona{leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", false)}}}
	e.m.bindPersonas(f)
	a := e.live(poolSidA, "purdex-54-zoe")
	e.m.afterBackfillScan = func() {
		f.change(leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", false), pp("zoe", "Zoe", false))
	}
	e.backfill(a)
	assert.Equal(t, "purdex-54-zoe", e.storedName(poolSidA))
	assert.Empty(t, e.sent())
}

// The notice workers are admitted and closed under one lock: a notice that was admitted before Stop is joined by Stop, and none
// starts after it (codex R2 on NP-3c-3).
func TestBackfill_StopJoinsANoticeThatWasAdmittedJustBeforeIt(t *testing.T) {
	e := newBackfillEnv(t)
	inGap, release := make(chan struct{}), make(chan struct{})
	e.m.beforeNoticeAdmit = func() { close(inGap); <-release }
	n := []renameNotice{{sid: poolSidA, inbox: "/tmp/x.sock", oldName: "purdex-54-a1", newName: "purdex-54-anna"}}
	go e.m.sendRenameNotices(n, "mlab")
	<-inGap // checked, not yet added

	stopped := make(chan struct{})
	go func() {
		_ = e.m.Stop(context.Background())
		close(stopped)
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	<-stopped
	assert.Len(t, e.sent(), 1, "Stop did not return before the admitted notice went out")

	e.m.beforeNoticeAdmit = nil
	e.m.sendRenameNotices(n, "mlab") // after Stop: nothing starts
	e.m.workers.Wait()
	assert.Len(t, e.sent(), 1)
}

func TestBackfill_AMemberOfARemoteLeadTakesRule3(t *testing.T) {
	e := newBackfillEnv(t)
	mem := e.live(poolSidB, e.refName(poolSidB))
	e.team.seats[poolSidB] = team.Seat{Role: team.SeatMemberRemote, TeamID: "t9", LeadHostID: "host-L", LeadSessionID: "lead-sid"}
	e.backfill(mem)
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidB))
}

func TestBackfill_TwoSessionsOfOnePassGetDifferentNamesAndNotAHeldOne(t *testing.T) {
	e := newBackfillEnv(t)
	e.holdRemote(tEarly, "anna")
	a := e.live(poolSidA, e.refName(poolSidA))
	b := e.live(poolSidB, e.refName(poolSidB))
	e.backfill(a, b)
	got := []string{e.storedName(poolSidA), e.storedName(poolSidB)}
	assert.ElementsMatch(t, []string{"purdex-54-mia", "purdex-54-hiro"}, got, "anna is held; mia first, hiro (lead:true) last")
}

func TestBackfill_ARenameThatLosesTheCompareAndSetIsLeftAndNoNoticeIsSent(t *testing.T) {
	e := newBackfillEnv(t)
	a := e.live(poolSidA, e.refName(poolSidA))
	e.m.peerNames = renameStore{PeerNameStore: e.m.peerNames, answer: func(string) (store.PeerNameEntry, bool, error) {
		return store.PeerNameEntry{Name: "other-name"}, false, nil
	}}
	names := e.backfill(a)
	assert.Equal(t, e.refName(poolSidA), names[poolSidA], "the pass's name is unchanged")
	assert.Empty(t, e.sent())
}

func TestBackfill_AFailedNoticeDoesNotUndoTheRename(t *testing.T) {
	e := newBackfillEnv(t)
	e.noticeE = errors.New("the session is gone")
	a := e.live(poolSidA, e.refName(poolSidA))
	e.backfill(a)
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA))
	assert.True(t, e.logged("notice"))
}

func TestBackfill_ASessionWithoutAnInboxIsRenamedButNotTold(t *testing.T) {
	e := newBackfillEnv(t)
	a := e.live(poolSidA, e.refName(poolSidA))
	a.Inbox = ""
	e.backfill(a)
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA))
	assert.Empty(t, e.sent())
	assert.True(t, e.logged("inbox"))
}

func TestBackfill_TheNoticeTextNamesTheNewAndTheOldAddress(t *testing.T) {
	assert.True(t, strings.HasPrefix(renameNoticeFmt, "[pdx team] "))
}
