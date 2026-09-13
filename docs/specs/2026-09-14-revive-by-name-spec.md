# Spec — Revive terminated panes by tmux session name ("Revive by Name")

Status: draft v1
Date: 2026-09-14
Branch: `worktree-revive-by-name`
Scope: SPA only. No daemon change.

## 1. Problem

Two Purdex clients (call them pA and pB) attach to the same daemon. The host
reboots, the tmux server is replaced, and the tab bound to session `foo` shows
"tmux restarted" on **both** clients. The user presses Rebuild on pA. pA's pane
is re-pointed onto the recreated `foo` (`spa/src/lib/rebuild/engine.ts`,
step 4). pB's pane stays dead forever, even though a live session with exactly
the name it was bound to is now in every `sessions` payload pB receives.

Why: a pane's binding is `(hostId, sessionCode, tmuxInstance)` and
`reconcileSessionsPayload` (`spa/src/lib/rebuild/reconcile.ts`) is only ever
handed the **non-terminated** panes (`useMultiHostEventWs.ts:157`). Once a pane
is terminated nothing looks at it again until the user acts on it.

The same gap exists inside one client: a single-pane Rebuild re-points only the
pane the button was pressed on; another tab bound to the same dead session is
left dead.

## 2. Goals / non-goals

### Goals

1. A pane terminated with reason `tmux-restarted` re-attaches automatically
   when a live session on the same host carries the name the pane last saw.
2. Zero user action on the client that did not press Rebuild.
3. No interference with a rebuild operation in progress on the same client.

### Non-goals

- `session-closed` panes. The user closed that session (or it exited on its
  own); a later session that happens to reuse the name must not resurrect the
  tab. **Product decision, 2026-09-14.**
- `host-removed` panes. The host is gone; there is no live list to match.
- Stream-mode panes. Out of scope like every other rebuild feature.
- A manual "check now" button. The daemon broadcasts `sessions` on every
  change (`internal/module/session/watcher.go`) and a reconnect delivers the
  full list, so the check already runs at every moment it could succeed.
- A settings toggle. YAGNI.
- Matching a collision-suffixed name. If pA's rebuild had to create `foo-2`,
  pB's `foo` pane does not match; the session picker remains the escape hatch.
- Daemon-side boot restore (Layer 2). Separate spec.

## 3. Design

### 3.1 The decision: `revive` in `reconcileSessionsPayload`

`ReconcileInput` gains a second pane list:

```ts
export interface RevivablePane {
  hostId: string
  tabId: string
  paneId: string
  /** The pane's dead binding — what it must still hold when the decision is applied. */
  sessionCode: string
  tmuxInstance: string
  /** The name the pane last saw for its session. */
  cachedName: string
}

export interface ReconcileInput {
  hostId: string
  sessions: ReconcileSession[]
  panes: ReconcilePane[]          // unchanged: live panes
  revivable?: RevivablePane[]     // NEW: panes with terminated === 'tmux-restarted'
}

export interface ReviveDecision {
  tabId: string
  paneId: string
  binding: { hostId: string; sessionCode: string; tmuxInstance: string }
  session: ReconcileSession
}

export interface ReconcileOutcome {
  terminate: TerminateDecision[]
  adoptInstance: AdoptDecision[]
  revive: ReviveDecision[]        // NEW
}
```

Rule, for each `revivable` pane on this host:

- find the live session whose `name === pane.cachedName`. tmux names are
  unique per server and one host is one server, so there is at most one;
- that session's `tmux_instance` must be **non-empty and different from
  `pane.tmuxInstance`**. Empty is unknown, and the whole feature stands on the
  Tab Rebuild rule "no evidence, no action" (spec §4.6): a pane is re-pointed
  only onto a generation the daemon has proven is a new one. Equal cannot
  happen for a `tmux-restarted` pane and is refused rather than reasoned about;
- if `mode` is present on the payload it must be `'terminal'`;
- otherwise emit `{ tabId, paneId, binding, session }`.

`revivable` is optional and defaults to `[]`, so every existing caller and test
is unchanged.

The caller filters the input to `terminated === 'tmux-restarted'` and
`mode === 'terminal'` panes. The pure function does not see the reason — it is
handed exactly the panes that are eligible, the same way it is handed exactly
the live panes today.

### 3.2 Applying it: `useMultiHostEventWs.ts`

In the `sessions` handler, after `markTerminatedForGeneration` and before
`openAttachGate` / the cwd and provenance probes (so a revived pane is probed
on its final binding, mirroring the ordering comment already there):

```ts
for (const d of outcome.revive) {
  if (!reviveAllowed(d.paneId, d.binding)) continue
  repointPaneToSession(d.tabId, d.paneId, d.session)
}
```

`reviveAllowed` (in `spa/src/lib/rebuild/revive.ts`, pure over the rebuild
store state, so it is testable without React):

```
const op = useRebuildStore.getState().operations[paneId]
if (!op) return true
if (!bindingEquals(op.binding, binding)) return true   // op describes an earlier cycle
if (op.status === 'running') return false
if (op.createdSession) return false
return true
```

Why each line:

- **running** — the engine's invariant #2: the resume runs *before* the
  re-point precisely because clearing `terminated` unmounts the panel that
  reports the resume result. pA's own create fires a `sessions` broadcast
  before its resume has finished; reviving on that broadcast would swap the
  panel out from under the operation.
- **done with `createdSession`** — the engine deliberately did **not**
  re-point (resume failed, or a generation refusal); the panel is showing the
  report with "Retry resume" / "Attach anyway". Reviving would silently do the
  attach the user was just asked to decide on.
