package peers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// firstFree picks the first free name, so a test knows which name a pick gets.
type firstFree struct{}

func (firstFree) Pick(free []string) string { return free[0] }

type pickerFunc func(free []string) string

func (f pickerFunc) Pick(free []string) string { return f(free) }

// poolEnv is a namer over a real meta store with a registry dir whose entries
// the test controls: live[pid] is the start time the fake liveness reports.
type poolEnv struct {
	t    *testing.T
	m    *Module
	meta *store.MetaStore
	dir  string
	mu   sync.Mutex
	live map[int]time.Time
	next int
	logs []string
}

func newPoolEnv(t *testing.T) *poolEnv {
	t.Helper()
	m, meta := namerFixture(t)
	e := &poolEnv{t: t, m: m, meta: meta, dir: t.TempDir(), live: map[int]time.Time{}, next: 1000}
	m.registryDir = e.dir
	m.liveness = ipeers.Liveness{
		Stat:     func(string) error { return nil },
		PidAlive: func(pid int) bool { e.mu.Lock(); defer e.mu.Unlock(); _, ok := e.live[pid]; return ok },
		StartTime: func(pid int) (time.Time, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if s, ok := e.live[pid]; ok {
				return s, nil
			}
			return time.Time{}, errors.New("no such pid")
		},
	}
	m.now = time.Now
	m.logf = func(f string, a ...any) {
		e.mu.Lock()
		e.logs = append(e.logs, fmt.Sprintf(f, a...))
		e.mu.Unlock()
	}
	m.WithNamePool(firstFree{})
	return e
}

// start registers a live registry entry for sid that started at procStart and
// returns it.
func (e *poolEnv) start(sid string, started time.Time) ipeers.Entry {
	e.t.Helper()
	e.mu.Lock()
	e.next++
	pid := e.next
	e.live[pid] = started
	e.mu.Unlock()
	ps := started.UTC().Format(ipeers.ProcStartLayout)
	body := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/w","startedAt":1,"procStart":%q,"version":"2.1.270","peerProtocol":1,"kind":"interactive","entrypoint":"cli","pidDomain":"darwin","messagingSocketPath":"/tmp/cc-socks/%d.sock","name":"purdex-pool","status":"idle"}`, pid, sid, ps, pid)
	if err := os.WriteFile(filepath.Join(e.dir, fmt.Sprintf("%d.json", pid)), []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return ipeers.Entry{PID: pid, SessionID: sid, ProcStart: ps, Name: "purdex-pool"}
}

// stop removes sid's liveness, as a process dying does.
func (e *poolEnv) stop(en ipeers.Entry) {
	e.mu.Lock()
	delete(e.live, en.PID)
	e.mu.Unlock()
}

func (e *poolEnv) assign(sid, name, source string) {
	e.t.Helper()
	if _, err := e.meta.PeerNames().Assign(context.Background(), sid, ipeers.RefID(sid), name, source, 1); err != nil {
		e.t.Fatal(err)
	}
}

func (e *poolEnv) logged(sub string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range e.logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// holdRemote makes the snapshot say a remote host holds these pool names, all
// started at start.
func (e *poolEnv) holdRemote(start time.Time, names ...string) {
	var rows []ipeers.PeerRecord
	for _, n := range names {
		rows = append(rows, snapRow("remote-"+n, start.UTC().Format(ipeers.ProcStartLayout)))
	}
	e.m.remoteNames.record("host-remote", rows, ipeers.AddressVersionV5, time.Now())
}

const (
	poolSidA = "bbbbbbbb-2222-4222-8222-00000000000a"
	poolSidB = "bbbbbbbb-2222-4222-8222-00000000000b"
	poolSidC = "bbbbbbbb-2222-4222-8222-00000000000c"
	poolSidD = "bbbbbbbb-2222-4222-8222-00000000000d"
)

var (
	tEarly = time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	tLate  = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
)

func poolSuffixOf(t *testing.T, name string) string {
	t.Helper()
	p, ok := ipeers.PoolNameOf(name)
	if !ok {
		t.Fatalf("%q does not end in a pool name", name)
	}
	return p
}

func TestNamePool_ANewConversationGetsAFreePoolNameStoredAsPool(t *testing.T) {
	e := newPoolEnv(t)
	e.start(poolSidA, tEarly)
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54")})[poolSidA]
	if got != "purdex-54-adam" {
		t.Fatalf("name = %q, want purdex-54-adam", got)
	}
	if row := storedRow(t, e.meta, poolSidA); row != (store.PeerNameEntry{Name: "purdex-54-adam", Source: store.PeerNameSourcePool}) {
		t.Fatalf("stored row = %+v", row)
	}
}

func TestNamePool_ANameAnotherHostHoldsIsNotPicked(t *testing.T) {
	e := newPoolEnv(t)
	e.start(poolSidA, tEarly)
	e.holdRemote(tEarly, "adam", "emma")
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54")})[poolSidA]
	if got != "purdex-54-kenji" {
		t.Fatalf("name = %q, want purdex-54-kenji (adam and emma are held remotely)", got)
	}
}

func TestNamePool_ALocalLiveConversationsNameIsNotPicked(t *testing.T) {
	e := newPoolEnv(t)
	e.start(poolSidA, tEarly)
	e.start(poolSidB, tLate)
	e.assign(poolSidA, "other-adam", store.PeerNameSourcePool)
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidB, "purdex-54")})[poolSidB]
	if got != "purdex-54-emma" {
		t.Fatalf("name = %q, want purdex-54-emma", got)
	}
}

