# Reconcile from a fresh list whenever the operation lock is released (#1309 + #1310) — plan

Spec: `docs/specs/2026-09-23-reconcile-on-lock-release-spec.md`. One PR (expected ≤ 10 files).
Coordinator's P3d-4c touches only `CurrentBlock.tsx`, `useSotProfiles.ts`, locales, PRODUCT.md.

## Tasks (TDD — failing test first, one commit each)

**T1 `lib/rebuild/refresh-after-switch.ts` → `lib/rebuild/refresh-sessions.ts` (git mv) + `reconcileAfterLockRelease()`.**
The file is no longer only about switches; rename it and its test, keep `refreshHost`,
`recoverHostSessions`, `cancelSessionRefresh`. New export `reconcileAfterLockRelease(): Promise<void>`
(spec §3.1): per host, versioned & live → `refreshHost(…, requireGate: true)` (gate + conn read in
one synchronous step, as `refreshAfterSwitch` does today); otherwise `runRevivePass(hostId)`
(caught per host). `refreshSessionsAfterSwitch` is removed (T3).
Tests:
- gate open + held versioned → one fetch, reconcile with the fetched list, revive pass runs from it
  (a `tmux-restarted` pane whose name is in the FETCHED list — not in the old snapshot — is revived);
- gate open + held null (unversioned daemon) → no fetch, `runRevivePass` called (today's behaviour);
- gate closed → no fetch, `runRevivePass` called (it is itself gated and returns);
- two hosts, one throws → the other still runs;
- two releases back-to-back → the first host refresh is replaced (one reconcile, from the second answer).

**T2 `hooks/useMultiHostEventWs.ts` — the lock-release subscription calls `reconcileAfterLockRelease()`**
instead of `runRevivePassAll()`. Update `useMultiHostEventWs.revive.test.ts`: the pre-switch-list
case still holds (world fence); add: after a release with a versioned host, the revive comes from
the fetched list, and a list-less / unversioned host keeps the old synchronous path.
`runRevivePassAll` stays exported only if something else uses it; otherwise delete it (and its
tests move to `runRevivePass`).

**T3 `lib/profile/switch-active.ts` — drop the explicit `refreshSessionsAfterSwitch()` call** (spec §3.2)
and update the header paragraph "WHAT A PARKED WORLD DOES NOT HEAR" (the refresh now comes from the
lock release). Test in `switch-active.test.ts` (the existing #1255 integration): a switch with the
real lock-release subscription wired produces exactly one fresh fetch per live host and still marks
the slave's dead pane `session-closed`.

**T4 #1310 integration** (`apply-to-stores.test.ts` or a new `lib/rebuild/refresh-sessions.apply.test.ts`,
whichever reaches the real `applySectionToStores` + the real lock-release subscription): master on
screen; apply a `tabs.<id>` payload holding a pane on session `S`; the fresh list (mocked
`listSessionsFresh`, versioned, newer) lacks `S` → after the release the pane is
`terminated: 'session-closed'`; a list that has `S` → no change; a `tmux-restarted` arriving pane
is revived from the fetched list, not from the pre-apply snapshot. Slave on screen (the apply writes
the parked master) → the refresh runs but changes nothing on screen.

**T5 follow-up issue** for spec §3.4 (a WS frame read before an apply, delivered after it).

## Gates
lint · `tsc --noEmit -p tsconfig.app.json` · full vitest (known load flakes: rerun the failing files
alone) · build. Mutations (no browser/dev server open): lock-release subscription back to
`runRevivePassAll` → T2/T4 red; `reconcileAfterLockRelease` ignores `heldVersion` (fetches for an
unversioned host) → T1 red; keep the explicit switch call → T3 red (two fetches).

## Real machine (:5176, this worktree)
Spec §4. Token into the page via the one-shot localhost server (file 600 in scratchpad, served only on
127.0.0.1, wait until it listens, `page.request` from `run-code`, return only the length; kill the
server and delete the file right after). Throwaway tmux sessions only. Not attached to any sync profile
unless the coordinator clears it. `playwright cli -s=apply-reconcile-fresh`, closed afterwards.
