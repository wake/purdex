# Plan — host ownership H2 (H2a / H2b / H2c / H2d)

Status: **rev 2** (2026-09-24) — revised after the codex plan review `task-muei1qbr-0hu3ol` (§Review).

Spec: `docs/specs/2026-09-23-host-ownership-spec.md` (§4, decisions 3, 4, 7, 9; §3.3 re-resolve pass; §3.4; §6.4 steps
5 / 7; §7 H2a–H2d; §8 H2). Measured on the worktree at `618ab449` (alpha.442: H1a, H4a, H4b merged; H1b NOT merged —
its branch `worktree-host-ownership-h1b` was read at `70851837`). Every task is TDD (failing test first) and its own
commit. File lists are the files each PR touches, counted from the code — not estimates.

Test / lint / build: `cd spa && npx vitest run <files>`, `pnpm run lint`, `pnpm run build`, and
`npx tsc --noEmit -p tsconfig.app.json` (a bare `tsc --noEmit` checks nothing in `spa/`).

**Order and dependencies.** Nine PRs (the spec's four; H2b, H2c and H2d split to stay ≤ 20 files — §0.15):

```
H2a ─► H2b-1 ─► H2b-2 ─► H2c-1 ─► H2c-2 ─► H2c-3 ─► H2d-1 ─► H2d-2 ─► H2d-3
                                    ▲                   ▲
H1b (T3 pass, T4 triggers/hydration, T6 integration) ───┴── merged before H2c-2 and before H2d-1
```

- **No H1b dependency:** H2a, H2b-1, H2b-2 (pure refactors — the selector reads `HostConfig` only) and H2c-1 (store +
  wire). They can start now, in parallel with H1b. H2c-1 is sequenced after H2b only so the look store never exists
  while a surface still reads `HostConfig` directly.
- **Needs H1b merged** (its T3 pass, T4 trigger / hydration wiring and T6 integration test — the interface §0.11
  lists): H2c-2 (look re-key) and H2d-1 (shown-hosts re-key).
- H2c-3 is the H4b sender / receiver switch of §6.4 step 7 (H4b merged first, so H2c owns it — §0.9).
- H2d-1 follows H2c-3 (ordinal 6 → 7); H2d-2 and H2d-3 follow.
- Release: H2c-2 and H2c-3 share one bump PR (no released build has the look store as SOT while the transfer still
  writes looks to `HostConfig`, and New Tab labels follow a synced rename from the same release — §0.9, §0.15).

## 0. Where the spec and the code disagree (found while measuring)

Status per item: **DECIDED** (by the review, adopted here), **RECOMMENDED** (evidence confirmed by the review; the
plan is written for it; still to be confirmed by the coordinator), **NEEDS DECISION** (options side by side; the
plan marks the affected tasks per option), or plain (a measurement / plan choice).

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
   - **dead:** `components/SessionPanel.tsx` — only `SessionPanel.test.tsx` imports it (item 2).
   Plan: every file above is in H2a / H2b-1 / H2b-2 (colour/icon in H2a, names split by area).
2. **`SessionPanel` is dead code** (spec §4.2 name surface, §4.5 filter surface). No production import; last touched
   by P-D.3. **RECOMMENDED** — delete `SessionPanel.tsx` + `SessionPanel.test.tsx` in H2b-1 (then it is neither
   migrated nor filtered). Alternative: migrate its name read (H2b-1) and add the filter (H2d-2), +1 file each.
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
   cannot occur. (Plan choice, not contested by the review.)
5. **Field-by-field fallback makes "no colour" impossible (§4.2 "looks[wireId] field by field, else HostConfig").**
   After H2c every write goes to the look store and `HostConfig` keeps its old colour forever; clearing the colour in
   the look store (a field removed) would reveal that old colour again. **NEEDS DECISION** — two options:
   - **Option A — group fallback** (the rev-1 recommendation). Once `looks[key]` exists, the colour group (`colors` +
     legacy `color`) and the icon group (`icon` + `iconWeight`) come from the entry only (absent = none / default);
     `name` still falls back to `HostConfig.name` field by field. No entry → every field from `HostConfig`.
     - projection / ordinal 6: `purdex-host-looks.looks` as is; the entry domain is the five optional fields, no null.
     - migration: copies the host's present fields (unchanged).
     - selector: group rule (H2c-2 T1).
     - clearing: removes the group's keys from the entry (a writer seeds an absent entry from `HostConfig` first, so the
       first edit of one field does not drop the others).
     - tests: "entry without colour → no colour although `HostConfig` has one"; "entry without name → `HostConfig.name`";
       mutation "field-by-field colour fallback" red.
     - cost: an entry created by a name-only write (e.g. a rename of a host with no entry) is seeded with the colour
       and icon too, so nothing is lost; but a remote entry that only carries a name (an older client of THIS build
       never writes one — the seed rule) would read as "no colour" rather than fall back.
   - **Option B — field-by-field + tombstones** (codex). Keep the spec's per-field fallback; a cleared field is an
     explicit `null` in the entry: `colors: null` (and the legacy `color` key removed) = "no colour, do not fall back";
     `icon: null` (and `iconWeight` removed) = "default icon, do not fall back". `undefined` / absent = fall back.
     - projection / ordinal 6: path unchanged, but the entry DOMAIN gains `null` for `colors` and `icon` — it must be
       defined in H2c-1 (store `merge`, the sanitiser, the H2c-1 wire tests) so ordinal 6 covers it (no later bump).
       `applySettings`' shape check compares only the top-level `looks` record, so `null` inside entries is not
       rejected — tested.
     - migration: unchanged (copies present fields, never writes `null`).
     - selector: per field — `undefined` → `HostConfig`; `null` → none / default; a value → it.
     - clearing: writes the tombstone (the "No color" button → `colors: null`, drop `color`; icon reset →
       `icon: null`, drop `iconWeight`); setting a value replaces the tombstone.
     - transfer: the sender resolves tombstones before sending (a `null` never enters a payload row — the H4b row
       parser would drop it anyway); the receiver's look entry may be written with the resolved fields only.
     - tests: tombstone round-trips apply → build byte-for-byte; `null` survives `merge`; "cleared colour does not
       fall back"; "absent field falls back"; mutation "tombstone read as absent" red.
   Tasks written for both: H2c-1 T1 has an **[B only]** sanitiser case; H2c-2 T1 / T2 have **[A]** / **[B]** variants.
