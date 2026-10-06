package nex

// D23 against the REAL embedded Nexen (≥ v0.18.0, nexen#113), through the
// mount fixture's real nexen.Assemble: the exit's archive carries the
// terminate's lease, the engine checks it in the archive's own UPDATE, and
// a lease that changed hands after the terminate is not archived over.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"
)

// engineSpy wraps the real service: it records the Terminate and Archive
// requests (and the archive's answer), and with handoverTo set, hands the
// lease over right after a successful Terminate — released as its holder,
// then acquired by handoverTo — the window between terminate and archive
// that #113 closes.
type engineSpy struct {
	nexService
	handoverTo string

	mu          sync.Mutex
	terminates  []execution.TerminateRequest
	archives    []execution.ArchiveRequest
	archiveErrs []error
	handoverErr error
}

func (s *engineSpy) Terminate(ctx context.Context, req execution.TerminateRequest) error {
	s.mu.Lock()
	s.terminates = append(s.terminates, req)
	s.mu.Unlock()
	if err := s.nexService.Terminate(ctx, req); err != nil || s.handoverTo == "" {
		return err
	}
	var herr error
	if err := s.nexService.ReleaseLease(ctx, req.ExecutionID, req.LeaseID, req.PrincipalID); err != nil {
		herr = fmt.Errorf("handover: release: %w", err)
	} else if _, err := s.nexService.AcquireLease(ctx, req.ExecutionID, s.handoverTo); err != nil {
		herr = fmt.Errorf("handover: acquire: %w", err)
	}
	s.mu.Lock()
	s.handoverErr = herr
	s.mu.Unlock()
	return nil
}

func (s *engineSpy) Archive(ctx context.Context, req execution.ArchiveRequest) error {
	err := s.nexService.Archive(ctx, req)
	s.mu.Lock()
	s.archives = append(s.archives, req)
	s.archiveErrs = append(s.archiveErrs, err)
	s.mu.Unlock()
	return err
}

// realIdleWorker delegates one execution on a real engine, waits for its
// turn to finish, and spies on the module's service from here on.
func realIdleWorker(t *testing.T, handoverTo string) (*mountFixture, *engineSpy, store.Execution) {
	t.Helper()
	f := newMountFixture(t)
	id := f.delegate(t)
	f.waitTurnDone(t, id, 1)
	spy := &engineSpy{nexService: f.m.sys.service, handoverTo: handoverTo}
	f.m.sys.service = spy
	exec, err := f.m.getExecution(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if exec.State != store.StateIdle || exec.ArchivedAt != 0 {
		t.Fatalf("worker before exit = %+v, want idle and unarchived", exec)
	}
	return f, spy, exec
}

// archivedPrincipals is principal_id of every execution.archived event.
func (f *mountFixture) archivedPrincipals(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	for _, ev := range f.events(t, id) {
		if ev.Kind == "execution.archived" {
			out = append(out, principalOf(t, ev))
		}
	}
	return out
}

func TestExitWorker_RealEngine_IdleExitArchivesUnderTheTerminateLease(t *testing.T) {
	f, spy, exec := realIdleWorker(t, "")
	self := "pdx:" + f.hostID

	out, herr := f.m.exitWorker(context.Background(), exec, nil, self)
	if herr != nil || !out.Terminated || !out.Archived || out.State != store.StateTerminated {
		t.Fatalf("out=%+v herr=%+v", out, herr)
	}
	if len(spy.terminates) != 1 || len(spy.archives) != 1 {
		t.Fatalf("terminates=%+v archives=%+v", spy.terminates, spy.archives)
	}
	term, arch := spy.terminates[0], spy.archives[0]
	if arch.LeaseID == "" || arch.LeaseID != term.LeaseID || arch.PrincipalID != term.PrincipalID || arch.PrincipalID != self {
		t.Fatalf("archive under %q/%q, terminate under %q/%q: want the same lease, principal %q",
			arch.LeaseID, arch.PrincipalID, term.LeaseID, term.PrincipalID, self)
	}
	if spy.archiveErrs[0] != nil {
		t.Fatalf("fenced archive refused by the real engine: %v", spy.archiveErrs[0])
	}

	row, err := f.m.getExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != store.StateTerminated || row.ArchivedAt == 0 {
		t.Fatalf("row after exit = state %s archived_at %d, want terminated and archived", row.State, row.ArchivedAt)
	}
	if row.LeaseID != "" {
		t.Fatalf("row lease = %q after exit, want the own lease released (after the archive)", row.LeaseID)
	}
	if got := f.archivedPrincipals(t, exec.ID); len(got) != 1 || got[0] != self {
		t.Fatalf("execution.archived principals = %q, want [%q] (the lease holder)", got, self)
	}
}

func TestExitWorker_RealEngine_LeaseHandedOverAfterTerminateIsNotArchivedOver(t *testing.T) {
	const ploom = "ploom:agent-7"
	f, spy, exec := realIdleWorker(t, ploom)
	self := "pdx:" + f.hostID

	out, herr := f.m.exitWorker(context.Background(), exec, nil, self)
	if spy.handoverErr != nil {
		t.Fatalf("test setup: %v", spy.handoverErr)
	}
	if herr != nil {
		t.Fatalf("herr = %+v, want none: the worker is exited", herr)
	}
	if !out.Exited() || !out.Terminated || out.Archived || out.State != store.StateTerminated {
		t.Fatalf("out = %+v, want terminated, not archived", out)
	}
	if len(spy.archives) != 1 || spy.archives[0].LeaseID == "" {
		t.Fatalf("archives = %+v, want one fenced archive", spy.archives)
	}
	if !errors.Is(spy.archiveErrs[0], store.ErrLeaseMismatch) {
		t.Fatalf("real engine answered the fenced archive with %v, want lease_mismatch", spy.archiveErrs[0])
	}

	row, err := f.m.getExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != store.StateTerminated || row.ArchivedAt != 0 {
		t.Fatalf("row after exit = state %s archived_at %d, want terminated and NOT archived", row.State, row.ArchivedAt)
	}
	if row.LeasePrincipalID != ploom {
		t.Fatalf("row lease holder = %q, want %q (untouched)", row.LeasePrincipalID, ploom)
	}
	if got := f.archivedPrincipals(t, exec.ID); len(got) != 0 {
		t.Fatalf("execution.archived events = %q, want none (a refused archive emits nothing)", got)
	}
}
