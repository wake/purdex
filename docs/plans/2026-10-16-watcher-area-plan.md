# Watcher area — plan (rev 1: W5a1 … W5e)

Spec: `docs/specs/2026-10-16-watcher-area-spec.md` (B line, with A3 as agreed in #3010). Load-control spec §12.4–§12.6
for the watcher itself. Facts below were read on origin/main `bd4b0d7d1` (alpha.745 + #3014), 2026-10-16.

**Flow: the quick one** (user-facing, wanted soon): this plan → one focused subagent review → TDD by subagents (Sonnet,
one PR per phase, own worktree) → one full subagent review per PR → merge. No codex round. W5b (daemon) also goes past
1f (`internal/fleet` is shared with the A line's sampler).

## 1. Design choices (for the reviewer)

- **The area is a real workspace** (`WATCH_WORKSPACE_ID = 'watch'`, next to `UNSORTED_WORKSPACE_ID`): it is an ordinary
  syncable workspace in the `workspaces` section and `tabs.watch` (Profile Sync carries its tab order, spec §3), and the UI
  hides it. Excluding it from the `workspaces` section does not work: `applyTabs` drops a `tabs.<id>` whose workspace is
  unknown (`lib/profile/applier.ts:235`), and the order would not travel.
- **Fixed tab ids**: `watch-guardian`, `watch-events`, `watch-host-<fleet id>` (pane id `<tab id>.p`). Two devices, or two
  windows (`adopt-standalone.ts:176` warns of these), that create the area each make the same tabs, so a sync apply or a
  rehydrate converges instead of duplicating. Generated ids are 6 chars of `[0-9a-z]` (`lib/id.ts`) and never contain `-`,
  so they cannot collide.
- **`watch-host.hostId` is the fleet host id** (a daemon's host id or a probe row's id). It is global already, so
  `lib/profile/host-identity.ts` `mapContent` leaves it alone (asserted).
- **The three kinds sync** (`KIND_DECISION` 'syncs' in `lib/profile/sections.test.ts`; `DEVICE_LOCAL_PANE_KINDS` stays at
  7). A tab made only of device-local kinds is never sent, so the order would not travel.
- **Created on first open, then kept in step.** `openWatchArea` ensures the workspace. While it exists, a subscription
  (`startWatchArea()`, installed in `main.tsx` beside `startStandaloneAdoption`) reconciles its tabs with the fleet's host
  list. Each run happens only in a settled world (`readMasterWorld().settled`, as adoption does) and writes only on a
  difference. An apply or a profile switch that removes the workspace leaves it removed until the next open: the area
  is never pushed into a world that does not have it.
- **Locked tabs, a fixed tab row.** Tabs are `locked: true`, and every close path already refuses them
  (`useTabStore.ts:704`, `features/workspace/store.ts:237`, `lib/tab-lifecycle.ts:11`, the menu). `toggleLock` refuses
  tabs of `watch` (store level, so `WorkspaceSettingsPage.tsx:200` is covered). `TabBar` gains `fixed`: no "+", no lock
  icon (`SortableTab.tsx:122/187`), no context menu, no middle click; dragging to reorder stays. While `watch` is active,
  `App` shows this top tab row whatever `tabPosition` is, because the area is not in the sidebar and its tabs would
  otherwise have no row.
- **Nothing else enters or leaves the area.** `insertTab` sends a non-watch tab whose target resolves to `watch` to the
  return workspace (the last active workspace other than `watch`, kept in memory) or else the first non-watch workspace.
  Sessions opened, Cmd+T and "+" while the area is shown all go there. Watch tabs cannot be dragged onto a sidebar
  workspace, and `removeWorkspace('watch')` is a no-op that is never chosen as an heir.
- **Watcher status without a push yet**: `GET /api/watch/status` exists (`internal/module/watch/module.go:97`, shape
  `{self: {host_id, state, session_id?, since?…}, current?, last?, enabled, auto_wake, mode, targets}`); `watch.changed` /
  `watch.events` and a capability do not. W5a reads the status of every connected host on connect and every 30 s while
  the window is visible; a 404 means "no watch support". W5e replaces the timer with the pushes.
- **The guardian pane renders the real terminal through a synthetic pane** (W5a3). `SessionPaneContent` works with a
  pane that is not in the tab store, but its per-pane writes (rebuild, re-point, terminated, the session strip's
  `PaneModeButtons`) need a stored pane and silently do nothing otherwise (`SessionPaneContent.tsx:87-102`,
  `TerminatedPane.tsx:72-176`, `StatusBar.tsx:169/377`). The guardian tab therefore draws its own strip (status text
  only) and handles a missing `pdx-watch` itself. **Rebuild and detach are not offered** (the daemon owns the watcher;
  a rebuild would start a second agent outside `pdx-watch`). **Deck is deferred** (open point Q3, §13). This changes
  spec §4, and the prototype showed all three buttons.

## 2. Phases

| PR | content | depends on | who | est. lines (code + tests) |
|---|---|---|---|---|
| W5a1 | pane kinds, the `watch` workspace and its guards, `fixed` TabBar, `openWatchArea`, watch status store + shield, host cells' click, host tab header + 行程, guardian status panel | — | B subagent | ~1000 |
| W5a2 | host tab 資源: gauges, charts, range switch, history adapter, view memory | W5a1 | B subagent | ~800 |
| W5a3 | guardian tab: the `pdx-watch` terminal on the holder, strip text, resting band (no input yet) | W5a1 | B subagent | ~500 |
| W5b | queue wire: `slow.queue.holders`, `waiters[].session`, `admission_reason` (daemon + golden JSON + SPA types/parse) | — | 53-pe | ~400 |
| W5b1 | queue 當下 UI: lanes, gate line, waiting cards, table, hover pairing | W5a1, W5b | B subagent | ~700 |
| W5b2 | the hourglass on the agent icon | W5b | B subagent | ~350 |
| W5c1 | 狀態｜設定 switch; 設定: the 守門員 section (existing API, with `slot_bounds`), probe-host settings (F-2a2's editor) | W5a1 | B subagent | ~800 |
| W5c2 | 設定: 設定組, 資源管制, gate settings | W5c1, A2 | B subagent | ~600 |
| W5d | queue 歷史 | W5b1, A1 | B subagent | ~700 |
| W5e | 事件 tab, the count (shield, tab label), the two pushes, 啟動守門員, the wake input | W5a3, A3 | B subagent | ~900 |

This replaces spec §9's W5a/W5b/W5c rows (one PR each would be 1700 / 1100 / 1400 lines). The spec is updated in the
same docs PR as this plan. Line counts include tests. Going over 800 lines is accepted when the PR is one reviewable
unit and about half of it is tests, as in the fleet plan; the PR body lists code and test lines separately.

Order: **W5a1 and W5b now, in parallel.** After W5a1: W5a2, W5a3 and W5c1 (they touch different files; at most two
implementation subagents at once, because mlab's heavy slot count is 1). W5b1 after W5b. W5b2 can go any time after W5b.
W5c2, W5d and W5e wait for A2, A1 and A3.

Every PR: TDD, one commit per task, and a mutation table in the PR body (each named mutation made, the test seen red,
then reverted). SPA: `vitest related --run <changed files>`, `tsc -b --force` (worktree `node_modules` is a symlink: a
plain `-b` can be green from another worktree's cache), and `eslint` on the changed files. Never the full vitest or the
full lint (test tiers, 10-15). Daemon: `go test` of the touched packages one command at a time, `-race` on
`internal/fleet`, `gofmt -l`, `go vet`; never `./...`. Every brief says that the subagent may stop only its own
commands, by killing its own pid outside the pdx gate (`pkill -f` and `killall` are forbidden).

## 3. W5a1 — the area, the shield, the cells

- **T-a1.1 Pane kinds.** `types/tab.ts`: `{kind:'watch-guardian'}`, `{kind:'watch-events'}`,
  `{kind:'watch-host'; hostId: string}`. `lib/pane-labels.ts`: labels 守門員 / 事件 / the host's alias (the fleet
  snapshot's `alias`, falling back to the id). Icons: `ShieldCheck`, `ListBullets`, `Desktop`, added to
  `components/tab-icon-map.tsx`. `lib/route-utils.ts` → `'/'`. `contentMatches`: `watch-host` by `hostId`, the other two
  by kind. Register one non-disableable module `watch-area` with the three panes (`lib/register-modules/index.tsx`).
  Tests: `KIND_DECISION` 'syncs' ×3; `isLightTab` is false for each; `mapContent` leaves `watch-host.hostId` unchanged.
  M: mark a kind device-local → the order test of T-a1.2 goes red.
- **T-a1.2 The workspace.** `features/workspace/store.ts`: `WATCH_WORKSPACE_ID`, `ensureWatchWorkspace()` (appended
  last, name `t('watch.area.name')` 守門員專區, idempotent), `reconcileWatchTabs(hosts: string[] | null)`:
  - the guardian tab exists, locked (W5e adds the events tab after it);
  - with `hosts` given, a host tab is appended for each host that lacks one, and the tabs of hosts no longer listed are
    removed (bypassing the lock, the area's own removal);
  - `hosts === null` (no fleet snapshot: fleet off or not read yet) leaves the host tabs as they are;
  - watch tabs found in another workspace (`repairTabOwnership` can put them in Unsorted) move back, and non-watch tabs
    found in `watch` move to the return workspace;
  - the user's order is kept.

  Tests: create; idempotent; append and remove follow the host list; null keeps; the order is kept across a reconcile;
  a `tabs.watch` payload round-trips through `applyTabs` with order and `locked`; two reconciles with different
  starting order give the same tab set. M: drop the null guard → "null keeps" red.
- **T-a1.3 Guards.**
  - `App.tsx:208` passes `workspaces` without `watch` (both bars).
  - `useShortcuts`: switch-workspace-N and prev/next skip `watch`; Home selects the first non-watch workspace.
  - `removeWorkspace`: no-op for `watch`, never its heir.
  - `insertTab`: the return-workspace rule (§1).
  - `toggleLock` refuses for tabs of `watch`.
  - Cross-workspace drag (`ActivityBarWide` `onMoveTabToWorkspace`): it refuses watch tabs as the source, and `watch` as
    the target.

  Tests: one per guard. M: remove the `insertTab` redirect → opening a session while the area is shown puts it in
  `watch`.
- **T-a1.4 The fixed tab row.**
  - `TabBar` takes `fixed?: boolean`: no "+", `SortableTab` draws no lock icon, no context menu, no middle click; drag
    reorder goes to `reorderWorkspaceTabs('watch', …)`.
  - `App`: with `watch` active, this row is shown whatever `tabPosition` is.
  - Tests: no "+" and no lock icon in fixed mode; reorder; shown with `tabPosition: 'left'`.
- **T-a1.5 `openWatchArea(target)`** (`features/workspace/lib/open-watch-area.ts`): `target` is `{tab:'guardian'}` or
  `{host: fleetId}`. It ensures the workspace, reconciles with the current host list, remembers the return workspace
  when entering from elsewhere, and selects the tab (the `handleSelectTab` path: active tab, active workspace, workspace
  active tab). A host with no tab yet (the fleet just listed it) gets one first. Tests: from a session workspace; from
  inside the area; an unknown host id falls back to the guardian tab.
- **T-a1.6 Watch status.**
  - `lib/watch/api.ts` `fetchWatchStatus(hostId)`, through the same host fetch helper as `lib/fleet/api.ts`. 404 →
    `unsupported`. A malformed body or an unknown `state` → `error`; the `state` values are `awake | resting | none`.
  - `lib/watch/status-store.ts`: `byHost[hostId] = {kind: 'ok', status} | {kind: 'unsupported'} | {kind: 'error'}`, with
    `at`. A read runs when a host connects (the fleet support's trigger) and every 30 s while `document.visibilityState`
    is visible. A host that leaves is forgotten.
  - `deriveGuardian(byHost)` →
    `{state: 'awake' | 'resting' | 'none' | 'unknown', hostId?, alias?, sessionId?, since?, reachable}`:
    1. hosts whose `self.state` is awake: the earliest `since`, then the smaller host id (load-control §12.3);
    2. else a resting `self`: the latest `since`;
    3. else any `current` that is awake or resting: `reachable: false`, because the holder is not connected to this App;
    4. else `none` when at least one host answered `ok`;
    5. else `unknown`.
  - Tests: each rule; a 404 host plus an ok host gives `none`; a timer test (fake timers) for 30 s and visibility.
- **T-a1.7 The shield** (`features/workspace/components/WatchShieldButton.tsx`, after `ProfileConflictButton` in
  `HomeRow`).
  - Phosphor `ShieldCheck` 15 px. Looks: none → outline, `text-muted`, title 守門員（未啟動）. Awake / resting →
    filled `status-success`, titles 守門員（醒著）/（休息中）. Unknown → outline, title 守門員（讀不到狀態）.
  - Click → `openWatchArea({tab:'guardian'})` (W5e: 事件 while the count is > 0). `aria-pressed` while `watch` is
    active.
  - Not in the narrow bar, which has no `HomeRow`; there the host cells are the way in.
  - Tests: four looks, click, aria-pressed.
- **T-a1.8 Host cells.** `FleetCells.tsx:46-53` `open(v)` → `openWatchArea({host: v.id})` for every host, daemon and
  probe. The interim Hosts-page jump and `requestProbeFocus` leave here; W5c1 uses the editor inside the area. Tests:
  daemon and probe cells open their tab. M: keep the old target → red.
- **T-a1.9 The host tab, first part** (`components/watch/WatchHostPane.tsx`).
  - Header as spec §6: `HostBadge` 20 px with the cell's look (`lib/host-look`), the alias as `h2`, the zone word in
    its colour (normal 正常, warning 偏高, danger 過載, offline 離線), and 「量測：{primary alias}（主）」. Offline
    adds 「・最後一次成功：{time}」.
  - 行程（每 5 秒更新）: two tables, CPU 前 5 名 and 記憶體前 5 名 (PID · 行程 · CPU % or MB), from `slow.top_cpu` and
    `top_mem`. Offline: 「離線，沒有資料」.
  - No fleet snapshot: 「讀不到主機群資料」. A host gone from the snapshot keeps its last frame until the reconcile
    removes the tab.
  - Tests: header words for each zone and offline; the two tables; the empty states.
- **T-a1.10 The guardian tab, first part** (`components/watch/WatchGuardianPane.tsx`): a centred panel from
  `deriveGuardian`.
  - none: 「這台主機群目前沒有守門員」 and 「啟動後，它會看管重指令名額與記憶體壓力，事件交給它處理。」 (the button
    comes in W5e).
  - awake / resting: 「守門員在 {alias}・醒著／休息中」 (W5a3 replaces it with the terminal).
  - unknown: 「讀不到守門員狀態」.
- **T-a1.11 i18n**: zh-TW and en keys for all of the above (`locales/*.json`, `locale-completeness.test.ts`).

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

## 5. W5a3 — the guardian tab's terminal

- **T-a3.1** The holder's App host: `deriveGuardian().hostId` (a daemon host id) → the App host whose `daemonId`
  matches (`fleetHostsView` does the same mapping). The tmux session: `pdx-watch` (load-control §12.4; A3's
  `tmux_session` replaces the constant when the status carries it). The session: `useSessionStore.sessions[appHost]`
  `.find(s => s.name === 'pdx-watch')`.
- **T-a3.2** Awake or resting, holder connected, session found → the strip and `SessionPaneContent` with a synthetic
  pane:
  - the pane is `{id: '<tab pane id>:watch', content: {kind: 'tmux-session', hostId: appHost, sessionCode, mode:
    'terminal', cachedName: 'pdx-watch', tmuxInstance}}` (the builder of `lib/open-session-tab.ts:45`);
  - the strip reads 「守門員 · 在 {alias} · 醒著／休息中 · {巡檢模式｜常駐模式}」 (mode from the holder's status),
    followed by 「· {model}」 when the holder's `watch` row `agent_command` names `--model <x>`;
  - resting adds the band 「守門員休息中（{since} 起）」 above the pane (its input comes in W5e).

  The pane follows `deriveGuardian`: a failover re-derives and re-renders, because nothing is persisted.
- **T-a3.3** The other cases:
  - holder not connected to this App → 「守門員在 {alias}，這台 App 沒有連到它」;
  - holder connected but no `pdx-watch` in its session list → 「{alias} 上沒有 pdx-watch」;
  - none or unknown → T-a1.10's panel.
- **T-a3.4 Tests**:
  - each case;
  - a failover: the status changes from host A awake to host B awake → the pane re-binds to B's session code;
  - a tab switch away and back still shows the terminal (no persisted state);
  - no write to the tab store happens through the synthetic pane (spy on `setPaneContent` / `markTerminated`).

  M: persist the session code in the tab → the failover test goes red.

## 6. W5b — the queue wire (53-pe)

- **T-b.1 `internal/fleet/load.go` `QueueSummary`**:
  - `Holders []QueueHolder` from `st.Heavy` then `st.Light`: `{class, slot, command (shorten), project (projectOf),
    session, since (Started), pid (the gate's pid: the 名額鎖 column)}`, at most `maxQueueShown`;
  - `QueueWaiter` gains `Session`;
  - `AdmissionReason string`. When the gate status carries `Admission` (the local `gate.Status`, and the remote
    `pdx gate status --json` when the object is present), `admission_held` and `admission_reason` come from it.
    Otherwise the existing rebuild decides and the reason is empty. A status JSON without the object must stay
    distinguishable from "not held": decode its presence.
- **T-b.2 Golden JSON**: update the fixtures shared with the SPA (`spa/src/lib/fleet/__fixtures__/fleet-changed.json`,
  `api-fleet-primary.json`) and their daemon golden tests. Check that the remote frame's `gateShare` cut
  (`internal/fleet/loop.go:15`) still holds a status with two holders and five waiters (a test with a captured
  `pdx gate status --json`).
- **T-b.3 SPA wire** (`lib/fleet/types.ts`, `parse.ts`): `FleetQueue.holders: FleetHolder[]`, `admission_reason:
  string`, `FleetWaiter.session: string`. Lenient: missing → `[]` or `''`, so a 744 daemon still parses. Parse tests
  for both shapes.
- Tests: holders from both pools, the cap, session carried, admission from the status vs rebuilt, an old status
  without the object. M: drop the presence check → "old status" red. Commands: `go test -race ./internal/fleet/`,
  `go test ./internal/module/fleet/`, `./internal/execstat/` if any child process code moves (none expected), `go vet`,
  `gofmt -l`; SPA `vitest related --run` on the two files.

## 7. W5b1 — queue 當下 UI

Spec §6.1 重指令排隊 (daemon hosts only):
- the 當下｜歷史 switch (歷史 disabled until W5d) in view memory;
- 「排隊 n · 最久 m:ss」;
- the two lanes, each with 「名額 used / total」, slot cards (running: accent, command, elapsed; free: dashed 空名額);
- the gate line (「等名額」 when the slots are full, 「暫停放行」 and status-error while `admission_held`, with
  `admission_reason` as its title);
- the waiting cards (place, command, wait; warning past 2 min);
- the table 執行中 / 排隊中（n）;
- hover pairing between a card and its row;
- elapsed times counted locally each second from `since`;
- offline: 「這台離線，排隊與名額資訊暫停。」

`components/watch/QueueNow.tsx` and `QueueLane.tsx` (from the prototype's `Queue.tsx`). Tests: lanes with free and
used slots, the gate words, waiting warning at 2 min, the pairing, a tab switch keeps the sub-tab.

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
- **Daemon hosts, the 守門員 section**: the host's `watch` row from `GET /api/hostconfig` (field `watch`), written
  with `PUT /api/hostconfig/watch` as a read-modify-write of the whole row with its revision (the same `putHandler`
  contract as `lib/fleet/config-api.ts`; 409 → reload and say so). Its fields:
  - 啟用守門員;
  - the seven detection fields (`x_cores`, `y_mb`, `t_s`, `pressure_level`, `pressure_t_s`, `page_rate`, `page_t_s`);
  - 通知與開啟順序 (`targets`);
  - 模式 (常駐／巡檢) and 巡檢閒置分鐘;
  - 自動喚醒;
  - 啟動指令 (`agent_command`; the spec's 模型 became this on the A line, 10-16);
  - 工作目錄;
  - 啟動 prompt and 喚醒 prompt (textareas; empty = built-in);
  - 守門員可調的名額範圍 (`slot_bounds`, #3017): 最少 / 最多 for 重指令 and 輕指令 (default heavy 1–2, light 2–4;
    the daemon checks 1 ≤ min ≤ max ≤ 64 per class). A pair that the daemon refuses shows the 400 text under that
    pair. A 744 daemon's row without `slot_bounds` shows the defaults as 預設 and still writes.

  Each value carries its source (預設 when the row does not set it). A failed write restores the field and shows the
  daemon's 400 text under it. When the two daemons' `targets` differ, a warning line. Offline: nothing editable.
- **Probe hosts**: the note (spec §6.3), then F-2a2's probe-host row editor for that row (`components/hosts/`),
  saving to both daemons as it does today.
- Tests: load, one write, 409 reload, 400 shown and the field restored, the targets warning, offline, the probe editor
  saves both. M: write only the changed field (not the whole row) → the round-trip test goes red.

## 10. W5c2, W5d — after A2 and A1

- **W5c2** (A2 `GET/PUT /api/gate/config`, `POST /api/gate/profile`): 設定組 select, 資源管制 switch (off dims the next
  section), and the gate fields of spec §6.2.3, each one `PUT {key, value}` with the 400 reason under the field. Tests
  as W5c1. Device-token 403 is the daemon's; the App uses the host's admin token as for host config.
- **W5d** (A1 `GET /api/gate/decisions`): the 歷史 sub-tab of spec §6.1: the class switch, rows newest first, the
  stacked bar on one scale, the four rusage columns (「—」 when absent or for records before 743 with `session: null`),
  and the hover card. Read on open and every 30 s while shown. Tests: bar scale, absent fields, failed records, the
  hover card.

## 11. W5e — after A3

- `watch.changed` and `watch.events` join the host event stream (`hooks/useMultiHostEventWs.ts:324` beside
  `fleet.changed`, with the same connection-key guard; `lib/host-events.ts:28` union). The 30 s status timer goes: a
  `watch.changed` updates the host's status, and a `watch.events` re-reads that host's events.
- The 事件 tab (`watch-events`, created by the reconcile after the guardian tab once any host supports events). Spec §5
  in full: count line, guardian line, three chip filters (view memory), day separators, the rows with the A line's
  fields, the offline note per host. The shield's count (`CountBadge`) and the tab label 事件 n. The shield opens 事件
  while n > 0.
- 啟動守門員 (`POST /api/watch/start`: 200, 202 `{host}`, 409 `{cooldown_until}`), and the resting band's input
  (`POST /api/watch/wake {prompt}`, ≤ 4 KB with the wake prompt, 400 text shown).
- No 全部略過 (user, 10-16, #3017): nothing in the area changes an event.
- Tests per item. A capability from A3 (Q4) gates the events tab and the count, with the real-module `/api/info` test on
  the daemon side.

## 12. Acceptance per deploy

- After W5a1: on both Macs, the shield's state matches `pdx watch status` on mlab. A cell click opens that host's tab.
  The area is not in the sidebar. Cmd+T in the area opens a tab in the previous workspace. The tab order is the same on
  the second device after a sync.
- After W5a2 / W5a3: the charts fill over 5 min. The guardian tab shows mlab's `pdx-watch` terminal while the manual
  watcher runs there.
- After W5b: `GET /api/fleet` on the primary shows `holders` with a session while a heavy command runs.

## 13. Asks and open points

- **Q1**: decided 10-16 (#3017): no 全部略過, no skip route, `watch.events` causes are `recorded | acked` only.
- **Q3 (user)**: the guardian tab's strip without Deck / rebuild / detach (§1). Rebuild and detach are left out on
  purpose. Deck could come later through a stored pane, if the user wants the watcher in Deck view.
- **Q4 (A line)**: declare a capability (for example `watch.v1`) in `/api/info` once events, ack and the push are in
  (W2b). The App's events tab keys on it rather than on a 404, and the A line tests the declaration with the real
  module, as fleet.v1 now does.
- A3's `tmux_session` field: W5a3 uses the fixed `pdx-watch` until the status carries it.
