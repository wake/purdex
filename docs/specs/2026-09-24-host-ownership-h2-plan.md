# Plan — host ownership H2 (H2a / H2b / H2c / H2d)

Status: **rev 4** (2026-09-24) — rev 2 revised after the codex plan review `task-muei1qbr-0hu3ol` (§Review); rev 3
records the coordinator's decisions below; rev 4 replaces the "hidden tabs" model of §0.21 by the user's "disabling
closes" model and re-plans H2d (H2d-2 … H2d-6), measured on this worktree at `4f8414ba`.

**Coordinator decisions (2026-09-24) — these override every alternative written further down:**
- §0.5 → **option A** (group fallback). Every **[B]** variant, `null` tombstone and "[B only]" case below is VOID;
  implement the **[A]** text. Recorded as a deviation from spec §4.2 ("field by field") — reason: with every write in
  the look store, per-field fallback makes "no colour" / "default icon" unreachable (the old `HostConfig` value comes
  back), and option A reaches the same UX without widening the synced entry domain with `null`.
- §0.2 → **DECIDED**: delete `SessionPanel` in H2b-1.
- §0.6 → **DECIDED**: `purdex-shown-hosts` = `{ all: boolean; ids: string[] }`, both keys always present.
- Confirmed as written: the guard test lands in H2a (§0.14); `useHostStore.reset()` also resets the two new stores
  (§0.18); "hosts added later" look seeding lives in the add-host dialog and `registerLocalHost`, not `addHost`
  (§0.19); §0.8 — before the H2 real-device acceptance, both clients' hosts must have a verified daemonId.
