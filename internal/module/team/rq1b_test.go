package teammod

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// RQ-1b (#2062): the rule that spends a quota, behind the hostconfig switch relay_quota (default off).

// fakeQuotaRule is the hostconfig RelayQuotaReader of these tests.
type fakeQuotaRule struct {
	mu  sync.Mutex
	on  bool
	err error
}

func (q *fakeQuotaRule) RelayQuotaRule() (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.on, q.err
}

func (q *fakeQuotaRule) set(on bool, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.on, q.err = on, err
}

func (f *fixture) selfLeft(sid string) int {
	f.t.Helper()
	q, _, err := f.m.store.RelayQuotaOf(sid)
	if err != nil {
		f.t.Fatal(err)
	}
	return q.SelfLeft
}

func (f *fixture) setQuota(sid string, self int) {
	f.t.Helper()
	if _, _, err := f.m.store.SetRelayQuota(sid, &self, nil, f.clock.Load(), "app"); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) rowState(id string) team.State {
	f.t.Helper()
	a, ok, err := f.m.store.Get(id)
	if err != nil || !ok {
		f.t.Fatalf("get %s: %v %v", id, ok, err)
	}
	return a.State
}

// Rule off (the default): unattended mode behaves exactly as U23 did, whatever the quota says. Mutation gate: spend with
// the switch off → red.
func TestQuotaRule_OffSpendsNothing(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true)
	f.setQuota("sid-1", 0)
	b := f.begin("sid-1") // approved at begin although the quota is 0
	if st := f.rowState(b.RequestID); st != team.StateApproved {
		t.Fatalf("rule off: row %s, want approved as in U23", st)
	}
	f.setQuota("sid-2", 5)
	f.begin("sid-2")
	if n := f.selfLeft("sid-2"); n != 5 {
		t.Fatalf("rule off: self_left %d, want untouched 5", n)
	}
}

// Rule on: an automatic approval at begin spends one, in the same transaction as the approval. Mutation gate: no spend → red.
func TestQuotaRule_OnSpendsOnTheAutomaticApprovalAtBegin(t *testing.T) {
	f := newFixture(t)
	f.qrule.set(true, nil)
	f.unatt.set(true)
	f.setQuota("sid-1", 2)
	b := f.begin("sid-1")
	if st := f.rowState(b.RequestID); st != team.StateApproved || f.selfLeft("sid-1") != 1 {
		t.Fatalf("row %s, self_left %d; want approved and 1", f.rowState(b.RequestID), f.selfLeft("sid-1"))
	}
}

// Exhausted at begin: the request opens for a person exactly as with unattended off — op awaiting approval, `opened`,
// nothing spent, never approved. Mutation gate: approve anyway → red.
func TestQuotaRule_ExhaustedAtBeginOpensForAPerson(t *testing.T) {
	f := newFixture(t)
	f.qrule.set(true, nil)
	f.unatt.set(true)
	f.events()
	b := f.begin("sid-1") // no quota row: 0
	if st := f.rowState(b.RequestID); st != team.StateOpen {
		t.Fatalf("row %s, want open", st)
	}
	if op := f.op(b.Op.ID); op.State != team.RelayAwaitingApproval {
		t.Fatalf("op %s, want awaiting_approval", op.State)
	}
	if ops := f.opsOf(); len(ops) != 1 || ops[0] != "opened" {
		t.Fatalf("events = %v, want [opened]", ops)
	}
	if n := f.selfLeft("sid-1"); n != 0 {
		t.Fatalf("self_left %d after a refusal, want 0", n)
	}
}