6. **`purdex-shown-hosts: { ids: wireId[] | null }` cannot travel through the settings apply (§4.1).** Two measured
   rules in `applier.ts`: (a) `applySettings` rejects the WHOLE `settings` payload when a patched field changes shape
   class, and `null` is its own class (`shapeOf`) — `null` ↔ array is `rejected-settings`; (b) a store the payload
   lacks is "not sent" and left alone, so encoding "all" as an absent field (the builder then emits no store key)
   could never clear another device's list — that device would push its list back. **RECOMMENDED** —
   `{ all: boolean; ids: string[] }`, both always present (default `{ all: true, ids: [] }`); `all: true` is the
   spec's `null`. `ids` keeps unknown ids and order in both modes. (The look store has the same need and meets it:
   `looks` defaults to `{}` and is always built — a test pins it.)
7. **A host added while a workbench lists hosts is hidden there** (decision 4: unlisted = hidden). With `all: false`,
   adding mlab-2 through the dialog makes it invisible at once in this workbench. **NEEDS DECISION** — three options:
   - **(a) auto-add**: the three add paths (dialog, `registerLocalHost`, transfer create) append the new host's wire id
     to `ids`. Cost (codex): an implicit write to the workbench-wide synced setting, and the wire id reaches devices
     that do not have the host — decision 4 read literally says unlisted = hidden, so this needs the user's explicit
     consent.
   - **(b) stay hidden**: nothing on add; the Settings › 工作台 editor shows the host unticked.
   - **(c) prompt**: nothing written on add; the add-host dialog (and the transfer's done step) shows "This host is
     hidden in the current workbench — show it?" with a button; only the button writes (`ids` += the wire id).
     `registerLocalHost` (installer, no dialog) writes nothing.
   Files per option (H2d-2): (a) `AddHostDialog.tsx`, `AddHostDialog.test.tsx`, `useHostStore.ts`, `useHostStore.test.ts`
   (+ transfer create); (b) none; (c) `AddHostDialog.tsx`, `AddHostDialog.test.tsx`, `ReceiveHostsDialog.tsx`,
   `ReceiveHostsDialog.test.tsx`, locales. The H2d-2 list below carries the union and is re-trimmed once decided.
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
11. **What H2c-2 / H2d-1 need from H1b (not merged).** H1b's pass (branch at `70851837`,
    `spa/src/lib/host-reresolve.ts`): `runHostReresolve()` → `wireResolverOf` (`null` → `'conflict'`, nothing written)
    → acquires the operation lock (owner `host-reresolve`) → `rewriteHostRefs(map)` (synchronous) → releases;
    `requestHostReresolve()` retries `busy` every 500 ms. Its trigger / hydration wiring (`startHostReresolve`, H1b T4)
    and integration test (T6) are not committed yet. The contract H2c-2 / H2d-1 rely on:
    - (T3) `runHostReresolve` stays the place where the conflict gate and the operation-lock grant are taken, and its
      body stays synchronous — H2c-2 adds `rekeyWireKeyedStores(hosts)` AFTER `rewriteHostRefs(map)` in that body;
    - (H1c T1) `rewriteHostRefs(map)` stays the explicit-map core that deletion reuses — the look re-key must NOT go
      there, or a deletion (local → wire direction) would move looks (decision 9: deletion touches no look);
    - (T4) the pass runs on every `hostResolverSignature` change (daemonId learned is one — the identity pairs move)
      and after hydration of the stores it lists; H2c-2 adds `useHostLookStore`, H2d-1 `useShownHostsStore` to that
      list and to the `onFinishHydration` re-requests (H1b's M9 rule);
    - (T6) `host-reresolve.integration.test.ts` exists with real builders and hashes over all sections; H2c-2 and
      H2d-1 extend it.
    If H1b lands with a different shape, H2c-2 T5 / H2d-1 T4 adapt; the four properties are the contract.
12. **Re-key is the opposite direction of the pass's map.** The pass maps non-local ids → local ids; the look /
    shown-hosts stores are keyed by WIRE id and move local id → `d1_…` (§4.3). Plan: a separate step computing moves
    `[host.id → syncIdOfSync(host.daemonId)]` for every local host with a valid daemonId and `host.id !== d1`, applied
    per store (existing `d1_…` wins, the local-id entry is dropped; shown ids: replaced in place, deduped keeping the
    first). A daemonId CLEARED by a re-point moves nothing back: the `d1_…` entry still names that daemon; the host
    reads its `HostConfig` fallback until the id is re-learned (normally the next connect).
13. **The H1b no-push invariant gets its spec exception.** H1b T6 asserts every section hash is unchanged by the pass.
    With H2c-2 a pass that re-keys a look (and with H2d-1 a shown id) changes `settings` — one push, as spec §4.3 says.
    H2c-2 T5 and H2d-1 T4 extend H1b's integration test: a host arriving with a daemonId (no local-id entry) → all
    sections identical (H1b's case, still true); a local-id entry + daemonId learned → only `settings` differs, once.
14. **The guard test cannot be a grep, and the type-checker guard has known blind spots.** `.name` is read off
    hundreds of non-host objects (`entry.name`, `session.name`, `file.name`, `slaves[id].name`…). Plan: a vitest file
    running the TypeScript checker over `tsconfig.app.json` (scratch run: 545 files, ~4 s). It lands in H2a for the
    colour / icon fields (the spec puts the guard in H2b) so H2a's refactor has a test that turns red; H2b adds
    `name`. **DECIDED** (review item 8) — what it counts and what it cannot see:
    - counted as a READ: `x.f` / `x?.f` and `x['f']` (string-literal element access) where the object's non-nullable
      type is, or has in a union / intersection, the `HostConfig` interface of `stores/useHostStore.ts` (a type alias
      of it resolves to the same symbol and is covered); object-binding destructuring of `f` from such an initializer;
      parameter destructuring `({ f }: HostConfig)` (the binding's declared / contextual type).
    - NOT a read: the left side of an assignment (`next.icon = icon`), a `delete x.f`, and an object-literal property
      (`{ name: … }`) — writes are checked by the writer tests, not the guard.
    - allowlist: per file AND per field an EXACT hit count (e.g. `stores/useHostStore.ts: colors 2, color 1, icon 1,
      iconWeight 1` — the destructure at :501 counts for `icon` / `iconWeight`); a count going up or down fails and
      names the file:line list. Measured counts are recorded when H2a T3 first runs (not guessed here).
    - **known false negatives** (listed in the test's header): computed access with a non-literal key (`h[f]`, e.g.
      `host-transfer-plan.ts` look loop); a value first narrowed or copied into another type (`const { name } = host as
      { name: string }`, `Pick<HostConfig, 'name'>`, `Partial<…>`, a spread `{ ...host }` then read); reads inside
      `.d.ts` or `any`-typed code; reads in test files (excluded by design). The fixture tests pin each so a future
      change of the detector is visible.
15. **Sizes.** H2b is 26 surface files (§0.1) → H2b-1 / H2b-2. H2c is ≈36 files → H2c-1 (wire) / H2c-2 (switch) /
    H2c-3 (transfer + New Tab labels). H2d is ≈33 → H2d-1 (store, wire, re-key) / H2d-2 (editor, filters) / H2d-3
    (not-filtered behaviour tests only). Ordinals: `settings` is **5** today (`projections.ts:107`); H2c-1 → **6** with
    `@wire:host-look=1`; H2d-1 → **7** with `@wire:shown-hosts=1` (`WIRE_MARKERS.settings`, `projections.ts:172`).
16. **Migration runs after hydration and before Profile Sync starts.** **DECIDED** (review item 1, critical). The
    rev-1 "defer to `onFinishHydration` without delaying `startProfileSync`" branch is removed: it let the first
    `settings` build run before the migration whenever hydration is not synchronous. Boot now does, in `main.tsx`:
    `bootHostLooks()` = await `useHostStore` AND `useHostLookStore` hydrated (`persist.hasHydrated()` or their
    `onFinishHydration`) → take the host snapshot (`useHostStore.getState().hosts` — the only input `wireIdOfHost`, and
    the identity, need) → `migrateHostLooksOnce()` → THEN `startProfileSync()`. Every other starter keeps its place;
    only `startProfileSync()` moves behind the gate. With today's synchronous `localStorage` the gate resolves in a
    microtask. Test (`spa/src/main.test.tsx`, H2c-2 T3): the two stores' hydration is held back (a deferred storage /
    manual `onFinishHydration`), and `startProfileSync` (mocked) is not called — so no collector exists and no
    `settings` build or report can happen — until hydration finishes AND the migration has returned; plus a real-store
    test in `host-look-migration.test.ts` with an async storage.
17. **"Settings › 工作台" is `ProfileSection`, and settings stores are device-global** (spec §2). The shown-hosts setting
    therefore applies to whatever workbench is on screen (a local slave too) and syncs with the attached master. The
    editor block (`ShownHostsBlock`, H2d-2) says "synced with the attached workbench". `ProfileSection`'s header rule
    "opening this page writes nothing" (and the iron rule, `start.ironrule.test.ts`) extends to the block — tested.
18. **Test isolation.** The look store is module state; tests that write a colour and later expect none for the same
    host id would leak inside one file. `useHostStore.reset()` has no production caller and 29 test files use it —
    H2c-2 makes it also reset `useHostLookStore`; H2d-2 adds `useShownHostsStore`.
19. **"Hosts added later" seeding (§4.3) goes to the add paths, not `addHost`.** `addHost` is also the undo of a
    deletion (`host-lifecycle.ts:359`), which must not create entries. Plan: the add-host dialog (`AddHostDialog.tsx:187`)
    and `registerLocalHost` (`useHostStore.ts:515`) seed `looks[wireIdOfHost(host)]` from the new `HostConfig` when
    absent; the transfer receiver does it per item 9.
20. **The transfer commit cannot be all-or-nothing across two stores (§6.4 step 5, §7 H4b "look store identical").**
    **DECIDED** (review item 2, critical) — option (a). Rev 1 wrote the host store, then the look store, and restored
    both on a throw; during that window subscribers see the half state, persist may already have written it, and the
    restore can itself fail. Spec §6.4.5's "one `set()`" is about the HOST store; the look belongs to the workbench
    layer. So the commit is two steps, each valid alone:
    1. the host step — `applyHostTransfer`'s single `set()` exactly as H4b (a throw or `stale` → host store untouched,
       and step 2 does not run);
    2. the look step — ONE `useHostLookStore.setState` writing `{ name, ...look }` only for keys with no entry
       (skip-if-present). If it throws, the hosts stay added; the result says `looks: 'failed'` and the dialog tells the
       user ("Hosts added; their names / colours from the code could not be saved.") with a **Retry** that re-runs step
       2 alone — skip-if-present makes the retry idempotent and it never overwrites a look that arrived meanwhile.
    Between the steps a subscriber sees the new hosts with their `HostConfig` fallback (the received name) — a valid
    state. Invariants and tests: H2c-3 T2.

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
that `useHostStore` or the look store imports may import `host-look.ts` (a test in H2c-1 imports the look store alone
and asserts `useHostStore` is not loaded).

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
- **T3 — the guard (colour / icon)** per §0.14. `// @vitest-environment node`, timeout 60 s. Program from
  `spa/tsconfig.app.json` (`ts.readConfigFile` + `parseJsonConfigFileContent`); scan `spa/src` excluding `*.test.*`,
  `__tests__/`, `test-setup.ts`, `test-utils*`. Checked fields in H2a: `color`, `colors`, `icon`, `iconWeight`.
  Allowlist with exact per-file-per-field counts: `stores/useHostStore.ts`, `lib/host-color.ts` (sanitiser),
  `lib/host-look.ts`. The detector is an exported function of a small helper inside the test file, run twice:
  - **fixture half** (in-memory program via a `CompilerHost` over virtual files that import the real `HostConfig`
    type): one snippet per rule — flagged: property read, optional-chain read, `h['icon']`, `hosts[id]?.colors`,
    object destructure, parameter destructure `({ icon }: HostConfig)`, alias `type H = HostConfig` then `h.icon`;
    NOT flagged: `next.icon = x` (assignment left side), `delete next.icon`, `{ icon: x }` literal; pinned known
    false negatives (asserted NOT flagged, commented as blind spots): `h[f]` with a variable `f`,
    `(h as { icon?: string }).icon`, `const p: Pick<HostConfig, 'icon'> = h; p.icon`, `({ ...h }).icon`.
  - **repo half**: the real program; fails with the `file:line field` list for any hit outside the allowlist and
    for any allowlisted count that differs.
  It fails first (the four surfaces still read directly). Commit.
- **T4 — move the four surfaces.** `useTabHostBadge` (`useHostLook(hostId)` → `resolveHostColors({colors, color})`,
  memo on the look object), `HostBadgePreview` (name + look), `HostColorField` (`colors` / `color` from the look),
  `HostIconField` (`icon` / `iconWeight`). Existing tests stay green unchanged; add to `useTabHostBadge.test.ts`: a
  tab whose pane names an unresolvable `d1_…` → `colors: null`, default icon, no throw. The guard turns green; record
  the measured allowlist counts. Commit.

Invariants: behaviour identical (every existing test of the four surfaces unchanged and green); no direct colour /
icon read of `HostConfig` outside the allowlist, at exactly the allowlisted counts.

Mutations: M1 `useTabHostBadge` reads `s.hosts[hostId]?.colors` again (repo half red); M2 detector ignores union
types (`hosts[id]?.colors` fixture red); M3 detector skips destructuring (object- / parameter-destructure fixtures and
the `useHostStore.ts` icon count red); M4 detector counts assignment left sides (fixture + `useHostStore.ts` count
red); M5 detector ignores string-literal element access (fixture red); M6 `wireIdOfHost` uses `toWire` (conflict
test red); M7 `hostLookOf` without `Object.hasOwn` (`__proto__` test red).

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
  comment "emptied by H2b-2") plus `lib/host-transfer-plan.ts` (`name` 1 — the share payload, until H2c-3), all with
  exact counts. Red on files 1–13. Commit.
