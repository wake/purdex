package execution

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeTerminalStore records MarkTerminal calls and serves a fixed execution by
// session_code.
type fakeTerminalStore struct {
	bySession map[string]*Execution
	markErr   error
	marked    []markCall
}

type markCall struct {
	execID string
	status Status
	source OutcomeSource
}

func (f *fakeTerminalStore) GetBySessionCode(code string) (*Execution, bool, error) {
	e, ok := f.bySession[code]
	return e, ok, nil
}

// MarkTerminalWithReport mirrors the real store: the report is built inside the
// transition, so a build error aborts the transition (nothing is recorded).
func (f *fakeTerminalStore) MarkTerminalWithReport(execID string, to Status, source OutcomeSource, build ReportBuilder) error {
	if f.markErr != nil {
		return f.markErr
	}
	if build != nil {
		row := &Execution{ExecutionID: execID}
		for _, e := range f.bySession {
			if e.ExecutionID == execID {
				row = e
				break
			}
		}
		committed := *row
		committed.Status = to
		committed.OutcomeSource = sql.NullString{String: string(source), Valid: true}
		if _, err := build(&committed); err != nil {
			return err
		}
	}
	f.marked = append(f.marked, markCall{execID, to, source})
	return nil
}

// fakeTerminalReporter captures the terminal report builds.
type fakeTerminalReporter struct {
	calls []terminalCall
	err   error
}

type terminalCall struct {
	exec      *Execution
	status    Status
	artifacts []Artifact
	errCode   string
	errMsg    string
}

func (f *fakeTerminalReporter) BuildTerminal(exec *Execution, status Status, artifacts []Artifact, errCode, errMsg string) (ReportEnvelope, error) {
	f.calls = append(f.calls, terminalCall{exec, status, artifacts, errCode, errMsg})
	if f.err != nil {
		return ReportEnvelope{}, f.err
	}
	return ReportEnvelope{Seq: 3, Status: string(status), Payload: []byte(`{"status":"` + string(status) + `"}`)}, nil
}

// stubDiff returns a fixed diff artifact with the given file/add/del counts.
func stubDiff(files, add, del int) diffFunc {
	return func(_ context.Context, daemonID, execID, _, _ string) (Artifact, error) {
		return Artifact{
			Kind:    "diff",
			Pointer: DiffPointer(daemonID, execID),
			Meta:    map[string]any{"files": files, "add": add, "del": del},
		}, nil
	}
}

func runningExec(execID, sessionCode string) *Execution {
	return &Execution{
		ExecutionID:  execID,
		DispatchID:   "dsp_" + execID,
		RepoLocation: "/abs/repo",
		HeadAtStart:  "base123",
		Status:       StatusRunning,
		SessionCode:  sql.NullString{String: sessionCode, Valid: true},
	}
}

func artifactKinds(arts []Artifact) []string {
	out := make([]string, len(arts))
	for i, a := range arts {
		out[i] = a.Kind
	}
	return out
}

func TestTerminalProcessor_CompletedWithDiff(t *testing.T) {
	store := &fakeTerminalStore{bySession: map[string]*Execution{"c1": runningExec("exc_c1", "c1")}}
	rep := &fakeTerminalReporter{}
	p := NewTerminalProcessor(store, rep, stubDiff(2, 10, 3), "dmn_1")

	err := p.Handle(context.Background(), "c1", 0, false, ResultOutcome{HasResult: true, Subtype: "success"})
	require.NoError(t, err)

	require.Len(t, store.marked, 1)
	require.Equal(t, StatusCompleted, store.marked[0].status)
	require.Equal(t, OutcomeResult, store.marked[0].source)

	require.Len(t, rep.calls, 1)
	call := rep.calls[0]
	require.Equal(t, StatusCompleted, call.status)
	require.Equal(t, []string{"diff", "transcript"}, artifactKinds(call.artifacts))
	require.Equal(t, "pdx://dmn_1/execution/exc_c1/diff", call.artifacts[0].Pointer)
	require.Empty(t, call.errCode)
}

