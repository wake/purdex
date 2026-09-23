# Interface-only tabs stay on the device — spec + plan

Status: draft 1 · 2026-09-23 · branch `worktree-tabs-local-only` · base `dd9f401e` (alpha.437)

## 1. Problem

Profile Sync sends every tab of a workspace (`tabs.<ws>`). Tabs that only show the app's own interface — a New Tab
page, Settings, the hosts page — then appear on every device, and one device closing its Settings tab closes it
everywhere. They carry no state worth sharing; each device should keep its own.

## 2. Decisions (user, 2026-09-23 — not to be reopened)

1. **Device-local pane kinds**: `new-tab`, `settings`, `dashboard`, `hosts`, `history`, `memory-monitor`,
   `editor-buffers`. Every other kind syncs as today (`tmux-session`, `browser`, `editor`, `image-preview`,
   `pdf-preview`, `execution`). A `new-tab` pane that is launched (`setPaneContent` replaces its content in place,
   same tab id) starts syncing from then on.
2. **Split tabs**: a tab syncs as a whole when at least ONE leaf is not device-local (its `new-tab` leaves travel
   with it as empty panes); only a tab whose EVERY leaf is device-local stays on the device.
3. **Old clients are locked out**: a new `WIRE_MARKERS.tabs` entry and `SECTION_SCHEMA_ORDINAL.tabs` 2 → 3.
   Without it an old client would apply a payload that omits its own interface tabs and delete them.

## 3. Design

### 3.1 One predicate (sections.ts)

`DEVICE_LOCAL_PANE_KINDS: ReadonlySet<PaneContent['kind']>` — the seven kinds of §2.1 — and
`isSyncableTab(tab: Tab): boolean` = some leaf of `tab.layout` has a kind outside that set (a leaf-walk; a split
with no leaves, or a missing layout, is not syncable). A kind added later syncs unless it is listed — the safe
default for data, and a type-level test lists every `PaneContent['kind']` in exactly one of the two sets so a new
kind cannot be added without a decision.

### 3.2 The build leaves them out (sections.ts `buildTabsSection`, ~219)

`order = uniqueKnown(ws.tabs, id => Object.hasOwn(tabs, id) && isSyncableTab(tabs[id]))`; the record holds exactly
those. The collector, the document and the post-apply hash all go through this one builder, so they follow.

### 3.3 The apply keeps them (applier.ts `applyTabs`, ~383)

Precedent: device-local workspace ids (`applyWorkspaces`). The round trip `hash(build(apply(local, p))) ===
hash(p)` holds because the build filters them out again.

Let `keptLocal` = the tabs of the target workspace (`target.tabs`, existing in `local.tabs`) that are NOT syncable
and NOT in the incoming order. Then:

- **not removed**: `removedTabIds` excludes them; their `Tab` stays in the record untouched.
- **stay in `ws.tabs`, at their relative place**: each one is anchored to the nearest tab BEFORE it in the local
  `target.tabs` that is in the incoming order; with none, it goes to the front. The new `ws.tabs` is: the unanchored
  ones (local order), then for every id of the incoming order that id followed by the ones anchored to it (local
  order). They must stay in `ws.tabs`, or `repairTabOwnership` (sections.ts ~374) would adopt them into Unsorted.
- **`activeTabId` may stay on one**: kept when it is in the incoming order OR in `keptLocal`; otherwise as today
  (`order[0]`, now the first of the merged `ws.tabs`).
- A device-local tab that IS in the incoming order (a payload built before this change, §3.5) is applied like any
  arriving tab.

Other workspaces are untouched as today (`withoutTabs` only removes ARRIVING ids).

### 3.4 A deleted section keeps them (apply-to-stores.ts ~679)

`payload === null` is applied as `EMPTY_TABS`; with §3.3 the workspace's device-local tabs survive (only its
synced tabs go). Decided: keep — nothing on another device can mean "delete my Settings tab".

### 3.5 Wire marker + ordinal (projections.ts)

