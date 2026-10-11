# Idle relay: relay before the prompt cache expires (spec + plan)

Status: rev 2, D6 decided 2026-10-12; IR-1b shipped in alpha.713 (#2700) (rev 1 → codex plan review, 17 findings, dispositions in §11). Owner: A line (1f).
Builds on `2026-10-06-lead-team-relay-spec.md` (§8.1–§8.7 self relay, U7 hold, U9, §8.5 notices),
`2026-10-10-member-relay-ask-spec-plan.md` and `2026-10-11-push-workers-relay-spec.md` (self_relay push).

## 0. Background and decisions

**What happened (measured 2026-10-11).** Claude Code 2.1.296 has *idle compaction*: when the main conversation is idle,
its 1-hour prompt cache is still warm and the context is ≥ 200k tokens (env `CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS`,
floor 100k), it compacts at 0.9 of the cache TTL — **about 54 minutes after the last request** — and appends the
notice "Compacted while idle, before the prompt cache expired" (`compact_boundary.trigger = "auto"`). The fraction is a
server flag (clamped 0.5–0.95); it cannot be shortened locally. The lead `purdex-1f` was idle-compacted twice that day,
at 51% and 64.5% of a 1M window: below the 70% self-relay threshold, so no relay was asked, no card, no push
(`relay_ops` empty for the session).

**Why relay then.** Re-reading a warm cache costs about 1/20 of re-writing a cold one (cache read 0.1× vs 1-hour cache
write 2×). The cheapest moment to condense a large context is while it is warm; a relay then costs about what CC's idle
compaction costs and keeps Purdex's flow (handoff with the team roster, lineage, the card and push the person sees).

User decisions (2026-10-11):

- **D1.** An ordinary session idle for **40 minutes** with context **≥ 40%** asks for a relay — the same self-relay
  request as the 70% ask (card in the Mac App, push to the phone when away, approvable on iOS).
- **D2.** Not approved in time → CC's idle compaction at ~54 minutes runs as the fallback; an open request is cancelled
  `cancelled{compacted}` (existing §8.7 (c)). `idleCompaction` stays **on**.
- **D3.** A member does not ask the person: the **daemon tells its lead** ("member X idle 40 min, 55%, cache expiring"),
  and the lead decides with the existing `pdx relay _<ref>` (U9: the daemon only reports). This is the existing 70% idle
  notice (P7-1) with an idle-time trigger; no ask row, no new member wire.
- **D4.** The mod tells an idle compaction from a context-full one and reports `idle`; the lead's notice for a member
  then says so (today: "lead 未在 70% 時接力", wrong for an idle compaction at 40%).
- **D5.** Not done: vetoing CC's idle compaction for chosen sessions.
- **D6 (user decision 2026-10-12: option A — overrides U7 for idle asks only).** An idle ask **does not hold prompts**: a prompt
  that arrives while it is open (the person typing, or a peer message) **withdraws** it (`cancelled{resumed}`) and goes on
  unchanged — the session is no longer idle and that turn re-warms the cache. The 70% ask keeps U7's hold.
  (Option B — idle asks hold like U7, up to the 10-minute deadline — was declined.)

1f decisions (derived):

- **D7.** One ask per idle period, sharing the threshold ask's guards: `lastAskPct + REASK_POINTS` (one value for both
  kinds) and the `floor + minGrowth` loop guard (spec §8.1 / register.js `maybeBegin`). A withdrawn idle ask does not
  set `lastAskPct`. A compaction or a relay resets both, as today.
- **D8.** Skip when the session has a running background task or a session cron (the last `classic.Stop`'s
  `background_tasks` with status `running`, or `session_crons > 0`): a /clear would orphan them.
