# Plan — host ownership H1 (H1a / H1b / H1c)

Spec: `docs/specs/2026-09-23-host-ownership-spec.md` (§3, decision 9). Measured on the worktree at `efc55f0c`
(alpha.439 + spec commits). Three PRs, merged in order H1a → H1b → H1c; each task is TDD (failing test first) and its
own commit. File lists below are the files each PR touches, counted from the code — not estimates.

Test / lint / build: `cd spa && npx vitest run <files>`, `pnpm run lint`, `pnpm run build`, and
`npx tsc --noEmit -p tsconfig.app.json` (a bare `tsc --noEmit` checks nothing in `spa/`).

## 0. Where the spec and the code disagree (found while measuring)

Each item says what the plan does. §0.1, §0.5 and §0.8 were marked NEEDS DECISION; the coordinator decided all three
as proposed (2026-09-24) — they are now **DECIDED**.

1. **Which lock (spec §3.3, §3.4 say "world lock").** `withWorldLock` (`lib/storage/world-lock.ts`) is the
   cross-renderer lock for profile switch / promote, and its body must be synchronous. The code that rewrites the tab
   tree outside a switch takes the in-process **operation lock** instead: the `tabs.*` apply and the hosts apply's
   cascade (`withOperationLock`, `stores/useRebuildStore.ts`); the settings apply takes neither and re-reads
   `resolverSignature` after each await; the UI host deletion (`deleteHostWithUndoToast`) takes no lock at all.
   **DECIDED** — the re-resolve pass is one synchronous body that acquires the operation lock
   (owner `host-reresolve`, `acquireOperationLock` is synchronous) and retries on refusal (§H1b T3). Neither the pass
   nor a deletion takes the world lock.
   **Amended (PR #1413 review)** — the host deletion rewrites the tab tree, so it runs under the operation lock on
   every path; `deleteHostCascade` itself takes none, its callers hold it:
   - the Hosts page (`deleteHostWithUndoToast`) acquires it (owner `host-delete`) for the whole cascade. Held
     elsewhere (e.g. a batch rebuild): retried every 250 ms, for up to ~4 s from the click; still held → nothing is
     deleted and a toast says so (`hosts.delete_busy`). The target is fixed at the click — endpoint identity (ip, port,
     token) and daemonId — and watched until the lock is taken: the row gone, gone and back under the same id, or
     re-pointed meanwhile → nothing is deleted, `hosts.delete_stale` is said (never "deleted" with an Undo); a
     rename is not another host. With the lock free the deletion is synchronous, as before;
   - the hosts apply keeps its own grant (`withOperationLock`, owner `profile-sync`) and passes it to the cascade,
     whose undo re-resolves under it during a rollback.
   The cascade is one unit: any step failing puts every store it wrote back and rethrows (a failing put-back is
   `HostDeleteRollbackIncompleteError`; the Hosts page then shows a persistent notice to reload and check the host
   settings, any other failure a plain one).
2. **The pass cannot rely on host-identity changes alone (spec §3.3 trigger).** The settings apply rolls back by
   `setState(old)` when its scope or the hosts move mid-apply (`applySettingsSection`, `HOSTS_MOVED`), and `old` may hold
   wire ids the pass had already resolved — and when a store write or the rollback itself fails the apply THROWS
   (`apply-to-stores.ts` rollback path, `unfinished`) instead of returning an outcome. The plan adds a trigger at the
   SETTLEMENT boundary: `applySectionToStores` requests a pass in a `finally`, so a resolved outcome, `busy`,
   `invalid` and a throw all request one (the pass is idempotent and cheap). Spec gap, not a contradiction.
3. **New Tab placement uses `knownIds`, not the layout (spec §3.3 "layout holds either id").** `ensureDefaults`
   (`stores/useNewTabLayoutStore.ts`) places every provider whose id is not in `knownIds`. The plan's check covers
   `knownIds` AND every preset column, and the pass also rewrites `knownIds` (the spec's scope list omits it).
4. **An unknown-host tmux pane would attach to the WRONG host today if unmarked.** `SessionPaneContent` takes
   `getWsBase(hostId)`, which falls back to the active / first host for an unknown id (`useHostStore.getWsBase`).
   The `host-removed` mark is what prevents that now; H1a must add the guard in the same commit that stops marking.
5. **Pane kinds other than tmux (spec §3.2 table "pane renders 'this device has no host ‹name›'").** An execution
   pane already shows the local-only `problem: 'host_removed'` (`useExecutionSubscription`) — H1a only changes its copy.
   Editor / image / pdf panes with a daemon source already REFUSE an unknown host (`fs-backends.tsx` resolver returns
   `null`) and show the editor's generic "no backend" error. **DECIDED** — leave file panes on
   that generic error (safe, visible; the named state would add the editor pane + two viewers + tests, 5–6 files).
6. **The undo cannot rely on the ASYNC pass (spec §3.4 "the §3.3 pass rewrites every wireId reference back").** The
   hosts apply uses the cascade's undo to ROLL BACK a failed apply and requires that nothing is awaited between the
   staging write and the end of the rollback (`applyHostsSection` comment on `runtime[H]`). The pass is asynchronous
   (lock, retry). Plan: the undo re-adds the host and then runs the pass's synchronous BODY at once (under the hosts
   apply's grant in that path; in the UI path it acquires the operation lock synchronously, and only if refused falls
   back to a scheduled pass). No per-reference record is kept, and no owner / relabel bookkeeping is needed:
   - a `d1_…` id names one daemon on every device, and `makeWireResolver` maps a sync id to its local host whatever
     world or origin the reference has (`host-identity.ts` resolver, `isSyncId(wireId) → identity.toLocal`). So every
     `d1_X` reference in every world — including one that arrived inside the undo window, and the same tab / pane ids
     in two worlds — correctly becomes X's local id; that is exactly what the pass does for any `d1_X` anyway.
     Restricting the undo to "only the fields this deletion changed" would leave such a reference on `d1_X` until the
     next pass, i.e. the same end state later;
   - a no-daemonId host: the deletion rewrote nothing (wire id = local id), the undo has nothing to rewrite;
   - the re-added host conflicts again (a duplicate deleted under §0.7): `wireResolverOf` → `null`, nothing is
     rewritten; its references already resolved to the survivor through the pass while the conflict was cleared.
   The existing `relabelCount` / `locateWorld` machinery is only needed for restoring closed tabs and marks, which no
   longer exist — it goes with them.
7. **Which wire id a deletion writes under an identity conflict (spec §3.4 `identity.toWire(localId) ?? localId`).**
   Under a conflict `toWire` omits both conflicting hosts, so the deleted duplicate's references would stay its local
   id — unresolvable, although they name the same daemon as the survivor. The plan uses
   `isValidDaemonId(host.daemonId) ? syncIdOfSync(host.daemonId) : localId` (identical to `toWire` when there is no
   conflict), so once the conflict clears the pass maps them to the survivor.
8. **Lease on deletion (spec §3.4 "a held lease is released best-effort as today").** "Today" differs by mode: closeTabs
   releases, keep-tabs drops the local lease WITHOUT a release call (comment in `deleteHostCascade`: "the host — and
   its auth — is gone"). Under decision 9 no tab closes. **DECIDED** — release best-effort, so other devices are not
   blocked until the lease expires. **Amended (PR #1413 review)** — the release is a daemon side effect no rollback can
   take back, so it is sent only once the deletion has COMMITTED: its endpoint and auth are pinned while the row is
   still there (`pinnedLeaseRelease`); the Hosts page sends it when the cascade has committed, the hosts apply after
   its whole apply committed (a rollback drops it, so a restored lease is still held at the daemon).
9. **H1 real-device acceptance cannot use independent host lists (spec §8 H1).** Until H3 the `hosts` section still
   syncs, so a client that lacks host X gets it from the next `hosts` pull, and a client that deletes X pushes the
   removal to the other client (spec §3.4 last bullet already says so). "A and B with independent host lists" and
   "B deletes X → A notices nothing" are therefore not reproducible before H3. The acceptance below makes B lack X by
   INJECTING a `tabs.*` / `settings` payload that names a daemon neither client has, and states the pre-H3 outcome of
   a deletion honestly (A's list loses X too, A's tabs are untouched). The independent-lists scenario moves to H3's
   acceptance.
10. **Hostless execution panes** — spec (efc55f0c) settled: no special case. Verified: `resolveExecutionHostId('')`
    falls back to `hostOrder[0]`, and `SubscriptionProblem` has `'not_found'`, so the known behaviour holds.
11. **A reference present in BOTH forms (local and wire) collides on rewrite.** Stores can hold `sessions:<local>` and
    `sessions:d1_X` in one preset, or `purdex-host-settings` keys `<local>` and `d1_X` (persisted or synced inputs).
    The builds already treat this degenerate state asymmetrically: `hostSettingsToWire` → `rekeyEntries`
    (`host-identity.ts`) keeps the SYNC-id entry, so the payload holds only `d1_X`'s value; `presetColumnsToWire` maps
    column by column, so the payload holds `sessions:d1_X` TWICE. Rule, both directions (pass and deletion / undo):
    - host-settings: the sync-id entry wins (the `rekeyEntries` rule) → the payload is unchanged, the invariant holds;
      the local entry's value is lost here, and undo gives back `d1_X`'s value (what every other device already had);
    - New Tab columns and `knownIds`: **amended during H1b (real-device acceptance, scenario 2)** — where two ids of
      a preset (or of `knownIds`) become one, the WIRE-form one (`…:d1_…`, what the SOT already has) wins its place,
      whichever comes first — the host-settings rule; within such a group, duplicates of one form keep the first. Only
      two DIFFERENT ids becoming one collide: an id repeated as it is (a wire id twice, an unknown id twice) is left
      as it is, by the pass and the build alike, as older builds sent it. The settings BUILD
      applies the same rule after local → wire (`mapColumnsKeepingOne`, shared with the pass's rename), so the payload
      equals the SOT's before, during and after the pass in every arrangement — no push, no exception to the no-push
      invariant; undo gives back a single column. Known and
      absorbed by that build rule, not changed: the add-host dialog writes a host before its daemonId is known, so the
      New Tab bootstrap places `sessions:<local>` / `headless:<local>` next to a received `…:d1_X` until the pass
      (which may be held off by the operation lock) renames it.
12. **`layoutFromWire` never returns the same object.** `mapContent` spreads a new content for every host-bearing
    pane and `mapLayout` rebuilds every split (`host-identity.ts` `mapContent` / `mapLayout`), even when the mapped
    id is unchanged — so "unchanged → same object" (needed to keep the pass a no-op for untouched worlds and stores)
    cannot be built on it as is. H1b changes both helpers to return the input object when the mapped id is equal,
    and splits when no child changed (behaviour-neutral for the builders; `host-identity.ts` joins H1b's file list).
13. **"Every section" in the no-push invariants.** Spec §3.3 says every section payload is unchanged by the pass;
    spec §3.4 says every section hash is unchanged by a deletion, while its last bullet says a pre-H3 deletion
    changes `hosts`. Tests therefore compare ALL sections the collector builds (`hosts`, `workspaces`, `settings`,
    every `tabs.*`): for the pass, all of them identical (host already added before the "before" snapshot); for a
    deletion and its undo, all non-`hosts` sections identical and `hosts` changed — the pre-H3 exception, removed when
    H3 retires `hosts`. The acceptance checks the same split.

## H1a — tolerate unresolvable references (17 files)

Files:
1. `spa/src/lib/profile/apply-to-stores.ts`
2. `spa/src/lib/profile/apply-to-stores.test.ts`
3. `spa/src/lib/profile/applier.ts`
4. `spa/src/lib/profile/applier.test.ts`
5. `spa/src/lib/profile/executor.lock-release.integration.test.ts` (asserts `terminated: 'host-removed'` at l.146–159)
6. `spa/src/lib/new-tab-registry.ts`
7. `spa/src/lib/new-tab-registry.test.ts`
8. `spa/src/lib/session-new-tab-providers.tsx`
9. `spa/src/lib/session-new-tab-providers.test.ts`
10. `spa/src/lib/headless-new-tab-providers.tsx`
11. `spa/src/lib/headless-new-tab-providers.test.ts`
12. `spa/src/hooks/useNewTabBootstrap.test.ts` (l.41 "prunes a removed host's block" flips)
13. `spa/src/components/SessionPaneContent.tsx`
14. `spa/src/components/SessionPaneContent.test.tsx`
15. `spa/src/components/MissingHostPane.tsx` (new)
16. `spa/src/locales/en.json`
17. `spa/src/locales/zh-TW.json`

Tasks:
- **T1 — guard the tmux pane first** (§0.4). Test: a `tmux-session` pane whose `hostId` is not in `hosts` renders
  `MissingHostPane` ("This device has no host ‹id›"; H2 later swaps the id for the look name), no `TerminalView`, no
  `fetchWsTicket`, no probe call. Then implement: `SessionPaneContent` checks `hosts[hostId]` before anything that
  reaches the network; new locale keys `pane.missing_host.title` / `.desc` (en + zh-TW); `execution.host_removed`
  copy changed to "This device has no host for this execution." / zh-TW equivalent. Commit.
- **T2 — the tabs apply stops marking** (§3.1.1). Tests: `applyTabsSection` with a pane on unknown `d1_…` keeps the
  pane byte-for-byte (no `terminated`), returns the hash of the incoming payload (equal → nothing pushed); flip
  `apply-to-stores.test.ts` l.1074 and the `executor.lock-release` case; delete the `markHostRemovedPanes` unit tests.
  Implement: remove the `knownHosts` loop and `markHostRemovedPanes` (export and function). Commit.
- **T3 — the settings apply keeps unknown columns** (§3.1.2). Tests: `settingsFromWire` keeps `sessions:d1_x` /
  `headless:d1_x` in every preset; `applySettingsSection` with such a column → stores hold it, hash equals the
  payload's; an unknown `purdex-host-settings.hosts` key (`d1_unknown`) round-trips apply → store → build
  byte-for-byte; flip `applier.test.ts` l.1563 and `apply-to-stores.test.ts` l.1398. Implement: drop the
  `liveHostIds` parameter and the filter; update the doc comment. Commit.
- **T4 — host-bearing columns are never stale** (§3.2). Tests (registry): a ready source whose `retainsId(id)` is
  true never reports `id` stale; the legacy `sessions` id still is. Tests (providers): `retainsId` true for
  `sessions:<anything>` / `headless:<anything>`, false for `sessions`. Tests (bootstrap): with the host store
  hydrated, `sessions:d1_unknown` / `headless:d1_unknown` survive `run()`; the legacy `sessions` still migrates.
  Implement: optional `retainsId?: (id) => boolean` on `NewTabProviderSource`, honoured in
  `getStaleNewTabProviderIds`; both host sources set it. Commit.

Invariants tested in H1a: an apply that names an unknown host writes no synced field and returns the incoming
payload's hash (no push); an unknown column and an unknown host-settings key round-trip apply → bootstrap → build
byte-for-byte.

Mutations (deliverable — each must turn a test red; recorded in the PR body):
- M1 put the `markHostRemovedPanes` loop back; M2 put the `liveHostIds` filter back; M3 `retainsId` ignored in
  `getStaleNewTabProviderIds`; M4 `retainsId` returns true for the bare `sessions` id; M5 remove the `hosts[hostId]`
  guard in `SessionPaneContent`; M6 guard present but `fetchWsTicket` still called; M7 `settingsFromWire` drops a
  host-settings key whose host is not local.

## H1b — the re-resolve pass (15 files)

Files:
1. `spa/src/lib/host-reresolve.ts` (new)
2. `spa/src/lib/host-reresolve.test.ts` (new)
3. `spa/src/lib/host-reresolve.integration.test.ts` (new — collector build + hashes + parked worlds)
4. `spa/src/main.tsx`
5. `spa/src/lib/profile/sections.ts` (`hostResolverSignature`, moved from `apply-to-stores.ts` and exported)
6. `spa/src/lib/profile/apply-to-stores.ts` (imports it; requests a pass after each `tabs.*` / `settings` outcome)
7. `spa/src/lib/profile/apply-to-stores.test.ts`
8. `spa/src/stores/useTabStore.ts` (`rewritePaneHosts(map)` action)
9. `spa/src/stores/useTabStore.hostRefs.test.ts` (new)
10. `spa/src/stores/useNewTabLayoutStore.ts` (`renameIds(map)` over presets + `knownIds`)
11. `spa/src/stores/useNewTabLayoutStore.test.ts`
12. `spa/src/hooks/useNewTabBootstrap.ts`
13. `spa/src/hooks/useNewTabBootstrap.test.ts`
14. `spa/src/lib/profile/host-identity.ts` (`mapContent` / `mapLayout` keep identity when unchanged — §0.12)
15. `spa/src/lib/profile/host-identity.translate.test.ts`

Tasks:
- **T0 — identity-preserving layout mapping** (§0.12). Tests (`host-identity.translate.test.ts`): `layoutFromWire`
  / `layoutToWire` with a mapper that changes nothing return the SAME object for a leaf, a nested split and a tree
  where only one deep leaf changes (only that path is new; siblings keep identity); existing translate tests stay
  green. Implement in `mapContent` / `mapLayout`. Commit.
- **T1 — store actions.** Tests: `useTabStore.rewritePaneHosts(fn)` maps `tmux-session.hostId`, daemon
  `source.hostId`, `execution.host` (not `''`) in every tab, returns the same object when nothing changes;
  `useNewTabLayoutStore.renameIds(map)` renames in every preset and `knownIds`, dropping a rename whose target is
  already placed in that preset (first occurrence kept, §0.11); host-settings re-key follows `rekeyEntries`
  (sync-id entry wins). Implement via `layoutFromWire` (identity-preserving after T0). Commit.
- **T2 — `hostResolverSignature` moves to `sections.ts`** (pure move + export; existing settings-apply tests stay
  green unchanged). Commit.
- **T3 — the pass.** `runHostReresolve()`: `wireResolverOf(useHostStore)`; `null` → return `'conflict'`.
  Mapping `id → hosts[id] ? id : resolve(id)` (only non-local ids move). One synchronous body holding the operation
  lock (owner `host-reresolve`): tab store, every parked world (`updateParkedWorlds`, via `layoutFromWire`),
  `useHostSettingsStore.hosts` keys (`hostSettingsFromWire`), New Tab `renameIds` for `sessions:` / `headless:`.
  Refused → `'busy'`, retried after 500 ms until it runs (a newer request supersedes). Tests: each store rewritten;
  parked master AND parked slave rewritten; a local id / an unresolvable id untouched; conflict → nothing written;
  busy → retried with fake timers; idempotent (second run writes nothing). Commit.
- **T4 — triggers.** `startHostReresolve()` in `main.tsx`: waits until EVERY store the pass rewrites or reads has
  hydrated — `useHostStore`, `useTabStore`, `useNewTabLayoutStore`, `useLocalProfilesStore` AND
  `useHostSettingsStore` (`purdex-host-settings`, persisted via `purdexStorage`) — and also requests a pass on each of
  those stores' `onFinishHydration`, so a store hydrating after the first pass is still covered; then on every change
  of `hostResolverSignature(useHostStore)`. `applySectionToStores` requests a pass in a `finally` (§0.2). Tests:
  adding a host / learning a daemonId / changing `syncAliases` triggers; a rename does not; `useHostSettingsStore`
  hydrating AFTER the first pass with a `d1_x` key (host x present) ends with the key on the local id; an apply
  outcome triggers; a settings apply into which a pass is interleaved between its awaits, and which then THROWS after
  its rollback restored `d1_x`, still ends with `d1_x` resolved (the `finally` request). Commit.
- **T5 — no duplicate New Tab column** (§0.3). Test: host added whose `sessions:d1_x` is already in a preset (and not
  yet rewritten) → `run()` places no `sessions:<local>`; after the pass, exactly one column, now `sessions:<local>`.
  Implement in the bootstrap: before `ensureDefaults`, drop providers whose `<prefix>:<wire id of its host>` is in
  `knownIds` or any preset. Commit.
- **T6 — integration: hashes and the full path.** Tests (`host-reresolve.integration.test.ts`, real collector
  builders): (a) stores hold `d1_x` refs on screen and parked; add a host whose daemonId hashes to `d1_x`; snapshot
  EVERY section the collector builds (`hosts`, `workspaces`, `settings`, all `tabs.*`) before the pass; after the
  pass every one hashes EXACTLY as before and the executor sees nothing to push (§0.13); (a') the degenerate
  both-forms state of §0.11: host-settings payload unchanged, New Tab payload loses the duplicate (one push);
  (b) an alias-only reference is canonicalised → the `settings` hash changes once (one push, as a pull does);
  (c) receive an unknown column → simulate restart (rehydrate stores from storage) → add the daemon → one live
  column, no duplicate, no push; (d) conflict → nothing moves; the conflict clears → the pass runs. Commit.

Invariants: re-resolve leaves every section hash (all kinds) unchanged for `d1_…` refs (no push) — except the
§0.11 column duplicate; idempotent; never touches an unresolvable id; an untouched store / world keeps its object
identity.

Mutations: M1 the pass skips parked worlds; M2 the mapping resolves local ids too (`hosts[id]` check removed);
M3 the pass runs under conflict; M4 no retry on busy; M5 the bootstrap filter removed (duplicate column);
M6 `renameIds` skips `knownIds`; M7 the post-apply request moved out of the `finally` (the throw test goes red);
M8 `rewritePaneHosts` maps `execution.host === ''`; M9 `useHostSettingsStore` left out of the hydration wait /
hydration triggers; M10 `mapContent` spreads unconditionally again (identity tests red); M11 host-settings re-key
lets the local entry win.

## H1c — local deletion only affects this device (13 files)

Files:
1. `spa/src/lib/host-lifecycle.ts`
2. `spa/src/lib/host-lifecycle.test.ts`
3. `spa/src/lib/host-lifecycle.worlds.test.ts`
4. `spa/src/lib/host-lifecycle.hash.integration.test.ts` (new)
5. `spa/src/lib/host-reresolve.ts` (`rewriteHostRefs(map)` — the explicit-map core shared with the pass)
6. `spa/src/lib/host-reresolve.test.ts`
7. `spa/src/lib/profile/apply-to-stores.ts` (hosts apply: new cascade signature; doc comment)
8. `spa/src/lib/profile/apply-to-stores.test.ts` (l.1257 "marked host-removed in the PARKED master" flips)
9. `spa/src/lib/profile/world-fence.windows.test.ts` (calls `deleteHostCascade`)
10. `spa/src/components/hosts/OverviewSection.tsx` (dialog: no "close tabs" checkbox)
11. `spa/src/components/hosts/OverviewSection.test.tsx`
12. `spa/src/locales/en.json`
13. `spa/src/locales/zh-TW.json`

Tasks:
- **T1 — `rewriteHostRefs(map)`** extracted from the pass: the synchronous body of H1b T3 with an explicit
  `localId → wireId` map (the deletion direction; the reverse is the pass itself). Tests: parked + on screen, host
  settings key, both column kinds, `knownIds`, the §0.11 collisions in this direction (sync-id entry wins; first
  column kept). Commit.
- **T2 — the cascade rewrites instead of marking / closing** (§3.4, §0.7). New signature
  `deleteHostCascade(hostId, grant?)`. Before removal: `wireId` per §0.7; `rewriteHostRefs({[hostId]: wireId})`;
  release held leases best-effort (§0.8); then clear sessions / agent / execution / execution-list / nex / peer / cwd
  / runtime, `removeHost`. Removed: tab close, `markHostTerminated`, hostless pin, `removeHostFromParkedWorlds`,
  `useHostSettingsStore.clearHost`. Tests: on-screen panes, a parked master and a parked slave all carry `wireId`, none
  `terminated`, no tab closed, host settings / columns kept; a no-daemonId host's refs unchanged; a conflicting
  duplicate's refs get `syncIdOfSync(daemonId)`; a legacy hostless execution pane untouched; the last host still
  refused. Rewrite / drop the old-behaviour cases in `host-lifecycle.test.ts` and `host-lifecycle.worlds.test.ts`.
  Commit.
- **T3 — the undo** (§0.6). Undo: re-add host (same id, order, `activeHostId`), restore sessions and agent state,
  then run the pass BODY synchronously (the caller's grant, else a synchronous acquire; refused → a scheduled pass).
  `worldSkipped`, `closedTabs`, `tabWorkspaces`, `terminatedTabPaneIds`, the `hostSettings` snapshot,
  `relabelCount` / `locateWorld` use and `unmarkInParkedWorlds` go; `deleteHostWithUndoToast` loses its `worldSkipped`
  message. Tests: undo brings every `d1_X` ref back to X's local id — on screen, in a parked master and a parked slave,
  and after a profile switch and after a promote (relabel) inside the undo window; the SAME tab id / pane id present
  in two worlds, both on `d1_X` → both resolved (correct: `d1_X` is X everywhere); a ref on another wire id (`d1_Y`,
  and an unresolvable legacy id) untouched; a host recreated during the window (same id) is not overwritten; a
  re-added duplicate (conflict) rewrites nothing; undo is synchronous under a held grant (no await); UI undo with the
  lock held elsewhere → scheduled pass completes it. Commit.
- **T4 — the hosts apply** uses the new cascade under its grant; its rollback relies on T3's synchronous undo. Tests:
  a `hosts` payload dropping a host leaves its panes un-marked with `wireId`; a failing apply rolls back to local ids
  with nothing awaited; flip l.1257; `world-fence.windows.test.ts` adapted to the signature. Commit.
- **T5 — the dialog.** `OverviewSection`: checkbox and `closeTabs` state removed; copy says tabs stay, shown as
  "no host here" on this device, other devices unaffected; locale `hosts.undo_world_skipped` removed, new
  `hosts.delete_keeps_tabs` (en + zh-TW). Tests: no checkbox; delete calls the cascade; undo toast shown. Commit.
- **T6 — hash invariant** (§0.13). `host-lifecycle.hash.integration.test.ts`: with real builders, snapshot EVERY
  section the collector builds; after a deletion (daemonId host AND no-daemonId host) and again after its undo, every
  non-`hosts` section (`workspaces`, `settings`, all `tabs.*`) hashes identically and `hosts` differs (the pre-H3
  exception; asserted explicitly, so H3 flips it); the §0.11 both-forms state: host-settings unchanged, one column
  duplicate dropped. Commit.

Invariants: deletion and undo leave every non-`hosts` section hash unchanged (pre-H3; §0.11 column duplicate
excepted); no synced field written.

Mutations: M1 keep `markHostTerminated`; M2 keep `useHostSettingsStore.clearHost`; M3 skip parked worlds in the
rewrite; M4 use `identity.toWire(...) ?? localId` (conflict test goes red); M5 undo without the reverse rewrite
(relies on the pass → the synchronous-undo test goes red); M6 pin hostless execution panes again; M7 restore the
tab-close branch; M8 undo restricted to refs "recorded by the deletion" (the ref-arrived-in-window test goes red);
M9 T6 compares only `tabs.*` / `settings` (a deliberately injected `workspaces` change stays green → the test must
build all sections).

## Real-device acceptance (after each PR, H1a → H1c cumulative)

Setup (never print tokens — read them into variables, print only lengths):
- Worktree dev server on :5175 (the main checkout's :5174 is not touched). Two isolated clients:
  `playwright cli -s=host-ownership-a` and `-s=host-ownership-b`, run from the worktree root (sessions are keyed by cwd).
- Each client adds its hosts THROUGH ITS OWN UI, so their local host ids differ (feedback: distinct host ids per
  client). Both add mlab (`100.64.0.2:7860`) only. A fresh test workbench on mlab, A master, B attached. air26
  (`100.64.0.4:7860`) is host X: in NEITHER client's list at the start.
- Get X's wire id: `d1_…` of air26's daemonId (`/api/info` → `host_id`, hashed as `syncIdOfSync`; compute it with a
  one-off `node` script importing nothing secret).
- Close both sessions and stop the :5175 dev server before any mutation run (feedback: no mutation test with a live
  page open).

H1a:
1. Inject into the test workbench's SOT a `tabs.<ws>` payload with a pane `tmux-session.hostId = <X d1>` and a
   `settings` whose New Tab preset has `sessions:<X d1>` — `PUT /api/profiles/{id}/sections/{section}` against mlab
   with the current rev as base (from `GET /api/profiles/{id}`).
2. Both clients pull: the pane shows "This device has no host ‹d1_…›"; no terminal WS opened to mlab (check
   `playwright cli requests` — no `/ws/terminal/` for that pane); the New Tab page shows no X block.
3. `GET /api/profiles/{id}`: the revs of every section (`hosts`, `workspaces`, `settings`, `tabs.<ws>`) are unchanged
   after both clients settle (nothing pushed back).

H1b (continuing):
4. On B, add air26 through the UI. B: the pane goes live, the New Tab page shows exactly one air26 block.
   `workspaces` / `tabs.<ws>` / `settings` revs unchanged (only `hosts` moves — B pushes its list; pre-H3).
5. A receives air26 through `hosts`; A's pane goes live too; revs of `tabs.<ws>` / `settings` still unchanged.
6. Reload B (restart): still one block, pane live.

H1c (continuing):
7. On B, delete air26 (dialog: no "close tabs" choice). B: the tab stays, "This device has no host ‹d1_…›".
   `workspaces` / `tabs.<ws>` / `settings` revs unchanged; `hosts` moves. Pre-H3 (§0.9): A's host list loses air26 via `hosts`; A's tab stays,
   un-marked, "no host here" — nothing closed or marked on either client.
8. Undo on B within the toast: B's pane live again at once; A gets air26 back via `hosts` and its pane goes live.
9. Repeat 7 with B showing a local (slave) workbench, so the master world is parked: after undo the parked master's
   pane is live when switched back.

Cleanup: `playwright cli -s=host-ownership-a close`, `-s=host-ownership-b close` (same cwd), delete the test
workbench, remove air26 from the clients if added, stop :5175.

## Review 2026-09-24 (codex plan review task-muefl53l-q7ywrc)

All 7 checked against the code and adopted.

1. Pass ran before `purdex-host-settings` hydrated (critical) — confirmed (`useHostSettingsStore` persists via `purdexStorage`); H1b T4 waits for it and re-requests on every store's `onFinishHydration`; test + M9.
2. Settings-apply throw path had no compensating pass — confirmed (rollback then throw); request moved to a `finally` at the settlement boundary (§0.2, H1b T4), throw test + M7.
3. Undo reference location across relabel undefined — adopted as an executable rule without per-ref records: undo runs the pass body synchronously; a `d1_X` ref means X in every world (resolver maps sync ids regardless of origin), so refs that arrived in the window resolving to X is correct, not collateral (§0.6, H1c T3, same-ids-in-two-worlds test, M8).
4. Local/wire collision loses data and breaks the hash — confirmed (`rekeyEntries` sync-id wins; column mapping keeps duplicates); rule and tests per direction, column duplicate documented as the one-push exception (§0.11, H1b T1/T6, H1c T1/T6, M11).
5. No-push invariants narrowed to `tabs.*` / `settings` — adopted: all sections compared; deletion asserts non-`hosts` identical and `hosts` changed (pre-H3) (§0.13, H1b T6, H1c T6, acceptance, M9).
6. `layoutFromWire` cannot preserve identity — confirmed (`mapContent` spreads, `mapLayout` rebuilds unconditionally); H1b T0 changes both, `host-identity.ts` + its translate test join H1b (15 files) (§0.12, M10).
7. Unknown host-settings round-trip untested in H1a — adopted: H1a T3 test + M7.
