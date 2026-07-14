# Spec — Manually edit Session cwd (rebuild target) in the Snapshot settings

## Goal

Let the user correct or supply the rebuild-target working directory for any session in the Snapshot Sessions reconciliation table by double-clicking the **Directory** cell and editing it inline. This is a purely client-side edit of the persisted snapshot; it does **not** touch the daemon or any live tmux session — it only changes what cwd `Rebuild all` / `Restore everything` will pass to `createSession` for that session.

### Motivation

Captured cwd comes from tmux `#{pane_current_path}` (alpha.321), but it can still be wrong for a user's intent, or fall back to the unexpanded session start dir (`captureError: 'cwd-probe-failed'`), or be absent (`restorable: false`, structure-only). Manual editing gives the user direct control over each session's rebuild location, and — because **all rows are editable** — lets a structure-only (⚠️) session become rebuildable simply by supplying a cwd.

## Decisions (user-approved, do not relitigate)

1. **All rows are editable** (🟢 live / 🔴 dead-rebuildable / ⚠️ structure-only / ⚪ offline). Editing a ⚠️ no-cwd row to a non-empty path makes it rebuildable; editing a 🟢 live row sets the cwd that will be used if it later dies and is rebuilt.
2. **Re-capture overwrites**: a manual edit applies to the *current* snapshot only. Clicking "Capture snapshot" writes a fresh snapshot and manual edits are lost. No sticky-override layer (YAGNI).
3. **Empty input clears the cwd** → the session becomes structure-only (`restorable: false`). This is the intentional "make non-rebuildable" capability of an all-rows-editable table.
4. **No path validation / no `~` expansion / no daemon call**: accept the user's literal trimmed input. The host may be offline or the path created later; respecting the literal input is correct.

## Scope

- **In**: inline edit of the Directory (cwd) cell in the Snapshot settings Tmux reconciliation table; a pure snapshot-mutation function; persistence to snapshot storage; health-badge re-computation via the existing refresh flow.
- **Out**: name/command/host columns (not editable); the Tabs block; daemon interaction; live-session cwd change; sticky overrides across re-capture; path existence validation.

## Design

### Layer 1 — data mutation (pure, unit-testable)

New function in the snapshot lib (`spa/src/lib/snapshot/storage.ts` or a small sibling — implementer's choice, keep it near `readSnapshot`/`writeSnapshot`):

```ts
export function setSessionMetaCwd(
  snap: WorkspaceSnapshot,
  hostId: string,
  sessionCode: string,
  rawCwd: string,
): WorkspaceSnapshot
```

Behaviour (returns a NEW snapshot; input never mutated; composite key `[hostId][sessionCode]`):
- `const cwd = rawCwd.trim()`.
- If the target `sessionMeta[hostId][sessionCode]` does not exist → return `snap` unchanged (defensive; the UI only edits existing rows).
- **Non-empty** `cwd` → updated entry: `{ ...meta, cwd, restorable: true, captureError: undefined }` (the manual value is authoritative and supersedes `cwd-probe-failed` / a prior dead capture; the `restorable ⟺ has-usable-cwd` invariant, matching capture.ts and the `computeHealth` 🔴 predicate, is preserved).
- **Empty** `cwd` → updated entry: `{ ...meta, cwd: undefined, restorable: false, captureError: undefined }` (structure-only).
- Other hosts / other sessionCodes untouched.

A thin commit helper (either exported or inlined at the call site) reads the current snapshot, applies `setSessionMetaCwd`, and `writeSnapshot`s the result:
```ts
// pseudo: read → apply → persist
const next = setSessionMetaCwd(readSnapshot()!, hostId, code, value)
writeSnapshot(next)
```
Guard: if `readSnapshot()` is null (no snapshot), the cell is not rendered/editable, so this path is unreachable; still, no-op safely if it happens.

### Layer 2 — inline-edit UI

Extract a small `EditableCwdCell` component (nudges the #921 direction without doing the full refactor):

- **Display mode**: renders the current cwd string (or an em-dash / muted placeholder when `cwd` is undefined). A `title`/cursor affordance signals it is editable; `data-testid` stable for tests.
- **Enter edit**: `onDoubleClick` swaps to a controlled `<input>` pre-filled with the current cwd (empty string when undefined), auto-focused, text selected.
- **Commit**: **Enter key** or **blur** → trim value → call the commit helper → the parent's existing `refresh()` re-reads the snapshot and re-runs health reconciliation → the row re-renders with the new cwd and updated health badge (e.g. ⚠️ → 🔴 after supplying a path). Then leave edit mode.
- **Cancel**: **Esc** → discard, leave edit mode, no write.
- The cell owns only its local editing/draft state; the snapshot is the single source of truth.

Wire `EditableCwdCell` into the Tmux block row's Directory column in `SnapshotSettingsSection.tsx`, passing `hostId`, `sessionCode`, current `cwd`, and an `onCommit(hostId, code, value)` callback (which does read→apply→write→`refresh()`).

### Layer 3 — health re-computation

No new health state. Editing changes `restorable`/`cwd`; the existing `refresh()` (re-read snapshot + re-reconcile via `listSessions`) already re-renders the health badge. Re-running `listSessions` on a cwd edit is redundant (liveness unchanged) but harmless for an infrequent manual action; reuse the existing flow rather than adding a cwd-only fast path.

## Testing

**Pure function (`setSessionMetaCwd`)**
- Non-empty cwd on an existing entry → `cwd` set, `restorable: true`, `captureError` cleared; input snapshot not mutated (new object).
- Non-empty cwd on a `cwd-probe-failed` / dead (`restorable:false`) entry → becomes `restorable: true`, `captureError` undefined.
- Empty / whitespace-only cwd → `cwd: undefined`, `restorable: false`, `captureError: undefined`.
- Composite-key isolation: same `sessionCode` under a different host, and other codes under the same host, are untouched.
- Unknown `(hostId, sessionCode)` → snapshot returned unchanged.

**Component (`EditableCwdCell` + section)**
- Double-click Directory cell → input appears pre-filled with the current cwd.
- Enter → commits: `writeSnapshot` called with the updated cwd, row shows the new value.
- Blur → commits (same as Enter).
- Esc → cancels: no `writeSnapshot`, cell reverts to original display.
- Editing a ⚠️ (no-cwd) row to a real path → after commit, that row's health badge becomes 🔴 (rebuildable). (Drives the restorable-flip end-to-end through `refresh()`.)
- Empty input committed → row becomes structure-only (health ⚠️).

## Acceptance criteria

- `setSessionMetaCwd` unit tests + `EditableCwdCell`/section tests green; full `npx vitest run` no regressions; `pnpm run lint` + `pnpm run build` green.
- Double-clicking a Directory cell edits the cwd; Enter/blur persists to the snapshot and updates the row + health badge; Esc cancels.
- A structure-only session gains a cwd and becomes rebuildable; a rebuildable session's cwd cleared becomes structure-only.
- No daemon calls introduced by editing; live sessions untouched.

## Phasing

Single small PR (one review unit): Layer 1 pure function (+ tests) → Layer 2 `EditableCwdCell` + wiring (+ tests) → Layer 3 is emergent (reuses existing `refresh()`). Implemented as separate commits per layer for reviewability.