- **T2 — Hosts pages** (files 1–8): `useHostLook` for one host, `useHostLookResolver` in lists / pickers
  (`HostSidebar`, `ShareHostsDialog`, `ReceiveHostsDialog`, `PeersSection.toApp` — a callback reading
  `hostLookOf(id, hs)` from the `getState()` it already takes). `hostLabel(ref, look)` where the code falls back to
  the id today. Existing tests green. Commit.
- **T3 — New Tab and sessions** (files 9–13): providers build `labelParams.host` from
  `hostLabel(hostId, hostLookOf(hostId, hosts))`; `SessionSection` / `SessionPickerList` / `EditorNewTabSection`
  through the hooks. Existing provider tests green (label still equals the host name). Commit.
- **T4 — delete `SessionPanel`** (§0.2 RECOMMENDED; if the coordinator keeps it, migrate its :78 read here instead).
  `pnpm run build` and lint prove nothing imports it. Commit.

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
H2c-3) `host-transfer-plan.ts`, at exactly the allowlisted counts.

Mutations: M1 revert `useNotificationDispatcher.ts:305` (guard red); M2 revert `StopSyncControl.tsx:136` (guard red —
proves the missed files are covered); M3 put `StatusBar.tsx` back into the allowlist and revert it (the allowlist
content is snapshotted in the test → red).

