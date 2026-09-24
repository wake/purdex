# Spec — host ownership: hosts belong to the device, their look belongs to the workbench

Status: user decisions 2026-09-23 and 2026-09-24 (below are not to be re-litigated). Revised 2026-09-24 after the
codex spec review (§9); shown hosts revised 2026-09-24 to the user's final rules (§1.2, §4.1, §4.5, §7 H2d, §8).
Supersedes kickoff decision 7 ("host token in the profile") and the host half of decision 9 ("the master is the only
source of hosts"). Terms (zh-TW): profile = 工作台, master = 工作台主檔, slave = 本機工作台,
a workbench's settings = 工作台設定檔.

## 1. Decisions (user, 2026-09-23)

1. **The host list is device-level.** Which daemons a device reaches — the list, order, ip, port, token, daemonId —
   is the device's, and is NOT carried by Profile Sync.
2. **Hosts move between devices by a one-time transfer code, relayed through any host both devices share** (not
   necessarily the dev host). The receiver chooses per host and a mode: add new only / overwrite the same daemon /
   replace all. Only the short relayed code — no self-contained long code.
3. **A host's look (name, icon, colours) follows the workbench** — synced by Profile Sync, keyed by the daemon's wire
   id (`d1_…`), exactly as today's user experience.
4. **A workbench has a synced "shown hosts" setting.** Hosts not listed still exist and stay connected; they are only
   hidden in that workbench. ~~Unset = all shown.~~ **Superseded 2026-09-24 (§1.2):** empty = every host hidden; a
   new host is hidden. Ids this device lacks are kept, never pruned.
5. **Daemon-side settings (projects, commands, resume templates, daemon config, hooks, monitor, peers) are already
   shared by every client** through the daemon — no sync, no change.

### 1.1 Decisions (user, 2026-09-24)

6. **The relay daemon is trusted (H4).** Every host is the user's own. The relay holding the shared hosts' tokens in
   plain text, in memory only, for the code's TTL is acceptable. No end-to-end encryption, no PAKE. The UI MUST say
   so plainly (§6.1).
7. **Old clients are not supported; devices upgrade together.** H2 locks old clients out through the `settings` wire
   marker + ordinal (`locked:schema`); there is NO hosts↔looks dual-write bridge. H3 cannot guarantee a lock for a
   profile whose SOT has only a `hosts` section and no `tabs.*` / `settings` written by a new client — that is a
   documented known limitation (§5.4), not solved by a fencing write.
8. The terms above (profile = 工作台 …) stand.
9. **Deleting a host affects only this device.** A local host deletion rewrites this device's references to that
   host back to its wire id (the inverse of §3.3): its panes show the local-only "this device has no host ‹name›",
   no `terminated: 'host-removed'` mark is written, no tab is closed, and its `purdex-host-settings` entry, New Tab
   columns and look are kept. Other devices are not affected at all. H4's replace-all removes hosts the same way
   (§3.4).

### 1.2 Decisions (user, 2026-09-24, shown hosts — final; supersede decision 4's "unset = all shown", §4.1's
`ids: null` and the filter model of §4.5)

10. **Hosts management page** lists EVERY host. Per the current workbench: a shown host renders as today; a hidden
    host renders in a "hidden" style (muted, a 「未啟用／已隱藏」-type state), and can still be clicked and managed
    (overview, sessions list, settings, etc.).
11. **A newly added host is hidden in every workbench**; the user turns it on by hand. When the feature ships, every
    existing host is hidden too (no migration seeding the list with the current hosts).
12. Showing / hiding only changes presentation on the management page; its real effect is to **restrict the
    workbench's tab ↔ tmux session link**.
13. **Hiding a host never closes or changes tabs.** An open tab stays; each pane on the hidden host shows
    「主機已於此工作台關閉」 (en: "This host is turned off in this workbench") and opens no connection. Turning the host
    back on restores the same pane (reconnects, no reload). A split tab is not split and not restored: only the
    pane(s) on the hidden host show the message; closing that pane is up to the user.