func TestNamePool_TwoNewConversationsOfOnePassGetDifferentNames(t *testing.T) {
	e := newPoolEnv(t)
	// Neither has a registry entry (executions): only the pass's own list of
	// picks tells them apart.
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54"), cand(poolSidB, "purdex-54")})
	if got[poolSidA] == got[poolSidB] || got[poolSidA] == "" || got[poolSidB] == "" {
		t.Fatalf("names = %v, want two different pool names", got)
	}
}

func TestNamePool_ConcurrentPassesNeverHandOneNameToTwoConversations(t *testing.T) {
	e := newPoolEnv(t)
	const n = 10
	sids := make([]string, n)
	for i := range sids {
		sids[i] = fmt.Sprintf("bbbbbbbb-3333-4333-8333-%012d", i)
		e.start(sids[i], tEarly.Add(time.Duration(i)*time.Minute))
	}
	// A picker that always answers the first free name makes any lost update
	// show as a duplicate.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, sid := range sids {
		wg.Add(1)
		go func(i int, sid string) {
			defer wg.Done()
			<-start
			ctx := context.Background()
			if i%2 == 0 {
				e.m.resolveNames(ctx, []nameCandidate{cand(sid, "purdex-54")})
			} else {
				e.m.virtualNamesOf(ctx, ipeers.Entry{SessionID: sid, Name: "purdex-54"})
			}
		}(i, sid)
	}
	close(start)
	wg.Wait()
	rows, err := e.meta.PeerNames().Lookup(context.Background(), sids)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, sid := range sids {
		p := poolSuffixOf(t, rows[sid].Name)
		if other, dup := seen[p]; dup {
			t.Fatalf("%s and %s were both given %q", sid, other, p)
		}
		seen[p] = sid
	}
}

func TestNamePool_EveryPoolNameHeldFallsBackToTheRefSuffix(t *testing.T) {
	e := newPoolEnv(t)
	e.start(poolSidA, tEarly)
	e.holdRemote(tEarly, ipeers.NamePool[:]...)
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54")})[poolSidA]
	if want := vname(t, "purdex-54", poolSidA); got != want {
		t.Fatalf("name = %q, want the ref form %q", got, want)
	}
	if row := storedRow(t, e.meta, poolSidA); row.Source != store.PeerNameSourceRegistry {
		t.Fatalf("source = %q, want the base's own source", row.Source)
	}
}

func TestNamePool_ADeadConversationsNameIsFreeAgain(t *testing.T) {
	e := newPoolEnv(t)
	dead := e.start(poolSidA, tEarly)
	e.assign(poolSidA, "old-adam", store.PeerNameSourcePool)
	e.stop(dead)
	e.start(poolSidB, tLate)
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidB, "purdex-54")})[poolSidB]
	if got != "purdex-54-adam" {
		t.Fatalf("name = %q, want the dead conversation's adam back", got)
	}
}

