package codexbroker

import (
	"context"
	"strings"
	"time"
)

// lstartTolerance is the wallclock slack accepted between BrokerRecord.Lstart
// and the freshly-fetched ps row in VerifyIdentity. The tolerance defends
// against ps lstart string-formatting round-trip (BSD lstart only resolves
// to seconds and the parser may shift sub-second wallclock data).
const lstartTolerance = 1 * time.Second

// VerifyIdentity is spec §5.4 Step 0 (lines 434-437): re-fetch ps for
// rec.PID; confirm lstart matches (±1s) and cmdline contains the broker
// task-worker marker. A mismatch causes the kill sequence to abort and
// triggers E2 quarantine for this brokerKey.
//
// Returns (ok, detail). detail is a short audit-trace string explaining
// why the verify succeeded or failed.
//
// PID-reuse window: the time between this verify and Step 3's SIGTERM
// is bounded by gracefulShutdownTimeout (5s). Per plan §9 risks table,
// PID reuse within that window is documented as an acceptable known
// risk; E2 catches it on the next sweep.
func VerifyIdentity(rec BrokerRecord, lister ProcessLister) (bool, string) {
	if lister == nil {
		return false, "no-lister"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	rows, err := lister.List(ctx)
	if err != nil {
		return false, "lister-error"
	}
	for _, row := range rows {
		if row.PID != rec.PID {
			continue
		}
		// Cmdline check first — even a matching pid is suspicious if the
		// process isn't a broker.
		if !strings.Contains(row.Cmdline, brokerTaskWorkerCmdlineMarker) {
			return false, "cmdline-mismatch"
		}
		// Lstart tolerance check.
		drift := row.Lstart.Sub(rec.Lstart)
		if drift < 0 {
			drift = -drift
		}
		if drift > lstartTolerance {
			return false, "lstart-drift"
		}
		return true, "match"
	}
	return false, "pid-not-found"
}

// KillSequence is the per-broker kill orchestrator. Steps 0-6 per spec
// §5.4 are run sequentially via Run (task P).
//
// In task J the struct only needs to compile so VerifyIdentity has a
// natural caller; subsequent tasks (K-P) populate Step 1 (audit
// preimage), Step 2 (graceful), Steps 3+4 (TERM/KILL), Step 5 (verify
// gone), Step 6 (cleanup), and the Run wiring.
type KillSequence struct {
	Rec       BrokerRecord
	Lister    ProcessLister
	Dialer    Dialer
	Signaller Signaller
	FS        FS
	AuditDir  string

	GracefulTimeout time.Duration // default DefaultGracefulShutdownTimeout
	TermTimeout     time.Duration // default 5s
	KillTimeout     time.Duration // default 2s
}

// KillResult is the audit-postscript payload (spec §5.5 lines 495-500)
// plus the Step 6 cleanup outcome. StepLatencyMs[0]=graceful,
// [1]=SIGTERM, [2]=SIGKILL.
type KillResult struct {
	GracefulOk    bool
	TermOk        bool
	KillOk        bool
	StepLatencyMs [3]int64
	CleanedUp     bool
	Err           error
}