- §0.7 → **(b) stay hidden** (user): a host added while the workbench lists hosts starts **disabled** there
  (zh-TW「未啟用」). What "disabled" means is the user's model of §0.21 (2026-09-24, **supersedes** the rev-3 "hidden
  tabs" model and spec §4.5 for H2d): **a tab is never hidden — disabling a host in a workbench CLOSES its tabs
  there** (mixed split tabs are split, the host's panes closed), once, on the device that pressed it, after a
  confirmation; tmux sessions are untouched; an apply never closes anything. H2d-2 … H2d-6 are planned per §0.21;
  §0.22 – §0.29 are open (NEEDS DECISION).
- Order: H2a and H2b-1 / H2b-2 may start now; H2c-2 and H2d-1 wait for H1b (PR #1406).

Spec: `docs/specs/2026-09-23-host-ownership-spec.md` (§4, decisions 3, 4, 7, 9; §3.3 re-resolve pass; §3.4; §6.4 steps
5 / 7; §7 H2a–H2d; §8 H2). Measured on the worktree at `618ab449` (alpha.442: H1a, H4a, H4b merged; H1b NOT merged —
its branch `worktree-host-ownership-h1b` was read at `70851837`). Every task is TDD (failing test first) and its own
commit. File lists are the files each PR touches, counted from the code — not estimates.

Test / lint / build: `cd spa && npx vitest run <files>`, `pnpm run lint`, `pnpm run build`, and
`npx tsc --noEmit -p tsconfig.app.json` (a bare `tsc --noEmit` checks nothing in `spa/`).

**Order and dependencies.** Twelve PRs (the spec's four; H2b, H2c and H2d split to stay ≤ 20 files / ≤ 800 lines —
§0.15):

```
H2a ─► H2b-1 ─► H2b-2 ─► H2c-1 ─► H2c-2 ─► H2c-3 ─► H2d-1 ─► H2d-2 ─► H2d-3 ─► H2d-4 ─► H2d-5 ─► H2d-6
                                    ▲                   ▲
H1b (T3 pass, T4 triggers/hydration, T6 integration) ───┴── merged before H2c-2 and before H2d-1
```

- **No H1b dependency:** H2a, H2b-1, H2b-2 (pure refactors — the selector reads `HostConfig` only) and H2c-1 (store +
  wire). They can start now, in parallel with H1b. H2c-1 is sequenced after H2b only so the look store never exists
  while a surface still reads `HostConfig` directly.
- **Needs H1b merged** (its T3 pass, T4 trigger / hydration wiring and T6 integration test — the interface §0.11
  lists): H2c-2 (look re-key) and H2d-1 (shown-hosts re-key).
- H2c-3 is the H4b sender / receiver switch of §6.4 step 7 (H4b merged first, so H2c owns it — §0.9).
- H2d-1 follows H2c-3 (ordinal 6 → 7); H2d-2 … H2d-6 follow (§0.21).
- Release: H2c-2 and H2c-3 share one bump PR (no released build has the look store as SOT while the transfer still
  writes looks to `HostConfig`, and New Tab labels follow a synced rename from the same release — §0.9, §0.15).
- Release: H2d-2 … H2d-5 share one bump PR (H2d-6 is tests only and may join it). The editor (H2d-3) is the only
  writer of the shown-hosts store; no released build may let a user disable a host while New Tab, the Hosts page and
  the notification / deep-link landings still offer to open tabs on it.

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
   by P-D.3. **DECIDED (coordinator, 2026-09-24)** — delete `SessionPanel.tsx` + `SessionPanel.test.tsx` in H2b-1 (then it is neither
   migrated nor filtered). Alternative: migrate its name read (H2b-1) and add the filter (H2d-4), +1 file each.
3. **"The session launcher's host choice" (§4.5) does not exist.** `SessionLauncher` takes `hostId` as a prop and is
   mounted per host block (`SessionSection.tsx:134` inside `HostSessionSection`, and `hosts/SessionsSection.tsx:115`
   on that host's page); `HeadlessLauncher` likewise lives in the per-host `headless:` block. Plan: the New Tab block
   filter (H2d-4) hides a disabled host's launchers; the Hosts page's own launcher is §0.29.
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
   the look store (a field removed) would reveal that old colour again. **DECIDED (coordinator, 2026-09-24): option A**
   — a deviation from spec §4.2, reason in the decisions block at the top. The two options as they were weighed:
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
   could never clear another device's list — that device would push its list back. **DECIDED (coordinator,
   2026-09-24)** —
   `{ all: boolean; ids: string[] }`, both always present (default `{ all: true, ids: [] }`); `all: true` is the
   spec's `null`. `ids` keeps unknown ids and order in both modes. (The look store has the same need and meets it:
   `looks` defaults to `{}` and is always built — a test pins it.)
7. **A host added while a workbench lists hosts is hidden there** (decision 4: unlisted = hidden). With `all: false`,
   adding mlab-2 through the dialog makes it invisible at once in this workbench. **DECIDED (user, 2026-09-24): (b)
   stay hidden** (= disabled) — what a disabled host means is the user's model of §0.21, which supersedes spec §4.5. The options
   as they were weighed:
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
   `ReceiveHostsDialog.test.tsx`, locales. (b) was chosen: no H2d file exists for this item (H2d-3 T4 pins "an add
   writes nothing to the shown store").
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
    H2c-3 (transfer + New Tab labels). H2d (rev 4, §0.21) is 59 files (61 with §0.22 (b)) → H2d-1 (store, wire,
    re-key; 15) / H2d-2 (the disable action: plan, close, split; 7) / H2d-3 (editor + confirmation dialog; 10) /
    H2d-4 (no way to open a tab on a disabled host: New Tab, picker, Hosts page; 14, 16 with §0.22 (b)) / H2d-5
    (landings: notification, execution deep link, route; 8) / H2d-6 (not-filtered behaviour tests + import guard; 5).
    H2d-2 and H2d-3 are split by the 800-line limit, not the file limit. Ordinals: `settings` is **5** today
    (`projections.ts:107`); H2c-1 → **6** with
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
    editor block (`ShownHostsBlock`, H2d-3) says "synced with the attached workbench". `ProfileSection`'s header rule
    "opening this page writes nothing" (and the iron rule, `start.ironrule.test.ts`) extends to the block — tested.
18. **Test isolation.** The look store is module state; tests that write a colour and later expect none for the same
    host id would leak inside one file. `useHostStore.reset()` has no production caller and 29 test files use it —
    H2c-2 makes it also reset `useHostLookStore`; H2d-3 adds `useShownHostsStore`.
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
21. **Disabling a host closes its tabs — the user's model (2026-09-24; supersedes the rev-3 "hidden tabs" model and
    spec §4.5 for H2d).** There is no hidden tab: a tab the workbench does not allow is CLOSED. Wording: a host is
    **enabled** / **disabled** in a workbench (zh-TW「啟用」/「未啟用」); the store stays `purdex-shown-hosts`
    (`{ all, ids }`, H2d-1 unchanged) — "shown" = enabled.
    1. **Unticking X in workbench W closes every tab of X in W.** A split tab that mixes X panes with other panes is
       **split**: X's panes are closed and every remaining pane becomes a tab of its own. Nothing is merged back when X
       is re-enabled; the user reopens / re-merges by hand.
    2. **tmux sessions are untouched** — they belong to the host. They can be reopened from X's session list (after X
       is re-enabled), and the provenance backfill (`agent-backfill`) fills agent / cwd again.
    3. **The close syncs** — tabs are workbench data (`tabs.<ws>`), so the other devices on W lose the tabs through
       the normal `tabs.*` apply.
    4. **A confirmation comes first**, listing the tabs that will be closed and the split tabs that will be split. The
       copy says: the tmux sessions are not affected and can be reopened from the host; but if a session ends later,
       these tabs can no longer be Rebuilt (their rebuild records go with them).
    5. **While X is disabled** — see the table.
    6. **The close runs once, on the device that pressed.** Data that arrives later with tabs of X (a device that was
       offline, an older SOT row, a restore) is shown as it is; no apply closes anything.
    7. **Switching workbench needs nothing** — no close, no filter on switch.

    | while X is disabled in W | behaviour |
    |---|---|
    | Tabs of X that exist (arrived after the close — rule 6) | **shown and working** like any tab (badge, pane, terminal); never closed, filtered or re-focused by the disabled state |
    | New Tab `sessions:` / `headless:` blocks of X (and the launchers in them) | **not rendered**; provider stays REGISTERED, the column stays in every preset (H2d-4) |
    | Every other way to OPEN a tab on X — terminated-pane session picker, Hosts page session "open", Hosts page executions / Nex executions "open" | **not offered** (hidden or disabled with a hint) (H2d-4) |
    | Notification of X | **still fires**; activating it opens X's Hosts page (no tab created or focused) (H2d-5) |
    | Execution deep link (`purdex://`) / route `/execution/…` naming X | opens X's Hosts page instead of a tab (H2d-5) |
    | Hosts page sidebar / overview | **every host listed**, in every workbench; a disabled host carries the state 「未啟用」/ "Disabled" and a muted colour; it opens normally (H2d-4) |
    | New host added while W lists hosts (§0.7 (b)) | disabled (unticked) — nothing to close |
    | Connections, health, `useMultiHostEventWs`, session watch / refresh, backup triggers, New Tab provider registration and layout, `activeHostId` / `hostOrder[0]` fallbacks, device settings pickers | **unchanged** |
    | Hosts page "new session" (creates a tmux session, no tab) | §0.29 |

    **Why an apply never closes (rule 6).** (a) The confirmation is the user's consent, given on ONE device; a
    `settings` apply on B has no one to ask, and B may hold tabs of X opened after A's decision (B had not yet
    received it) that A's dialog never listed — an apply that closed them would delete work nobody confirmed. (b)
    `settings` and `tabs.<ws>` are separate sections pushed and applied in either order: the closes already travel in
    A's `tabs.<ws>` push, so a close-on-apply on B would write a SECOND, competing `tabs.<ws>` for the same change
    (a `locked:conflict` whenever B also had a local tab edit). (c) Two devices whose enabled sets briefly differ
    would each close what the other just re-opened — the "two devices delete each other's tabs" loop. So: the closing
    code runs only inside the dialog's confirm handler; no subscriber of the shown-hosts store, no apply path and no
    hydration path calls it (tested by behaviour in H2d-2, statically by the import guard in H2d-6).

    **What is "a tab of X"** (host-bearing leaves, matched in WIRE space — H2d-2 T1):
    - `tmux-session` → `hostId` (terminated panes included: they are X's too);
    - `execution` → its effective host `host ?? hostOrder[0]` (the rule `resolveExecutionHostId` and the old
      `deleteHostCascade` use — a legacy hostless pane renders against the first host);
    - `editor` / `image-preview` / `pdf-preview` with `source.type === 'daemon'` → **§0.22 NEEDS DECISION**
      (recommended: not host-bearing);
    - every other kind (new-tab, browser, settings, hosts, dashboard, history, memory-monitor, editor-buffers, local /
      in-app files) → not host-bearing.
    A pane's host ref is a LOCAL id for a host this device has, or a wire id (`d1_…`) it has not resolved (H1a keeps
    those; the re-resolve pass maps them once the host exists). The shown-hosts `ids` are WIRE ids. The matcher maps
    both into wire space: `wireOfRef(ref) = Object.hasOwn(hosts, ref) ? wireIdOfHost(hosts[ref]) : ref`, and a leaf
    is X's when `wireOfRef(hostRef) ∈ disabling`, where `disabling` = the wire ids enabled before and disabled after
    the edit (candidates: every local host's `wireIdOfHost` ∪ `before.ids` ∪ `after.ids`; `all: true` enables every
    candidate). Consequences, all intended: a pane still on `d1_X` (arrived, not yet re-resolved) is matched; two
    local rows claiming one daemon (conflict) are disabled together, as the H2d-1 selector already reads them;
    unticking an id "not on this device" closes the `MissingHostPane` tabs on that id (they name that daemon).

    **Which tabs: the world on screen.** W is the workbench on screen = the tab world in `useTabStore` /
    `useWorkspaceStore` (every workspace of it, plus a tab no workspace has adopted yet). Parked worlds are never
    touched — the rule `host-lifecycle.ts` already follows ("a parked world is marked, never closed") and rule 7
    (no work on switch). The device-global nature of settings stores makes one case visible: §0.27.

    **A tab is closed or split, never partly hidden:**
    - every leaf host-bearing on a disabling host → the tab is **closed**;
    - at least one leaf on a disabling host and at least one other leaf → **split**: the X leaves go; the survivors,
      in tree order (`collectLeaves`, depth-first), become one tab each. The ORIGINAL tab keeps its id, `pinned`,
      `locked`, `createdAt`, workspace position and (if it was) the active / workspace-active status, with the first
      survivor as its single leaf; each further survivor is a NEW tab (`generateId()`, `pinned` inherited, `locked:
      false`, `createdAt: now`) inserted right after the previous one, in the same workspace, without changing any
      active tab. **Survivor panes are moved, not recreated**: the same `Pane` object, same `pane.id`, content
      byte-for-byte unchanged (unlike `detachPane`, which mints a new pane id through `createTab` — the editor's
      `paneStates`, `useRebuildStore.operations` and every other pane-id-keyed state would lose track). Split ratios
      (`sizes`) are dropped with the split node (device-local anyway, `!tabs.*.layout..sizes`).
    - a tab with no leaf on a disabling host → untouched. Locked tabs → §0.23.

    **One synchronous body under the operation lock (H1 plan §0.1).** The action rewrites the tab tree outside a
    switch, which is exactly what the in-process operation lock guards (`useRebuildStore`: "everything that creates
    tmux sessions or rewrites the tab tree passes through it") — a `tabs.*` apply awaiting between its read and its
    write, or a rebuild re-pointing a pane, must not interleave with it. `applyHostDisable(next, shownPlan)` =
    `acquireOperationLock('host-disable')` (synchronous) → refused ⇒ `{ kind: 'busy', holder }` and NOTHING is written
    (the dialog says so, with Retry) → re-plan from the live stores; plan ≠ the plan the dialog showed (compared by
    a signature of tab ids / pane ids) ⇒ `{ kind: 'changed', plan }`, nothing written, the dialog shows the new list
    and asks again → else, with no `await` anywhere: closes (`closeTabInWorkspace(id, { skipHistory: true })` —
    workspace membership, `visitHistory` focus fallback and active-tab sync as for any close, and not
    `tab-lifecycle.closeTab`, whose dirty-editor `window.confirm` and browser teardown are for user closes of other
    kinds), then splits (`useTabStore.splitOutPanes` + `useWorkspaceStore.insertTabsAfter`, one `set()` each), then
    the shown-hosts write LAST (`setShown` / `toggle`) → release → `{ kind: 'applied', closed, split }`.
    It is several `set()` calls on three stores in one task, not one write. The collector (500 ms trailing debounce
    per section, builds from the stores when the timer fires) therefore reports the FINAL state once per touched
    section: `settings` (shown hosts) and `tabs.<ws>` for each workspace that lost or gained a tab; `workspaces`
    does not move (its projection holds no tab list); untouched `tabs.<ws2>` do not move. Across sections there is no
    atomicity, and none is needed: B may apply `settings` first (X disabled, X's tabs still there — a valid rule-6
    state) or `tabs.<ws>` first (tabs gone, X still enabled for a moment) — both valid. Another window of the SAME
    device sees the stores through their persistence only; it never runs the close itself.

    **X offline is fine.** Nothing in the action talks to a daemon: no session API, no ticket, no fetch. Closing an
    execution pane unmounts it and its own lease release runs as on any close (best-effort; an unreachable daemon
    lets the lease expire). The invariant "tmux sessions are not killed" is tested as: no call to `deleteSession`,
    to `hostFetch(…, { method: 'DELETE' })`, or to any `host-api` export during the action (H2d-2 T4).

    **The confirmation's data** (`planHostDisable`, pure — the dialog lists exactly what the executor will do):
    - per disabling host: its label (`hostLabel(wireOrLocal, look)`);
    - `closes: { tabId, workspaceId | null, label }[]` and `splits: { tabId, workspaceId | null, label, closing:
      { paneId, label }[], keeping: number }[]`, grouped by workspace (workspace name; a tab no workspace has adopted
      under the `workspace.unsorted` label), in workspace tab order;
    - labels: `getPaneLabel(content, sessionLookup, workspaceLookup, t)` (`lib/pane-labels.ts`, as
      `WorkspaceSettingsPage`'s delete dialog does) with the live session name from `useSessionStore` (falls back to
      `cachedName` / code; terminated panes say so); a tab's label = its primary pane's; a split lists the label of
      each closing pane and "N panes stay, as N tabs";
    - (per §0.23 (a)) `keptLocked: { tabId, label }[]`, shown as "kept (locked)".
    Empty plan → §0.26.

22. **Are file panes on X's disk "tabs of X"?** **NEEDS DECISION.** `editor` / `image-preview` / `pdf-preview` with
    `source: { type: 'daemon', hostId }` read and write X's file system.
    - **(a) not host-bearing — RECOMMENDED.** Only tmux and execution panes count. Reasons: New Tab has no per-host
      file block (the host blocks the user hides are `sessions:` / `headless:`); file tabs are opened from paths that
      stay unfiltered by the spec (`FileTreeView` follows `activeHostId`; a terminal link opens a file on the pane's
      host; `EditorNewTabSection`'s recent list); a dirty buffer would be discarded by a close the user thought was
      about sessions; H1 plan §0.5 already treats file panes separately. Cost: X's file tabs stay after X is disabled
      (they keep working — X stays connected).
    - (b) host-bearing. + the matcher arm, a "N unsaved files will be discarded" line in the dialog, and the file entry
      points filtered (`EditorNewTabSection` recent entries of X: +2 files in H2d-4; `FileTreeView` / terminal-link
      opener would need a rule of their own — not planned).
23. **Locked tabs.** **NEEDS DECISION.** Every close path refuses a locked tab (`closeTabInWorkspace`, `closeTab`).
    - **(a) keep them — RECOMMENDED**: a locked tab of X is neither closed nor split; the dialog lists it under "kept
      (locked)". It is then a rule-6 tab (shown, working). Lock means "do not close this by accident"; a bulk action
      is the accident it exists for.
    - (b) close / split them anyway (the dialog is the consent) — the executor unlocks first (`toggleLock`, as
      `WorkspaceSettingsPage` does for its settings tabs).
24. **Undo.** **NEEDS DECISION.**
    - **(a) no undo, no toast — RECOMMENDED.** The dialog is the guard and lists everything; re-enabling X and
      reopening from the session list is the way back (rule 1). An undo would have to put back closed tabs AND
      re-join split tabs AND re-enable X, after the 500 ms debounce may already have pushed the closes — a second
      synced rewrite, racing whatever B did meanwhile.
    - (b) an info toast "Closed N tabs of X" without an action (`useUndoToast.show(message)`); +0 files beyond locales.
    - (c) an undo toast: snapshot the closed tabs, the split tabs' original layouts and the previous shown value;
      undo restores them if every affected tab id is still as the action left it, else says it could not. ≈ +3 files,
      one more lock-guarded body, and its own mutation set.
25. **History (reopen closed tab).** **NEEDS DECISION.** `closeTabInWorkspace` records the close in
    `useHistoryStore` unless `skipHistory`.
    - **(a) `skipHistory: true` — RECOMMENDED**: reopening from history would be a way to open a tab on X while it is
      disabled (table: "not offered"), and it is how `deleteHostCascade` and `pane-move` close.
    - (b) record them (the History page lists them; reopening puts a tab of X back — a rule-6 tab).
26. **Nothing to close.** **NEEDS DECISION.** Unticking a host that has no tab in W (or turning "all" off, or
    re-ticking) closes nothing.
    - **(a) no dialog when the plan is empty — RECOMMENDED**: the dialog's purpose is the list; the tmux copy has
      nothing to warn about. Turning "all" off initialises `ids` to every candidate wire id (nothing becomes
      disabled, so the plan is always empty there); re-ticking and "all" on never close.
    - (b) always confirm a disable, even with an empty list.
27. **Settings are device-global, tabs are per world.** **NEEDS DECISION** (edge). When a local profile (slave) is
    on screen, the Settings › 工作台 block edits the device's shown-hosts store, which is synced with the attached
    master (§0.17) — but the tabs on screen are the slave's. The action closes the SLAVE's tabs of X; the parked
    master's tabs of X are not touched here, and the other devices on the master do not close theirs (rule 6).
    - **(a) as described — RECOMMENDED** (one rule: "the world on screen"; the master's tabs of X become rule-6 tabs,
      which the user can close by hand; rule 7 says switching does nothing).
    - (b) also close in the parked master when this device is attached (`updateParkedWorlds`, the path the old
      cascade used for marks) — a close of tabs the user cannot see, in a world the dialog would have to list too.
    - (c) disable the block while a slave is on screen.
28. **Split survivors that are only interface panes.** Plan choice, listed for visibility: a survivor that is a
    `new-tab` placeholder, a settings / hosts page or any other `DEVICE_LOCAL_PANE_KINDS` pane becomes a tab of its
    own like any survivor (rule 1 says every remaining pane). Such a tab is device-local (`tabs.*` build leaves it
    out), so on the other devices the original split tab simply disappears with the push. Alternative (not planned):
    drop blank `new-tab` survivors.
29. **Hosts page "new session" on a disabled host.** **NEEDS DECISION.** `hosts/SessionsSection.tsx` has an "open"
    per session (creates a tab — **not offered**, H2d-4) and a "new session" launcher that creates a tmux session
    WITHOUT a tab ("Host page semantics: creating only").
    - **(a) keep "new session" — RECOMMENDED**: rule 5 forbids ways to add a TAB; the Hosts page is the management
      surface and "照舊列出全部主機"; the session appears in the list, openable once X is enabled.
    - (b) disable it too (the rev-3 wording "cannot be used to add a tab / tmux") — +0 files (same component).

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
  this PR reset the store in their own `beforeEach`; the `useHostStore.reset()` hook of §0.18 is added by H2d-3.
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

## H2d-2 — the disable action: plan, close, split (7 files; §0.21)

No UI. Pure planner + a synchronous executor + two store actions. Nothing calls the executor until H2d-3.

Files:
1. `spa/src/lib/host-disable.ts` (new — `disablingWireIds`, `hostRefOf`, `planHostDisable`, `planSignature`,
   `applyHostDisable`)
2. `spa/src/lib/host-disable.test.ts` (new)
3. `spa/src/lib/host-disable.integration.test.ts` (new — real collector builders / hashes and `applySectionToStores`)
4. `spa/src/stores/useTabStore.ts` (`splitOutPanes(tabId, dropPaneIds): string[]`)
5. `spa/src/stores/useTabStore.split.test.ts`
6. `spa/src/features/workspace/store.ts` (`insertTabsAfter(wsId, afterTabId, tabIds)` — no active-tab change)
7. `spa/src/features/workspace/store-tabs.test.ts`

API (`host-disable.ts`; imports `useTabStore`, `useWorkspaceStore`, `useHostStore`, `useShownHostsStore`,
`useRebuildStore`, `host-identity`, `pane-tree`, `nex/resolve-host`; NOT `host-api` — pinned by T4):
- `disablingWireIds(before, after, hosts): Set<string>` — enabled-before minus enabled-after over the candidates of
  §0.21 (`all: true` enables every candidate).
- `hostRefOf(content, fallbackHost): string | null` — tmux → `hostId`; execution → `host || fallbackHost`; §0.22 (b)
  only: daemon-source file panes → `source.hostId`; else `null`.
- `planHostDisable({ tabs, workspaces, hosts, hostOrder, disabling, sessions, t }): DisablePlan` — pure;
  `{ closes, splits, keptLocked (§0.23 (a)), hostLabels }` per §0.21 "The confirmation's data"; empty when
  `disabling` is empty.
- `planSignature(plan)` — tab ids + closing pane ids, sorted.
- `applyHostDisable(next: ShownHosts, shown: string): DisableResult` — §0.21 "One synchronous body": lock → re-plan
  → signature check → closes → splits → shown-hosts write → release. `DisableResult = { kind: 'applied', closed,
  split } | { kind: 'busy', holder } | { kind: 'changed', plan }`.

Store actions:
- `useTabStore.splitOutPanes(tabId, dropPaneIds)` — ONE `set()`: survivors = `collectLeaves(layout)` minus the dropped
  ids, in order; none / no drop / unknown tab → no-op, returns `[]`; else the original tab's layout = `{ type: 'leaf',
  pane: survivors[0] }` (the same `Pane` object), and for each further survivor a tab `{ id: generateId(), pinned:
  tab.pinned, locked: false, createdAt: Date.now(), layout: { type: 'leaf', pane } }`, spliced into `tabOrder` right
  after the previous one (the `addTab` pinned-group rule applies); returns the new ids in order. `activeTabId` and
  `visitHistory` untouched.
- `useWorkspaceStore.insertTabsAfter(wsId, afterTabId, tabIds)` — ONE `set()`: `tabIds` spliced after `afterTabId`
  (appended when absent), deduped against every workspace (the singleton rule of `insertTab`), `activeTabId` of every
  workspace unchanged; unknown `wsId` → no-op.

Tasks:
- **T1 — matcher and plan.** Tests (`host-disable.test.ts`): `disablingWireIds` — all → list minus X = {X};
  list → list minus X = {X}; list → all = ∅; re-tick = ∅; "all" off initialised to every candidate = ∅; an unknown id
  unticked = {that id}. `planHostDisable`: single-pane X tmux tab → close; terminated X tmux → close; X execution with
  `host` → close; hostless execution with X = `hostOrder[0]` → close, with X ≠ `hostOrder[0]` → untouched; a pane on
  `d1_X` (unresolved) → close; a local host WITHOUT daemonId matched by its local id; two local rows claiming one
  daemon → both disabled together; `[mlab | X]` → split, closing = the X pane, keeping 1; `[X | [editor(local) |
  mlab]]` → split, keeping 2, survivor order editor then mlab; a tab with no host-bearing leaf → absent; tabs of every
  workspace AND a tab no workspace holds; §0.22 (a): a daemon-source editor on X → absent; §0.23 (a): a locked X tab
  → `keptLocked`, a locked mixed tab → `keptLocked` (not split); labels: live session name, else `cachedName`,
  terminated suffix; grouping and order follow the workspaces. Implement. Commit.
- **T2 — store actions.** Tests (`useTabStore.split.test.ts`, `store-tabs.test.ts`): `splitOutPanes` keeps the
  original tab id / pinned / locked / createdAt, its leaf is the SAME `Pane` object (`toBe`) and every survivor's
  `JSON.stringify(pane)` equals the one before; new tabs follow the original in `tabOrder`, in survivor order;
  `activeTabId` / `visitHistory` unchanged; a pinned original → pinned new tabs placed in the pinned group; nested
  split → one tab per survivor; drop-all / drop-none / unknown ids → no-op; `insertTabsAfter` places after the anchor,
  moves an id out of another workspace, leaves every `activeTabId` alone, unknown workspace no-op. Implement. Commit.
- **T3 — the executor.** Tests (`host-disable.test.ts`): lock held by another owner → `busy`, and the tab store, the
  workspace store AND the shown-hosts store are the same objects as before (no `set()` at all — subscriber spies);
  a plan signature that no longer matches (a tab of X added / closed between plan and confirm) → `changed` with the
  fresh plan, nothing written; applied → closes done through `closeTabInWorkspace` with `skipHistory` (§0.25 (a):
  `useHistoryStore` unchanged), splits done, shown hosts = `next`, lock released (also when a step throws — `finally`);
  closing the ACTIVE tab moves focus by `visitHistory` inside its workspace; splitting the active tab keeps it active;
  the shown-hosts write happens after the tab writes (call-order spy); X's host runtime status `offline` changes
  nothing. Implement. Commit.
- **T4 — the tmux sessions and the daemon are not touched.** Tests: during `applyHostDisable` no export of
  `lib/host-api` is called (module mock with every export spied — `deleteSession`, `hostFetch` included) and
  `fetch` is not called; closed X tmux panes' sessions remain in `useSessionStore`; `host-disable.ts` imports nothing
  from `host-api` (import-declaration scan of the file). Commit.
- **T5 — sync behaviour** (`host-disable.integration.test.ts`, real builders and hashes, two simulated devices through
  `applySectionToStores`):
  - on A the action changes exactly: `settings`, and `tabs.<ws>` of each workspace that lost / gained a tab;
    `workspaces` and an untouched `tabs.<ws2>` hash identical; a second build is identical (one push per section);
  - **apply never closes**: B holds tabs of X; applying A's `settings` (X disabled) leaves B's tab store and
    workspace store the SAME objects and every B `tabs.*` hash unchanged; applying A's `tabs.<ws>` afterwards makes
    B's tabs equal A's (the closes arrive as data); the other order (tabs first, then settings) ends the same;
  - **later data is kept**: after the action, applying an OLDER `tabs.<ws>` payload that still holds X's tab (a
    device that was offline) shows the tab on A and nothing closes it; a following rebuild of A's sections carries it;
  - B running `useShownHostsStore.setState(disabled X)` directly (no action) closes nothing (no subscriber closes);
  - survivors' pane objects on A are byte-for-byte what they were, and after the push / apply B's survivor panes
    are byte-for-byte A's.
  Commit.

Invariants: the executor is the only code that closes because of a host being disabled, and it runs only when
called (no subscriber, apply or hydration path calls it); an apply never closes; surviving panes are moved
byte-for-byte with their pane ids; no daemon call, no session deletion; a refused lock or a stale plan writes
nothing; the whole action is synchronous.

Mutations: M1 the matcher compares local ids only (`d1_X` pane test red); M2 hostless executions ignored (effective-
host test red); M3 `splitOutPanes` mints new pane ids like `detachPane` (same-object / byte-for-byte test red); M4 new
tabs appended at the end of `tabOrder` / workspace (position tests red); M5 `insertTabsAfter` activates the inserted
tab (active-unchanged test red); M6 the executor ignores a refused lock (busy test red); M7 the executor runs a stale
plan (changed test red); M8 a `useShownHostsStore.subscribe` that runs the close on every change (apply-never-closes
test red); M9 the executor calls `deleteSession` for each closed tmux pane (T4 red); M10 closes recorded in history
(history test red, §0.25 (a)); M11 locked tabs closed (keptLocked test red, §0.23 (a)); M12 the shown-hosts write
first, the tab writes after a `queueMicrotask` (call-order / synchronous test red).

## H2d-3 — the editor and the confirmation (10 files)

Files:
1. `spa/src/components/settings/profile/ShownHostsBlock.tsx` (new)
2. `spa/src/components/settings/profile/ShownHostsBlock.test.tsx` (new)
3. `spa/src/components/settings/profile/HostDisableDialog.tsx` (new)
4. `spa/src/components/settings/profile/HostDisableDialog.test.tsx` (new)
5. `spa/src/components/settings/profile/ProfileSection.tsx` (renders the block after `CurrentBlock`)
6. `spa/src/components/settings/profile/ProfileSection.test.tsx` (opening writes nothing)
7. `spa/src/stores/useHostStore.ts` (`reset` resets shown hosts — §0.18)
8. `spa/src/stores/useHostStore.test.ts`
9. `spa/src/locales/en.json` (`settings.profile.shown_hosts.*`, `settings.profile.host_disable.*`)
10. `spa/src/locales/zh-TW.json`

Editor (`ShownHostsBlock`): "Enable all hosts" switch (`all`); when off, one checkbox per local host in `hostOrder`
(label from `useHostLookResolver`), then every `ids` entry that is not a local host's wire id, labelled with its look
name or id and "not on this device". Copy: the choice is synced with the attached workbench; a disabled host stays
connected; disabling a host closes its tabs in this workbench (§0.21). Writes:
- switch off → `setShown(every candidate wire id)` directly (nothing becomes disabled); switch on → `showAll()`
  directly; tick → `setShown(ids + wireId)` directly;
- untick → `next` computed → `planHostDisable` → empty plan: write directly (§0.26 (a)) / else open the dialog.

Dialog (`HostDisableDialog`, modal): title "Disable ‹host› in this workbench?"; sections "Tabs that will close" and
"Tabs that will be split" (each with the closing panes' labels and "N panes stay, each as its own tab"), grouped by
workspace; "Kept (locked)" (§0.23 (a)); the fixed notice: tmux sessions are not affected and can be reopened from the
host's session list; if a session ends later, these tabs can no longer be Rebuilt; the closes sync to every device
on this workbench. Buttons: Cancel (writes nothing) / "Disable and close N tabs" → `applyHostDisable(next,
planSignature(plan))`: `applied` → close the dialog; `busy` → inline "another operation is running (‹holder›)" with
Retry, dialog stays; `changed` → the dialog re-renders with the new plan and the confirm button is re-armed (no
automatic retry).

Tasks:
- **T1 — the block.** Tests: local hosts in `hostOrder` with look labels; unknown ids as "not on this device"; a
  host added under `all: false` is shown unticked (§0.7 (b)); mounting `ProfileSection` with no master and no action
  writes nothing (spies on `localStorage.setItem` and on `setState` of the shown-hosts, tab and workspace stores);
  switch off / on and tick write directly, open no dialog and close nothing. `useHostStore.reset()` resets the store.
  Locale keys (en + zh-TW; `locale-completeness.test.ts` covers parity). Commit.
- **T2 — untick opens the confirmation.** Tests: untick X with tabs → dialog lists exactly the plan's closes / splits
  (labels, grouping, split pane labels, kept-locked); untick X without tabs → no dialog, store written (§0.26 (a));
  untick an unknown id with a `MissingHostPane` tab on it → dialog lists that tab. Commit.
- **T3 — cancel writes nothing; confirm runs the action.** Tests: Cancel (button, Escape, backdrop) → tab store,
  workspace store, shown-hosts store the SAME objects, `localStorage.setItem` not called, the checkbox is ticked
  again; Confirm → `applyHostDisable` called once with `next` and the shown signature; `applied` → dialog closed, the
  tabs gone, X unticked; `busy` → message, nothing written, Retry calls again; `changed` → new list shown, nothing
  written until a second confirm. Commit.
- **T4 — adds write nothing** (§0.7 (b)): with `all: false`, the add-host dialog add and `registerLocalHost` leave
  the shown-hosts store the same object. Commit.

Invariants: the block writes only on user action; a disable writes only after Confirm; Cancel writes nothing
anywhere; the dialog lists exactly what the executor does (same plan function, signature-checked).

Mutations: M1 the block writes on mount (iron-rule test red); M2 untick writes the store before the dialog (cancel
test red); M3 Cancel leaves X unticked (checkbox test red); M4 the dialog builds its own list instead of
`planHostDisable` (list-equals-plan test red); M5 `changed` auto-confirms (second-confirm test red); M6 the editor
drops unknown ids on toggle (unknown-id test red); M7 switch-off initialises `ids` to `[]` (nothing-closes test red).

## H2d-4 — no way to open a tab on a disabled host: New Tab, picker, Hosts page (14 files)

Files:
1. `spa/src/components/NewTabPage.tsx` (skip `sessions:<id>` / `headless:<id>` blocks of a disabled host —
   prefixes from `HOST_BEARING_COLUMN_PREFIXES`)
2. `spa/src/components/NewTabPage.test.tsx`
3. `spa/src/components/SessionPickerList.tsx` (`connectedHosts.filter(isShown)`)
4. `spa/src/components/SessionPickerList.test.tsx`
5. `spa/src/components/hosts/HostSidebar.tsx` (every host listed; a disabled one gets 「未啟用」/ "Disabled" and muted
   text; NOT filtered)
6. `spa/src/components/hosts/HostSidebar.test.tsx`
7. `spa/src/components/hosts/SessionsSection.tsx` ("open" per session not offered for a disabled host, with the hint
   "Enable this host in the workbench to open its sessions"; "new session" per §0.29)
8. `spa/src/components/hosts/SessionsSection.test.tsx`
9. `spa/src/components/executions/ExecutionsView.tsx` (row "open" not offered, same hint)
10. `spa/src/components/executions/ExecutionsView.test.tsx`
11. `spa/src/components/hosts/nex/NexExecutionsTable.tsx` (row "open" not offered, same hint)
12. `spa/src/components/hosts/nex/NexExecutionsTable.test.tsx`
13. `spa/src/locales/en.json` (`hosts.disabled_badge`, `hosts.disabled_open_hint`)
14. `spa/src/locales/zh-TW.json`
(§0.22 (b) only: + `spa/src/components/editor/EditorNewTabSection.tsx` and its test — recent entries of a disabled
host not offered → 16.)

All read `useIsHostShown(hostId)` / `useShownHostFilter()` (H2d-1).

Tasks:
- **T1 — New Tab and the picker.** Tests: a disabled host's `sessions:` / `headless:` blocks not rendered while other
  columns render; the preset still holds the columns afterwards; `all: true` shows all; an unknown id in `ids`
  disables nothing local; the terminated-pane picker omits the disabled host. Commit.
- **T2 — the Hosts page.** Tests: `HostSidebar` lists every host; the disabled one has the badge and muted class and
  still selects / expands; a host enabled by `all: true` has no badge; `SessionsSection` / `ExecutionsView` /
  `NexExecutionsTable` for a disabled host render the list without an "open" action (or disabled) and the hint,
  clicking a row creates no tab (`useTabStore` tabs unchanged); for an enabled host unchanged; §0.29 (a): "new
  session" still offered. Commit.

Invariants: no UI path creates a tab on a disabled host; nothing listed on the Hosts page disappears; the New Tab
layout data is never changed by the filter.

Mutations: M1 `NewTabPage` removes the column from the preset instead of skipping it (preset-kept test red); M2
`HostSidebar` filters the disabled host (listed test red); M3 the filter compares local ids (daemonId-host test red);
M4 `SessionsSection` "open" still creates a tab for a disabled host (no-tab test red); M5 the badge reads `ids`
without `all` (all-true test red).

## H2d-5 — landings: notification, execution deep link, route (8 files)

Files:
1. `spa/src/lib/shown-hosts.ts` (`landOnHostsPageIfDisabled(hostId): boolean` — disabled → open the Hosts page on
   that host (`openSingletonTab({ kind: 'hosts' })` + `setActiveHost`, the `open-host` action's body) and `true`)
2. `spa/src/lib/shown-hosts.test.ts`
3. `spa/src/hooks/useNotificationDispatcher.ts` (`open-session` for a disabled host → the landing; no tab focused
   or created — also when a rule-6 tab of that session exists)
4. `spa/src/hooks/useNotificationDispatcher.test.ts`
5. `spa/src/lib/deeplink/deeplinkResolver.ts` (`openExecutionDetailTab` → the landing first; covers `purdex://`
   deep links and the Nex executions table)
