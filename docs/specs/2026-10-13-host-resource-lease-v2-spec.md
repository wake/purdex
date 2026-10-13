# Host resource lease v2: zones, trend, parking, anomaly alerts (spec)

Date: 2026-10-13. Coordinator: purdex-1f (`mlab/purdex-1f-vq`). Line: A. Issue: #2759 (decisions in its 2026-10-13 comment).
Status: **user decisions final (§1)**; plan: `docs/plans/2026-10-13-host-resource-lease-v2-plan.md`.
Builds on `docs/specs/2026-10-08-host-resource-lease-spec.md` (v1). This spec **replaces** v1 R5, R6, R9 and D-2, D-3; **extends** D-4, D-8 and §5. Everything else in v1 stands (R1, R3, R7, R8, R10; D-1 measurement, D-5 fail open, D-6 scope, D-7 kinds).
Code facts: the code inventory (main @ 10116256b, file:line per claim) lives outside the repo, in the coordinator's notes (`lease-v2-inventory.md`); section numbers below cite it as "INV §n" and the claims were true at that commit.

## 1. User decisions (2026-10-13, do not reopen)

| # | Decision |
|---|---|
| V1 | 准入看「三區 × 是否有租約在跑 × 用量方向」。三區＝一般／警戒／危險。 |
| V2 | 准入表：一般＋沒有租約在跑＝放行；一般＋有＋持平或下降＝放行；一般＋有＋上升＝觀察；警戒＋沒有＝放行；警戒＋有＋下降＝放行；警戒＋有＋持平或上升＝觀察；**危險一律觀察**（含沒有租約在跑）。觀察＝繼續排隊、每次取樣重判。 |
| V3 | 放行後隔一段時間才看下一個，**從放行時間起算**（不是申請時間），避免前一個的用量還沒起來就放下一個。 |
| V4 | **拿掉逾時放行**（v1 R6）。一次呼叫等不到時，先回給 agent「排隊中，第 N 位，稍後重跑」，**保留排隊位置**。 |
| V5 | 監控與異常檢查由 **purdex daemon 統一做**（每台一個）；session 透過 pdx 查。booter 只負責看住 daemon。 |
| V6 | **沒人排隊時也要做異常檢查**（主機在危險區太久、租約跑太久或記憶體暴增、持有者被收養）。 |
| V7 | 判斷用**固定規則**；判斷不了就**直接警報給使用者**。不叫 AI。 |
| V8 | **只告警、不殺**。通知持有者的 session 與它的 lead；警報走推播／App。 |
| V9 | **保留近期紀錄**，用來觀察有沒有過度卡死。 |
| V10 | v2 上線前**不**先把現行規則切成 lease 模式（兩台維持 advise）。 |

## 2. Why

2026-10-13 05:51 mlab ran out of memory and the WindowServer watchdog panicked (ops session diagnosis: `~/.claude/projects/-Users-wake-Workspace-wake-mlab/memory/mlab-2026-10-13-watchdog-panic.md`). Three `pdx lease run --kind build -- pnpm exec vitest run <one file>` runs (05:00, 05:12, 05:13) hung at 3.3–4.0 GB each and held their leases 40–50 min. Both hosts were still in mode `advise` since 2026-10-09 (the planned "advise one day, then lease" switch never happened), so every request was granted at once; `would_wait=1` on all of them shows v1 lease mode would have queued them, but only for 5 min (R6) before an overrun grant. Nothing alerted. Two of the runs were moved to the background by Claude Code's Bash timeout; the third was a `( … &)` subshell adopted by launchd.

## 3. Facts the design rests on

