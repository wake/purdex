# Revive by Name — Implementation Plan

**Status:** v1 (draft, pending codex plan review)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A pane terminated with `tmux-restarted` re-attaches on its own when a
live session on the same host carries the name the pane last saw. Two clients
on one daemon: one presses Rebuild, the other's tab comes back without a click.

**Architecture:** One new decision (`revive`) in the pure reconcile function,
applied in the `sessions` handler through a gate that reads the rebuild
operation store, using the engine's existing re-point writer. Plus one
idempotency short-circuit in `repointMember` so "Rebuild all" reports a member
already revived by name as a success.

**Tech Stack:** React 19 / Zustand 5 / Vitest · pnpm. SPA only.

**Spec:** `docs/specs/2026-09-14-revive-by-name-spec.md`

## Global Constraints

- **TDD, no exceptions.** Failing test first, run it red, implement, run green.
  One commit per task.
- **Commit messages in English.** Every commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01GYmtjYhswExftPy2557iG4
  ```
- **Verification commands** (exact forms; root `package.json` has no lint/build):
  ```
  pnpm --prefix spa exec vitest run
  pnpm --prefix spa run lint
  pnpm --prefix spa run build
  ```
- **Every Bash command is prefixed `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/revive-by-name && `**
  and every Edit/Write path is absolute under that directory.
- **Existing tests stay green untouched.** If a pre-existing test needs editing
  to pass, stop and report — do not edit it.
- **No new i18n strings, no UI.** A revived pane simply stops being terminated.
- **`revivable` is optional** on `ReconcileInput` so no existing caller changes.

## File Structure

| File | Change |
|---|---|
| `spa/src/lib/rebuild/reconcile.ts` | `RevivablePane`, `ReviveDecision`, `revive` output, the name rule |
| `spa/src/hooks/useMultiHostEventWs.generation.test.ts` | new `describe('reconcileSessionsPayload — revive')` block |
| `spa/src/lib/rebuild/revive.ts` (new) | `reviveAllowed(paneId, binding, operations)` |
| `spa/src/lib/rebuild/revive.test.ts` (new) | gate table cases |
| `spa/src/lib/rebuild/engine.ts` | export `defaultRepoint` as `repointPaneToSession`; `repointMember` short-circuit |
| `spa/src/lib/rebuild/engine.test.ts` | `repointMember` idempotency cases |
| `spa/src/lib/rebuild/batch.test.ts` | S12 |
| `spa/src/hooks/useMultiHostEventWs.ts` | collect revivable panes, apply `revive` through the gate |
| `spa/src/hooks/useMultiHostEventWs.revive.test.ts` (new) | end-to-end through `FakeSocket` |

---

### Task 1: The `revive` decision

**Files:**
- Modify: `spa/src/lib/rebuild/reconcile.ts`
- Modify: `spa/src/hooks/useMultiHostEventWs.generation.test.ts`

**Interfaces produced** (exactly as spec §3.1):
```ts
export interface RevivablePane { hostId; tabId; paneId; sessionCode; tmuxInstance; cachedName }
export interface ReviveDecision { tabId; paneId; binding: { hostId; sessionCode; tmuxInstance }; session: ReconcileSession }
// ReconcileInput.revivable?: RevivablePane[]
// ReconcileOutcome.revive: ReviveDecision[]
```

**Rule.** For each revivable pane whose `hostId` matches: find the session with
`name === cachedName`. Emit only if that session's `tmux_instance` is non-empty
**and** differs from `pane.tmuxInstance`, and its `mode` is absent or
`'terminal'`. Build the live sessions into a `Map<name, session>` once; if two
payload entries share a name (cannot happen on one tmux server, but the
function must not depend on it) keep the **first** and treat the case as
undefined behaviour — do not add a tie-break rule, the spec has none.

The loop over `revivable` is separate from the loop over `panes` and touches
neither `terminate` nor `adoptInstance`.

- [ ] **Step 1: Write the failing tests** in the generation test file, new
  `describe('reconcileSessionsPayload — revive')`:
  - S1: revivable `dev` @ `111:1000`; live `{code:'abc123', name:'dev', tmux_instance:'222:2000'}` → one decision carrying `tabId`, `paneId`, `binding {h1, oldcode, 111:1000}`, the full session object.
  - S4: live instance `''` → `revive: []`.
  - live instance equal to the pane's → `revive: []`.
  - S5: live named `dev-2` only → `revive: []`.
  - S6: live `mode: 'stream'` → `revive: []`; `mode` absent → revives.
  - S13: revivable on `h2`, payload for `h1` → `revive: []`.
  - S14: two revivable panes both named `dev` → two decisions, same session.
  - no `revivable` key → `revive: []` and existing outputs unchanged (assert `terminate`/`adoptInstance` on an input that also has live panes equal the values they have today).
  - a revivable pane whose code is also in the live list under a new generation (the `$0` reuse case) → still decided by **name**, not by code; assert the decision's session is the one named `dev` even when a different live session carries the pane's old code.
- [ ] **Step 2: Run** `pnpm --prefix spa exec vitest run src/hooks/useMultiHostEventWs.generation.test.ts` — red.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same file — green. Then the full `vitest run`.
- [ ] **Step 5: Commit** — `feat(rebuild): decide revive-by-name for tmux-restarted panes`

---

### Task 2: The gate, the exported re-point, and the wiring

**Files:**
- Create: `spa/src/lib/rebuild/revive.ts`, `spa/src/lib/rebuild/revive.test.ts`
- Modify: `spa/src/lib/rebuild/engine.ts` (export only — rename `defaultRepoint` → `repointPaneToSession`, keep the default-parameter use in `repointMember`)
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`
- Create: `spa/src/hooks/useMultiHostEventWs.revive.test.ts`

