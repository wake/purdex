package convfeed

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeExec struct {
	owner Owner
	ok    bool
	err   error
	calls int
}

func (f *fakeExec) LiveExecution(context.Context, string) (Owner, bool, error) {
	f.calls++
	return f.owner, f.ok, f.err
}

func TestResolve_ExecutionOwnerWhenNoTerminal(t *testing.T) {
	e := newResEnv(t)
	p := e.put("-w", `{"a":1}`+"\n")
	x := &fakeExec{ok: true, owner: Owner{Backend: "execution", Status: "waiting", TranscriptPath: p, SeenAt: 1}}
	r := &Resolver{Home: e.home, Owners: fakeOwners{}, ExecOwners: x}
	s, err := resolve(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Live || s.Backend != "execution" || s.Status != "waiting" || s.Path != p {
		t.Fatalf("source = %+v", s)
	}
}

func TestResolve_TerminalFirstExecutionNotAsked(t *testing.T) {
	e := newResEnv(t)
	p := e.put("-w", `{"a":1}`+"\n")
	x := &fakeExec{ok: true, owner: Owner{Backend: "execution", Status: "waiting"}}
	r := &Resolver{Home: e.home, Owners: fakeOwners{owners: []Owner{{TranscriptPath: p, Status: "idle"}}}, ExecOwners: x}
	s, err := resolve(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend != "terminal" || s.Status != "idle" || x.calls != 0 {
		t.Fatalf("source = %+v, exec calls %d", s, x.calls)
	}
}

func TestResolve_TerminalErrorNotOverriddenByExecution(t *testing.T) {
	e := newResEnv(t)
	e.put("-w", `{"a":1}`+"\n")
	x := &fakeExec{ok: true, owner: Owner{Backend: "execution", Status: "running"}}
	r := &Resolver{Home: e.home, Owners: fakeOwners{err: errors.New("boom")}, ExecOwners: x}
	s, err := resolve(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live || s.Backend != "" || s.Status != "unknown" || x.calls != 0 {
		t.Fatalf("source = %+v, exec calls %d", s, x.calls)
	}
}

func TestResolve_ExecutionLookupErrorIsUnknown(t *testing.T) {
	e := newResEnv(t)
	e.put("-w", `{"a":1}`+"\n")
	r := &Resolver{Home: e.home, Owners: fakeOwners{}, ExecOwners: &fakeExec{err: errors.New("boom")}}
	s, err := resolve(t, r)
	if err != nil {
		t.Fatal(err)
	}
	if s.Live || s.Status != "unknown" {
		t.Fatalf("source = %+v", s)
	}
}

func TestEntry_ExecutionBackendBumpsOnlyOnChange(t *testing.T) {
	m := newMem(idle(1)...)
	e := NewEntry(sidA)
	t0 := time.Unix(1000, 0)
	s := src(m, "f1", true)
	s.Status, s.Backend, s.StatusAt = "running", "execution", t0
	refresh(t, e, s)
	if h := e.Header(); h.Backend != "execution" || h.Status != "running" || !h.Live {
		t.Fatalf("header = %+v", h)
	}
	rev := e.Revision()
	s.StatusAt = t0.Add(time.Second)
	refresh(t, e, s)
	if e.Revision() != rev {
		t.Fatalf("an unchanged reading bumped the revision")
	}
	s.Status, s.StatusAt = "waiting", t0.Add(2*time.Second)
	refresh(t, e, s)
	if e.Revision() == rev || e.Header().Status != "waiting" {
		t.Fatalf("a changed light did not bump: %+v", e.Header())
	}
}

func TestResolve_NoExecLookupUnchanged(t *testing.T) {
	e := newResEnv(t)
	e.put("-w", `{"a":1}`+"\n")
	s, err := resolve(t, &Resolver{Home: e.home, Owners: fakeOwners{}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Live || s.Backend != "" || s.Status != "ended" {
		t.Fatalf("source = %+v", s)
	}
}
