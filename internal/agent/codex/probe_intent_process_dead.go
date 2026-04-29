// internal/agent/codex/probe_intent_process_dead.go
//
// Codex-side detector for ProbeIntentKindProcessDead. Polls senderPID + paneID
// and emits one Signal when either invariant fails. Used by the daemon
// ProbeIntent dispatcher to recover the missing StopFailure transition that
// codex 0.124.0 does not emit (see docs/specs/2026-04-29-w6-3-codex-error-
// probe-intent-spec.md §4 for the full design).
package codex

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

// processDeadPollInterval is the polling cadence for pidAlive + HasPane.
// 1Hz is intentional — codex crash recovery is latency-tolerant and we
// deliberately run slower than ScreenChangeWatcher (200ms tick) to honour
// the "probe is recovery, not authority" principle. Tests override via the
// withFastPoll helper.
var processDeadPollInterval = 1 * time.Second

// isPidAliveFn is the injectable pid-liveness probe. Production points at
// probe.IsPidAlive (a syscall.Kill(pid, 0) wrapper). Tests override the
// package var to control the alive/dead state per call rather than spawning
// real processes (per b36pap7jc / by2z79ouc FH-1 — the
// pidAlive×paneAlive matrix needs deterministic injection).
//
// Same pattern as internal/module/agent/probe_orchestrator.go's isPidAliveFn.
var isPidAliveFn = probe.IsPidAlive

// tmuxPaneLister is the minimal contract this detector requires for pane
// existence checks. Implementation in production: tmux.Executor.HasPane(paneID)
// — wraps `tmux list-panes -a -F '#{pane_id}'` and scans for the target id.
//
// Per b36pap7jc ATK-2: the detector deliberately checks the global pane list
// (NOT probe.Prober.IsAliveFor(sessionTarget)). Multi-pane windows resolve
// session targets to the first pane via PanePID, which is the wrong
// observation for non-first siblings.
type tmuxPaneLister interface {
	HasPane(paneID string) bool
}

// StartProcessDeadDetector is the codex-side detector for
// ProbeIntentKindProcessDead. It polls senderPID + paneID; on the first
// dead-confirmed tick it emits one Signal carrying the PaneAlive observation
// and returns. Cancel ctx to stop early (e.g. on session rename or status
// exit from OnEntryStatus).
//
// Polling matrix (spec §4.2):
//
//	| pidAlive | paneAlive | action                                  |
//	|----------|-----------|-----------------------------------------|
//	| true     | true      | both alive — keep polling               |
//	| true     | false     | re-parented edge case → emit PaneAlive=false |
//	| false    | true      | process dead, pane alive → emit PaneAlive=true (W6-3 error) |
//	| false    | false     | process dead, pane gone → emit PaneAlive=false (W6-4 clear) |
//
// The detector emits at most one Signal. Caller is expected to consume the
// emitted Signal via dispatcher.applyProbeGuards (graceWindow / stale-callback
// / ErrorGuard / transition gate / OnSignal mapping all live there).
//
// paneLister is the minimal contract the detector needs (HasPane(paneID)).
// Production wires *tmux.Executor; tests inject a fake.
func StartProcessDeadDetector(
	ctx context.Context,
	paneLister tmuxPaneLister,
	paneID string,
	senderPID int,
	out chan<- agent.Signal,
) {
	ticker := time.NewTicker(processDeadPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pidAlive := isPidAliveFn(senderPID)
			paneAlive := paneLister.HasPane(paneID)
			if pidAlive && paneAlive {
				continue // both alive, keep polling
			}
			// pidAlive=true, paneAlive=false: process re-parented but pane
			// gone; treat as pane-gone path (PaneAlive=false → W6-4 clear).
			// Already covered by the Signal{PaneAlive: paneAlive} below.
			select {
			case out <- agent.Signal{
				Kind:      agent.ProbeIntentKindProcessDead,
				PaneAlive: paneAlive,
				PaneID:    paneID,
				SenderPID: senderPID,
			}:
			case <-ctx.Done():
			}
			return
		}
	}
}
