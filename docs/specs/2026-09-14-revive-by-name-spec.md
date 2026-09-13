# Spec — Revive terminated panes by tmux session name ("Revive by Name")

Status: v2.2 (v2 after codex spec review R1 `task-mu07biok-y3ko46`; v2.2 after
PR #1002 R2 reviews `review-mu08m766-qiy77j` / `review-mu08ofts-n32v2t` /
`review-mu08ojex-85uiy7` — evidence source and writer split changed, §8)
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

### 3.1 The decision: `decideRevive` (new module `spa/src/lib/rebuild/revive.ts`)

`reconcileSessionsPayload` is **not** changed. Review R1 finding 1: a pane
that this very payload marks `tmux-restarted` (pB was offline while the host
rebooted and pA rebuilt; pB reconnects and its first payload carries `foo`
under a new generation) is not terminated yet when the reconcile input is
collected. Collecting candidates before the terminate step would miss exactly
the client this feature exists for, and the list may never change again.

So the candidates are collected **after** `markTerminatedForGeneration` has
run, by rescanning the tab store, and decided by a separate pure function:

```ts
export interface ReviveCandidate {
  hostId: string
  tabId: string
  paneId: string
  /** The dead binding — what the pane must still hold when the write lands. */
  sessionCode: string
  tmuxInstance: string
  /** The name the pane last saw for its session. */
  cachedName: string
}

export interface ReviveDecision {
  tabId: string
  paneId: string
  binding: RebuildBinding           // { hostId, sessionCode, tmuxInstance }
  session: Session
}

export function decideRevive(hostId: string, sessions: Session[], candidates: ReviveCandidate[]): ReviveDecision[]
```

Rule, for each candidate on `hostId`:

- find the live session whose `name === cachedName`. tmux names are unique
  per server and one host is one server, so there is at most one; if a payload
  ever carried two, the first wins and nothing else is promised;
