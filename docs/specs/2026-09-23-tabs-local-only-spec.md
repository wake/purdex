# Interface-only tabs stay on the device — spec + plan

Status: rev 2 (codex plan review task-mue2c375-akwp64: 1 critical + 5 important + 2 minor, all adopted — §3.3
duplicates / `__proto__`, §3.5 legacy upcast on pull AND restore-local, §3.6 global active tab, §3.7 executor
placeholder, §3.8 resolve counts, T6 collector) · attacker R2 (A: a malformed tab is not device-local, §3.1; B: id conflict → the remote wins, §3.3) · 2026-09-23 · branch `worktree-tabs-local-only` · base `dd9f401e`
(alpha.437)

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
`isDeviceLocalTab(tab)` = `tab.layout` is COMPLETE — the tabs guard's own shape rule, `isLayoutShape` (a local
split's `sizes` admitted, the builder strips them) — AND every leaf's kind is in that set; `isSyncableTab = !isDeviceLocalTab`.
A malformed tab (no layout, unknown node, empty split, leaf without pane / content) is therefore NOT device-local:
it goes to the build exactly as before this feature, instead of becoming a ghost no payload can delete while the
section reads as converged (attacker R2, finding A). It reads only `pane.content.kind`, so it works on a local `Tab` and on a wire tab entry alike (the wire
layout only rewrites host ids). A kind added later syncs unless it is listed — the safe default for data — and a
type-level test puts every `PaneContent['kind']` in exactly one of two lists, so a new kind needs a decision.

### 3.2 The build leaves them out (sections.ts `buildTabsSection`, ~219)

`order = uniqueKnown(ws.tabs, id => Object.hasOwn(tabs, id) && isSyncableTab(tabs[id]))`; the record holds exactly
those. The collector, the document and the post-apply hash all go through this one builder.

### 3.3 The apply keeps them (applier.ts `applyTabs`, ~383)

Precedent: device-local workspace ids (`applyWorkspaces`). `incoming` here is already canonical (§3.5).

`keptLocal` = the ids of `unique(target.tabs)` (first occurrence wins, as the builder and the incoming order do)
that are not `PROTO_KEY`, exist as an own property of `local.tabs`, are NOT syncable, and are NOT in the incoming
order. Then:

- **not removed**: `removedTabIds` excludes them; their `Tab` stays in the record untouched.
- **stay in `ws.tabs`, at their relative place**: each is anchored to the nearest id BEFORE it in `unique(target
  .tabs)` that is in the incoming order; with none it goes to the front. New `ws.tabs` = the unanchored ones (local
  order), then for every id of the incoming order: that id, then the ones anchored to it (local order). No id
  appears twice (both lists are unique and disjoint). They must stay in `ws.tabs`, or `repairTabOwnership`
  (sections.ts ~374) would adopt them into Unsorted.
- **`Workspace.activeTabId`**: kept when it is in the incoming order OR in `keptLocal`; otherwise the first id of
  the new `ws.tabs`.
- Other workspaces are untouched as today (`withoutTabs` removes only ARRIVING ids). A device-local id that is
  listed by two workspaces locally is a pre-existing ownership violation: each workspace's apply keeps its own
  listing, and `repairTabOwnership` resolves it exactly as it does today — this apply adds no adoption and no drop.

**Id conflict** (attacker R2, finding B). An id is this device's to keep only while the SOT does not carry it.
When the incoming payload holds the same id as a device-local tab here, that entry is necessarily syncable (the
payload is canonical, §3.5), and the remote version wins wherever the local one sits:

