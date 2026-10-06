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

// d16Retry runs the first exit with the lease handed to handoverTo right
// after the terminate (its fenced archive refused: terminated, unarchived)
// and returns the row the next exit — the D16 retry — starts from.
func d16Retry(t *testing.T, f *mountFixture, spy *engineSpy, exec store.Execution, handoverTo string) store.Execution {
	t.Helper()
	spy.handoverTo = handoverTo
	out, herr := f.m.exitWorker(context.Background(), exec, nil, "pdx:"+f.hostID)
	if spy.handoverErr != nil {
		t.Fatalf("test setup: %v", spy.handoverErr)
	}
	if herr != nil || !out.Terminated || out.Archived {
		t.Fatalf("first exit: out=%+v herr=%+v, want terminated, not archived", out, herr)
	}
	spy.handoverTo = ""
	row, err := f.m.getExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != store.StateTerminated || row.ArchivedAt != 0 || row.LeasePrincipalID != handoverTo {
		t.Fatalf("row after the first exit = %+v, want terminated, unarchived, held by %q", row, handoverTo)
	}
	return row
}

// #1665: the D16 retry archives under a control too. A non-pdx holder
// that took the lease after the terminate is refused (D4), not archived over.
func TestExitWorker_RealEngine_D16RetryHeldByNonPdxIsRefused(t *testing.T) {
	const ploom = "ploom:agent-7"
	f, spy, exec := realIdleWorker(t, "")
	row := d16Retry(t, f, spy, exec, ploom)

	out, herr := f.m.exitWorker(context.Background(), row, nil, "pdx:"+f.hostID)
	if herr == nil || herr.code != "held_by" || herr.detail["principal"] != ploom {
		t.Fatalf("herr = %+v, want held_by %s", herr, ploom)
	}
	if out.Archived || len(spy.archives) != 1 {
		t.Fatalf("out=%+v archives=%+v, want no archive attempted by the retry", out, spy.archives)
	}
	after, err := f.m.getExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ArchivedAt != 0 || after.LeasePrincipalID != ploom || after.LeaseID != row.LeaseID {
		t.Fatalf("row after the retry = %+v, want unarchived, lease %s of %s untouched", after, row.LeaseID, ploom)
	}
	if got := f.archivedPrincipals(t, exec.ID); len(got) != 0 {
		t.Fatalf("execution.archived events = %q, want none", got)
	}
}

// #1665: a pdx holder's lease is borrowed — the retry archives under the
// holder's lease and principal, and the event names the holder.
func TestExitWorker_RealEngine_D16RetryUnderPdxHolderLease(t *testing.T) {
	f, spy, exec := realIdleWorker(t, "")
	holder := "pdx:" + f.hostID + "/other-tab"
	row := d16Retry(t, f, spy, exec, holder)

	out, herr := f.m.exitWorker(context.Background(), row, nil, "pdx:"+f.hostID)
	if herr != nil || !out.Terminated || !out.Archived {
		t.Fatalf("out=%+v herr=%+v", out, herr)
	}
	if len(spy.archives) != 2 {
		t.Fatalf("archives = %+v, want the refused one and the retry", spy.archives)
	}
	if arch := spy.archives[1]; arch.LeaseID != row.LeaseID || arch.PrincipalID != holder || spy.archiveErrs[1] != nil {
		t.Fatalf("retry archive under %q/%q (err %v), want the holder's %q/%q", arch.LeaseID, arch.PrincipalID, spy.archiveErrs[1], row.LeaseID, holder)
	}
	after, err := f.m.getExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ArchivedAt == 0 || after.LeaseID != row.LeaseID {
		t.Fatalf("row after the retry = %+v, want archived, the borrowed lease never released", after)
	}
	if got := f.archivedPrincipals(t, exec.ID); len(got) != 1 || got[0] != holder {
		t.Fatalf("execution.archived principals = %q, want [%q] (the lease holder)", got, holder)
	}
}

// #1665: no holder — the retry acquires its own lease, archives under it,
// and releases it after the archive.
func TestExitWorker_RealEngine_D16RetryUnderOwnLease(t *testing.T) {
	const ploom = "ploom:agent-7"
	f, spy, exec := realIdleWorker(t, "")
	self := "pdx:" + f.hostID
	row := d16Retry(t, f, spy, exec, ploom)
	ploomLease := row.LeaseID
	if err := spy.nexService.ReleaseLease(context.Background(), exec.ID, ploomLease, ploom); err != nil {
		t.Fatalf("test setup: releasing %s's lease: %v", ploom, err)
	}
	row.LeaseID, row.LeasePrincipalID, row.LeaseExpiresAt = "", "", 0

	out, herr := f.m.exitWorker(context.Background(), row, nil, self)
	if herr != nil || !out.Terminated || !out.Archived {
		t.Fatalf("out=%+v herr=%+v", out, herr)
	}
	if len(spy.archives) != 2 {
		t.Fatalf("archives = %+v, want the refused one and the retry", spy.archives)
	}
	if arch := spy.archives[1]; arch.LeaseID == "" || arch.LeaseID == ploomLease || arch.PrincipalID != self || spy.archiveErrs[1] != nil {
		t.Fatalf("retry archive under %q/%q (err %v), want a fresh own lease of %q", arch.LeaseID, arch.PrincipalID, spy.archiveErrs[1], self)
	}
	after, err := f.m.getExecution(context.Background(), exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ArchivedAt == 0 || after.LeaseID != "" {
		t.Fatalf("row after the retry = %+v, want archived and the own lease released", after)
	}
	if got := f.archivedPrincipals(t, exec.ID); len(got) != 1 || got[0] != self {
		t.Fatalf("execution.archived principals = %q, want [%q]", got, self)
	}
}
