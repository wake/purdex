# Revive by Name — Implementation Plan

**Status:** v3 — revised after codex plan review `task-mu07mlre-w7q0zt` (1 Blocker, 3 Important, 3 Minor; all folded in, dispositions at the end)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A pane terminated with `tmux-restarted` re-attaches on its own when a
live session on the same host carries the name the pane last saw. Two clients
on one daemon: one presses Rebuild, the other's tab comes back without a click.

**Architecture:** A new module `spa/src/lib/rebuild/revive.ts` holds one pure
decision (`decideRevive`), one pure gate (`reviveAllowed`) and one pass
(`runRevivePass`) that scans the tab store for `tmux-restarted` terminal panes
**after** reconciliation, matches them by name against the session store, and
re-points them through the engine's existing writer. The pass is skipped
wholesale while the rebuild operation lock is held, and is re-run for every
host when that lock is released. Nothing in `reconcile.ts`, `engine.ts`'s
logic or `batch.ts` changes.

**Tech Stack:** React 19 / Zustand 5 / Vitest · pnpm. SPA only.

**Spec:** `docs/specs/2026-09-14-revive-by-name-spec.md` (v2)

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
- **`reconcile.ts` is not modified.** The decision lives in `revive.ts`.

## File Structure

| File | Change |
|---|---|
| `spa/src/lib/rebuild/revive.ts` (new) | `ReviveCandidate`, `ReviveDecision`, `decideRevive`, `reviveAllowed`, `collectCandidates`, `runRevivePass`, `runRevivePassAll` |
| `spa/src/lib/rebuild/revive.test.ts` (new) | unit tests for the two pure functions and the pass |
| `spa/src/lib/rebuild/engine.ts` | rename `defaultRepoint` → exported `repointPaneToSession` (no logic change) |
| `spa/src/lib/rebuild/engine.test.ts` | pin the export |
| `spa/src/hooks/useMultiHostEventWs.ts` | call `runRevivePass(hostId)` in the `sessions` handler; subscribe to the lock release |
| `spa/src/hooks/useMultiHostEventWs.revive.test.ts` (new) | end-to-end through `FakeSocket` |

---

### Task 1: `decideRevive` and `reviveAllowed`

**Files:**
- Create: `spa/src/lib/rebuild/revive.ts`, `spa/src/lib/rebuild/revive.test.ts`

**Interfaces produced** (exactly as spec §3.1 / §3.2):
```ts
export interface ReviveCandidate {
  hostId: string; tabId: string; paneId: string
  sessionCode: string; tmuxInstance: string; cachedName: string
}
export interface ReviveDecision { tabId: string; paneId: string; binding: RebuildBinding; session: Session }
export function decideRevive(hostId: string, sessions: Session[], candidates: ReviveCandidate[]): ReviveDecision[]
export function reviveAllowed(paneId: string, binding: RebuildBinding, operations: Record<string, RebuildOperation>): boolean
```
`RebuildBinding` / `RebuildOperation` come from `stores/useRebuildStore.ts`;
`bindingEquals` from `lib/rebuild/binding.ts` (exact, not legacy); `Session`
from `lib/host-api.ts`.

**`decideRevive` rule.** Build `Map<name, Session>` from `sessions` once,
first entry wins on a duplicate name. For each candidate with
`candidate.hostId === hostId`: look up `cachedName`; require the session's
`tmux_instance` to be a non-empty string; reject when
`session.mode !== undefined && session.mode !== 'terminal'` (a `null` mode on
the wire is *present and wrong* — the handler does `JSON.parse` with no schema,
so an unexpected shape must fail closed); emit `{ tabId, paneId, binding: { hostId, sessionCode, tmuxInstance }, session }`.
No generation comparison against the candidate's own instance (spec §3.1,
review finding 5). Pure: no store access.

**`reviveAllowed` rule.** Exactly the five lines in spec §3.2.

