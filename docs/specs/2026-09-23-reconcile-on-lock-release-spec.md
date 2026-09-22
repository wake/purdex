# Reconcile from a fresh list whenever the operation lock is released (#1309 + #1310) — spec

Status: draft (2026-09-23) · Owner: mlab/purdex-3b · Coordinator: mlab/purdex-fb
Builds on #1255 (SPA half #1312, alpha.427): `lib/rebuild/{session-version,refresh-after-switch,ws-sessions}.ts`,
daemon contract `docs/specs/2026-09-23-session-list-fresh-spec.md`.

## 1. Problem

Two remaining ways panes come on screen — or are re-pointed — with no session evidence read
after the fact:

- **#1309** — when the operation lock is released, `runRevivePassAll` revives
  `tmux-restarted` panes by name from `reconciledSessions`: the last list reconciled for the
  current world, which can be behind the daemon (500 ms debounce; no push at all if nothing
  changed since). A revive re-points a pane (a master pushes that to the SOT).
- **#1310** — a profile apply (`applyTabsSection`, and the wizard's pull that ends in it)
  writes panes that arrived from the SOT into the world on screen. They are reconciled only by
  their host's next WS `sessions` frame — never, if nothing changes on the host. A pane bound
  to a session that is gone stays "live"; a `tmux-restarted` one is revived (by the apply's
  own lock release) from a list read before it arrived.

## 2. The observation

Every tree rewriter runs under the operation lock and releases it after its write: a switch
(`switchActiveProfile` → `underOperationLock`), a profile apply of `workspaces` / `tabs.*`
(`withOperationLock(PROFILE_SYNC_LOCK_OWNER, …)`), a rebuild / batch (`engine.ts`, `batch.ts`),
revive itself. The lock is in-memory per window and the release is already observed
(`useMultiHostEventWs`: `lockedBy` non-null → null → `runRevivePassAll`). A fetch sent from
that observer is sent **after** the write landed, so its list — read later still — is valid
evidence for the panes the write put on screen.

## 3. Design

### 3.1 One trigger: `reconcileAfterLockRelease()` (in `refresh-after-switch.ts`, renamed — see plan)

Replaces the `runRevivePassAll()` call in the lock-release subscription. For each host in
`hostOrder`, independently:

- **versioned & live** — the attach gate is open AND `heldVersion(hostId) !== null` (this
  window's live connection has reconciled a versioned list, i.e. a daemon that speaks the
  contract): `refreshHost(hostId, {world, conn, endpoint, requireGate: true})` — gate and conn
  read in one synchronous step as in #1255. The reconciliation it ends in runs the revive pass
  itself, now over a list read after the write.
- **otherwise** (gate closed, or an unversioned daemon): `runRevivePass(hostId)` — exactly
  today's lock-release behaviour for that host (still fenced by world, #1255 §3.5). A closed
  gate means the connection's own next frame reconciles; an old daemon gives nothing better.

