# A wizard pull applies only the `hosts` the user confirmed (#1366) — spec + plan

Status: draft rev 4 (2026-09-23, codex plan review folded in; rev 3: the barrier holds until `hosts` is applied; rev 4: codex PR R2 — the notice under its own key) · Owner: mlab/purdex-3b · Coordinator: mlab/purdex-fb
Follows #1362 (wizard: pull needs a verified host, lists the hosts it removes, re-checks before attach) and
#1365 (host sync identity wire). Files: `executor.ts`, `start.ts`, `useProfileStore.ts`, `wizard-run.ts`, the
Profile settings UI, locales.

## 1. Problem

Before a pull the wizard lists the local hosts the pull removes (from the SOT `hosts` section, `matchIncomingHosts`)
and re-checks right before `attachMaster` (`recheckBeforeAttach`). But `attachMaster` gets no idea of WHICH `hosts`
the user confirmed, and the executor's first reconciliation pulls whatever the SOT holds then. Between the last
re-check and the first pull, another device may push a `hosts` that removes more (#1366's scenario: B confirmed
"removes nas"; A pushes rev 8 that also drops `studio`; B's pull removes `studio` unannounced).

Worse than the issue says: `workspaces` has no pull gate, so during the first reconciliation it can be applied
BEFORE `hosts`. Any rule that refuses the `hosts` pull must therefore also hold back every other pull, or the
refusal lands on a half-pulled world.

## 2. Design

### 2.1 What the attach carries
`attachMaster(hostId, profileId, direction, opts?)` gains `opts.confirmedHosts?: { rev: number; hash: string } | 'absent'`
— the SOT `hosts` row the user saw (`'absent'` when the profile had no `hosts` section). Only meaningful with
`direction === 'pull'`; ignored (not stored) for push. The wizard passes it from the section it read in the
LAST check before the attach (`recheckBeforeAttach`), which is the one the removal list was computed from.

### 2.2 Where it lives
`useProfileStore.pendingPullHosts: { rev, hash } | 'absent' | null`, next to `pendingDirection` and with the same
life: set by `setMaster` together with the direction, cleared by `clearPendingDirection`, by detach, and by any
new `setMaster` (persisted and synced like `pendingDirection`, for the same reason — the first reconciliation may
continue in another window after a reload). Well-formedness on rehydrate: anything else → null.

### 2.3 The executor — a pre-step barrier (rev 2, codex plan review #1–#4)
Gating individual pulls is not enough: `restore-local` bypasses `mayPull` (sync-state.ts:390, executor.ts:886),
and a section that is absent on the SOT is PUSHED straight away by the decision table (sync-state.ts:405) — with
guard `'absent'` the local `hosts` itself would be pushed and `pull('hosts')` never run. So:

New dep `confirmedPullHosts?: () => { rev, hash } | 'absent' | null`, read live like `initialDirection()`.
When the executor starts (or first sees the guard) with direction `pull` AND a guard present, it raises a
**barrier**: `pump()` does nothing for ANY section but `hosts` — no pull, push, restore-local, delete, lock answer —
until the barrier is released. **The check and the apply of `hosts` are one step** (rev 3, coordinator): there is
no separate read that compares and then a pull that reads again, so no write can land between the two.
After every index (`checkConfirmedHosts()`, the index-level part):
- the index does NOT list `hosts` → the guard `'absent'` matches and there is nothing to apply: **released**. A row
  guard → **mismatch** (the index is the authority on liveness; a GET 404 alone is not, since it also means
  tombstone / unknown profile — codex #3). `profileGone` → the existing profile-gone handling, not a verdict.
- the index lists `hosts` → the guard `'absent'` is a **mismatch**; a row guard leaves the verdict to **the guarded
  pull**, the only action the barrier lets through: `hosts`' own lock (answered `sot` by the direction) and its
  `pull()`. That pull fetches `hosts` (same request options / endpoint pin as every read), compares the fetched
  row's `hash` with the guard's (rev may be higher: the same wire payload re-written is still what the user saw —
  the hash is of the canonical wire payload, hash.ts:101), and applies only on a match; a mismatch halts before
  anything is written. A 404 while listed → `pull-absent-but-listed`, re-index, never "absent".
- **released** only when `hosts` IS APPLIED — `applySectionToStores` succeeded and `pull-applied` was taken for the
  matching row — or, with nothing to apply, when `hosts` is up to date on the guard's hash (this device already
  held exactly that; the index folds the agreement; up to date on another hash → mismatch). Then everything
  proceeds as today (first reconciliation, forcePull etc.); the guard is not consulted again.
- a failed read, an apply that answers busy, is refused (`locked:invalid`) or throws → the ordinary backoff /
  lock handling of `pull()`, with the barrier still up: no other section moves.
- **mismatch** → the executor enters a terminal **halted** state at once (synchronously: the barrier never lifts,
  no action is started afterwards even if one was queued — codex #4), reports problem `pull-hosts-unconfirmed` and
  calls `deps.onPullUnconfirmed?.()` once.
No guard (null) or direction `push` → no barrier, today's behaviour. The guard is ignored once the first
reconciliation period has ended (an executor born without a direction never has one).

### 2.4 The start layer
`onPullUnconfirmed` → the start layer records the device-local notice `{ hostId, profileId, at }` FIRST (so it
survives whatever follows), then stops sync exactly like the user's Stop sync (`detachMaster`: master cleared, executor disposed, daemon DELETE of the
attachment). The DELETE is best effort as today: if it fails, the local detach still stands and the ghost
attachment is recorded in `pendingDetaches` (existing mechanism, codex #5); the notice is kept either way.
Nothing of this machine's world was replaced and nothing was pushed — the barrier held every action — so
stopping is safe; the wizard's promote / save happened BEFORE the attach and are the user's own choices (a saved
copy stays a local profile).

**Where the notice lives (rev 4, codex PR R2 #1)**: under its OWN localStorage key
(`STORAGE_KEYS.PROFILE_PULL_UNCONFIRMED`, `lib/profile/pull-unconfirmed.ts`) — NOT in `useProfileStore`. The window
that halts may hold a stale memory of the control plane (another window attached anew — new master, higher
`attachGeneration` — and this one has not rehydrated yet); a persisted zustand store writes its whole state on any
`set`, so a notice written through it would put the old master / generation / direction / guard back over that
attach. The key is device-local, not in the SOT, not registered with syncManager; every read / write is
try/catch'd. Cleared by Dismiss and by an attach that succeeds (`attachHeld`, right after `setMaster`).
Likewise the queued detach compares storage's `attachGeneration` with the generation the halted executor was BUILT
for (not one read at the halt, which may already be the other window's): a newer attach is left alone.

### 2.2a Pairing (codex #6)
`pendingPullHosts` and `pendingDirection` are one pair of the same attach generation: set together by `setMaster`,
cleared together by `clearPendingDirection` / detach / the next `setMaster`; rehydrate sanitises the pair (a guard
without direction `pull` → null). Tests: storage rehydrate; a leader handoff where another window continues the
first reconciliation with the guard; the other window calling `onInitialSettled` clears both at once.

### 2.5 UI
Settings › Profile Current block: when the notice is set, one sentence — "The hosts on the sync host
changed after you confirmed the pull, so nothing was pulled and sync was stopped. Set it up again to see what the
pull would remove now." (+ zh-TW) with a Dismiss and the existing "Set up sync" entry; it coexists with the
existing pending-detach notice. A toast at the moment it happens. The block reads the key through
`useSyncExternalStore`: this window's writes via the module's listeners, another window's via the native `storage`
event (listener attached only while the block is mounted).

### 2.6 Considered, not done (codex #8, #9)
- A conditional GET (`If-Match` / `baseRev`) on the daemon would make the check atomic and remove the index-vs-404
  reasoning, but still needs the barrier (other actions could run first) and turns #1366 into a Go/API change.
- A daemon/API contract test that a stored `hash` equals the payload's canonical hash: the daemon stores the hash
  the writer sends (PUT computes and verifies it client-side); recorded as a follow-up if wanted.

## 3. Not in scope
- Guarding sections other than `hosts` (the user confirmed only the host removals; replacing workspaces/tabs is
  what "pull" says).
- Push direction (it overwrites the SOT; nothing is removed locally).
- #1367 (daemon-side attachment fencing).

## 4. Tasks (TDD, one commit each)
- T1 store: `pendingPullHosts` (+ rehydrate guard); `setMaster` signature; tests. (rev 4: the notice moved out of
  the store to its own key — §2.4.)
- T2 executor: dep, the barrier, `checkConfirmedHosts`, halted state, problem + callback once. Tests: match by hash
  (rev higher) → released, proceeds; mismatch → NO `applySectionToStores`, NO `putSection`, NO `deleteSection`,
  NO restore for ANY section (including a section restored from the section store in `restoreLocal`, and sections
  absent on the SOT that would otherwise be pushed); guard `'absent'` + index without `hosts` → match; guard
  `'absent'` + index lists `hosts` → mismatch; index lists `hosts` + GET 404 → re-index, not absent; unknown
  profile → profile-gone path; an action queued before the verdict never starts after a mismatch; network failure
  → retried with the barrier up; no guard / push → today's behaviour. (rev 3) No other section's
  `applySectionToStores` before `hosts` is applied, `workspaces` included, while the `hosts` pull is out too; the
  `hosts` pull reads a non-matching row → halted, `workspaces` never applied; the `hosts` apply answers busy → the
  others still wait, the retry lands it and only then do they go; this device already holds the confirmed `hosts`
  → released on the agreement.
- T3 start layer: `attachMaster(…, { confirmedHosts })` stores it with the direction; `onPullUnconfirmed` → notice
  first, then detach; tests incl. the DELETE failing (notice kept, ghost in `pendingDetaches`) and the
  attach/detach queue busy at the moment of the mismatch (the executor is already halted); integration test through the fake daemon (`executor.direction.integration.test.ts` style): B attaches
  pull with guard rev 7, the daemon holds rev 8 → no store change, attachment removed, notice set.
- T4 wizard: `WizardPlan` gains the `hosts` row it was computed from (`{rev, hash} | 'absent'`); the attach step uses
  the plan RETURNED by `recheckBeforeAttach` (today it checks `again.ok` and discards `again.plan` — codex #7) and
  passes its `confirmedHosts`. Test: first prepare and last recheck return different markers → the attach gets the
  latter.
- T5 UI: Current block sentence + Dismiss; toast; locales.
- Gates: lint, tsc (`-p tsconfig.app.json`), full vitest, build. Mutations: barrier lets `restore-local` through (T2 red); barrier lets
  a push through (T2 red); compare rev instead of hash (T2 red on rev-higher-same-hash); no
  detach on mismatch (T3 red); wizard passes nothing (T4 red); (rev 3) release as soon as the fetched `hosts`
  matches, before it is applied (T2 red on the in-flight and busy tests).

## 5. Real machine
With my own profile name only (purdex-38 is running cross-device acceptance on mlab — never touch his profiles).
Two clients with independent host ids. A pushes; B opens the wizard (pull), confirms; before B presses Start —
or with a small delay injected in dev between recheck and attach — A pushes a `hosts` change; B: nothing
pulled, sync stopped, notice shown; re-run the wizard shows the new removal list.