6. `spa/src/lib/deeplink/deeplinkResolver.test.ts`
7. `spa/src/hooks/useRouteSync.ts` (`execution` route on a disabled host → the landing, no tab)
8. `spa/src/hooks/useRouteSync.test.ts`

Tasks:
- **T1 — the helper.** Tests: disabled → Hosts tab opened / focused, `activeHostId` = the host, returns `true`, no
  tab of that host created; enabled / unknown id → `false`, nothing done. Commit.
- **T2 — notifications still fire, clicks land on the Hosts page.** Tests: an agent notification of a disabled host
  is dispatched (the not-filtered half); its `open-session` click opens the Hosts page for that host, creates no tab,
  does NOT focus an existing tab of that session, still marks it read; enabled host unchanged. Commit.
- **T3 — execution deep link and route.** Tests: `resolveDeeplink` / `openExecutionDetailTab` for a disabled host →
  Hosts page, no execution tab; the `/execution/<id>/<host>` route likewise (and a hostless route whose fallback
  `hostOrder[0]` is disabled); enabled host → the tab as today. Commit.

Invariants: a notification of a disabled host is always delivered; no landing creates or focuses a tab on a disabled
host.

Mutations: M1 the dispatcher drops notifications of disabled hosts (delivery test red); M2 the click focuses the
existing tab (no-focus test red); M3 the route ignores the fallback host (hostless-route test red).