- **D9.** The daemon owns the numbers: `hello` answers `idle_minutes` (40) and `idle_threshold` (40). Absent or 0 ⇒ the
  feature is off for that session **whatever the env says** (an older daemon must never see the new flags). When hello
  advertises it, env `PDX_RELAY_IDLE_MINUTES` (1–50) / `PDX_RELAY_IDLE_THRESHOLD` (1–100) override the values
  (acceptance), like `PDX_RELAY_THRESHOLD`.
- **D10.** Timing: ask at 40 min; the self-relay deadline is 10 min (`SelfRelayDeadlineS`), so an unanswered ask closes
  at ~50 min, before CC's ~54-min compaction.
- **D11.** Unattended mode approves an idle self relay like any self relay (consumes the relay quota). No change.
- **D12.** Remote members (member host ≠ lead host) are out of scope, like the 70% notice today (P7-1: "Members of
  another host are the X series'"); follow-up issue.

## 1. Flow (ordinary session)

```
main turn.complete, state idle ──▶ arm idle timer (idle_minutes); re-arm replaces
   main turn.start / prompt.submit / session.start / /clear / compaction / state ≠ idle ──▶ disarm
timer fires ──▶ re-check gates (§4.2) ──▶ pdx relay begin --self --reason idle --idle-minutes 40
beginning: a prompt waits for begin's answer (existing ≤ BEGIN_HOLD_MS); begin opened an idle ask ⇒ withdraw (below)
awaiting (idle ask):
   approved ──▶ existing path (write, /clear, seed)
   prompt.submit ──▶ withdraw: toIdle, prompt goes on unchanged, report cancelled{resumed}      (D6)
   denied / timeout ──▶ idle, lastAskPct = pct                                                  (D7)
   CC idle compaction ──▶ existing: cancelled{compacted}, compaction runs                       (D2)
```

Member: §3.3 (daemon only). The member's mod changes only for D4 (§4.5).

## 2. Wire (`internal/team`)

- `RelayHelloResponse` + `IdleMinutes int \`json:"idle_minutes,omitempty"\``, `IdleThreshold int \`json:"idle_threshold,omitempty"\``;
  constants `RelayIdleMinutes = 40`, `RelayIdleThresholdPct = 40`.
- `RelayBeginRequest` + `Reason string \`json:"reason,omitempty"\`` (`""` today's ask, `"idle"`) and
  `IdleMinutes int \`json:"idle_minutes,omitempty"\`` (1–59 iff reason is idle, else 0). Unknown reason → 400
  `bad_request`; idle with `manual` (a /relay) → 400. The begin replay compares the full payload (it does today), now
  including both fields.
- `SelfRelayPayload` + `Reason`, `IdleMinutes` (the row's, like `Manual`).
- `RelayReasonResumed = "resumed"` (cancelled). Accepted from the mod only for an op whose payload reason is idle (any
  other op → 400 `bad_transition`). Its race with an approve is **exactly `compacted`'s** (`relay_report.go` terminal
  report: row CAS shared with approve; the loser reconciles) — whatever `compacted` yields today, `resumed` yields.
- `RelayCompactedRequest.Trigger` accepts `"idle"` besides `"auto"` / `"manual"`.

## 3. Daemon

### 3.1 Begin and the card
`handleRelayBegin` validates and stores `reason`/`idle_minutes`. The gate is unchanged: self-relay off / paused,
`relay_open`, a member → `member_relay_is_leads`, unattended immediate approval.

### 3.2 Push (`internal/push/content.go`)
New keys chosen when the payload reason is idle:
- `self_relay_idle`: zh-TW `"%s：%s 閒置 %d 分鐘，申請接力（已用 %d%%）"`; en `"%s: %s has been idle %d min and requests a relay (%d%% used)"`.
- `self_relay_idle_by`: zh-TW `"趁快取還在時接力最省；不核准的話，Claude Code 稍後會自行壓縮"`; en `"Relaying while the cache is warm is cheapest; otherwise Claude Code compacts it later"`.

### 3.3 Member idle notice (extends P7-1, `notice_usage.go`)
On the liveness tick (every 10 s, after `noticeUsage`), for each active local member of a live team:
- track `idleSince[sessionID]` in memory: set when `AgentStatus == idle` is first seen, cleared on any other status
  (a restart forgets it — conservative: the clock starts again);
- tell the lead **once per idle period** when idle for ≥ `RelayIdleMinutes`, `UsedPercentage ≥ RelayIdleThresholdPct`,
  no relay op open, no member ask open; mark the period told (in memory, cleared with `idleSince`);
- the send re-checks idle / relay / ask right before sending (as `usageNotice` does) and does not retry on failure
  (advisory; the lead still sees CTX in `pdx team`);
- independent of the 70% notice's armed flag; it does **not** skip members whose mod asks for itself (`asksForItself`):
  a member's own ask fires only at ≥ 70%.

`UsageIdleNoticeFmt = "[pdx team] member %s [%s]「%s」閒置 %d 分鐘、已用 %d%%，快取快過期。要趁現在接力請執行：pdx relay _%s（不接力的話，Claude Code 稍後會自行壓縮）"`.
Env `PDX_RELAY_IDLE_MINUTES` / `PDX_RELAY_IDLE_THRESHOLD` in the daemon's environment override (acceptance), like
`PDX_RELAY_THRESHOLD` for the 70% notice.

### 3.4 Compacted
`handleRelayCompacted` accepts `idle`. An active local member's `idle` tells the lead
`CompactedIdleNoticeFmt = "[pdx team] %s 閒置太久，被 Claude Code 自動壓縮了（快取過期前的閒置壓縮，不是 context 滿）"`;
`auto` keeps today's text; `manual` tells nobody. `idle` disarms the usage notice and withdraws an open ask exactly as
`auto` does. Remote members unchanged (D12).

## 4. Mod (`cmd/pdx/plugin/purdex/hooks/register.js`; `events.js`)

### 4.1 State
`s.idleMinutes`, `s.idleThreshold` (hello, then env per D9), `s.idleTimer` ({cancel, token}), `s.idleAsk`
(true from the timer's begin until the request ends).

### 4.2 Arm, fire, gates
- **Arm** at the main `turn.complete` (never a subagent's) after the existing handling, when `s.state === 'idle'`,
  `s.role !== 'member'` and `s.idleMinutes > 0`: `$.clock.after(idleMinutes × 60 000)` carrying `s.gen` and a fresh
  token; re-arming replaces the timer.
- **Disarm** on main `turn.start`, `prompt.submit`, `session.start`, `classic.SessionStart` (clear), `session.compact`,
  and every return to a non-idle state.
- **On fire** (from the timer; never inside a hook), ask only if all hold — and re-check the cheap ones again after
  every `await` (`session.usage()`, `session.id()`), as `maybeBegin` does: token and `s.gen` current; `s.interactive`;
  `s.helloOK`; `s.role !== 'member'`; `s.state === 'idle'`; `!s.turnRunning`; `s.control` empty; percent `≥
  s.idleThreshold`; `tokens ≥ s.floor + s.minGrowth` when `s.floor` is set; `pct ≥ s.lastAskPct + REASK_POINTS` when
  set; `!backgroundBusy()`.
- Then the existing `begin()` with `--reason idle --idle-minutes N`; `s.idleAsk = true`, `s.lastAskPct` is set only
  when the request ends denied / timed out (not at begin, unlike the threshold path — so a withdrawal costs nothing).

### 4.3 No hold, withdraw (D6)
`prompt.submit` with `s.idleAsk`:
- `beginning`: wait for begin's answer (existing `BEGIN_HOLD_MS`); nothing opened ⇒ go on; an op opened ⇒ withdraw.
- `awaiting`: withdraw.
- Withdraw = `toIdle($)` first (the prompt goes on at once, unchanged), `$.ui.status(undefined)`, then
  `report(op, 'cancelled', ['--error', 'resumed'])`. The wait loop's late answer is ignored (`letGo`, as for compaction).
- `approved` or later: today's behaviour (NOTE appended, relay proceeds).
The threshold ask's hold is unchanged.

### 4.4 Background (`events.js`)
`ev.background` is updated on every `classic.Stop` **whether or not the event reporter is on** (today `stopped()`
returns early when `!ev.on`); export `backgroundBusy()` = a task with status `running` or `crons > 0`; `null` (no Stop
seen yet) = not busy.

### 4.5 Telling an idle compaction apart (D4)
In `session.compact` (main, not `precompute`), every role: `trigger === 'auto' && !s.turnRunning` ⇒ idle compaction;
report `--trigger idle` when hello advertised `idle_minutes > 0`, else `auto` as today. Basis (CC 2.1.296 source): the
idle compactor refuses while a turn is in flight (`busy`) and compacts between turns; a context-full auto compaction
runs inside a query. All other compaction handling is unchanged.

### 4.6 Protocol
No protocol bump: every new behaviour is gated on hello's `idle_minutes` (D9).

## 5. CLI (`cmd/pdx/relay.go`)
`relay begin … --reason idle --idle-minutes N` (validated as §2); `relay compacted --trigger idle`;
`relay report <op> cancelled --error resumed`; `relay hello` prints `idle_minutes` / `idle_threshold`.

## 6. SPA and iOS
- `spa/src/lib/team/types.ts`: `SelfRelayPayload` + optional `reason`, `idle_minutes`; `selfRelayPayloadOf` keeps them
  (malformed → dropped, the dialog renders as today).
- Self-relay dialog: when `reason === 'idle'`, one line above the usage: 「閒置 {n} 分鐘，快取快過期；趁現在接力最省。」
  (en "Idle {n} min; the cache expires soon — relaying now is cheapest.").
- iOS card: same line — follow-up to the interface lead (88); the push text (§3.2) already covers the notification.

## 7. Phases (each one PR ≤ 800 lines / 20 files)
- **IR-1a daemon + CLI, self**: §2 (all but compacted), §3.1, §3.2, §5 (begin/report/hello).
- **IR-1b daemon + CLI, member**: §3.3, §3.4, `relay compacted --trigger idle`. Independent of IR-1a.
- **IR-2 mod**: §4. Needs IR-1a deployed (hello); ships with a `pdx setup` re-run on both hosts.
- **IR-3 SPA**: §6 (after #2695, same file).
- iOS line: follow-up issue to 88. Remote members: follow-up issue (D12).

## 8. Tests (named, TDD; mutation gate = remove the rule ⇒ red)
**IR-1a**: `TestRelayHello_IdleNumbers`; `TestRelayBegin_IdleReasonStored`; `TestRelayBegin_IdleValidation` (minutes
0 / 60, unknown reason, idle + manual, non-idle with minutes → 400); `TestRelayBegin_IdleReplayMismatch409`;
`TestRelayReport_ResumedOnlyForIdleOps`; `TestRelayReport_ResumedRacesApproveLikeCompacted` (both orders, asserting
the same outcomes as the compacted race test); `TestPushContent_SelfRelayIdle` (zh-TW, en); CLI: flags reach the wire,
validation errors exit 2.
**IR-1b**: `TestIdleNotice_AfterIdleMinutes` (fake clock); `_OncePerIdlePeriod`; `_ResetOnRunning`;
`_SkipsRelayOpenOrAskOpen`; `_BelowThreshold`; `_RecheckBeforeSend`; `_IgnoresAsksForItself`;
`TestRelayCompacted_IdleTellsLead` / `_IdleDisarmsAndWithdrawsLikeAuto` / `_ManualTellsNobody`; CLI `--trigger idle`.
**IR-2** (`relay.test.ts`, `events.test.ts`, fake clock): arms at main turn.complete, not a subagent's; re-arm keeps
one timer; disarms on turn.start / prompt.submit / clear / compaction / session.start; stale token or gen does nothing;
a prompt / turn / reset while the fire handler awaits usage or id ⇒ no begin; fires ⇒ begin argv has `--reason idle
--idle-minutes 40`; below threshold, under floor + minGrowth, under lastAskPct + 10, control waiting, member role ⇒ no
begin; hello without idle numbers ⇒ never arms even with env set; env override when advertised; background running /
cron ⇒ no begin, non-running task ⇒ begin, `null` ⇒ begin, reporter off still tracks background; prompt during idle
`beginning` (op opened / not) and `awaiting` ⇒ prompt unchanged + `cancelled{resumed}` / no report; prompt during a
threshold ask ⇒ still held; denied ⇒ no idle ask until +10; withdrawn ⇒ asks again next idle period; unattended
immediate approval ⇒ relay proceeds; compaction with no turn running ⇒ `compacted --trigger idle`, inside a turn ⇒
`auto`, hello without idle ⇒ `auto`.
**IR-3**: payload parser keeps / drops the fields; dialog line only for reason idle; i18n completeness.

## 9. Measurements (in IR-2's acceptance)
- **M-I1** a long `$.clock.after` fires in an idle interactive session (throwaway session with
  `PDX_RELAY_IDLE_MINUTES=2`, then one natural 40-minute observation after deploy).
- **M-I2** a peer message to an idle session passes through `prompt.submit` (so §4.3 sees it).
- **M-I3** the first natural idle compaction after deploy reaches the daemon as `trigger=idle`.

## 10. Risks
- §4.5 relies on CC internals (trigger `auto`, between turns). If CC changes, the report degrades to today's `auto`:
  wrong notice text, no functional harm.
- A session on a 5-minute cache TTL gains nothing from a 40-minute ask (the mod cannot read the TTL). Accepted.
- More cards and pushes for people with many large idle sessions; bounded by D7 and the 40% floor.

## 11. Codex plan review (rev 1, task-mv3y4jht-irioan) — dispositions
| # | sev | finding | disposition |
|---|---|---|---|
| 1 | important | D6 overrides U7; a product decision, not derived | **Accepted**: D6 moved to user decisions; the user chose A (2026-10-12) |
| 2 | critical | member idle via `maybeAsk` is blocked by the 70% gate | **Moot**: members use a daemon notice (D3, §3.3) |
| 3 | critical | no withdraw path for a member ask | **Moot** (D3) |
| 4 | critical | member withdraw vs lead approve race | **Moot** (D3); the lead's `pdx relay` is the only action |
| 5 | critical | self withdraw vs approve race; "only from awaiting" leaves a claimed op | **Fixed**: `resumed` = `compacted`'s race semantics, no awaiting-only rule (§2) |
| 6 | important | `beginning` contradictory | **Fixed**: §4.3 beginning rule; tests added |
| 7 | critical | `relay_asks.reason` already used; migration wrong | **Moot** (no ask change) |
| 8 | important | ask replay "as today" wrong | **Moot** (no ask change); begin replay stated precisely |
| 9 | important | remote ask facts missing | **Moot** (D3); remote members out of scope (D12) |
| 10 | important | remote member idle compaction | **Out of scope** (D12), follow-up issue |
| 11 | important | idle gate skips `floor + minGrowth` | **Fixed** (D7, §4.2) |
| 12 | important | member deny/timeout unobservable; two lastAskPct | **Fixed**: one shared `lastAskPct` (D7); member moot |
| 13 | important | env vs hello gating contradictory | **Fixed** (D9) |
| 14 | important | background snapshot not kept when reporter off | **Fixed** (§4.4) |
| 15 | important | SPA payload parser missing | **Fixed** (§6) |
| 16 | important | timer race tests missing | **Fixed** (§8 IR-2) |
| 17 | minor | CLI tests missing | **Fixed** (§8 IR-1a/1b) |