## H2c-1 — the look store and its wire (12 files; no H1b dependency)

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
`HostLookEntry = { name?: string; colors?; color?; icon?; iconWeight? }` (**[B]**: `colors?: … | null`,
`icon?: string | null`). `merge` sanitises every entry (the `sanitizeHostConfig` validators from `host-color.ts`;
unknown fields dropped; a non-string name dropped; **[B]** `null` kept for `colors` / `icon` only); keys kept verbatim,
`__proto__` never copied. Actions: `putLook(key, entry)`, `patchLook(key, fn)`, `putLooksIfAbsent(entries)` (the
transfer's step 2, H2c-3), `rekey(moves)` (H2c-2). Keys are opaque strings; the module imports no store.

Wire (`projections.ts`):
- `PROJECTIONS.settings` += `...settingsPaths('purdex-host-looks', ['looks'])` (the whole record: keys are wire ids
  already; entries sanitised by the store).
- `SECTION_SCHEMA_ORDINAL.settings`: **5 → 6**, comment `// 6: purdex-host-looks.looks (host looks keyed by wire id;
  host ownership H2c)` (**[B]**: "…; `null` = cleared").
- `WIRE_MARKERS.settings`: `['@wire:host-id=d1', '@wire:host-look=1']`.
- `buildSettingsSection` and `settingsFromWire` are NOT changed: no local↔wire mapping for this key (spec §4.1) — a
  test proves the build passes keys verbatim WITH an identity handed in.

Tasks:
- **T1 — the store.** Tests: default `{looks: {}}`; `merge` drops an invalid colour / icon / weight / unknown field
  and keeps unknown keys (`d1_zzz`, a legacy local id); `__proto__` key ignored; `putLook` / `patchLook` /
  `putLooksIfAbsent` (existing key untouched) / `rekey` (existing target wins, source dropped; missing source no-op);
  **[B only]** `colors: null` / `icon: null` survive `merge`, `null` on any other field is dropped; registered with
  `syncManager`; importing the module does not load `useHostStore` (module registry check). Commit.
- **T2 — projection, ordinal, marker, guard snapshot, old-client lock.** Tests (`projections.test.ts`): the guard
  snapshot updated (settings fingerprint new, ordinal 6) in the same commit, and `WIRE_MARKERS.settings` pinned as
  `['@wire:host-id=d1', '@wire:host-look=1']`; a **real lock regression** in the style of `projections.test.ts:473`:
  the ordinal-5 client's shapes (`settings` = `PROJECTIONS.settings` minus `purdex-host-looks.looks` + marker
  `['@wire:host-id=d1']`, ordinal 5; the other kinds as today) and a SOT index holding ONLY a `settings` row written by
  this build → `profileLock(index, oldShapes)` matches `{ section: 'settings', kind: 'settings', verdict:
  'sot-is-newer' }` (= `locked:schema`); and this build meets the old row as `i-am-newer`. No projection path starts
  with `purdex-host-looks-migrated`. Commit.
- **T3 — collector and apply maps.** Tests: `collector.test.ts` nine keys, a look write re-schedules `settings`;
  `buildSettingsSection` with an EMPTY look store emits `'purdex-host-looks': { looks: {} }` (always present — §0.6);
  keys `d1_a`, `localX`, `d1_unknown` pass through verbatim with an identity whose `toWire` maps `localX` → `d1_x`
  (no mapping). `applier.test.ts`: `isWellFormedSection('settings', …)` accepts the store, refuses an unlisted field
  inside it; `applySettings` replaces `looks` whole (an entry the payload lacks is dropped locally — replace
  semantics, like host-settings); **[B]** an entry with `colors: null` is not `rejected-settings`.
  `apply-to-stores.test.ts`: a `settings` payload with `looks: {d1_unknown: …, d1_a: …}` → store holds both
  byte-for-byte, the returned hash equals the payload's (nothing pushed); an ordinal-5 payload without the store
  leaves the look store untouched and the rebuilt hash differs (the new build is pushed — expected once). Commit.
