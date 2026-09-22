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

### 3.2 The switch no longer calls the refresh itself

`switchActiveProfile` always runs under the operation lock, whose release now triggers §3.1
— with the world fence already raised (the fence moves before the block writes). The explicit
`refreshSessionsAfterSwitch()` call is removed so a switch costs one fetch per host, not two.
(A refused switch also releases the lock and triggers a refresh: one harmless fetch.)

### 3.3 What this does to the verdicts that get pushed

The reconciliation after an apply can mark an arriving pane `session-closed`, adopt an
instance, or revive it — and a master pushes those changes to the SOT. That is the point:
the evidence is a versioned list read after the pane landed, the same evidence a WS frame
gives for panes already on screen. Two devices that both reconcile the same pane compute
the same result from the same host; identical content hashes identically.

### 3.4 Not in scope (pre-existing, recorded)

- A WS frame read by the daemon BEFORE an apply landed, delivered after it, is reconciled
  against the newly arrived panes (a session created just before the apply may be absent
  from it → `session-closed`). The version cannot tell "read before the apply": it orders
  lists, not lists against local writes. The post-release refresh is read later and wins
  on `seq`, but a verdict already made is not undone (`session-closed` is irreversible).
  Follow-up issue: e.g. hold `session-closed` for panes younger than the held seq.
- Other windows: they do not hold the lock. They receive the apply / switch result through
  the tab store's rehydrate; their own lock releases (their rebuilds) now refresh freshly.

## 4. Acceptance (real machine, :5176, mlab ≥ alpha.423, not attached to sync unless cleared)

- #1309: a `tmux-restarted` pane is revived after a lock release from a list fetched after the
  release (network shows one `fresh=1` per host per release).
- #1310: simulated with the dev hooks on a throwaway profile only if the coordinator clears
  attaching :5176; otherwise covered by the integration test (apply of a `tabs.*` section with a
  pane on a dead session → `session-closed` after the release's refresh).
- A switch makes exactly one `fresh=1` per live host.
