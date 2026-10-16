# Watcher area: the App's load-control UI (spec)

> B line, 2026-10-16. The App part of the host load control (`docs/specs/2026-10-15-host-load-control-spec.md` §12.6,
> plan W5) and the click target the fleet status spec (`docs/specs/2026-10-14-fleet-status-spec.md` §8.1) leaves to
> "the watcher area spec". Visual reference: the prototype v5 (`spa/prototypes/load-control/`, published at
> https://pages.mlab.host/wake/purdex/load-control/), built from the App's own components. Mac App only; iOS later.

## 1. User decisions (2026-10-15/16, do not reopen)

| # | Decision |
|---|---|
| U1 | One **watcher area**: a workspace of its own, apart from the session workspaces. Its tabs, in this order by default: **守門員** (the watcher, an ordinary session tab), **事件** (events of all hosts), then **one tab per host**. Tabs can be dragged, cannot be closed; the order goes with Profile Sync. |
| U2 | Entry: a shield at the right end of the sidebar's top row (`HomeRow`, the profile-name row the user calls "Main Station"), and the host cells under the BottomNav (fleet spec §8.1): a click opens the area on that host's tab. |
| U3 | Events are **not marked handled by a person**. The watcher acknowledges an event with a one-line note; the count is "waiting for the watcher". |
| U4 | Each host tab embeds **狀態｜設定**. 狀態: the five readings with a line chart each (5 分／1 時／6 時／24 時; the history lives in the App's memory only), the top processes, and for the daemon hosts the queue as **當下｜歷史**. |
| U5 | The queue history shows four new columns: 平均核心數, 記憶體峰值, 換頁拖累, 磁碟讀寫 (owed by the A line, §7). |
| U6 | Settings live on each daemon (the `watch` row in host config; the gate's `[gate]` through a daemon API), not Profile Sync; each daemon host has its own 資源管制 switch (`gate.enabled`). |
| U7 | Words: 正常／偏高／過載／離線 for a host's zone; the shield badge is the host cells' `CountBadge` (≈ 11 px), the shield is green. |
| U8 | Design and prototypes are done with Opus; the style follows the real App (the prototype imports the App's own `TabBar`, `Section`, `SettingItem`, `SegmentControl`, `ToggleSwitch`, `HostBadge`). |
| U9 | (10-16, #3017) **No 全部略過**: nothing in the area closes an event; an event is pending or handled by the watcher, nothing else. |
| U10 | (10-16, #3020, replacing #3017/#3019) **No bounds on the watcher's slot changes**: the watcher and the person have full control; a slot count is limited only by the gate's own range 0–64, and 0 pauses a class (new commands of it queue, running ones go on; A line W3c). The area has no slot-bounds setting. |

## 2. Entry points

### 2.1 The shield (HomeRow)

- Placed where the real `HomeRow` puts its conflict icon (right end of the row), Phosphor `ShieldCheck`, 15 px.
- States, from the watch status (§6) and the unhandled count:

  | State | Look | Title |
  |---|---|---|
  | no watcher (`current` empty and nobody awake or resting) | outline, `text-muted` | 守門員（未啟動） |
  | no host answered the status (an older daemon, or none reachable) | outline, `text-muted` | 守門員（讀不到狀態） |
  | awake | filled, `status-success` | 守門員（醒著） |
  | resting | filled, `status-success` | 守門員（休息中） |
  | unhandled events > 0 (any of the above) | the state's look + red `CountBadge` with the count | 守門員（n 件待處理） |

- Click: opens the area on **事件** when the count > 0, otherwise on **守門員**. `aria-pressed` while the area is shown.

### 2.2 Host cells

The fleet cells (F-2b) switch their click target from the interim one (the Hosts page) to the area's tab of that host.
Hover tooltips stay as F-2b built them.

### 2.3 The queued symbol on the agent icon (user, 10-16 07:5x)

- The agent icon's top-left symbol (`BackgroundSymbol`, one at a time: workflow > monitor > schedule) gains a fourth
  kind, **queued** (Phosphor `Hourglass`), **ranked first**: while any command of the session — its subagents' included,
  since a subagent's command carries the main session's id — waits in a host's gate, the hourglass replaces the other
  symbol; when the command starts, it goes and the other symbol comes back.
- One symbol for heavy and light; **the same colour as the other symbols** (`currentColor`), never a warning tint.
- Tooltip: 「排隊中：第 {place} 位・已等 {m} 分鐘・{command}（重指令｜輕指令）」; with more than one waiting command,
  the one that has waited longest, then 「・另有 n 個」.
- Source: the fleet frames' `slow.queue.waiters[].session` (W5b), matched to the session; 5 s late at most. No fleet
  (off, or no primary) → no hourglass (accepted). Drawn only where the other symbols are (not when the lights are off).

## 3. The area

- A reserved workspace (id `watch`) in the workspace store, built with the normal workspace and tab machinery (the
  real `TabBar`, drag to reorder, `locked: true` so close is not offered, no "+" tab). It is **not listed** among the
  sidebar's workspaces; it is reached from §2 only. Selecting any other workspace or session tab leaves it.
- Tabs and their pane contents (new `PaneContent` kinds):

  | Tab | Content | Label |
  |---|---|---|
  | 守門員 | `{ kind: 'watch-guardian' }` (§4: shows the watcher's tmux session pane when there is one) | 守門員 |
  | 事件 | `{ kind: 'watch-events' }` | 事件, or 事件 n while n > 0 |
  | host | `{ kind: 'watch-host'; fleetHostId }` — every host of the fleet (daemon and probe hosts); the fleet id, global, not an App host id | the host's alias |

- The host tabs follow the fleet's host list: a host added (probe host saved, a paired daemon) gets a tab appended at
  the end; a host removed loses its tab; the user's order is kept for the rest. Tab order is part of the synced
  workspace state (Profile Sync).
- The new pane kinds are not on the light-pane allowlist (`lib/pane-weight.ts`), so a tab unmounts when the user
  leaves it: the host tab's 狀態｜設定 choice, its range and its queue sub-tab, and the events filters, are kept outside
  the components (in memory, per host) and survive a tab switch (CLAUDE.md tab-hosted checklist, with a `TabContent`
  switch-away-and-back test).

## 4. The 守門員 tab

The watcher is an ordinary Claude Code session started by the daemon of the host that holds it (load-control spec
§12.4). The tab is a **full session tab** of that session (user, 10-16: 「要，就是現在的完整 tab 可用三種模式」): the
three views terminal, Deck and Chat, the bottom status bar, drafts and the conversation work as in any session tab,
and nothing is added around it (user, 10-16: 「守門員應該就是現在 tab 本來的樣子，包括底下 status bar」). One difference: when the watcher's session ends, the tab shows the watcher's
state (the table below) instead of offering a rebuild, because the daemon starts and wakes the watcher, and a rebuild
would run it outside `pdx-watch`.

| Watch state | The tab shows |
|---|---|
| none (no current, no last; or the `pdx-watch` tmux session is gone) | **The watcher's launch panel** (below) with **啟動守門員** → `POST /api/watch/start` (A3) on any reachable daemon host; the daemon runs §12.3's order (first reachable host in `targets`), creates `pdx-watch` in `workdir` and runs `agent_command` with the start prompt. Busy state while it starts; an error line if the start failed (the cooldown time from the answer). When the watcher registers, the tab becomes its session. |
| awake | The watcher's session on its host, as the stock session tab (terminal, Deck, Chat; the bottom status bar). |
| resting | The holder's `pdx-watch` session, which stays at a shell while the watcher rests (load-control spec §12.4/§12.6), as the same stock session tab: the terminal view shows the shell; in Deck and Chat, where a session with no agent shows the App's launch panel (`LaunchPanel`), the guardian tab shows **the watcher's launch panel** with **叫醒守門員** → `POST /api/watch/wake` (A3) on the holding host, which types `agent_command --resume <the kept session>` with the wake prompt into `pdx-watch`. "Resting" and "none" are different answers of the status and never look alike. |

- **Follows the watcher**: the tab's session is set from the watch status whenever the watcher's host or session
  changes (it is stored and synced like any session tab, and rewritten on the next change). After a failover (a26
  closed → mlab) the tab shows the new holder's session; the bar names the host.
- The holding host's daemon must say which tmux session the watcher lives in (A3, `tmux_session`), so the pane is a
  normal `tmux-session` pane on that host; the A line's identification of the watcher (e.g. a fixed tmux name, still
  open on its side) does not change this tab.

## 5. The 事件 tab

Journal v4's timeline style (the prototype's `EventsPage`): an `h2` 事件 with the count line, a guardian line, three
filter rows of chips (主機／種類／狀態), then the events newest first with day separators (今天／昨天／date).

- **Count line**: 「n 件待守門員處理」 (status-warning) or 「沒有待處理的事件」.
- **Guardian line**: 「守門員在 {host}・{醒著｜休息中，有新事件會自動醒來｜醒著，正在處理}・{mode}・{model}」, or when
  there is none 「沒有守門員：事件不會被處理。可以到「守門員」頁簽啟動。」
- **Row**: time; a kind tag (one hue per kind) and, for E1, its label tag; the host label; the status on the right;
  the command (mono) or for E2 「{host} 整機記憶體壓力過高」; a facts line (cwd・session・峰值 n 核心・n MB・持續 …);
  under a handled row the watcher's note with a filled shield.

  | Kind (wire) | Tag text | Short (filter) |
  |---|---|---|
  | `unmanaged_heavy` | 不在管制內的重指令 | 不在管制內 |
  | `pressure_high` | 記憶體壓力過高 | 記憶體壓力 |
  | `slot_orphaned` | 名額被孤兒行程佔住 | 孤兒佔名額 |
  | `slot_long` | 名額佔用過久 | 佔用過久 |

  E1 labels: `gate_gap` 漏網 (status-error), `unknown` 未知工具 (status-warning), `allowed` 允許的 (text-secondary).
- **Statuses**: pending 「待守門員處理」 (or 「待處理（沒有守門員）」 when there is none), handled 「守門員已處理」 + its
  note.
- **Nobody closes an event** (U3, U9): the area has no button that changes an event; the count goes down only when the
  watcher acknowledges.
- **Sources**: each daemon host's own record (mlab and a26); the App reads both and merges by time. E2 for a19 and
  mserv arrives later from the fleet primary (load-control spec §12.8) and needs no UI change: it is an event of the
  host that records it, labelled with the probe host.
- **Live**: the list and the badge update on the daemon's `watch.changed` event (A3); no polling while the App is
  idle. A daemon host that is offline shows a one-line note above the list (「a26 離線：它的事件暫時讀不到」).

## 6. The host tab

Header (as the prototype's `HostPage`): `HostBadge` 20 px, the alias as `h2`, the zone word in its colour
(正常／偏高／過載／離線), 「量測：{primary alias}（主）」 and, offline, 「・最後一次成功：{time}」; on the right the range
switch (狀態 only) and the 狀態｜設定 `SegmentControl`.

### 6.1 狀態

- **資源（{range}）** section:
  - Five gauges (netdata easy-pie: 56 px, 4 px stroke, track `--border-default`, round cap, from 12 o'clock counter-
    clockwise, `ringTransform('ccw')`), value in the middle: CPU 實際使用率 %, 記憶體使用率 %, 記憶體壓力 (正常／警告／
    嚴重 at 33／66／100 % of the arc), 換頁率 頁／秒 (full at 800), 磁碟空間 %. Tones: CPU and memory by `usageTone`
    (70／90), pressure by level, paging > 50 warn and > 800 error, disk ≥ 85 warn and ≥ 95 error. Offline: `—`,
    no arc.
  - One chart row per reading (label 8 rem · chart · 「平均 … · 峰值 …」 9.5 rem): the line is the average, a faint band
    the peak; only the stretches above the dashed warning line take the warning or error colour. A shared time axis
    under the last row (現在 … −n 分／小時) and 「累積中：已收集 n 分鐘」 while the App's history is shorter than the
    range. The data is F-2a1's in-memory history (`lib/fleet/history.ts`): 1 s points for 5 分, 10 s buckets
    {avg, max} for 1／6／24 時; a newly started App starts empty.
- **行程（每 5 秒更新）**: two tables side by side, CPU 前 5 名 and 記憶體前 5 名 (PID · 行程 · CPU % or MB), from the
  frame's `slow.top_cpu` / `top_mem`. Offline: 「離線，沒有資料」.
- **重指令排隊** (daemon hosts only; offline: 「這台離線，排隊與名額資訊暫停。」), a 當下｜歷史 `SegmentControl`:
  - **當下**: 「排隊 n · 最久 m:ss」 (warning when n > 0); two lanes, 重指令 (h-7) and 輕指令 (h-6): the lane's name and
    「名額 used / total」, one card per slot (running: accent tint, command, elapsed m:ss; free: dashed 「空名額」), the
    gate (a 1 px line with 「等名額」 when the slots are full, line and 「暫停放行」 in status-error while admission
    holds; no word when it holds nobody; a class paused at 0 slots (U10, W3c) shows 「已暫停」 on its lane and
    「暫停（新指令排隊）」 on its gate line in status-warning), then the waiting cards (place, command, wait; warning
    past 2 min). Under the
    lanes a table grouped 執行中 / 排隊中（n）: 類別 · 指令 · 專案 · 時間 (已跑／等) · 名額鎖 (pid). Hovering a card or a
    row highlights its pair. Elapsed times count locally each second from `since`; the data refreshes with the 5 s
    frame.
  - **歷史**: 「最近 3 小時的{重｜輕}指令（n 個）」 and a 重指令｜輕指令 switch; one row per command, newest first: 排入 ·
    類別 · 指令 · session · a stacked bar on one shared scale (striped = waited, solid = ran, red = failed; 「滿格 n 分」)
    with 「等 … · 跑 …」 · 平均核心數 · 記憶體峰值 · 換頁拖累 · 磁碟讀寫 · 結束碼. Hover: a card with the full command,
    project · session, 排入／開始／結束, 等候原因, and 套用參數與來源. The four new columns show 「—」 until the
    A line's records carry them (A1). Read on open and on every 30 s while shown (no push).

### 6.2 設定, daemon hosts (mlab, a26)

Built from `Section` and `SettingItem`; every value carries its source on the right (預設 quiet; 設定檔／設定組／自動
in text-secondary). Writes go to that host's daemon; a failed write restores the field and shows the error under it.
Offline: 「這台離線，無法讀寫設定。」 and nothing editable.

1. **設定組**: a select of the gate's profiles plus 無 (`pdx gate profile use`), A2.
2. **資源管制**: 啟用資源管制 (`gate.enabled`), 「關閉時，這台的重指令不排隊，直接執行。」 — off dims the next section.
3. **gate 設定** (A2): 重指令名額, 輕指令名額, 並行模式 (自動／固定／關閉), 保留核心數, vitest 最多 worker, 優先序
   (nice／背景／不調整), nice 值, 壓力門檻 (等級), 換頁率門檻 (頁／秒), 磁碟最低剩餘 (GB). Each change is one
   `key=value` write, like `pdx gate set` (applies to the next command).
4. **守門員** (the host's `watch` row, existing `GET /api/hostconfig` + `PUT /api/hostconfig/watch`, read-modify-write
   of the whole row): 啟用守門員, the seven detection fields (`x_cores`, `y_mb`, `t_s`, `pressure_level`,
   `pressure_t_s`, `page_rate`, `page_t_s`), 通知與開啟順序 (`targets`), 模式 (常駐／巡檢), 巡檢閒置分鐘, 自動喚醒,
   啟動指令 (`agent_command`, which replaced the model name on the A line, 10-16), 工作目錄, 啟動 prompt and 喚醒
   prompt (textareas; empty = built-in). No slot bounds (U10). When the two daemons' `targets` differ, a warning
   line under the field (the daemons warn as well, §12.3).

### 6.3 設定, probe hosts (a19, mserv)

A note 「沒有 daemon 的主機：由主方用 SSH 探查；沒有資源管制，也沒有守門員設定。」, then F-2a2's probe-host editor
(name, icon, colour, the SSH alias on each daemon host); saving writes both daemons as F-2a2 does.

## 7. Data sources

| Data | Source | Owner / state |
|---|---|---|
| readings, zone, offline, last ok | fleet frames from the primary (`fleet.changed`) | B line, F-2a1 |
| history (charts) | `lib/fleet/history.ts`, App memory | B line, F-2a1 |
| top processes | frame `slow.top_cpu` / `top_mem` | B line, F-1 |
| queue summary + waiters | frame `slow.queue` (`heavy_used` … `waiters`, `longest_wait_s`, `admission_held`) | B line, F-1 |
| **slot holders** (command, project, session, since, pid), waiter `session`, admission reason | `slow.queue.holders`, `slow.queue.waiters[].session`, `slow.queue.admission_reason` | **B line adds** (W5b): from `gate.Status.Heavy/Light` (already read over SSH and locally) and `Admission{Held,Reason}` (#2971) |
| queue history | `GET /api/gate/decisions` | **A line** (A1) |
| gate settings, profiles, `gate.enabled` | `GET/PUT /api/gate/config`, `POST /api/gate/profile` | **A line** (A2) |
| watcher where/state, events, unhandled, start, wake, `watch.changed`, `watch.events` | `/api/watch/*` | **A line** (A3; W2b/W3b) |
| `watch` settings | `GET /api/hostconfig`, `PUT /api/hostconfig/watch` | existing |
| probe-host settings | F-2a2 | B line |

## 8. What the A line is asked for

All admin-token routes on each daemon host; the App calls each daemon host it is connected to. Times RFC 3339.
The A line takes them after 743, in the order A1 → A3 → A2 (1f, 10-16). The writes (A2's `PUT`/`POST`, A3's start
and wake) are refused to device tokens (403) and are not opened to peers (`HostRoutePolicy`): only the Mac App,
which holds each host's admin token as it does for the other host-config writes, changes a host's load policy. A2's
writes use `pdx gate set`'s validation.

- **A1 `GET /api/gate/decisions?class=heavy|light&since=<time>&limit=<n>`** → `{records: [...]}` newest first, from
  `decisions.jsonl` (+ `.1`), each a `Decision` as written today (`time`, `argv`, `cwd`, `session`, `class`, `outcome`,
  `slot`, `wait_s`, `wait_reasons`, `applied`, `exit`, `duration_s`) **plus** the start time if `time` is the end, and
  the four rusage fields of the command tree: `cpu_s` (user + system; the UI shows `cpu_s / duration_s` as 平均核心數),
  `max_rss_mb` (largest single process, 記憶體峰值), `major_faults` (pages read back from swap, 換頁拖累), `disk_read_mb`
  and `disk_write_mb` (磁碟讀寫). Absent fields = an older record (UI shows 「—」). Records written before 743 carry
  `session: null` (#2959 deploys with 743): the session column shows 「—」 for them.
- **A2 gate config**: `GET /api/gate/config` → `{enabled, profile, profiles: [names], settings: [{key, value, source}]}`
  (the same list `pdx gate status` prints, sources `default|config|profile|auto`); `PUT /api/gate/config` with
  `{key, value}` (one value, written the way `pdx gate set` writes it; 400 with the reason for an unknown key or a bad
  value); `POST /api/gate/profile` with `{name}` (`""` = none). `gate.enabled` is one of the keys.
- **A3 watch**:
  - `GET /api/watch/status` names the watcher this host sends to — host id, alias, session id, `awake` or `resting`,
    the tmux session `pdx-watch` — or none (agreed in the load-control spec §12.6, #3007). The strip's mode and model
    come from the holder's `watch` row (`GET /api/hostconfig`), not from the status.
  - `GET /api/watch/events?since=<time>&limit=<n>` → `{events: [...]}` newest first: `id`, `kind`, `at`, the E1 fields
    (`label`, `command` scrubbed, `cwd`, `session`, `peak_cores`, `peak_mb`, `duration_s`, `new`), the E2 fields
    (`pressure`, `page_rate`, `detail`), the E3/E4 fields (`slot`, `holder_pid`, `lock_pids`), and `status`
    (`pending|handled`), `note`, `handled_at`.
  - `GET /api/watch/unhandled` (plan W2b) → `{count}` for this host.
  - Handled with a note: `pdx watch ack <id>… --note "<one line>"` (the watcher's command; the note is required).
  - No skip route (U9; #3017).
  - `POST /api/watch/start` → starts a watcher by §12.3 (202 with the chosen host, or 409 with the cooldown end).
  - `POST /api/watch/wake` with `{prompt}` → resumes the resting watcher with `wake_prompt` followed by the prompt.
  - `watch.changed` on the daemon's event stream when the watcher's fields above change, once per change (§12.6);
    and **`watch.events`** `{host_id, unhandled, cause: recorded|acked}` for the badge and the events list,
    once per operation (an ack of three ids sends one). Both plain `Broadcast` (agreed with the A line, #3010).
  - The A line's plan (10-16): `GET /api/watch/events` and `pdx watch ack <id>… --note` (required, one line ≤ 500
    bytes) in W2b; `POST /api/watch/start` follows
    §12.3 — 200 when one is already awake, else 202 `{host}` (the chosen host's alias and id) or 409
    `{cooldown_until}`; `POST /api/watch/wake {prompt}` resumes with `wake_prompt` + a space + the prompt (≤ 4 KB in
    all, no control characters, else 400), in W3b.
  - The A line's watcher identification (the fixed tmux session `pdx-watch`) comes as its spec §12.4／§12.6 and plan
    W3 update after 743; A3 is aligned with it then.

## 9. Phasing (one phase = one PR, ≤ 800 lines or ≤ 20 files)

The phases are planned in `docs/plans/2026-10-16-watcher-area-plan.md` §2. Splitting the first draft's W5a, W5b and
W5c (about 1700, 1100 and 1400 lines) gives these phases:

| Phase | Content | Depends |
|---|---|---|
| W5a1 | The area (reserved workspace, the three pane kinds, tab order sync, host tabs following the fleet), host cells' click target, the host tab's header and 行程 | F-2a1, F-2b |
| W5a2 | The host tab's 資源 (gauges, charts, range) | W5a1 |
| W5a3 | The watcher in the area: the status from `watch/status` (no count yet), the shield, the 守門員 tab (the stock `pdx-watch` session tab on the holder, or the status panel) | W5a1 |
| W5b | The queue wire: `queue.holders` / `waiters[].session` / `admission_reason` / the paused flags in `internal/fleet` + golden JSON + SPA types | #2971 |
| W5b1 | Queue 當下: the lanes and the table | W5a1, W5b |
| W5b2 | The queued symbol on the agent icon (§2.3) | W5b |
| W5c1 | 狀態｜設定; the 守門員 section (existing API), probe-host settings (F-2a2's editor) | W5a3 |
| W5c2 | The gate sections | W5c1; A2 |
| W5d | Queue 歷史 | W5b1; A1 |
| W5e | The 事件 tab, the count, the two pushes, 啟動守門員, the wake input | W5a3; A3 |

W5a1 … W5a3, W5b, W5b1, W5b2 and W5c1 need nothing from the A line.

## 10. Open points

- Q1 (user): 全部略過 — **decided 10-16: dropped** (U9, #3017).
- Q2 (A line): how the watcher's session is identified — **decided**: the fixed tmux session `pdx-watch` (load-control
  spec §12.4). The UI uses `tmux_session` from the status when it carries it, `pdx-watch` until then.
- Q3 (user): **decided 10-16** — the 守門員 tab is a full session tab with terminal, Deck and Chat (§4).
- Q5 (user, 10-16): the watcher is started and woken like a session tab's rebuild, never by typing in Deck. **The
  watcher's launch panel** stands where a session tab shows its launch or rebuild panel: the host it runs on (mlab or
  a26), the tmux session `pdx-watch`, the working directory (`workdir`), the command (`agent_command`), and for a resting
  watcher the conversation it resumes; one button, 啟動守門員 or 叫醒守門員, which asks the daemon (it picks the host and
  keeps two hosts from starting two), then the tab shows the session. Proposed default: the button, not a run on
  opening, so that looking at a resting watcher does not wake it (to be confirmed by the user).

Out of scope: iOS; alerts or pushes on zone changes (the watcher's job); any history kept on a daemon for the charts;
`pdx gate free` from the UI (the watcher's command after the person agrees).