- **Readings available every 5 s** (`SampleInterval`, INV §1): `Mem` % (vm_stat used = total − free − inactive − speculative), `Pressure` (1 normal, 2 warn, 4 critical), `Load1`, `NCPU`; per-process `PID, PPID, Pcpu, RSSBytes` from one `ps` fork. Only the latest snapshot is kept; **no history** (`module.go:50`). `host_minutes` keeps per-minute maxima only, **no pressure column** (INV §1).
- **mlab distribution since 2026-10-09** (6,426 minutes, minute maxima): mem < 80 % 93.6 %, 80–90 % 6.2 %, ≥ 90 % 0.2 %; load1 ≥ ncpu 16.6 %; latched `full` 23.8 %; a held lease with weight ≥ 30 in 36.9 % of minutes.
- **Admission runs** on every good sampler tick, every 1 s sweeper tick while something waits, inline in POST, after every release/cancel and after a disk trim, under `stateMu` (INV §2). `Admit` is pure (`admit.go:99-136`); FIFO is **not** strict today (`admit.go:92`).
- **Waiting is a long poll** that renews `lease_until = now+30 s` per poll; a row nobody polls for 30 s is `abandoned` (INV §3). POST is idempotent by `client_id` (replay returns the same row). The poll answer already has `position` (`lease_handler.go:404-413`); `pdx lease acquire`'s JSON line does not, and acquire exits 0 on every outcome (INV §3).
- **The mod** (`lease.js:471-506`) always calls `next(e)` after acquire and releases by client id in `finally`. A `tool.call` hook can answer `{deny: <text>}` without running the tool (`ask.js:267`).
- **Holders**: the mod's leases are scope `session-new` with the **agent pid** as holder, so `holder_gone` fires only when the agent dies; `pdx lease run` is scope `process` with the `pdx` pid as holder (INV §4, §5). `Proc.PPID` is sampled; `ProcessSnapshot.PPID/Start` is fork-free (INV §4).
- **Unverified**: what `next(e)` does when Claude Code moves a *foreground* Bash call to the background after its timeout (INV §3 "Background-move"). v1 M-R2 measured only explicit `run_in_background:true`.
- **The resources module reaches only the registry (peers origin resolver as `RootSource`, hostconfig settings) and WS events**; no push, no peers sender, no team lookup (INV §9). **No SPA or iOS code consumes `resources.changed`**; there is no lease UI (INV §8).

## 4. Model

### D-1′ Zones

A pure function of one reading, with hysteresis:

| zone | enter when any of | leave downward when all of |
|---|---|---|
| danger | `Pressure ≥ 4`, `Mem ≥ mem_danger_pct` (90) | `Pressure < 4`, `Mem < mem_danger_pct − 2` |
| warning | `Pressure ≥ 2`, `Mem ≥ mem_warn_pct` (80), `Load1 ≥ NCPU` | `Pressure < 2`, `Mem < mem_warn_pct − 2`, `Load1 < 0.9 × NCPU` |
| normal | otherwise | — |

- The zone is the worst one entered. Load never puts the host in danger (CPU saturation slows, memory exhaustion crashes).
- A reading that is unavailable (sampler failing, mode off, `NCPU ≤ 0`) gives zone **unknown**. Mode measure computes zones (status and alerts use them) but runs no admission.
- One latch per module, updated by the sampler goroutine like `FullLatch` (`full.go`). `Full` stays as is, for the record and the report only; admission no longer reads it.

### D-2′ Trend

- An in-memory ring of the last 10 min of good samples: `{at, mem, cpu = 100·Load1/NCPU, pressure}`. It is not persisted; it starts empty at boot.
- Over the last `trend_window_s` (60 s), take the least-squares slope of `mem` and of `cpu`, in points per minute.
- **rising** if the pressure level at the end is higher than at the window start, or the mem slope > `trend_mem_ppm`, or the cpu slope > `trend_cpu_ppm`.
- Otherwise **falling** if the pressure level went down, or the mem slope < −`trend_mem_ppm`, or the cpu slope < −`trend_cpu_ppm`.
- Otherwise **flat**.
- Fewer than 6 samples, or a span shorter than half the window, gives **unknown**; admission treats unknown as rising.
- Initial thresholds: `trend_mem_ppm` 1.0, `trend_cpu_ppm` 5.0. They are tuned at gate G1 (§6).

