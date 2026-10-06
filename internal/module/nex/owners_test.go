package nex

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/module/agent"
	"lab.protype.tw/wake/nexen/store"
)

func row(id, state string, archived bool, sid, resume string, created int64) store.Execution {
	e := store.Execution{ID: id, State: store.State(state), SessionID: sid, ResumeSessionID: resume, CreatedAt: created}
	if archived {
		e.ArchivedAt = 1
	}
	return e
}

func TestIsLiveExecution(t *testing.T) {
	cases := map[string]struct {
		e    store.Execution
		want bool
	}{
		"idle":                {row("a", "idle", false, "", "", 1), true},
		"running":             {row("a", "running", false, "", "", 1), true},
		"queued":              {row("a", "queued", false, "", "", 1), true},
		"failed":              {row("a", "failed", false, "", "", 1), true},
		"rejected":            {row("a", "rejected", false, "", "", 1), true},
		"terminated":          {row("a", "terminated", false, "", "", 1), false},
		"idle archived":       {row("a", "idle", true, "", "", 1), false},
		"terminated archived": {row("a", "terminated", true, "", "", 1), false},
	}
	for name, c := range cases {
		if got := isLiveExecution(c.e); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestExecutionIsFor(t *testing.T) {
	if !executionIsFor(row("a", "idle", false, "S", "", 1), "S") || !executionIsFor(row("a", "idle", false, "", "S", 1), "S") {
		t.Fatal("session_id / resume_session_id should match")
	}
	if executionIsFor(row("a", "idle", false, "", "", 1), "") || executionIsFor(row("a", "idle", false, "X", "Y", 1), "S") {
		t.Fatal("empty sid and non-matching rows must not match")
	}
}

func TestLiveWorkersFor_PagesFiltersAndOrders(t *testing.T) {
	env := newTakebackEnv(t)
	var rows []store.Execution
	for i := 0; i < ownerScanPageSize+3; i++ { // forces a second page
		rows = append(rows, row(fmt.Sprintf("01%04d", i), "terminated", false, "OTHER", "", int64(i)))
	}
	rows = append(rows,
		row("09a", "idle", false, "S", "", 100),
		row("09b", "rejected", false, "", "S", 200), // matched by resume_session_id
		row("09c", "terminated", false, "S", "", 300),
		row("09d", "idle", true, "S", "", 400), // archived: List never returns it
	)
	env.store.listRows = rows

	got, err := env.m.liveWorkersFor(context.Background(), "S")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	if !reflect.DeepEqual(ids, []string{"09b", "09a"}) {
		t.Fatalf("ids = %v, want [09b 09a] (newest first, live only)", ids)
	}
	if env.store.listCalls < 2 {
		t.Fatalf("listCalls = %d, want >= 2 (cursor followed)", env.store.listCalls)
	}
}

func TestLiveWorkersFor_CapAndErrors(t *testing.T) {
	env := newTakebackEnv(t)
	env.store.listRows = make([]store.Execution, ownerScanPageSize*ownerScanMaxPages+1)
	for i := range env.store.listRows {
		env.store.listRows[i] = row(fmt.Sprintf("%06d", i), "idle", false, "S", "", int64(i))
	}
	got, err := env.m.liveWorkersFor(context.Background(), "S")
	if !errors.Is(err, errOwnerScanTruncated) || len(got) != ownerScanPageSize*ownerScanMaxPages {
		t.Fatalf("got %d, err %v", len(got), err)
	}

	env.store.listRows, env.store.listErr = nil, errors.New("db down")
	if _, err := env.m.liveWorkersFor(context.Background(), "S"); err == nil {
		t.Fatal("want the store error")
	}
	if got, err := env.m.liveWorkersFor(context.Background(), ""); err != nil || got != nil {
		t.Fatalf("empty sid: %v %v", got, err)
	}
}

// pageTwoFails: S's live worker "000000" sits on page 1 of a two-page table,
// and the second List call fails.
func pageTwoFails(st *fakeNexStore) {
	rows := []store.Execution{row("000000", "idle", false, "S", "", 1)}
	for i := 1; i <= ownerScanPageSize; i++ {
		rows = append(rows, row(fmt.Sprintf("%06d", i), "terminated", false, "OTHER", "", int64(i+1)))
	}
	st.listRows, st.listErr, st.listErrAt = rows, errors.New("db down"), 2
}

// PR #1590 R1-2: a page error keeps the matches found before it (the
// contract Q1 and the reconcile rely on); the error still comes back.
func TestLiveWorkersFor_PageErrorKeepsWhatWasFound(t *testing.T) {
	env := newTakebackEnv(t)
	pageTwoFails(env.store)
	got, err := env.m.liveWorkersFor(context.Background(), "S")
	if err == nil || errors.Is(err, errOwnerScanTruncated) {
		t.Fatalf("err = %v, want the store error", err)
	}
	if len(got) != 1 || got[0].ID != "000000" {
		t.Fatalf("got %v, want the page-1 match", got)
	}
	if env.store.listCalls != 2 {
		t.Fatalf("listCalls = %d, want 2", env.store.listCalls)
	}
}

func TestCheckOwners(t *testing.T) {
	ts := func(pane string, verified bool) agent.TerminalSession {
		return agent.TerminalSession{FrameID: "f" + pane, PaneID: pane, AgentType: "cc", SessionID: "S", Verified: verified}
	}
	t.Run("free", func(t *testing.T) {
		env := newTakebackEnv(t)
		if herr := env.m.checkOwners(context.Background(), "S", "", ""); herr != nil {
			t.Fatal(herr)
		}
	})
	t.Run("verified terminal elsewhere → 409 session_owned", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{"S": {ts("%9", true)}}
		herr := env.m.checkOwners(context.Background(), "S", "", "")
		if herr == nil || herr.status != 409 || herr.code != "session_owned" || herr.detail["owner"] != "terminal" || herr.detail["tmux_pane_id"] != "%9" {
			t.Fatalf("herr=%+v", herr)
		}
	})
	t.Run("unverifiable terminal elsewhere → 503 owner_check_failed (D1: not an owner, but not provably free)", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{"S": {ts("%9", false)}}
		herr := env.m.checkOwners(context.Background(), "S", "", "")
		if herr == nil || herr.status != 503 || herr.code != "owner_check_failed" || herr.detail["tmux_pane_id"] != "%9" {
			t.Fatalf("herr=%+v", herr)
		}
	})
	t.Run("terminal in the allowed pane", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.terminals.live = map[string][]agent.TerminalSession{"S": {ts("%1", true)}}
		if herr := env.m.checkOwners(context.Background(), "S", "", "%1"); herr != nil {
			t.Fatal(herr)
		}
	})
	t.Run("live worker other than the allowed one", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1), row("E2", "failed", false, "S", "", 2)}
		herr := env.m.checkOwners(context.Background(), "S", "E1", "")
		if herr == nil || herr.detail["owner"] != "worker" || herr.detail["execution_id"] != "E2" {
			t.Fatalf("herr = %+v", herr)
		}
		env.store.listRows = []store.Execution{row("E1", "idle", false, "S", "", 1)}
		if herr := env.m.checkOwners(context.Background(), "S", "E1", ""); herr != nil {
			t.Fatal(herr)
		}
	})
	t.Run("lookup errors → 503", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.terminals.err = errors.New("db")
		if herr := env.m.checkOwners(context.Background(), "S", "", ""); herr == nil || herr.status != 503 || herr.code != "owner_check_failed" {
			t.Fatalf("herr = %+v", herr)
		}
		env.terminals.err = nil
		env.store.listErr = errors.New("db")
		if herr := env.m.checkOwners(context.Background(), "S", "", ""); herr == nil || herr.code != "owner_check_failed" {
			t.Fatalf("herr = %+v", herr)
		}
	})
	t.Run("a page error after a match → 503 (partial results never prove absence)", func(t *testing.T) {
		env := newTakebackEnv(t)
		pageTwoFails(env.store)
		herr := env.m.checkOwners(context.Background(), "S", "", "")
		if herr == nil || herr.status != 503 || herr.code != "owner_check_failed" {
			t.Fatalf("herr = %+v", herr)
		}
		// Even when the only match is the allowed execution: absence of others is unproven.
		env.store.listCalls = 0 // page 1 answers again, page 2 fails again
		if herr := env.m.checkOwners(context.Background(), "S", "000000", ""); herr == nil || herr.code != "owner_check_failed" {
			t.Fatalf("allowed match, herr = %+v", herr)
		}
	})
}
