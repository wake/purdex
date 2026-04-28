# sweep:pid_dead Broadcast Clear-Status Hotfix Spec

- **Version**: 1.0.0-alpha.249 (target bump after merge)
- **Date**: 2026-04-29
- **Base**: `7463883f` (main @ alpha.249)
- **Author**: claude-code + wake
- **Status**: Draft
- **Tracking**: #717 (bug, daemon, spa)
- **Worktree**: `.claude/worktrees/sweep-pid-dead-broadcast`
- **Branch**: `worktree-sweep-pid-dead-broadcast`

## 1. Context

When an agent process inside a tmux pane dies (e.g. `Ctrl+C` on opencode),
the daemon's sweep loop detects the dead PID and clears the corresponding
frame from the DB. After the frame is cleared, sweep emits a
`sweep:pid_dead` broadcast so the SPA can update the agent indicator.

Live mlab repro (issue #717) confirms detection and DB cleanup are
correct, but the broadcast carries `status=""` instead of
`status="clear"`. The SPA's `useAgentStore.handleNormalizedEvent`
strictly tests `event.status === 'clear'` and a falsy `if (status)`
guard at the status block, so an empty status is silently dropped: the
indicator (icon + agent_type) stays at its last value, looking as if
the agent is still alive.

Symptom: SPA agent activity indicator does not clear after opencode
exits via SIGINT, even though the daemon has fully reaped the frame.

## 2. Root Cause — Asymmetric Nil Branches

`internal/module/agent/frame_ops.go:649-670` defines
`buildProjectionNormalized`, which converts internal projection state
into a wire-format `NormalizedEvent` for the SPA. The function has
three control branches:

```go
normalized := agentpkg.NormalizedEvent{
    AgentType: fallbackAgentType,
    Status:    string(result.Status),   // (*) caller-supplied status
    ...
}
if projection == nil {
    return normalized                   // [A] caller's status leaks through
}
normalized.Subagents = append(...)
if projection.TopFrame == nil {
    normalized.Status = string(agentpkg.StatusClear)  // [B] forces clear
    return normalized
}
normalized.AgentType = projection.TopFrame.AgentType
normalized.Status = string(projection.TopFrame.Status)  // [C] uses TopFrame
return normalized
```

Branches [A] and [B] semantically mean the same thing: **no top frame
exists for the SPA to display**. They should produce the same
`Status` (`clear`). But only [B] enforces this — [A] passes through
whatever the caller put into `result.Status`.

`sweep.go:551` (the `afterFrameCleared` callsite) calls the function
with an empty `agentpkg.DeriveResult{}` because sweep is not deriving
a status from a hook event — it is just reporting "frame cleared,
recompute projection". When the dead frame was the only frame in the
session, projection is nil → branch [A] runs → empty status leaks.

## 3. Decision — Option B (frame_ops symmetric guard)

Two viable fixes were considered.

### Option A — Surgical at sweep callsite

Pre-cook `DeriveResult{Status: agentpkg.StatusClear}` at `sweep.go:551`
so branch [A] receives `clear` instead of `""`.

**Rejected** for these reasons:

1. **Scatters the invariant.** "No top frame ⇒ broadcast clear" is a
   property of the wire format, not of any specific caller. Encoding
   it at the callsite means each future nil-projection caller must
   re-discover and re-encode the rule.
2. **Misleads the reader.** `Status: StatusClear` at the sweep
   callsite reads as "I'm broadcasting clear", but if a sibling frame
   survived the sweep, branch [C] still wins and the actual broadcast
   carries `TopFrame.Status`. The callsite hint is divorced from the
   real output.
3. **Leaves the latent asymmetry in `frame_ops.go`.** A future caller
   that passes nil projection without setting status would re-trigger
   the same bug.
4. **Internally contradictory.** Sweep calls
   `buildProjectionNormalized` precisely so the function will figure
   out the right status across the three projection states. Forcing
   the callsite to also pre-supply a status undermines that
   abstraction.

### Option B — Symmetric guard in `buildProjectionNormalized` (chosen)

Add a defensive guard inside the `projection == nil` branch so the
function itself enforces "no top frame ⇒ status defaults to clear":

```go
if projection == nil {
    if normalized.Status == "" {
        normalized.Status = string(agentpkg.StatusClear)
    }
    return normalized
}
```

**Properties**:

- **Single source of truth.** The invariant lives in the function that
  owns the wire format. Callers no longer need to know about the
  nil-projection edge case.
- **Backwards-compatible.** When a caller passes nil projection with
  a non-empty `result.Status` (e.g. `handler.go:211` error_guard
  path), the guard is skipped and the caller's status is preserved.
- **Surgical.** One conditional inside an existing 22-line function.
  No new helpers, types, abstractions, files, comments, or
  reorganization.
- **Symmetric to branch [B].** Both branches now share the
  "no top frame ⇒ clear" semantics; only the means differ
  (unconditional in [B], guarded in [A]).

## 4. Callsite Audit — Why the Guard is Safe

`buildProjectionNormalized` has 7 callsites
(`grep -n buildProjectionNormalized internal/module/agent/*.go`):

| Callsite | Projection | `result.Status` | Branch |
|----------|-----------|-----------------|--------|
| `module.go:388` (replay) | non-nil | `""` (DeriveResult{}) | [B] or [C] |
| `handler.go:211` (error_guard_blocked) | **nil** | **non-empty** (gated by line 196: `result.Valid && result.Status != "" && result.Status != StatusError`) | [A] |
| `handler.go:266` (event handler) | non-nil | derived | [B] or [C] |
| `handler.go:328` (event handler) | non-nil | derived | [B] or [C] |
| `probe_orchestrator.go:357` (probe:activity) | non-nil | `""` | [B] or [C] |
| `sweep.go:327` (sweep:proxy_canonicalized) | non-nil | `""` | [C] |
| `sweep.go:499` (sweep:proxy_pruned) | non-nil | `""` | [C] |
| `sweep.go:551` (sweep:pid_dead) | **nil-or-non-nil** | `""` | **[A] or [B] or [C]** |

The new guard only triggers when **projection is nil AND result.Status
is empty**. The audit confirms that combination is unique to
`sweep.go:551` (the bug case). `handler.go:211` is the only other
nil-projection callsite, and line 196 guarantees a non-empty
`result.Status` reaches it — guard is bypassed, behavior unchanged.

## 5. Test Strategy

### 5.1 Test placement

Tests live in `internal/module/agent/frame_ops_test.go`
(invariant-level) and `internal/module/agent/sweep_test.go`
(integration-level).

### 5.2 frame_ops_test.go — invariant coverage

Direct unit tests on `buildProjectionNormalized` covering all four
state combinations:

| # | projection | TopFrame | result.Status | Expected `Status` |
|---|-----------|----------|---------------|-------------------|
| 1 | nil | — | `""` | `"clear"` (new guard fires) |
| 2 | nil | — | `"running"` | `"running"` (guard skipped, caller wins) |
| 3 | non-nil | nil | any | `"clear"` (branch [B], unchanged) |
| 4 | non-nil | non-nil | any | `TopFrame.Status` (branch [C], unchanged) |

Test #2 specifically guards against regression at `handler.go:211`.

### 5.3 sweep_test.go — broadcast capture

End-to-end test that exercises sweep → DB delete → broadcast pipeline:

- **Test A**: Single frame, PID dies, sweep clears. Capture
  `sweep:pid_dead` broadcast payload and assert
  `event.status === "clear"`.
- **Test B**: Two frames in same session (different panes), one PID
  dies, sweep clears it. Capture broadcast and assert
  `event.status` reflects the surviving `TopFrame.Status` (i.e. the
  guard does not over-clear when siblings exist).

### 5.4 Capture mechanism

Inspect existing `newSweepTestModule` helper. If `m.core.Events` is
already wired with a capture-friendly broadcaster, reuse it.
Otherwise, install a minimal in-memory capture seam (e.g. a fake
broadcaster that records `(code, kind, payload)` tuples). Do **not**
introduce a new abstraction or production seam — capture is
test-only.

### 5.5 SPA-side regression coverage

`spa/src/stores/useAgentStore.test.ts` is not modified. The SPA's
`status === 'clear'` branch is already covered by existing tests; the
fix is in the daemon's wire output, not in SPA logic.

## 6. Scope Discipline (What NOT To Touch)

- ❌ Do not collapse branches [A] and [B] into a single
  `projection == nil || projection.TopFrame == nil` block. That is a
  separate refactor.
- ❌ Do not introduce a helper (e.g. `clearStatusIfNoTopFrame`) for
  the new guard — keep it inline.
- ❌ Do not add comments inside `buildProjectionNormalized`. Per
  CLAUDE.md "default no comments". The function name + symmetric
  branches are self-documenting.
- ❌ Do not modify `sweep.go:551`, `sweep.go:327`, or `sweep.go:499`.
- ❌ Do not modify `handler.go:211` (callsite audit confirms it is
  unaffected; touching it expands review surface for no reason).
- ❌ Do not modify any SPA file. The fix is server-side; SPA contract
  (`status === 'clear'`) is unchanged.
- ❌ Do not add liveness probe / heartbeat / process-tree watcher
  features (originally proposed in #717 body but ruled out — sweep
  already does PID polling every 2s; adding more polling is
  unnecessary).

## 7. Phase Plan

Single phase. The change is one production-code edit (3 lines) plus
test additions.

| Step | Action |
|------|--------|
| 1 | Spec review by codex (this file) |
| 2 | Write plan (single-phase TDD task breakdown) |
| 3 | Plan review by codex |
| 4 | TDD: write failing tests in `frame_ops_test.go` and `sweep_test.go` first |
| 5 | Apply 3-line guard at `frame_ops.go:659` |
| 6 | Verify all tests green: `go test ./internal/module/agent/...` |
| 7 | Open PR; cross-model codex review (2 rounds: standard + adversarial 3-parallel) |
| 8 | Address findings, merge |
| 9 | Independent bump PR (alpha.250) |

## 8. Manual Verification Plan (PR Test Plan)

Live test on mlab to reproduce the original symptom and confirm the
fix:

```bash
# Build daemon with fix
go build -o /tmp/pdx-fix ./cmd/pdx

# Restart daemon
pkill -f "pdx serve" || true
PDX_DEV_MODE=1 nohup /tmp/pdx-fix serve > /tmp/pdx.log 2>&1 &

# Trigger repro: open opencode in tmux, exchange a prompt, Ctrl+C
tmux new -s opencode-fix-test
opencode
# > /chat hello
# Wait for response, then Ctrl+C twice to exit opencode

# Verify SPA agent indicator clears immediately for that session
# (check Purdex SPA — the agent dot should disappear within ~2s sweep cadence)

# Forensic confirmation
sqlite3 ~/.config/pdx/agent_events.db \
  "SELECT root_event_name, latest_decision FROM agent_trace_chains \
   WHERE root_agent_type='opencode' ORDER BY started_at DESC LIMIT 5"
# Expected: latest sweep:pid_dead broadcast carries status=clear (visible in
# WS frame, captured separately if needed)
```

## 9. Rollback

The guard is purely additive within an existing branch. Reverting is
a single-line removal with no migration concerns.

## 10. Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Guard fires for an unintended callsite | Very low | Wrong status broadcast | Callsite audit (§4) + invariant test #2 |
| Test capture seam leaks into production | Low | Code smell | Keep capture test-only (§5.4) |
| SPA contract changes underneath | Very low | Fix becomes ineffective | SPA test suite covers `status === 'clear'`; no SPA changes in this PR |

## 11. Out of Scope (Tracked Elsewhere)

- Liveness heartbeat / process-tree watch / upstream opencode
  shutdown hook — issue #717 originally listed these, but sweep's
  existing 2s PID poll is sufficient once the broadcast is correct.
- Refactor of `buildProjectionNormalized` to collapse branches [A]
  and [B] — possible future cleanup, not required for this fix.
- Other `sweep:*` reasons (`proxy_canonicalized`, `proxy_pruned`) —
  audited safe (§4); no changes.
