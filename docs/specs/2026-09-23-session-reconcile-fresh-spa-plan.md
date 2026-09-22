# Re-reconcile sessions after a profile switch (#1255, SPA half) — plan

Spec: `docs/specs/2026-09-23-session-reconcile-fresh-spa-spec.md`. Daemon contract:
`docs/specs/2026-09-23-session-list-fresh-spec.md`. One PR (expected ≤ 12 files).
Off-limits (coordinator's P3d-4b): `lib/profile/{sync-status,executor,collector,apply-to-stores}.ts`,
`components/settings/profile/*`.

## Tasks (TDD — each: failing test first, then code, one commit)

**T1 `lib/host-events.ts` — `close()` retires the socket (spec §3.4).**
Test (`host-events.epoch.test.ts`, fake WebSocket like the existing epoch tests): a frame
delivered on a socket after `close()` does not reach `onEvent`; `onClose` is not called by
the socket's own close event after `close()`. Existing supersede tests stay green.

**T2 `lib/rebuild/session-version.ts` (new) + test (spec §3.1).**
`parseVersion` (valid / wrong epoch length or case / seq 0, negative, float, > 2^53−1, string);
`decide` table: held null; same epoch newer / equal / older; different epoch via ws; different
epoch via fetch on the current conn / an older conn; `connectionOpened/Closed` bump `conn`;
`note`; `forgetHost` (entry teardown) clears held + bumps conn; unversioned WS clears `held`.
Module state has a `__resetForTests`.

**T3 `lib/host-api.ts` — `listSessionsFresh(hostId)`.**
Returns `{kind: 'versioned', epoch, seq, sessions}` or `{kind: 'unversioned'}` (array body, or
an object failing `parseVersion`, or `sessions` not an array); throws on non-2xx like
`listSessions`. Test with a mocked `hostFetch` / fetch.

**T4 `hooks/useMultiHostEventWs.ts` + `HostEvent` type (spec §3.3).**
Tests (existing hook test harness; if none reaches the handler, extract the `sessions` branch
into `lib/rebuild/ws-sessions.ts` `handleSessionsFrame(hostId, event)` and test that):
versioned frame applies + notes; an older-seq frame after a newer one is NOT reconciled
(no `markTerminatedForGeneration`, session store unchanged) while the gate is open; the same
older frame IS reconciled while the gate is closed; an unversioned frame reconciles and clears
`held`; `onOpen`/`onClose`/teardown call the conn hooks.

**T5 `lib/rebuild/refresh-after-switch.ts` (new) + call from `switchActiveProfile` (spec §3.2).**
World generation: pin the accessor by reading `switch-active.ts` / `world-fence.ts` (the value
raised on every exchange; must differ after a switch in ANY window). Call site: the `{ok:true}`
path of `switchActiveProfile`, after `withWorldLock` resolves (a `.then` on its promise), never
inside the locks.
Tests (unit, mocked `listSessionsFresh` + `reconcileHostSessions` spy + real stores):
- gate closed → no fetch;
- unversioned → no reconcile; fetch throws → no reconcile, other hosts still run;
- versioned newer → `note` + reconcile with the fetched list;
- world generation changes during the fetch → dropped;
- gate closes during the fetch → dropped; host removed / endpoint changed → dropped;
- conn bumped during the fetch with a different epoch → dropped; with the same epoch and newer seq → applied;
- a WS frame with a higher seq lands during the fetch → fetch result dropped (stale);
- two hosts: one fails, the other reconciles;
- `switchActiveProfile` refused (`busy` etc.) → no refresh; ok → one refresh per switch.
Integration (one test, real `reconcileHostSessions`): a slave world on screen with a pane on
session `S`, fetched list without `S` → pane `terminated: 'session-closed'`; a list that still
has `S` → no change (idempotent).

**T6 follow-up issue** for `runRevivePassAll` (spec §3.5), labels `refactor`, `spa`, milestone
Backlog; referenced from the spec.

Also: update the `switch-active.ts` header paragraph "WHAT A PARKED WORLD DOES NOT HEAR" to
describe the new behaviour (it currently explains why the call does not exist).

## Gates
lint · `tsc --noEmit -p tsconfig.app.json` · full vitest · build. Mutations (no browser/dev
server open): drop the `close()` epoch bump → T1 red; `decide` same-epoch `>` → `>=` → T2/T4 red;
remove the world-generation check → T5 red; remove the gate check in T5 → red.

## Real machine (:5176, this worktree)
Spec §5. mlab daemon is ≥ 423 (`fresh=1` live) — verify with one `curl` using the token
extracted into a variable (length printed only). Kill the session with `tmux kill-session` on a
throwaway session created for the test, never a user's. `playwright cli -s=session-reconcile-fresh`,
closed afterwards. Not attached to any sync profile.