func TestTerminalProcessor_FailedFromResultError(t *testing.T) {
	store := &fakeTerminalStore{bySession: map[string]*Execution{"f1": runningExec("exc_f1", "f1")}}
	rep := &fakeTerminalReporter{}
	p := NewTerminalProcessor(store, rep, stubDiff(1, 5, 0), "dmn_1")

	err := p.Handle(context.Background(), "f1", 0, false, ResultOutcome{HasResult: true, IsError: true, Subtype: "error_during_execution"})
	require.NoError(t, err)

	require.Equal(t, StatusFailed, store.marked[0].status)
	require.Equal(t, OutcomeResult, store.marked[0].source)
	require.Equal(t, StatusFailed, rep.calls[0].status)
	require.NotEmpty(t, rep.calls[0].errCode)
	require.Contains(t, rep.calls[0].errMsg, "error_during_execution")
	// failed WITH changes → diff attached.
	require.Equal(t, []string{"diff", "transcript"}, artifactKinds(rep.calls[0].artifacts))
}

func TestTerminalProcessor_FailedNonzeroExitNoChanges_OmitsDiff(t *testing.T) {
	store := &fakeTerminalStore{bySession: map[string]*Execution{"f2": runningExec("exc_f2", "f2")}}
	rep := &fakeTerminalReporter{}
	p := NewTerminalProcessor(store, rep, stubDiff(0, 0, 0), "dmn_1")

	err := p.Handle(context.Background(), "f2", 7, false, ResultOutcome{HasResult: false})
	require.NoError(t, err)

	require.Equal(t, StatusFailed, store.marked[0].status)
	require.Equal(t, OutcomeExitOnly, store.marked[0].source)
	// failed with NO changes → diff omitted, transcript still present.
	require.Equal(t, []string{"transcript"}, artifactKinds(rep.calls[0].artifacts))
}

func TestTerminalProcessor_CompletedNoChanges_KeepsDiff(t *testing.T) {
	store := &fakeTerminalStore{bySession: map[string]*Execution{"c2": runningExec("exc_c2", "c2")}}
	rep := &fakeTerminalReporter{}
	p := NewTerminalProcessor(store, rep, stubDiff(0, 0, 0), "dmn_1")

	require.NoError(t, p.Handle(context.Background(), "c2", 0, false, ResultOutcome{HasResult: false}))
	// completed always carries the diff artifact even when empty (0 files).
	require.Equal(t, []string{"diff", "transcript"}, artifactKinds(rep.calls[0].artifacts))
}

func TestTerminalProcessor_UnknownSessionCode_NoOp(t *testing.T) {
	store := &fakeTerminalStore{bySession: map[string]*Execution{}}
	rep := &fakeTerminalReporter{}
	p := NewTerminalProcessor(store, rep, stubDiff(1, 1, 1), "dmn_1")

	require.NoError(t, p.Handle(context.Background(), "ghost", 0, false, ResultOutcome{HasResult: true}))
	require.Empty(t, store.marked)
	require.Empty(t, rep.calls)
}

func TestTerminalProcessor_AlreadyTerminal_NoOp(t *testing.T) {
	done := runningExec("exc_done", "d1")
	done.Status = StatusCompleted
	store := &fakeTerminalStore{bySession: map[string]*Execution{"d1": done}}
	rep := &fakeTerminalReporter{}
	p := NewTerminalProcessor(store, rep, stubDiff(1, 1, 1), "dmn_1")

	require.NoError(t, p.Handle(context.Background(), "d1", 0, false, ResultOutcome{HasResult: true}))
	require.Empty(t, store.marked)
	require.Empty(t, rep.calls)
}

func TestTerminalProcessor_MarkRaceLost_NoEnqueue(t *testing.T) {
	store := &fakeTerminalStore{
		bySession: map[string]*Execution{"r1": runningExec("exc_r1", "r1")},
		markErr:   ErrIllegalTransition,
	}
	rep := &fakeTerminalReporter{}
	p := NewTerminalProcessor(store, rep, stubDiff(1, 1, 1), "dmn_1")

	require.NoError(t, p.Handle(context.Background(), "r1", 0, false, ResultOutcome{HasResult: true}))
	// lost the terminal transition race → must NOT enqueue a duplicate report.
	require.Empty(t, rep.calls)
}