- **done without `createdSession`** — a refusal that never started (lock
  held, host unknown). Nothing was created, nothing is pending; there is no
  reason to hold this pane hostage until its next death.
- **binding differs** — the operation is about a previous cycle of this pane
  (`usePaneOperation` already scopes reads this way).

`repointPaneToSession` is `engine.ts`'s existing `defaultRepoint`, exported
under that name. It already: writes the new `(code, name, tmuxInstance)`,
drops `terminated`, restamps `rebuild.sessionName` / `rebuild.tmuxInstance`,
keeps the rest of the record, and syncs the session store generation-scoped.
In the revive path the session store was just replaced from the same payload,
so the sync is a no-op by construction; the function is reused rather than
copied so the two re-point paths cannot drift.

The engine is still a re-entrancy hazard in one direction: between the
decision (read from the tab store) and the write, nothing else runs — the
handler is synchronous — so the binding the decision was made from is the
binding the write lands on. `repointPaneToSession` re-reads the pane and
returns if it is no longer a terminal tmux pane.

### 3.3 Batch members: idempotent `repointMember`

"Rebuild all" (`spa/src/lib/rebuild/batch.ts`) groups panes by dead binding and
re-points the non-source members after the group's create + resume. Those
members hold no operation entry, so with 3.2 they are revived by name the
moment the create broadcasts — and `repointMember` then finds "the pane binding
changed" and reports the member as not re-pointed, although it is alive on
exactly the session the group created.

Fix: `repointMember` returns `{ repointed: true }` when the pane is already
bound to `created` (`sessionCode === created.code` and
`tmuxInstance === created.tmux_instance`). Re-pointing onto the session you are
already on is a success, whoever did it first. The "binding changed" reason is
kept for every other destination.

### 3.4 What is deliberately not touched

- `markTerminatedForGeneration`, `adoptTmuxInstance`, `updateSessionCache`:
  their inputs are unchanged (still the live panes).
- `useAgentStore` / path cache: `tmux-restarted` never cleared them and a
  revive does not either. Same as the engine's re-point today.
- `TerminatedPane`: nothing to render. A revived pane simply stops being
  terminated and `SessionPaneContent` mounts the terminal.

## 4. Scenarios

| # | Setup | Expected |
|---|---|---|
| S1 | pB: pane `foo` terminated `tmux-restarted`, gen `111:1000`. Payload: `foo` live, gen `222:2000` | pane re-bound to `foo`'s code / gen `222:2000`, `terminated` cleared, record `sessionName`/`tmuxInstance` restamped |
| S2 | same, but pane is `session-closed` | untouched (never handed to the decision) |
| S3 | same, but pane is `host-removed` | untouched |
| S4 | same, live `foo` has `tmux_instance: ''` | untouched; the next payload that carries a generation revives it |
| S5 | same, live session is named `foo-2` | untouched |
| S6 | same, pane is `mode: 'stream'` | untouched |
| S7 | pA: pane X has op `running` on its dead binding; payload shows `foo` live | X untouched; engine step 4 re-points it later |
| S8 | pA: pane X op `done`, `createdSession` set, resume failed (panel showing retry) | X untouched |
| S9 | pA: pane X op `done`, no `createdSession` (lock-refused) | X revived |
| S10 | pA: pane X op is for an older binding of X | X revived |
| S11 | pA: single Rebuild on X; tab Y bound to the same dead session | Y revived by the create's broadcast; X by the engine |
| S12 | "Rebuild all": group with members M1, M2 | M1/M2 revived by the create's broadcast; `repointMember` reports them `repointed: true` |
| S13 | payload for host h2 while the pane is on h1 | untouched (hostId mismatch) |
| S14 | two panes terminated with the same `cachedName` | both revived onto the same session |

## 5. Testing

- `reconcile.ts`: S1, S4, S5, S6 (mode on payload), S13, S14, plus "no
  `revivable` → `revive: []`" and "reviving does not alter `terminate` /
  `adoptInstance`". Existing generation tests must stay green untouched.
- `revive.ts` (`reviveAllowed`): S7–S10 as table cases over a seeded
  `useRebuildStore`.
- `useMultiHostEventWs.revive.test.ts`: S1, S2, S3 and S7 end to end through
  `FakeSocket` (same harness as `gate.test.ts`), asserting on the tab store.
- `engine.test.ts`: `repointMember` already-on-created → `repointed: true`;
  bound elsewhere → unchanged reason.
- `batch.test.ts`: S12 with a `sessions` broadcast simulated between create and
  member re-point.

## 6. Phases

Small enough for one PR; ordered so each step is independently green.

1. **Decision** — `RevivablePane` / `ReviveDecision`, the rule in
   `reconcileSessionsPayload`, tests.
2. **Gate + re-point** — `revive.ts` with `reviveAllowed`, export
   `repointPaneToSession`, wire the handler, hook tests.
3. **Batch idempotency** — `repointMember` short-circuit, engine + batch tests.

## 7. Risks

- **Name reuse for an unrelated project.** Someone creates a brand-new session
  named `foo` after the reboot and pB's old `foo` tab attaches to it. Accepted:
  this is what "match by name" means, and the user stated they never run
  same-name sessions.
- **A later broadcast reviving a pane the user is looking at.** Only
  `tmux-restarted` panes are eligible and they revive on the first qualifying
  broadcast, so the panel the user sees is one that has had no live match yet.
  The engine gates (3.2) cover the one panel that carries state.