- [ ] **Step 1: Write the failing tests** in `revive.test.ts`.
  `describe('decideRevive')`:
  - S1: candidate `dev` @ `111:1000`, live `{code:'abc123', name:'dev', tmux_instance:'222:2000'}` → one decision with `tabId`, `paneId`, `binding {h1, old, 111:1000}`, the identical session object (`toBe`).
  - S1c: live `dev` @ `111:1000` (same as candidate) → revives.
  - S4: live instance `''` → `[]`; instance key absent → `[]`.
  - S5: only `dev-2` live → `[]`.
  - S6: live `mode: 'stream'` → `[]`; `mode` absent → revives; `mode: null` (cast at the boundary: `{ ...session({...}), mode: null } as unknown as Session`) → `[]`.
  - S13: candidate on `h2`, call for `h1` → `[]`.
  - S14: two candidates named `dev` → two decisions, same session.
  - empty candidates → `[]`; empty sessions → `[]`.
  - name beats code: live `[{code:'old', name:'other', tmux_instance:'222:2000'}, {code:'new1', name:'dev', tmux_instance:'222:2000'}]`, candidate `sessionCode:'old', cachedName:'dev'` → decision session is `new1`.
  `describe('reviveAllowed')` — `it.each` over: no op → true; op binding ≠ → true; running (same binding) → false; done + `createdSession` → false; done, no `createdSession` → true. Build ops with a small typed factory `op(over: Partial<RebuildOperation>): RebuildOperation` returning `{ paneId:'p1', tabId:'t', hostId:'h1', plan:{createSession:true,applyCwd:true,runResume:true}, binding, resumeCommand:'', status:'done', report:{hostId:'h1', steps:{create:{status:'skipped'},resume:{status:'skipped'},repoint:{status:'skipped'}}, repointed:false}, startedAt:0, ...over }`.
  **Session fixtures:** copy the typed `session(over: Partial<Session>): Session` factory from `engine.test.ts:18` (it fills `cwd`, `mode`, `cc_session_id`, `cc_model`, `has_relay` — the project is strict TypeScript). The only place `as unknown as Session` is allowed is the explicit wire-shape cases (`mode` absent / `null`); never to hide a missing field in an ordinary fixture.
- [ ] **Step 2: Run** `pnpm --prefix spa exec vitest run src/lib/rebuild/revive.test.ts` — red (module missing).
- [ ] **Step 3: Implement** the two functions and the types. Do **not** add `collectCandidates` / `runRevivePass` yet.
- [ ] **Step 4: Run** the file — green. Then the full `vitest run`.
- [ ] **Step 5: Commit** — `feat(rebuild): decide revive-by-name and its per-pane gate`

---

### Task 2: The pass, the exported writer, and the two triggers

**Files:**
- Modify: `spa/src/lib/rebuild/revive.ts`, `spa/src/lib/rebuild/revive.test.ts`
- Modify: `spa/src/lib/rebuild/engine.ts`, `spa/src/lib/rebuild/engine.test.ts`
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`
- Create: `spa/src/hooks/useMultiHostEventWs.revive.test.ts`

**Interfaces produced:**
```ts
// engine.ts — rename only. `repointMember`'s default parameter keeps using it.
export function repointPaneToSession(tabId: string, paneId: string, session: Session): void