## H2d-6 — disabled ≠ absent: behaviour tests for the not-filtered list (5 files, tests only)

Files (all new):
1. `spa/src/lib/shown-hosts.not-filtered.connection.test.tsx`
2. `spa/src/lib/shown-hosts.not-filtered.tabs.test.tsx`
3. `spa/src/lib/shown-hosts.not-filtered.fallbacks.test.tsx`
4. `spa/src/lib/shown-hosts.not-filtered.newtab.test.tsx`
5. `spa/src/lib/shown-hosts.import-guard.test.ts`

Every case sets `{ all: false, ids: [<wire id of mlab>] }` so air26 (a local host with a daemonId) is DISABLED, and
asserts air26 behaves exactly as with `{ all: true }` (`it.each` over both settings, results compared). One
behaviour test per "unchanged" row of the §0.21 table:

- **T1 — connections and health** (file 1): `useHostConnection` runs for air26 and updates `runtime[air26].status`;
  `useMultiHostEventWs` opens air26's event WS (the `WebSocket` mock sees its URL) and processes its `sessions` event;
  `useSessionWatch` / session refresh (`lib/rebuild/refresh-sessions.ts`) fetches air26's sessions. Commit.
- **T2 — tabs that exist (rule 6)** (file 2): an air26 tmux tab present in the store (as if synced in after the
  close) stays in the tab bar (`SortableTab` / `InlineTab` rendered) with its badge (`useTabHostBadge` non-null);
  `SessionPaneContent` renders the terminal path (not `MissingHostPane`, ticket fetched); an air26 execution pane
  subscribes; keyboard next / previous tab and "close others" treat it like any tab; nothing closes it over a
  `settings` apply that keeps air26 disabled. Commit.