One refresh per host still holds (`refreshHost` replaces the running one): two releases in a
row (apply #1, apply #2) leave only the second refresh, whose list is read after both writes.

**Lock fence (codex plan review #1).** A refresh started by release A can answer while the
next holder B is mid-write. `RefreshFences` gains `lockGen`: a counter bumped on every
acquire (observed in the same `useRebuildStore` subscription, `lockedBy` null → non-null).
Before each attempt AND before applying an answer: `lockedBy === null` and `lockGen` unchanged,
else the refresh ends without a retry — B's release starts the next one, read after B's write.

**The recovery refresh is lock-fenced too (codex adversarial on PR #1330).** A WS frame whose
reconciliation threw asks for a recovery refresh (`recoverHostSessions`: no gate required, held
to its connection). Unfenced, its GET could be read before holder B's write and applied while B
still holds the lock — a pane B just put on screen, absent from that list, would be marked
`session-closed` for good. So it carries the same `lockGen` fence. But the failed frame may have
been the one meant to open the gate, and a release with the gate closed only runs the revive
pass — the host would never be reconciled. So a recovery the lock stops is **owed**
(`needsRecovery`):

- marked when the recovery is asked for while the lock is held (nothing is fetched), when the
  lock is acquired while a recovery is in flight or waiting to retry, and when a recovery's
  attempt or answer is dropped by the lock fence;
- on release, a marked host gets the recovery again (`recoverHostSessions`: gate open or not,
  still fenced by world / endpoint / conn and the lock) instead of the two paths above — after
  today's revive pass when the host is not versioned & live; its barrier ends when it settles;
- cleared when a recovery starts for the host, when one ends by anything but the lock (applied,
  another fence moved, stale / unversioned, retries exhausted), and on entry teardown.

### 3.1.1 The barrier: no WS verdict on panes a write just put on screen (codex plan review #2)

A WS frame read by the daemon before a write landed but delivered after it would be reconciled
against the new panes (a session created just before the write, absent from that frame →
`session-closed`, irreversible — the later fresh list cannot undo it). The version orders lists,
not lists against local writes, so the SPA closes the window itself:

- A host enters **barrier** when the operation lock is acquired, if at that moment its gate is
  open and `heldVersion(hostId) !== null` (versioned & live).
- While in barrier, a versioned `sessions` frame is not reconciled; the handler keeps only the
  newest one (`stash`, by seq). Unversioned frames and frames on a closed gate are handled as today
  (no barrier: nothing better to wait for / the gate must open).
- The barrier ends when the release's refresh for that host settles:
  - it applied a list (held = F, read after the write) → the stashed frame goes through `decide`:
    `seq ≤ F` → dropped; newer → reconciled;
  - it ended without applying (fence moved, stale, failed after retries, cancelled, gate closed,
    or the host was not refreshable at release) → the stashed frame is reconciled as today
    (through `decide`), exactly what would have happened without the barrier.
- A new acquire while a barrier is up keeps it up (the next release's refresh ends it).
- Entry teardown clears the host's barrier and stash.
Cost: during a lock hold plus one fetch, WS reconciliation for versioned hosts is delayed —
not lost (the newest frame is kept).

### 3.1.2 Writes that do not go through the lock, and why they need nothing (codex #3, #4)

- Host removal / undo (`host-lifecycle.ts`): the undo puts the host back into `hostOrder`
  before it restores tabs; the hook creates a new connection for it, the gate starts closed, and
  that connection's own first frame reconciles every pane of the host.
- Session picker on a terminated pane, New Tab session choice: the binding is taken from the
  current session list (a live session the user just picked), and terminal attach waits on the
  gate — nothing arrives without evidence.
- Pane move (`pane-move.ts`): moves content already on screen, already reconciled.

### 3.2 The switch no longer calls the refresh itself

`switchActiveProfile` always runs under the operation lock, whose release now triggers §3.1
— with the world fence already raised (the fence moves before the block writes). The explicit
`refreshSessionsAfterSwitch()` call is removed so a switch costs one fetch per host, not two.
(A refused switch also releases the lock and triggers a refresh: one harmless fetch.)

### 3.3 What this does to the verdicts that get pushed

The reconciliation after an apply can mark an arriving pane `session-closed`, adopt an
instance, or revive it — and a master pushes those changes to the SOT. That is the point:
the evidence is a versioned list read after the pane landed, the same evidence a WS frame
gives for panes already on screen.

Two devices whose fresh reads straddle a tmux change can reach different verdicts (codex #5):
A reads without `S` and pushes `session-closed`; B read with `S` and keeps the pane live. B's
pane is unchanged from the base, so B's section is clean: B pulls A's push (and its own next WS
frame says the same). B pushes "live" only if it had its own edit to that section — a conflict
the user answers, the existing sync semantics. `session-closed` is irreversible on both sides,
so no pull → reconcile → push cycle can flip it back. No new mechanism.

### 3.4 Not in scope

- A WS frame delivered to a NON-versioned host (old daemon) or on a closed gate during a write:
  handled as today (§3.1.1).
- Other windows: they do not hold the lock. They receive the apply / switch result through
  the tab store's rehydrate; their own lock releases (their rebuilds) now refresh freshly.

## 4. Acceptance (real machine, :5176, mlab ≥ alpha.423, not attached to sync unless cleared)

- #1309: a `tmux-restarted` pane is revived after a lock release from a list fetched after the
  release (network shows one `fresh=1` per host per release).
- #1310: simulated with the dev hooks on a throwaway profile only if the coordinator clears
  attaching :5176; otherwise covered by the integration test (apply of a `tabs.*` section with a
  pane on a dead session → `session-closed` after the release's refresh).
- A switch makes exactly one `fresh=1` per live host.