- same workspace → it is simply arriving (`keptLocal` excludes arriving ids): the record is the incoming one;
- another workspace → `withoutTabs` takes it out like any arriving tab (that workspace's `activeTabId` moves by
  `withoutTabs`' rule) and the record is overwritten.

It happens when a tab once synced everywhere was turned into an interface tab on this device while another device
moved it to another workspace or kept editing it. The version kept is the one with state (session, URL, file), not
the stateless interface page, and only this way does the round trip hold for the incoming payload. Pinned by
applier.test.ts `applyTabs — a device-local tab is kept where it was` (i) same workspace and (j) across workspaces.

**Round trip.** `hash(build(apply(local, p))) === hash(p)` for every `p` the NEW builder can produce — i.e. every
canonical payload (no device-local tab in it). A legacy payload is made canonical first (§3.5).

### 3.4 A deleted section keeps them (apply-to-stores.ts ~679)

`payload === null` is applied as `EMPTY_TABS`; with §3.3 the workspace's device-local tabs survive. Decided: keep.

### 3.5 Legacy payloads: marker, ordinal, upcast (projections.ts, applier.ts, executor.ts)

`WIRE_MARKERS.tabs` gains `'@tabs:device-local=v1'`; `SECTION_SCHEMA_ORDINAL.tabs` = 3 (comment: interface-only
tabs are left out of the build and kept on apply). The projection does not change. An old client facing a new SOT
→ `sot-is-newer` → schema lock (it writes nothing). A new client facing an ordinal-2 SOT → `i-am-newer` → applies.

`upcastLegacyTabs(payload): TabsPayload` (applier.ts, next to `upcastLegacySettings`): the payload without its
device-local tabs (order and record); the SAME object when it has none. Used at both places a payload of an older
build can reach the stores:

- **pull** — `applyTabsSection` (apply-to-stores.ts) upcasts `incoming` before `applyTabs`. A legacy device-local
  tab therefore never arrives on a device that does not have it; one it already has is device-local there and kept.
  The rebuild then equals the upcast payload, not the SOT's: the section is pushed once without them.
- **restore-local** (codex critical) — `restoreLocal` (executor.ts ~1525) upcasts a `tabs.*` snapshot exactly as
  it does `settings`: the hash of the upcast becomes `restoredHash`, it is what is applied, stashed and pushed.
  Without it a persisted keep-local choice from before the upgrade would push the old payload under ordinal 3.

**Not a problem.** That one push-back after a pull is the migration, not "the stores did not keep what arrived".
The ok arm's `aliasesOnly?: true` (#1369) becomes `rewrite?: 'aliases' | 'device-local-tabs'`; the tabs apply sets
`'device-local-tabs'` when the upcast removed something and the rebuild's hash equals the upcast payload's hash.
The executor records `pull-hash-mismatch` only when `mismatch && outcome.rewrite === undefined`.

### 3.6 The global active tab (apply-to-stores.ts ~683, master-world.ts)

The commit re-points `useTabStore.activeTabId` through `repointActiveTab`. Required and tested on both commit paths
(the world on screen and a parked master): a global active tab that is a kept device-local tab stays active; one
that was a removed synced tab moves as today (to the workspace's pointer).

### 3.7 The executor's "not arrived yet" placeholder (executor.ts ~67–81, ~1166)

A workspace whose tabs are all device-local builds the empty payload. Before this client agreed on the section
(`base.hash === null`) while the SOT has content, that report is "not arrived" and the section is pulled — the pull
brings the synced tabs and keeps the device-local ones. Once agreed, an empty report is an edit (the user closed
the last synced tab) and is pushed. No code change; both pinned by tests, plus: a held placeholder settles when the
index lands, and launching the only New Tab of such a workspace turns it into an ordinary non-empty report.

### 3.8 Resolve counts (components/settings/profile/resolve-counts.ts)

The Resolve dialog counts `payload.tabs` keys on both sides; the SOT side of a legacy payload would count interface
tabs this device's side no longer has. Both sides are counted after `upcastLegacyTabs`.

### 3.9 Behaviours pinned by tests, no code change

- Closing the launched leaf of a split so only `new-tab` leaves remain → the tab leaves the payload → other devices
  delete their copy. Expected.
- `openSingletonTab` for Settings finds only this device's own Settings tab.
- `host-identity` is untouched: the device-local kinds carry no host id.

## 4. Not in scope

Per-pane sync; the position of a device-local tab on OTHER devices; the daemon.

## 5. Plan (TDD, one commit each; gates: `cd spa && npx vitest run`, `pnpm run lint`,
`npx tsc --noEmit -p tsconfig.app.json`; every mutation backed up and `cmp`-checked before and after)

**T1 — `isSyncableTab`.** Each device-local kind alone → false; each syncing kind alone → true; split new-tab +
tmux → true; split settings + hosts + new-tab → false; nested splits; a wire entry; the exhaustiveness test.
Mutation: drop one kind from the set → red.

**T2 — the build.** [tmux, settings, new-tab, browser] → order [tmux, browser], record exactly those; all
device-local → `{order: [], tabs: {}}`; split new-tab + tmux sent whole. Mutation: remove the filter → red.

**T3 — the apply (pure).** (a) local [s1, L1, s2, L2], incoming [s2, s3, s1] → [s2, L2, s3, s1, L1], removed [],
L records untouched; (b) a local-only tab before every synced one stays first; its anchor removed → the nearest
earlier surviving anchor; (c) `Workspace.activeTabId` on L1 stays, on a removed synced tab → first of the new list;
(d) duplicates: local [s1, L, L, s2] → L once; L also listed by another workspace → both keep it, no new
duplicate; (e) an own `__proto__` device-local record → neither in `ws.tabs` nor the record (as today);
(f) EMPTY_TABS → only synced tabs removed; (g) round trip over canonical payloads, with the existing property
generator extended with device-local tabs; (h) `repairTabOwnership` after the apply adopts and drops nothing new.
Mutations: count them as removed → (a) red; append instead of anchoring → (a) red; reset activeTabId → (c) red.

**T4 — marker, ordinal, upcast.** Shape-table snapshot (tabs fingerprint changed, ordinal 3); `compareShape` both
ways; `upcastLegacyTabs` (same object when clean); `applyTabsSection` with an ordinal-2 payload listing a settings
tab this device lacks → not created, `rewrite: 'device-local-tabs'`; one this device has → kept; executor: no
`pull-hash-mismatch` for `rewrite`, one push without the tab; `aliasesOnly` → `rewrite: 'aliases'` everywhere
(existing #1369 tests follow); restore-local of a persisted ordinal-2 tabs snapshot with a settings tab →
`restoredHash` is the upcast's, the push carries no settings tab. Mutations: skip the pull upcast → red; skip the
restore upcast → red.

**T5 — stores + executor.** `applySectionToStores('tabs.<ws>')` on the screen world and on a parked master: global
active tab on a kept device-local tab stays; placeholder cases of §3.7 (no base + SOT content → pulled, locals
kept; base → empty report pushed; held placeholder settles on index; launching the only New Tab → ordinary
report); resolve counts after upcast.

**T6 — collector + end to end (two devices, FakeDaemon harness).** Collector: closing only a device-local tab → no
report; `setPaneContent` on a New Tab → a report of the new hash. End to end: A has [tmux, settings, new-tab]; A
pushes; B sees only the tmux tab. A launches its New Tab → B gets that tab. B's own Settings tab between two synced
tabs survives A's next push at the same place. A split [new-tab, tmux] reaches B whole; A closes the tmux leaf →
B's copy is deleted. Each device's Settings singleton is its own.

## 6. Real machine

Two clients with independent host ids, own profile name. A opens New Tab / Settings / Hosts tabs → none appears on
B. A launches a tmux session from a New Tab → B gets that tab. B's own Settings tab stays after A pushes, at the same
place. A split with one tmux leaf reaches B; a split of Settings + New Tab does not. Clean up: close wizards, Stop
sync on both, REST DELETE the profile.
