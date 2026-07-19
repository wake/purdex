package dispatch

import (
	"errors"
	"strings"

	"github.com/wake/purdex/internal/module/execution"
)

// This file holds the P.7 consumer glue that binds the poll/claim/fetch worker
// (P.3) to the execution launch durable cut (P.6): the LaunchReporter adapter
// that lets the execution Coordinator enqueue accepted/running through the
// dispatch report outbox (P.4), the prompt composer, and the pre-launch failure
// (admission reject) reporter. The wiring that assembles these into the module
// lives in module.go.

// Report seqs are fixed for the M0 launch lifecycle: accepted is always seq=1
// (the durability-cut anchor) and running is always seq=2. Terminal reports
// (completed/failed) are seq≥3 and land in P.8 — except the pre-launch failed
// below, which rides on its own accepted(1) since it never reached running.
const (
	seqAccepted = 1
	seqRunning  = 2
	seqFailed   = 2
)

// reportEnqueuer is the narrow slice of *Sender the consumer glue needs: durably
// queue one already-built report payload. Tests inject a fake.
type reportEnqueuer interface {
	Enqueue(executionID, dispatchID string, seq int, status string, payload []byte) error
}

// launchReporter adapts the dispatch report Sender onto execution.LaunchReporter,
// so the execution Coordinator can durably enqueue accepted/running without
// importing dispatch (which would cycle). accepted is built from the execution
// row's immutable facts via the same BuildAcceptedPayload used for restart
// reconstruction, so a live-enqueued accepted and a reconstructed one are
// byte-identical (spec §3.3 durability cut).
type launchReporter struct {
	sender reportEnqueuer
}

var _ execution.LaunchReporter = launchReporter{}

// EnqueueAccepted durably queues accepted(seq=1) from the execution row.
func (r launchReporter) EnqueueAccepted(exec *execution.Execution) error {
	payload, err := BuildAcceptedPayload(acceptedRowFromExec(exec))
	if err != nil {
		return err
	}
	return r.sender.Enqueue(exec.ExecutionID, exec.DispatchID, seqAccepted, "accepted", payload)
}

// EnqueueRunning durably queues running(seq=2) once the agent's relay connected.
func (r launchReporter) EnqueueRunning(exec *execution.Execution) error {
	payload, err := BuildRunningPayload(exec.ExecutionID, seqRunning)
	if err != nil {
		return err
	}
	return r.sender.Enqueue(exec.ExecutionID, exec.DispatchID, seqRunning, "running", payload)
}

// acceptedRowFromExec projects a live execution row onto the accepted-report
// immutable facts. It is the single mapping shared by the live enqueue path here
// and the restart-reconstruction path (executionStoreReader.LoadAcceptedRow), so
// both produce identical accepted payloads for the same row.
func acceptedRowFromExec(e *execution.Execution) AcceptedRow {
	sessionCode := ""
	if e.SessionCode.Valid {
		sessionCode = e.SessionCode.String
	}
	return AcceptedRow{
		ExecutionID:             e.ExecutionID,
		DispatchID:              e.DispatchID,
		RepoLocation:            e.RepoLocation,
		Provider:                e.Provider,
		AttemptNo:               e.AttemptNo,
		EffectiveSandboxProfile: e.SandboxProfile,
		HeadAtStart:             e.HeadAtStart,
		DirtyAtStart:            e.DirtyAtStart,
		SessionCode:             sessionCode,
	}
}

// buildPrompt composes the agent's starting prompt from the fetched issue. The
// prompt is delivered to the agent over relay stdin as a stream-json message
// (never a shell argument), so there is no injection surface — this only shapes
// the human-readable instruction the agent begins from. Title and body are
// joined with a blank line; a bare title (no body) yields just the title.
func buildPrompt(issue Issue) string {
	title := strings.TrimSpace(issue.Title)
	body := strings.TrimSpace(issue.Body)
	switch {
	case title == "" && body == "":
		return ""
	case body == "":
		return title
	case title == "":
		return body
	default:
		return title + "\n\n" + body
	}
}

// reportAdmissionRejected records a pre-launch failure (admission rejection or a
// launch error the Coordinator surfaced before/without a running report) as a
// terminal failed for the dispatch.
//
// The E4 contract forbids a lifecycle report before accepted(seq=1) is acked, so
// a rejected dispatch cannot emit a bare failed. Instead we enqueue accepted(1)
// then failed(2): Ploom projects queued→failed carrying the rejection reason.
// The dispatch was already claimed (E2) before the sink ran, so it will not be
// re-polled; a single one-shot report is sufficient. execID is a synthetic id
// for this rejection projection — no execution row is created (single-live is
// preserved: a failed row would not be live anyway, but here we avoid the row
// entirely since admission never captured head/dirty).
func reportAdmissionRejected(sender reportEnqueuer, execID, dispatchID string, detail DispatchDetail, cause error) error {
	acceptedRow := AcceptedRow{
		ExecutionID:             execID,
		DispatchID:              dispatchID,
		Provider:                reportProvider,
		AttemptNo:               1,
		RepoLocation:            detail.RepoLocation.LocalDir,
		EffectiveSandboxProfile: detail.SandboxProfile,
	}
	acceptedPayload, err := BuildAcceptedPayload(acceptedRow)
	if err != nil {
		return err
	}
	if err := sender.Enqueue(execID, dispatchID, seqAccepted, "accepted", acceptedPayload); err != nil {
		return err
	}
	failedPayload, err := BuildTerminalPayload(execID, seqFailed, "failed", nil, &ReportError{
		Code:    rejectionCode(cause),
		Message: cause.Error(),
	})
	if err != nil {
		return err
	}
	return sender.Enqueue(execID, dispatchID, seqFailed, "failed", failedPayload)
}

// rejectionCode maps an admission/launch failure onto a stable error code for
// the failed report. Unknown causes fall back to a generic launch_failed.
func rejectionCode(err error) string {
	switch {
	case errors.Is(err, execution.ErrRepoBusy):
		return "repo_busy"
	case errors.Is(err, execution.ErrCanonical):
		return "invalid_repo_location"
	default:
		return "launch_failed"
	}
}