func TestNamePool_ARelaySuccessorInheritsThePoolNameWithoutPicking(t *testing.T) {
	e := newPoolEnv(t)
	e.start(poolSidA, tEarly)
	e.start(poolSidB, tLate)
	pred := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54")})[poolSidA]
	succ := cand(poolSidB, "purdex-54", ipeers.RefID(poolSidA))
	got := e.m.resolveNames(context.Background(), []nameCandidate{succ})[poolSidB]
	if got != pred {
		t.Fatalf("successor = %q, want the predecessor's %q", got, pred)
	}
	if row := storedRow(t, e.meta, poolSidB); row.Source != store.PeerNameSourceLineage {
		t.Fatalf("source = %q, want lineage", row.Source)
	}
}

func TestNamePool_WithoutThePoolNamingIsTheOldRefSuffix(t *testing.T) {
	m, _ := namerFixture(t)
	got := m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54")})[poolSidA]
	if want := vname(t, "purdex-54", poolSidA); got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

// settle runs the newcomer rule over the live entries with the names the store
// holds, as an inventory pass does.
func (e *poolEnv) settle(entries []ipeers.Entry, lineage map[string][]string) map[string]string {
	e.t.Helper()
	var cands []nameCandidate
	for _, en := range entries {
		cands = append(cands, nameCandidate{sid: en.SessionID, ref: ipeers.RefID(en.SessionID), registryName: en.Name, previousRefs: lineage[en.SessionID]})
	}
	names := e.m.resolveNames(context.Background(), cands)
	e.m.settlePoolCollisions(context.Background(), entries, nil, lineage, names)
	return names
}

func TestNamePool_TheLaterProcessOfTwoLiveHoldersPicksAgain(t *testing.T) {
	e := newPoolEnv(t)
	holder := e.start(poolSidA, tEarly)
	resumed := e.start(poolSidB, tLate) // a resume is a new process
	e.assign(poolSidA, "purdex-54-adam", store.PeerNameSourcePool)
	e.assign(poolSidB, "purdex-54-adam", store.PeerNameSourcePool)

	names := e.settle([]ipeers.Entry{holder, resumed}, nil)
	if names[poolSidA] != "purdex-54-adam" {
		t.Errorf("the holder's name = %q, want it kept", names[poolSidA])
	}
	if names[poolSidB] != "purdex-54-emma" {
		t.Errorf("the resumed conversation's name = %q, want purdex-54-emma", names[poolSidB])
	}
	if row := storedRow(t, e.meta, poolSidB); row != (store.PeerNameEntry{Name: "purdex-54-emma", Source: store.PeerNameSourcePool}) {
		t.Errorf("stored row = %+v", row)
	}
	if row := storedRow(t, e.meta, poolSidA); row.Name != "purdex-54-adam" {
		t.Errorf("the holder's stored name = %q", row.Name)
	}
}

func TestNamePool_AResumedConversationWhoseNameIsFreeKeepsIt(t *testing.T) {
	e := newPoolEnv(t)
	resumed := e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-54-adam", store.PeerNameSourcePool)
	if names := e.settle([]ipeers.Entry{resumed}, nil); names[poolSidB] != "purdex-54-adam" {
		t.Fatalf("name = %q, want adam kept", names[poolSidB])
	}
}

// A pass remembers nothing: the holder, a die-and-resume between two passes and
// the resumed process all show in the one pass's registry.
func TestNamePool_ADieAndResumeBetweenPassesStillYieldsToTheLiveHolder(t *testing.T) {
	e := newPoolEnv(t)
	holder := e.start(poolSidA, tEarly)
	e.assign(poolSidA, "purdex-54-adam", store.PeerNameSourcePool)
	old := e.start(poolSidB, tEarly.Add(time.Minute))
	e.assign(poolSidB, "purdex-54-adam", store.PeerNameSourcePool) // B's earlier life; A took the name since
	e.stop(old)
	// Pass 1 sees B dead.
	e.settle([]ipeers.Entry{holder}, nil)
	// B resumes, in between passes; pass 2 is the first to see the new process.
	resumed := e.start(poolSidB, tLate)
	names := e.settle([]ipeers.Entry{holder, resumed}, nil)
	if names[poolSidA] != "purdex-54-adam" || names[poolSidB] == "purdex-54-adam" {
		t.Fatalf("names = %v, want A to keep adam and B to move", names)
	}
}

func TestNamePool_AFreshModuleOverStoredRowsRenamesOnlyTheLaterOfTwoSharedNames(t *testing.T) {
	e := newPoolEnv(t)
	a := e.start(poolSidA, tEarly)
	b := e.start(poolSidB, tLate)
	e.assign(poolSidA, "p-adam", store.PeerNameSourcePool)
	e.assign(poolSidB, "p-adam", store.PeerNameSourcePool)
	// A daemon restart: a new Module over the same store and registry.
	fresh := New(nil, nil).WithPeerNames(e.meta.PeerNames(), e.meta.ConversationNames()).WithNamePool(firstFree{})
	fresh.registryDir, fresh.liveness, fresh.logf = e.dir, e.m.liveness, e.m.logf
	names := fresh.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "p"), cand(poolSidB, "p")})
	fresh.settlePoolCollisions(context.Background(), []ipeers.Entry{a, b}, nil, nil, names)
	if names[poolSidA] != "p-adam" || names[poolSidB] == "p-adam" {
		t.Fatalf("names = %v, want only the later one renamed", names)
	}
}