- that session's `tmux_instance` must be a **non-empty string** — `typeof
  === 'string' && length > 0`, not truthiness: the payload is `JSON.parse`d
  with no schema, and a number / boolean / object there is not a generation
  the pane may be re-bound to (R2 defender finding 2). Empty is unknown, and
  the whole feature stands on the Tab Rebuild rule "no evidence, no action"
  (spec §4.6). It need **not** differ from the pane's own generation
  (R1 finding 5): `snapshot/restore.ts:132` marks a pane `tmux-restarted` when
  its session lookup merely failed, so a same-generation live session is a
  legitimate recovery target, not a contradiction;
- if the payload carries a `mode` key at all (`mode !== undefined`, so `null`
  counts as present) it must be `'terminal'`;
- otherwise emit `{ tabId, paneId, binding, session }`.

The caller supplies exactly the eligible panes: `kind === 'tmux-session'`,
`mode === 'terminal'`, `terminated === 'tmux-restarted'`, on `hostId`. The
pure function never sees a reason.

### 3.2 The pass: `runRevivePass(hostId)`

One function, two triggers, always reading the same evidence:

```ts
export function noteReconciledSessions(hostId: string, sessions: Session[]): void  // handler → snapshot
export function runRevivePass(hostId: string): void {
  if (!canAttachTerminal(hostId)) return                    // no payload from this connection yet (attach-gate.ts:16)
  if (useRebuildStore.getState().lockedBy !== null) return  // a rebuild owns the outcome
  const sessions = reconciledSessions.get(hostId) ?? []     // the snapshot, never the session store
  for (const d of decideRevive(hostId, sessions, collectCandidates(hostId))) {
    if (!reviveAllowed(d.paneId, d.binding, useRebuildStore.getState().operations)) continue
    try { repointPane(d.tabId, d.paneId, d.session) } catch { /* one pane's persist failure must not stop the others, nor surface elsewhere — see Trigger 2 */ }
  }
}
```

**Evidence — the reconciled snapshot, not the session store** (R2 attacker
finding 1 / defender finding 1, v2.2). `useSessionStore.sessions[hostId]` is
NOT reconciled evidence: `fetchHost` (`useSessionStore.ts:23-27`) overwrites
it unconditionally whenever an HTTP list response lands, and the hook's
`onOpen` starts such a fetch on every connection. A response that left the
daemon *before* a later WS payload can land *after* it — during a held lock
— and the release pass would then act on a list older than the one the gate
was opened for. So the handler hands the pass its own evidence: right after
`replaceHost`, it calls `noteReconciledSessions(hostId, data)` and the pass
reads only that per-host snapshot. Nothing else writes it. The `attachReady`
guard still ties it to the *current* connection: the gate closes on every
(re)connect and reopens only after that connection's own payload has been
reconciled — which is also the moment the snapshot is overwritten — so a
pass can never act on a snapshot left over from a dropped connection.

A session the engine created is in the snapshot only once the daemon has
broadcast it (`watcher.go` does, on every change); if that broadcast lands
before the lock is released it is the release pass that acts, otherwise
Trigger 1 does on the broadcast itself. Either way the evidence is a daemon
payload, never a create response.

**The lock guard** (R1 finding 2). Any rebuild in flight — a single pane or
"Rebuild all" — holds the operation lock (`useRebuildStore.lockedBy`). While it
is held the pass does nothing at all, for every pane on every host:

- The engine's invariant #2 (the resume runs *before* the re-point, because
  clearing `terminated` unmounts the panel that reports the resume result)
  must hold for **every** pane the operation is going to re-point, and a batch
  records an operation entry only for each group's *source* pane
  (`batch.ts:183`). Its members, and the sources of groups still queued, have
  no entry to check. The lock is the one thing every one of them is under.
- pA's own create fires a `sessions` broadcast before its resume has finished.
  Under the lock that broadcast revives nothing; the operation re-points its
  own panes when it is done, and the release trigger (below) picks up any
  other pane the new session's name matches.

**The per-pane gate** `reviveAllowed(paneId, binding, operations)`:

```
const op = operations[paneId]
if (!op) return true
if (!bindingEquals(op.binding, binding)) return true   // op describes an earlier cycle of this pane
if (op.status === 'running') return false             // unreachable under the lock guard; kept so the rule stands alone
if (op.createdSession) return false
return true
```

- **done with `createdSession`** — the engine deliberately did **not**
  re-point (resume failed, or a generation refusal); the panel is showing the
  report with "Retry resume" / "Attach anyway". Reviving would silently do the
  attach the user was just asked to decide on. This pane stays as it is until
  its binding changes (retry, attach-anyway, the picker, or its next death).
- **done without `createdSession`** — nothing was created: a refusal that
  never started (lock held, host unknown, pane moved) *or* a create that ran
  and failed (R1 finding 3 — both land here, `engine.ts:506`). Either way
  nothing is pending on this pane and there is no panel state a revive would
  destroy that a failed create had not already made moot.
- **binding differs** — `usePaneOperation` already scopes reads this way.

**Trigger 1 — the `sessions` handler** (`useMultiHostEventWs.ts`), after
`markTerminatedForGeneration` and `openAttachGate(hostId)`, before the cwd and
provenance probes — so a revived pane is probed on its final binding, the same
reason the probes already run after reconciliation. The handler is
synchronous: between the candidate scan and the write nothing else runs, so
the binding a decision was made from is the binding the write lands on.

**Trigger 2 — lock release** (R1 finding 3). The hook subscribes to
`useRebuildStore` and, on a `lockedBy` transition from non-null to `null`,
runs the pass for every host. The subscription lives in the hook's effect so
it is set up once and torn down with it. This is what turns "skipped under the
lock" into "revived as soon as the lock is gone", with the same evidence and
the same gates.

The listener runs **synchronously inside `releaseOperationLock`'s `set`**,
i.e. inside the `finally` of the rebuild that is releasing. A throw there
(the persisted tab store's `localStorage.setItem` can throw on quota) would
turn a finished rebuild's promise into a rejection and stop the remaining
panes and hosts (R2 attacker finding 2). So: every pane write is isolated
(`try/catch` per pane in the pass) and `runRevivePassAll` isolates each
host. A failed revive leaves that pane terminated for the next trigger;
it never changes the outcome of the operation that released the lock.

**The writer — split in two** (R2 health finding 1, v2.2). `engine.ts`'s
`defaultRepoint` did two things: write the pane, then `syncSessionStore`,
which **evicts** the dead code from the session list when its generation
matches the dead binding. That eviction is right for a rebuild (the dead
session is gone) and wrong for a revive: §3.1 allows a same-generation
revive, and in that case the pane's old code can still be a live session
under another name — evicting it would make a real session vanish from the
picker until the next broadcast. So:

- `repointPane(tabId, paneId, session)` (exported from `engine.ts`) — the
  pane write only: new `(code, name, tmuxInstance)`, `terminated` dropped,
  `rebuild.sessionName` / `rebuild.tmuxInstance` restamped, the rest of the
  record kept; re-reads the pane and returns if it is no longer a terminal
  tmux pane. **This is what the revive pass calls.**
- `defaultRepoint` = `repointPane` + `syncSessionStore`, unchanged in
  behaviour, still the engine's step 4 and `repointMember`'s default.

The revive path touches the session store not at all: its evidence is the
snapshot, and the store was already replaced from the same payload.

### 3.3 Idempotency

A revived pane is a live pane. On the next payload it is reconciled by code
and generation like any other; it is not a candidate (not terminated) and is
never re-pointed twice. Replaying the same payload after a revive changes
nothing and does not rewrite the rebuild record.

### 3.4 What is deliberately not touched

- `reconcileSessionsPayload`, `markTerminatedForGeneration`,
  `adoptTmuxInstance`: unchanged inputs, unchanged outputs.
- `updateSessionCache`: its existing generation-scoped behaviour is kept as
  is; the pass runs after it.
- `repointMember` / `batch.ts`: under the lock guard no member can be revived
  by name while its batch runs, so the batch re-points its members itself
  exactly as today. No idempotency short-circuit is needed.
- `useAgentStore` / path cache: `tmux-restarted` never cleared them and a
  revive does not either, same as the engine's re-point.
- `TerminatedPane` / `RebuildActionSet`: no UI. A revived pane simply stops
  being terminated and `SessionPaneContent` mounts the terminal.

## 4. Scenarios

| # | Setup | Expected |
|---|---|---|
| S1 | pB: pane `foo` terminated `tmux-restarted`, gen `111:1000`. Payload: `foo` live, gen `222:2000` | pane re-bound to `foo`'s code / gen `222:2000`, `terminated` cleared, record `sessionName`/`tmuxInstance` restamped, `rebuild.agent` kept |
| S1b | pB was offline: pane `foo` **live** at gen `111:1000` when the payload with `foo` @ `222:2000` arrives | same payload marks it `tmux-restarted` **and** revives it |
| S1c | pane `tmux-restarted` at gen `111:1000` (restore.ts marked it); live `foo` @ `111:1000` | revived — same generation is not a refusal |
| S2 | pane is `session-closed` | untouched |
| S3 | pane is `host-removed` | untouched |
| S4 | live `foo` has `tmux_instance: ''` | untouched; the next payload that carries a generation revives it |
| S5 | live session is named `foo-2` only | untouched |
| S6 | pane is `mode: 'stream'` (caller filter) / live `mode: 'stream'` (decision) | untouched, both |
| S7 | pA: single Rebuild on X in flight (lock held); payload shows `foo` live | X untouched; every other candidate untouched too |
| S8 | pA: X op `done`, `createdSession` set, resume failed (panel showing retry); lock released | X untouched |
| S9 | pA: X op `done`, no `createdSession` (refused, or create failed); lock released | X revived on the release pass |
| S10 | pA: X op is for an older binding of X | X revived |
| S11 | pA: single Rebuild on X; tab Y bound to the same dead session | during the op: neither; on release: X already re-pointed by the engine, Y revived by the release pass |
| S12 | "Rebuild all" with members M1, M2 | during the batch: nothing revives; members re-pointed by `repointMember` as today; a group whose resume failed leaves its members dead until the release pass revives them by name (the source keeps its report) |
| S13 | payload for host h2 while the pane is on h1 | untouched |
| S14 | two panes terminated with the same `cachedName` | both revived onto the same session |
| S15 | same payload replayed after S1 | nothing changes |
| S16 | host's attach gate closed (reconnecting) when the lock is released | no pass for that host; its next payload runs Trigger 1 |
| S17 | lock held; WS payload `dev`→`new1` reconciled; then a **late HTTP** `fetchHost` response overwrites the session store with `dev`→`old`; lock released | pane revived onto `new1` (the snapshot), never `old` |
| S18 | S1c shape, and the pane's old code is still live under another name at the same generation | pane revived onto `dev`; the session list still contains the old code |
| S19 | two hosts h1/h2 each with a revivable pane; lock held while both payloads land; lock released | both revived |
| S20 | the tab store's persist throws on the first pane's write during a release pass | that pane stays terminated; the second pane still revives; the rebuild that released the lock still resolves with its own report |
| S21 | live `tmux_instance` is a number / boolean / object on the wire | untouched |

## 5. Testing

- `revive.test.ts` — `decideRevive`: S1, S1c, S4, S5, S6 (payload mode), S13,
  S14, S21, empty candidates, name match wins over a code match (the `$0` reuse
  case: a different live session carries the pane's old code). `reviveAllowed`:
  S8, S9, S10, no op, running. `runRevivePass`: S18 (session list keeps the
  old code), S20 (per-pane isolation), the snapshot is what is read (a session
  store seeded with a different list is ignored).
- `useMultiHostEventWs.revive.test.ts` — through `FakeSocket`, asserting on
  the tab store: S1, S1b, S2, S3, S6 (caller filter), S7 + S11 (lock held
  during the payload, then released → Y revives, X's binding untouched by the
  pass), S8 through the handler (the pane keeps its failed-op binding), S15,
  S16, S17 (real `fetchHost` resolving late with a stale list), S19, S20 (the
  rebuild's promise resolves normally).
- `engine.test.ts` — the exported `repointPane` keeps the record, drops `terminated` and does not touch the session store; the engine's step 4 still syncs it. (Task 2 shipped this as `repointPaneToSession`; v2.2 split it — the plan records the history.)

- All existing tests green and untouched.

## 6. Phases

Small enough for one PR; ordered so each step is independently green.

1. **Decision + gate** — `revive.ts` with `decideRevive`, `reviveAllowed`,
   unit tests.
2. **The pass and its two triggers** — `collectCandidates`, `runRevivePass`,
   export the writer, wire the handler and the lock subscription,
   hook tests.

## 7. Risks

- **Name reuse for an unrelated project.** Someone creates a brand-new session
  named `foo` after the reboot and pB's old `foo` tab attaches to it. Accepted:
  this is what "match by name" means, and the user stated they never run
  same-name sessions.
- **An unsaved draft in the rebuild panel** (R1 finding 6). `RebuildActionSet`'s
  `EditableValue` keeps a half-typed cwd / resume command in component state
  until Enter or blur. A revive that lands while the user is mid-edit unmounts
  the panel and the draft is gone. **Accepted for v1**: it needs the user on
  pB to be editing the exact pane pA is rebuilding at the same moment, and
  what they lose is a draft for a rebuild that the revive has just made
  unnecessary. Tracked as a follow-up issue (gate on an editing flag, or lift
  the draft into the store).
- **Members of a failed-resume group revive on release.** The source pane
  keeps the report and its retry; the members attach to a session whose agent
  did not come back and show a shell. That is the same outcome pB would have
  for those panes, and nothing is hidden — the report is still on screen.

## 8. Review dispositions

### Spec review R1 (`task-mu07biok-y3ko46`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | Panes terminated by the same payload are never candidates | **Fixed** — candidates rescanned after terminate (§3.1); S1b |
| 2 | Blocker | Batch members / queued sources have no operation entry; revived before resume | **Fixed** — whole pass skipped while the operation lock is held (§3.2); v1 §3.3 short-circuit dropped as unnecessary |
| 3 | Should-fix | No re-evaluation when the gate flips to allow; "done without createdSession" mis-described | **Fixed** — lock-release trigger with `attachReady` guard; text corrected |
| 4 | Should-fix | `repointMember` short-circuit hides real failures | **Moot** — v1 §3.3 removed with finding 2 |
| 5 | Should-fix | Same-generation revive is legitimate (restore.ts) | **Fixed** — "must differ" dropped; S1c |
| 6 | Should-fix | Unsaved `EditableValue` draft lost on revive | **Accepted risk**, §7; follow-up issue |
| 7 | Should-fix | Scenario/test coverage gaps | **Fixed** — §4/§5 extended (S1b, S1c, S6 both halves, S7+S11 sequence, S8 via handler, S15, S16) |
| 8 | Nit | `syncSessionStore` is not a no-op; `updateSessionCache` claim | **Fixed** — §3.2 / §3.4 reworded |

### PR #1002 R2 (attacker `review-mu08m766-qiy77j`, defender `review-mu08ofts-n32v2t`, health `review-mu08ojex-85uiy7`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| A | high (attacker 1, defender 1) | Release pass read `useSessionStore`, which a late `fetchHost` response overwrites unconditionally | **Fixed** — evidence is the handler's reconciled-payload snapshot (§3.2); S17 |
| B | medium (attacker 2) | Trigger 2 runs inside the releasing rebuild's `finally`; a persist throw becomes that rebuild's rejection | **Fixed** — per-pane and per-host isolation (§3.2); S20 |
| C | medium (health 1) | Shared writer's `syncSessionStore` evicts a still-live old code on a same-generation revive | **Fixed** — writer split: `repointPane` for revive, `defaultRepoint` = `repointPane` + sync for the engine (§3.2); S18 |
| D | medium (defender 2) | `tmux_instance` checked by truthiness; number/boolean/object accepted | **Fixed** — non-empty string (§3.1); S21 |
| E | note (defender) | No multi-host release test | **Fixed** — S19 |