// revive.ts
export function collectCandidates(hostId: string): ReviveCandidate[]   // reads useTabStore
export function runRevivePass(hostId: string): void                     // spec §3.2, verbatim
export function runRevivePassAll(): void                                // every id in useHostStore.hostOrder
```

**`collectCandidates`**: `scanPaneTree` over every tab in
`useTabStore.getState().tabs`; keep panes with `content.kind === 'tmux-session'
&& content.hostId === hostId && content.mode === 'terminal' && content.terminated === 'tmux-restarted'`.

**`runRevivePass`** exactly as spec §3.2. The gate query is the existing
`canAttachTerminal(hostId)` (`attach-gate.ts:16`) — spec §3.2 calls it that
too; do **not** add or rename a gate function. Check it first, then `useRebuildStore.getState().lockedBy !== null`
→ return, then decide over `useSessionStore.getState().sessions[hostId] ?? []`,
gate each, write with `repointPaneToSession`.

**Trigger 1** — in the `sessions` handler of `useMultiHostEventWs.ts`, insert
`runRevivePass(hostId)` immediately **after** `openAttachGate(hostId)` and
**before** `probeMissingCwds(hostId)`. One comment line: revived panes are
probed on their final binding, like everything else after reconciliation.

**Trigger 2** — a third `useEffect(() => ..., [])` in the hook:
```ts
useEffect(() => useRebuildStore.subscribe((s, prev) => {
  if (prev.lockedBy !== null && s.lockedBy === null) runRevivePassAll()
}), [])
```
(Zustand's `subscribe` returns the unsubscribe, which is the effect cleanup.)

- [ ] **Step 1: Write the failing tests.**

  `revive.test.ts`, `describe('runRevivePass')` — seed stores directly
  (`useHostStore.setState` with `runtime: { h1: { status:'connected', attachReady: true } }`,
  `useSessionStore.setState({ sessions: { h1: [...] } })`, `useTabStore.setState` with a
  leaf tab whose pane is `tmux-restarted`, `useRebuildStore.setState({ operations: {}, lockedBy: null })`;
  `beforeEach` resets all four). Reuse the fixture shapes from `engine.test.ts` (`seedPane`, `paneContent`).
  - S1: pane revived — `sessionCode`, `tmuxInstance`, `terminated === undefined`, `cachedName`, `rebuild.sessionName`, `rebuild.tmuxInstance` restamped, `rebuild.agent` and `rebuild.cwd` kept.
  - S16: `attachReady: false` (session store seeded with the matching session, lock free) → untouched.
  - S7: `lockedBy: 'rebuild:p9'` → untouched (even though the pane has no op).
  - S8: op `done` + `createdSession` on the pane's binding, lock free → untouched.
  - S9: op `done`, no `createdSession`, lock free → revived.
  - S15: first call → assert revived; capture `paneContent(...)` and its `.rebuild` references; second call → both references unchanged (`toBe`).
  - a pane with no `rebuild` record → revived, `rebuild` stays `undefined`.

  `engine.test.ts`, `describe('repointPaneToSession')`: seeded `tmux-restarted`
  pane with a record → after the call `terminated` is gone, code/name/instance
  are the session's, `rebuild.sessionName`/`tmuxInstance` restamped, other
  record fields kept, and `useSessionStore.sessions.h1` contains the session.

  `useMultiHostEventWs.revive.test.ts` — copy the `FakeSocket` harness from
  `gate.test.ts`; mock `../lib/host-connection` (`checkHealth`),
  `../lib/rebuild/cwd-probe` and `../lib/rebuild/provenance-probe` (both to
  `vi.fn()`s) so no fetch fires. **Use the real `useSessionStore`**: in
  `beforeEach`, `useSessionStore.setState({ sessions: {}, fetchHost: vi.fn(async () => {}) } as never)`
  — the pass reads `sessions` from the store, so `replaceHost` must be real.
  Drive one host to `connected` the way `gate.test.ts` does (`act` + `waitFor` on
  `sockets[0]`, `onopen`), then `emit` a `{type:'sessions', session:'', value: JSON.stringify([...])}`
  event (check the exact envelope shape `connectHostEvents` parses in
  `lib/host-events.ts` and copy it).
  - S1: pane `tmux-restarted` @ `111:1000`, payload `dev` @ `222:2000` → revived.
  - S1b (code reuse — the `$0` case): seed the pane **live**, no `terminated`, as `{ sessionCode:'old111', cachedName:'dev', tmuxInstance:'111:1000' }`; emit `[{ code:'old111', name:'dev', tmux_instance:'222:2000', mode:'terminal', ... }]`. The reconciler marks it `tmux-restarted` only because the code is present under a new generation (`reconcile.ts:88-101`; a missing code would be `session-closed` and never eligible). After the **same** event: `terminated` undefined, `tmuxInstance === '222:2000'`, `sessionCode === 'old111'`.
  - S2 / S3: `session-closed` / `host-removed` → untouched.
  - S6: `mode: 'stream'` pane → untouched.
  - S4 as a sequence: payload with `tmux_instance: ''` → untouched; a second payload with `'222:2000'` → revived.
  - S8 through the handler: op `done` + `createdSession` on the pane's dead binding (seed via `beginOperation` + `finishOperation`), lock free; emit matching payload → untouched.
  - S9 through the release trigger: `const grant = useRebuildStore.getState().acquireOperationLock('rebuild:pX')`; emit matching payload → untouched; `beginOperation` + `finishOperation(pX, { report })` with **no** `createdSession`; `releaseOperationLock(grant)` → revived **without** emitting another payload.
  - S7 + S11 with the real engine: tabs X, Y both `tmux-restarted` on `old111`@`111:1000`, name `dev`, records with an agent. Start `const run = rebuildPane('h1', 'tX', 'pX', plan, { createSession: async () => session({ code:'new1', name:'dev', tmux_instance:'222:2000' }), sendKeys: () => resume.promise })` where `resume = deferred()` (copy `deferred` from `engine.test.ts`). `await waitFor(() => expect(useRebuildStore.getState().lockedBy).not.toBeNull())`. Emit the payload `[new1]` (this is the daemon's create broadcast) → X and Y both still terminated. `resume.resolve(); await run` → X re-pointed **by the engine** (`report.repointed === true`, `sessionCode === 'new1'`); the lock release fires the subscription → Y revived onto `new1` with its record restamped. Also assert the pass did not touch X: X's `rebuild` reference is the one the engine wrote (capture it right after `await run`, run one more matching payload, `toBe`).
  - S12 with the real batch, two `it`s, each one group of source `p1` + member `p2` on `old111` (use the `content()`/`seedPane` fixture from `batch.test.ts`):
    (a) success — `runBatchRebuild({ createSession: ..., sendKeys: () => resume.promise })`; while pending emit the `[new1]` payload → both untouched; resolve → both on `new1`, `p2` via `repointMember` (`members[0].repointed === true`).
    (b) resume failed — `sendKeys: async () => { throw new Error('boom') }`; `await runBatchRebuild(...)` → `p1` still terminated with an op `done` + `createdSession` (report on screen), `p2` still terminated **during** the batch; the batch's own lock release then fires the pass → `p2` revived onto `new1`, `p1` untouched. Assert `p1`'s op entry still describes its dead binding (`usePaneOperation` semantics: `operations.p1.binding.sessionCode === 'old111'`).
  - S15: emit payload → assert revived; capture the pane content and `rebuild` references; emit the identical payload again → references unchanged (`toBe`). Value equality is not enough — the handler swallows exceptions, so "no throw" proves nothing.
  - S16 with the preconditions that make the guard observable: `acquireOperationLock`; emit matching payload → session store holds `new1`, `attachReady` true, pane still terminated; `sockets[0].onclose?.()` → `attachReady` false; `releaseOperationLock` → pane **still** terminated (this is the assertion the guard exists for); `await waitFor(() => expect(sockets).toHaveLength(2))`, `sockets[1].onopen?.()` → still terminated; `sockets[1].emit(payload)` → revived.
- [ ] **Step 2: Run the three files — red.**
- [ ] **Step 3: Implement** the rename, the pass, the wiring, the subscription.
- [ ] **Step 4: Run the three files green; then full `vitest run`, `lint`, `build`.**
- [ ] **Step 5: Commit** — `feat(rebuild): revive tmux-restarted panes by session name`

---

## Manual verification (after deploy, user's call)

On the two-client setup: kill the tmux server on mlab, Rebuild one tab on
client A, watch the same tab on client B come back on its own.

## Review items carried in (codex plan review `task-mu07mlre-w7q0zt`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | `isAttachReady` does not exist; the gate query is `canAttachTerminal` | Fixed — Task 2 + spec §3.2 snippet |
| 2 | Important | S1b fixture needs the old code present under a new generation, else it is `session-closed` | Fixed — S1b rewritten with `old111` |
| 3 | Important | S11 replaced by a failure case; S8/S9/S12/S4 sequences missing | Fixed — S4, S8, S9, S11 (real engine, deferred resume), S12 (real batch, success + resume-failed) added |
| 4 | Important | S16 lacked the preconditions that make the gate observable | Fixed — full sequence written out |
| 5 | Minor | S15 value equality / "no throw" proves nothing | Fixed — reference identity via `toBe` after an asserted first revive |
| 6 | Minor | `mode` guard looser than spec (`null` slipped through) | Fixed — `mode !== undefined && mode !== 'terminal'`, `null` test added; spec §3.1 wording aligned |
| 7 | Minor | Interface members untyped; session literals missing required fields | Fixed — typed interfaces, typed `session()` factory mandated |