func TestNamePool_EqualStartsMakeTheLargerSessionIDYield(t *testing.T) {
	e := newPoolEnv(t)
	a := e.start(poolSidA, tEarly)
	b := e.start(poolSidB, tEarly)
	e.assign(poolSidA, "p-adam", store.PeerNameSourcePool)
	e.assign(poolSidB, "p-adam", store.PeerNameSourcePool)
	names := e.settle([]ipeers.Entry{a, b}, nil)
	if names[poolSidA] != "p-adam" || names[poolSidB] == "p-adam" {
		t.Fatalf("names = %v, want the larger session id (B) to move", names)
	}
}

func TestNamePool_APredecessorAndItsSuccessorSharingANameNeverRename(t *testing.T) {
	e := newPoolEnv(t)
	pred := e.start(poolSidA, tEarly)
	succ := e.start(poolSidB, tLate)
	e.assign(poolSidA, "p-adam", store.PeerNameSourcePool)
	e.assign(poolSidB, "p-adam", store.PeerNameSourceLineage)
	lineage := map[string][]string{poolSidB: {ipeers.RefID(poolSidA)}}
	names := e.settle([]ipeers.Entry{pred, succ}, lineage)
	if names[poolSidA] != "p-adam" || names[poolSidB] != "p-adam" {
		t.Fatalf("names = %v, want both to keep p-adam", names)
	}
}

func TestNamePool_ALocalConversationYieldsToARemoteHolderOnlyWhenTheRemoteStartedFirst(t *testing.T) {
	for _, c := range []struct {
		name        string
		remoteStart time.Time
		wantYield   bool
	}{
		{"remote earlier", tEarly.Add(-time.Hour), true},
		{"remote later", tLate.Add(time.Hour), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newPoolEnv(t)
			local := e.start(poolSidA, tEarly)
			e.assign(poolSidA, "p-adam", store.PeerNameSourcePool)
			e.holdRemote(c.remoteStart, "adam")
			names := e.settle([]ipeers.Entry{local}, nil)
			if yielded := names[poolSidA] != "p-adam"; yielded != c.wantYield {
				t.Fatalf("name = %q, yielded = %v, want %v", names[poolSidA], yielded, c.wantYield)
			}
		})
	}
}

// A store that answers a rename the way the test says.
type renameStore struct {
	PeerNameStore
	answer func(old string) (store.PeerNameEntry, bool, error)
}

func (r renameStore) RenamePool(_ context.Context, _, old, _ string) (store.PeerNameEntry, bool, error) {
	return r.answer(old)
}