// The sweeps hold an exhausted row quietly (one log line, listed in the unattended view) and approve it within one
// tick of the user raising the quota. Mutation gate: log every tick → red; never release → red.
func TestQuotaRule_SweepHoldsQuietlyAndApprovesOnceTheQuotaIsRaised(t *testing.T) {
	f := newFixture(t)
	logs := f.logs()
	b := f.begin("sid-1") // unattended off: an open request
	f.qrule.set(true, nil)
	f.unatt.set(true)
	for range 4 {
		f.m.tick()
	}
	if st := f.rowState(b.RequestID); st != team.StateOpen {
		t.Fatalf("row %s, want held open", st)
	}
	if n := countLines(logs(), "not auto-approved"); n != 1 {
		t.Fatalf("%d refusal lines in %q, want exactly 1", n, logs())
	}
	if _, v, _ := f.getUnattended(""); len(v.Held) != 1 || v.Held[0].ID != b.RequestID {
		t.Fatalf("held = %+v, want the open row", v.Held)
	}
	// the user raises the quota from the App: the next tick approves it and spends one
	if code, _, body := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1", SelfLeft: ip(1), Client: appClient2}); code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	f.m.tick()
	if st := f.rowState(b.RequestID); st != team.StateApproved || f.selfLeft("sid-1") != 0 {
		t.Fatalf("row %s self_left %d, want approved and 0", f.rowState(b.RequestID), f.selfLeft("sid-1"))
	}
	if _, v, _ := f.getUnattended(""); len(v.Held) != 0 {
		t.Fatalf("held = %+v after the approval, want none", v.Held)
	}
}

// A person's click spends nothing, with the rule on and the quota at 0. Mutation gate: spend on a click → red.
func TestQuotaRule_AClickSpendsNothing(t *testing.T) {
	f := newFixture(t)
	f.qrule.set(true, nil)
	f.setQuota("sid-1", 3)
	b := f.begin("sid-1") // unattended off
	if code, body := f.decide(b.RequestID, "approve"); code != http.StatusOK {
		t.Fatalf("click: %d %s", code, body)
	}
	if n := f.selfLeft("sid-1"); n != 3 {
		t.Fatalf("self_left %d after a click, want 3", n)
	}
}

// A switch that cannot be read is off (the host behaves as before the rule). Mutation gate: treat an error as on → red.
func TestQuotaRule_AnUnreadableSwitchIsOff(t *testing.T) {
	f := newFixture(t)
	f.qrule.set(true, errors.New("bad stored value"))
	f.unatt.set(true)
	b := f.begin("sid-1") // quota 0, rule unreadable: approved as U23 did
	if st := f.rowState(b.RequestID); st != team.StateApproved {
		t.Fatalf("row %s, want approved (switch off)", st)
	}
}

// Of two automatic approvals racing for the last unit of one chain exactly one wins; the other's transaction rolls
// back with its row open. Mutation gate: read-then-write instead of the guarded UPDATE → red (or flaky).
func TestQuotaRule_TwoApprovalsForTheLastUnit(t *testing.T) {
	s := openTestStore(t)
	lineage(t, s, "s1", "root")
	lineage(t, s, "s2", "root")
	one := 1
	if _, _, err := s.SetRelayQuota("root", &one, nil, 1, "app"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a1", "a2"} {
		a := openApproval(id, "s"+id[1:], 1000)
		a.Kind = team.KindSelfRelay
		if _, _, _, err := s.Create(a, "h-"+id); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wins := make([]bool, 2)
	for i, id := range []string{"a1", "a2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := daemonClose(2000, nil)
			c.SpendQuota = true
			_, wins[i], _, errs[i] = s.CloseSelfRelayApproved(id, c, "s"+id[1:])
		}()
	}
	wg.Wait()
	won, exhausted := 0, 0
	for i := range errs {
		switch {
		case errs[i] == nil && wins[i]:
			won++
		case errors.Is(errs[i], ErrQuotaExhausted):
			exhausted++
		}
	}
	if won != 1 || exhausted != 1 {
		t.Fatalf("wins=%v errs=%v, want one approval and one ErrQuotaExhausted", wins, errs)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM approval_requests WHERE state = 'open'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d rows still open (%v), want the loser's one", n, err)
	}
	if q, _, _ := s.RelayQuotaOf("s1"); q.SelfLeft != 0 {
		t.Fatalf("self_left %d, want 0 (never negative)", q.SelfLeft)
	}
}

