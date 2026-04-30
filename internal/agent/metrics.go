// Package agent — Phase 3.5 in-process observability counters.
//
// These counters expose canonicalization and projection-dedup activity for
// debugging cold-start race resolution. They are accumulated per daemon
// process via stdlib expvar (atomic, sub-microsecond add cost) and are not
// wired to a metrics endpoint in this PR — see plan §2.6.
//
// Limitations (honest by design):
//   - No HTTP exposure: callers reach them via expvar.Get(name) in process.
//   - No SessionStart denominator: counters are absolute, not ratios.
//   - daemon restart resets to zero: not a long-term SLO substrate.
//
// PR-3.5a callers increment partial / dedup counters. Sweep counters land in
// PR-3.5b but the variables are declared here so the four telemetry surfaces
// stay co-located (one file, one entry point for any future endpoint).
package agent

import "expvar"

// MetricPartialCanonicalizationCreated tallies hot-path canonicalization
// attempts that left a partial state behind (proxy ref attached on the
// ancestor but the descendant's standalone frame DELETE failed under
// optimistic-concurrency conflict). Sweep canonicalize repairs within 2s;
// projection_dedup hides from SPA in the meantime.
var MetricPartialCanonicalizationCreated = expvar.NewInt("purdex_phase35_partial_canonicalization_created_total")

// MetricProjectionDedupHidden counts standalone frames hidden by
// buildPaneProjection because an ancestor frame in the same pane carries
// a matching IsProxy ref. Each hidden frame is +1.
var MetricProjectionDedupHidden = expvar.NewInt("purdex_phase35_projection_dedup_hidden_total")

// MetricSweepCanonicalized counts standalone frames that the sweep
// canonicalizePane pass folded into ancestor proxy refs. Incremented in
// PR-3.5b sweep work.
var MetricSweepCanonicalized = expvar.NewInt("purdex_phase35_sweep_canonicalized_total")

// MetricSweepPrunedProxy counts dead/PID-reused proxy refs detached by the
// sweep pruneDeadProxyRefs pass. Incremented in PR-3.5b sweep work.
var MetricSweepPrunedProxy = expvar.NewInt("purdex_phase35_sweep_pruned_proxy_total")

// --- Phase 4a PR-4a-1 probe-orchestrator counters --------------------------
// Independent namespace ("purdex_probe_*") so dashboards built on
// purdex_phase35_* keep working. Each counter is process-cumulative; daemon
// restart resets to zero. Tests must compare deltas, never absolute values.

// MetricProbeWatchStarted counts probeOrchestrator.startWatch invocations
// that successfully reach the underlying prober.Watch call. Pre-watch nil
// guards (no prober wired) do NOT increment — the counter measures real
// watcher lifetimes, not orchestrator entry-points.
var MetricProbeWatchStarted = expvar.NewInt("purdex_probe_watch_started_total")

// MetricProbeWatchStopped is the symmetric counter for stopWatch.
var MetricProbeWatchStopped = expvar.NewInt("purdex_probe_watch_stopped_total")

// MetricProbeScreenEvent counts ACCEPTED status transitions driven by a
// probe screen-change event. v2.0 semantics: incremented only when the
// orchestrator's transition gate decides to broadcast (currentStatus !=
// new status). Repeated ScreenChanged on a Running session does NOT count
// — orchestrator dedups before broadcast.
var MetricProbeScreenEvent = expvar.NewInt("purdex_probe_screen_event_total")

// MetricProbeGraceWindowSuppressed counts probe events suppressed because
// they arrived within probeGraceWindow of a recordHookAt call. Each
// suppressed event +1 (no dedup) — the counter measures hook authority
// over probe.
var MetricProbeGraceWindowSuppressed = expvar.NewInt("purdex_probe_grace_window_suppressed_total")

// --- W6-3 P2-T6 ProbeIntent dispatcher counters ---------------------------
// Independent namespace ("purdex_probe_intent_*") so existing probe / phase35
// dashboards stay separable from the per-Kind ProbeIntent fan-out. Each
// counter is process-cumulative; daemon restart resets to zero. Tests must
// compare deltas, never absolute values.
//
// Drop counters use caller-aware semantics: ScreenChange path leaves
// probeGuardArgs.OnDrop nil and only bumps MetricProbeGraceWindowSuppressed
// (legacy contract); ProbeIntent path supplies OnDrop = probeIntentOnDrop
// so each drop reason routes to a distinct counter for fault isolation
// (stale-callback vs grace vs error-guard vs transition-gate).

// MetricProbeIntentStarted counts active ProbeIntent detector arming
// (applyIntentLifecycle case 2 / 5). Increments after the entry is recorded
// in m.activeProbeIntents and the goroutine has been scheduled.
var MetricProbeIntentStarted = expvar.NewInt("purdex_probe_intent_started_total")

// MetricProbeIntentStopped counts every detector cancel from the dispatcher
// (lifecycle case 3, reconcileSessionActive teardown, stopAll). Each
// distinct activeIntent.cancel invocation = +1.
var MetricProbeIntentStopped = expvar.NewInt("purdex_probe_intent_stopped_total")

// MetricProbeIntentSignalEmitted counts every Signal observed by
// consumeSignals before the guard pipeline runs. Decoupled from
// MetricProbeIntentApplied so dashboards can show the apply ratio.
var MetricProbeIntentSignalEmitted = expvar.NewInt("purdex_probe_intent_signal_emitted_total")

// MetricProbeIntentApplied counts signals that successfully traversed the
// full applyProbeGuards pipeline (StaleCheck + graceWindow + Mapping +
// ErrorGuard + transition gate) and broadcast a status change. Increments
// happen inside applyProbeGuards on the ProbeIntent code path.
var MetricProbeIntentApplied = expvar.NewInt("purdex_probe_intent_applied_total")

// MetricProbeIntentDroppedStale counts signals dropped because StaleCheck
// returned false — either at the early fast-path lock (active map cleared
// between detector emit and consumeSignals delivery) or at the final
// critical-section re-check (race between guard pipeline and cancel/rearm).
var MetricProbeIntentDroppedStale = expvar.NewInt("purdex_probe_intent_dropped_stale_total")

// MetricProbeIntentDroppedGrace counts signals dropped by the
// recordHookAt-grace window. Distinct from the legacy
// MetricProbeGraceWindowSuppressed counter so ProbeIntent path can be
// reasoned about independently from the ScreenChange path.
var MetricProbeIntentDroppedGrace = expvar.NewInt("purdex_probe_intent_dropped_grace_total")

// MetricProbeIntentDroppedErrorGuard counts signals dropped because
// currentStatus was already Error (probe is recovery-only; never overwrite
// an existing error state).
var MetricProbeIntentDroppedErrorGuard = expvar.NewInt("purdex_probe_intent_dropped_error_guard_total")

// MetricProbeIntentDroppedTransitionGate counts signals dropped because the
// mapped status equaled the existing currentStatus (transition gate;
// suppresses no-op broadcasts).
var MetricProbeIntentDroppedTransitionGate = expvar.NewInt("purdex_probe_intent_dropped_transition_gate_total")

// MetricProbeIntentUnsupportedKind counts arm attempts for kinds with no
// production detector wiring. Per audit F6: lifecycle MUST fail-closed so
// new providers declaring an unwired kind surface a runtime signal rather
// than silently appearing armed with a noop detector. Drift test exercises
// production routing rather than just a wiredKinds mirror.
var MetricProbeIntentUnsupportedKind = expvar.NewInt("purdex_probe_intent_unsupported_kind_total")
