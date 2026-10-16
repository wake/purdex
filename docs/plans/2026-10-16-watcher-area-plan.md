# Watcher area — plan (rev 1.2: W5a1 … W5e)

Spec: `docs/specs/2026-10-16-watcher-area-spec.md` (B line, with A3 as agreed in #3010, U9 from #3017, U10 from #3020).
Load-control spec §12.4–§12.6 for the watcher itself. Facts below were read on origin/main `bd4b0d7d1` (alpha.745 +
#3014), 2026-10-16.

**Rev 1.1** takes the focused plan review (subagent, 0 critical, 11 important, 5 minor; all taken):
- the guardian tab's session pane (rev 1.2: a full session tab, user 10-16; see §1);
- `watch-host` carries `fleetHostId`, so that no generic `content.hostId` reader takes it for an App host;
- a host tab is removed only by the device that saw its host leave;
- the full list of workspace consumers;
- shortcuts relative to the return workspace;
- `activeWorkspaceId` may be `watch`;
- the `toggleLock` guard by tab id;
- the `fixed` row's tests;
- W5b decodes the presence of `Admission`, never sends a slot token, and is tested with 20 holders;
- the watcher status and the holder's `watch` row move together into W5a3, with the shield;
- mixed-version rollout.

It also takes 1f's notes:
- no slot bounds at all (#3020, replacing #3017/#3019): a slot count is limited only by the gate's 0–64, and 0 pauses a
  class (W3c);
- W2b will declare `watch.v1` (Q4).

**Rev 1.2** (user, 10-16): the guardian tab is a full session tab with its three views (terminal, Deck, Chat); §1 and
W5a3 rewritten, Q3 closed.

**Flow: the quick one** (user-facing, wanted soon): this plan → one focused subagent review (done) → TDD by subagents
(Sonnet, one PR per phase, own worktree) → one full subagent review per PR → merge. No codex round. W5b (daemon) also
goes past 1f (`internal/fleet` is shared with the A line's sampler).

## 1. Design choices (for the reviewer)

- **The area is a real workspace** (`WATCH_WORKSPACE_ID = 'watch'`, next to `UNSORTED_WORKSPACE_ID`): it is an ordinary
  syncable workspace in the `workspaces` section and `tabs.watch` (Profile Sync carries its tab order, spec §3), and the UI
  hides it. Excluding it from the `workspaces` section does not work: `applyTabs` drops a `tabs.<id>` whose workspace is
  unknown (`lib/profile/applier.ts:235`), and the order would not travel.
- **Fixed tab ids**: `watch-guardian`, `watch-events`, `watch-host-<fleet id>` (pane id `<tab id>.p`). Two devices, or two
  windows (`adopt-standalone.ts:176` warns of these), that create the area each make the same tabs, so a sync apply or a
  rehydrate converges instead of duplicating. Generated ids are 6 chars of `[0-9a-z]` (`lib/id.ts`) and never contain `-`,
  so they cannot collide. `isWatchTabId(id)` (the `watch-` prefix) is how other stores recognise them without reading
  the workspace store.
- **`watch-host.fleetHostId`** is the fleet host id (a daemon's host id or a probe row's id). It is global already, so
  `lib/profile/host-identity.ts` `mapContent` leaves it alone. It is not named `hostId`, so the generic readers of a
  pane's `hostId` (`lib/host-color.ts`, `SortableTab` host-offline, `useTabHostBadge`) do not take it for an App host.
- **The three kinds sync** (`KIND_DECISION` 'syncs' in `lib/profile/sections.test.ts`; `DEVICE_LOCAL_PANE_KINDS` stays at
  7). A tab made only of device-local kinds is never sent, so the order would not travel.
- **Created on first open, then kept in step.** `openWatchArea` ensures the workspace. While it exists, a subscription
  (`startWatchArea()`, installed in `main.tsx` beside `startStandaloneAdoption`) reconciles its tabs with the fleet's host
  list on fleet and workspace changes. Each run happens only in a settled world (`readMasterWorld().settled`, as adoption
  does) and writes only on a difference. An apply or a profile switch that removes the workspace leaves it removed
  until the next open: the area is never pushed into a world that does not have it.
- **Host tabs: append freely, remove only what this device saw leave.** A sync apply removes local tabs the payload
  lacks (`applier.ts:244`), so a device that removed a tab for a host it merely did not list would remove it on every
  device, and the next device would add it back. The rule:
  - a tab is appended for every host the device's fleet snapshot lists;
  - a tab is removed only when this device listed that host in a non-stale snapshot and has not listed it for 60 s;
  - a host tab that arrives by sync for a host this device never listed stays, and shows 「讀不到這台主機的資料」.
- **Locked tabs, a fixed tab row.** Tabs are `locked: true`, and every close path already refuses them
  (`useTabStore.ts:704`, `features/workspace/store.ts:237`, `lib/tab-lifecycle.ts:11`, `TabContextMenu.tsx:105`).
  `toggleLock` (`useTabStore.ts:886`) refuses watch tab ids, which covers the menu (`features/workspace/hooks.ts:194`) and
  `WorkspaceSettingsPage.tsx:200`. `TabBar` gains `fixed`: no "+", no lock icon (`SortableTab.tsx:122/187`), no context
  menu, no middle click; dragging to reorder stays. While `watch` is active, `App` shows this top tab row whatever
  `tabPosition` is, because the area is not in the sidebar and its tabs would otherwise have no row.
- **Nothing else enters or leaves the area.**
  - `insertTab` sends a non-watch tab whose target resolves to `watch` (an explicit id, or its fallback chain
    `store.ts:190-194`) to the return workspace, or else to the first non-watch workspace. The return workspace is the
    last active workspace other than `watch`, kept in memory.
  - Sessions opened, terminal links, Cmd+T and "+" while the area is shown all go there.
  - Watch tabs cannot be dragged onto a sidebar workspace (`computeDragEndAction.ts`, `useCrossWorkspaceDragOver.ts`).
  - `removeWorkspace('watch')` is a no-op, and `watch` is never chosen as an heir.
  - The reconcile also moves non-watch tabs found in `watch`, for example from another window's rehydrate, to the
    return workspace.
- **`activeWorkspaceId` may be `watch`**, persisted like any other: a restart reopens the area. When an apply or a
  switch removes the workspace while it is active, the existing removed-active fallback runs, and it must never pick
  `watch`.
- **The watcher status, without a push yet**: `GET /api/watch/status` exists (`internal/module/watch/module.go:97`,
  shape `{self: {host_id, state, session_id?, since?…}, current?, last?, enabled, auto_wake, mode, targets}`), but
  `watch.changed`, `watch.events` and `watch.v1` do not exist yet. W5a3 reads the status of every connected host on
  connect and every 30 s while the window is visible; a 404 means "no watch support". W5e replaces the timer with the
  pushes and keys the events tab on `watch.v1`.
- **The guardian tab is a full session tab** (user, 10-16: 「要，就是現在的完整 tab 可用三種模式」 — terminal, Deck and
  Chat). Its one leaf pane, in the store under the fixed tab id `watch-guardian`, holds the real `tmux-session` content of
  the holder's `pdx-watch` session, so `SessionPaneContent`, the status strip's view buttons (`PaneModeButtons`), the
  conversation, drafts and session-view state all work as in any session tab. When there is no session to show (none,
  unknown, the holder not connected, no `pdx-watch` in its list) the pane holds `{kind: 'watch-guardian'}`, a panel.
  - **Who writes it**: the reconcile, edge-triggered. When this device's derived guardian (holder host, session code)
    changes, it sets the pane's content once (`setPaneContent` on the fixed tab). It does not keep enforcing, so a
    content that arrived by sync from another device stays until this device's own derivation changes. Both devices
    read the same daemons, so they converge without ping-pong. The `tmux-session` content syncs like any session tab's
    (its `hostId` is translated by `host-identity`).
  - **No rebuild**: when the pdx-watch session ends, the pane would be marked terminated and offer a rebuild, which
    would start the watcher outside `pdx-watch`. For the guardian tab id, `SessionPaneContent`'s terminated branch shows
    the guardian panel instead of `TerminatedPane`, and the next derivation sets the panel content. The daemon restarts
    the watcher (W5e's start and wake).
  - **Nothing added around it** (user, 10-16: 「守門員應該就是現在 tab 本來的樣子，包括底下 status bar」): no bar above
    the pane, the stock bottom status bar with its Deck / Chat switch. Where the watcher is and whether it is awake show
    on the shield and its title (T-a3.3). How a resting watcher is woken from this tab is settled with the user in W5e.
  - The tab is locked like every area tab: no close, no tear-off, no merge.
- **Mixed versions.** An App older than W5a1 that receives the `watch` workspace by sync shows it in its sidebar with
  unknown panes. Both Macs load the SPA from mlab's dev server, so they update together. The acceptance checks the
  second Mac right after the deploy. iOS does not apply workspaces.

## 2. Phases

| PR | content | depends on | who | est. lines (code + tests) |
|---|---|---|---|---|
| W5a1 | pane kinds, the `watch` workspace, its guards and consumer audit, the `fixed` tab row, `openWatchArea`, host cells' click, host tab header + 行程 | — | B subagent | ~1000 |
| W5a2 | host tab 資源: gauges, charts, range switch, history adapter, view memory | W5a1 | B subagent | ~800 |
| W5a3 | the watcher in the area: watch status store, the shield, the guardian tab (the stock `pdx-watch` session tab on the holder, or the status panel), the holder's `watch` row reader | W5a1 | B subagent | ~900 |
| W5b | queue wire: `slow.queue.holders`, `waiters[].session`, `admission_reason`, `heavy_paused` / `light_paused` (daemon + golden JSON + SPA types/parse) | — | 53-pe | ~450 |
| W5b1 | queue 當下 UI: lanes (paused included), gate line, waiting cards, table, hover pairing | W5a1, W5b | B subagent | ~700 |
| W5b2 | the hourglass on the agent icon | W5b | B subagent | ~350 |
| W5c1 | 狀態｜設定 switch; 設定: the 守門員 section (existing API), probe-host settings (F-2a2's editor) | W5a3 (row reader) | B subagent | ~800 |
| W5c2 | 設定: 設定組, 資源管制, gate settings | W5c1, A2 | B subagent | ~600 |
| W5d | queue 歷史 | W5b1, A1 | B subagent | ~700 |
| W5e | 事件 tab, the count (shield, tab label), the two pushes, 啟動守門員, the wake input | W5a3, A3 (`watch.v1`) | B subagent | ~900 |

This replaces spec §9's W5a, W5b and W5c rows (one PR each would be 1700, 1100 and 1400 lines). The spec is updated in
the same docs PR as this plan. Line counts include tests. Going over 800 lines is accepted when the PR is one
reviewable unit and about half of it is tests, as in the fleet plan; the PR body lists code and test lines separately.

Order:
- **W5a1 and W5b start now, in parallel.**
- After W5a1: W5a2 and W5a3, which touch different files. At most two implementation subagents run at once, because
  mlab's heavy slot count is 1.
- W5c1 after W5a3. W5b1 after W5a1 and W5b. W5b2 any time after W5b.
- W5c2, W5d and W5e wait for A2, A1 and A3.

Every PR follows these rules:
- **Method**: TDD, one commit per task, and a mutation table in the PR body. For each named mutation, the PR shows
  the mutation made, the test seen red, and the revert.
- **SPA checks**:
  - `vitest related --run <changed files>`;
  - `tsc -b --force` (the worktree's `node_modules` is a symlink, so a plain `-b` can be green from another worktree's
    cache);
  - `eslint` on the changed files.

  Never the full vitest or the full lint (test tiers, 10-15).
- **Daemon checks**: `go test` of the touched packages, one command at a time; `-race` on `internal/fleet`;
  `gofmt -l`; `go vet`. Never `./...`.
- **Brief**: every brief says that the subagent may stop only its own commands, by killing its own pid outside the
  pdx gate (`pkill -f` and `killall` are forbidden).

## 3. W5a1 — the area and the host tabs

- **T-a1.1 Pane kinds.**
  - `types/tab.ts`: `{kind:'watch-guardian'}`, `{kind:'watch-events'}`, `{kind:'watch-host'; fleetHostId: string}`.
  - `lib/pane-labels.ts` labels: 守門員, 事件, and the host's alias (the fleet snapshot's `alias`, falling back to the
    id).
  - Icons `ShieldCheck`, `ListBullets`, `Desktop`, added to `components/tab-icon-map.tsx`.
  - `lib/route-utils.ts` → `'/'`.
  - `contentMatches` (`lib/pane-utils.ts:23`): `watch-host` by `fleetHostId`, the other two by kind.
  - One non-disableable module `watch-area` with the three panes (`lib/register-modules/index.tsx`). The guardian pane
    is a stub in W5a1; W5a3 fills it, and the reconcile does not create its tab before W5a3 (T-a1.2).
  - Tests:
    - `KIND_DECISION` is 'syncs' for all three;
    - `isLightTab` is false for each;
    - `mapContent` leaves `fleetHostId` unchanged;
    - `lib/host-color.ts`, the host-offline check and `useTabHostBadge` ignore a `watch-host` pane.

  M: mark a kind device-local → the order round-trip of T-a1.2 goes red.
- **T-a1.2 The workspace** (`features/workspace/store.ts` + `features/workspace/lib/watch-area.ts`).
  - `WATCH_WORKSPACE_ID`, `isWatchTabId`, and `ensureWatchWorkspace()`: appended last, named
    `t('watch.area.name')` 守門員專區, idempotent.
  - `reconcileWatchTabs(view)`: `view` is the fleet host ids plus "stale" from `useFleetStore`, or null when there is
    no snapshot.
    - It applies the host-tab rule of §1, with the per-device "seen" set and the 60 s absence kept in memory.
    - With `view === null`, host tabs are left as they are.
    - Watch tabs found in another workspace (`repairTabOwnership` can put them in Unsorted) move back. Non-watch tabs
      found in `watch` move to the return workspace.
    - The user's order is kept. A new host tab is appended at the end.
    - From W5a3 on, the guardian tab exists and comes first when created. W5e adds the events tab after it.
  - `startWatchArea()`: subscribes to the fleet store and the workspace and tab stores, and runs the reconcile only
    when the workspace exists and the world is settled.
  - Tests:
    - create; idempotent;
    - append; remove after 60 s (fake timers); not removed while the snapshot is stale; a synced tab for a never-seen
      host stays;
    - two devices whose snapshots differ by one host converge with no ping-pong (two reconcilers, one `applyTabs`
      each way);
    - `view === null` keeps the tabs;
    - the order is kept;
    - a `tabs.watch` payload round-trips through `applyTabs` with order and `locked`;
    - a non-watch tab put into `watch` by a rehydrate moves out.

  M: remove after the first absent snapshot (no 60 s) → the stale test goes red; drop the never-seen rule → the
  ping-pong test goes red.
- **T-a1.3 Guards and the consumer audit.** Grep every non-test reader of `.workspaces` (about 40 files today). Each
  one is decided in the PR body: either it shows or picks workspaces for the user and excludes `watch`, or it is sync
  or bookkeeping and keeps `watch`. Known UI readers:
  - `App.tsx`: the bars (`:208`), Home (`:171`, the first non-watch workspace), `nextWorkspaceName` (`:176`), and the
    title (`:101`, which shows 守門員專區 while the area is shown).
  - `useShortcuts.ts:141-165`: switch-workspace-N indexes the visible list; prev and next from inside the area are
    relative to the return workspace.
  - `NewTabPage` relocation candidates; `WorkspaceSettingsPage`; `useWorkspaceWindowActions`;
    `hooks/useTabDisplay.ts:76`; `useTeamViews` and `lib/team/*` (team tabs never go to `watch`); `ProfileConflict`;
    `lib/tab-insert/compute-cluster-insert-target.ts`; `lib/open-browser-tab.ts`.
  - The store: `insertTab` (return-workspace rule), `removeWorkspace` (no-op, never the heir), and the removed-active
    fallback.
  - `toggleLock`: refuses watch tab ids.
  - Drag: `computeDragEndAction.ts` and `useCrossWorkspaceDragOver.ts` refuse a watch tab as the source and `watch` as
    the target.

  Tests: one per listing that excludes `watch`, and one per guard. `toggleLock` is tested from the menu action and from
  the store. M: remove the `insertTab` redirect → opening a session while the area is shown puts it in `watch`.
- **T-a1.4 The fixed tab row.**
  - `TabBar` takes `fixed?: boolean`: no "+", no lock icon in either `SortableTab` branch (`:122` icon-only, `:187`
    normal), no context menu, and no close by middle click or ×. Drag reorder calls `reorderWorkspaceTabs('watch', …)`.
  - `App`: with `watch` active, this row is shown whatever `tabPosition` is.
  - Tests:
    - no "+";
    - no lock icon in both branches;
    - a middle click and a context-menu event call nothing;
    - reorder;
    - the row is shown with `tabPosition: 'left'`.
- **T-a1.5 `openWatchArea(target)`** (`features/workspace/lib/open-watch-area.ts`).
  - `target` is `{tab:'guardian'}` (from W5a3 on) or `{host: fleetHostId}`.
  - It ensures the workspace, reconciles, remembers the return workspace when entering from elsewhere, and selects
    the tab. Selection follows the `handleSelectTab` path: active tab, active workspace, and the workspace's active tab.
  - A host listed by the fleet but without a tab yet gets one first.
  - Tests: entering from a session workspace; from inside the area; an unknown host id selects the first tab;
    restarting with `watch` active reopens the area.
- **T-a1.6 Host cells.**
  - `FleetCells.tsx:46-53` `open(v)` calls `openWatchArea({host: v.id})` for every host, daemon and probe.
  - The interim Hosts-page jump and `requestProbeFocus` leave here; W5c1 uses the editor inside the area.
  - Tests: daemon and probe cells open their tab. M: keep the old target → red.
- **T-a1.7 The host tab, first part** (`components/watch/WatchHostPane.tsx`).
  - The header follows spec §6:
    - `HostBadge` 20 px with the cell's look (`lib/host-look`);
    - the alias as `h2`;
    - the zone word in its colour: normal 正常, warning 偏高, danger 過載, offline 離線;
    - 「量測：{primary alias}（主）」, and when offline 「・最後一次成功：{time}」.
  - 行程（每 5 秒更新）: two tables, CPU 前 5 名 and 記憶體前 5 名 (PID · 行程 · CPU % or MB), from `slow.top_cpu` and
    `top_mem`. Offline: 「離線，沒有資料」.
  - Empty states: no snapshot → 「讀不到主機群資料」; a host this snapshot does not list → 「讀不到這台主機的資料」.
  - Tests: the header word for each zone and offline; the two tables; both empty states.
- **T-a1.8 i18n**: zh-TW and en keys (`locales/*.json`, `locale-completeness.test.ts`).

## 4. W5a2 — the host tab's 資源

- **T-a2.1 Metric defs** (`lib/watch/metrics.ts`, from the prototype's `METRICS`).
  - The readings: cpu, mem, level, page_rate, disk; `HISTORY_ITEMS` already has them.
  - Each def has a `tone` rule: `usageTone` 70/90 for cpu and mem; level 1 / 2 / 4 → ok / warn / error; page_rate
    > 50 warn and > 800 error; disk ≥ 85 warn and ≥ 95 error.
  - Each def also has `warnAt`, `yMax`, `fmt`, and the gauge fraction: level → 33 / 66 / 100 %, level 0 → no arc;
    page_rate full at 800.
  - Tests: tables of edges (e.g. 69.9 / 70 / 90, level 0, page_rate 50 / 51 / 800 / 801).
- **T-a2.2 Gauge** (`components/watch/Gauge.tsx`): 56 px, 4 px stroke, track `--border-default`, round cap,
  `ringTransform(56, 'ccw')` (`lib/usage-display.ts:118`), value in the middle. Offline: `—` and no arc. Tests: the arc
  length for 0 / 50 / 100, tone class, offline.
- **T-a2.3 Chart** (`components/watch/MetricChart.tsx`; moves the prototype's `stretches` and `SeriesPaths` from
  `prototypes/load-control/ui.tsx`).
  - It takes `HistoryPoint {t, avg, max}` from `fleetHistory.series(host, item, range, now)` and maps `t` onto
    [now − range, now].
  - The average is a line and the peak a faint band. A dashed warning line; only the stretches above it take the
    warning or error colour.
  - A shared axis under the last row (現在 … −n 分／小時), and 「累積中：已收集 n 分鐘」 while `now − first.t` is
    shorter than the range (the history has no helper for it).
  - Tests: stretches split at the warning line; the band uses `max`; the collected minutes; an empty series.
- **T-a2.4 The section**:
  - 資源（{range}）: five gauges, then one row per reading (label · chart · 「平均 … · 峰值 …」 over the range). The
    range switch (5 分／1 時／6 時／24 時) sits in the header. A re-render on each `useFleetStore` snapshot change, so
    the 1 s pushes redraw; the history has no subscription.
  - `lib/watch/view-memory.ts` keeps the range per host (W5b1 / W5c1 add the queue sub-tab and the 狀態｜設定 choice).
  - Tests: range switch; **tab switch with a real `TabContent`** (`keepAliveCount: 0`, as
    `WorkerDeckPane.tab-switch.test.tsx` does): pick 1 時, switch to another tab and back, still 1 時.
    M: keep the range in `useState` → red.

## 5. W5a3 — the watcher in the area

- **T-a3.1 Watch status.**
  - `lib/watch/api.ts` `fetchWatchStatus(hostId)` uses the same host fetch helper as `lib/fleet/api.ts`.
    - 404 → `unsupported`.
    - A malformed body or an unknown `state` → `error`. The `state` values are `awake | resting | none`.
  - `lib/watch/status-store.ts` keeps `byHost[hostId] = {kind: 'ok', status} | {kind: 'unsupported'} | {kind:
    'error'}`, with `at`.
    - A read runs when a host connects (the fleet support's trigger) and every 30 s while `document.visibilityState`
      is visible.
    - A host that leaves is forgotten.
  - `deriveGuardian(byHost)` →
    `{state: 'awake' | 'resting' | 'none' | 'unknown', hostId?, alias?, sessionId?, since?, mode?, reachable}`:
    1. hosts whose `self.state` is awake: the earliest `since`, then the smaller host id (load-control §12.3);
    2. else a resting `self`: the latest `since`;
    3. else any `current` that is awake or resting: `reachable: false`, because the holder is not connected to this App;
    4. else `none` when at least one host answered `ok`;
    5. else `unknown`.

    `mode` is the holder's own status `mode`, which is that host's `watch` row.
  - Tests: each rule; a 404 host plus an ok host gives `none`; a timer test (fake timers) for the 30 s read and for
    visibility.
- **T-a3.2 The holder's `watch` row** (`lib/watch/watch-row.ts`).
  - `GET /api/hostconfig`, field `watch`, read when the holder changes. It is parsed leniently: unknown fields are
    kept for W5c1's whole-row write.
  - `modelOf(agentCommand)` takes `--model <x>` or `--model=<x>`, else nothing.
  - W5c1 reuses this reader for its 守門員 section.
  - Tests: parse, `modelOf` cases.
- **T-a3.3 The shield** (`features/workspace/components/WatchShieldButton.tsx`, after `ProfileConflictButton` in
  `HomeRow.tsx:49`).
  - Phosphor `ShieldCheck`, 15 px. The looks:

    | state | look | title |
    |---|---|---|
    | none | outline, `text-muted` | 守門員（未啟動） |
    | unknown | outline, `text-muted` | 守門員（讀不到狀態） |
    | awake | filled, `status-success` | 守門員（醒著） |
    | resting | filled, `status-success` | 守門員（休息中） |

  - Click → `openWatchArea({tab:'guardian'})` (W5e: 事件 while the count is > 0). `aria-pressed` while `watch` is
    active.
  - Not in the narrow bar, which has no `HomeRow`; there the host cells are the way in.
  - Tests: the four looks, the click, aria-pressed.
- **T-a3.4 The guardian tab's content** (`features/workspace/lib/watch-area.ts` reconcile +
  `components/watch/WatchGuardianPane.tsx` for the panel kind). The reconcile now creates the guardian tab first, and
  sets its pane content from `deriveGuardian` when that derivation changes (edge-triggered, §1):
  - **awake or resting, with the holder connected and its session found** → `{kind: 'tmux-session', hostId: appHost,
    sessionCode, mode: 'terminal', cachedName: name, tmuxInstance}` (the builder of `lib/open-session-tab.ts:45`):
    - Holder: `deriveGuardian().hostId` (a daemon host id) maps to the App host whose `daemonId` matches, as
      `fleetHostsView` does.
    - Session: `useSessionStore.sessions[appHost].find(s => s.name === name)`. `name` is the status's `tmux_session`
      when A3 adds it, and `pdx-watch` until then.
  - **otherwise** → `{kind: 'watch-guardian'}`, whose panel reads:
    - none: 「這台主機群目前沒有守門員」 and 「啟動後，它會看管重指令名額與記憶體壓力，事件交給它處理。」 (the start
      button comes in W5e);
    - unknown: 「讀不到守門員狀態」;
    - the holder not connected to this App: 「守門員在 {alias}，這台 App 沒有連到它」;
    - the holder connected but its session list has no `pdx-watch`: 「{alias} 上沒有 pdx-watch」.
  - A session list that is still loading leaves the content as it is (it is not read as "no pdx-watch").
- **T-a3.5 The terminated branch.** `SessionPaneContent`: when the pane is terminated and its tab is `watch-guardian`,
  render `WatchGuardianPane`'s panel for the current derivation instead of `TerminatedPane` (no rebuild offer). Nothing
  else in the stock session tab changes.
- **T-a3.6 Tests**:
  - each content case of T-a3.4, through the real reconcile;
  - the three views: with the guardian tab holding the session, the bottom status bar is the stock one and its view
    buttons switch terminal → Deck → Chat and back, through the real `StatusBar`, `SessionPaneContent` and
    `useSessionViewStore` (the same as a session tab);
  - a failover: the status goes from host A awake to host B awake → the content becomes B's session;
  - edge-triggered: a content that arrived by sync is not overwritten while this device's derivation is unchanged, and
    two devices with the same derivation converge (two reconcilers and one `applyTabs` each way);
  - terminated: the guardian tab shows the panel, and no rebuild button exists;
  - a tab switch away and back keeps the chosen view (the stock session-view store);

  M: enforce the content on every run (not edge-triggered) → the sync test goes red; show `TerminatedPane` for the
  guardian tab → the terminated test goes red.
- **T-a3.7 i18n** for the above.

## 6. W5b — the queue wire (53-pe)

- **T-b.1 `internal/fleet/load.go` `QueueSummary`** gains:
  - `Holders []QueueHolder`, from `st.Heavy` then `st.Light`: `{class, slot, command (shorten), project (projectOf),
    session, since (Started), pid (the gate's pid: the 名額鎖 column)}`, at most `maxQueueShown`. **Never the slot's
    `Token`**: `QueueHolder` has no such field, and a test asserts that no `token` key is serialised.
  - `Session` on `QueueWaiter`.
  - `AdmissionReason string`, and `HeavyPaused` / `LightPaused bool`: true when the status's effective settings carry
    `slots.<class>` = `0` (W3c lets a class be set to 0, #3020; an older gate refuses 0 and falls back to the default,
    so a 0 means W3c). `QueueSummary` already reads `slots.*` from `st.Settings` for the totals, so this needs no new
    key from the A line, whatever W3c's own `paused` JSON looks like.

  The status's own fields decide when present: the local `gate.Status` always carries `Admission`. A remote
  `pdx gate status --json` from an older pdx lacks it, and an absent object must stay distinct from "not held":
  `frame.go:299-311` decodes into `gate.Status`, where the value struct cannot tell the two apart. So `frame.go`
  decodes into a wrapper with `Admission *gate.AdmissionState` and passes the presence on. `internal/gate` is not
  changed (it is shared with the A line). Without the object, the existing rebuild decides and the reason is empty.
- **T-b.2 Golden JSON**: update the fixtures shared with the SPA (`spa/src/lib/fleet/__fixtures__/fleet-changed.json`,
  `api-fleet-primary.json`) and their daemon golden tests. The remote frame's `gateShare` cut (`internal/fleet/loop.go:15`,
  32 KiB) is unaffected, because the holders are already in `--json`. Tests with a captured `pdx gate status --json`
  of two holders and five waiters, and a summary of 20 holders (the cap).
- **T-b.3 SPA wire** (`lib/fleet/types.ts`, `parse.ts`):
  - `FleetQueue.holders: FleetHolder[]`, `admission_reason: string`, `heavy_paused` / `light_paused: boolean`;
  - `FleetWaiter.session: string`.

  Parsing is lenient: a missing field becomes `[]`, `''` or `false`, so a 744 daemon still parses. Parse tests for
  both shapes.
- **Exposure**: `/api/fleet` and `fleet.changed` already carry commands and projects to the App's device and admin
  tokens. The session id joins them. The fleet's peer routes (role and announce) carry no frames.
- **Tests**:
  - holders from both pools; the cap; the session carried;
  - admission from the status vs rebuilt; an old status without the object; `slots.light=0` → light paused with a
    total of 0, and no `slots.*` → not paused;
  - no token.

  M: drop the presence check → "old status" goes red.
- **Commands**: `go test -race ./internal/fleet/`, `go test ./internal/module/fleet/`, `go vet`, `gofmt -l`; SPA
  `vitest related --run` on the two files.

## 7. W5b1 — queue 當下 UI

Spec §6.1 重指令排隊 (daemon hosts only):
- the 當下｜歷史 switch (歷史 disabled until W5d) in view memory;
- 「排隊 n · 最久 m:ss」;
- the two lanes, each with 「名額 used / total」, slot cards (running: accent, command, elapsed; free: dashed 空名額);
- a paused class (`*_paused`, or a total of 0): the lane shows 「已暫停」 and its gate line 「暫停（新指令排隊）」 in
  status-warning;
- the gate line (「等名額」 when the slots are full, 「暫停放行」 and status-error while `admission_held`, with
  `admission_reason` as its title);
- the waiting cards (place, command, wait; warning past 2 min);
- the table 執行中 / 排隊中（n）;
- hover pairing between a card and its row;
- elapsed times counted locally each second from `since`;
- offline: 「這台離線，排隊與名額資訊暫停。」

`components/watch/QueueNow.tsx` and `QueueLane.tsx` (from the prototype's `Queue.tsx`). Tests: lanes with free and
used slots, a paused lane, the gate words, waiting warning at 2 min, the pairing, a tab switch keeps the sub-tab.

## 8. W5b2 — the hourglass

- `BackgroundKind` gains `'queued'` (`stores/useAgentStore.ts:88`), `ICONS.queued = Hourglass`
  (`components/BackgroundSymbol.tsx:11`), ranked above workflow / monitor / schedule wherever the tab aggregate picks
  the highest kind (`hooks/useTabDisplay.ts`, N6).
- Source: a selector over the fleet snapshot, the session ids of all `waiters` of all daemon hosts →
  `Map<sessionId, Waiter[]>`; a pane is queued when its agent's session id (`useAgentStore` provenance `sessionId`) is
  in it. Subagent commands carry the main session's id (spec §2.3).
- Tooltip 「排隊中：第 {place} 位・已等 {m} 分鐘・{command}（重指令｜輕指令）」; with more than one, the longest
  wait plus 「・另有 n 個」. Same colour as the other symbols. Drawn only where the others are (`!lightsOff`).
- Tests: rank over workflow, gone when the waiter goes, the tooltip for one and three, no fleet → none.

## 9. W5c1 — settings without the A line

- The header's 狀態｜設定 `SegmentControl` (view memory; tab switch test). The range switch shows on 狀態 only.
- **Daemon hosts, the 守門員 section**. The host's `watch` row is read with W5a3's reader. It is written with
  `PUT /api/hostconfig/watch` as a read-modify-write of the whole row with its revision: the same `putHandler`
  contract as `lib/fleet/config-api.ts`, where a 409 means reload and say so. Its fields:
  - 啟用守門員;
  - the seven detection fields (`x_cores`, `y_mb`, `t_s`, `pressure_level`, `pressure_t_s`, `page_rate`, `page_t_s`);
  - 通知與開啟順序 (`targets`);
  - 模式 (常駐／巡檢) and 巡檢閒置分鐘;
  - 自動喚醒;
  - 啟動指令 (`agent_command`; the spec's 模型 became this on the A line, 10-16);
  - 工作目錄;
  - 啟動 prompt and 喚醒 prompt (textareas; empty = built-in).

  No slot bounds (spec U10, #3020). The slot counts themselves are gate settings (W5c2, 0–64, 0 = paused).

  Each value carries its source (預設 when the row does not set it). A failed write restores the field and shows the
  daemon's 400 text under it. When the two daemons' `targets` differ, a warning line. Offline: nothing editable.
- **Probe hosts**: the note (spec §6.3), then F-2a2's probe-host row editor for that row (`components/hosts/`),
  saving to both daemons as it does today.
- **Tests**:
  - load; one write; 409 reload; the 400 text shown and the field restored;
  - the `targets` warning; offline;
  - the probe editor saves both daemons.

  M: write only the changed field (not the whole row) → the round-trip test goes red.

## 10. W5c2, W5d — after A2 and A1

- **W5c2** (A2 `GET/PUT /api/gate/config`, `POST /api/gate/profile`): 設定組 select, 資源管制 switch (off dims the next
  section), and the gate fields of spec §6.2.3, each one `PUT {key, value}` with the 400 reason under the field. Tests
  as W5c1. Device-token 403 is the daemon's; the App uses the host's admin token as for host config.
- **W5d** (A1 `GET /api/gate/decisions`): the 歷史 sub-tab of spec §6.1: the class switch, rows newest first, the
  stacked bar on one scale, the four rusage columns (「—」 when absent or for records before 743 with `session: null`),
  and the hover card. Read on open and every 30 s while shown. Tests: bar scale, absent fields, failed records, the
  hover card.

## 11. W5e — after A3

- **Pushes**: `watch.changed` and `watch.events` join the host event stream (`hooks/useMultiHostEventWs.ts:324`,
  beside `fleet.changed`, with the same connection-key guard; the `lib/host-events.ts:28` union). The 30 s status timer
  goes:
  - a `watch.changed` updates the host's status;
  - a `watch.events` re-reads that host's events.
- **The 事件 tab** (`watch-events`): the reconcile creates it after the guardian tab once any connected host declares
  `watch.v1`. It has all of spec §5:
  - the count line and the guardian line;
  - three chip filters (view memory) and day separators;
  - the rows with the A line's fields;
  - the offline note per host.
- **The count**: the shield's `CountBadge` and the tab label 事件 n. The shield opens 事件 while n > 0.
- **啟動守門員**: `POST /api/watch/start`, answered 200, 202 `{host}` or 409 `{cooldown_until}`.
- **Waking a resting watcher from the guardian tab** (`POST /api/watch/wake {prompt}`, ≤ 4 KB with the wake prompt):
  where the person types it, given that the tab stays the stock session tab (for example Deck's own input while the
  watcher rests), is settled with the user before W5e.
- **No 全部略過** (user, 10-16, #3017): nothing in the area changes an event.
- **Tests** for each item. The A line tests the `watch.v1` declaration with the real module (1f, 10-16).

## 12. Acceptance per deploy

- **After W5a1**, on both Macs:
  - a cell click opens that host's tab;
  - the area is not in the sidebar;
  - Cmd+T in the area opens a tab in the previous workspace;
  - the tab order is the same on the second Mac after a sync, and its sidebar has no 守門員專區 row.
- **After W5a2**: the charts fill over 5 min.
- **After W5a3**:
  - the shield's state matches `pdx watch status` on mlab;
  - the guardian tab shows mlab's `pdx-watch` terminal while the watcher runs there.
- **After W5b**: `GET /api/fleet` on the primary shows `holders` with a session while a heavy command runs.

## 13. Asks and open points

- **Q1**: decided 10-16 (#3017): no 全部略過, no skip route, `watch.events` causes are `recorded | acked` only.
- **Q3**: decided 10-16 — the guardian tab is a full session tab with terminal, Deck and Chat (§1). Only the rebuild
  of a terminated pane is replaced by the panel, because the daemon restarts the watcher.
- **Q4 (A line)**: `watch.v1` in `/api/info` when W2b's events, ack and `watch.events` land, with the real-module
  test. Agreed by 1f, 10-16.
- **A3's `tmux_session`**: W5a3 uses the fixed `pdx-watch` until the status carries it.
- **W3c (A line)**: the gate accepts 0 slots (#3020). W5b tells a paused class from its effective `slots.<class>` = 0,
  not from W3c's own `paused` key (told to 1f, 10-16).