**Interfaces produced:**
```ts
// revive.ts — pure over the store slice so the table test needs no React.
export function reviveAllowed(
  paneId: string,
  binding: RebuildBinding,
  operations: Record<string, RebuildOperation>,
): boolean
```
Rule (spec §3.2): no op → true; op binding ≠ pane binding → true; running → false;
done with `createdSession` → false; done without → true. Use `bindingEquals`
from `binding.ts` (exact, not legacy).

**Wiring** in the `sessions` handler of `useMultiHostEventWs.ts`. While
collecting `panes` (the existing `scanPaneTree` at ~line 157), also collect
`revivable`: `kind === 'tmux-session' && hostId === hostId && terminated === 'tmux-restarted' && mode === 'terminal'`. Pass it to `reconcileSessionsPayload`.
Apply after the terminate loop and before `openAttachGate(hostId)`:
```ts
for (const d of outcome.revive) {
  if (!reviveAllowed(d.paneId, d.binding, useRebuildStore.getState().operations)) continue
  repointPaneToSession(d.tabId, d.paneId, d.session as Session)
}
```
`d.session` is a `ReconcileSession` (partial); `repointPaneToSession` reads
`code`, `name`, `tmux_instance` and passes the object into the session store.
The handler's `data` is already `Session[]`, so the objects in the decision
ARE full sessions — cast at the call site with a one-line comment, do not widen
`ReconcileSession`.

- [ ] **Step 1: Write the failing tests.**
  `revive.test.ts` — one `it.each` table: S7 (running), S8 (done + createdSession), S9 (done, no createdSession), S10 (op binding differs), no op. Seed `operations` literals directly; the `RebuildOperation` type needs `paneId, tabId, hostId, plan, binding, resumeCommand, status, report, startedAt` — build a small factory.
  `useMultiHostEventWs.revive.test.ts` — copy the `FakeSocket` + `beforeEach` harness from `gate.test.ts`, but give `useSessionStore` its real `replaceHost` (the handler's `replaceHost` runs before revive; the fake in gate.test is a `vi.fn()` and that is fine too — revive reads the payload, not the store). Seed one tab via `useTabStore.setState` with a `tmux-restarted` terminal pane `cachedName: 'dev'`, `sessionCode: 'old', tmuxInstance: '111:1000'`, with a rebuild record. Emit `{type:'sessions', value: JSON.stringify([{code:'new1', name:'dev', tmux_instance:'222:2000', mode:'terminal', ...}])}`. Assert on the tab store:
  - S1: `sessionCode === 'new1'`, `tmuxInstance === '222:2000'`, `terminated` undefined, `rebuild.sessionName === 'dev'`, `rebuild.tmuxInstance === '222:2000'`, `rebuild.agent` preserved.
  - S2: same but `terminated: 'session-closed'` → untouched.
  - S3: `'host-removed'` → untouched.
  - S7: seed `useRebuildStore` with a running op on the pane's binding → untouched.
  - a second payload after S1 (same list) → nothing changes, no error (the pane is now live and reconciles normally).
  Look at `useMultiHostEventWs.cwd-probe.test.ts` for how the existing tests keep the probes quiet (mock `../lib/rebuild/cwd-probe` and `../lib/rebuild/provenance-probe` if they fire fetches).
- [ ] **Step 2: Run both new files — red** (the import of `revive.ts` fails; the hook test fails on `sessionCode`).
- [ ] **Step 3: Implement** `revive.ts`, the export rename in `engine.ts`, the wiring.
- [ ] **Step 4: Run both files green, then full `vitest run`, `lint`, `build`.**
- [ ] **Step 5: Commit** — `feat(rebuild): revive tmux-restarted panes by session name on every sessions payload`

---

### Task 3: `repointMember` is idempotent

**Files:**
- Modify: `spa/src/lib/rebuild/engine.ts` (`repointMember`)
- Modify: `spa/src/lib/rebuild/engine.test.ts`, `spa/src/lib/rebuild/batch.test.ts`

**Rule** (spec §3.3): before the `bindingUnchanged` check, read the pane; if it
is a terminal tmux pane with `sessionCode === created.code &&
tmuxInstance === (created.tmux_instance ?? '')`, return `{ repointed: true }`
without calling `repoint`. `assertHostUnchanged` still runs first — a pinned
host that changed is still the truer reason.

- [ ] **Step 1: Write the failing tests.**
  `engine.test.ts`, new `describe('repointMember — idempotent')`:
  - pane already bound to `created` (code + instance) → `{ repointed: true }` and the injected `repoint` spy is **not** called.
  - pane bound to `created.code` but a different instance → `{ repointed: false, reason: 'the pane binding changed' }`.
  - pane bound elsewhere → unchanged reason.
  - `assertHostUnchanged` throws while the pane is already on `created` → the host reason wins.
  `batch.test.ts`, in `runBatchRebuild`: S12 — two panes on the same dead session; `createSession` fake re-points `p2` onto the created session **by name** (use `useTabStore.getState().setPaneContent` with the created code/instance and no `terminated`, i.e. what the WS handler will do) before returning; assert the member result for `p2` is `repointed: true` and both panes end on `new1`.
- [ ] **Step 2: Run — red.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Full `vitest run`, `lint`, `build` — green.**
- [ ] **Step 5: Commit** — `fix(rebuild): a batch member already on the created session counts as re-pointed`

---

## Manual verification (after deploy, user's call)

On the two-client setup: kill the tmux server on mlab, Rebuild one tab on
client A, watch the same tab on client B come back on its own.

## Review items carried in

(filled after codex plan review)