`WIRE_MARKERS.tabs` gains `'@tabs:device-local=v1'`, `SECTION_SCHEMA_ORDINAL.tabs` = 3 (comment: interface-only
tabs are left out of the build and kept on apply). The projection does not change. `compareShape`: an old client
(ordinal 2) facing a new SOT → `sot-is-newer` → schema lock (it writes nothing, so it cannot delete anything); a new
client facing an ordinal-2 SOT → `i-am-newer` → it applies it. Such a payload may still list interface tabs: they
arrive as ordinary tabs, the next build leaves them out, the section is pushed once without them (one
`pull-hash-mismatch` on that device — the migration), and every new client then keeps its copy as device-local.
The shape-table snapshot test is updated.

### 3.6 Behaviours pinned by tests, no code change

- Closing the launched leaf of a split so that only `new-tab` leaves remain → the tab leaves the payload → other
  devices delete their copy (their copy is still syncable there). Expected.
- A synced tab whose content becomes device-local on this device (e.g. its only leaf turned into `settings`) leaves
  this device's payload and is deleted on the others.
- `openSingletonTab` for Settings finds only this device's own Settings tab (each device has its own; none
  arrives).
- `host-identity` is untouched: the device-local kinds carry no host id.

## 4. Not in scope

Per-pane (rather than per-tab) sync; syncing a device-local tab's position relative to synced tabs on OTHER
devices; the daemon.

## 5. Plan (TDD, one commit each; gates: `cd spa && npx vitest run`, `pnpm run lint`,
`npx tsc --noEmit -p tsconfig.app.json`)

**T1 — `isSyncableTab`.** Tests: each device-local kind alone → false; each syncing kind alone → true; split of
`new-tab` + `tmux-session` → true; split of `settings` + `hosts` + `new-tab` → false; nested splits; the
exhaustiveness test over `PaneContent['kind']`. Mutation: drop one kind from the set → red.

**T2 — the build.** Tests: a workspace with [tmux, settings, new-tab, browser] builds order [tmux, browser] and a
record of exactly those; an all-device-local workspace builds `{order: [], tabs: {}}`; a split with a new-tab leaf
+ tmux is sent whole (new-tab leaf included). Mutation: remove the filter → red.

**T3 — the apply.** Tests: (a) local [s1, L1, s2, L2], incoming [s2, s3, s1] → ws.tabs [s2, L2, s3, s1, L1],
removedTabIds [], L1/L2 records untouched; (b) a local-only tab before every synced one stays at the front; its
anchor removed → it moves to the nearest earlier surviving anchor; (c) `activeTabId` on L1 stays; on a removed
synced tab → first of the merged list; (d) incoming lists a device-local-kind tab (old payload) → applied as an
arriving tab; (e) null payload (EMPTY_TABS) → only synced tabs removed; (f) round trip `hash(build(apply(local, p)))
=== hash(p)` over (a)–(e) and the existing property test's generator extended with device-local tabs; (g) after
the apply, `repairTabOwnership` adopts nothing. Mutations: count device-local as removed → (a) red; append them
at the end instead of anchoring → (a) red; reset activeTabId → (c) red.

**T4 — marker + ordinal.** Tests: shape-table snapshot (tabs fingerprint changed, ordinal 3); `compareShape` old
vs new both ways; a new client applies an ordinal-2 payload listing a `settings` tab, then pushes the section
once without it.

**T5 — end to end (two devices, FakeDaemon harness).** A has [tmux, settings, new-tab]; A pushes; B sees only the
tmux tab. A launches its new-tab into a tmux session (`setPaneContent`) → B gets that tab. B's own Settings tab
(between two synced tabs) survives A's next push at the same relative place. A split [new-tab, tmux] reaches B
whole; A closes the tmux leaf → B's copy is deleted. Each device's Settings singleton is its own.

## 6. Real machine

Two clients with independent host ids, own profile name. A opens New Tab / Settings / Hosts tabs → none appears on
B. A launches a tmux session from a New Tab → B gets that tab. B's own Settings tab stays after A pushes, at the same
place. A split with one tmux leaf reaches B; a split of Settings + New Tab does not. Clean up: close wizards, Stop
sync on both, REST DELETE the profile.
