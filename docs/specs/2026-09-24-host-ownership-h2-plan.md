# Plan — host ownership H2 (H2a / H2b / H2c / H2d)

Status: **rev 7** (2026-09-24) — H2d rewritten to the user's final rules on shown hosts (below). History: rev 2 took the
codex plan review `task-muei1qbr-0hu3ol`; rev 3 recorded the coordinator's decisions; rev 4 replaced the "hidden tabs"
model of §0.21 by a "disabling closes" model; rev 5 decided §0.22 – §0.29; rev 6 corrected H2d against the
pre-measurement, split the pane gate out and took the codex plan review `task-mufbtxpn-8p1egj`. **Rev 7 drops the whole
closing model of rev 4 – rev 6** (it was never the user's decision — see "Superseded" below) and re-plans H2d as five
PRs, H2d-1 … H2d-5, counted from the code at the branch base of PR #1421 (`b680e092`, H2c-3 merged) plus the four
H2d-1 commits already on that branch (`151f8bf8` … `6858fb1f`, written for the old `{ all, ids }` store — H2d-1 is
reworked, see H2d-1). H2a … H2c-3 are merged and unchanged by rev 7. Rev 7 then took the codex plan review
`task-mufjfxo4-h4e2rf` (Review log): one opener rule (the wire-space matcher), Handoff / Take back / Take to terminal
re-checks in H2d-3, recovery deduped per daemon, every §0.30 question DECIDED.

**User rules (2026-09-24, final — confirmed by the user directly; they override everything below that disagrees):**

1. **Hosts management page** lists EVERY host. Per the current workbench (profile): a shown host renders as today; a
   hidden host renders in a "hidden" style (muted, a 「未啟用／已隱藏」-type state). A hidden host can still be
   clicked and managed (overview, sessions list, settings, etc.).
2. **A newly added host is hidden in every workbench**; the user turns it on by hand. When this feature ships,
   **every existing host is hidden too** (no migration that seeds the list with current hosts).
3. Showing / hiding only changes presentation on the management page; its real effect is to **restrict the
   workbench's tab ↔ tmux session link**.
4. **Hiding a host never closes or changes tabs.** An already-open tab stays; each pane on the hidden host shows
   「主機已於此工作台關閉」 (en: "This host is turned off in this workbench") and opens no connection. Turning the host
   back on restores the same pane (reconnects, no reload). **A split tab is not split and not restored**: only the
   pane(s) on the hidden host show the message, the other panes work as usual; closing that pane is up to the user.
5. **Block New Tab and every related way to open a tab** on a hidden host (New Tab sessions / headless blocks, the
   terminated-pane session picker, Hosts page session "open", executions "open", deep links / notifications that
   would open a tab — landing on the host's management page instead is fine).

Model: each workbench keeps a plain **shown list** of wire ids (`d1_…`, or a local id until the daemonId is known and
the re-resolve pass re-keys it). No "all" flag. Showing / hiding adds / removes exactly that host; nothing else in the
list changes. Unknown ids (hosts this device does not have) are kept. Default and migration: empty list = every host
hidden. The list syncs in `settings` like the look store.

**Superseded by rev 7 (do not implement):**
- §0.6 `{ all: boolean; ids: string[] }` → a plain list `{ ids: string[] }`, no `all` (§0.6 rewritten).
- §0.7 "(b) stay hidden" for hosts added while a list exists → **every** host starts hidden: new hosts AND all
  existing hosts at ship time (§0.7 rewritten).
- §0.21's "disabling closes its tabs" model and everything derived from it: the split of mixed tabs, survivors, the
  confirmation dialog (`HostDisableDialog`), the planner / executor (`planHostDisable`, `applyHostDisable`), the
  parked-world editor, the four-store snapshot / rollback, the `superseded` / `unsettled` / `busy` / `changed`
  results; §0.23 as "locked tabs are kept by the dialog"; §0.24 (undo); §0.25 (history); §0.26 (empty plan); §0.27
  (closing in parked worlds); §0.28 (split survivors). §0.21 is rewritten to the new behaviour; §0.22 – §0.28 keep a
  one-line record each.
- The "exclude model" (a list of HIDDEN ids) that a peer relayed earlier was **not** the user's decision and is not
  planned.
- rev 6's H2d-2 … H2d-6 (the disable action, the editor + confirmation, the old H2d-4 / H2d-4b / H2d-5 / H2d-6 split).

**Coordinator decisions (2026-09-24) still in force (H2a – H2c, merged):**
- §0.5 → **option A** (group fallback). Every **[B]** variant, `null` tombstone and "[B only]" case below is VOID;
  implement the **[A]** text. Recorded as a deviation from spec §4.2 ("field by field") — reason: with every write in
  the look store, per-field fallback makes "no colour" / "default icon" unreachable (the old `HostConfig` value comes
  back), and option A reaches the same UX without widening the synced entry domain with `null`.
- §0.2 → **DECIDED**: delete `SessionPanel` in H2b-1.
- Confirmed as written: the guard test lands in H2a (§0.14); `useHostStore.reset()` also resets the two new stores
  (§0.18); "hosts added later" look seeding lives in the add-host dialog and `registerLocalHost`, not `addHost`
  (§0.19); §0.8 — before the H2 real-device acceptance, both clients' hosts must have a verified daemonId.
- From the rev-6 H2d pre-measurement, still true under rev 7 (adapted in §0.21 / §0.23): the pane gate sits in
  `PaneLayoutRenderer`'s leaf branch; host-level connections (event WS, health, session watch) stay unchanged; the
  per-pane sweeps (reconcile's revive pass, `probeMissingCwds`, the provenance triggers incl.
  `useMultiHostEventWs.ts:190-192`) and the StatusBar peer info skip panes of hidden hosts; showing a host again calls the
  existing `recoverHostSessions` from a store subscriber.

Spec: `docs/specs/2026-09-23-host-ownership-spec.md` (§1.2 — the five rules, added with rev 7; §4, decisions 3, 4, 7, 9;
§3.3 re-resolve pass; §3.4; §6.4 steps 5 / 7; §7 H2a–H2d; §8 H2). H2a – H2c measured on the worktree at `618ab449`
(alpha.442); H2d (rev 7) at `b680e092` + the H2d-1 commits. Every task is TDD (failing test first) and its own commit.
File lists are the files each PR touches, counted from the code — not estimates.

Test / lint / build: `cd spa && npx vitest run <files>`, `pnpm run lint`, `pnpm run build`, and
`npx tsc --noEmit -p tsconfig.app.json` (a bare `tsc --noEmit` checks nothing in `spa/`).

**Order and dependencies.** Eleven PRs (the spec's four; H2b, H2c and H2d split to stay ≤ 20 files / ≤ 800 lines —
§0.15):

```
H2a ─► H2b-1 ─► H2b-2 ─► H2c-1 ─► H2c-2 ─► H2c-3 ─► H2d-1 ─► H2d-2 ─► H2d-3 ─► H2d-4 ─► H2d-5
                                    ▲                   ▲
H1b (T3 pass, T4 triggers/hydration, T6 integration) ───┴── merged before H2c-2 and before H2d-1
```

- **No H1b dependency:** H2a, H2b-1, H2b-2 (pure refactors — the selector reads `HostConfig` only) and H2c-1 (store +
  wire). H2c-1 is sequenced after H2b only so the look store never exists while a surface still reads `HostConfig`
  directly.
- **Needs H1b merged** (its T3 pass, T4 trigger / hydration wiring and T6 integration test — the interface §0.11
  lists): H2c-2 (look re-key) and H2d-1 (shown-hosts re-key). Both merged / on branch.
- H2c-3 is the H4b sender / receiver switch of §6.4 step 7 (H4b merged first, so H2c owns it — §0.9).
- H2d (rev 7): H2d-1 store / wire / selectors / re-key (ordinal 6 → 7) → H2d-2 the Hosts page (hidden style, the
  per-host show / hide switch — the only writer — and no "open" on a hidden host) → H2d-3 no New Tab / picker entry
  and the landings (notification, deep link, route), in-flight handoff re-checks → H2d-4 the pane gate, per-pane sweeps, StatusBar, re-show
  recovery → H2d-5 tests only (not-filtered behaviour + import guard). The writer (H2d-2) merges BEFORE any
  restriction (H2d-3, H2d-4), so the main checkout's live SPA (:5174 HMR) always has the switch to show a host before
  anything is hidden from it.
- Release: H2c-2 and H2c-3 share one bump PR (no released build has the look store as SOT while the transfer still
  writes looks to `HostConfig`, and New Tab labels follow a synced rename from the same release — §0.9, §0.15).
- Release: **H2d-1 … H2d-4 share one bump PR** (H2d-5 is tests only and may join it). Reason: the store's default
  `[]` hides every host (rule 2); a released build that reads it (H2d-3 / H2d-4) without the switch (H2d-2) would
  leave the user no way to show a host, and a build with the switch but without the gate would "hide" a host whose
  panes still connect. Nothing with ordinal 7 has been released (PR #1421 is open), so the rework of H2d-1 needs no
  wire compatibility with the `{ all, ids }` shape.

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
   migrated nor filtered). Alternative: migrate its name read (H2b-1) and add the filter (H2d), +1 file each.
3. **"The session launcher's host choice" (§4.5) does not exist.** `SessionLauncher` takes `hostId` as a prop and is
   mounted per host block (`SessionSection.tsx:134` inside `HostSessionSection`, and `hosts/SessionsSection.tsx:115`
   on that host's page); `HeadlessLauncher` likewise lives in the per-host `headless:` block. Plan: the New Tab block
   filter (H2d-3) hides a hidden host's launchers; the Hosts page's own launcher is §0.29.
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
6. **`purdex-shown-hosts` is a plain list `{ ids: string[] }` (rev 7, user rules; supersedes the rev-3 `{ all, ids }`
   and spec §4.1's `{ ids: wireId[] | null }`).** Two measured rules in `applier.ts` still shape it: (a)
   `applySettings` rejects the WHOLE `settings` payload when a patched field changes shape class, and `null` is its
   own class (`shapeOf`) — so `ids` is never `null`; (b) a store the payload lacks is "not sent" and left alone — so
   the builder always emits the store (`{ ids: [] }` included; a test pins it, as for the look store). There is no
   "all" state at all: `[]` = every host hidden (rule 2), and a host is shown only while its id is listed.
   **Why no `all` (the attacker finding on PR #1421).** The rev-6 store turned `all: true` into a list the first time
   a host was toggled — `toggle(wireId, knownWireIds)` materialised "all" into the wire ids THIS device knows. A host
   only another device knows (B has `c`, A does not) was thereby dropped from the list by A's toggle, i.e. hidden on B
   although nobody hid it. With a plain list every write adds or removes exactly one host's ids and carries every
   other id — unknown ones included — through untouched, so the case cannot arise. `ids` keeps unknown ids and their
   order through `merge`, apply, build, show / hide and re-key.
   **Concurrent toggles on two devices** (review `task-mufjfxo4-h4e2rf` item 3) are not a new defect: the `settings`
   section is replaced whole, but it syncs under the existing per-section compare-and-swap. Two devices changing it
   from the same base (A hides `a`, B shows `b`) → the second push gets a 409 and its section goes `locked:conflict`
   (`sync-state.ts:58`); a human picks a side (spec decisions 4–5; the P2b rules). That is the model of every
   `settings` store (the look store included), not something the shown list adds; there is no silent loss. H2d-1 T2
   pins it with a test.
7. **Every host starts hidden (rev 7, user rule 2; supersedes the rev-4 "(b) stay hidden only while a list exists").**
   A host added later (add-host dialog, `registerLocalHost`, transfer receive) is hidden in every workbench — nothing
   is written on add; the user shows it from the Hosts page (H2d-2). At ship time the store starts at `[]`, so every
   existing host is hidden too: there is **no** migration seeding the list with the current hosts, and none may be
   added later (a test pins "an add writes nothing to the shown store", H2d-1 T1). Consequence, intended: after the
   upgrade every existing tab's tmux / execution panes show 「主機已於此工作台關閉」 until the user shows their hosts
   (the release note says so — H2d bump PR).
   The list belongs to the WORKBENCH, not the device: a host whose wire id W already lists (shown in W from another
   device) is shown in W on a device that adds it later — "hidden on add" means nothing is written on add, not that an
   existing entry is ignored.
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
    H2c-3 (transfer + New Tab labels). **H2d (rev 7) is 64 files** → H2d-1 (store, wire, selectors incl. the pane
    matcher, re-key, `reset`; **19** — the four commits on PR #1421 touch exactly these files, the rework adds none) /
    H2d-2 (the Hosts page: hidden style, the show / hide switch, no "open" on a hidden host; **12**) / H2d-3 (no New
    Tab / picker entry; landings: notification, execution deep link, route; Handoff / Take back / Take to terminal
    re-checks; **16**) / H2d-4 (pane gate, per-pane sweeps, StatusBar, re-show recovery; **16**) / H2d-5 (not-filtered
    behaviour tests + import guard; **5**, tests only) — **68** files. Rev 6's H2d was 78 files in seven PRs; the
    closing model's planner, executor, parked-world editor, store actions, editor block and dialog (H2d-2 / H2d-3 of
    rev 6, 20 files) are gone. Lines: H2d-1 at `6858fb1f` is +952 / −30 over 19 files (≈ 2/3 tests); the rework
    removes `all` / `showAll` / `setShown` / `addShown` / the known-ids `toggle` and adds the two-client regression and
    the conflict test — it is expected to stay around 850 – 900 lines, above the 800 target, test-heavy. **DECIDED
    (§0.30 item 4): one PR** — the user authorised H2d end to end, the store, wire and matcher are one contract, and the
    overage is tests. The other H2d PRs are expected well under 800. Ordinals:
    `settings` is **5** before H2 (`projections.ts:107`); H2c-1 → **6** with `@wire:host-look=1`; H2d-1 → **7** with
    `@wire:shown-hosts=1` (`WIRE_MARKERS.settings`, `projections.ts:172`) — nothing with 7 has been released, so the
    shape change of the rework keeps ordinal 7 and the marker.
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
17. **Settings stores are device-global** (spec §2). The shown-hosts setting therefore applies to whatever workbench
    is on screen (a local slave too) and syncs with the attached master. **Where the switch lives (rev 7):** on the
    Hosts page — `OverviewSection` gets a "Show in this workbench" switch per host (H2d-2), and `HostSidebar` shows
    the hidden style; there is **no** Settings › 工作台 editor (rev 6's `ShownHostsBlock` is dropped). Reason: rule 1
    puts the state on the management page and rule 3 says showing / hiding is a management-page presentation whose
    effect is on tabs; the overview page is where a host is managed. The switch's copy says the choice is for the
    workbench on screen and synced with it. Unknown ids (hosts this device lacks) have no row anywhere — they are kept,
    invisible, and act on the device that has the host (DECIDED, §0.30 item 2). Opening the Hosts page writes nothing
    (tested in H2d-2).
18. **Test isolation.** The look store is module state; tests that write a colour and later expect none for the same
    host id would leak inside one file. `useHostStore.reset()` has no production caller and 29 test files use it —
    H2c-2 makes it also reset `useHostLookStore`; H2d-1 (already on PR #1421, `useHostStore.ts` +2) resets
    `useShownHostsStore` to `{ ids: [] }`.
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
21. **Hiding a host: a pane gate and blocked "open a tab" surfaces — nothing closes (rev 7, user rules 3 – 5;
    supersedes the rev-4 … rev-6 closing model and spec §4.5).** Wording: a host is **shown** / **hidden** in a
    workbench (zh-TW state 「已隱藏」, en "Hidden"; the switch 「在此工作台顯示」 / "Show in this workbench"); the pane
    placeholder says 「主機已於此工作台關閉」 / "This host is turned off in this workbench" (user wording, rule 4).
    1. **Hide = a pane gate + blocked open-a-tab surfaces.** Hiding X in workbench W writes the shown list only (one
       `settings` change). No tab, workspace, parked world, rebuild record or tmux session is written, closed, split or
       re-focused — by the device that pressed, by an apply on another device, or by hydration. There is no dialog, no
       planner, no executor and no rollback: the only write is one `set()` on `useShownHostsStore`.
    2. **Split tabs are untouched except the gated pane.** In `[mlab | X]` the X leaf renders the placeholder and the
       mlab leaf keeps working; the layout, sizes and pane ids do not change. Closing the gated pane (pane header /
       context menu, as for any leaf) is the user's choice.
    3. **Showing X again restores the same panes.** The gate subscribes live: the leaf re-renders with its renderer —
       same pane id, same content, the renderer REMOUNTS (a new xterm, WS and ticket; tmux redraws the screen; the xterm
       scrollback from before the gate is lost — accepted, it is a client-side buffer of a session still running; an
       execution pane re-subscribes). On the hidden → shown transition the EXISTING `recoverHostSessions(hostId)`
       (`lib/rebuild/refresh-sessions.ts:264`) runs once per daemon (deduped by wire identity: two local rows claiming
       one daemon — an identity conflict — shown together give ONE call, for the first of them in `hostOrder`), from a
       store subscriber (not from the switch's click handler), so a show that ARRIVES by a `settings` apply recovers
       too. Reason: the daemon pushes a
       `sessions` frame only when its session list changes, so an idle host would otherwise leave a revivable pane on
       Rebuild. `recoverHostSessions` needs no attach gate, defers to the operation-lock release when the lock is held
       (`needsRecovery`), and runs `refreshHost` → the `sessions` reconcile → `runRevivePass`, all fenced as today.
    4. **Sync.** The shown list travels in `settings`; B applies it and B's panes gate / ungate live. No `tabs.*`,
       `workspaces` or `hosts` section moves because of a hide / show (tested, H2d-1 T2 and H2d-5 T2). Two devices
       toggling different hosts from the same base meet the existing per-section conflict (`locked:conflict`, a human
       picks a side — §0.6; tested, H2d-1 T2); nothing is merged or lost silently.
    5. **Worlds.** Nothing is done per world: a parked world's panes are gated when it is switched in (the gate reads
       the live store at render). Switching workbench needs nothing.

    | while X is hidden in W | behaviour |
    |---|---|
    | Tabs with panes on X | the tab **stays** in the tab bar with its badge (never closed, filtered, split or re-focused); **every pane of it on X renders 「主機已於此工作台關閉」 and opens no connection** — no terminal WS, no ticket, no execution history / attach / SSE / `CostPanel` fetch, no nex `ensure`, no mount-time cwd / provenance probe, no terminated-pane Rebuild / session picker; other panes of the tab work as usual (H2d-4) |
    | New Tab `sessions:` / `headless:` blocks of X (and the launchers in them) | **not rendered**; provider stays REGISTERED, the column stays in every preset and in `knownIds` (H2d-3) |
    | Terminated-pane session picker (`SessionPickerList`) | X **not offered** (H2d-3) |
    | Hosts page session "open", Hosts page Nex executions "open", the sidebar Executions view "open" | **not offered** (hidden, with the hint "Show this host in the workbench to open its sessions") (H2d-2) |
    | Notification of X | **still fires**; activating it opens X's Hosts page (no tab created or focused — also when a tab of that session exists) (H2d-3) |
    | Execution deep link (`purdex://`) / route `/execution/<host>/<execution id>` naming X (hostless: `hostOrder[0]` = X) | lands on X's Hosts page, no tab (H2d-3) |
    | Deep link / route / notification naming a ref that is neither a local host nor listed (an unresolved `d1_X`, a deleted host's id, a hostless link with an empty `hostOrder`) | **not openable**: lands on the Hosts page (no host selected), no tab (H2d-3) — one opener rule, below |
    | Handoff ("Hand to nex"), Take back, Take to terminal on X | **not offered** (the entries live in the gated leaf — H2d-4); a call already in flight when X is hidden writes no pane at completion, and the Handoff success toast offers no "open" / its opener lands on X's Hosts page (H2d-3) |
    | Hosts page sidebar / overview | **every host listed**, in every workbench; a hidden host in the hidden style (muted, 「已隱藏」 / "Hidden"), still selectable, expandable and manageable (overview, sessions list, settings, logs, peers, nex); the overview carries the show / hide switch (H2d-2) |
    | Hosts page "new session" (creates a tmux session, no tab) | **kept** (§0.29) |
    | New host added | hidden (§0.7) — nothing written |
    | Host-level connections: health, `useMultiHostEventWs` (the event WS and its `sessions` reconcile incl. terminated marks and `openAttachGate`), session watch / refresh, backup triggers, New Tab provider registration and layout, `activeHostId` / `hostOrder[0]` fallbacks, device settings pickers, direct navigation `/hosts/<X>/…` | **unchanged** — but the per-pane sweeps inside them (the revive pass, `probeMissingCwds`, the provenance triggers of `reconcile-host.ts:125-127` and `useMultiHostEventWs.ts:190-192`) and the StatusBar peer info (`usePeerInfo`) **skip panes of hidden hosts** (H2d-4) |

    **What is "a pane on X"** (`hostRefOf`, `lib/shown-hosts.ts`, H2d-1):
    - `tmux-session` → `hostId` (terminated panes included);
    - `execution` → its effective host `host || hostOrder[0]` (the `resolveExecutionHostId` rule — a legacy hostless
      pane renders against the first host; `null` when there is no host);
    - `editor` / `image-preview` / `pdf-preview` with `source.type === 'daemon'` → **not host-bearing** (§0.22 —
      DECIDED, §0.30 item 1);
    - every other kind → not host-bearing.
    The shown list holds WIRE ids; a pane's ref is a LOCAL id, or a wire id (`d1_…`) this device has not resolved (H1a
    keeps those; the re-resolve pass maps them once the host exists). The pane matcher works in wire space:
    `wireOfRef(ref, hosts) = Object.hasOwn(hosts, ref) ? wireIdOfHost(hosts[ref]) : ref`, and a pane is shown when
    `wireOfRef(ref) ∈ ids` — **or**, for a local host, its local id `host.id ∈ ids` (**the local-id form**, part of the
    model — user rules: "a local id until the daemonId is known and the re-resolve pass re-keys it"; the local id is
    the pre-re-key form of the same entry; without it a host shown before its daemonId was learned would read as hidden
    between the daemonId write and the pass's re-key, and the gate would unmount / remount its panes and fire a
    spurious recovery). Hiding a host removes both forms; showing adds `wireIdOfHost(host)`. Consequences: a pane on an
    unresolved `d1_X` is gated (hidden placeholder) unless `d1_X` is listed, in which case it renders `MissingHostPane`
    as today (DECIDED, §0.30 item 3); two local rows claiming one daemon (identity conflict) are shown / hidden
    together.
    **One rule for every opener too** (review `task-mufjfxo4-h4e2rf` item 1). Every "open a tab" surface — New Tab,
    the session picker, the Hosts page "open", the Executions "open", the notification click, the `purdex://` deep link
    (`lib/deeplink/deeplinkResolver.ts:18`), the `/execution/<host>/<execution id>` route (`hooks/useRouteSync.ts:105`,
    hostless via `lib/nex/resolve-host.ts:11`), the Handoff toast opener — uses the SAME wire-space predicate as the
    pane gate: `isRefShown(ref)` = `wireOfRef(ref) ∈ ids`, or the local-id form. A ref that is neither a local host nor
    listed is **not openable**: a landing (deep link, route, notification) goes to the Hosts page instead, a list
    (New Tab, picker) does not offer it. There is **no** helper answering "not a local host → shown": rev 7's first
    draft had one (`isHostShown`), and with it `/execution/d1_X/<id>` opened a tab that the gate then placeholdered —
    an opener and the gate disagreeing. So an opener never creates a tab whose pane the gate would hide.

    **Where the gate sits (measured, rev 6, re-checked at `6858fb1f`).** Not in `SessionPaneContent`: gating there would
    miss the execution pane (`ExecutionPaneWrapper`, `register-modules/index.tsx:102-119` — history fetch, attach, SSE,
    lease, `CostPanel`'s `fetchNexHost`), the nex `ensure(tmuxHostId)` that `PaneLayoutRenderer` runs for every tmux
    leaf (`PaneLayoutRenderer.tsx:62-64`, an HTTP fetch) and the probes `SessionPaneContent` starts in an effect before
    any render branch (`SessionPaneContent.tsx:39-51`), and it would leave the terminated branch's Rebuild / session
    picker reachable. The gate is in **`PaneLayoutRenderer`'s leaf branch** (`PaneLayoutRenderer.tsx:67-90`), above
    the module renderer resolution: a leaf whose host ref is hidden renders `HostHiddenPane` instead of the renderer,
    and the nex `ensure` effect keys on the SHOWN host only (null while hidden — no fetch, no "Hand to nex" item, so
    the nex handoff / take-back surfaces of that pane are unreachable too). It **subscribes live** (`usePaneHostShown`),
    unlike the module-enabled `pinnedEnabled` snapshot of the same component (`:43`). Tabs kept by `useTabAlivePool`
    (`TabContent.tsx:14-17`) are mounted while in the background, so the gate also closes the live connections of a
    kept background tab the moment X is hidden. **Unmounting a gated execution pane runs its best-effort
    `releaseLease`** (`useExecutionLease.ts:179-193`, from `lib/nex/nex-api`): that one request to X is expected, and an
    unreachable daemon simply lets the lease expire.

    **Where the sweep guards sit (measured).** Inside the three functions, not at their call sites: `runRevivePass`
    (`revive.ts:171`, next to `canAttachTerminal`) is also called directly by `refresh-sessions.ts:329, :333`
    (`reconcileAfterLockRelease`); `probeMissingCwds` (`cwd-probe.ts:128`) and `provenanceBindings`
    (`reconcile-host.ts:37-58` — it feeds BOTH provenance triggers, the sweep of `reconcileHostSessions` `:125-127`
    and the hook-event trigger `useMultiHostEventWs.ts:190-192`, so the hook file is unchanged) have one call site each
    besides the hook. The tmux sweeps are per host already (a tmux pane's host ref IS the `hostId` they are called
    with), so the host-level check is the per-pane rule. `reconcileHostSessions` itself — the verdict, the store writes,
    `openAttachGate` — runs unchanged: a hidden host's panes still learn that their session ended, so the placeholder
    and the later re-show see the true state.

    **X offline is fine.** Hiding or showing calls no daemon; the recovery on show is the existing refresh.

22. **File panes on X's disk.** `editor` / `image-preview` / `pdf-preview` with `source: { type: 'daemon', hostId }`
    are **not host-bearing** in rev 7 (the rev-5 decision (a), carried over): they are not gated and New Tab has no
    per-host file block. Rule 3 ("restrict the tab ↔ tmux session link") supports it; rule 4's "each pane on the
    hidden host" could be read to include them — **DECIDED: not gated** (§0.30 item 1). Gating them would add the matcher arm
    (+0 files: `hostRefOf`), a dirty-buffer question (a gated editor unmounts its Monaco) and the file entry points
    (`EditorNewTabSection` recent entries, `FileTreeView`, the terminal-link opener — +2 files at least in H2d-3).
23. **Locked tabs.** Rev 6's "a locked tab is kept by the dialog" is void (nothing closes). The one rule that remains:
    every pane on a hidden host is gated, whatever its tab (locked or not, synced in or local).
24. **Undo** — SUPERSEDED (nothing closes; showing the host again is the way back).
25. **History (reopen closed tab)** — SUPERSEDED (nothing closes). Reopening a closed tab of X from history while X is
    hidden reopens a tab whose X panes are gated — like any existing tab, and not "a way to open a tab on X" that
    connects; history is left as it is (plan choice).
26. **Nothing to close** — SUPERSEDED.
27. **Settings are device-global, tabs are per world.** The closing half is SUPERSEDED. What remains: the shown list is
    device-global and synced with the attached master (§0.17); every world on this device — the one on screen, the
    parked master, the parked slaves — gates its panes by the same list when it is on screen.
28. **Split survivors** — SUPERSEDED (no split).
29. **Hosts page "new session" on a hidden host.** **DECIDED: (a) keep it** (rev 5, carried over). `hosts/SessionsSection.tsx`
    has an "open" per session (creates a tab — **not offered**, H2d-2) and a "new session" launcher that creates a tmux
    session WITHOUT a tab ("Host page semantics: creating only"). Rule 5 forbids ways to add a TAB; rule 1 keeps the
    host manageable; the session appears in the list, openable once X is shown.
30. **Former open questions (rev 7) — all DECIDED** (coordinator, after the codex plan review `task-mufjfxo4-h4e2rf`,
    items 4 and 7; the defaults below are the decisions).
    1. **File panes** (§0.22): **DECIDED — not gated.** `hostRefOf` returns `null` for them; no file entry point is
       filtered.
    2. **Unknown ids have no UI.** **DECIDED — accepted.** An id of a host this device lacks has no row (Hosts page or
       elsewhere); it is kept and acts on the device that has the host (rule "unknown ids are kept"). No Settings ›
       工作台 list.
    3. **A pane on a host this device does not have** (unresolved `d1_…`): **DECIDED — the wire-space matcher.** Not
       listed → the hidden placeholder 「主機已於此工作台關閉」; listed → `MissingHostPane` ("this device has no host
       ‹name›", H1a). (The local-id form is not a question: it is part of the model — §0.21 "What is a pane on X".)
    4. **H2d-1 size.** **DECIDED — one PR** (≈ 850 – 900 lines, ≈ 2/3 tests, 19 files; the user authorised H2d end to
       end; the overage is tests).
    5. **Copy.** **DECIDED — as written:** the state 「已隱藏」 / "Hidden", the switch 「在此工作台顯示」 / "Show in this
       workbench", the pane placeholder 「主機已於此工作台關閉」 / "This host is turned off in this workbench".

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

## H2d-1 — shown hosts: store, wire, selectors, re-key (19 files; needs H1b; PR #1421, reworked)

PR #1421 holds this PR written for the rev-6 `{ all, ids }` store (`151f8bf8` … `6858fb1f`, +952 / −30). Rev 7 reworks
it IN PLACE — the same 19 files, new commits on the same branch (one per task below, each TDD: the changed tests first).
Nothing reads the store for behaviour yet (the readers are H2d-2 … H2d-4).

Files (all already touched by the four commits):
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
11. `spa/src/lib/profile/applier.test.ts` (`isWellFormedSection`: the store's only field is `ids`)
12. `spa/src/lib/profile/executor.direction.integration.test.ts` (ordinal pin 6 → 7)
13. `spa/src/lib/shown-hosts.ts` (new — selectors, pane matcher, the writer helper, re-key)
14. `spa/src/lib/shown-hosts.test.ts` (new)
15. `spa/src/lib/host-reresolve.ts` (re-key step in `passBody`; hydration / re-read lists)
16. `spa/src/lib/host-reresolve.test.ts`
17. `spa/src/lib/host-reresolve.integration.test.ts` (re-key of a shown id: only `settings` pushed, once)
18. `spa/src/stores/useHostStore.ts` (`reset()` also resets the shown store — §0.18)
19. `spa/src/stores/useHostStore.test.ts`

Store (`useShownHostsStore`, §0.6): `{ ids: string[] }`, default `{ ids: [] }` (= every host hidden, rule 2), persisted
(`purdexStorage`, key `purdex-shown-hosts`, `partialize` → `{ ids }`), registered with `syncManager`. `merge` keeps
strings only, dedupes keeping the FIRST, keeps unknown ids and their order; a persisted value without an array `ids`
→ `[]`; a legacy `all` key in storage (dev builds of the rev-6 H2d-1) is ignored — never read, never written back.
Actions — each one `set()`, and a no-op returns the same state (no persist, no notify):
- `show(id)` — appends `id` when absent;
- `hide(id)` — removes `id` when present;
- `toggle(id)` — `hide` when present, else `show`;
- `rekey(moves)` — per step, `from` replaced in place by `to`, or dropped when `to` is already listed.
There is NO `all`, NO `showAll`, NO `setShown(ids)` and nothing that builds a list from the ids this device knows (§0.6
— the #1421 attacker finding). The module imports no store.

Wire: `PROJECTIONS.settings` += `...settingsPaths('purdex-shown-hosts', ['ids'])`; ordinal **6 → 7** (comment
`// 7: purdex-shown-hosts.ids (wire ids of the hosts shown in the workbench; host ownership H2d)`);
`WIRE_MARKERS.settings` = `['@wire:host-id=d1', '@wire:host-look=1', '@wire:shown-hosts=1']`. Nothing with ordinal 7 was
released, so the rework keeps 7 and the marker; the guard snapshot's fingerprint changes (one path instead of two).
`buildSettingsSection` / `settingsFromWire` unchanged: ids pass verbatim (no local↔wire mapping, like the look keys).

Selectors, matcher and writer (`lib/shown-hosts.ts`; imports `useHostStore`, `useShownHostsStore`, `host-identity`,
`host-look` (`wireKeyMovesOf`); none of them may import it):
- `shownFormsOf(host): string[]` — `[wireIdOfHost(host), host.id]`, deduped (one element when the host has no
  daemonId);
- **ONE predicate** (§0.21 "One rule for every opener too"): `isRefShown(ref, hosts, ids)` = a local host → some form
  of it (`shownFormsOf`) ∈ `ids`; any other ref (an unresolved `d1_…`, a deleted host's id, `''`) → `ref ∈ ids` (`''`
  never is). There is NO `isHostShown` with a "not a local host → `true`" arm, and no other pure predicate: the hooks
  and the non-hook read below all evaluate `isRefShown`:
  `useIsRefShown(ref)` (live on that host object and `ids`), `useShownRefFilter(): (ref) => boolean` (for lists),
  `isRefShownNow(ref)` (non-hook, `getState()` of both stores — the openers, the sweeps, the in-flight re-checks);
- pane matcher (§0.21 "What is a pane on X"): `hostRefOf(content, hostOrder)` (tmux → `hostId`; execution →
  `host || hostOrder[0]`, `null` without a host; every other kind → `null`); `wireOfRef(ref, hosts)`;
  `isPaneHostShown(content, hosts, hostOrder, ids)` = `hostRefOf` null → `true`, else `isRefShown`;
  `usePaneHostShown(content): boolean` (live on hosts + `hostOrder` + `ids`, a primitive per selector);
- `setHostShown(hostId, shown: boolean)` — the only writer the UI calls (H2d-2): unknown host → no-op; `shown` →
  `show(wireIdOfHost(host))`; hidden → `hide(form)` for each of `shownFormsOf(host)`. Never touches another id.
- `rekeyShownHosts(hosts)` — as on the branch: moves from `wireKeyMovesOf(hosts)` filtered to listed ids; `null` when
  nothing moves, else `{ commit, undo }` (undo restores `{ ids }`) for `passBody` to commit under its grant.
(Rev 7 renames the rev-6 "enabled" names — `isPaneHostEnabled` / `usePaneHostEnabled` / `isHostRefEnabledNow` /
`isRefEnabled` — to "shown"; the rev-6 `ShownHostsView` with `all` goes, and so do the rev-6 `isHostShown` /
`useIsHostShown` / `useShownHostFilter` with their "not a local host → `true`" arm.)

Tasks:
- **T1 — store.** Tests (`useShownHostsStore.test.ts`, rewritten): default `{ ids: [] }`; `merge` — non-strings
  dropped, duplicates dropped keeping the first, unknown ids and order kept, a non-array `ids` → `[]`, a stored `all`
  key ignored (not in memory, not in the next persisted value); `show` appends once (idempotent, same state object on
  the second call); `hide` removes only that id (the others, unknown ones included, keep their order); `toggle` both
  ways; `rekey` (`d1_…` already listed → the local id dropped; else replaced in place; missing source no-op); the
  persisted value is exactly `{ ids }` (+ zustand's `version`); registered with `syncManager`; the store has no `all`
  / `showAll` / `setShown` / `addShown` (a `Object.keys(getState())` pin). `useHostStore.test.ts`: `reset()` → `{ ids:
  [] }`; **adds write nothing** — `addHost`, `registerLocalHost` and a transfer `applyHostTransfer` (created row) each
  leave the shown store the same object (`toBe`) and `localStorage['purdex-shown-hosts']` unchanged (§0.7). Commit.
- **T2 — wire and old-client lock.** `projections.test.ts`: guard snapshot (ordinal 7, the new fingerprint) with the
  marker array pinned exactly; a real `profileLock` regression (as H2c-1 T2): the ordinal-6 client's `settings` shape
  (`PROJECTIONS.settings` minus `purdex-shown-hosts.ids`, markers `['@wire:host-id=d1', '@wire:host-look=1']`,
  ordinal 6) against an index holding only this build's `settings` row → `verdict: 'sot-is-newer'` (`locked:schema`);
  the ordinal-5 case still locks; no projection path `purdex-shown-hosts.all`. `applier.test.ts`:
  `{ 'purdex-shown-hosts': { ids: [] } }` and `{ ids: ['d1_a', 'localX'] }` well-formed; `{ ids: [], all: true }` NOT
  well-formed (unlisted field). `collector.test.ts`: a `show` re-schedules `settings`; an EMPTY store still builds
  `'purdex-shown-hosts': { ids: [] }` (always present, §0.6). `apply-to-stores.test.ts`: a payload `{ ids: ['d1_unknown',
  'd1_a'] }` → the store holds both byte-for-byte, the returned hash equals the payload's (nothing pushed); `[]` ↔ a
  list applies without `rejected-settings`; applying a shown-hosts change leaves the tab, workspace and local-profiles
  stores the same objects (nothing closes on apply — §0.21 rule 1). `executor.direction.integration.test.ts`: ordinal
  pin 7; **concurrent toggles meet the existing conflict** (review item 3, §0.6): from a synced base `{ ids: ['d1_a'] }`
  another device writes the SOT with `b` shown (`{ ids: ['d1_a', 'd1_b'] }`, same base rev), then this client hides
  `a` (`{ ids: [] }`) → its push gets the 409 and `settings` is `locked:conflict`; the SOT still holds the other
  device's ids and this client's store its own (no silent merge, no silent loss); `resolve('settings', 'sot')` lands
  `['d1_a', 'd1_b']`. **The two-client regression (the #1421 finding)** — `apply-to-stores.test.ts`, two simulated devices through
  real builders and `applySectionToStores`: device A has local hosts `a`, `b` (daemonIds → `d1_a`, `d1_b`), device B
  has `a`, `b`, `c`; the synced `settings` holds `{ ids: ['d1_a', 'd1_b', 'd1_c'] }` on both; on A
  `setHostShown(a, false)` → A's built payload is `{ ids: ['d1_b', 'd1_c'] }` (`d1_c` kept although A does not know
  `c`); B applies it → on B `isRefShown(c)` is still `true` and `isRefShown(a)` is `false`; then a host `d` added on
  A and a host `e` added on B are each hidden on their device and nothing is written (the payloads rebuild
  byte-identical). Commit.
- **T3 — selectors, matcher, writer.** Tests (`shown-hosts.test.ts`, rewritten): `[]` → every local host hidden;
  listed `d1_…` → the local host of that daemon shown; a no-daemonId host by its local id; **the local-id form**: a
  host listed under its local id and whose daemonId is then learned (before any re-key) is still shown, both by
  `isRefShown` and `isPaneHostShown`; a host listed under neither form → hidden; conflict (two rows, one daemon) →
  shown / hidden together. **One rule for refs that are not local hosts:** `isRefShown('d1_X')` with `d1_X` unlisted
  → `false` (NOT `true`), listed → `true`; a deleted host's local id unlisted → `false`; `''` → `false`; and the
  module exports no other predicate (an export-list pin: no `isHostShown`, no `useIsHostShown`, no
  `useShownHostFilter`). `useIsRefShown` / `useShownRefFilter` / `isRefShownNow` agree with `isRefShown` on every case
  above (one table, `it.each` over the four readers). Pane matcher: `hostRefOf` for tmux / terminated tmux /
  execution with `host` / hostless execution (→ `hostOrder[0]`; empty `hostOrder` → `null`) / daemon-source editor (→
  `null`) / new-tab (→ `null`); `isPaneHostShown` — a pane on an unresolved `d1_X` with `ids` lacking `d1_X` →
  `false`; with `d1_X` listed → `true`; a hostless execution pane with `hostOrder[0]` = a hidden host → `false`, = a
  shown host → `true`; a non-host-bearing pane → `true`; a host added after the list was written (its wire id not
  listed) → hidden by every reader; `usePaneHostShown` re-renders on a `show` / `hide` and on a daemonId learned (the host's wire id moves), and
  NOT on an unrelated id's write when the result is unchanged (render counter). Writer: `setHostShown(a, true)` on a
  host with daemonId appends `d1_a` only; `setHostShown(a, false)` on a host listed under BOTH forms removes both and
  nothing else (unknown ids and order intact); unknown host → no-op (same state object). Commit.
- **T4 — re-key in the pass** (as on the branch, adapted to `{ ids }`). Tests (`host-reresolve.test.ts`): local id →
  `d1_…` on daemonId learned (in place); `d1_…` already listed → local id dropped; conflict → nothing moves;
  `rewriteHostRefs` (the deletion direction) moves nothing; idempotent; nothing listed → `null`, no lock taken.
  Integration (`host-reresolve.integration.test.ts`, real builders and hashes): `ids` holding a host's local id + its
  daemonId learned → the pass changes only the `settings` hash, a second pass changes nothing; with both a look entry
  and a shown id on that local id, still exactly one `settings` change. Commit.

Invariants: one predicate (`isRefShown`) — no export answers "not a local host → shown"; the store is a plain list —
no write ever adds or removes an id other than the host's own forms; unknown
ids survive merge, apply, build, show / hide and re-key; `[]` hides every host; an add path writes nothing; an apply
of shown hosts writes no tab / workspace / local-profiles store; old clients (ordinal 5 and 6) lock.

Mutations: M1 `toggle` / `hide` materialise the ids this device knows (the list rebuilt from the local hosts' wire
ids — the two-client regression red, `d1_c` lost); M2 `merge` drops ids that are not local hosts (unknown-id tests
red); M3 ordinal not bumped (snapshot red); M4 marker missing (marker pin + lock regression red); M5 the selector
compares the local id only (daemonId case red); M6 re-key keeps both forms (dedupe test red); M7 re-key moved into
`planRewrite` (the `rewriteHostRefs` deletion test red); M8 the matcher answers a non-local ref through a "not a local
host → `true`" helper (the `d1_X` unlisted tests red); M9 `hostRefOf` ignores the hostless-execution fallback
`hostOrder[0]` (fallback tests red); M10 a new host shown by default (the store seeds its wire id on add, or a
missing-from-list host reads as shown — the adds-write-nothing and host-added-later tests red); M11 the local-id form
ignored (the learned-daemonId-before-re-key test red); M12 `setHostShown(false)` removes only the wire form (the
both-forms writer test red).

## H2d-2 — the Hosts page: hidden style, the show / hide switch, no "open" (12 files)

The only writer of the shown list (§0.17). Merges before any restriction (H2d-3, H2d-4), so the live main checkout
always has the switch first.

Files:
1. `spa/src/components/hosts/HostSidebar.tsx` (every host listed; a hidden one gets muted text and a 「已隱藏」 / "Hidden"
   tag; still selects / expands; NOT filtered)
2. `spa/src/components/hosts/HostSidebar.test.tsx`
3. `spa/src/components/hosts/OverviewSection.tsx` (the switch "Show in this workbench" under the header, with the one-
   line copy "Applies to the workbench on screen and syncs with it. A hidden host stays connected; its tabs stay and
   show 「主機已於此工作台關閉」." → `setHostShown(hostId, next)`)
4. `spa/src/components/hosts/OverviewSection.test.tsx`
5. `spa/src/components/hosts/SessionsSection.tsx` ("open" per session not offered for a hidden host, with the hint;
   "new session" kept — §0.29)
6. `spa/src/components/hosts/SessionsSection.test.tsx`
7. `spa/src/components/executions/ExecutionsView.tsx` (the sidebar Executions view: row "open" not offered, same hint)
8. `spa/src/components/executions/ExecutionsView.test.tsx`
9. `spa/src/components/hosts/nex/NexExecutionsTable.tsx` (row "open" — `openExecutionDetailTab` — not offered, same hint)
10. `spa/src/components/hosts/nex/NexExecutionsTable.test.tsx`
11. `spa/src/locales/en.json` (`hosts.shown.switch`, `hosts.shown.switch_hint`, `hosts.shown.hidden_badge`,
    `hosts.shown.open_hint`)
12. `spa/src/locales/zh-TW.json`

All read `useIsRefShown(hostId)` / `useShownRefFilter()` (the one predicate, H2d-1) and write only through
`setHostShown` (H2d-1).

Tasks:
- **T1 — sidebar and switch.** Tests: `HostSidebar` lists every host with `ids: []` (none filtered), each hidden one
  has the tag and the muted class and still selects / expands / opens its sub-pages; a shown host has no tag; the
  switch on `OverviewSection` reflects the store, ON → `ids` gains exactly `wireIdOfHost(host)`, OFF → loses exactly the
  host's forms (an unknown id present before stays, in order); mounting the Hosts page (sidebar + overview) with no
  action writes nothing (spies on `localStorage.setItem` and `useShownHostsStore.setState`); toggling writes no tab /
  workspace store (same objects). Locale keys (en + zh-TW; `locale-completeness.test.ts` covers parity). Commit.
- **T2 — no "open" on a hidden host.** Tests: `SessionsSection` / `ExecutionsView` / `NexExecutionsTable` for a hidden
  host render the list (sessions / executions still listed) without an "open" action and with the hint; clicking a
  row creates no tab (`useTabStore` tabs unchanged, `openExecutionDetailTab` not called); a shown host unchanged;
  "new session" still offered and still creates no tab (§0.29). Commit.

Invariants: nothing on the Hosts page disappears because a host is hidden; the switch is the only writer and writes
only that host's forms; opening the page writes nothing; no tab is created from the Hosts page for a hidden host.

Mutations: M1 `HostSidebar` filters hidden hosts (listed test red); M2 the switch calls `toggle(wireId)` only (the
both-forms OFF test red when the host is listed under its local id too); M3 the switch writes on mount (no-write test
red); M4 `SessionsSection` "open" still creates a tab for a hidden host (no-tab test red); M5 the tag reads a local id
(daemonId-host test red); M6 the sessions list itself hidden for a hidden host (list-still-rendered test red — rule 1).

## H2d-3 — no New Tab / picker entry; landings: notification, deep link, route; in-flight handoffs (16 files)

Every opener uses the one predicate `isRefShown` (§0.21 "One rule for every opener too"; review `task-mufjfxo4-h4e2rf`
item 1): a ref that is neither a local host nor listed is not openable.

Files:
1. `spa/src/components/NewTabPage.tsx` (skip `sessions:<id>` / `headless:<id>` blocks of a hidden host — prefixes from
   `HOST_BEARING_COLUMN_PREFIXES`; the preset is not changed)
2. `spa/src/components/NewTabPage.test.tsx`
3. `spa/src/components/SessionPickerList.tsx` (`connectedHosts.filter(isShown)`)
4. `spa/src/components/SessionPickerList.test.tsx`
5. `spa/src/lib/shown-hosts.ts` (`landOnHostsPageIfHidden(ref): boolean` — `isRefShownNow(ref)` → `false`, nothing
   done; hidden and a local host → open the Hosts page on that host (`openSingletonTab({ kind: 'hosts' })` +
   `setActiveHost`, the `open-host` action's body — `useNotificationDispatcher.ts:375-377`) and `true`; hidden and NOT
   a local host (an unlisted `d1_X`, a deleted host's id, `''` from a hostless link with an empty `hostOrder`) → open
   the Hosts page without changing `activeHostId` and `true` — not openable, never a tab)
6. `spa/src/lib/shown-hosts.test.ts`
7. `spa/src/hooks/useNotificationDispatcher.ts` (`open-session` for a hidden host → the landing; no tab focused or
   created — also when a tab of that session exists)
8. `spa/src/hooks/useNotificationDispatcher.test.ts`
9. `spa/src/lib/deeplink/deeplinkResolver.ts` (`openExecutionDetailTab` (`:18`) → the landing first; covers
   `purdex://` deep links, whose host is resolved by `resolveExecutionHostId` (`lib/nex/resolve-host.ts:11` — a present
   hint verbatim, else `hostOrder[0]`, else `''`))
10. `spa/src/lib/deeplink/deeplinkResolver.test.ts`
11. `spa/src/hooks/useRouteSync.ts` (the `execution` case, `:105-110`: parsed `/execution/<host>/<execution id>`
    (`lib/route-utils.ts:84-92`) or the legacy hostless `/execution/<execution id>` → `resolveExecutionHostId` → the
    landing when not shown, no tab)
12. `spa/src/hooks/useRouteSync.test.ts`
13. `spa/src/lib/nex/handoff.ts` (the in-flight re-check: `handToNex` (`:83`), `takeBack` (`:146`) and
    `takeToTerminal` (`:204`) evaluate `isRefShownNow(hostId)` AFTER the daemon answers and BEFORE
    `trySetPaneContent` (`:100`, `:160`, `:240`); hidden → no pane write, `swapped: false`. The daemon-side action has
    happened and is not undone: the pane keeps its old content, gated; on re-show the existing `sessions` reconcile /
    the execution pane's own fetch show the true state (a handed-off tmux session ended → terminated; an archived
    execution → its problem state), and the execution / session is listed on the Hosts page)
14. `spa/src/lib/nex/handoff.test.ts`
15. `spa/src/components/HandoffConfirmDialog.tsx` (`confirm` (`:39`): hidden at click → close without calling
    `handToNex`; the success toast (`:46-54`) offers "open execution" only when the host is still shown at completion,
    and its opener re-checks at click — hidden then → `landOnHostsPageIfHidden(hostId)`, no `openSingletonTab`)
16. `spa/src/components/HandoffConfirmDialog.test.tsx`

Not offered (no file here): "Hand to nex" needs the SHOWN `tmuxHostId` in `PaneLayoutRenderer` (H2d-4 — the nex
`ensure` / `selectHandoffReady` key on the shown host only) and Take back / Take to terminal live in `ExecutionView`,
which a gated leaf never mounts (H2d-4). `ExecutionView.runTakeBack`'s toast (`ExecutionView.tsx:85`) opens nothing;
with the re-check it says `takeback.archived_no_pane` — accurate (no pane was re-pointed). H2d-1 … H2d-4 ship in one
bump, so no release has the re-checks without the gate.

Tasks:
- **T1 — New Tab and the picker.** Tests: a hidden host's `sessions:` / `headless:` blocks (and the launchers in them)
  not rendered while other columns render; the preset and `knownIds` still hold the columns afterwards; a shown host's
  blocks as today; `ids: []` → no host block at all, the non-host columns still render; an unknown id listed shows
  nothing local; the terminated-pane picker omits the hidden host. Commit.
- **T2 — the landing helper.** Tests: shown → `false`, nothing done; hidden local host → Hosts tab opened / focused,
  `activeHostId` = the host, returns `true`, no tab of that host created; an unlisted `d1_X` → Hosts tab opened,
  `activeHostId` unchanged, `true`, no tab; a listed `d1_X` → `false` (openable: the pane shows `MissingHostPane` as
  today); `''` → Hosts tab, `true`. Commit.
- **T3 — notifications still fire, clicks land on the Hosts page.** Tests: an agent notification of a hidden host is
  dispatched (the not-filtered half); its `open-session` click opens the Hosts page for that host, creates no tab, does
  NOT focus an existing tab of that session, still marks it read; shown host unchanged. Commit.
- **T4 — execution deep link and route.** Tests: `resolveDeeplink` / `openExecutionDetailTab` for a hidden host →
  Hosts page, no execution tab; the `/execution/<host>/<execution id>` route likewise, and a hostless route whose
  fallback `hostOrder[0]` is hidden; **`/execution/d1_X/<execution id>` with `d1_X` not a local host and not listed →
  Hosts page, no tab** (the review's case), and the equivalent deep link `{ executionId, host: 'd1_X' }` likewise;
  with `d1_X` listed → the tab as today; shown host → the tab as today. Commit.
- **T5 — in-flight handoffs** (review item 2). Tests (`handoff.test.ts`, daemon calls mocked with a deferred promise):
  for each of `handToNex`, `takeBack`, `takeToTerminal` — start the call with the host shown, hide it
  (`setHostShown(host, false)`) before the deferred answer resolves → the tab store is the SAME object after
  completion (no `trySetPaneContent` write), the outcome has `swapped: false`, the daemon call happened once; the same
  with the host still shown → the pane re-pointed as today. `HandoffConfirmDialog.test.tsx`: hidden at click →
  `handToNex` not called, the dialog closes; hidden during the flight → the toast has no "open execution" action;
  shown at completion then hidden before the toast's action is clicked → the click opens the Hosts page on that host,
  no execution tab (`openSingletonTab` not called with an execution). Commit.

Invariants: no New Tab / picker / landing / toast path creates or focuses a tab whose pane the gate would hide; every
opener evaluates `isRefShown` (one rule — a non-local unlisted ref is not openable); a handoff answered after its host
was hidden re-points no pane; a notification of a hidden host is always delivered; the New Tab layout data is never
changed by the filter.

Mutations: M1 `NewTabPage` removes the column from the preset instead of skipping it (preset-kept test red); M2 the
filter compares local ids (daemonId-host test red); M3 the dispatcher drops notifications of hidden hosts (delivery
test red); M4 the click focuses the existing tab (no-focus test red); M5 the route ignores the fallback host
(hostless-route test red); M6 the landing treats a non-local ref as shown (the `/execution/d1_X/…` route and deep-link
tests red); M7 one of the three handoff functions writes the pane without the re-check (its hide-during-flight test
red — run once per function); M8 the toast opener opens the execution without the re-check (toast test red).

## H2d-4 — pane gate, per-pane sweeps, StatusBar, re-show recovery (16 files; §0.21)

The rule of §0.21 — in W, a pane on a hidden host renders 「主機已於此工作台關閉」 and opens no connection; showing the
host again restores the same pane — plus the per-pane sweeps and the StatusBar peer info that would otherwise keep
talking to X about those panes, and the recovery on show. Host-level connections (event WS, health, session watch /
refresh, the `sessions` reconcile itself) are NOT touched (H2d-5 T1). Uses the H2d-1 matcher (`usePaneHostShown`,
`isRefShownNow`, `isRefShown`).

Files (counted at `6858fb1f`):
1. `spa/src/components/PaneLayoutRenderer.tsx` (the leaf gate; the nex `ensure` effect keyed on the shown host)
2. `spa/src/components/PaneLayoutRenderer.test.tsx`
3. `spa/src/components/HostHiddenPane.tsx` (new — the placeholder: 「主機已於此工作台關閉」 with the host's label, one hint
   line "Show it from the Hosts page" with a link opening that host's page; rendered and tested through file 2, no own
   test file)
4. `spa/src/components/StatusBar.tsx` (`usePeerInfo(null, null, …)` for a hidden pane, as for a terminated one,
   `StatusBar.tsx:300-305`)
5. `spa/src/components/StatusBar.test.tsx`
6. `spa/src/lib/rebuild/revive.ts` (`runRevivePass(hostId)`: early return when hidden, next to `canAttachTerminal`,
   `revive.ts:171-172`)
7. `spa/src/lib/rebuild/revive.test.ts`
8. `spa/src/lib/rebuild/cwd-probe.ts` (`probeMissingCwds(hostId)`: early return, `cwd-probe.ts:128`)
9. `spa/src/lib/rebuild/cwd-probe.test.ts`
10. `spa/src/lib/rebuild/reconcile-host.ts` (`provenanceBindings(hostId, …)` returns `[]` for a hidden host,
    `reconcile-host.ts:37-58` — feeds both provenance triggers, so `useMultiHostEventWs.ts` is unchanged)
11. `spa/src/hooks/useMultiHostEventWs.provenance-probe.test.ts` (both triggers, through the real hook)
12. `spa/src/locales/en.json` (`pane.host_hidden.title`, `pane.host_hidden.hint`, `pane.host_hidden.open_hosts`)
13. `spa/src/locales/zh-TW.json`
14. `spa/src/lib/rebuild/host-reshow.ts` (new — `startHostReshowRecovery()`)
15. `spa/src/lib/rebuild/host-reshow.test.ts` (new)
16. `spa/src/main.tsx` (one install line, next to `startStandaloneAdoption()`)

The gate (`PaneLayoutRenderer`, leaf branch, `:67-90`):
- `const hostShown = usePaneHostShown(leafContent)` (constant `true` for split nodes and non-host-bearing leaves), read
  BEFORE the nex subscriptions: `tmuxHostId` used by `ensure` / `selectHandoffReady` is `null` while hidden — no
  `ensure` fetch, no "Hand to nex" item;
- in the leaf branch, before `resolvePaneRenderer`: `!hostShown` → `HostHiddenPane` (with the pane header / context
  menu as for any leaf, so the pane can be closed or detached by hand) instead of the module component; nothing below
  mounts — no `SessionPaneContent` (no probe effect, no `TerminatedPane` Rebuild / picker, no `TerminalView` / ticket /
  WS), no `ExecutionPaneWrapper` (no history fetch, attach, SSE, lease, `CostPanel` fetch);
- live: a shown-list write or a daemonId learned re-renders the leaf (no reload); on show the renderer mounts fresh —
  same pane id, same content, new xterm (scrollback before the gate lost — accepted, §0.21);
- the gate never writes the tab store; the module-enabled `pinnedEnabled` snapshot (`:43`) is unchanged.

Re-show recovery (`host-reshow.ts`): a subscriber over `useShownHostsStore` + `useHostStore` computes the set of LOCAL
host ids shown (`isRefShown`); the local ids hidden before and shown now are grouped by wire identity
(`wireIdOfHost`), and each group gets ONE `recoverHostSessions(id)` call, for its first id in `hostOrder` (review
`task-mufjfxo4-h4e2rf` item 5: two local rows claiming one daemon are shown together and are one daemon — two calls
would refresh it twice). The transition is tracked per local id and only the call is deduped, so a daemonId learned
(the host's wire id moves, its local id stays shown) is still no transition. A host merely added (hidden, rule 2), a host removed, or a daemonId learned (the local-id form keeps it shown — §0.21) is
not a transition. It never writes the tab / workspace / shown stores. The baseline is taken after both stores have
hydrated (the `onFinishHydration` pattern of `startHostReresolve`), so the boot hydration of a stored list is not a
transition. If it needs more than "subscribe + call the existing function", or pushes this PR past 20 files, STOP and
report.

Tasks:
- **T1 — the gate.** Tests (`PaneLayoutRenderer.test.tsx`, real `useShownHostsStore` / `useHostStore`, module
  renderers registered as spies): a tmux leaf on hidden X → the placeholder text (en), the terminal renderer never
  mounted, `fetchWsTicket` not called, `useNexHostStore.ensure` not called, no "Hand to nex" item; a terminated X tmux
  leaf → placeholder, no Rebuild button, no picker; an execution leaf with `host: X` → placeholder, the execution
  renderer not mounted; a HOSTLESS execution leaf with `hostOrder[0]` = X → placeholder; a pane on an unresolved
  `d1_X` not listed → placeholder (the one wire-space predicate), listed → `MissingHostPane` as today; a
  daemon-source editor on X → renders normally (§0.22); X shown → every renderer as today; in a split `[mlab | X]`
  only the X leaf is gated and the mlab renderer stays mounted through hide and show (mount / unmount spies: no
  remount); **live**: hide X while mounted → the renderer unmounts and the placeholder shows, the tab store is the SAME
  object (no write); show X → the renderer mounts again with the SAME `pane.id` and content (`toBe` on the pane
  object), `fetchWsTicket` called once. **The lease, for real (no new file):** the execution renderer registered for
  these cases is a small test component calling the REAL `useExecutionLease(hostId, executionId)`, with
  `lib/nex/nex-api` module-mocked (`attachControl`, `releaseLease`, `renewLease` spied) and the lease seeded through
  `useExecutionStore.setLease`: lease held → hiding X calls `releaseLease` exactly once with X, the execution id and
  the lease id; no lease held → not called; `releaseLease` rejecting → the placeholder still renders, no unhandled
  rejection; while hidden `attachControl` is never called; after show it is still not called until the test calls
  the hook's `ensureLease()` (the lease is lazy). Locale keys (en + zh-TW). Commit.
- **T2 — StatusBar.** Tests: the active tab's primary pane on hidden X → `usePeerInfo` receives `null` host / code (no
  `usePeerStore.refresh`, no `useSessionCwdStore.refresh` call) and no peer row renders; X shown → as today; terminated
  behaviour unchanged. Commit.
- **T3 — the sweeps.** Tests: `runRevivePass(X)` with X hidden and a revivable pane → no `repointPane` (tab store
  unchanged), X shown → the pane is revived (as today); `probeMissingCwds(X)` hidden → no `probeSessionCwd` call, shown
  → one per binding; through the real hook (`useMultiHostEventWs.provenance-probe.test.ts`): a `sessions` frame and a
  `hook` event for hidden X → no provenance probe, while the `sessions` frame IS reconciled (a vanished session marks
  its pane terminated) and `openAttachGate(X)` is called; X shown → probes as today. Commit.
- **T4 — re-show recovery.** Tests (`host-reshow.test.ts`, real stores, `recoverHostSessions` spied): `ids: ['d1_m']`
  → `show(d1_x)` → one call with X's local id; the same write again → no call; X shown → hidden → no call; a
  `settings` apply path (store `setState` as `apply-to-stores` does) showing X → one call; a host added (hidden) → no
  call; a daemonId learned for a host shown under its local id → no call; a boot hydration of a stored list holding X
  → no call; **two local rows of one daemon (a conflict pair) shown by one write → ONE call, with the id first in
  `hostOrder`** (and, the pair hidden and shown again, one more call); two different daemons shown by one write → one
  call each; installing twice → one call per transition. Integration
  (same file, `refresh-sessions` real, `fetch` mocked): a pane on X marked terminated while X was hidden whose session
  is alive → after `show`, within the same test tick sequence, the pane is repointed live (revive ran), no `sessions`
  frame needed; with the operation lock held the call defers to its release. Install in `main.tsx`. Commit.

Invariants: in W, no pane on a hidden host mounts a renderer, fetches a ticket, opens a terminal WS or an execution
stream, calls the nex `ensure`, or is probed / revived by a sweep; hiding and showing never write the tab, workspace or
local-profiles store (pane id and content survive); other panes of a split tab are never remounted by it; host-level
connections and the `sessions` reconcile are unchanged; a non-host-bearing pane is never gated; a show — local or
synced — recovers the host's sessions once per daemon.

Mutations: M1 the gate placed in `SessionPaneContent` (execution / hostless-execution / ensure tests red); M2 the gate
reads a snapshot like `pinnedEnabled` (live hide / show test red); M3 the gate answers a non-local ref "shown" (a
"not a local host → `true`" helper; `d1_X` test red);
M4 the `ensure` effect keyed on `tmux?.hostId` regardless of the gate (ensure-not-called test red); M5 show rewrites
the pane (new id / `detachPane`) or hide removes it from the layout (same-pane / tab-store-same-object tests red); M6
the whole tab gated instead of the leaf (split test red — mlab remounted); M7 StatusBar still passes the host to
`usePeerInfo` (peer-refresh test red); M8 the revive guard moved to `reconcileHostSessions` (direct `runRevivePass`
test red); M9 `provenanceBindings` unguarded (hook-event test red); M10 the guard also skips `reconcileHostSessions`
(terminated-marking test red); M11 recovery called from the switch's click handler only (the apply-path test red);
M12 `runRevivePass` called instead of `recoverHostSessions` (the lock-held defer test red); M13 the gate keeps the
execution renderer mounted and only hides it (the real-lease `releaseLease`-once test red); M14 no baseline after
hydration (boot-hydration test red); M15 recovery not deduped by wire identity (the conflict-pair one-call test red).

## H2d-5 — hidden ≠ absent: behaviour tests for the not-filtered list (5 files, tests only)

Files (all new):
1. `spa/src/lib/shown-hosts.not-filtered.connection.test.tsx`
2. `spa/src/lib/shown-hosts.not-filtered.tabs.test.tsx`
3. `spa/src/lib/shown-hosts.not-filtered.fallbacks.test.tsx`
4. `spa/src/lib/shown-hosts.not-filtered.newtab.test.tsx`
5. `spa/src/lib/shown-hosts.import-guard.test.ts`

Every case sets `{ ids: [<wire id of mlab>] }` so air26 (a local host with a daemonId) is HIDDEN, and asserts air26
behaves exactly as with `{ ids: [<mlab>, <air26>] }` (`it.each` over both settings, results compared). One behaviour
test per "unchanged" row of the §0.21 table:

- **T1 — connections and health** (file 1): `useHostConnection` runs for air26 and updates `runtime[air26].status`;
  `useMultiHostEventWs` opens air26's event WS (the `WebSocket` mock sees its URL) and reconciles its `sessions` event
  (session store and the panes' terminated marks identical in both settings); `useSessionWatch` / session refresh
  (`lib/rebuild/refresh-sessions.ts`) fetches air26's sessions. The per-pane sweeps (revive, cwd / provenance probes)
  are NOT compared — they skip a hidden host by design (H2d-4 T3). Commit.
- **T2 — tabs stay** (file 2): an air26 tmux tab and a split `[mlab | air26]` tab stay in the tab bar (`SortableTab` /
  `InlineTab` rendered) with their badge (`useTabHostBadge` non-null); keyboard next / previous tab and "close others"
  treat them like any tab; hiding air26 (`setHostShown`) and applying a `settings` payload that hides it leave the tab,
  workspace and local-profiles stores the SAME objects and every `tabs.*` / `workspaces` section hash unchanged (real
  builders) — nothing closes, splits or moves. The panes themselves are NOT compared (the gate renders
  `HostHiddenPane` — H2d-4). Commit.
- **T3 — fallbacks and direct navigation** (file 3): with air26 as `activeHostId` / `hostOrder[0]`:
  `nex/resolve-host.ts` returns air26 for a hostless id; the fs backends resolve air26; `backup-auto-trigger` targets
  air26; `HostPage` at `/hosts/<air26>/overview` renders `OverviewSection`; `DevEnvironmentSection` lists air26 in its
  picker. Commit.
- **T4 — New Tab registration and layout** (file 4): the provider sources still return air26's `sessions:` /
  `headless:` providers; `getStaleNewTabProviderIds` does not report their ids; `useNewTabBootstrap` keeps the columns
  in every preset and in `knownIds`; the next `settings` build carries them unchanged. Commit.
- **T5 — import guard** (file 5, the static half; scan of import declarations in non-test files):
  `useShownHostsStore` may be imported only by `lib/shown-hosts.ts`, `stores/useHostStore.ts`, `lib/host-reresolve.ts`,
  `lib/profile/collector.ts`, `lib/profile/apply-to-stores.ts` and `lib/rebuild/host-reshow.ts`; `lib/shown-hosts`
  only by the H2d-2 / H2d-3 / H2d-4 production files (`HostSidebar.tsx`, `OverviewSection.tsx`,
  `SessionsSection.tsx`, `ExecutionsView.tsx`, `NexExecutionsTable.tsx`, `NewTabPage.tsx`, `SessionPickerList.tsx`,
  `useNotificationDispatcher.ts`, `deeplinkResolver.ts`, `useRouteSync.ts`, `handoff.ts`, `HandoffConfirmDialog.tsx`,
  `PaneLayoutRenderer.tsx`, `StatusBar.tsx`,
  `revive.ts`, `cwd-probe.ts`, `reconcile-host.ts`, `host-reshow.ts`) and `host-reresolve.ts`; none of those files
  imports a tab-closing API (`closeTabInWorkspace`, `closeTab`, `useTabStore.getState().closeTab`) that it did not
  import at `6858fb1f` (the static half of "hiding never closes"). Commit.

Invariant: every "unchanged" row of §0.21 has a behaviour test that is identical with the host hidden and shown;
hiding never writes a tab-world store.

Mutations (each a one-line change in production code, reverted after): M1 `useMultiHostEventWs` skips hidden hosts (T1
red); M2 `useHostConnection` skips them (T1 red); M3 `useTabHostBadge` returns null for a hidden host (T2 red); M4 the
tab bar filters tabs of hidden hosts (T2 red — the rev-3 model must not come back); M5 a `useShownHostsStore.subscribe`
that closes tabs of a hidden host (T2 same-object test red — the rev-6 model must not come back); M6
`nex/resolve-host` skips hidden hosts in its `hostOrder[0]` fallback (T3 red); M7 the provider sources filter hidden
hosts (T4 red); M8 `getStaleNewTabProviderIds` reports a hidden host's column (T4 red); M9 import the store in
`useSessionWatch` (T5 red); M10 `useMultiHostEventWs` skips the `sessions` reconcile for a hidden host (T1
terminated-mark comparison red).

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

After H2d-4 (H2d-1 … H2d-4 released together; H2d-5 is tests only). Two clients with independent host ids (A =
`-s=host-ownership-a`, B = `-s=host-ownership-b`, each added mlab and air26 through its OWN UI, daemonIds verified —
§0.8), a fresh test workbench W on mlab, A master, B attached. Record before step 6: the `settings` and every
`tabs.<ws>` / `workspaces` rev on the SOT; air26's session list (`GET /api/sessions` on `100.64.0.4:7860`, auth header
from a variable).

6. **Ship state = everything hidden.** Load the H2d build on both clients (from a build where W already had tabs on
   mlab and air26 — open them on the H2c build first: T1 = an air26 tmux session `acc-s1` alone, T2 = split
   `[acc-m1 (mlab) | acc-s2 (air26)]`, T3 = an air26 execution). After the reload: `settings.purdex-shown-hosts` on
   the SOT is `{ ids: [] }`; on both clients every tab is still in the tab bar with its badge; every mlab and air26 pane
   shows 「主機已於此工作台關閉」 (en: "This host is turned off in this workbench" — switch A's language once); New Tab
   has no `sessions:` / `headless:` block for either host (the layout editor still lists the columns); the
   terminated-pane picker offers nothing; the Hosts sidebar lists both hosts, muted, tagged 「已隱藏」 / "Hidden", each
   page opens (overview, sessions list, settings), their session lists show no "open"; `playwright cli
   -s=host-ownership-a requests` (log cleared, each tab activated in turn) shows no terminal WS, no ticket, no execution
   history / SSE, no nex host fetch, no cwd / provenance probe for either host, while both hosts' event WS stay open.
   No `tabs.*` / `workspaces` rev moved.
7. **Show mlab and air26 by hand.** On A, each host's overview → "Show in this workbench" ON. SOT: `settings` moved
   (once per click), `ids` = `[d1_mlab, d1_air26]`, no `tabs.*` rev moved. Within a few seconds, without reload, on
   BOTH clients every pane of T1 / T2 / T3 attaches (same pane ids in `purdex-tabs` — saved to the scratchpad and
   compared by a script, print only "equal" / differing paths); New Tab shows both hosts' blocks. Open a new tab on
   air26 (`acc-s3`) from New Tab to prove the path is back.
8. **Hide air26 on A.** Overview → OFF. On both clients: every tab stays (T1, T2, T3, `acc-s3` — same tab ids, same
   layout, T2 still a split); the air26 panes (T1, T2's right pane, T3, `acc-s3`) show the message; T2's mlab pane keeps
   working — type into it, the output appears, and its terminal WS is the SAME connection (`requests`: no new ticket /
   WS for `acc-m1` around the hide); `requests` (log cleared at the hide) shows for air26's panes no terminal WS, no
   ticket, no execution history / SSE, no nex fetch, no probes; with an air26 pane active the StatusBar shows no peer
   info (no peers / cwd request to air26). New Tab has no air26 block, the picker offers no air26, air26's Hosts page
   offers no "open" (sessions and Nex executions) and no Executions-view "open", its "new session" still works (and
   opens no tab). air26 stays connected (its status on its page, its event WS open). SOT after 5 s: `settings` moved
   once; no `tabs.*`, `workspaces` or `hosts` rev moved. air26's session list is unchanged (no `DELETE` to either
   daemon in `requests`).
9. **Notification and landings.** Trigger an agent notification in `acc-s1` on air26 (e.g. a short `claude -p` turn in
   that session ends) → the notification appears on A; clicking it opens air26's Hosts page, and T1 is neither
   focused nor duplicated. Navigate A to `/execution/<air26 id>/<T3's execution id>` (the parser's order —
   `lib/route-utils.ts:84-92`, canonical form `:148-149`) → air26's Hosts page, no new tab. Then `/execution/d1_ffff…/<any
   execution id>` (a wire id no host of A has and W does not list) → the Hosts page, no new tab.
10. **Show air26 again → restore without reload.** Keep `acc-s2` idle during the hide (create / kill no session on
    air26, so no `sessions` frame is pushed). On A → ON. Within 5 s on BOTH clients (B through the `settings` apply)
    every air26 pane attaches — the same pane ids, the terminal shows the session's current screen (earlier scrollback
    gone — expected), T3 re-subscribes; `requests` shows one `GET /api/sessions?fresh=1` to air26 per client after the
    switch; no pane in W shows Rebuild.
11. **A host only B knows (the #1421 regression, on devices).** Needs host lists that differ per device, i.e. H3
    released (before H3 the `hosts` section brings every host to both clients). With H3: on B add a third host `h3`
    (e.g. air19 `100.64.0.1:7860`) that A never adds, show it on B → the SOT's `ids` = `[d1_mlab, d1_air26, d1_h3]`;
    on A hide mlab → `ids` = `[d1_air26, d1_h3]` (`d1_h3` kept although A does not know it); on B `h3` is still shown
    (New Tab block present, its panes attach) and mlab is hidden. Before H3: the same property through an injected id
    (see "Not reachable before H3" below). Show mlab again on A.
12. **A newly added host is hidden on both.** Add a host neither client has had in W (air19 or a temporary daemon) on
    A: on A it is hidden (tag, no New Tab block), `settings` rev unchanged; on B — through `hosts` before H3, or added by
    hand after H3 — it is hidden too, and still nothing is written. (A host whose wire id W already lists — shown there
    from another device — appears shown on the device that adds it: the list belongs to the workbench, §0.7.)
13. **Parked worlds.** On A switch to a local profile (slave) holding its own air26 tab; hide air26 → the slave's air26
    pane shows the message, nothing closes; switch back to the master → its air26 panes show the message too (the list
    is device-global); show air26 → both worlds' panes attach when on screen. No `tabs.*` rev moved by any of it.

Not reachable before H3 (the `hosts` section still syncs — same reason as H1 plan §0.9):
- A client that lacks a host the workbench has a look / shown id for (step 11 before H3): inject into W's SOT a
  `settings` payload (current rev as base, `PUT /api/profiles/{id}/sections/settings`) whose `looks` and `ids` carry
  an extra `d1_ffff…` → both clients keep it through apply and their next build (`settings` rev moves only by the
  injection); a pane injected on that id shows the hidden placeholder while unlisted and `MissingHostPane` while
  listed (§0.30 item 3, DECIDED); a hide / show of mlab keeps `d1_ffff…` in `ids`.
- "B deletes a host, A unaffected": pre-H3 A loses the host through `hosts`; what IS checked is that the look entry
  and shown id survive on both.
- Independent host lists with different add orders / names per device: the `HostConfig` fallback differs per device
  only after H3; here both fallbacks are equal, so steps 2–4 prove the look path through the `hosts`-rev-unchanged
  check instead.

Cleanup: `playwright cli -s=host-ownership-a close`, `-s=host-ownership-b close`, `-s=host-ownership-c close` (same
cwd), delete W, stop :5175.

## Review

Entries below rev 7 are history: where they describe the `{ all, ids }` store, the closing model (plan / executor /
dialog / split / parked-world close / rollback / `superseded` / `unsettled`) or rev 6's H2d numbering, rev 7 replaces
them (header "Superseded", §0.6, §0.7, §0.21 – §0.30, H2d-1 … H2d-5).

### 2026-09-24 — rev 7: the user's final rules on shown hosts (rev 6 → rev 7)

Why: the user confirmed five rules directly (header "User rules"; spec §1.2). They replace the closing model that rev 4
introduced from a relayed description — hiding a host now closes nothing: a tab stays, only its panes on the hidden
host are gated, and showing the host restores them in place. The "exclude model" (a list of HIDDEN ids) relayed by a
peer earlier was not the user's decision either and is not planned.

What the attacker on PR #1421 found (H2d-1 as coded for `{ all, ids }`): the first toggle from `all: true`
materialised "all" into the ids THIS device knows (`toggle(wireId, knownWireIds)`), so a host only another device knows
was dropped from the synced list — hidden on that device although nobody hid it. The plain list (§0.6) makes every
write touch exactly one host's ids; H2d-1 T2 carries the two-client regression.

What changed:
- store `{ ids: string[] }`, default `[]` = every host hidden, no `all` / `showAll` / `setShown` / `addShown`; actions
  `show` / `hide` / `toggle` / `rekey`; the writer `setHostShown` removes both forms of a host (wire id and local id),
  and the matcher accepts both forms (a host shown before its daemonId was learned stays shown until the re-key);
- every host starts hidden, existing hosts at ship time included — no seeding migration (§0.7);
- the switch lives on the Hosts page overview, the hidden style on the sidebar; no Settings › 工作台 editor (§0.17);
- H2d re-planned as five PRs: H2d-1 (19, reworked in place on #1421), H2d-2 Hosts page (12), H2d-3 New Tab / picker /
  landings (12), H2d-4 pane gate / sweeps / StatusBar / re-show recovery (16), H2d-5 tests only (5) — 64 files
  (rev 6: 78 in seven PRs); one bump for H2d-1 … H2d-4;
- acceptance rewritten: ship state all hidden, show by hand, hide keeps every tab and gates only that host's panes (a
  split's other pane keeps its connection), show restores without reload, the host-only-B-knows case, a new host
  hidden.

Dropped: the confirmation dialog (`HostDisableDialog`), the planner / executor (`planHostDisable`, `applyHostDisable`),
`lib/host-disable-world.ts`, `splitOutPanes` / `insertTabsAfter`, the four-store snapshot / rollback, the settled check
and the `unsettled` / `busy` / `changed` / `superseded` results, closing in parked worlds, `ShownHostsBlock`, §0.23
"kept (locked)", §0.24 – §0.26, §0.28. Kept (adapted): the pane gate at `PaneLayoutRenderer`'s leaf branch, the
per-pane sweep guards inside `runRevivePass` / `probeMissingCwds` / `provenanceBindings`, the StatusBar peer-info skip,
the expected `releaseLease` on unmount of a gated execution pane, `recoverHostSessions` on the hidden → shown
transition from a store subscriber, the blocked open-a-tab surfaces, the notification / deep-link / route landings,
the not-filtered behaviour tests and the import guard.

Open questions: §0.30 (file panes; unknown ids have no UI; which placeholder for a host this device lacks, and the
local-id form; H2d-1 size vs the 800-line target; copy) — all DECIDED by the review below.

### 2026-09-24 — rev 7 plan review `task-mufjfxo4-h4e2rf`

codex plan review of rev 7 (`3d49d278`), seven findings; triage by the coordinator: 1, 2, 4, 5, 6, 7 ADOPTED; 3
DOCUMENTED + test.
1. [important 0.98] An unresolved wire ref bypassed the opener gate: rev 7's `isHostShown` said `true` for a non-local
   id while the pane matcher said hidden for an unlisted `d1_…`, so `/execution/d1_X/<id>` created a tab that the gate
   then placeholdered — ADOPTED. One predicate for every opener and the gate: `isRefShown` (wire-space; the local-id
   form for a local host). A ref that is neither local nor listed is not openable → the Hosts page (landings) / not
   offered (lists). `isHostShown` / `useIsHostShown` / `useShownHostFilter` removed (§0.21 "One rule for every opener
   too"; H2d-1 selectors, T3 tests, M8; H2d-3 file 5, T2, T4 with the `/execution/d1_X/<id>` route and deep link, M6).
   Code refs re-verified at `3d49d278`: `lib/nex/resolve-host.ts:11`, `lib/deeplink/deeplinkResolver.ts:18`,
   `hooks/useRouteSync.ts:105-110`.
2. [important 0.96] Handoff / Take back / Take to terminal write the pane after the daemon answers, and the Handoff
   toast can open an execution tab; hiding the host during the flight did not stop either — ADOPTED into H2d-3 (+4
   files, 12 → 16): the re-check `isRefShownNow(hostId)` before each `trySetPaneContent` (`lib/nex/handoff.ts:100`,
   `:160`, `:240`; functions at `:83`, `:146`, `:204`), the dialog's confirm (`HandoffConfirmDialog.tsx:39`) and the
   toast's opener (`:46-54`); the entries themselves are unreachable through the H2d-4 gate. T5, M7 / M8.
3. [important 0.94] Concurrent toggles of different hosts on two devices overwrite each other — evidence against it as
   a new defect: `settings` syncs under per-section CAS (`applier.ts:560`, `sync-state.ts:58`), so the second push is
   `locked:conflict` and a human picks a side, as for every settings store. DOCUMENTED in §0.6 and §0.21 step 4;
   H2d-1 T2 adds the conflict test (`executor.direction.integration.test.ts`).
4. [important 1.00] §0.30 kept the local-id form open although the model decided it — ADOPTED: removed from the
   questions; it is part of the model (§0.21 "What is a pane on X").
5. [minor 0.91] Two local rows of one daemon → `recoverHostSessions` twice on one show — ADOPTED: the re-show
   subscriber tracks transitions per local id and calls once per wire identity (first id in `hostOrder`); H2d-4 T4
   conflict-pair test, M15.
6. [important 1.00] Acceptance step 9 had the route segments reversed — ADOPTED: `/execution/<host>/<execution id>`
   (`lib/route-utils.ts:84-92`, canonical `:148-149`), plus an unlisted `d1_…` route.
7. [minor 0.99] The H2d-1 800-line question was still open — DECIDED: one PR. Every §0.30 item is DECIDED with its
   default (file panes not gated; unknown ids have no UI; a pane on a host this device lacks: unlisted → the hidden
   placeholder, listed → `MissingHostPane`; copy 「已隱藏」 / 「在此工作台顯示」 / 「主機已於此工作台關閉」).

H2d totals after this review: H2d-1 19, H2d-2 12, H2d-3 16, H2d-4 16, H2d-5 5 — 68 files.

### 2026-09-24 — rev 6 plan review `task-mufbtxpn-8p1egj`

codex plan review of rev 6 (`116b2806`), five findings; triage approved by the coordinator.
1. [critical 0.98] The executor had no cross-store rollback — ADOPTED. Snapshot of the four stores before the first
   write; any throw → all four restored, `{ kind: 'failed', rollbackFailed?: true }`, lock released in `finally`, the
   action never throws. Nothing is pushed: the body is synchronous and the collector's 500 ms debounce builds the
   restored state → hashes unchanged. Pattern: `commitTabWorld` (`master-world.ts:221-248`) and H1b's `commitAll` /
   `rollback-failed` (`lib/host-reresolve.ts` on `origin/main`). Tests per write point + a throw inside the rollback;
   M19 / M20 (§0.21 steps 4–7, H2d-2 API / T3; H2d-3 dialog copy, T3, M10).
2. [critical 0.96] A standalone mixed tab's survivors were left in no workspace ("a tab in no workspace enters no
   section", `sections.ts:374`) — ADOPTED. Same body: survivors go to the original's owner workspace; with none,
   `repairTabOwnership(…, { only: original + survivors })` adopts them first. **Correction to the triage:** the rule
   is not "active → first → Unsorted" — it is `adoptStandaloneTabs`' (`sections.ts:404-420`): the workspace with id
   `unsorted`, else the first named `t('workspace.unsorted')`, else a new `unsorted` workspace. Tests incl. the
   `tabs.<ws>` build and the reload / switch round trip; M21 / M22 (§0.21 "Two editors", H2d-2 T3 / T5).
3. [critical 0.93] No Web Lock → a stale local-profiles snapshot overwrites a world another window just parked —
   EVIDENCE AGAINST, design kept. Parking always moves the epoch (`openEpoch`, `switch-active.ts:185-189`;
   `swapActive` requires the next epoch, `useLocalProfilesStore.ts:446-463`); a write below the fence is refused and
   the store rehydrated (`lib/storage/world-fence.ts:224-228` — the file is under `lib/storage/`). What remains is
   the epoch-free class (rename, appearance, add / copy / delete / reorder slaves, `replaceParkedWorld` in another
   window between read and write) = last writer wins = #1256. Added: the residual written out (§0.21 "No Web Lock"),
   a post-write fence re-read reporting `applied` + `superseded: true`, and the H2d-2 T3 fence test; M23.
4. [important 0.94] H2d-3 T4 tests the add-host dialog without its test file — ADOPTED: `AddHostDialog.test.tsx`
   listed, H2d-3 10 → 11.
5. [important 0.91] The lease release was asserted only through an unmount spy — ADOPTED, no new file: H2d-4b T1
   mounts the real `useExecutionLease` with `lib/nex/nex-api` mocked (release once / none / rejecting; no
   `attachControl` while disabled nor after re-enable until the user acts); M13.

File counts: H2d-2 9 (unchanged; more tests in its existing files — if the added rollback / adoption code and tests
take it past 800 lines, stop and report before splitting), H2d-3 10 → 11, H2d-4b 16; H2d total 77 → 78.

### 2026-09-24 — H2d pre-measurement and coordinator decisions (rev 5 → rev 6)

Read-only measurement at H2c-1 head `2efaade2` (≈ main `1e3d1812`). Plan statements that did not match the code, and
where each is fixed:
1. §0.27 "respects the world fence (… as `promoteToMaster` / `copyMasterAsSlave` do)" — the cascade path checks no
   fence and `promoteToMaster` takes the async world lock; the check is written new, synchronous, `copyMasterAsSlave`
   style (§0.27 note, §0.21 step 1).
2. `closeTabInWorkspace` + `visitHistory` fallback and `splitOutPanes` / `insertTabsAfter` are live-store only; parked
   worlds have no `tabOrder` / `visitHistory` and no parked close / split exists → new pure
   `lib/host-disable-world.ts` (§0.21 "Two editors", H2d-2 files 4–5).
3. `planSignature` "tab ids + closing pane ids" is not unique across worlds → owner-keyed entries and signature
   (§0.21, H2d-2 API / T1, M15).
4. "Several `set()` calls on three stores" → four stores (+ `useLocalProfilesStore`); the three world stores are
   fenced (§0.21 "Four stores").
5. No unsettled check for the on-screen world although its stores are fenced → the settled check covers the whole
   action (§0.21 step 1, H2d-2 T3, M13 / M14).
6. "`SessionPaneContent` renders the placeholder" misses the execution pane, the nex `ensure`, the mount probes and the
   terminated branch → gate in `PaneLayoutRenderer`'s leaf branch (§0.23 note, H2d-4b, H2d-6 T2).
7. H2d-4's 14 files hold no gate file although H2d-6 T2 said the gate is built there → own PR H2d-4b (13 files).
8. H2d-1's `isHostShown` returns `true` for a non-local id; the gate needs the wire-space matcher → `hostRefOf` /
   `wireOfRef` / `isPaneHostEnabled` / `usePaneHostEnabled` / `isHostRefEnabledNow` in `lib/shown-hosts.ts` (H2d-1,
   +0 files; T3, M8 / M9).
9. §0.21 "session watch / refresh … unchanged" vs §0.23 "opens no connection": the revive / cwd / provenance sweeps and
   the StatusBar `usePeerInfo` kept asking X about disabled panes → they skip them (§0.21 table, H2d-4b T2 / T3).
10. §0.21 "nothing in the action talks to a daemon": closing / gating an execution pane with a lease fires its
    best-effort `releaseLease` on unmount → reworded; expected (§0.21 "X offline is fine").

Coordinator decisions (header list, rev 6): (1) no Web Lock — synchronous settled check first, `{ kind: 'unsettled',
reason }` writes nothing, the residual accepted and commented citing #1256; (2) the pane gate is its own PR, H2d-4b;
(3) host-level connections unchanged, per-pane sweeps and StatusBar peer info skip disabled hosts' panes (H2d-4b).
File counts: H2d-2 7 → 9, H2d-3 10, H2d-4b 13 (new; 16 after the re-enable recovery decision), H2d total 59 → 77 (then H2d-3 → 11, total → 78 by the rev 6 plan review above); the §0.27 "stop and report" gate is passed.
Open (not decided here): H2d-4b's re-enable does not re-run the revive pass (see the end of H2d-4b).

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