14. **Block New Tab and every related way to open a tab** on a hidden host (New Tab sessions / headless blocks, the
    terminated-pane session picker, Hosts page session "open", executions "open", deep links / notifications that
    would open a tab — landing on the host's management page instead is fine).

Model: each workbench keeps a plain shown list of wire ids; no "all" flag; showing / hiding adds / removes exactly that
host; unknown ids are kept; empty = every host hidden. **Each workbench keeps its own list** (2026-09-25,
`2026-09-25-per-workbench-shown-hosts-plan.md` §0): the **master's** list syncs in `settings`; a local workbench's list
is stored with that workbench on this device and never syncs.

## 2. Where each piece of host data lives afterwards (measured on alpha.439)

| Data | Owner | Synced? |
|---|---|---|
| host list, `hostOrder`, ip, port, token, daemonId, `activeHostId`, `devHostId`, `syncAliases` | device (`purdex-hosts`) | no — transfer code only |
| `HostConfig.name` / colours / icon | device — the FALLBACK look only (§4.2) | no (after H3) |
| name, colours (`colors`, legacy `color`), icon, iconWeight | workbench: `purdex-host-looks`, keyed by wire id | yes (`settings`) |
| shown hosts | workbench (master: `purdex-shown-hosts`; local workbench: its record in `purdex-local-profiles`), wire ids | master only (`settings`) |
| `purdex-host-settings` (editor.homePath), New Tab `sessions:` / `headless:` columns | workbench | yes (unchanged) |
| pane host references in `tabs.*` | workbench | yes (unchanged) |
| projects, commands, resume templates, daemon config, hooks | daemon | no (already shared) |

Settings stores are device-global (one set per device, synced with the master's `settings`; only workspace-scoped
entries follow workspaces — `master-world.ts`), so the look store is one per device like every other settings store.
**The shown list is the exception** (2026-09-25, per-workbench plan §0.1): one per workbench — the master's in
`purdex-shown-hosts`, each local workbench's on its own record.

The wire identity (`d1_…` from the daemonId, else the local id) stays: tabs and settings still name hosts across
devices. Only the `hosts` SECTION and what exists solely to carry or reconcile it goes (§5 lists exactly what).

## 3. Host references this device cannot resolve (H1)

Today the lists agree, so every incoming reference resolves. With per-device lists they will not — these traps must
be fixed BEFORE hosts stop syncing.

### 3.1 Traps

1. **`applyTabsSection` brands panes of hosts unknown here as `terminated: 'host-removed'`** and that synced mark is
   pushed back (`apply-to-stores.ts` `markHostRemovedPanes`, called at the tabs apply). One device lacking host X
   would mark every live X pane removed on every device. → The apply no longer marks anything (§3.2).
2. **The settings apply prunes New Tab columns of hosts not here** (`applier.ts` `settingsFromWire`, the
   `liveHostIds` filter) **and so does the registry**: `createHostSessionProviderSource` /
   `createHeadlessProviderSource` `ownsId` claim every `sessions:*` / `headless:*`, so `getStaleNewTabProviderIds`
   reports a column whose host is not here as stale once the source is ready, and `useNewTabBootstrap` prunes it —
   the prune is pushed. → §3.2.
3. **Gates:** `tabs.*` / `settings` wait for `hosts` settled — the `hosts` half of the gate goes in H3; `settings`
   keeps waiting for `workspaces`.

### 3.2 The rule: an unresolvable reference is kept as is

A host reference is **unresolvable** when the wire resolver (`wireResolverOf`) returns an id that is not a key of
`useHostStore.hosts` (the resolver already returns an unknown id unchanged — `makeWireResolver`). Every locally stored
host reference that is unresolvable is **stored verbatim** (the wire id, byte for byte) and **built verbatim** (the
builders already pass an unknown id through — `layoutToWire` / `hostSettingsToWire` / `presetColumnIdToWire`):

| Reference | Stored as | Behaviour while unresolvable |
|---|---|---|
| pane `tmux-session.hostId`, file-source `source.hostId`, `execution.host` (tab store AND every parked world) | the wire id | pane renders the local-only state "this device has no host ‹name›" (name: the look for that wire id (H2+), else the wire id); no `terminated` mark is written; rebuild / attach disabled |
| `purdex-host-settings.hosts` key | the wire id | kept; no host reads it |
| New Tab `sessions:<id>` / `headless:<id>` column | the wire id | kept in the store and the build; NOT stale, never pruned by apply or bootstrap; not rendered |
| look / shown-hosts entries (H2) | the wire id (these stores are wire-keyed anyway) | kept; unused until a local host has that wire id |

New Tab ownership (finding 2): host-bearing columns are no longer subject to stale pruning at all. The host sources
keep `ownsId` (so the layout editor knows the family), and the registry gains an explicit rule: an id of a
host-bearing prefix is never reported stale by `getStaleNewTabProviderIds`. The only id still pruned is the legacy
single `sessions` (via its migration). Rendering skips a column whose host is not a local host (and, H2+, a hidden
host — §4.5).

### 3.3 The re-resolve pass

Keeping a wire id verbatim is only half: when the host later arrives here, the reference must point at the local host.

- **Trigger:** the host identity signature changes (`identityOfSync(hosts).signature` — it moves on add, remove,
  daemonId learned/cleared, conflict entered/left) or a host's persisted `syncAliases` change. Subscribed once at boot;
  also run once after hydration.
- **Scope:** under the in-process operation lock (owner `host-reresolve`; not the world lock — plan §0.1), every world this device holds: the on-screen tab store,
  every parked world (`useLocalProfilesStore.updateParkedWorlds`), `purdex-host-settings`, `purdex-newtab-layout`
  presets, and (H2+) the look and shown-hosts stores.
- **Rewrite:** each host reference whose id is not a local host is passed through the current `wireResolverOf`; if
  that yields a local host, the reference is rewritten to the local id (covers `d1_…` ids and legacy aliases). A
  reference that stays unresolvable is left untouched. Under an identity conflict (`wireResolverOf` → `null`) the
  pass does nothing and runs again when the conflict clears.
- **No-push invariant (tested):** for a `d1_…` reference, the build maps the new local id back through
  `identity.toWire` to the SAME `d1_…` id, so every section payload and hash is byte-identical before and after the
  pass, and nothing is pushed. A reference resolved through an alias (a legacy local id) IS canonicalised to `d1_…`
  by the next build — one push, exactly as a pull of that reference already does today.
- **New Tab placement race:** the pass is asynchronous (lock), so the bootstrap may see the new host before its
  column is rewritten. `ensureDefaults` treats a provider as already placed when the layout holds EITHER its id or
  `sessions:` / `headless:` + the provider host's wire id — no duplicate column.
- **Look re-key (H2+):** the same pass moves a look / shown-hosts entry keyed by a host's local id to its `d1_…` key
  when that host gains a daemonId (§4.3).

### 3.4 Local host deletion only affects this device (decision 9, H1c)

`deleteHostCascade` today closes the host's tabs (`closeTabs: true`) or writes the synced `terminated:
'host-removed'` mark (on screen and in every parked world), pins hostless execution panes to the removed host, and
deletes the host's `purdex-host-settings` entry — all synced, so the deletion reaches every device. New behaviour, for
every deletion path (Hosts page, H4 replace-all; before H3 also a `hosts` apply that drops a host):

- **One step under the operation lock** (not the world lock — plan §0.1; the Hosts page acquires it as `host-delete`,
  retrying every 250 ms for up to ~4 s, then gives up with a toast, and deletes only the host confirmed — gone,
  gone-and-back or re-pointed meanwhile → nothing deleted, a stale notice; the hosts apply runs it under its own
  grant): capture the host's wire id BEFORE removal — `wireIdOfHost(host)`: the sync id of its daemon
  (`syncIdOfSync(daemonId)`) when it has a valid `daemonId`, else its local id (plan §0.7). It reads that one row
  only, so it is the same under an identity conflict (two rows claiming one daemon), where `identity.toWire` leaves
  both duplicates out: a deleted duplicate's references get the shared `d1_…`, which names the survivor, and resolve
  to it once the conflict clears (with no conflict it equals `identity.toWire(localId)`); rewrite
  every reference to `localId` into `wireId` in the on-screen tab store, every parked world
  (`updateParkedWorlds`), `purdex-host-settings` keys and New Tab `sessions:` / `headless:` columns (a no-daemonId
  host: wire id = local id, nothing changes); THEN remove the host and clear this device's own per-host state
  (sessions, agent, execution view, runtime). A held lease is released best-effort only once the deletion has
  committed — to the endpoint pinned before removal; a rolled-back deletion releases nothing. The step is all or
  nothing: a failing write puts every store back. Look / shown-hosts entries are wire-keyed and are not touched.
- **Not done any more:** no `terminated` mark (on screen or parked), no tab close — the delete dialog loses its
  "close tabs" choice and says the tabs stay, shown as "no host here" on this device, and other devices are
  unaffected; no host-settings deletion; no look deletion.
- **No hostless special case.** Every path that creates an execution pane writes `host`
  (`useHeadlessLaunchSubmit.ts`, `ExecutionsView.tsx`, `nex/handoff.ts` `executionContentFor`, `useRouteSync.ts` via
  `resolveExecutionHostId`); `host === ''` exists only in legacy data from early versions. Deletion therefore pins
  nothing and writes nothing synced for it. Known behaviour: a legacy hostless pane afterwards looks up the same
  `executionId` on this device's new first host, and when that host does not have it the pane shows "not found" — a
  visible failure, never a silent attach to a different execution, which is the intent of the Nexen spec §4.3.2
  step 5 no-rebind rule.
- **No-push invariant (tested):** every section hash is identical before and after the deletion — the build mapped
  `localId` to `wireId` before; the stored `wireId` passes through unchanged after.
- **Undo** restores the host row (same local id, config, order, `activeHostId`) and the device-local stores it
  cleared (sessions, agent state) as today. It restores no tabs, workspace memberships or marks — none were touched.
  The identity signature changes, so the §3.3 pass rewrites every `wireId` reference (on screen and parked, whoever's
  world it is by then — a relabel since the deletion no longer matters) back to the local id. The `worldSkipped`
  result and the snapshot's `closedTabs` / `tabWorkspaces` / `terminatedTabPaneIds` go.
- **Existing `host-removed` marks** already in data are left alone and render as today.
- **Before H3** a deletion still changes this device's `hosts` build; another device applying that drops the host
  through this same rule — its tabs are not touched either. Only after H3 is the host list fully per-device.

## 4. Host looks and shown hosts (H2)

### 4.1 Stores and wire

- `purdex-host-looks`: `{ looks: { [wireId]: { name?, colors?, color?, icon?, iconWeight? } } }`, keys are wire ids
  in the store itself (no local↔wire mapping on build or apply; the identity decides which local host a key means).
- `purdex-shown-hosts`: `{ ids: wireId[] }` — the hosts SHOWN in the **master's** workbench (on screen or not); `[]`
  (the default, and the state at ship time) = every host hidden; no `null`, no "all" (§1.2). Unknown ids kept, order
  kept. It also persists a device-local `relabelStamp` (never projected): the `relabelCount` its list belongs to.
- `LocalProfile.shownHostIds: wireId[]` (2026-09-25) — a local workbench's own list: same rules (plain list, unknown ids
  kept, `[]` = all hidden), sanitised with `sanitizeShownIds`, never projected. A record from before it existed reads
  `[]`.
- Both are projected in `settings`; unknown ids are carried through apply and build untouched. Each store's arrival
  bumps the `settings` ordinal and adds a wire marker (`@wire:host-look=1`, `@wire:shown-hosts=1`), so an old client
  sees `settings` as newer and locks the whole profile (decision 7).

### 4.2 One selector, and every surface that must use it (finding 7)

`hostLookOf(hostId)` / `useHostLook(hostId)`: local id → wire id (identity) → `looks[wireId]` field by field, else the
device's `HostConfig` field (the fallback, e.g. a host with no look yet). The New Tab host providers
(`session-new-tab-providers.tsx`, `headless-new-tab-providers.tsx`) build `labelParams.host` from it and their
`subscribe` ALSO listens to `purdex-host-looks`, so a label follows a rename that arrives by sync.

Surfaces reading `HostConfig.name` / `color` / `colors` / `icon` / `iconWeight` directly on alpha.439 (grep of
`spa/src`, tests excluded) — every one moves to the selector in H2; a guard test greps for new direct reads outside
`useHostStore.ts`, `host-color.ts` (sanitiser) and the selector:

- Colour / icon: `hooks/useTabHostBadge.ts` (tab bar + sidebar badges via `SortableTab`, `InlineTab`),
  `components/hosts/HostBadgePreview.tsx`, `components/hosts/HostColorField.tsx`, `components/hosts/HostIconField.tsx`.
- Name, Hosts pages: `components/hosts/HostSidebar.tsx`, `OverviewSection.tsx`, `LogsSection.tsx`,
  `PeersSection.tsx`, `nex/NexConfigForm.tsx`, `AddHostDialog.tsx` (duplicate-daemon message).
- Name, sessions / New Tab: `lib/session-new-tab-providers.tsx`, `lib/headless-new-tab-providers.tsx`,
  `components/SessionSection.tsx`, `SessionPanel.tsx`, `SessionPickerList.tsx`,
  `components/editor/EditorNewTabSection.tsx`.
- Name, elsewhere: `components/StatusBar.tsx`, `components/editor/EditorStatusBar.tsx`, `MemoryMonitorPage.tsx`,
  `components/executions/ExecutionsView.tsx`, `hooks/useNotificationDispatcher.ts`,
  `components/settings/DevEnvironmentSection.tsx`, `components/settings/profile/CurrentBlock.tsx`,
  `components/settings/profile/wizard/ProfileWizard.tsx`, `wizard/WizardChoiceSteps.tsx`, `lib/peer-pairing-load.ts`
  (caller-supplied name — the caller moves).

Writes of name / colour / icon go to the look store under the host's current wire id (`useHostStore` setters
`setHostColorLayer` & co. and the rename in `OverviewSection` are rerouted); `HostConfig` look fields are then only
written by add-host and the transfer receiver (§6.4).

### 4.3 Migration, new hosts, re-key (finding 4)

- **First run:** for each local host, key = its CURRENT wire id (`d1_…` when it has a daemonId, else its local id);
  if `looks[key]` exists, skip (never overwrite); else copy the host's `HostConfig` name / colors / color / icon /
  iconWeight. Then set the device-local, non-projected marker `purdex-host-looks-migrated = 1`; the migration never
  runs again with it set. The skip-if-present rule alone makes a re-run harmless; the marker stops a re-run from
  resurrecting a look the user reset. Before H3 the `hosts` section keeps every device's `HostConfig` looks equal, so
  two devices migrating the same `d1_…` host produce the same entry and the same `settings` hash — no conflict.
- **Hosts added later** (add-host dialog, transfer): name / look go to `HostConfig` (the device fallback) AND a look
  entry is created under the host's current wire id only if none exists — a workbench look already under that
  `d1_…` wins.
- **Re-key** (in the §3.3 pass): when a host's wire id changes from its local id to `d1_…` (daemonId learned), the
  entry under the local id moves to the `d1_…` key; if a `d1_…` entry already exists it wins and the local-id entry
  is dropped. Same for shown-hosts ids. This changes the payload: one push.
- **Two local rows later proven to be one daemon:** that is an identity conflict — nothing is built and the pass
  does nothing (§3.3). The user removes one row (existing duplicate flow); the pass then re-keys the survivor by the
  rule above. Deleting a host deletes no look / shown-hosts entry (decision 9) — the removed duplicate's local-id entry
  stays as an unresolvable id, like any other.
- **Transferred looks and the H2c / H4b merge order:** whichever of H2c and H4b merges SECOND makes the transfer
  receiver follow §6.4 step 5 (§6.4 step 7).

### 4.4 The period between H2 and H3 (finding 3)

The `hosts` section still syncs, and decision 7 locks every old client, so only new clients write. For a new client:

- The look store is the only look SOT. UI reads go through the selector; UI writes go to the look store.
- `hosts` apply still updates `HostConfig` name / colours / icon (it is the device fallback; harmless); the `hosts`
  build still sends them. Neither the apply nor the build ever touches the look store.
- `HostConfig` look fields are read as a SOURCE only by the first-run migration and by the selector's fallback.

### 4.5 Shown hosts — what hiding does (finding 8; rewritten 2026-09-24 per §1.2)

Hidden ≠ absent, and hidden ≠ closed. Hiding host X in workbench W:

- **gates X's panes**: every tmux / execution pane on X (in any tab, split or not, locked or not, synced in or local)
  renders 「主機已於此工作台關閉」 and opens no connection (no terminal WS / ticket, no execution attach / SSE, no nex
  ensure, no per-pane probe or revive); the other panes of a split tab work as usual; showing X again remounts the
  same panes (same pane ids) and recovers X's sessions without a reload. No tab is closed, split, moved or written.
- **blocks every way to open a tab on X**: New Tab `sessions:` / `headless:` blocks of X are not rendered (the column
  stays in the layout); the terminated-pane session picker omits X; the Hosts page session "open" and the executions
  "open" are not offered; a notification of X still fires but its click, an execution deep link and the
  `/execution/…` route land on X's Hosts page instead of a tab.
- **changes only the presentation of X on the Hosts page**: every host is listed; X in a hidden style, still fully
  manageable (overview with the show / hide switch, sessions list incl. "new session", settings, logs, peers, nex).

NOT affected (the host keeps working): connections and health, `useMultiHostEventWs` subscriptions and its `sessions`
reconcile, `useSessionWatch` / session refresh, notifications (delivery), backup triggers, New Tab provider
REGISTRATION and the layout (columns stay), the tab bar (X's tabs stay with their badge), `activeHostId` /
`hostOrder[0]` fallbacks (`HostPage`, `nex/resolve-host`, fs backends), device settings pickers
(`DevEnvironmentSection`), and direct navigation to `/hosts/<hidden id>/…` (opens normally). There is no Settings ›
工作台 editor; the switch is on the Hosts page.

## 5. The `hosts` section leaves Profile Sync (H3)

### 5.1 Client

- Stop building, pushing, pulling, applying and deleting `hosts`. `hosts` becomes a retired kind: skipped by
  `profileLock`, not in the managed section set, its persisted section-store record discarded, never touched by any
  orphan sweep; the collector ignores it.
- Remove: hosts apply / refusal / cascade path, the #1370 pull guard (`confirmedPullHosts`, the barrier), the
  wizard's host removal list, the `hosts` half of `GATES` / `SETTINGS_GATES`.
- Keep: wire identity, `blocked: host-identity-*`, master endpoint.
- Wizard: pull no longer replaces hosts; the attach host must exist on this device (it always does — the wizard talks
  to it).
- `profiles.db` no longer receives tokens from new clients (old `hosts` rows keep theirs until the profile is deleted —
  say so in the UI copy of "delete a workbench on the sync host").

### 5.2 Alias machinery stays (finding 6)

H3 does NOT remove: `makeWireResolver` / `wireResolverOf`, persisted `HostConfig.syncAliases`, `mergeAliases`, the
hosts wire builder as the resolver's alias source (`wireResolverOf` builds rows from this device's own hosts — no
longer a section), and every legacy-reference test. `tabs.*`, `settings` and parked worlds may still hold legacy
local ids only an alias resolves. After H3 no new aliases are learned (no `hosts` row is read any more); the
persisted ones keep resolving.

Exit condition (tracked in its own issue, NOT part of this spec): every client is ≥ H3, AND each SOT profile's
`tabs.*` / `settings` have been verified to hold only `d1_…` or unresolvable-but-canonical ids (an audit that reports
zero alias-resolved references), AND no local host that any reference names lacks a daemonId.

### 5.3 Daemon keeps legacy `hosts` support (finding 13)

The daemon's section validator (`sectionPattern` in `validate.go`), section API, index and DB keep `hosts` as a
writable section, unchanged — old rows must stay readable for the lock comparison of any straggler and deletable with
the profile. Removal condition (own issue): every client ≥ H3 AND no profile on the daemon still holds a `hosts` row
(a later, user-consented cleanup; the rows hold tokens).

New-client guarantees, each a test: with a SOT that lists `hosts`, a new client never PUTs or DELETEs it in
restore-local, in a resolve action (keep local / keep SOT), in the orphan / section-lifecycle sweep, in a push after a
pull, in the wizard's attach, or in "detach and keep local".

### 5.4 Known limitation (decision 7)

Old clients are locked by the `settings` (H2) and `tabs.*` markers — but only once a new client has written such a
section to that SOT. A profile whose SOT has only a `hosts` section (or whose `tabs.*` / `settings` were last written
by an old client) does not lock an old client, which may keep syncing `hosts` there. Not fixed: devices upgrade
together. The H3 release notes say so.

## 6. Host transfer code (H4)

Daemon + SPA; independent of H1–H3.

### 6.1 Trust (decision 6)

The relay daemon holds the shared hosts' tokens in plain text in memory for ≤ the TTL; whoever controls that daemon
process can read them. Share dialog copy (both locales), shown before the code is created: "‹relay› will hold the
access tokens of the hosts you share, readable by that host, until the code is used or expires (10 min). Only relay
through a host you trust." The payload is never logged or written to disk; a daemon restart loses it.

### 6.2 Daemon store and endpoints

- `POST /api/host-transfer` (TokenAuth): body `{ hosts: [...] }` — ≤ 32 hosts, ≤ 64 KiB (else 413/400). Returns
  `{ code, expiresAt }`. Code: 8 characters of Crockford base32 from `crypto/rand` (40 bits); on collision with a
  live code, regenerate, at most 5 tries, then 503.
- Capacity: at most 16 unredeemed, unexpired codes per daemon; one more → 429 `{"reason":"capacity"}`. Expired
  entries are swept on every call.
- `POST /api/host-transfer/redeem` (TokenAuth): body `{ code }`, normalised Crockford-style (upper-case, `-` and
  spaces dropped, `I`/`L` → `1`, `O` → `0`).

### 6.3 Redeem: atomicity and brute force (findings 10, 11)

- One mutex guards the store. Rate-limit check, lookup, TTL check and delete happen in ONE critical section: of two
  concurrent redeems of one code exactly one gets the payload, the other gets `invalid_code`.
- Unknown, expired and already-redeemed codes all answer the same 404 `{"reason":"invalid_code"}` (same body, no
  timing branch worth measuring). A wrong code consumes nothing and affects no stored code; a right code is deleted
  on its first redeem.
- Brute force, per daemon process (not per client — a request carries only the shared daemon token, no client
  identity, and IP keys break behind NAT / proxies): a fixed window starts at the first failure; 10 failures inside
  60 s → every redeem answers 429 `{"reason":"rate_limited"}` with `Retry-After` until the window ends, then the
  counter resets. A success does NOT reset the counter. Creating codes is not rate-limited (capacity bounds it).
  Accepted cost: a guesser holding the relay's token can delay a genuine redeem by ≤ 60 s. Worst case ≈ 14 400
  guesses/day against ≤ 16 live codes in 2^40 → ≈ 2·10⁻⁷/day.

### 6.4 Receiver: verify, preview, one commit (finding 12)

1. Redeem → payload rows `{ name, ip, port, token, daemonId?, look? }`.
2. For EVERY row, `GET /api/info` at `ip:port` with the PAYLOAD token; the observed `host_id` is the truth.
3. Preview, one line per row, each with a status that decides what can be committed:
   - `new` — verified, no local row has that daemonId;
   - `existing` — verified, exactly one local row has that daemonId (add-only: skipped; overwrite: ip / port / token
     replaced, local id kept);
   - `mismatch` — the payload's daemonId ≠ observed: not committable;
   - `unverified` — unreachable, auth failed, or the daemon reports no id: not committable (retry button);
   - `duplicate` — a second payload row with the same observed daemonId: not committable (first row wins);
   - `local-conflict` — two local rows already claim that daemonId, or the endpoint equals a local row with a
     different daemonId: not committable.
4. The user picks rows and a mode and confirms. Nothing is written before this.
5. ONE `useHostStore` action applies the whole plan in a single `set()`; any throw → the store is untouched.
   Looks from the payload are written to the look store only where no entry exists for that `d1_…` (§4.3).
6. `replace-all` removes every local host not in the committed set EXCEPT: the relay host used for this transfer, the
   current master's attach host, `activeHostId` (else reassigned to a kept host) and `devHostId` (else cleared); it
   refuses to leave zero hosts. Removals go through the normal delete path, i.e. decision 9 / §3.4: references become
   wire ids, nothing synced is marked or closed, each removal is undoable.
7. **Merge order with H2c (agreed with d4).** If H4b merges BEFORE H2c, a received look is written to the new host's
   `HostConfig` name / colors / color / icon / iconWeight (the device fallback) — to an EXISTING host's only in
   overwrite mode — and H2c's first-run migration (§4.3) later moves it into the look store. Whichever of the two
   merges second owns making the receiver match step 5: after H2c a received look goes to the look store, only where
   no entry exists for that `d1_…`; `HostConfig` gets the name only (the required fallback field). That PR carries the
   test.

## 7. Phases (each a PR ≤ 20 files, merged in order; file counts are estimates the plan measures)

- **H1a — tolerate unresolvable references** (§3.1, §3.2; no wire change, safe alone; ~16 files: `applier.ts`,
  `apply-to-stores.ts`, `new-tab-registry.ts`, both host provider sources, `useNewTabBootstrap.ts`,
  `SessionPaneContent.tsx` + the missing-host state, 2 locales, tests). Tests: a `tabs.*` payload naming an unknown
  `d1_…` keeps the pane byte-for-byte, writes no `terminated`, and the next build hashes equal (nothing pushed);
  a `settings` payload with an unknown `sessions:d1_…` / `headless:d1_…` column keeps it through apply, bootstrap
  (source ready) and build; the legacy `sessions` id is still migrated/pruned; unknown host-settings key round-trips;
  the pane renders "no host ‹id› here".
- **H1b — the re-resolve pass** (§3.3; ~8 files: new `host-reresolve.ts`, boot wiring, `ensureDefaults` wire-id
  check, tests). Tests: add a host whose daemonId hashes to a stored `d1_…` → panes (on screen AND parked), host
  settings and both column kinds point at the local id; every section hash is unchanged and nothing is pushed; the
  full path "receive unknown column → restart → add the daemon" ends with the column live and no duplicate; an
  alias reference is canonicalised with one push; identity conflict → pass does nothing, runs after resolution;
  the pass holds the operation lock.
- **H1c — local deletion only affects this device** (§3.4, decision 9; after H1b, whose pass the undo relies on; ~9
  files: `host-lifecycle.ts`, the delete dialog in `OverviewSection.tsx`, `apply-to-stores.ts` (its
  `deleteHostCascade(id, false)` caller), 2 locales, `host-lifecycle.test.ts`, `host-lifecycle.worlds.test.ts`,
  `apply-to-stores.test.ts`, `OverviewSection` test). Tests: deleting a host with panes on screen, in a parked master
  and in a parked slave rewrites them to the wire id, writes no `terminated`, closes no tab, keeps host settings /
  columns / look, and every section hash is unchanged (nothing pushed); a no-daemonId host's references stay its local
  id; a legacy hostless execution pane is not pinned and nothing synced changes for it; undo re-adds the host and the pass brings every reference back to the
  local id (also after a relabel); a `hosts` apply dropping a host (pre-H3) follows the same rule; the dialog has no
  "close tabs" choice.
- **H2a — the look selector, colour/icon surfaces** (§4.2; pure refactor, selector reads `HostConfig` only; ~14
  files). Tests: selector unit tests; badge / preview / colour & icon fields unchanged in behaviour.
- **H2b — the look selector, name surfaces** (§4.2; pure refactor; ~18 files) + the guard test against new direct
  reads.
- **H2c — the look store** (§4.1, §4.3, §4.4; `settings` ordinal +1, `@wire:host-look=1`; ~16 files: store,
  projection + guard snapshot, settings build/apply, migration, writers rerouted, provider `subscribe` on the look
  store, re-key in the pass; if H4b merged first, also the receiver switch of §6.4 step 7). Tests: migration keyed by current wire id, skip-if-present, marker stops a
  re-run, a no-daemonId host's look re-keys to `d1_…` when the daemonId is learned (existing `d1_…` wins), host
  deletion keeps every look entry, a look arriving by `settings` relabels the New Tab provider, an old client
  locks on the new `settings`.
- **H2d — shown hosts** (§1.2, §4.5; ordinal +1, `@wire:shown-hosts=1`; planned as five PRs, 64 files — store /
  wire, Hosts page switch and hidden style, blocked open-a-tab surfaces and landings, the pane gate, tests).
  "Hidden ≠ absent" tests: a hidden host stays connected and its event WS open; its tabs stay in the tab bar and
  nothing closes, while its panes are gated and restored on show; notifications still fire; its New Tab provider stays
  registered and its column is not pruned; `/hosts/<hidden>` opens; unknown ids survive apply + build + show / hide;
  `[]` hides every host.
- **H3a — stop syncing `hosts`** (§5.1 client, §5.3 guarantees; ~15 files: executor, start, collector, sections,
  applier, apply-to-stores, profile-state, sync-status / sync-view, tests). Tests: §5.3's never-PUT/DELETE list;
  `profileLock` ignores a `hosts` row; gates no longer wait on `hosts`; a device adding a host locally pushes nothing
  about it; the alias resolver still resolves a legacy id after H3 (§5.2).
- **H3b — wizard and dead code** (§5.1 wizard, UI copy; ~12 files). Tests: a pull never removes a local host; the
  attach host check; the delete-workbench copy mentions stored tokens.
- **H4a — daemon transfer store** (§6.2, §6.3; ~4 files). Tests: TTL, one-time, concurrent redeem (N goroutines,
  one winner), identical `invalid_code` for unknown / expired / used, 10 failures → 429 until window end, success does
  not reset, capacity 16 → 429, collision retry via an injected generator, payload never in logs.
- **H4b — SPA share / receive** (§6.1, §6.4; ~14 files: API client, pure planner, store commit action, two dialogs,
  Hosts page entries, locales, tests). Tests: planner statuses (new / existing / mismatch / unverified / duplicate /
  local-conflict); `/api/info` uses the payload token; received looks land where §6.4 step 7 says for the merge
  order at hand; rollback for each mode (a throw inside the commit leaves
  `hosts`, `hostOrder`, `activeHostId`, `devHostId` and the look store identical); replace-all keeps the relay, the
  master's attach host and `activeHostId`, clears a removed `devHostId`, refuses zero hosts, and its
  removals leave tabs and synced data untouched (§3.4); the trust copy is shown.

## 8. Acceptance

H1: A and B with independent host lists (B lacks host X): A opens a tab on X → B shows it as "no host X here", A
still live, nothing marked; B then adds X → the pane goes live, no push from B; B deletes X → B's X tabs stay ("no
host X here"), A notices nothing; B undoes → live again. H2: change mlab's colour on A → B
follows; rename on A → B's New Tab label follows; every host starts hidden; show / hide a host in workbench W on A → the same
on B in W, still connected on both, its tabs kept on both with only its panes gated, restored without reload on show;
show a host in local workbench L on A → nothing changes in the master on A or anywhere on B; switch A to the master →
the master's own list applies. H3: a device adds a host locally → no other device gets it; a pull never removes a local host. H4: A shares
mlab + air26 through mlab → B enters the code → gets both (add-new mode), connected, looks from the workbench; a
second redeem of the same code fails.

## 9. Review 2026-09-24 (codex spec review task-mue86vwz-bnbayc)

1. H1 unknown wire id then host added — adopted: verbatim storage + re-resolve pass with the no-push invariant (§3.2, §3.3, H1b).
2. New Tab stale — adopted: host-bearing columns never stale; full receive→restart→add path tested (§3.2, H1a/H1b).
3. H2 two look SOTs — adopted via decision 7: old clients locked by the `settings` marker; H2–H3 read/write rules (§4.4).
4. H2 migration without daemonId — adopted: current-wire-id keys, re-key in the pass, idempotent marker, later hosts, duplicate rows (§4.3).
5. H3 fencing — adopted as a known limitation per decision 7, no fencing write (§5.4).
6. Alias machinery — adopted: resolver, `syncAliases`, legacy tests stay; exit condition in its own issue (§5.2).
7. Look selector scope — adopted: surface inventory + guard test; provider labels subscribe to the look store (§4.2, H2a/H2b/H2c).
8. Shown-hosts boundary — adopted: filter list, not-filtered list, "hidden ≠ absent" tests (§4.5, H2d).
9. H4 relay trust — adopted via decision 6: trusted relay, explicit UI copy, no E2E (§6.1).
10. H4 brute force — adopted: per-daemon fixed window 10/60 s → 429, success no reset, capacity 16, collision retry (§6.2, §6.3).
11. H4 redeem atomicity — adopted: one critical section, one winner, wrong code consumes nothing (§6.3).
12. H4 receiver commit — adopted: verify all → preview statuses → one commit; replace-all invariants; rollback tests (§6.4, H4b).
13. H3 daemon side — adopted: daemon keeps legacy `hosts` support with a removal condition; never-rewrite tests (§5.3, H3a).

Decisions recorded 2026-09-24 after this review: (1) the question raised while revising — a local host deletion
propagating to other devices — decided by the user as option (b), "only this device": decision 9, §3.4, H1c; H4
replace-all uses it (§6.4 step 6). (2) H4b / H2c merge order, agreed with d4: before H2c a received look goes to
`HostConfig` and H2c's migration moves it; whichever merges second makes the receiver write the look store (§4.3,
§6.4 step 7, H2c / H4b).
(3) Hostless execution panes (coordinator, 2026-09-24): no special case on deletion — creation always writes `host`,
`''` is legacy only; such a pane then looks up its `executionId` on the new first host and shows "not found" when
absent, a visible failure rather than a silent rebind (§3.4).