// SpendQuota is honoured for an Auto close only: a forged non-Auto close spends nothing.
func TestQuotaRule_SpendQuotaWithoutAutoSpendsNothing(t *testing.T) {
	s := openTestStore(t)
	two := 2
	if _, _, err := s.SetRelayQuota("s1", &two, nil, 1, "app"); err != nil {
		t.Fatal(err)
	}
	a := openApproval("a1", "s1", 1000)
	a.Kind = team.KindSelfRelay
	if _, _, _, err := s.Create(a, "h"); err != nil {
		t.Fatal(err)
	}
	app := team.Client{Kind: "app", Label: "x"}
	if _, won, _, err := s.CloseSelfRelayApproved("a1", Close{State: team.StateApproved, DecidedAt: 2000, DecidedBy: &app, SpendQuota: true}, "s1"); err != nil || !won {
		t.Fatalf("close: %v %v", won, err)
	}
	if q, _, _ := s.RelayQuotaOf("s1"); q.SelfLeft != 2 {
		t.Fatalf("self_left %d, want 2", q.SelfLeft)
	}
}

// Every session of a relay chain spends the one shared row (R3): A relayed, A′ asks again.
func TestQuotaRule_TheChainSharesOneQuota(t *testing.T) {
	f := newFixture(t)
	f.qrule.set(true, nil)
	f.unatt.set(true)
	lineage(t, f.m.store, "sid-1", "sid-0") // sid-1 is the successor of sid-0: one chain, root sid-0
	f.setQuota("sid-0", 1)
	b := f.begin("sid-1")
	if st := f.rowState(b.RequestID); st != team.StateApproved || f.selfLeft("sid-0") != 0 {
		t.Fatalf("row %s, chain self_left %d, want approved and 0 on the root", f.rowState(b.RequestID), f.selfLeft("sid-0"))
	}
}

// The 610 rule: what the rule adds to one automatic approval. A 10-deep chain on a lineage of 5 000 other rows; each
// iteration closes one fresh open self_relay row with the spend, in its own transaction on a file database.
func BenchmarkQuotaRule_ApproveWithSpendOn10DeepChain(b *testing.B) {
	s, err := OpenStore(b.TempDir() + "/team.db")
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 5000; i++ {
		if _, err := s.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES (?, ?, '_x', ?, 1)`,
			fmt.Sprintf("o%d", i), fmt.Sprintf("o%d-pred", i), fmt.Sprintf("op%d", i)); err != nil {
			b.Fatal(err)
		}
	}
	for i := 1; i <= 10; i++ {
		if _, err := s.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES (?, ?, '_x', ?, 1)`, fmt.Sprintf("c%d", i), fmt.Sprintf("c%d", i-1), fmt.Sprintf("cop%d", i)); err != nil {
			b.Fatal(err)
		}
	}
	big := 99
	if _, _, err := s.SetRelayQuota("c0", &big, nil, 1, "app"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if i%90 == 0 { // keep the chain's quota above 0
			if _, _, err := s.SetRelayQuota("c0", &big, nil, 1, "app"); err != nil {
				b.Fatal(err)
			}
		}
		id := fmt.Sprintf("a%d", i)
		a := openApproval(id, "c10", 1000)
		a.Kind = team.KindSelfRelay
		if _, _, _, err := s.Create(a, "h-"+id); err != nil {
			b.Fatal(err)
		}
		c := daemonClose(2000, nil)
		c.SpendQuota = true
		b.StartTimer()
		if _, won, _, err := s.CloseSelfRelayApproved(id, c, "c10"); err != nil || !won {
			b.Fatalf("close: %v %v", won, err)
		}
	}
}

// Same without the spend, for the difference.
func BenchmarkQuotaRule_ApproveWithoutSpend(b *testing.B) {
	s, err := OpenStore(b.TempDir() + "/team.db")
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		id := fmt.Sprintf("a%d", i)
		a := openApproval(id, "c10", 1000)
		a.Kind = team.KindSelfRelay
		if _, _, _, err := s.Create(a, "h-"+id); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, won, _, err := s.CloseSelfRelayApproved(id, daemonClose(2000, nil), "c10"); err != nil || !won {
			b.Fatalf("close: %v %v", won, err)
		}
	}
}
