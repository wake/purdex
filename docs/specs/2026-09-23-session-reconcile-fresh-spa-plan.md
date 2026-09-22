# Re-reconcile sessions after a profile switch (#1255, SPA half) — plan

Spec: `docs/specs/2026-09-23-session-reconcile-fresh-spa-spec.md`. Daemon contract:
`docs/specs/2026-09-23-session-list-fresh-spec.md`. One PR (expected ≤ 12 files).
Off-limits (coordinator's P3d-4b): `lib/profile/{sync-status,executor,collector,apply-to-stores}.ts`,
`components/settings/profile/*`.

## Tasks (TDD — each: failing test first, then code, one commit)

**T1 `lib/host-events.ts` — `close()` retires the socket (spec §3.4).**
Test (`host-events.epoch.test.ts`, fake WebSocket like the existing epoch tests): a frame
delivered on a socket after `close()` does not reach `onEvent`; `onClose` is not called by
the socket's own close event after `close()`; `close()` while `getTicket()` is pending (lazy
connection) opens no socket when the ticket resolves; `close()` interleaved with
`reconnectWithTicket()` (before / during the ticket await) leaves nothing open and no
hand-over firing after close (codex #6). Existing supersede tests stay green.

**T2 `lib/rebuild/session-version.ts` (new) + test (spec §3.1).**
`parseVersion` (valid / wrong epoch length or case / seq 0, negative, float, > 2^53−1, string);
`decide` table: held null; same epoch newer / equal / older; different epoch via ws; different
epoch via fetch on the current conn / an older conn; a fetch captured BEFORE the
connection's `onOpen` that returns a different epoch after it → stale (codex #8); `connectionOpened/Closed` bump `conn`;
`note`; `forgetHost` (entry teardown) clears held + bumps conn; unversioned WS clears `held`.
Module state has a `__resetForTests`.

**T3 `lib/host-api.ts` — `listSessionsFresh(hostId)`.**
Returns `{kind: 'versioned', epoch, seq, sessions}` or `{kind: 'unversioned'}` (array body, or
an object failing `parseVersion`, or `sessions` not an array); throws on non-2xx like
`listSessions`. Test with a mocked `hostFetch` / fetch.

**T4 `hooks/useMultiHostEventWs.ts` + `HostEvent` type (spec §3.3).**
Tests (existing hook test harness; if none reaches the handler, extract the `sessions` branch
into `lib/rebuild/ws-sessions.ts` `handleSessionsFrame(hostId, event)` and test that):
versioned frame reconciles then notes; an older-seq frame after a newer one is NOT reconciled
(no `markTerminatedForGeneration`, session store unchanged) — with the gate open AND with it
closed (codex #2; the gate stays closed); reconcile throwing → `held` unchanged, gate not opened
(codex #4); an unversioned frame reconciles and clears `held`; after the `onClose` callback
returns, conn has moved and the gate is closed (codex #5); teardown bumps conn.

**T5 `lib/rebuild/refresh-after-switch.ts` (new) + call from `switchActiveProfile` (spec §3.2).**
World generation: pin the accessor by reading `switch-active.ts` / `world-fence.ts` (the value
raised on every exchange; must differ after a switch in ANY window). Call site: the `{ok:true}`
path of `switchActiveProfile`, after `withWorldLock` resolves (a `.then` on its promise), never
inside the locks.
Tests (unit, mocked `listSessionsFresh` + `reconcileHostSessions` spy + real stores):
- gate closed → no fetch;
- unversioned → no reconcile; fetch throws → no reconcile, other hosts still run;
- versioned newer → reconcile with the fetched list, then `note`; reconcile throws → `held` unchanged;
- gate + conn captured in one synchronous step (a test that closes the connection right after
  the refresh starts sees no fetch or a dropped result, never an applied one);
- world generation changes during the fetch → dropped;
- gate closes during the fetch → dropped; host removed / endpoint changed → dropped;
- conn bumped during the fetch with a different epoch → dropped; with the same epoch and newer seq → applied;
- a WS frame with a higher seq lands during the fetch → fetch result dropped (stale);
- two hosts: one fails, the other reconciles;
- `switchActiveProfile` refused (`busy` etc.) → no refresh; ok → one refresh per switch.
Integration (one test, real `reconcileHostSessions`): a slave world on screen with a pane on
session `S`, fetched list without `S` → pane `terminated: 'session-closed'`; a list that still
has `S` → no change (idempotent).

**T6 `lib/rebuild/revive.ts` — revive snapshots bound to the world (spec §3.5, codex #1).**
`noteReconciledSessions` records the world fence; `runRevivePass` returns early on a mismatch.
Tests: a `tmux-restarted` pane in a world switched onto the screen is NOT revived by the
switch's own lock release (drive the real `switchActiveProfile` + the hook's lock-release
subscription, or the subscription + a fence change); after the post-switch reconcile it IS
revived from the new list; a snapshot noted before a fence change is ignored by
`runRevivePassAll`; same fence → behaviour unchanged (existing revive tests green).

**T7 follow-up issues** (labels `refactor`/`bug` as fits, `spa`, milestone Backlog), linked
from the spec: (a) `runRevivePassAll` on lock release could fetch instead of using the held
list; (b) profile applies (`apply-to-stores` workspaces/tabs, wizard pull) put panes on screen
without post-apply session evidence.

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
