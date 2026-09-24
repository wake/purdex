# Per-workbench shown hosts + the three create buttons — spec delta and plan

Date: 2026-09-25. Spec owner: coordinator mlab/purdex-13 (user: "規格由你送審，其他 session 只跑執行"). Executor: mlab/purdex-48.
Status: **FINAL** — user decisions §0 and §1.4 answered; codex plan review task-muft8kwp-9pyicy handled in §7.

## 0. The user's decisions (2026-09-25, final — do not reopen)

1. "Which hosts are shown" is kept **per workbench**. The master's list syncs to other devices; a local workbench's
   list exists only on this device; switching workbenches switches the list; turning a host on / off in one
   workbench never touches another. **Host looks (name / colour / icon) stay one per device** (as today).
2. Duplicating a workbench comes in two kinds: **"duplicate all"** = shown list + every workspace and tab (tmux bindings
   copied verbatim — both workbenches share the same sessions); **"duplicate settings only"** = the shown list only, one
   empty workspace, no tab.
3. **"New blank workbench"** = shown list empty (0 hosts); the user turns hosts on in Settings.
4. The user's model (restated 2026-09-25): the **host list** is one per device and moves only by the one-time
   transfer code — whichever workbench syncs, the host list is the same device list. The **shown-hosts list** is one
   per workbench; whichever workbench is the master is the one whose list syncs.
5. Upgrade: EXISTING local workbenches start with **every host hidden** (`[]`) — hiding never closes a tab (H2 rule),
   so turning a host on brings its panes back.
6. Promote: **the list follows the workbench** — the workbench made master syncs its own list.
7. UI: **three separate buttons** — "Duplicate all", "Duplicate settings only", "New blank workbench". Duplicates
   carry no name / icon / colour (existing rule).

## 1. Spec delta

### 1.1 host-ownership spec (`docs/specs/2026-09-23-host-ownership-spec.md`)

- §1.2 "Model": "the list syncs in `settings`" → "each workbench keeps its own list: the **master's** list syncs in
  `settings`; a local workbench's list is stored with that workbench on this device and never syncs".
- §2 table row "shown hosts": owner "workbench (master: `purdex-shown-hosts`; local workbench: its record in
  `purdex-local-profiles`)", synced "master only".
- §2 paragraph under the table ("…so the look and shown-hosts stores are one per device…"): the look store stays one
  per device; **the shown list is the exception** — one per workbench (decision 1).
