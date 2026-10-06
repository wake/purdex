package nex

import (
	"context"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/module/agent"
	"lab.protype.tw/wake/nexen/store"
)

// Nexen stores the provider-reported session_id verbatim and filters on
// lower(session_id): a row stored upper-case is S's worker all the same.
func TestOwners_UpperCaseStoredSessionIDIsStillS(t *testing.T) {
	up := strings.ToUpper(tS)
	env := newTakebackEnv(t)
	env.store.listRows = []store.Execution{row("E1", "idle", false, up, "", 1)}
	got, err := env.m.liveWorkersFor(context.Background(), tS)
	if err != nil || len(got) != 1 || got[0].SessionID != up {
		t.Fatalf("%v %v", got, err)
	}
	herr := env.m.checkOwners(context.Background(), tS, "", "")
	if herr == nil || herr.code != "session_owned" || herr.detail["owner"] != "worker" {
		t.Fatalf("herr = %+v", herr)
	}
}

func TestManualResume_ExitsAnUpperCaseStoredRow(t *testing.T) {
	env := newHandoffEnv(t)
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, strings.ToUpper(tS), "", 1)}
	env.svc.lease = store.Lease{ID: "L"}
	env.m.onSessionStart(ev("resume"))
	if ids := archivedIDs(env); len(ids) != 1 || ids[0] != "E1" {
		t.Fatalf("archived = %v", ids)
	}
}

// One session in two cases is one reconcile group, not two.
func TestReconcile_GroupsSessionIDsCaseInsensitively(t *testing.T) {
	env := newHandoffEnv(t)
	verifiedTerminals(env, tS)
	env.svc.lease = store.Lease{ID: "L"}
	fakeStore(env).listRows = []store.Execution{
		row("E1", "idle", false, strings.ToUpper(tS), tS, 1),
		row("E2", "idle", false, tS, "", 2),
	}
	env.m.onSessionStart(agent.SessionStartEvent{Overflow: true})
	if ids := archivedIDs(env); len(ids) != 2 {
		t.Fatalf("archived = %v, want E1 and E2 once each", ids)
	}
	if n := len(env.svc.terminateCalls); n != 2 {
		t.Fatalf("terminate calls = %d, want 2 (no double handling)", n)
	}
	lookups := 0
	for _, o := range fakeStore(env).allListOpts {
		if o.SessionID != "" {
			lookups++
		}
	}
	if lookups > 2 {
		t.Fatalf("per-session scans = %d: the two cases formed two groups", lookups)
	}
}
