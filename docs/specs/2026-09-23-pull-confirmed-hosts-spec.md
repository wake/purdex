# A wizard pull applies only the `hosts` the user confirmed (#1366) — spec + plan

Status: draft (2026-09-23) · Owner: mlab/purdex-3b · Coordinator: mlab/purdex-fb
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

### 2.3 The executor
New dep `confirmedPullHosts?: () => { rev, hash } | 'absent' | null`, read live like `initialDirection()`.
While the first reconciliation is open with direction `pull` AND a guard is present ("guarded pull"):
- **Every pull except `hosts` waits for `hosts`** (it joins `pullGatesOf` for `workspaces` too), and `hosts` itself
  may only reach the stores after the check below. Pushes are already held back under `pull` for tabs; nothing new
  there (a push under `pull` for `hosts` / `workspaces` / `settings` is what the first reconciliation already
  answers via forcePull).
- **The check** (in `pull('hosts')`, after the fetch, before `applySectionToStores`): the fetched row matches the
  guard when `hash` equals (rev may be higher — same content re-written is still what the user saw); `'absent'`
  matches only a 404 / no section. Match → apply as today; the guard has done its job (it stays stored until the
  period ends, harmless).
- **Mismatch** → nothing is applied; the executor reports problem `pull-hosts-unconfirmed` and calls
  `deps.onPullUnconfirmed?.()` once. No section state changes (no new lock kind).

### 2.4 The start layer
`onPullUnconfirmed` → the start layer **stops sync** exactly like the user's Stop sync (detach: attachment
removed, master cleared, executor disposed) and records a device-local notice
`useProfileStore.pullUnconfirmed = { hostId, profileId, at }` (not synced; cleared by the next attach or by
dismissing it). Nothing of this machine's world was replaced — every pull waited for `hosts` — so stopping is
safe, and the user starts the wizard again, which lists the new removal set.

### 2.5 UI
Settings › Profile Current block: when `pullUnconfirmed` is set, one sentence — "The hosts on the sync host
changed after you confirmed the pull, so nothing was pulled and sync was stopped. Set it up again to see what the
pull would remove now." (+ zh-TW) with a Dismiss and the existing "Set up sync" entry. A toast at the moment it
happens (the wizard may still be open; if it is, its run shows the attach step as done and the toast explains).

## 3. Not in scope
- Guarding sections other than `hosts` (the user confirmed only the host removals; replacing workspaces/tabs is
  what "pull" says).
- Push direction (it overwrites the SOT; nothing is removed locally).
- #1367 (daemon-side attachment fencing).

## 4. Tasks (TDD, one commit each)
- T1 store: `pendingPullHosts` (+ rehydrate guard) and `pullUnconfirmed`; `setMaster` signature; tests.
- T2 executor: dep, guarded-pull gating (all pulls wait for `hosts`), the check, mismatch → problem + callback
  once. Tests: match by hash (rev higher) applies; `'absent'` vs 404; mismatch applies NOTHING (workspaces pull
  that would have come first also waits — assert no `applySectionToStores` call for any section), callback once;
  no guard (null) → today's behaviour; push direction unaffected; guard ignored after the period ends.
- T3 start layer: `attachMaster(…, { confirmedHosts })` stores it with the direction; `onPullUnconfirmed` → detach +
  notice; integration test through the fake daemon (`executor.direction.integration.test.ts` style): B attaches
  pull with guard rev 7, the daemon holds rev 8 → no store change, attachment removed, notice set.
- T4 wizard: pass `confirmedHosts` from the last check (`recheckBeforeAttach`'s section read); test.
- T5 UI: Current block sentence + Dismiss; toast; locales.
- Gates: lint, tsc (`-p tsconfig.app.json`), full vitest, build. Mutations: no gate on workspaces (T2 red: a
  workspaces apply before the refused hosts); compare rev instead of hash (T2 red on rev-higher-same-hash); no
  detach on mismatch (T3 red); wizard passes nothing (T4 red).

## 5. Real machine
With my own profile name only (purdex-38 is running cross-device acceptance on mlab — never touch his profiles).
Two clients with independent host ids. A pushes; B opens the wizard (pull), confirms; before B presses Start —
or with a small delay injected in dev between recheck and attach — A pushes a `hosts` change; B: nothing
pulled, sync stopped, notice shown; re-run the wizard shows the new removal list.
