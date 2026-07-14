# Plan — Manual cwd edit in Snapshot settings

Implements `docs/specs/2026-07-14-snapshot-cwd-edit-spec.md`. Pure-SPA. Subagent TDD, one commit per task.

## What already exists (verbatim, do not rebuild)

- `spa/src/lib/snapshot/types.ts` — `SessionMeta { hostId; sessionCode; name; mode; cwd?; currentCommand?; restorable; captureError?: 'host-unreachable'|'session-dead-at-capture'|'cwd-probe-failed' }`; `WorkspaceSnapshot { …; sessionMeta: Record<hostId, Record<sessionCode, SessionMeta>> }`.
- `spa/src/lib/snapshot/storage.ts` — `readSnapshot(): WorkspaceSnapshot | null`, `writeSnapshot(snap): void`.
- `spa/src/components/settings/SnapshotSettingsSection.tsx`:
  - `refresh = () => setSnap(readSnapshot())` (:163) — re-reads snapshot + (via the `[snap]`-keyed effect) re-runs health reconciliation.
  - `TmuxBlock({ snap, liveByHost, busy, onRebuild, t })` (:373) renders the table; the **Directory** `<td>` (:429) is `{meta.cwd ?? '—'}`.
  - `computeHealth` (:61) already gates 🔴 `dead` on `restorable && cwd` (F2), so a restorable flip re-renders the badge correctly.

## Task 1 — `setSessionMetaCwd` pure function (+ tests)

**File:** `spa/src/lib/snapshot/storage.ts` (near read/write) or a small sibling; **Test:** extend `storage.test.ts` (or a new `edit.test.ts`).

```ts
export function setSessionMetaCwd(
  snap: WorkspaceSnapshot, hostId: string, sessionCode: string, rawCwd: string,
): WorkspaceSnapshot
```
- `const cwd = rawCwd.trim()`. Composite key `[hostId][sessionCode]`. Return a NEW snapshot; never mutate input.
- Missing target entry → return `snap` unchanged.
- Non-empty → `{ ...meta, cwd, restorable: true, captureError: undefined }`.
- Empty → `{ ...meta, cwd: undefined, restorable: false, captureError: undefined }`.
- Only the target entry changes; other hosts / codes untouched.

**Tests:** non-empty sets cwd+restorable+clears captureError; non-empty on a `cwd-probe-failed`/dead entry flips restorable true + clears captureError; empty/whitespace → cwd undefined + restorable false; input not mutated (new object); cross-host same code + other codes isolated; unknown key → unchanged.

**Commit:** `feat(snapshot): setSessionMetaCwd pure snapshot cwd mutation (T1)`

## Task 2 — `EditableCwdCell` + wire into TmuxBlock (+ tests)

**Files:** new `spa/src/components/settings/EditableCwdCell.tsx` + test; modify `SnapshotSettingsSection.tsx`.

- `EditableCwdCell({ cwd, onCommit }: { cwd?: string; onCommit: (value: string) => void })`:
  - Display mode: `<span>` showing `cwd ?? '—'` (keep the `font-mono` styling), a `title`/cursor affordance, stable `data-testid` (e.g. `snapshot-cwd-cell`), `onDoubleClick` → edit mode.
  - Edit mode: controlled `<input>` pre-filled with `cwd ?? ''`, auto-focus + select. **Enter** or **blur** → `onCommit(inputValue)` then leave edit; **Esc** → leave edit without committing. Guard against double-commit on Enter-then-blur.
  - Owns only local draft/editing state.
- In `SnapshotSettingsSection`, add `handleCommitCwd = (hostId, code, value) => { const cur = readSnapshot(); if (!cur) return; writeSnapshot(setSessionMetaCwd(cur, hostId, code, value)); refresh() }`. Pass `onCommitCwd` down to `TmuxBlock`; replace the Directory `<td>` body (:429) with `<EditableCwdCell cwd={meta.cwd} onCommit={(v) => onCommitCwd(meta.hostId, meta.sessionCode, v)} />`.

**Tests (RTL, mock `snapshot/storage`):** double-click → input pre-filled; Enter → `writeSnapshot` called with updated cwd + cell shows new value; blur → commits; Esc → no `writeSnapshot`, reverts; editing a ⚠️ no-cwd row to a real path → after commit that row's `snapshot-health-*` badge becomes 🔴 (drive restorable flip via real `setSessionMetaCwd` + `refresh`, mocking `listSessions` to keep host reachable + session not live); empty commit → row health ⚠️.

**Commit:** `feat(snapshot): inline-editable cwd cell in snapshot settings (T2)`

## Done criteria

`npx vitest run` full green (no regressions); `pnpm run lint` + `pnpm run build` green. PR → codex review.

## Notes

- No daemon calls; live sessions untouched. Re-capture overwrites edits (by design). No path validation / `~` expansion.
- `EditableCwdCell` extraction is scoped; the broader hook/subcomponent refactor stays in #921.
