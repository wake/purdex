package nex

// D23 against the REAL embedded Nexen (≥ v0.18.0, nexen#113), through the
// mount fixture's real nexen.Assemble: the exit's archive carries the
// terminate's lease, the engine checks it in the archive's own UPDATE, and
// a lease that changed hands after the terminate is not archived over.
// Rows that need no terminate (#1665) archive under a control taken, and
// renewed, for the archive.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"

	pdxconfig "github.com/wake/purdex/internal/config"
)

// engineSpy wraps the real service: it records the Terminate and Archive
// requests (and the archive's answer), and with handoverTo set, hands the
// lease over right after a successful Terminate — released as its holder,
// then acquired by handoverTo — the window between terminate and archive
// that #113 closes.
//
// expireNext, when set, runs once right before the next RenewLease or
// Archive — the window between taking control and the archive (#1665).
type engineSpy struct {
	nexService
	handoverTo string

	mu          sync.Mutex
	terminates  []execution.TerminateRequest
	archives    []execution.ArchiveRequest
	archiveErrs []error
	renews      []spyRenew
	handoverErr error
	expireNext  func()
}

// spyRenew is one RenewLease the module made, and the engine's answer.
type spyRenew struct {
	LeaseID, PrincipalID string
	Err                  error
}

// fireExpireNext runs expireNext once, if set.
func (s *engineSpy) fireExpireNext() {
	s.mu.Lock()
	hook := s.expireNext
	s.expireNext = nil
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (s *engineSpy) RenewLease(ctx context.Context, executionID, leaseID, principalID string) (store.Lease, error) {
	s.fireExpireNext()
	l, err := s.nexService.RenewLease(ctx, executionID, leaseID, principalID)
	s.mu.Lock()
	s.renews = append(s.renews, spyRenew{leaseID, principalID, err})
	s.mu.Unlock()
	return l, err
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
	s.fireExpireNext()
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

// noSessionClaude ends its first turn without ever reporting a session:
// Nexen fails the execution (a first turn that never produced a session is
// one of its two fatal endings).
const noSessionClaude = `#!/bin/sh
head -n 1 >/dev/null
exit 1
`

// realEndedRow returns an unarchived row in state (failed or rejected) on
// a real engine, spied from here on.
func realEndedRow(t *testing.T, state store.State) (*mountFixture, *engineSpy, store.Execution) {
	t.Helper()
	var f *mountFixture
	var id string
	switch state {
	case store.StateFailed:
		f = newMountFixtureWith(t, func(cfg *pdxconfig.Config) {
			cfg.Nex.ClaudeBin = writeScript(t, t.TempDir(), "claude", noSessionClaude)
		})
		id = f.delegate(t)
		waitFor(t, "execution failed", func() bool {
			var s string
			_ = json.Unmarshal(f.summary(t, id)["state"], &s)
			return s == string(store.StateFailed)
		})
	case store.StateRejected:
		f = newMountFixture(t)
		res, err := f.m.sys.service.Delegate(context.Background(), execution.Request{
			PrincipalID:    "pdx:" + f.hostID,
			Provider:       "claude",
			Brief:          "say hi",
			SandboxProfile: "trusted",
			Mounts:         []execution.Mount{{Path: t.TempDir(), Role: "cwd", Writable: true}}, // under no repo root
		})
		if err != nil || res.State != store.StateRejected {
			t.Fatalf("delegate = %+v, %v; want a rejected row", res, err)
		}
		id = res.ID
	default:
		t.Fatalf("realEndedRow: no recipe for %s", state)
	}
	spy := &engineSpy{nexService: f.m.sys.service}
	f.m.sys.service = spy
	exec, err := f.m.getExecution(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if exec.State != state || exec.ArchivedAt != 0 {
		t.Fatalf("row = %+v, want %s and unarchived", exec, state)
	}
	return f, spy, exec
}

// expireLease backdates id's lease to just expired: what its TTL running
// out looks like to Nexen, whose store compares lease_expires_at with its
// own clock on every lease check (store.leaseRefusal).
func (f *mountFixture) expireLease(t *testing.T, id string) {
	t.Helper()
	st, ok := f.m.sys.store.(*store.Store)
	if !ok {
		t.Errorf("engine store is %T, want *store.Store", f.m.sys.store)
		return
	}
	res, err := st.DB().ExecContext(context.Background(),
		`UPDATE executions SET lease_expires_at = ? WHERE id = ? AND lease_id IS NOT NULL`, time.Now().UnixMilli()-1, id)
	if err != nil {
		t.Errorf("expiring the lease of %s: %v", id, err)
		return
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Errorf("expiring the lease of %s: %d rows (%v), want 1", id, n, err)
	}
}

// #1665: the archive of a row that needs no terminate borrows a pdx
// holder's lease, and renews it before the archive. Here the lease runs out
// between the take and the archive: the renew is refused as expired,
// control is re-taken — an own lease, displacing the expired one — and the
// archive goes through under it; the own lease is released after.
func TestExitWorker_RealEngine_BorrowedLeaseExpiringBeforeTheArchive(t *testing.T) {
	holderOf := func(f *mountFixture) string { return "pdx:" + f.hostID + "/other-tab" }
	// heldBy hands row's lease to holderOf(f): a live lease of another pdx client.
	heldBy := func(t *testing.T, f *mountFixture, spy *engineSpy, row store.Execution) store.Execution {
		t.Helper()
		if _, err := spy.nexService.AcquireLease(context.Background(), row.ID, holderOf(f)); err != nil {
			t.Fatalf("test setup: %s acquiring the lease: %v", holderOf(f), err)
		}
		held, err := f.m.getExecution(context.Background(), row.ID)
		if err != nil {
			t.Fatal(err)
		}
		return held
	}
	rows := []struct {
		name  string
		setup func(t *testing.T) (*mountFixture, *engineSpy, store.Execution)
	}{
		{"D16 retry", func(t *testing.T) (*mountFixture, *engineSpy, store.Execution) {
			f, spy, exec := realIdleWorker(t, "")
			return f, spy, d16Retry(t, f, spy, exec, holderOf(f))
		}},
		{"failed", func(t *testing.T) (*mountFixture, *engineSpy, store.Execution) {
			f, spy, row := realEndedRow(t, store.StateFailed)
			return f, spy, heldBy(t, f, spy, row)
		}},
		{"rejected", func(t *testing.T) (*mountFixture, *engineSpy, store.Execution) {
			f, spy, row := realEndedRow(t, store.StateRejected)
			return f, spy, heldBy(t, f, spy, row)
		}},
	}
	for _, c := range rows {
		t.Run(c.name, func(t *testing.T) {
			f, spy, row := c.setup(t)
			self, holder := "pdx:"+f.hostID, holderOf(f)
			if row.LeaseID == "" || row.LeasePrincipalID != holder || row.LeaseExpiresAt <= time.Now().UnixMilli() {
				t.Fatalf("test setup: row = %+v, want a live lease of %s", row, holder)
			}
			before := len(spy.archives)
			spy.expireNext = func() { f.expireLease(t, row.ID) }

			out, herr := f.m.exitWorker(context.Background(), row, nil, self)
			if herr != nil || !out.Archived || out.State != row.State {
				t.Fatalf("out=%+v herr=%+v, want archived", out, herr)
			}
			if len(spy.renews) != 1 || spy.renews[0].LeaseID != row.LeaseID || spy.renews[0].PrincipalID != holder ||
				!errors.Is(spy.renews[0].Err, store.ErrLeaseExpired) {
				t.Fatalf("renews = %+v, want one under the borrowed %s/%s, refused as expired", spy.renews, row.LeaseID, holder)
			}
			archives, errs := spy.archives[before:], spy.archiveErrs[before:]
			if len(archives) != 1 || archives[0].LeaseID == "" || archives[0].LeaseID == row.LeaseID ||
				archives[0].PrincipalID != self || errs[0] != nil {
				t.Fatalf("archives = %+v (errs %v), want one under a fresh own lease of %s, accepted", archives, errs, self)
			}

			after, err := f.m.getExecution(context.Background(), row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.ArchivedAt == 0 || after.LeaseID != "" {
				t.Fatalf("row after the exit = %+v, want archived and the own lease released", after)
			}
			if got := f.archivedPrincipals(t, row.ID); len(got) != 1 || got[0] != self {
				t.Fatalf("execution.archived principals = %q, want [%q]", got, self)
			}
		})
	}
}