- §4.1: `purdex-shown-hosts` holds the **master's** list (on screen or not). Add: `LocalProfile.shownHostIds:
  wireId[]` — same rules (plain list, unknown ids kept, `[]` = all hidden), sanitised with `sanitizeShownIds`, never
  projected.
- §8 H2: add "show a host in local workbench L on A → nothing changes in the master on A or anywhere on B; switch A
  to the master → the master's own list applies".

### 1.2 Profile Sync spec (`docs/specs/2026-09-20-profile-sync-spec.md`) §4.1 "slave"

"holding `workspaces` + tabs only. It borrows the master's hosts and settings" → "holding `workspaces` + tabs and its
own shown-hosts list; it borrows every other setting of the master (host looks included)".

### 1.3 H2 plan (`2026-09-24-host-ownership-h2-plan.md`)

Not rewritten (as-built record). A one-line note at the top of §0.17 / §0.21 item 5 / §0.27 / acceptance 13:
"superseded 2026-09-25 by per-workbench lists — see 2026-09-25-per-workbench-shown-hosts-plan.md".

### 1.4 User decisions (answered 2026-09-25 — see §0 items 5–7)

Q1 upgrade default = `[]` (§0.5). Q2 promote = the list follows the workbench (§0.6). Q3 = three buttons (§0.7).

## 2. Design (chosen: "Design 2") and why

- **The master's list stays in `useShownHostsStore`, always** — on screen or parked. Collector, apply-to-stores,
  master-world, projections, the settings ordinal and the wire markers are **unchanged**: they already read / write
  exactly the master's list. (Today a toggle made while a slave is on screen reaches the SOT; after this change it
  cannot, because a slave's toggle writes the slave record.)
- **A slave's list lives on its `LocalProfile` record** (`shownHostIds: string[]`), NOT inside `ParkedWorld`. A world
  switch therefore moves no list and the epoch fence stays three stores. Rejected "Design 1" (list inside
  `ParkedWorld`, swapped into the live store on each switch): the live store would have to join the world fence
  (tag in its persisted shape, `readMasterWorld` settled check, `openEpoch`, recover, `commitTabWorld` /
  `restampWorld` rollback) — otherwise a window that settles on "master on screen" before its shown store rehydrates
  pushes the slave's list to the SOT; five places build fresh `ParkedWorld` literals and would drop the field; ~30
  files.
- **Which list applies = the world the live tab stores hold**: `useTabStore.worldId` (the world tag every switch and
  promote stamps). `worldId === 'master'` → `useShownHostsStore.ids`; a slave id → that slave's `shownHostIds`;
  unknown id → `[]` (fail closed: gated, never connected). Keyed on the tab tag, not `activeProfileId`, so panes are
  judged by the list of the world they belong to while another window's switch rehydrates store by store.
- **Fail closed (codex #4)**: the reader returns `[]` (every host hidden → gated, no connection) whenever it cannot
  be sure which list applies: `useTabStore.worldId` differs from `useLocalProfilesStore.activeProfileId` (a
  cross-window switch / promote mid-rehydrate), or any of the tab / local-profiles / shown-hosts stores has not
  hydrated. A hidden pane opens no connection (spec §4.5), so a transient `[]` can only delay a connection, never
  open a wrong one. Tested for every rehydrate order of a promote (A2).

## 3. PR split (≤ 20 files each — codex #8; the executor measures the real list before each PR and splits
further at a task boundary if needed)

- **Docs PR** (no code, no codex): this plan + the spec delta §1.1–§1.3.
- **PR-A1 `worktree-per-workbench-shown-hosts`**: A1 store field, A2 reader (fail closed), A3 writer + promote,
  A7 copies' list plumbing.
- **PR-A2** (stacked on A1): A5 re-key, A6 reshow, A8 locale copy.
- **PR-B = `worktree-profile-create-buttons`** (merges origin/main after A2): the three buttons.
- PR-A1 + PR-A2 + PR-B ship in ONE bump (a slave list that nothing can set, or buttons without per-workbench lists,
  is not a release).

## 4. PR-A tasks (TDD; one commit per task; `git commit --only`)

A1 **Store field** — `useLocalProfilesStore.ts`: `LocalProfile.shownHostIds: string[]`; `sanitiseSlave` keeps
   `sanitizeShownIds(v.shownHostIds)` when it is an array and **defaults to `[]`** when the persisted record has
   none (§0.5 — this IS the upgrade; no separate step). **Every path that rebuilds a `LocalProfile` keeps the list**
   (codex #1, critical): `setProfileAppearance` (~:417), rename, `sanitiseSlave`, `addSlave`, `promoteSlave` — a
   regression test per path (appearance patch, rename, reload) asserts the same array survives; `addSlave(name, world, shownHostIds = [])`; new action
   `setSlaveShownHosts(id, fn: (ids) => string[]) → {ok} | not-found` (no-op when unchanged: same reference, no set).
   The persisted-keys test (:608) is unchanged (field is inside `slaves`). Import `sanitizeShownIds` from
   `useShownHostsStore` (pure function; check the import guard allows a store→store type/function import, else move
   `sanitizeShownIds` to `lib/shown-hosts-ids.ts`).
A2 **Reader** — `lib/shown-hosts.ts`: `currentShownIds(tabWorldId, local, shownIds)` pure + `currentShownIdsNow()`;
   every function that read `useShownHostsStore.ids` (`isRefShownNow`, `useIsRefShown`, `useShownRefFilter`,
   `usePaneHostShown`, `landOnHostsPageIfHidden`) reads the current list instead. Hooks select primitives / the
   stable array reference (the slave's `shownHostIds` array or the store's `ids`), never a fresh array per render.
   Update `shown-hosts.import-guard.test.ts` `STORE_IMPORTERS` if needed. No consumer file changes.
A3 **Writer + promote** — `setHostShown`: target = current world by the tab tag; master → today's code; slave →
   `setSlaveShownHosts`; slave record missing → no-op, return `false` (signature becomes `boolean`; the Hosts page
   switch ignores it). Promote (`relabel` in `switch-active.ts`, Q2=a): in the same synchronous block, the
   promoted slave's list → `useShownHostsStore.setState({ids})`, the demoted slave's record gets the old store ids;
   rollback restores the store (extend `captureLocal`/`restoreLocal` or a local undo). Store-level `promoteSlave`
   takes the demoted list as an argument so the record is written in its one `set`.
   **Two-store write, defined (codex #3)**: capture both before-states (local-profiles `slaves` + shown-store `ids`)
   before the first write; write local-profiles, then the shown store, then `restampWorld`; if any write throws
   (including a persist `setItem` after the in-memory set), restore every touched store in reverse; if a restore
   itself throws, report a distinct `rollback-incomplete` outcome (not swallowed — `restoreLocal` today swallows) and
   leave a problem the UI shows. Failure-injection tests: local write fails / shown write fails / `restampWorld`
   fails, and each of their rollback writes failing.
A4 **(dropped)** — Q1 = `[]` makes the upgrade a `sanitiseSlave` default (A1); codex #2 (cross-key upgrade race)
   no longer applies. **Known residual (codex #6, accepted)**: a window still running the pre-upgrade build that writes
   `purdex-local-profiles` drops `shownHostIds` → the next read defaults to `[]` = every host hidden in that local
   workbench (fail closed, no tab closed). Both clients upgrade together (same :5174), as for every schema bump.
A5 **Re-key** — `rekeyShownHosts` also re-keys each slave's `shownHostIds` with the same moves. It must NOT plan the
   slave write from the pre-state (the 'parked worlds' write of `planRewrite` also sets `slaves`): its commit reads
   `useLocalProfilesStore.getState().slaves` AT COMMIT, maps, sets, and remembers that value for its undo; undos run
   in reverse, so the parked-worlds undo still restores the pre-state. Deletion (`rewriteHostRefs`) never re-keys lists.
   **Two stores, one step (codex #5)**: the step captures BOTH before-states (master `ids`, `slaves`), writes both,
   and its own undo restores both — including when its own commit threw after the first write; a failing undo reports
   through the pass's existing `rollback-failed` path (H1b). Tests: second write fails → both restored; undo fails →
   `rollback-failed` + retry.
A6 **Reshow** — `host-reshow.ts` snapshots with the current list and also subscribes to `useLocalProfilesStore`
   (the current slave's list) and to `useTabStore` `worldId` changes; hydration gate covers **four** stores — host, shown-hosts, local-profiles, tab (codex #7: identity resolution
   needs the host store); a test per hydration order. A world
   switch that turns host X from hidden to shown recovers X's sessions (one call per local row, unchanged rule).
A7 **Copies' list plumbing** — `addCopyAsSlave(name, source, tabOrder, shownHostIds)`: `saveScreenAsSlave` passes the
   CURRENT list; `copyMasterAsSlave` (wizard) passes `useShownHostsStore.ids` (the master's list). `copyWorld` unchanged.
A8 **Docs + copy** — spec delta §1.1–§1.3; `hosts.shown.switch_hint` en / zh-TW: "Applies to the workbench on screen.
   The workbench master's list syncs to your other devices; a local workbench's list stays on this device." (zh:
   「只套用在畫面上的工作台。工作台主檔的清單會同步到其他裝置；本機工作台的清單只留在這台。」); `settings.profile.local.desc`
   en and zh-TW made to say the same thing ("keeps workspaces, tabs and its own shown hosts; stays on this device;
   host looks and other settings are shared with the master").

Expected PR-A files (~18): useLocalProfilesStore.ts/.test, shown-hosts.ts/.test, shown-hosts.import-guard.test,
switch-active.ts/.test, host-reresolve.ts? (only if A5 needs it; the step lives in shown-hosts.ts)/host-reresolve
.integration.test, host-reshow.ts/.test, new `lib/profile/shown-hosts-upgrade.ts`/.test, main.tsx, en.json, zh-TW.json,
2 spec files + this plan, host-overview switch test if the return type matters. Over 20 → split A8 docs into a docs PR.

Mutation (delivery item): (m5) `setProfileAppearance` drops the list → A1 red; (m6) promote skips the rollback of
the second store → A3 red; (m7) reader not fail closed on worldId ≠ activeProfileId → A2 red; (m8) re-key undo
restores one store → A5 red; (m9) reshow gate without the host store → A6 red; (m1) reader ignores the tab tag (reads the store always) → A2 tests red; (m2) slave toggle
writes the store → A3 red + a collector test "slave toggle builds no new settings hash" red; (m3) re-key plans the
slave write from the pre-state → A5 integration red; (m4) promote leaves the list in the slot → A3 red.

## 5. PR-B tasks (buttons branch, after PR-A2 merges and origin/main is merged in)

B1 `createBlankSlave` → `addSlave(name, world, [])`; comment "Hosts and settings are the device's" rewritten.
   `createSettingsCopySlave(name)` = the blank world (one empty workspace, no tab) + a copy of the CURRENT list.
   "Duplicate all" = today's copy of the screen world + a copy of the CURRENT list.
B2 UI (§0.7): three separate buttons "Duplicate all" / "Duplicate settings only" / "New blank workbench", each with
   the existing name field flow; tests pin: duplicate-all copies workspaces + tabs + list (tmux bindings verbatim);
   settings-only has one empty workspace + list; blank has `[]`; the screen never moves; no name/icon/colour copied.
B3 en / zh-TW labels (zh: 「全部複製」「只複製設定」「新增空白工作台」); `local.desc` en/zh consistency test.

## 6. Real-device acceptance (two clients, independent host ids — feedback_acceptance_distinct_host_ids)

A and B attached to one master; hosts mlab, air26 shown in the master.
1. A: duplicate-all → L1. Switch to L1: same tabs, live (shared sessions). Hide air26 in L1 → L1's air26 panes gated;
   switch to master → air26 live; B unchanged (master list untouched on SOT).
2. A: settings-only → L2: one empty workspace; L2's list = the list of the screen at copy time.
3. A: blank → L3: every host hidden; New Tab offers no host; turn mlab on in L3 → only L3 changes.
4. B: hide mlab in master → A's master follows; A's L1/L2/L3 unaffected.
5. Reload A on L1 → list kept. Promote L1 (detached) → per Q2.
6. Upgrade: a pre-upgrade build with a slave that has tabs on mlab → upgraded → that slave shows every host hidden;
   its tabs stay (placeholder "主機已於此工作台關閉", no connection); turning mlab on in it brings the panes back live;
   the master's list unchanged.
7. Promote a slave with only air26 shown → the SOT `settings` now carries that list; the demoted workbench keeps
   the old master list.

## 7. Codex plan review task-muft8kwp-9pyicy (8 findings) — handled

1 critical `setProfileAppearance` drops the list → A1 every rebuild path + m5. 2 critical cross-key upgrade race →
moot (Q1 = `[]`, A4 dropped). 3 promote two-store rollback → A3 defined + failure injection + m6. 4 cross-window
promote may open a wrong connection → §2 fail closed + m7. 5 re-key two-store atomicity → A5 + m8. 6 old-shape
window overwrite → accepted residual (A4 note; fail closed). 7 reshow gate needs the host store → A6 four stores + m9.
8 file count → §3 split (docs / A1 / A2 / B).