- **T4 — §4.4 guarantees.** Tests (`apply-to-stores.test.ts`, `sections.test.ts`): a `hosts` apply that renames and
  recolours a host leaves `useHostLookStore.getState()` the SAME object AND a `useHostLookStore.subscribe` spy
  registered before the apply is never called; `buildHostsSection` output is identical whatever the look store holds,
  and building it (and the collector's `hosts` report) calls no look-store subscriber (spy); `buildSettingsSection`
  output does not change when a `HostConfig` name / colour changes. Commit.

Invariants: `settings` round-trips any `looks` payload byte-for-byte (apply → build); `hosts` build / apply never
read, write or notify the look store; the look store is always present in the build; an ordinal-5 client locks.

Mutations: M1 ordinal left at 5 with the new path (guard snapshot red); M2 marker missing (marker-array pin and
lock regression red); M3 the builder maps look keys through `toWire` (verbatim test red); M4 the apply filters look
keys to local hosts (unknown-id test red); M5 `hosts` apply writes / touches the look store (subscriber spy red);
M6 look store default without `looks` (always-present test red).

## H2c-2 — the look store becomes the SOT (17 files; needs H1b)

Files:
1. `spa/src/lib/host-look.ts` (reads the look store; §0.5 fallback per option; `rekeyWireKeyedStores`)
2. `spa/src/lib/host-look.test.ts`
3. `spa/src/lib/host-look-migration.ts` (new)
4. `spa/src/lib/host-look-migration.test.ts` (new)
5. `spa/src/main.tsx` (hydration gate → migration → `startProfileSync()`, §0.16)
6. `spa/src/main.test.tsx` (boot order under delayed hydration)
7. `spa/src/stores/useHostStore.ts` (writers rerouted, `setHostName`, `registerLocalHost` seed, `reset` clears looks)
8. `spa/src/stores/useHostStore.test.ts`
9. `spa/src/components/hosts/OverviewSection.tsx` (:146 rename → `setHostName`)
10. `spa/src/components/hosts/OverviewSection.test.tsx`
11. `spa/src/components/hosts/AddHostDialog.tsx` (seed after add, §0.19)
12. `spa/src/components/hosts/AddHostDialog.test.tsx`
13. `spa/src/lib/host-reresolve.ts` (from H1b: re-key step + hydration list)
14. `spa/src/lib/host-reresolve.test.ts`
15. `spa/src/lib/host-reresolve.integration.test.ts` (from H1b: §0.13)
16. `spa/src/lib/host-look.guard.test.ts` (allowlist += `lib/host-look-migration.ts`, exact counts)
17. `spa/src/components/SessionPaneContent.test.tsx` (missing-host pane shows the look name)

(The New Tab provider `subscribe` moved to H2c-3 — review item 3.)

Selector after H2c-2: `hostLookOf(ref, hosts = …, looks = useHostLookStore.getState().looks)`: local host → key
`wireIdOfHost(host)`, entry `looks[key]` → **[A]** group rule / **[B]** per-field with tombstones (§0.5); not local →
`looks[ref]` fields (`{}` when absent). `useHostLook` subscribes to the host object AND `s.looks[key]` (key recomputed
from the host); `useHostLookResolver` to `s.hosts` and `s.looks`.

Writers (`useHostStore.ts`), all: unknown host → no-op; key = `wireIdOfHost(host)`; base = `looks[key]` ?? seed from
`HostConfig` (`name`, `colors`, `color`, `icon`, `iconWeight` present); apply the SAME validation / legacy-`color`
rules as today (the body of `setHostColorLayer` :451–492 and `setHostIcon` :496–511 becomes a pure function over a
look object, kept in this file); clearing per §0.5 (**[A]** remove the group's keys / **[B]** write the tombstone);
nothing changed → no write; `HostConfig` is never touched. `setHostColor` reads the current alpha from `hostLookOf`.
New `setHostName(hostId, name)`: trimmed, blank → no-op. `registerLocalHost` seeds when it CREATES a host. `reset()`
also resets the look store.

Migration (`host-look-migration.ts`) `migrateHostLooksOnce(hosts): 'already' | 'migrated'`: marker
`localStorage[HOST_LOOKS_MIGRATED] === '1'` → `'already'` (read in try/catch; a throw reads as "not migrated").
Else, in `hostOrder` order then remaining hosts: key = `wireIdOfHost(host)`; `looks[key]` present → skip; else the
seed entry (present fields only, never `null`). ONE `setState`. Then write the marker (try/catch; a failed write only
means a harmless re-run next boot). `bootHostLooks()` (same module) is the §0.16 gate: resolves after both stores
hydrated and the migration returned.

Tasks:
- **T1 — selector reads the look store** (§0.5). Tests common to both options: entry name → shown; entry without
  name → `HostConfig.name`; no entry → all from `HostConfig`; an unresolvable `d1_…` ref → its entry; a host WITHOUT
  daemonId → the entry under its local id; two rows claiming one daemon (conflict) → both read the `d1_…` entry; the
  hook re-renders on a write to its own entry only. **[A]**: entry without colour → `colors`/`color` absent even
  though `HostConfig` has one. **[B]**: entry field absent → `HostConfig` value; `colors: null` → no colour although
  `HostConfig` has one; `icon: null` → default icon. Commit.
- **T2 — writers.** Tests (`useHostStore.test.ts`, the existing colour / icon cases rewritten to read the look
  store): every write lands in `looks[wireIdOfHost(host)]` and leaves `hosts` the same object; first write seeds from
  `HostConfig` (set icon → the old colour is still there); "No color" on a legacy-`color` host clears it (**[A]** keys
  removed / **[B]** `colors: null`, no `color`); icon reset (**[A]** keys removed / **[B]** `icon: null`); validation
  no-ops unchanged; `setHostName`; `registerLocalHost` creating a host seeds, re-registering does not; `reset()` clears
  looks. `OverviewSection.test.tsx`: rename → look store, `HostConfig.name` unchanged. `AddHostDialog.test.tsx`: add →
  entry under the new local id with the typed name; an existing `d1_…` entry is never overwritten by a later add of
  that daemon (seed only when absent). Commit.
- **T3 — migration and boot order** (§0.16). Tests (`host-look-migration.test.ts`): keyed by the CURRENT wire id
  (`d1_…` with a daemonId, local id without); an existing entry is never overwritten; marker set → nothing runs (a
  reset look stays reset); marker read throwing → runs; hosts under conflict both map to one key, one entry; the
  marker key is not in `PROJECTIONS` and not registered with `syncManager`; with an ASYNC storage for both stores,
  `bootHostLooks()` does not migrate before both have hydrated and migrates the hydrated hosts (not the defaults).
  `main.test.tsx` (existing boot test file, same mocks): hydration of `useHostStore` / `useHostLookStore` held back
  (deferred storage, or `hasHydrated` false + a captured `onFinishHydration` fired by the test) → after `import
  './main'` and flushing microtasks, `startProfileSync` (mocked) has NOT been called and the migration has not run;
  fire hydration for ONE store → still not called; fire the second → the migration runs, then `startProfileSync` is
  called exactly once, after it (call-order spy). Since the collector is created only inside `startProfileSync`, this
  proves no `settings` build / report precedes the migration. Commit.
- **T4 — missing-host pane shows the look name.** Test (`SessionPaneContent.test.tsx`): a pane on unresolvable
  `d1_x` with `looks.d1_x.name = 'air26'` → "This device has no host air26"; without an entry → the id. Commit.
- **T5 — re-key in the pass** (§0.11, §0.12, §0.13). `rekeyWireKeyedStores(hosts)` (in `host-look.ts`, pure moves +
  `useHostLookStore.rekey`), called in `runHostReresolve` after `rewriteHostRefs(map)`, under the same grant; look
  store added to the pass's hydration wait and `onFinishHydration` triggers. Tests (`host-reresolve.test.ts`): a
  local-id entry moves to `d1_…` when the daemonId is learned; an existing `d1_…` entry wins and the local-id entry is
  dropped; conflict → nothing moves; `rewriteHostRefs({[localId]: d1})` called directly (the deletion direction H1c
  uses, merged or not) moves no look; idempotent. Integration (`host-reresolve.integration.test.ts`, real collector
  builders and hashes over every section): host added with its daemonId, no local-id entry → every section hash
  identical (H1b's invariant kept); local-id entry + daemonId learned → only `settings` differs, and a second pass
  changes nothing (one push). Commit.

Invariants: UI reads only through the selector, UI writes only to the look store (§4.4); `HostConfig` look fields are
written only by add paths (name) and — until H2c-3 — the transfer receiver; the migration never overwrites, never runs
twice, and always runs after hydration and before `startProfileSync`; deletion never touches the look store.

Mutations: M1 **[A]** field-by-field colour fallback / **[B]** tombstone read as absent (T1 red); M2 a writer also
writes `HostConfig` (`hosts`-same-object test red); M3 no seeding (icon-keeps-colour test red); M4 migration
overwrites an existing entry; M5 migration ignores the marker; M6 migration keyed by local id always (daemonId case
red); M7 `startProfileSync()` called before `bootHostLooks()` resolves (`main.test.tsx` red); M8 the gate waits for
one store only (single-store step red); M9 re-key inside `rewriteHostRefs` (deletion-moves-no-look test red); M10
re-key where the local-id entry wins; M11 look store missing from the pass's hydration wait.

## H2c-3 — transfer through the look store, New Tab labels follow (15 files)

Files:
1. `spa/src/stores/useHostStore.ts` (`applyHostTransfer` / `transferPatch`, `applyTransferLooks`)
2. `spa/src/stores/useHostStore.test.ts` (flip the cases at l.760, l.772, l.808; the two-step commit)
3. `spa/src/lib/host-transfer-plan.ts` (`payloadRowsOf(hosts, lookOf)`; `transferLookEntries(change)`)
4. `spa/src/lib/host-transfer-plan.test.ts`
5. `spa/src/components/hosts/ShareHostsDialog.tsx` (:72 passes `hostLookOf`)
6. `spa/src/components/hosts/ShareHostsDialog.test.tsx`
7. `spa/src/components/hosts/ReceiveHostsDialog.tsx` (`looks: 'failed'` notice + Retry)
8. `spa/src/components/hosts/ReceiveHostsDialog.test.tsx`
9. `spa/src/locales/en.json` (`hosts.transfer.looks_failed`, `hosts.transfer.looks_retry`)
10. `spa/src/locales/zh-TW.json`
11. `spa/src/lib/host-look.guard.test.ts` (allowlist − `lib/host-transfer-plan.ts`)
12. `spa/src/lib/session-new-tab-providers.tsx` (`subscribe` also on the look store)
13. `spa/src/lib/session-new-tab-providers.test.ts`
14. `spa/src/lib/headless-new-tab-providers.tsx`
15. `spa/src/lib/headless-new-tab-providers.test.ts`

Tasks:
- **T1 — the sender sends the workbench look.** Tests: `payloadRowsOf` takes `lookOf`; name and look come from it
  (an entry that differs from `HostConfig` wins; a host without entry sends its `HostConfig` look; **[B]** a tombstone
  is resolved — the field is omitted, never `null`); the guard is green without the allowlist entry. Commit.
- **T2 — the receiver: two steps** (§0.20, spec §6.4 step 5 / 7). Step 1: `transferPatch` stops spreading `o.look` /
  `c.look` into `HostConfig` — created rows get `name`; overwritten rows get `name`, `ip`, `port`, `token` (look
  fields untouched); `applyHostTransfer`'s single host `set()` is otherwise unchanged. Step 2:
  `applyTransferLooks(entries)` = `useHostLookStore.putLooksIfAbsent(entries)` with `entries` from
  `transferLookEntries(change)` (pure: key `syncIdOfSync(daemonId)` per created / overwritten row, value
  `{ name, ...look }`, a row without a look still gives `{ name }`); called only after step 1 returned `applied`;
  its throw is caught → `TransferApplyResult` `{ kind: 'applied', …, looks: 'ok' | 'failed' }`.
  Tests:
  - created → `HostConfig` has the name and no look field; the look entry holds name + look;
  - an existing `d1_…` entry is not changed (workbench wins); overwrite → `HostConfig.name` replaced, look store
    unchanged when an entry exists, written when none;
  - step 1 `stale` / throw → host store AND look store untouched (step 2 never ran — spy);
  - step 2 throws (spy on `putLooksIfAbsent`) → hosts committed exactly as a success would, look store unchanged,
    `looks: 'failed'`; a look-store subscriber saw no call; calling `applyTransferLooks` again (the Retry) writes the
    entries; a Retry after another device's look arrived for that key leaves it untouched;
  - `ReceiveHostsDialog.test.tsx`: confirm → the look store holds the entry; with step 2 failing → the notice
    (both locales) and a Retry button that, clicked, writes the entry and removes the notice.
  Commit.
- **T3 — New Tab labels follow a synced rename** (moved from rev-1 H2c-2 — review item 3). Tests: provider
  `subscribe` fires on a look-store write to that host's entry; `getProviders()` label then carries the new name; an
  apply of a `settings` payload whose `looks` renames the host's `d1_…` entry → the listener fires and the label
  changes (through `applySectionToStores`). Commit.

Invariants: after a transfer `HostConfig` holds no look fields it did not hold before; a workbench look is never
overwritten by a received one; the host step is all-or-nothing (one `set()`); the look step is idempotent and its
failure leaves a valid, user-visible, retryable state.

Mutations: M1 keep spreading `look` into `HostConfig`; M2 overwrite an existing entry; M3 step 2 runs after a
`stale` step 1; M4 a step-2 throw propagates / reports `stale` (the committed-hosts test red); M5 `payloadRowsOf`
reads `HostConfig` again (guard red); M6 provider `subscribe` without the look store (T3 red).

## H2d-1 — shown hosts: store, wire, re-key (15 files; needs H1b)

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
15. `spa/src/lib/host-reresolve.integration.test.ts` (re-key of a shown id: only `settings` pushed, once)

Store (per §0.6, RECOMMENDED): `{ all: boolean; ids: string[] }`, default `{ all: true, ids: [] }`, persisted
(`purdexStorage`, `syncManager`), `merge` keeps strings only, dedupes keeping the first, keeps unknown ids and order.
Actions: `showAll()`, `setShown(ids)` (`all: false`), `toggle(wireId, knownWireIds)` (from `all: true` → `ids` = every
known wire id except that one, unknown ids kept), `addShown(wireId)` (used by §0.7 (a) or (c)), `rekey(moves)`.
Wire: `PROJECTIONS.settings` += `...settingsPaths('purdex-shown-hosts', ['all', 'ids'])`; ordinal **6 → 7**
(comment `// 7: purdex-shown-hosts (all, ids: wire ids)`); `WIRE_MARKERS.settings` becomes
`['@wire:host-id=d1', '@wire:host-look=1', '@wire:shown-hosts=1']`.
Selector (`shown-hosts.ts`): `isHostShown(hostId, hosts, shown)` = `shown.all || shown.ids.includes(wireIdOfHost(hosts[hostId]))`
(an id that is not a local host → `true`: navigation to it is not this module's business); `useIsHostShown(hostId)`;
`useShownHostFilter(): (hostId) => boolean`.

Tasks:
- **T1 — store.** Tests: defaults; `merge`; `toggle` from all / from a list; `addShown` idempotent; unknown ids
  survive every action; `rekey` (`d1_…` already present → local id dropped, else replaced in place). The tests of
  this PR reset the store in their own `beforeEach`; the `useHostStore.reset()` hook of §0.18 is added by H2d-2.
  Commit.
- **T2 — wire and old-client lock.** Guard snapshot (ordinal 7) with the final marker array pinned exactly as
  `['@wire:host-id=d1', '@wire:host-look=1', '@wire:shown-hosts=1']`; a real `profileLock` regression (as in H2c-1 T2):
  the ordinal-6 client's `settings` shape (`PROJECTIONS.settings` minus `purdex-shown-hosts.all` / `.ids`, markers
  `['@wire:host-id=d1', '@wire:host-look=1']`, ordinal 6) and an index holding only this build's `settings` row →
  `verdict: 'sot-is-newer'` (`locked:schema`); the ordinal-5 client (H2c-1 T2's case) still locks. A payload
  `{all: true, ids: ['d1_unknown']}` round-trips apply → build byte-for-byte; `{all: false, ids: [...]}` ↔
  `{all: true, ids: [...]}` applies without `rejected-settings` (§0.6 — the reason for the shape); the store is always
  present in the build. Commit.
- **T3 — selector.** Tests: `all` → every host shown; list → by `wireIdOfHost` (a `d1_…` id shows the local host of
  that daemon; a no-daemonId host by local id); unknown host id → shown. Commit.
- **T4 — re-key in the pass.** Same shape as H2c-2 T5: local id → `d1_…` on daemonId learned; dedupe; conflict →
  nothing; `rewriteHostRefs` (deletion direction) moves nothing. Integration (`host-reresolve.integration.test.ts`,
  real collector builders and hashes): `ids` holding a host's local id + its daemonId learned → the pass changes only
  the `settings` hash (every other section identical), a second pass changes nothing; with both a look entry and a
  shown id on that local id, still exactly one `settings` change. Commit.

Invariants: unknown ids kept through apply, build, toggle and re-key; `all` ↔ list transitions always apply; old
clients (ordinal 5 and 6) lock.

Mutations: M1 `ids: string[] | null` store (T2 transition test red — `rejected-settings`); M2 `merge` drops ids that
are not local hosts; M3 ordinal not bumped (snapshot red); M4 marker missing (marker pin + lock regression red); M5
selector compares local ids (daemonId case red); M6 re-key keeps both forms (dedupe test red); M7 re-key runs in
`rewriteHostRefs` (deletion test red).

## H2d-2 — shown hosts: editor and filters (up to 20 files; trimmed by §0.7)

Files (files marked ⁷ exist only for a §0.7 option — (a) or (c); drop them for (b)):
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
11. `spa/src/stores/useHostStore.ts` (`reset` resets shown hosts — §0.18; ⁷(a) append in `registerLocalHost` and
    transfer create)
12. `spa/src/stores/useHostStore.test.ts`
13. `spa/src/locales/en.json`
14. `spa/src/locales/zh-TW.json`
15. `spa/src/components/hosts/AddHostDialog.tsx` ⁷ ((a) append / (c) prompt)
16. `spa/src/components/hosts/AddHostDialog.test.tsx` ⁷ (listed before the decision, per review item 5)
17. `spa/src/components/hosts/ReceiveHostsDialog.tsx` ⁷ ((c) prompt in the done step)
18. `spa/src/components/hosts/ReceiveHostsDialog.test.tsx` ⁷

Count: 14 for (b), 16 for (a) (+ transfer create in file 11), 18 for (c).

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
  `locale-completeness.test.ts` covers parity). `useHostStore.reset()` resets the store. Commit.
- **T2 — the three filters.** Tests: hidden host absent from `HostSidebar`, from `SessionPickerList`, and its
  `sessions:` / `headless:` blocks not rendered on the New Tab page while other columns render; the preset still holds
  the hidden host's columns afterwards; `all: true` shows all; an unknown id in `ids` hides nothing local. Commit.
- **T3 — a new host under a list** (§0.7, per the decision). (a): with `all: false`, the dialog add /
  `registerLocalHost` / transfer create call `addShown(wireId)`; with `all: true` nothing changes. (b): no code; a
  test pins that an add with `all: false` writes nothing to the shown store. (c): with `all: false`, the add-host
  dialog and the receive done step show the prompt; nothing is written until the button; the button calls
  `addShown`; `registerLocalHost` writes nothing. Commit.

Invariants: only the three navigation surfaces filter; the editor writes only on user action; §0.7's option is the
only writer on add.

Mutations: M1 `NewTabPage` removes the column from the preset instead of skipping it (preset-kept test red); M2 the
sidebar filters by local id (daemonId host test red); M3 the editor drops unknown ids on toggle (unknown-id test red);
M4 the block writes on mount (iron-rule test red); M5 (c) the dialog writes before the button (prompt test red).

## H2d-3 — hidden ≠ absent: behaviour tests for the not-filtered list (5 files, tests only)

Files (all new):
1. `spa/src/lib/shown-hosts.not-filtered.connection.test.tsx`
2. `spa/src/lib/shown-hosts.not-filtered.panes.test.tsx`
3. `spa/src/lib/shown-hosts.not-filtered.fallbacks.test.tsx`
4. `spa/src/lib/shown-hosts.not-filtered.newtab.test.tsx`
5. `spa/src/lib/shown-hosts.import-guard.test.ts`

Every case sets `{ all: false, ids: [<wire id of mlab>] }` so air26 (a local host with a daemonId) is HIDDEN, and
asserts air26 behaves exactly as with `{ all: true }` (each case is run under both settings via `it.each` and the two
results compared). One behaviour test per item of spec §4.5's not-filtered list (review item 4):

- **T1 — connections and health** (file 1): `useHostConnection` (health / reconnect state machine) runs for air26 and
  updates `runtime[air26].status`; `useMultiHostEventWs` opens the event WS for air26 (the `WebSocket` mock sees its
  URL) and processes its `sessions` event (attach gate opens); `useSessionWatch` / session refresh
  (`lib/rebuild/refresh-sessions.ts`) fetches air26's sessions. Commit.
- **T2 — panes, tabs, notifications** (file 2): a tab with an air26 tmux pane stays in the tab bar (`SortableTab` /
  `InlineTab` rendered) with its badge (`useTabHostBadge` non-null); `SessionPaneContent` for air26 renders the
  terminal path (not `MissingHostPane`, ticket fetched); an execution pane on air26 subscribes (not
  `host_removed`); `useNotificationDispatcher` dispatches an air26 agent notification. Commit.
- **T3 — fallbacks and direct navigation** (file 3): with air26 as `activeHostId` / `hostOrder[0]`:
  `nex/resolve-host.ts` returns air26 for a hostless id; the fs backends (`register-modules/fs-backends.tsx`)
  resolve air26; `backup-auto-trigger` targets air26; `HostPage` at `/hosts/<air26>/overview` renders
  `OverviewSection`; `DevEnvironmentSection` lists air26 in its picker. Commit.
- **T4 — New Tab registration and layout** (file 4): the provider sources still return air26's `sessions:` /
  `headless:` providers; `getStaleNewTabProviderIds` does not report their ids; `useNewTabBootstrap` run keeps the
  columns in every preset and in `knownIds`; the next `settings` build carries them unchanged. Commit.
- **T5 — import guard** (file 5, the static half): only `ShownHostsBlock.tsx`, `HostSidebar.tsx`,
  `SessionPickerList.tsx`, `NewTabPage.tsx`, `shown-hosts.ts`, `useHostStore.ts`, `host-reresolve.ts`,
  `collector.ts`, `apply-to-stores.ts` (and, per §0.7, `AddHostDialog.tsx` / `ReceiveHostsDialog.tsx`) may import
  `useShownHostsStore` / `lib/shown-hosts` (scan of import declarations in non-test files). Commit.

Invariant: every item of the not-filtered list has a behaviour test that is identical with the host hidden and shown.

Mutations (each a one-line change in production code, reverted after): M1 `useMultiHostEventWs` skips hidden hosts
(T1 red); M2 `useHostConnection` skips them (T1 red); M3 `useTabHostBadge` returns null for a hidden host (T2 red);
M4 `nex/resolve-host` skips hidden hosts in its `hostOrder[0]` fallback (T3 red); M5 the provider sources filter
hidden hosts (T4 red); M6 `getStaleNewTabProviderIds` reports a hidden host's column (T4 red); M7 import the store in
`useSessionWatch` (T5 red).

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

After H2c-3 (H2c-2 + H2c-3 released together):
1. Reload both. `settings.looks` holds exactly the two `d1_…` keys; both clients' `settings` synced, none locked.
2. On A change mlab's console colour → B's tab badge and sidebar badge follow without reload. The `hosts` rev is
   UNCHANGED (the colour no longer travels in `hosts`); `settings` moved once.
3. On A rename air26 → B's New Tab block label for air26 follows without reload; `hosts` rev unchanged.
4. On A clear mlab's colour → B shows no colour (§0.5, either option; B's `HostConfig` still holds the old one).
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

Cleanup: `playwright cli -s=host-ownership-a close`, `-s=host-ownership-b close`, `-s=host-ownership-c close` (same
cwd), delete W, stop :5175.

## Review

### 2026-09-24 — codex plan review `task-muei1qbr-0hu3ol` (rev 1 → rev 2)

1. [critical] Migration could run after the first build (defer branch) — adopted: hydration gate → migration →
   `startProfileSync()`; delayed-hydration tests in `main.test.tsx` and `host-look-migration.test.ts` (§0.16, H2c-2 T3).
2. [critical] Two-store "all or nothing" with reverse restore is not atomic — adopted option (a): host step one `set()`,
   look step skip-if-present and retryable, failure reported to the user (§0.20, H2c-3 T2).
3. [important] H2c-2 was 21+ files (`main.test.tsx` missing) — adopted: provider `subscribe` moved to H2c-3; H2c-2 17,
   H2c-3 15.
4. [important] Not-filtered list guarded only by an import scan — adopted: new test-only PR H2d-3, one behaviour test
   per item, import guard kept as the static half.
5. [important] `AddHostDialog.test.tsx` missing from H2d-2 — adopted: listed (marked ⁷, pending §0.7).
6. [important] H2d-1 re-key lacked the integration test — adopted: `host-reresolve.integration.test.ts` in H2d-1 T4.
7. [important] Old-client lock tested only through `compareShape` — adopted: real `profileLock` regressions for
   ordinal 6 (H2c-1 T2) and 7 (H2d-1 T2) asserting `sot-is-newer` / `locked:schema`.
8. [important] Guard coverage and allowlist granularity — adopted: read vs write context, literal element access,
   parameter destructure, alias; exact per-file-per-field counts; known false negatives listed and pinned (§0.14,
   H2a T3).
9. [minor] Dependency graph vs text — adopted: H2c-1 has no dependency; H2c-2 and H2d-1 need H1b T3 / T4 / T6.
10. [minor] H2c-1 T4 observer assertion — adopted: hosts apply / build notify no look-store subscriber.
11. [minor] Final marker array not pinned — adopted: H2d-1 T2 pins
    `['@wire:host-id=d1', '@wire:host-look=1', '@wire:shown-hosts=1']`.
12. §0.5 look fallback — pending (coordinator): option A group fallback vs option B field-by-field with tombstones,
    both written out; tasks carry [A] / [B] variants.
13. §0.7 new host under a list — pending (coordinator): (a) auto-add / (b) stay hidden / (c) prompt; H2d-2 files and
    T3 per option.
14. §0.2 delete `SessionPanel` and §0.6 `{ all, ids }` — confirmed by the review's evidence; RECOMMENDED, pending the
    coordinator's confirmation.