func TestNamePool_ARenameThatLosesTheCompareAndSetKeepsTheStoredNameAndLogs(t *testing.T) {
	e := newPoolEnv(t)
	a := e.start(poolSidA, tEarly)
	b := e.start(poolSidB, tLate)
	e.assign(poolSidA, "p-adam", store.PeerNameSourcePool)
	e.assign(poolSidB, "p-adam", store.PeerNameSourcePool)
	e.m.peerNames = renameStore{PeerNameStore: e.m.peerNames, answer: func(string) (store.PeerNameEntry, bool, error) {
		return store.PeerNameEntry{Name: "lead-omar", Source: store.PeerNameSourceLineage}, false, nil
	}}
	names := e.settle([]ipeers.Entry{a, b}, nil)
	if names[poolSidB] != "lead-omar" {
		t.Fatalf("name = %q, want the stored lead-omar", names[poolSidB])
	}
	if !e.logged("changed under the rename") {
		t.Fatal("the lost compare-and-set was not logged")
	}
}

func TestNamePool_ARenameStoreErrorKeepsTheOldNameForThisPass(t *testing.T) {
	e := newPoolEnv(t)
	a := e.start(poolSidA, tEarly)
	b := e.start(poolSidB, tLate)
	e.assign(poolSidA, "p-adam", store.PeerNameSourcePool)
	e.assign(poolSidB, "p-adam", store.PeerNameSourcePool)
	e.m.peerNames = renameStore{PeerNameStore: e.m.peerNames, answer: func(string) (store.PeerNameEntry, bool, error) {
		return store.PeerNameEntry{}, false, errors.New("busy")
	}}
	names := e.settle([]ipeers.Entry{a, b}, nil)
	if names[poolSidB] != "p-adam" {
		t.Fatalf("name = %q, want p-adam kept", names[poolSidB])
	}
	if !e.logged("failed") {
		t.Fatal("the failed rename was not logged")
	}
}

func TestNamePool_ARenameWithEveryPoolNameHeldTakesTheRefSuffix(t *testing.T) {
	e := newPoolEnv(t)
	a := e.start(poolSidA, tEarly)
	b := e.start(poolSidB, tLate)
	e.assign(poolSidA, "p-adam", store.PeerNameSourcePool)
	e.assign(poolSidB, "p-adam", store.PeerNameSourcePool)
	e.holdRemote(tLate.Add(time.Hour), ipeers.NamePool[:]...)
	names := e.settle([]ipeers.Entry{a, b}, nil)
	if want := vname(t, "p", poolSidB); names[poolSidB] != want {
		t.Fatalf("name = %q, want the ref form %q", names[poolSidB], want)
	}
}

// A Module without the pool never renames, even over rows that already carry
// pool names (the pool was switched off after they were assigned).
func TestNamePool_WithoutThePoolTwoSharedNamesAreLeftAlone(t *testing.T) {
	e := newPoolEnv(t)
	a := e.start(poolSidA, tEarly)
	b := e.start(poolSidB, tLate)
	e.assign(poolSidA, "p-adam", store.PeerNameSourcePool)
	e.assign(poolSidB, "p-adam", store.PeerNameSourcePool)
	e.m.WithNamePool(nil)
	names := e.settle([]ipeers.Entry{a, b}, nil)
	if names[poolSidA] != "p-adam" || names[poolSidB] != "p-adam" {
		t.Fatalf("names = %v, want both left alone", names)
	}
}

// When the live registry cannot be read the held names are unknown, so the
// conversation stays unnamed this pass (its ref form) and nothing is stored —
// a name, once assigned, is never corrected.
func TestNamePool_AnUnreadableRegistryNamesNothingThisPass(t *testing.T) {
	e := newPoolEnv(t)
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.m.registryDir = notADir
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54")})
	if len(got) != 0 {
		t.Fatalf("names = %v, want none", got)
	}
	if row := storedRow(t, e.meta, poolSidA); row != (store.PeerNameEntry{}) {
		t.Fatalf("stored row = %+v, want none", row)
	}
	if !e.logged("virtual names") {
		t.Fatal("the store error was not logged")
	}
}
