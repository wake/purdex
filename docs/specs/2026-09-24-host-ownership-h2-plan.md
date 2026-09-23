# Plan — host ownership H2 (H2a / H2b / H2c / H2d)

Spec: `docs/specs/2026-09-23-host-ownership-spec.md` (§4, decisions 3, 4, 7, 9; §3.3 re-resolve pass; §3.4; §6.4 steps
5 / 7; §7 H2a–H2d; §8 H2). Measured on the worktree at `618ab449` (alpha.442: H1a, H4a, H4b merged; H1b NOT merged —
its branch `worktree-host-ownership-h1b` was read at `70851837`). Every task is TDD (failing test first) and its own
commit. File lists are the files each PR touches, counted from the code — not estimates.

Test / lint / build: `cd spa && npx vitest run <files>`, `pnpm run lint`, `pnpm run build`, and
`npx tsc --noEmit -p tsconfig.app.json` (a bare `tsc --noEmit` checks nothing in `spa/`).

**Order and dependencies.** Seven PRs (the spec's four, three of them split to stay ≤ 20 files — §0.15):

```
H2a ─► H2b-1 ─► H2b-2 ─┐
                        ├─► H2c-1 ─► H2c-2 ─► H2c-3 ─► H2d-1 ─► H2d-2
H1b (merged) ───────────┘            (H1c may land anywhere after H1b; see §0.11)
```

- H2a, H2b-1, H2b-2 are pure refactors (the selector reads `HostConfig` only) and can start now, in parallel with H1b.
- H2c-1 (store + wire) needs nothing from H1b but is sequenced after H2b so the look store never exists without every
  surface reading through the selector.
- **H2c-2 waits for H1b to merge** (it adds the look re-key to the pass — §0.11 lists what it needs from H1b).
- H2c-3 is the H4b receiver switch of §6.4 step 7 (H4b merged first, so H2c owns it — §0.9).
- H2d-1 follows H2c-3 (ordinal 6 → 7); H2d-2 follows H2d-1.
- Release: H2c-2 and H2c-3 are bumped together (one bump PR after H2c-3) so no released build has the look store as
  SOT while the transfer receiver still writes looks to `HostConfig` (§0.9).

## 0. Where the spec and the code disagree (found while measuring)

Items marked **NEEDS DECISION** carry a recommendation; the plan below is written for the recommendation.

1. **The §4.2 surface list is stale (measured on 442).** Found with a type-checker scan of `spa/src` (non-test files;
   every property read / destructure of `name` `color` `colors` `icon` `iconWeight` on an expression typed
   `HostConfig`) cross-checked with `rg` — 59 checker hits + 2 grep-only hits (`AddHostDialog.tsx:209` `same.name`,
   `EditorStatusBar.tsx:53` — the scratch scan resolved their store types only partially; the in-repo guard of H2a
   sees them). Against the spec's list:
   - **new since the spec** (H4b / H1a): `components/hosts/ShareHostsDialog.tsx` (:69, :94, :150, :173),
     `components/hosts/ReceiveHostsDialog.tsx` (:96, :222), `lib/host-transfer-plan.ts:109` (`payloadRowsOf` — the
     payload, not a screen; §0.10), `components/MissingHostPane.tsx` (H1a; shows the raw id, spec §3.2 says "the look
     for that wire id (H2+)").
   - **missed by the spec** (present on 439): `components/settings/profile/StopSyncControl.tsx:136`,
     `components/settings/LocalDaemonSection.tsx:186`, and the NAME read in `components/hosts/HostBadgePreview.tsx:25`
     (the spec lists that file under colour/icon only).
   - **gone:** none. `lib/peer-pairing-load.ts` reads a caller-supplied `PairingAppHost.name` (:81, :148, :170) —
     unchanged; its caller `PeersSection.tsx:73` moves (as the spec says).
   - **dead:** `components/SessionPanel.tsx` — only `SessionPanel.test.tsx` imports it (see item 2).
   Plan: every file above is in H2a / H2b-1 / H2b-2 (colour/icon in H2a, names split by area).
2. **`SessionPanel` is dead code** (spec §4.2 name surface, §4.5 filter surface). No production import; last touched
   by P-D.3. **NEEDS DECISION** — recommended: delete `SessionPanel.tsx` + `SessionPanel.test.tsx` in H2b-1 (then it is
   neither migrated nor filtered). Alternative: migrate its name read (H2b-1) and add the filter (H2d-2), +1 file each.
3. **"The session launcher's host choice" (§4.5) does not exist.** `SessionLauncher` takes `hostId` as a prop and is
   mounted per host block (`SessionSection.tsx:134` inside `HostSessionSection`, and `hosts/SessionsSection.tsx:115`
   on that host's page); `HeadlessLauncher` likewise lives in the per-host `headless:` block. Plan: the New Tab block
   filter (H2d-2) hides a hidden host's launchers; nothing else to filter.
4. **Wire id in the selector: per host, not `identity.toWire` (§4.2 "local id → wire id (identity)").** Under an
   identity conflict `toWire` omits both conflicting hosts (`buildIdentity`, `host-identity.ts`), so the selector
   would lose their looks exactly while the user is fixing the duplicate. Plan: `wireIdOfHost(host) =
   isValidDaemonId(host.daemonId) ? syncIdOfSync(host.daemonId) : host.id` (memoised per daemonId) — equal to `toWire`
   whenever there is no conflict, and the rule H1 plan §0.7 already uses for deletion. Two rows claiming one daemon
   read and write the same `d1_…` entry (correct: same daemon); the `settings` build refuses under a conflict
   (`refuseConflict`), so nothing is pushed until it clears. No hook ever needs `toWire`, so "toWire lacks the id"
   cannot occur. (Plan choice, flagged for review.)
5. **Field-by-field fallback makes "no colour" impossible (§4.2 "looks[wireId] field by field, else HostConfig").**
   After H2c every write goes to the look store and `HostConfig` keeps its old colour forever; clearing the colour
   in the look store would reveal that old colour again. **NEEDS DECISION** — recommended: the fallback is per GROUP
   once an entry exists: `looks[key]` present → colours (`colors` + legacy `color`) and icon (`icon` + `iconWeight`)
   come from the entry only (absent = none / default); `name` still falls back to `HostConfig.name` field by field
   (an entry never means "no name"). No entry → every field from `HostConfig`. Writers seed an absent entry from the
   host's `HostConfig` look (copy-on-write) before applying the change, so the first edit of one field does not
   drop the others.
6. **`purdex-shown-hosts: { ids: wireId[] | null }` cannot travel through the settings apply (§4.1).** Two measured
   rules in `applier.ts`: (a) `applySettings` rejects the WHOLE `settings` payload when a patched field changes shape
   class, and `null` is its own class (`shapeOf`) — `null` ↔ array is `rejected-settings`; (b) a store the payload
   lacks is "not sent" and left alone, so encoding "all" as an absent field (the builder then emits no store key)
   could never clear another device's list — that device would push its list back. **NEEDS DECISION** — recommended:
   `{ all: boolean; ids: string[] }`, both always present (default `{ all: true, ids: [] }`); `all: true` is the
   spec's `null`. `ids` keeps unknown ids and order in both modes. (The look store has the same need and meets it:
   `looks` defaults to `{}` and is always built — a test pins it.)
7. **A host added while a workbench lists hosts is hidden there** (decision 4: unlisted = hidden). With `all: false`,
   adding mlab-2 through the dialog makes it invisible at once in this workbench. **NEEDS DECISION** — recommended: the
   three add paths (add-host dialog, `registerLocalHost`, transfer create) append the new host's current wire id to
   `ids` when `all === false` (one `settings` push; the host is shown in this workbench, other devices keep it as an
   unknown id until they have it). Alternative: leave it hidden; the Settings › 工作台 editor shows it unticked.
8. **Upgrade may open one `settings` conflict for hosts without a daemonId (§4.3 "same entry, same hash, no
   conflict").** That holds for `d1_…` keys only. A host with no learned daemonId is migrated under its LOCAL id,
   which differs per device, so two devices upgrading build different `settings` payloads → the second push is a
   `locked:conflict` (sync-state.ts, 409 while dirty). Plan: accept (rare — a connected host learns its daemonId at
   once, `startHostDaemonIdVerification`); the acceptance setup waits until both clients have verified every host's
   daemonId before upgrading, and checks no section is locked.
9. **H4b merged before H2c, so H2c owns §6.4 step 7 — and the H4b code also reads looks on the SHARE side.** The
   receiver (`useHostStore.applyHostTransfer` → `transferPatch`, `useHostStore.ts:317`) spreads `o.look` / `c.look`
   into `HostConfig` for created AND overwritten rows (:334, :350). The sender (`payloadRowsOf`,
   `host-transfer-plan.ts:105`) reads name + look from `HostConfig`. Plan (H2c-3): receiver per step 5 — `HostConfig`
   gets the name only (created: name; overwritten: name + ip / port / token as today); a look entry
   `{ name, ...look }` is written under `syncIdOfSync(daemonId)` only when none exists; sender sends
   `hostLookOf(host)` (the workbench look). Spec §4.3 says "name / look go to `HostConfig`" for later hosts while
   §6.4 step 7 says "`HostConfig` gets the name only" — the plan follows §6.4 step 7 (the later, agreed text).
   Between H2c-2 and H2c-3 a received look lands in `HostConfig` only; the selector falls back to it (visible, not
   lost) — hence the joint bump.
10. **Spec gap: the share payload's look source** — covered in item 9 (`payloadRowsOf` takes a look resolver).
11. **What H2c needs from H1b (not merged).** H1b's pass (branch at `70851837`, `spa/src/lib/host-reresolve.ts`):
    `runHostReresolve()` → `wireResolverOf` (`null` → `'conflict'`, nothing written) → acquires the operation lock
    (owner `host-reresolve`) → `rewriteHostRefs(map)` (synchronous) → releases; `requestHostReresolve()` retries
    `busy` every 500 ms. Its trigger / hydration wiring (`startHostReresolve`, H1b T4) is not committed yet. H2c-2 and
    H2d-1 need exactly:
    - `runHostReresolve` stays the place where the conflict gate and the operation-lock grant are taken, and its body
      stays synchronous — H2c-2 adds `rekeyWireKeyedStores(hosts)` AFTER `rewriteHostRefs(map)` in that body;
    - `rewriteHostRefs(map)` stays the explicit-map core that H1c T1 exports for deletion — the look re-key must NOT
      go there, or a deletion (local → wire direction) would move looks (decision 9: deletion touches no look);
    - the pass runs on every `hostResolverSignature` change (daemonId learned is one — the identity pairs move) and
      after hydration of the stores it lists; H2c-2 adds `useHostLookStore`, H2d-1 `useShownHostsStore` to that list
      and to the `onFinishHydration` re-requests (H1b T4's rule, M9 there).
    If H1b lands with a different shape, H2c-2's T6 adapts to it; the three properties above are the contract.
12. **Re-key is the opposite direction of the pass's map.** The pass maps non-local ids → local ids; the look /
    shown-hosts stores are keyed by WIRE id and move local id → `d1_…` (§4.3). Plan: a separate step computing moves
    `[host.id → syncIdOfSync(host.daemonId)]` for every local host with a valid daemonId and `host.id !== d1`, applied
    per store (existing `d1_…` wins, the local-id entry is dropped; shown ids: replaced in place, deduped keeping the
    first). A daemonId CLEARED by a re-point moves nothing back: the `d1_…` entry still names that daemon; the host
    reads its `HostConfig` fallback until the id is re-learned (normally the next connect).
13. **The H1b no-push invariant gets its spec exception.** H1b T6 asserts every section hash is unchanged by the pass.
    With H2c-2 a pass that re-keys a look (or H2d-1 a shown id) changes `settings` — one push, as spec §4.3 says.
    H2c-2 T6 extends H1b's integration test: a host arriving with a daemonId (no local-id entry) → all sections
    identical (H1b's case, still true); a local-id look entry + daemonId learned → only `settings` differs.
14. **The guard test cannot be a grep.** `.name` is read off hundreds of non-host objects (`entry.name`,
    `session.name`, `file.name`, `slaves[id].name`…). Plan: a vitest file running the TypeScript checker over
    `tsconfig.app.json` (scratch run: 545 files, ~4 s) and failing on any read of a look field from a `HostConfig`-typed
    expression outside an allowlist (§H2a T3). It lands in H2a for the colour/icon fields (the spec puts the guard in
    H2b) so H2a's refactor has a test that turns red; H2b adds `name`.
15. **Sizes.** H2b is 26 surface files (§0.1) → H2b-1 / H2b-2. H2c is ≈31 files (store, wire, migration, selector
    switch, writers, providers, re-key, transfer) → H2c-1 (wire) / H2c-2 (switch) / H2c-3 (transfer). H2d is ≈25 →
    H2d-1 (store, wire, re-key) / H2d-2 (UI, filters). Ordinals: `settings` is **5** today
    (`projections.ts:107`); H2c-1 → **6** with `@wire:host-look=1`; H2d-1 → **7** with `@wire:shown-hosts=1`
    (`WIRE_MARKERS.settings`, `projections.ts:172`).
16. **Migration timing is simple here.** `purdexStorage` is synchronous `localStorage` (`storage/browser-backend.ts`),
    so `useHostStore` and `useHostLookStore` are hydrated when their modules load, and no identity is needed (item 4).
    Plan: `migrateHostLooksOnce()` runs in `main.tsx` right before `startProfileSync()` (`main.tsx:51`) — before any
    collector exists, hence before the first build. It asserts both stores `hasHydrated()`; if either is not (a future
    async backend), it defers to their `onFinishHydration` AND `startProfileSync()` is not reordered — a test pins
    that the migration ran before `startProfileSync` is called.
17. **"Settings › 工作台" is `ProfileSection`, and settings stores are device-global** (spec §2). The shown-hosts setting
    therefore applies to whatever workbench is on screen (a local slave too) and syncs with the attached master. The
    editor block (`ShownHostsBlock`, H2d-2) says "synced with the attached workbench". `ProfileSection`'s header rule
    "opening this page writes nothing" (and the iron rule, `start.ironrule.test.ts`) extends to the block — tested.
18. **Test isolation.** The look store is module state; tests that write a colour and later expect none for the same
    host id would leak inside one file. `useHostStore.reset()` has no production caller and 29 test files use it —
    H2c-2 makes it also reset `useHostLookStore` (H2d-1: `useShownHostsStore`).
19. **"Hosts added later" seeding (§4.3) goes to the add paths, not `addHost`.** `addHost` is also the undo of a
    deletion (`host-lifecycle.ts:359`), which must not create entries. Plan: the add-host dialog (`AddHostDialog.tsx:187`)
    and `registerLocalHost` (`useHostStore.ts:515`) seed `looks[wireIdOfHost(host)]` from the new `HostConfig` when
    absent; the transfer receiver does it per item 9.

## H2a — the look selector, colour / icon surfaces (10 files)

Files:
1. `spa/src/lib/profile/host-identity.ts` (`wireIdOfHost`, memoised)
2. `spa/src/lib/profile/host-identity.test.ts`
3. `spa/src/lib/host-look.ts` (new)
4. `spa/src/lib/host-look.test.ts` (new)
5. `spa/src/lib/host-look.guard.test.ts` (new)
6. `spa/src/hooks/useTabHostBadge.ts`
7. `spa/src/hooks/useTabHostBadge.test.ts`
8. `spa/src/components/hosts/HostBadgePreview.tsx` (colour / icon AND its name read at :25)
9. `spa/src/components/hosts/HostColorField.tsx`
10. `spa/src/components/hosts/HostIconField.tsx`

Placement (no cycle): `host-identity.ts` imports only a TYPE from `useHostStore`, so `wireIdOfHost` lives there and
`useHostStore` can call it in H2c. `host-look.ts` imports `useHostStore` (and in H2c `useHostLookStore`); nothing
that `useHostStore` or the look store imports may import `host-look.ts` (the guard's allowlist names it; a test in
H2c-1 imports the look store alone and asserts `useHostStore` is not loaded).

API (`host-look.ts`):
- `interface HostLook { name?: string; colors?: HostConfig['colors']; color?: string; icon?: string; iconWeight?: IconWeight }`
- `hostLookOf(ref: string, hosts = useHostStore.getState().hosts): HostLook` — `ref` is a local id or a wire id.
  H2a/H2b: `Object.hasOwn(hosts, ref)` → that host's five fields (only those present); else `{}`. Memoised per host
  object (`WeakMap`), so equal input → same object.
- `useHostLook(ref: string | null): HostLook` — subscribes to `s.hosts[ref]` (the host object; `Object.hasOwn`
  guarded) and returns the memoised look.
- `useHostLookResolver(): (ref: string) => HostLook` — for lists (a hook cannot run in a loop): subscribes to
  `s.hosts`, returns a callback stable while `hosts` is.
- `hostLabel(ref, look) = look.name ?? ref`.

Tasks:
- **T1 — `wireIdOfHost`.** Tests (`host-identity.test.ts`): valid daemonId → `syncIdOfSync(daemonId)`; absent / `""` /
  invalid → `host.id`; equals `identityOfSync(hosts).toWire.get(id)` for every host of a conflict-free fixture;
  under a conflict (two rows, one daemonId) both return the same `d1_…`; the memo returns the same string without
  re-hashing (spy on an injected hash). Implement. Commit.
- **T2 — the selector.** Tests (`host-look.test.ts`): a local host → its fields, absent fields absent; an unknown ref
  (incl. a `d1_…` and `__proto__`) → `{}`; same host object → same look object; `useHostLook` re-renders on that
  host's rename / colour write and NOT on another host's write or a `runtime` write (render counter);
  `useHostLookResolver` returns a stable callback across `runtime` writes. Implement. Commit.
- **T3 — the guard (colour / icon).** `// @vitest-environment node`, timeout 60 s. Build a program from
  `spa/tsconfig.app.json` (`ts.readConfigFile` + `parseJsonConfigFileContent`); scan source files under `spa/src`
  excluding `*.test.*`, `__tests__/`, `test-setup.ts`, `test-utils*`; flag (a) a `PropertyAccessExpression` whose
  name is a checked field and whose object's non-nullable type is, or has in a union / intersection, the
  `HostConfig` interface declared in `stores/useHostStore.ts`; (b) an object-binding element of a checked field whose
  initializer has that type. Checked fields in H2a: `color`, `colors`, `icon`, `iconWeight`. Allowlist (file-level):
  `stores/useHostStore.ts`, `lib/host-color.ts` (sanitiser), `lib/host-look.ts`. (`host-transfer-plan.ts` reads look
  fields as `h[f]` with a variable key — not a property access; its `h.name` joins the allowlist in H2b-1 and leaves
  it in H2c-3.) Positive control in the same test: `stores/useHostStore.ts` must yield ≥ 1 hit
  (proves the detector sees through zustand types), and the failure message lists `file:line field`. It fails first
  (the four surfaces still read directly). Commit.
- **T4 — move the four surfaces.** `useTabHostBadge` (`useHostLook(hostId)` → `resolveHostColors({colors, color})`,
  memo on the look object), `HostBadgePreview` (name + look), `HostColorField` (`colors` / `color` from the look),
  `HostIconField` (`icon` / `iconWeight`). Existing tests stay green unchanged; add to `useTabHostBadge.test.ts`: a
  tab whose pane names an unresolvable `d1_…` → `colors: null`, default icon, no throw. The guard turns green.
  Commit.

Invariants: behaviour identical (every existing test of the four surfaces unchanged and green); no direct colour /
icon read of `HostConfig` outside the allowlist.

Mutations: M1 `useTabHostBadge` reads `s.hosts[hostId]?.colors` again (guard red); M2 detector ignores union types
(`HostConfig | undefined` reads, i.e. every `hosts[id]?.x`, pass → the positive control / guard red); M3 detector
skips destructuring (`useHostStore.ts:501` no longer counted → positive-control count test red; the test pins the
exact allowlisted hit count per field); M4 `wireIdOfHost` uses `toWire` (conflict test red); M5 `hostLookOf` without
`Object.hasOwn` (`__proto__` test red).

## H2b-1 — the selector, name surfaces: Hosts pages, New Tab, sessions (16 files)

Files (13 surfaces + guard + the dead-code deletion of §0.2):
1. `spa/src/components/hosts/HostSidebar.tsx` (:93)
2. `spa/src/components/hosts/OverviewSection.tsx` (:101, :129, :145 — the rename WRITE moves in H2c-2)
3. `spa/src/components/hosts/LogsSection.tsx` (:16)
4. `spa/src/components/hosts/PeersSection.tsx` (:73, :156, :178)
5. `spa/src/components/hosts/nex/NexConfigForm.tsx` (:78)
6. `spa/src/components/hosts/AddHostDialog.tsx` (:209 duplicate-daemon message)
7. `spa/src/components/hosts/ShareHostsDialog.tsx` (:69, :94, :150, :173 — display only; payload in H2c-3)
8. `spa/src/components/hosts/ReceiveHostsDialog.tsx` (:96, :222)
9. `spa/src/lib/session-new-tab-providers.tsx` (:46)
10. `spa/src/lib/headless-new-tab-providers.tsx` (:51)
11. `spa/src/components/SessionSection.tsx` (:111)
12. `spa/src/components/SessionPickerList.tsx` (:40)
13. `spa/src/components/editor/EditorNewTabSection.tsx` (:106)
14. `spa/src/components/SessionPanel.tsx` (deleted — §0.2)
15. `spa/src/components/SessionPanel.test.tsx` (deleted)
16. `spa/src/lib/host-look.guard.test.ts` (adds `name`)

Tasks:
- **T1 — guard covers `name`** with a TEMPORARY allowlist of exactly the H2b-2 files (named in the test with a
  comment "emptied by H2b-2") plus `lib/host-transfer-plan.ts` (the share payload, until H2c-3). Red on files 1–13.
  Commit.
- **T2 — Hosts pages** (files 1–8): `useHostLook` for one host, `useHostLookResolver` in lists / pickers
  (`HostSidebar`, `ShareHostsDialog`, `ReceiveHostsDialog`, `PeersSection.toApp` — a callback reading
  `hostLookOf(id, hs)` from the `getState()` it already takes). `hostLabel(ref, look)` where the code falls back to
  the id today. Existing tests green. Commit.
- **T3 — New Tab and sessions** (files 9–13): providers build `labelParams.host` from
  `hostLabel(hostId, hostLookOf(hostId, hosts))`; `SessionSection` / `SessionPickerList` / `EditorNewTabSection`
  through the hooks. Existing provider tests green (label still equals the host name). Commit.
- **T4 — delete `SessionPanel`** (if §0.2 is decided so; else migrate its :78 read here). `pnpm run build` and lint
  prove nothing imports it. Commit.

Invariants: behaviour identical; guard green with the temporary allowlist.

Mutations: M1 revert `HostSidebar.tsx:93` to `host.name` (guard red); M2 revert a provider's `labelParams` (guard
red); M3 add a new `hosts[id]?.name` read to a non-allowlisted file (guard red).

## H2b-2 — the selector, name surfaces elsewhere (13 files)

Files:
1. `spa/src/components/StatusBar.tsx` (:359)
2. `spa/src/components/editor/EditorStatusBar.tsx` (:53)
3. `spa/src/components/MemoryMonitorPage.tsx` (:283)
4. `spa/src/components/executions/ExecutionsView.tsx` (:40)
5. `spa/src/hooks/useNotificationDispatcher.ts` (:305 — non-hook: `hostLookOf(hostId, state.hosts)`)
6. `spa/src/components/settings/DevEnvironmentSection.tsx` (:344)
7. `spa/src/components/settings/LocalDaemonSection.tsx` (:186 — missed by the spec)
8. `spa/src/components/settings/profile/CurrentBlock.tsx` (:230, :248)
9. `spa/src/components/settings/profile/StopSyncControl.tsx` (:136 — missed by the spec)
10. `spa/src/components/settings/profile/wizard/ProfileWizard.tsx` (:357, :548)
11. `spa/src/components/settings/profile/wizard/WizardChoiceSteps.tsx` (:74 twice, :288)
12. `spa/src/components/MissingHostPane.tsx` (shows `hostLabel(hostId, useHostLook(hostId))` — the raw id in
    H2b, the workbench look name from H2c-2; spec §3.2 table)
13. `spa/src/lib/host-look.guard.test.ts` (temporary allowlist emptied; only the permanent entries and
    `lib/host-transfer-plan.ts` remain)

Tasks:
- **T1 — empty the temporary allowlist** (red on files 1–11). Commit.
- **T2 — move files 1–11** (hooks in components, `hostLookOf` in `useNotificationDispatcher` and in callbacks);
  existing tests green. Commit.
- **T3 — `MissingHostPane` through the selector** (no visible change yet; its behaviour test comes in H2c-2). Commit.

Invariants: no direct look read of `HostConfig` outside `useHostStore.ts`, `host-color.ts`, `host-look.ts` and (until
H2c-3) `host-transfer-plan.ts`.

Mutations: M1 revert `useNotificationDispatcher.ts:305` (guard red); M2 revert `StopSyncControl.tsx:136` (guard red —
proves the missed files are covered); M3 put `StatusBar.tsx` back into the allowlist and revert it (the allowlist
content is snapshotted in the test → red).

## H2c-1 — the look store and its wire (12 files)

Files:
1. `spa/src/stores/useHostLookStore.ts` (new)
2. `spa/src/stores/useHostLookStore.test.ts` (new)
3. `spa/src/lib/storage/keys.ts` (`HOST_LOOKS: 'purdex-host-looks'`, `HOST_LOOKS_MIGRATED: 'purdex-host-looks-migrated'`
   — the latter documented as manual, device-local, never projected, never `syncManager`)
4. `spa/src/lib/profile/types.ts` (`SettingsStorageKey` += `'purdex-host-looks'`)
5. `spa/src/lib/profile/projections.ts`
6. `spa/src/lib/profile/projections.test.ts`
7. `spa/src/lib/profile/collector.ts` (`SETTINGS_STORES` += the store)
8. `spa/src/lib/profile/collector.test.ts` (`EIGHT_KEYS` → nine; register record)
9. `spa/src/lib/profile/apply-to-stores.ts` (`SETTINGS_STORES` += the store)
10. `spa/src/lib/profile/apply-to-stores.test.ts` (l.751 `toHaveLength(8)` → 9; new cases)
11. `spa/src/lib/profile/applier.test.ts`
12. `spa/src/lib/profile/sections.test.ts`

Store: `create(persist(...))`, `name: STORAGE_KEYS.HOST_LOOKS`, `storage: purdexStorage`,
`syncManager.register(...)`. State `{ looks: Record<string, HostLookEntry> }`, default `{ looks: {} }`;
`HostLookEntry = { name?: string; colors?; color?; icon?; iconWeight? }`. `merge` sanitises every entry (the
`sanitizeHostConfig` validators from `host-color.ts`; unknown fields dropped; a non-string name dropped; keys kept
verbatim, `__proto__` never copied). Actions: `putLook(key, entry)`, `patchLook(key, fn)`, `rekey(moves)`
(H2c-2 uses it), no host lookup — keys are opaque strings. The module imports no store.

Wire (`projections.ts`):
- `PROJECTIONS.settings` += `...settingsPaths('purdex-host-looks', ['looks'])` (the whole record: keys are wire ids
  already; entries sanitised by the store).
- `SECTION_SCHEMA_ORDINAL.settings`: **5 → 6**, comment `// 6: purdex-host-looks.looks (host looks keyed by wire id;
  host ownership H2c)`.
- `WIRE_MARKERS.settings`: `['@wire:host-id=d1', '@wire:host-look=1']`.
- `buildSettingsSection` and `settingsFromWire` are NOT changed: no local↔wire mapping for this key (spec §4.1) — a
  test proves the build passes keys verbatim WITH an identity handed in.

Tasks:
- **T1 — the store.** Tests: default `{looks: {}}`; `merge` drops an invalid colour / icon / weight / unknown field
  and keeps unknown keys (`d1_zzz`, a legacy local id) and their order-independent content; `__proto__` key ignored;
  `putLook` / `patchLook` / `rekey` (existing target wins, source dropped; missing source no-op); registered with
  `syncManager`; importing the module does not load `useHostStore` (`vi.importActual` + module registry check).
  Commit.
- **T2 — projection, ordinal, marker, guard snapshot.** Tests (`projections.test.ts`): the guard snapshot updated
  (settings fingerprint new, ordinal 6) in the same commit; a coexistence test "an ordinal-5 client is locked": the
  ordinal-5 list (`PROJECTIONS.settings` minus `purdex-host-looks.looks`) + marker `['@wire:host-id=d1']` →
  `compareShape(old, mine) === 'sot-is-newer'`, `compareShape(mine, old) === 'i-am-newer'`; no projection path starts
  with `purdex-host-looks-migrated`. Commit.
- **T3 — collector and apply maps.** Tests: `collector.test.ts` nine keys, a look write re-schedules `settings`;
  `buildSettingsSection` with an EMPTY look store emits `'purdex-host-looks': { looks: {} }` (always present — §0.6);
  keys `d1_a`, `localX`, `d1_unknown` pass through verbatim with an identity whose `toWire` maps `localX` → `d1_x`
  (no mapping). `applier.test.ts`: `isWellFormedSection('settings', …)` accepts the store, refuses an unlisted field
  inside it; `applySettings` replaces `looks` whole (an entry the payload lacks is dropped locally — replace
  semantics, like host-settings). `apply-to-stores.test.ts`: a `settings` payload with `looks: {d1_unknown: …,
  d1_a: …}` → store holds both byte-for-byte, the returned hash equals the payload's (nothing pushed); an ordinal-5
  payload without the store leaves the look store untouched and the rebuilt hash differs (the new build is pushed —
  expected once). Commit.
- **T4 — §4.4 guarantees.** Tests (`apply-to-stores.test.ts`, `sections.test.ts`): a `hosts` apply that renames and
  recolours a host leaves `useHostLookStore.getState()` the SAME object; `buildHostsSection` output is identical
  whatever the look store holds; `buildSettingsSection` output does not change when a `HostConfig` name / colour
  changes. Commit.

Invariants: `settings` round-trips any `looks` payload byte-for-byte (apply → build); `hosts` build / apply never
read or write the look store; the look store is always present in the build.

Mutations: M1 ordinal left at 5 with the new path (guard snapshot red); M2 marker missing (coexistence test red —
fingerprint only moves by the path, the test asserts the marker list); M3 the builder maps look keys through
`toWire` (verbatim test red); M4 the apply filters look keys to local hosts (unknown-id test red); M5 `hosts` apply
writes the look store (§4.4 test red); M6 look store default without `looks` (always-present test red).

## H2c-2 — the look store becomes the SOT (20 files)

Files:
1. `spa/src/lib/host-look.ts` (reads the look store; group fallback §0.5; `rekeyWireKeyedStores`)
2. `spa/src/lib/host-look.test.ts`
3. `spa/src/lib/host-look-migration.ts` (new)
4. `spa/src/lib/host-look-migration.test.ts` (new)
5. `spa/src/main.tsx` (migration before `startProfileSync()`)
6. `spa/src/stores/useHostStore.ts` (writers rerouted, `setHostName`, `registerLocalHost` seed, `reset` clears looks)
7. `spa/src/stores/useHostStore.test.ts`
8. `spa/src/components/hosts/OverviewSection.tsx` (:146 rename → `setHostName`)
9. `spa/src/components/hosts/OverviewSection.test.tsx`
10. `spa/src/components/hosts/AddHostDialog.tsx` (seed after add, §0.19)
11. `spa/src/components/hosts/AddHostDialog.test.tsx`
12. `spa/src/lib/session-new-tab-providers.tsx` (`subscribe` also on the look store)
13. `spa/src/lib/session-new-tab-providers.test.ts`
14. `spa/src/lib/headless-new-tab-providers.tsx`
15. `spa/src/lib/headless-new-tab-providers.test.ts`
16. `spa/src/lib/host-reresolve.ts` (from H1b: re-key step + hydration list)
17. `spa/src/lib/host-reresolve.test.ts`
18. `spa/src/lib/host-reresolve.integration.test.ts` (from H1b: §0.13)
19. `spa/src/lib/host-look.guard.test.ts` (allowlist += `lib/host-look-migration.ts`)
20. `spa/src/components/SessionPaneContent.test.tsx` (missing-host pane shows the look name)

Selector after H2c-2: `hostLookOf(ref, hosts = …, looks = useHostLookStore.getState().looks)`: local host → key
`wireIdOfHost(host)`, entry `looks[key]` → §0.5 group rule; not local → `looks[ref]` fields (`{}` when absent).
`useHostLook` subscribes to the host object AND `s.looks[key]` (key recomputed from the host); `useHostLookResolver`
to `s.hosts` and `s.looks`.

Writers (`useHostStore.ts`), all: unknown host → no-op; key = `wireIdOfHost(host)`; base = `looks[key]` ?? seed from
`HostConfig` (`name`, `colors`, `color`, `icon`, `iconWeight` present); apply the SAME validation / legacy-`color`
rules as today (the body of `setHostColorLayer` :451–492 and `setHostIcon` :496–511 becomes a pure function over a
look object, kept in this file); nothing changed → no write; `HostConfig` is never touched. `setHostColor` reads the
current alpha from `hostLookOf`. New `setHostName(hostId, name)`: trimmed, blank → no-op. `registerLocalHost` seeds
when it CREATES a host. `reset()` also resets the look store.

Migration (`host-look-migration.ts`) `migrateHostLooksOnce(): 'already' | 'migrated'`: marker
`localStorage[HOST_LOOKS_MIGRATED] === '1'` → `'already'` (read in try/catch; a throw reads as "not migrated").
Else, in `hostOrder` order then remaining hosts: key = `wireIdOfHost(host)`; `looks[key]` present → skip; else the
seed entry. ONE `setState`. Then write the marker (try/catch; a failed write only means a harmless re-run next boot).

Tasks:
- **T1 — selector reads the look store** (§0.5). Tests: entry present → name / colours / icon from it; entry with no
  colour → `colors`/`color` absent even though `HostConfig` has one (§0.5); entry with no name → `HostConfig.name`;
  no entry → all from `HostConfig`; an unresolvable `d1_…` ref → its entry; a host WITHOUT daemonId → the entry under
  its local id; two rows claiming one daemon (conflict) → both read the `d1_…` entry; the hook re-renders on a write
  to its own entry only. Commit.
- **T2 — writers.** Tests (`useHostStore.test.ts`, the existing colour / icon cases rewritten to read the look
  store): every write lands in `looks[wireIdOfHost(host)]` and leaves `hosts` the same object; first write seeds from
  `HostConfig` (set icon → the old colour is still there); "No color" on a legacy-`color` host clears it in the
  entry; validation no-ops unchanged; `setHostName`; `registerLocalHost` creating a host seeds, re-registering does not;
  `reset()` clears looks. `OverviewSection.test.tsx`: rename → look store, `HostConfig.name` unchanged.
  `AddHostDialog.test.tsx`: add → entry under the new local id with the typed name; an existing `d1_…` entry is never
  overwritten by a later add of that daemon (seed only when absent). Commit.
- **T3 — migration.** Tests: keyed by the CURRENT wire id (`d1_…` with a daemonId, local id without); an existing
  entry is never overwritten; marker set → nothing runs (a reset look stays reset); marker read throwing → runs;
  hosts under conflict both map to one key, one entry; the marker key is not in `PROJECTIONS` and not registered with
  `syncManager`. `main.tsx`: a test (module-level, `vi.mock` of `startProfileSync`) asserts the migration ran before
  `startProfileSync` was called. Commit.
- **T4 — New Tab labels follow a synced rename.** Tests: provider `subscribe` fires on a look-store write to that
  host's entry; `getProviders()` label then carries the new name; an apply of a `settings` payload whose `looks`
  renames the host's `d1_…` entry → the listener fires and the label changes (via `applySectionToStores`). Commit.
- **T5 — missing-host pane shows the look name.** Test (`SessionPaneContent.test.tsx`): a pane on unresolvable
  `d1_x` with `looks.d1_x.name = 'air26'` → "This device has no host air26"; without an entry → the id. Commit.
- **T6 — re-key in the pass** (§0.11, §0.12, §0.13). `rekeyWireKeyedStores(hosts)` (in `host-look.ts`, pure moves +
  `useHostLookStore.rekey`), called in `runHostReresolve` after `rewriteHostRefs(map)`, under the same grant; look
  store added to the pass's hydration wait and `onFinishHydration` triggers. Tests (`host-reresolve.test.ts`): a
  local-id entry moves to `d1_…` when the daemonId is learned; an existing `d1_…` entry wins and the local-id entry is
  dropped; conflict → nothing moves; `rewriteHostRefs({[localId]: d1})` called directly (the deletion direction H1c
  uses, merged or not) moves no look; idempotent.
  Integration (`host-reresolve.integration.test.ts`): host added with its daemonId, no local-id entry → every
  section hash identical (H1b's invariant kept); local-id entry + daemonId learned → only `settings` differs, once.
  Commit.

Invariants: UI reads only through the selector, UI writes only to the look store (§4.4); `HostConfig` look fields are
written only by add paths (name) and — until H2c-3 — the transfer receiver; migration never overwrites and never runs
twice; deletion never touches the look store.

Mutations: M1 field-by-field colour fallback (the §0.5 test red); M2 a writer also writes `HostConfig` (the
`hosts`-same-object test red); M3 no seeding (icon-keeps-colour test red); M4 migration overwrites an existing entry;
M5 migration ignores the marker; M6 migration keyed by local id always (daemonId case red); M7 provider `subscribe`
without the look store (T4 red); M8 re-key inside `rewriteHostRefs` (deletion-moves-no-look test red); M9 re-key
where the local-id entry wins; M10 look store missing from the pass's hydration wait (hydrate-after-first-pass test
red); M11 migration moved after `startProfileSync` (ordering test red).

## H2c-3 — the transfer uses the look store (8 files)

Files:
1. `spa/src/stores/useHostStore.ts` (`applyHostTransfer` / `transferPatch`)
2. `spa/src/stores/useHostStore.test.ts` (flip the cases at l.760, l.772, l.808; add rollback)
3. `spa/src/lib/host-transfer-plan.ts` (`payloadRowsOf(hosts, lookOf)`)
4. `spa/src/lib/host-transfer-plan.test.ts`
5. `spa/src/components/hosts/ShareHostsDialog.tsx` (:72 passes `hostLookOf`)
6. `spa/src/components/hosts/ShareHostsDialog.test.tsx`
7. `spa/src/components/hosts/ReceiveHostsDialog.test.tsx`
8. `spa/src/lib/host-look.guard.test.ts` (allowlist − `lib/host-transfer-plan.ts`)

Tasks:
- **T1 — the sender sends the workbench look.** Tests: `payloadRowsOf` takes `lookOf`; name and look come from it
  (an entry that differs from `HostConfig` wins; a host without entry sends its `HostConfig` look); the guard is green
  without the allowlist entry. Commit.
- **T2 — the receiver (spec §6.4 step 5 / 7).** `transferPatch` stops spreading `o.look` / `c.look` into
  `HostConfig`: created rows get `name`; overwritten rows get `name`, `ip`, `port`, `token` (look fields untouched).
  `applyHostTransfer` computes the look patch purely before the host `set` (`syncIdOfSync(daemonId)` of each created /
  overwritten row; `{ name, ...look }` only where `looks[key]` is absent — a row without a look still gets `{name}`),
  commits the host `set`, then the look `setState`; if the look write throws, the host store is restored to the
  captured `{hosts, hostOrder, runtime, activeHostId, devHostId}` and the look store to its captured `looks`, result
  `stale`. Tests: created → `HostConfig` has name and no look field, the look entry holds name + look; an existing
  `d1_…` entry is not changed (workbench wins); overwrite → `HostConfig.name` replaced, look store unchanged when an
  entry exists, written when none; a throw in the look write → hosts, hostOrder, activeHostId, devHostId and looks all
  identical to before (spy); `ReceiveHostsDialog.test.tsx` end to end: confirm → the look store holds the entry.
  Commit.

Invariants: after a transfer `HostConfig` holds no look fields it did not hold before; a workbench look is never
overwritten by a received one; the commit is all-or-nothing across both stores.

Mutations: M1 keep spreading `look` into `HostConfig`; M2 overwrite an existing entry; M3 no rollback on a look-write
throw; M4 `payloadRowsOf` reads `HostConfig` again (guard red).

## H2d-1 — shown hosts: store, wire, re-key (14 files)

Files:
1. `spa/src/stores/useShownHostsStore.ts` (new)
2. `spa/src/stores/useShownHostsStore.test.ts` (new)
3. `spa/src/lib/storage/keys.ts` (`SHOWN_HOSTS: 'purdex-shown-hosts'`)
4. `spa/src/lib/profile/types.ts` (`SettingsStorageKey` += `'purdex-shown-hosts'`)
5. `spa/src/lib/profile/projections.ts`
6. `spa/src/lib/profile/projections.test.ts`
7. `spa/src/lib/profile/collector.ts`
8. `spa/src/lib/profile/collector.test.ts`
9. `spa/src/lib/profile/apply-to-stores.ts`
10. `spa/src/lib/profile/apply-to-stores.test.ts`
11. `spa/src/lib/shown-hosts.ts` (new — selector)
12. `spa/src/lib/shown-hosts.test.ts` (new)
13. `spa/src/lib/host-reresolve.ts` (re-key ids; hydration list)
14. `spa/src/lib/host-reresolve.test.ts`

Store (per §0.6 recommendation): `{ all: boolean; ids: string[] }`, default `{ all: true, ids: [] }`, persisted
(`purdexStorage`, `syncManager`), `merge` keeps strings only, dedupes keeping the first, keeps unknown ids and order.
Actions: `showAll()`, `setShown(ids)` (`all: false`), `toggle(wireId, knownWireIds)` (from `all: true` → `ids` = every
known wire id except that one, unknown ids kept), `rekey(moves)`.
Wire: `PROJECTIONS.settings` += `...settingsPaths('purdex-shown-hosts', ['all', 'ids'])`; ordinal **6 → 7**
(comment `// 7: purdex-shown-hosts (all, ids: wire ids)`); `WIRE_MARKERS.settings` += `'@wire:shown-hosts=1'`.
Selector (`shown-hosts.ts`): `isHostShown(hostId, hosts, shown)` = `shown.all || shown.ids.includes(wireIdOfHost(hosts[hostId]))`
(an id that is not a local host → `true`: navigation to it is not this module's business); `useIsHostShown(hostId)`;
`useShownHostFilter(): (hostId) => boolean`.

Tasks:
- **T1 — store.** Tests: defaults; `merge`; `toggle` from all / from a list; unknown ids survive every action;
  `rekey` (`d1_…` already present → local id dropped, else replaced in place). The tests of this PR reset the store in
  their own `beforeEach`; the `useHostStore.reset()` hook of §0.18 is added by H2d-2, which touches `useHostStore.ts`
  anyway. Commit.
- **T2 — wire.** Guard snapshot (ordinal 7); coexistence: the ordinal-6 list + markers locks against ours; a
  payload `{all: true, ids: ['d1_unknown']}` round-trips apply → build byte-for-byte; `{all: false, ids: [...]}` ↔
  `{all: true, ids: [...]}` applies without `rejected-settings` (§0.6 — the reason for the shape); the store is always
  present in the build. Commit.
- **T3 — selector.** Tests: `all` → every host shown; list → by `wireIdOfHost` (a `d1_…` id shows the local host of
  that daemon; a no-daemonId host by local id); unknown host id → shown. Commit.
- **T4 — re-key in the pass.** Same shape as H2c-2 T6: local id → `d1_…` on daemonId learned; dedupe; conflict →
  nothing; deletion path moves nothing. Commit.

Invariants: unknown ids kept through apply, build, toggle and re-key; `all` ↔ list transitions always apply.

Mutations: M1 `ids: string[] | null` store (T2 transition test red — `rejected-settings`); M2 `merge` drops ids that
are not local hosts; M3 ordinal not bumped; M4 selector compares local ids (daemonId case red); M5 re-key keeps both
forms (dedupe test red).

## H2d-2 — shown hosts: editor and filters (16 files)

Files:
1. `spa/src/components/settings/profile/ShownHostsBlock.tsx` (new)
2. `spa/src/components/settings/profile/ShownHostsBlock.test.tsx` (new)
3. `spa/src/components/settings/profile/ProfileSection.tsx` (renders the block after `CurrentBlock`)
4. `spa/src/components/settings/profile/ProfileSection.test.tsx` (opening writes nothing)
5. `spa/src/components/hosts/HostSidebar.tsx`
6. `spa/src/components/hosts/HostSidebar.test.tsx`
7. `spa/src/components/SessionPickerList.tsx`
8. `spa/src/components/SessionPickerList.test.tsx`
9. `spa/src/components/NewTabPage.tsx` (filter `byId` for host-bearing ids)
10. `spa/src/components/NewTabPage.test.tsx`
11. `spa/src/components/hosts/AddHostDialog.tsx` (§0.7 append)
12. `spa/src/stores/useHostStore.ts` (§0.7 append in `registerLocalHost` and transfer create; `reset` resets shown)
13. `spa/src/stores/useHostStore.test.ts`
14. `spa/src/locales/en.json`
15. `spa/src/locales/zh-TW.json`
16. `spa/src/lib/shown-hosts.hidden-not-absent.test.tsx` (new)

Editor (`ShownHostsBlock`): "Show all hosts" switch (`all`); when off, one checkbox per local host in `hostOrder`
(label from `useHostLookResolver`), then every `ids` entry that is not a local host's wire id, labelled with its look
name or id and "not on this device" (checked, can be unticked = removed). Copy says the choice is synced with the
attached workbench and hidden hosts stay connected. Writes only on user action.

Filters (spec §4.5, and only these): `HostSidebar` (`hostOrder.filter(isShown)` — the selected host is still rendered
by `HostPage` if navigated to directly); `SessionPickerList` (`connectedHosts.filter(isShown)`); `NewTabPage`
(`byId` drops a provider whose id is `sessions:<id>` / `headless:<id>` — prefixes from
`HOST_BEARING_COLUMN_PREFIXES` — with `isShown(id) === false`; the launchers go with the block, §0.3). `SessionPanel`
is gone (§0.2).

Tasks:
- **T1 — editor.** Tests: renders local hosts + unknown ids ("not on this device"); toggling writes the store; the
  switch writes `all`; mounting `ProfileSection` with no master and no action writes nothing (spy on
  `localStorage.setItem` and the store's `setState`). Locale keys `settings.profile.shown_hosts.*` (en + zh-TW;
  `locale-completeness.test.ts` covers parity). Commit.
- **T2 — the three filters.** Tests: hidden host absent from `HostSidebar`, from `SessionPickerList`, and its
  `sessions:` / `headless:` blocks not rendered on the New Tab page while other columns render; `all: true` shows
  all; an unknown id in `ids` hides nothing local. Commit.
- **T3 — hidden ≠ absent** (`shown-hosts.hidden-not-absent.test.tsx`). Behaviour with a hidden host: provider sources
  still return its providers and `getStaleNewTabProviderIds` does not report its column; `useNewTabLayoutStore`
  presets keep the column after bootstrap; `useTabHostBadge` still returns its badge; `HostPage` at
  `/hosts/<hidden>/overview` renders `OverviewSection`; `DevEnvironmentSection` lists it; `useNotificationDispatcher`
  still dispatches for it (fixture from its existing test). Static half: an import guard — only
  `ShownHostsBlock.tsx`, `HostSidebar.tsx`, `SessionPickerList.tsx`, `NewTabPage.tsx`, `shown-hosts.ts`,
  `useHostStore.ts`, `AddHostDialog.tsx`, `host-reresolve.ts`, `collector.ts`, `apply-to-stores.ts` may import
  `useShownHostsStore` / `lib/shown-hosts` (scan of import declarations; proves `useMultiHostEventWs`,
  `useSessionWatch`, backup triggers, `nex/resolve-host`, `fs-backends`, the tab bar and every `activeHostId` /
  `hostOrder[0]` fallback cannot filter). Commit.
- **T4 — a new host is shown** (§0.7, if decided). Tests: with `all: false`, the dialog add / `registerLocalHost` /
  transfer create append the new host's wire id; with `all: true` nothing changes. Commit.

Invariants: only the three navigation surfaces filter; everything in spec §4.5's not-filtered list is unaffected
(behaviour tests + import guard); the editor writes only on user action.

Mutations: M1 filter in `useMultiHostEventWs` (import guard red); M2 `NewTabPage` removes the column from the preset
instead of skipping it (preset-kept test red); M3 the sidebar filters by local id (daemonId host test red); M4
the editor drops unknown ids on toggle (unknown-id test red); M5 the block writes on mount (iron-rule test red).

## Real-device acceptance (spec §8 H2)

Setup (never print tokens — read them into variables, print only lengths):
- Worktree dev server on :5175 (the main checkout's :5174 is not touched). Two clients, each its own host ids
  (feedback: distinct host ids per client): `playwright cli -s=host-ownership-a` and `-s=host-ownership-b`, run from
  the worktree root. Each adds mlab (`100.64.0.2:7860`) and air26 (`100.64.0.4:7860`) THROUGH ITS OWN UI. Wait until
  both clients show both hosts verified (daemonId learned — §0.8), THEN load the H2c-2 build. A fresh test workbench
  W on mlab, A master, B attached.
- Close both sessions and stop :5175 before any mutation run (feedback: no mutation test with a live page open).
- Section revs: `GET /api/profiles/{id}` on mlab (auth header from a variable).

After H2a / H2b-2 (regression only): tab-bar and sidebar badges, Hosts page names, New Tab block labels, status bar,
wizard host names unchanged on both clients.

After H2c-1: the `settings` payload on the SOT holds `purdex-host-looks: {looks: {}}` and the `settings` row's ordinal
is 6; no section locked on either client.

After H2c-3 (H2c-2 + H2c-3 together):
1. Reload both. `settings.looks` holds exactly the two `d1_…` keys; both clients' `settings` synced, none locked.
2. On A change mlab's console colour → B's tab badge and sidebar badge follow without reload. The `hosts` rev is
   UNCHANGED (the colour no longer travels in `hosts`); `settings` moved once.
3. On A rename air26 → B's New Tab block label for air26 follows without reload; `hosts` rev unchanged.
4. On A clear mlab's colour → B shows no colour (the §0.5 rule; B's `HostConfig` still holds the old one).
5. Transfer precedence. A third session `-s=host-ownership-c` (NOT attached to any workbench, so its hosts never
   sync) adds air26 under another name and colour. On B delete air26 (pre-H3 A loses it too via `hosts`; record
   `settings.looks.<d1 air26>` — it survives the deletion). C shares air26 through mlab; B redeems (add-new): B's
   air26 shows the WORKBENCH name / colour, not C's; `settings.looks.<d1 air26>` is byte-for-byte what was recorded;
   B's `HostConfig` for air26 carries C's name only (inspect `purdex-hosts` in B's localStorage, no look fields).

After H2d-2:
6. On A in W, Settings › 工作台: turn off "show all", untick air26 → B: air26 gone from the Hosts sidebar, the New
   Tab page and the terminated-pane picker; on BOTH clients air26 stays connected (Hosts page status via direct URL
   `/hosts/<air26 id>/overview`, tab badge of an air26 tab still shown, `playwright cli requests` shows its event WS
   open).
7. On A turn "show all" back on → B shows air26 again; no section locked.

Not reachable before H3 (the `hosts` section still syncs — same reason as H1 plan §0.9):
- A client that lacks a host the workbench has a look / shown id for: the host lists converge through `hosts`, so
  "unknown ids kept, listed as not on this device" and "look of a host this device lacks" are checked by INJECTING
  into W's SOT a `settings` payload (current rev as base, `PUT /api/profiles/{id}/sections/settings`) whose `looks` and
  `ids` carry an extra `d1_ffff…` → both clients keep it through apply and their next build (`settings` rev moves
  only by the injection), the editor lists it as "not on this device", a pane injected on that id shows its look name.
- "B deletes a host, A unaffected": pre-H3 A loses the host through `hosts`; what IS checked is that the look entry
  and shown id survive on both.
- Independent host lists with different add orders / names per device: the `HostConfig` fallback differs per device
  only after H3; here both fallbacks are equal, so steps 2–4 prove the look path through the `hosts`-rev-unchanged
  check instead.

Cleanup: `playwright cli -s=host-ownership-a close`, `-s=host-ownership-b close` (same cwd), delete W, stop :5175.

## Review
