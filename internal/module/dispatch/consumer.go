package dispatch

import (
	"encoding/json"
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
	// seqTerminal is the completed/failed report seq on the normal lifecycle
	// (accepted=1 → running=2 → terminal=3). The pre-launch admission-reject
	// failed above rides seqFailed=2 because it never reached running.
	seqTerminal = 3
)

// reportEnqueuer is the narrow slice of *Sender the consumer glue needs: durably
// queue one already-built report payload. It is used only for reports that stand
// alone (the admission-rejection projection, which has no execution row and so no
// transition to pair with). Reports that accompany a state transition are queued
// by the execution store inside that transition's transaction. Tests inject a fake.
type reportEnqueuer interface {
	Enqueue(executionID, dispatchID string, seq int, status string, payload []byte) error
}

// launchReporter adapts the E4 wire format onto execution.LaunchReporter, so the
// execution Coordinator can render accepted/running/failed without importing
// dispatch (which would cycle). These are pure builders: the execution store does
// the durable enqueue inside the transaction that commits the transition being
// reported. accepted is built from the execution row's immutable facts via the
// same BuildAcceptedPayload used for restart reconstruction, so a live-built
// accepted and a reconstructed one are byte-identical (spec §3.3 durability cut).
type launchReporter struct{}

var _ execution.LaunchReporter = launchReporter{}

// BuildAccepted renders accepted(seq=1) from the execution row.
func (launchReporter) BuildAccepted(exec *execution.Execution) (execution.ReportEnvelope, error) {
	payload, err := BuildAcceptedPayload(acceptedRowFromExec(exec))
	if err != nil {
		return execution.ReportEnvelope{}, err
	}
	return execution.ReportEnvelope{Seq: seqAccepted, Status: "accepted", Payload: payload}, nil
}

// BuildRunning renders running(seq=2), queued as the agent's relay connects.
func (launchReporter) BuildRunning(exec *execution.Execution) (execution.ReportEnvelope, error) {
	payload, err := BuildRunningPayload(exec.ExecutionID, seqRunning)
	if err != nil {
		return execution.ReportEnvelope{}, err
	}
	return execution.ReportEnvelope{Seq: seqRunning, Status: "running", Payload: payload}, nil
}

// BuildFailed renders the terminal failed(seq=2) report for a pre-relay launch
// failure. It rides seqFailed=2 on the already-queued accepted(1) — the launch
// never reached running, so this is the execution's terminal report and the outbox
// replays accepted→failed, unwedging Ploom without any reconcile pass.
func (launchReporter) BuildFailed(exec *execution.Execution, errCode, errMsg string) (execution.ReportEnvelope, error) {
	payload, err := BuildTerminalPayload(exec.ExecutionID, seqFailed, "failed", nil, &ReportError{
		Code:    errCode,
		Message: errMsg,
	})
	if err != nil {
		return execution.ReportEnvelope{}, err
	}
	return execution.ReportEnvelope{Seq: seqFailed, Status: "failed", Payload: payload}, nil
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
		RepoLocationJSON:        e.RepoLocationJSON,
		Provider:                e.Provider,
		AttemptNo:               e.AttemptNo,
		EffectiveSandboxProfile: e.SandboxProfile,
		HeadAtStart:             e.HeadAtStart,
		DirtyAtStart:            e.DirtyAtStart,
		SessionCode:             sessionCode,
	}
}

// terminalReporter adapts the E4 wire format onto execution.TerminalReporter, so
// the execution TerminalProcessor and Reconciler can render the completed/failed
// report (with pointer-first artifacts) without importing dispatch. It maps the
// execution-side Artifact onto the report Artifact and stamps the terminal seq;
// execution never computes a seq itself.
type terminalReporter struct{}

var _ execution.TerminalReporter = terminalReporter{}

// terminalSeq returns the seq a terminal report must carry, from the lifecycle the
// execution actually had. running(2) is only ever queued by MarkLaunched, so a row
// that never reached launch_state=launched never had a running report — its
// terminal report must ride seq=2 (matching the pre-relay launch failure) rather
// than leaving a hole at 2 that Ploom's ordered projection would wait on forever.
func terminalSeq(exec *execution.Execution) int {
	if exec.LaunchState == execution.LaunchLaunched {
		return seqTerminal
	}
	return seqFailed
}

// BuildTerminal renders the terminal report. errCode/errMsg are populated only for
// failed; completed carries no error object.
func (terminalReporter) BuildTerminal(exec *execution.Execution, status execution.Status, artifacts []execution.Artifact, errCode, errMsg string) (execution.ReportEnvelope, error) {
	reportArts := make([]Artifact, len(artifacts))
	for i, a := range artifacts {
		reportArts[i] = Artifact{Kind: a.Kind, Pointer: a.Pointer, Meta: a.Meta}
	}
	var errObj *ReportError
	if status == execution.StatusFailed {
		errObj = &ReportError{Code: errCode, Message: errMsg}
	}
	seq := terminalSeq(exec)
	payload, err := BuildTerminalPayload(exec.ExecutionID, seq, string(status), reportArts, errObj)
	if err != nil {
		return execution.ReportEnvelope{}, err
	}
	return execution.ReportEnvelope{Seq: seq, Status: string(status), Payload: payload}, nil
}

// marshalRepoLocation serialises the full E3 repo_location object for durable
// echo (contract §2). It is the single encoder used by both the launch path
// (module.go consumeSink) and the admission-rejection path, so every accepted
// report echoes the identical object shape. On the (practically impossible)
// marshal error it returns "" — the payload then falls back to {local_dir}.
func marshalRepoLocation(loc RepoLocation) string {
	b, err := json.Marshal(loc)
	if err != nil {
		return ""
	}
	return string(b)
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
		RepoLocationJSON:        marshalRepoLocation(detail.RepoLocation),
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
	case errors.Is(err, execution.ErrUnknownProfile):
		return "unknown_sandbox_profile"
	default:
		return "launch_failed"
	}
}