- **T3 — fallbacks and direct navigation** (file 3): with air26 as `activeHostId` / `hostOrder[0]`:
  `nex/resolve-host.ts` returns air26 for a hostless id; the fs backends resolve air26; `backup-auto-trigger`
  targets air26; `HostPage` at `/hosts/<air26>/overview` renders `OverviewSection`; `DevEnvironmentSection` lists
  air26 in its picker. Commit.
- **T4 — New Tab registration and layout** (file 4): the provider sources still return air26's `sessions:` /
  `headless:` providers; `getStaleNewTabProviderIds` does not report their ids; `useNewTabBootstrap` keeps the columns
  in every preset and in `knownIds`; the next `settings` build carries them unchanged. Commit.
- **T5 — import guard** (file 5, the static half; scan of import declarations in non-test files):
  `useShownHostsStore` may be imported only by `shown-hosts.ts`, `host-disable.ts`, `ShownHostsBlock.tsx`,
  `useHostStore.ts`, `host-reresolve.ts`, `collector.ts`, `apply-to-stores.ts`; `lib/shown-hosts` only by the H2d-4 /
  H2d-5 files and `ShownHostsBlock.tsx`; **`lib/host-disable` only by `ShownHostsBlock.tsx` and
  `HostDisableDialog.tsx`** (the static proof that only the pressing device's dialog closes — rule 6). Commit.

Invariant: every "unchanged" row of §0.21 has a behaviour test that is identical with the host disabled and enabled;
the closing code is reachable only from the dialog.

Mutations (each a one-line change in production code, reverted after): M1 `useMultiHostEventWs` skips disabled hosts
(T1 red); M2 `useHostConnection` skips them (T1 red); M3 `useTabHostBadge` returns null for a disabled host (T2 red);
M4 the tab bar filters tabs of disabled hosts (T2 red — the rev-3 model must not come back); M5 `nex/resolve-host`
skips disabled hosts in its `hostOrder[0]` fallback (T3 red); M6 the provider sources filter disabled hosts (T4 red);
M7 `getStaleNewTabProviderIds` reports a disabled host's column (T4 red); M8 import `host-disable` in
`apply-to-stores.ts` (T5 red); M9 import the store in `useSessionWatch` (T5 red).

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

After H2d-5 (H2d-2 … H2d-5 released together; H2d-6 is tests only). Extra setup, on A in W, before step 6: three
tmux sessions on air26 (`acc-s1`, `acc-s2`, `acc-s3`) and two on mlab (`acc-m1`, `acc-m2`), created from the Hosts
page; tabs: T1 = `acc-s1` alone; T2 = split `[acc-m1 | acc-s2]`; T3 = split `[acc-s3 | [local editor on a scratch
file | acc-m2]]`; T4 = `acc-m2` alone in a second workspace; T5 = `acc-s1` again, LOCKED (only if §0.23 (a)). Wait
until B shows all of them. Record: the `settings` and every `tabs.<ws>` rev on the SOT; A's `purdex-tabs`
(`playwright cli -s=host-ownership-a localstorage-get purdex-tabs`, saved to the scratchpad, not printed); air26's
session list (`GET /api/sessions` on `100.64.0.4:7860`, auth header from a variable).
6. **The confirmation.** On A, Settings › 工作台: turn "Enable all hosts" off → no dialog, every box ticked, no tab
   changes; `settings` rev moved once, no `tabs.*` rev moved. Untick air26 → the dialog lists: close T1; split T2
   (closing `acc-s2`, 1 pane stays) and T3 (closing `acc-s3`, 2 panes stay, as 2 tabs); T4 not listed; T5 under
   "kept (locked)"; the tmux / Rebuild notice is there (en and zh-TW — switch A's language once).
7. **Cancel writes nothing.** Cancel → air26 ticked again; the SOT revs and A's `purdex-tabs` are exactly the recorded
   ones; B unchanged.
8. **Confirm.** Untick air26 again → Confirm. On A: T1 gone; T2 = `acc-m1` alone (same tab id); T3 = the editor alone
   (same tab id) and a new tab `acc-m2` right after it, in the same workspace; T4 and T5 unchanged; the survivor panes
   in A's `purdex-tabs` have the recorded pane ids and byte-identical content (compare the saved JSON with a
   scratchpad script; print only "equal" / the differing paths). SOT: `settings` moved once, `tabs.<W's first
   workspace>` moved once, T4's workspace and `workspaces` did not move. On B, without reload: the same tab structure
   (T1 gone, T2 single, T3 split into two), nothing locked on either client.
9. **tmux untouched.** air26's session list still holds `acc-s1`, `acc-s2`, `acc-s3` (same codes as recorded);
   `playwright cli -s=host-ownership-a requests` shows no `DELETE` to either daemon during steps 6–8.
10. **While disabled** (both clients): New Tab has no air26 `sessions:` / `headless:` block (and the layout editor
   still lists the columns); the terminated-pane picker offers no air26; the Hosts sidebar lists air26 with
   「未啟用」/ "Disabled" and muted colour, its page opens, its session list shows the sessions without "open" and with
   the hint; air26 stays connected (status on its Hosts page, `requests` shows its event WS open).
11. **Old data is not closed (rule 6).** Inject into W's SOT the `tabs.<ws>` payload recorded before step 6 (current
   rev as base, `PUT /api/profiles/{id}/sections/tabs.<ws>`) → both clients show T1 and the split T2 / T3 again, with
   badges, and air26 still disabled; wait 5 s: the `tabs.<ws>` rev moved only by the injection (no client closed
   anything and pushed). Close those tabs by hand afterwards.
