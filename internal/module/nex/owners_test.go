package nex

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

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