### D-3′ Admission (replaces v1 D-2, D-3, R5, R6, R9)

- **A lease is running** when any row is `held` (any kind). Weights and the capacity sum no longer decide anything; `Charge`, `sum_charge` and `would_wait_r2` stay as records.
- **Strict FIFO, head only.** A pass considers only the oldest present waiter (`created_at`). Parked rows (D-4′) keep their place but are skipped while parked; the next present waiter can be granted. The existing cuts stay: a session-new waiter without a baseline, or a disk-guarded kind during a trim, stops the walk (`admission_pass.go:89-105`).
- **Gap (V3).** While the newest `granted_at` of **any** row (held or already ended) is less than `gap_s` (30 s) old, nothing is granted. Every grant starts a gap, including one whose command ended quickly; it is read from the table, so it survives a restart. After the gap, the trend window sees the new command ramp up, and the table keeps the queue waiting while it rises.
- **Table (V2)** for the head, after the gap:

  | zone | no lease running | running, falling | running, flat | running, rising or unknown |
  |---|---|---|---|---|
  | normal | grant | grant | grant | observe |
  | warning | grant | grant | observe | observe |
  | danger | observe | observe | observe | observe |
  | unknown | grant (fail open, D-5) | grant | grant | grant |

- At most one grant per pass (a grant starts a gap).
- **Mode advise** still grants everything at once. The counterfactual runs as an in-memory **shadow queue**:
  - every request joins it at creation;
  - each pass runs `AdmitV2` over it, with the real zone and trend, the real held rows as "running", and a gap counted from the shadow's own grants;
  - a shadow grant records `v2_wait_ms` on the row;
  - a row that ends before its shadow grant records the elapsed time with `v2_wait_open = 1` (a lower bound).
  - `would_wait` = the shadow did not grant it at once; its first blocking reason is recorded.
  - Known bias: in advise the real commands are already running, so the shadow sees "running" earlier than lease mode would. Its waits are an upper estimate.
  - The shadow is lost on restart; rows pending in it at boot record nothing.
- **Mode lease** is real waiting with **no overrun**: `Admit` no longer grants at a deadline. Paths become `immediate`, `waited` and `unknown_zone`; `overrun` is never written again (old rows keep it).

### D-4′ Waiting, parking, re-attach (replaces v1 R6; extends D-4)