12. **Notification.** Trigger an agent notification in `acc-s1` on air26 (e.g. a short `claude -p` turn in that session
   ends) → the notification appears on A; clicking it opens air26's Hosts page, and no tab of `acc-s1` is created or
   focused. Navigate A to `/execution/<any id>/<air26 id>` → air26's Hosts page, no execution tab.
13. **Re-enable.** On A tick air26 → no dialog; B shows the air26 block in New Tab again; from air26's Hosts page open
   `acc-s1` → a tab attaches to the SAME session (same code), and if an agent runs there its provenance appears on the
   pane's rebuild record (backfill). Nothing re-merged automatically.
14. On A turn "Enable all hosts" back on → B unchanged apart from the setting; no section locked.
(Only if §0.27 is decided (a): with a local profile on screen on A, untick air26 → the dialog lists the local
profile's tabs only; the parked master's air26 tabs are there after switching back.)

Not reachable before H3 (the `hosts` section still syncs — same reason as H1 plan §0.9):
- A client that lacks a host the workbench has a look / shown id for: the host lists converge through `hosts`, so
  "unknown ids kept, listed as not on this device" and "look of a host this device lacks" are checked by INJECTING
  into W's SOT a `settings` payload (current rev as base, `PUT /api/profiles/{id}/sections/settings`) whose `looks` and
  `ids` carry an extra `d1_ffff…` → both clients keep it through apply and their next build (`settings` rev moves
  only by the injection), the editor lists it as "not on this device", a pane injected on that id shows its look name.
- "B deletes a host, A unaffected": pre-H3 A loses the host through `hosts`; what IS checked is that the look entry
  and shown id survive on both.
- Unticking an id "not on this device" (§0.21: closes the tabs whose panes name it) is checked on the injected
  `d1_ffff…` above: with the injected pane's tab present, untick it on A → the dialog lists that tab; Confirm → it
  closes on both clients and `settings.ids` no longer holds the id.
- Independent host lists with different add orders / names per device: the `HostConfig` fallback differs per device
  only after H3; here both fallbacks are equal, so steps 2–4 prove the look path through the `hosts`-rev-unchanged
  check instead.

Cleanup: `playwright cli -s=host-ownership-a close`, `-s=host-ownership-b close`, `-s=host-ownership-c close` (same
cwd), delete W, stop :5175.

## Review

### 2026-09-24 — user decision (rev 3 → rev 4)

- User decision superseded §0.21 hidden-tabs model: disabling a host closes its tabs in the workbench on screen (mixed
  split tabs are split), once, on the device that pressed, after a confirmation; tmux sessions untouched; an apply
  never closes. PROPOSED 1–4 of rev 3 (tab-bar filter, focus move, keyboard / bulk actions count visible tabs only,
  hidden-tab deep links) are void. H2d re-planned as H2d-2 … H2d-6; new open items §0.22 – §0.29.

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