- `deadline_s` changes meaning: it becomes the **hold**, the longest one client call waits (default **540**, range 10..590; `MaxWaitS` 590 stays under the mod's 600 s cap). `wait_s` in a request still overrides it.
- **Parking happens inside the admission pass**, under `stateMu`, before `AdmitV2` runs (so a row past its deadline can never be granted by a pass that ran before the sweeper):
  - every waiting row with `deadline_at <= now` and `parked_until = 0` is parked by one CAS (`WHERE id=? AND state='waiting' AND parked_until=0 AND deadline_at=?`): `parked_until = now + park_s` (900), `lease_until = parked_until`, `parks = parks + 1`;
  - the pending long poll answers `state:"queued"` with `position` and `parked_until`;
  - a parked row is never granted;
  - a poll of a parked row does **not** renew `lease_until`; the sweeper abandons it when `parked_until` passes (end reason `abandoned`).
- **Polls name their client.** `GET …/leases/{id}` carries `client_id`. If the row's `client_id` is different (it was re-attached by a newer call), the answer is `state:"replaced"`; nothing is renewed. A replaced client behaves like a queued one: it does not run its command.
- **Session for scope process.** Resolved first, before any lookup: for a `pdx lease run` request without `session_id`, the daemon walks the holder's ancestors (fork-free `PPID` and start time, ≤ 16 hops) to a registered agent pid. Not found means no session, so no re-attach.
- **Re-attach (parked rows only).** A POST that is not a replay of its own `client_id` looks for the oldest **parked** row with the same `session_id`, `kind` and `weight`. Waiting rows that are not parked are never adopted: two parallel calls of the same session are both real requests.
  - Adopting is one CAS on the old client id (`WHERE id=? AND state='waiting' AND client_id=? AND parked_until>0`). It replaces `client_id`, `holder_pid`, `holder_start`, `tool_use_id`, `scope`; clears `parked_until`; sets `deadline_at = now + hold` and `lease_until = now + 30 s`.
  - `created_at` is kept, so the position is kept.
  - A lost CAS tries the next parked row, then inserts a new one.
  - The answer is that row.
- **Clients.**
  - `pdx lease acquire`: its JSON line adds `state`, `position` and `parked_until`. A `queued` or `replaced` answer exits **15** (new `ExitQueued`).
  - `pdx lease run`: on `queued` it prints the line below to stderr and exits **15** without running the command.
  - The mod: on `queued` it returns `{deny: <text>}` and does **not** call `next(e)`.
  - None of these send a release for a queued row, because a release by client id would cancel the parked row and lose its position.
  - Text, for the mod's deny and `lease run`'s stderr (Chinese, wording final in the plan):
    「主機資源忙，這個指令還沒執行：排隊第 N 位，已等 M 分鐘。位置會保留 P 分鐘；先做不需要這個指令的事，之後用**同一個指令**重跑就會接回原位。不要改成背景執行或加 `&` 繞過排隊。」
- The `pdx-lease` skill text is updated to match: queued is normal, re-run later; never background or wrap to bypass.

### D-5 Fail open (unchanged)

A daemon that is down or does not answer means the command runs. Zone unknown means grant (table).

### D-6 Anomaly checks (V5–V8)

- **Where they run.** A check step in the sweeper, once per 5 s, independent of the queue. They only alert; they never end, kill or signal anything (V8).

| rule | condition | sent to |
|---|---|---|
| A1 danger too long | zone danger continuously ≥ `alert_danger_s` (180) | user |
| A2 held too long | a held row's age ≥ the kind's hold limit (below) | holder (see delivery) |
| A3 memory spike | a held row's measured RSS ≥ `alert_lease_mem_pct` (20) % of host memory | holder (see delivery) |
| A4 holder adopted | scope process and the holder's PPID is 1 | holder (see delivery) |
| A5 queue stuck | the head has waited ≥ `alert_stuck_s` (600), parked time included | diagnosis below |

- **A2 hold limit.** The larger of 15 min and 3 × the p90 hold time (`ended_at − granted_at`) of that kind over 14 days, over every row that was granted and has ended (any end reason), when there are ≥ 10 of them; otherwise 30 min.
- **A5 diagnosis** (fixed rules, V7):
  - (b) some held row trips A2, A3 or A4 → those alerts are what to act on (sent if not already); A5 records "b".
  - (a) else, the zone is warning or danger, or the trend is rising, and every held row is within its limits → the queue is legitimately busy; record "a", no message.
  - (c) else (nothing explains the wait: zone normal and no lease running, sampler unavailable, a waiter cut for ≥ 10 min, or a wait ≥ 30 min under any diagnosis) → user alert.
- **A5 sending.** Diagnoses a and b are records only: they never send and never touch any send state. Diagnosis c sends to the user at once, even if the same open A5 was a or b before; then at most every 15 min while it stays c.
- **De-duplication.**
  - An alert is keyed by (rule, subject), where subject = the lease id, or `host` for A1 and A5. It opens once and closes when the condition clears; the close is recorded and not sent.
  - Send state is **per recipient** (holder, lead, user): each has its own last success time and attempt count. A recipient is re-sent at most every 15 min while the alert stays open. A failed send is retried on the next check (every 5 s, backing off to 1 min); `ResultDeliveryUncertain` counts as sent.
  - The text says what is wrong, with figures, and that nothing is ended or killed automatically.
- **Delivery.**
  - **Holder that is a team member:** the notice goes from its lead's inbox to the member, and a copy goes from the member's inbox to the lead (one prefix line naming the member). This is the path team notices already use (`team/notify.go:80-145`, `team/notice_usage.go:164-185`); the address comes from `OriginResolver.ResolveOriginBySession`.
  - **Holder that is a solo session or a lead:** peers refuses a send from a session's own inbox to itself (`peers/send.go:515`), and there is no daemon sender identity today. So it becomes a **user alert** naming the session; `pdx lease ls` shows it as well. A daemon sender identity is a follow-up issue.
  - **No holder session** (A4 with no agent found, A1, A5 c): user alert.
  - **User alert:** always the WS event `resources.alert` and the record; also a push with a new kind `resources` when `alert_push = 1`. There is no presence suppression (unlike pause).
  - The team seam (`LeadOf`) and the push notifier are looked up from the registry **at send time**, every time, like the pause outbox (`team/pause_outbox.go:141`). A missing one leaves that recipient unsent, to be retried; it never counts as sent.
  - **Mac and iOS display of `resources.alert` and push kind `resources` is the interface line's** (88). An iOS app that does not know a kind shows "這個版本不認得" (relay review G-1), so the push is gated by the resources setting `alert_push` (0/1, default 0). 88 turns it on once the iOS app ships the kind.

### D-7 Records (V9, extends v1 D-8)

- **Lease rows**, new columns (ALTER migration; resources.db is live):
  - `dec_zone`, `dec_trend`, `dec_running` (bool), `dec_gap_ms` (remaining gap at the decision);
  - `parks` (count), `parked_until`;
  - `v2_wait_ms`, `v2_wait_open` (advise shadow, D-3′);
  - `wait_reasons`: a JSON map of ms by reason (`gap`, `danger`, `warning_flat`, `rising`, `behind`, `cut`, `parked`).
- **How `wait_reasons` is counted.**
  - The module keeps, per waiting row, the reason the last pass gave it and when (`reason`, `since`). Each pass first adds `now − since` to the **previous** reason, then stores the new reason with `since = now`. The time between two passes belongs to the reason that was in force during it.
  - On grant and on every end of a waiting row (cancel, abandon, a lost CAS that ends it), the open interval is flushed first, then the JSON is written in the same UPDATE, and the entry is dropped.
  - It is lost on restart.
  - The state has its own mutex. Lock order: `stateMu` → reasons mutex, never the reverse. GET and the event goroutine read a copy under the reasons mutex only.
  - The current reason and `since` are also what status shows and what A5 reads (e.g. "cut for ≥ 10 min").
- **host_minutes**, new columns: `sampled_s` (good-tick seconds), `normal_s`, `warning_s`, `danger_s`, `pressure_max`, `zone_max` (0 normal, 1 warning, 2 danger), `waiting_max`. Zone shares are `x_s / sampled_s`; legacy rows (`sampled_s = 0`) are left out of them.
- **`resource_alerts`** table: `id, rule, subject, lease_id, session_id, opened_at, closed_at (0 while open), diagnosis, detail_json, sends_json` (per recipient: `{last_ok_at, attempts, last_error}`); partial unique index on `(rule, subject) WHERE closed_at = 0`. Retention 14 days, like the other tables.
- **Report** (`pdx lease report`, `GET /api/resources/report`) adds:
  - share of time per zone;
  - wait p50/p90/max overall and by dominant reason;
  - parks;
  - alerts by rule and diagnosis;
  - the 5 longest waits, with their reasons and the zone/trend at grant.

  This is the over-blocking review of V9.
- The ring (D-2′) is exposed read-only through `GET /api/resources?samples=1`, for debugging and for G1.

### D-8 Surfaces

- `GET /api/resources` and the `resources.changed` event add `host.zone`, `host.trend`, `gap_until`, each waiter's `position`, `parked_until`, current blocking reason and its `since`, and the open alerts (`alerts: [...]`, the App contract shape).
- **`pdx lease ls` is the status command** (V5 "session 透過 pdx 查"): it shows zone, trend, gap, the queue with positions and reasons, and open alerts. No new top-level command.
- `pdx team`'s CPU/MEM columns are unchanged.

### Settings (hostconfig `resources`; all ints, validated and clamped like v1)

| field | default | range | note |
|---|---|---|---|
| `mem_warn_pct` | 80 | 50..95 | |
| `mem_danger_pct` | 90 | warn+1..99 | |
| `gap_s` | 30 | 0..300 | |
| `trend_window_s` | 60 | 20..300 | |
| `trend_mem_dppm` | 10 | 1..100 | tenths of a point per minute (1.0) |
| `trend_cpu_dppm` | 50 | 1..500 | (5.0) |
| `deadline_s` | **540** (from P2a; 300 until then) | 10..590 | meaning changed: hold per call |
| `park_s` | 900 | 60..3600 | |
| `alert_danger_s` | 180 | 30..3600 | |
| `alert_stuck_s` | 600 | 60..7200 | |
| `alert_lease_mem_pct` | 20 | 5..90 | |
| `alert_push` | 0 | 0..1 | user alerts also go out as push |

`warmup_s`, `floor_pct`, `ewma_half_life_s`, `heavy_min_weight`, `max_hold_s`, `kinds` stay. `max_hold_s` still ends a lease as `expired`, which is bookkeeping only (nothing is killed).

## 5. Phases (one PR each, ≤ 800 lines / 20 files)

| phase | content | ships with |
|---|---|---|
| P1a | pure library: zones + latch, ring + trend, `AdmitV2` (table, gap, head-only, no overrun), new settings fields with validation | nothing user-visible |
| P1b | module wiring: ring in the sampler, zone latch, pass uses `AdmitV2` in lease mode, decision columns and the reasons state, host_minutes columns | |
| P1c | advise shadow queue, report additions, GET/WS fields, `pdx lease ls`, G1 | deploy with mode **advise** (V10): v2 counterfactual recorded |
| P2a | daemon: session attribution, parking inside the pass, polls name their client, re-attach of parked rows, `deadline_s` default 540 | |
| P2b | clients: acquire `state`/`position`/exit 15, `lease run` exit 15, mod `{deny}` and no release when queued, skill text; probe M-P1 | after G2, switch both hosts to **lease** |
| P3a | anomaly rules A1–A5, `resource_alerts`, report additions | |
| P3b | delivery: holder/lead notices (team seam), push kind `resources` behind `alert_push`, WS `resources.alert` | Mac/iOS display by 88 |

## 6. Gates and measurements

- **G1 (in P1c, before merge):** run the new trend code over ≥ 30 min of real 5 s samples during ordinary multi-session work. Report what share of samples each direction gets with nothing heavy running; if "flat" is under 60 % at rest, raise the thresholds. Record the numbers in the PR.
- **G2 (after the P1c deploy, ≥ 24 h of advise):** `pdx lease report` over the advise period. How many grants v2 would have held, for how long, and for which reasons; the 5 longest. The lead decides any threshold change, then switches to lease after P2b, and tells the user.
- **M-P1 (in P2b):** in a throwaway real session, a heavy-classified foreground Bash (`sleep 150`-class command forced heavy by a test kind) with a short Bash timeout. Record whether `next(e)` resolves when Claude Code moves the call to the background, and so whether the mod releases the lease while the command still runs. If it does, P3a adds rule A6 "lease released while its tree still runs" from the mod's own report (out of scope until measured).

## 7. Coordination

- **88 (interface line):** Mac/iOS display of `resources.alert` and push kind `resources`; turning on `alert_push` once iOS knows the kind. Contract in the plan's "App contract" section.
- **mlab ops session (`mlab/_yqgyb9`):** informed via #2759; nothing for booter.
- **#2760** (tmux processes left with ppid=1 after daemon restarts) stays separate: its cause is unverified, and it is checked live at the next restart.

## 8. Out of scope

Ending, killing or signalling any process (V8); any AI judgement (V7); booter changes (V5); a UI in this line (88 owns it); persistence of the 5 s ring; per-session quotas.
