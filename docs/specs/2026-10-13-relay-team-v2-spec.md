# Relay, unattended mode and team rules v2 (spec)

Date: 2026-10-13. Coordinator: purdex-1f (`mlab/purdex-1f-vq`). Lines: A (daemon, mod) and B / 88 (Mac, iOS).
Status: **user decisions final (§1)**; §3–§4 updated with 1f's decisions of 2026-10-13 (plan §0 「已決」 list); plan: `docs/plans/2026-10-13-relay-team-v2-plan.md`.
Current behaviour: described in two reviews of main 10116256b (alpha.726) that live outside the repo (the lead's `review-relay-rules.md`, "RR §n", and `review-relay-ui.md`, "RU §n", gaps G-1…G-18); the code is the source of truth. `H` = `cmd/pdx/plugin/purdex/hooks/register.js`, `T` = `internal/module/team/`.
Related: host resource lease v2 (lease v2, #2759) provides the host zones used in §4.6; a shared host-metrics spec (research pending) will own the indicator definitions.

## 1. User decisions (2026-10-13, do not reopen)

| # | Decision |
|---|---|
| U-0 | 無人值守**只管單一／團隊 session 的接力用量**。團隊設定（member 上限、資料夾、其他主機資料夾）從無人值守面板搬到 **team 面板**。 |
| U-1 | **使用者親手打的指令一律直接生效**，不詢問、不扣額度，無人值守開關都一樣（`/relay`、team 指令）。 |
| U-2 | 指令收在 `/team` 底下（使用者提議前綴，1f 設計細節，見 §4.1）。`/relay finish` 不需要：`/relay` 本身就會接上已寫好的接力檔。 |
| U-3 | member 啟動要過資源檢查：一般模式資源緊時跳**警示卡（附當下資源狀況）**由使用者決定；使用者用指令強起也一樣檢查並跳警示。 |
| U-4 | 接力門檻維持：一輪結束時 ≥ 70 %；或閒置 40 分鐘且 ≥ 40 %。**40 % 以下讓 Claude Code 自己壓縮**（閒置壓縮不關）。 |
| U-4.1 | 閒置接力和用量接力連續發生時要去重：已寫好的接力檔沒有過期，就不重寫。 |
| U-4.2 | 閒置時寫好接力檔後，使用者回來直接繼續打字：照常進行，不另外處理。 |
| U-5.1 | 使用者核准的 session（單一 session、lead）閒置申請**逾時後自動做前半套**（寫好接力檔），畫面明確說明這是快取將過期的接力；之後 `/relay` 完成，或直接關掉 session。 |
| U-5.2 | member 閒置由 lead 判斷：內容重要且之後沒任務 → 交接後關閉（交接內容交給 lead 納管，不必由 member 自己改記憶與文件）；內容不重要且沒任務 → 直接關閉；內容重要且跟下個任務連貫 → 接力。 |
| U-6.1 | member 的 70 % 申請由 lead 判斷。 |
| U-6.2 | 使用者逾時未同意：80 %、90 % 再申請一次；**最後一次仍未同意 → 明確停止作業**（不是關閉訊息），之後進來的訊息一律回「我目前暫停作業，等待使用者確認中」。lead 也要明確核准或拒絕 member，不能含糊。 |
| U-7 | member 的閒置：Claude Code 要閒置壓縮 member 時**擋下來回報 lead**，lead 照 U-5.2 決定。 |
| U-7.1 | 無人值守時，閒置接力一律直接做前半套。 |
| U-7.2 | 無人值守時，70 % 以上的接力（單一 session、lead、member 都一樣）照額度自動；**額度用完就暫停**，等無人值守解除後重新申請。 |
| U-7.3 | 無人值守時，**lead／member 相關申請一律拒絕**，回給申請者：「無人值守模式不開放申請，請等待模式解除後，依情況重新申請」。 |
| U-8.1 | Mac 開無人值守前跳確認框，**全部資訊一次列出**。 |
| U-8.2 | 拿掉核准卡上的「這個 session 不再詢問」勾選。 |
| U-8.3 | App badge＝所有 session 去重後的 unread 數；申請也算 unread。Mac 桌面版也要顯示同一個 badge。（交給 88） |
| U-9 | 資源觀察指標全系統統一，高頻低耗探查（參考 netdata），狀態顯示在 Purdex 介面上。（另一份 spec） |

## 2. Why

The 2026-10-13 review found 18 interface gaps (RU §6) and 15 rule inconsistencies (RR §10). The worst in practice:
- a card nobody saw counted as a refusal and silenced the session until +10 points (RR §2, case A);
- a person's own `/relay` spent quota or paused the session they were sitting at (RR §10.5);
- three overlapping notices about one member (RR §10.12).

The user rewrote the rules above.

## 3. Facts

- **Who decides what.** The mod decides when to ask (70 %, +10 gate `REASK_POINTS`, growth floor, idle, latch); the daemon decides what happens to an ask (card, auto-approve, pause, refuse) and runs the 600 s timeout (RR §0).
- **lastAskPct is set at begin**, before the daemon answers (H:685, H:793), so a daemon blip at 70 % costs the ask until 80 (RR §10.1).
- **Idle ask** fires from a one-shot 40-min timer armed at turn end, needs ≥ 40 % and ≥ lastAskPct + 10, and never holds prompts (RR §5).
- **Manual `/relay`** goes `begin --manual`. The daemon still runs the unattended path (relay_begin.go:231 ignores Manual). A member's manual `/relay` is always a card (RR §3).
- **Pause machinery exists**: rows, bounce of peer messages, outbox notices, push kind `pause`; resume on unattended-off with a reask control, protocol ≥ 5 (RR §7).
- **Switch-on sweep** auto-approves every open lead / self_relay / adopt / member_relay card (unattended.go:362; RR §7).
- **Member notices to the lead**: the member's own ask (300 s window), the daemon's 70 %-idle notice (notice_usage.go), and the 40-min idle notice (notice_idle.go) (RR §3, §10.12).
- **Claude Code idle compaction** fires about 54 min after the last request when context ≥ minTokens (default 200k). `compact_boundary` has trigger `auto`, the same as a context-full compaction. A `session.compact` hook answering `{skip}` cancels it: read in CC 2.1.296 code, **not verified on a real session** (memory `reference_cc_idle_compaction_bypasses_relay`).
- **Relay op states** today: requested → claimed → writing → written → cleared → done (mod `settle`, H:1031). The handoff file is written by one model turn under the relay lock.
- **No resource check on spawn** today. The only spawn limit is the 20 s register window: #2761's PR #2765 (merged, d3a6e69f) kept 20 s, made the timeout message say why with the host's load, and added `resources.HostReader` / `HostReaderKey` (`LatestHost()`), the read of the last resource sample that §4.6 uses.

## 4. Model

### 4.1 Commands (U-1, U-2)

Typed by the person in a session. Each acts at once: no card, no quota.

| command | where | effect |
|---|---|---|
| `/relay` | any session | Relay now. If a **fresh handoff** exists (§4.3), skip writing and go straight to clear + take over. Works even if the session's self relay is switched off. Never spends quota, never pauses. A member's `/relay` is direct too. |
| `/relay off` / `on` / `status` | any | Unchanged: the person's switch for automatic asks. |
| `/team [name or note]` | neither a lead nor a member | Become lead now. Grant = the default member cap (3, `team.DefaultMaxMembers`), folder = cwd, name/label from the note or chosen by the agent. Editable later in the team panel. In a **member** it only answers that the member must be released first (`pdx release`, or out of the team in the App); nothing else happens. |
| `/team` | a lead | Print the team (members, tasks, context), like `pdx team`. |
| `/team spawn <task> [--model m] [--effort e]` | a lead | Open a member for the task. Model and effort are the lead's choice unless given. Passes the resource check (§4.6). |
| `/team adopt <ref>` | a lead | Take a session in as a member. |
| `/team release <ref>` / `/team kill <ref>` | a lead | Let a member go, or close it. |
| `/lead [note]` | not a lead | Alias of `/team [note]` for one version, then removed. |

- **How "typed by the person" is known.** The mod handles these as registered commands (`$.command.register`, `on('command.run')`, as `/lead` and `/relay` today, H:1450, 1664-1669) and marks the request it then makes as person-originated with a **person token**: minted by the daemon for the mod, used once, bound to the session and the command, valid **60 s** for the commands the mod runs itself (`/relay`, `/team adopt`) and **10 min** for the ones the model runs after composing them (`/team`, `/team spawn`).
- **The person token is a misuse guard, not a security boundary.** It tells the daemon the request came through the mod's command handler, and it stops an agent from re-using a token it once saw. It does not stop an agent that ignores its skill: the agent can type a slash command into its own pane (`tmux send-keys`, the send-keys route) and holds the same daemon bearer the App uses. Today's cards rest on the same honour rule; the skill keeps it. Separating the App's permissions from the agent's is #2774.
- The daemon grants a person-originated lead request, adopt or spawn without a card. A person's command that meets an open request of the same kind (the agent's lead or adopt card, or the session's open relay card) approves that request instead of being refused.
- `/team` (become lead) and `/team spawn` typed while a relay is in progress (write, clear, seed) wait and run as soon as the relay returns to idle; the other `/team` commands act at once in any state.
- The same request made by an agent on its own (`pdx lead request`, `pdx adopt`, `pdx spawn` from a turn the person did not start with a command) follows §4.7.

Lead-only CLI (agents):
- `pdx relay _<ref>`: approve a member's relay (unchanged).
- `pdx relay _<ref> --deny`: **refuse it explicitly** (new, U-6.2).
- `pdx kill _<ref> --handoff`: **hand over, then close** (new, U-5.2). The member writes the handoff file (relay first half, §4.3); the lead gets `[pdx team] member … 已交接：<path>` and the member is closed. The lead decides what goes into memory and docs.

### 4.2 The ask ladder (U-4, U-6.1, U-6.2)

- **Thresholds.** Ask at turn end at ≥ 70 %. After a denial or timeout, ask again at ≥ 80 % and at ≥ 90 % (the existing +10 gate). There is no ask after the 90 % one.
- **lastAskPct moves only when the daemon answered** (an open card, a refusal, an approval). A daemon that does not answer costs nothing; the next turn end asks again. This fixes RR §10.1.
- **Who answers:**
  - solo session and lead → the person (card), or unattended (§4.5);
  - member → its lead. The lead must answer within 5 min with `pdx relay` or `pdx relay --deny`; no answer is a timeout.
- **After the last ask.**
  - Timeout (nobody answered) → the session **stops work**: a pause with cause `relay_unanswered`.
    - The mod does not start new work; peer messages are answered by the daemon without a model turn: 「我目前暫停作業，等待使用者確認中」, or 「…等待 lead 確認中」 for a member.
    - The final card stays open until answered: no deadline and no lease expiry; it also ends when the session ends. A mod that reloads picks it up again.
    - Approving it relays and resumes. Text the person types still goes through (current pause semantics).
    - A compaction ends it: if a turn the person typed fills the context and Claude Code compacts, the card is cancelled, the pause ends, and the ladder starts again from 70 % on the smaller context.
    - Releasing the pause in the App (「解除」) is a denial: the session goes on and asks no more.
  - **Which cards are final**: a self-relay card at ≥ 90 % that is not an idle ask and not an older mod's manual `/relay` card; a re-ask card after a pause is final too.
  - **A lead paused this way stops alone.** Its members keep working; their messages to the lead get the paused auto-reply; their own asks follow the ladder with the lead as the answerer, so they time out and may pause themselves at 90 %.
  - Explicit denial at 90 % → no pause and no more threshold asks: the person (or lead) chose to let it run. Claude Code compacts it when the context is full. The idle ask (§4.3) still comes as usual.
- **Hold.** A threshold card still holds prompts while it is open, up to 10 min (unchanged, RR §6). An idle ask never holds.

### 4.3 Idle relay and the fresh handoff (U-4, U-4.1, U-4.2, U-5.1, U-7.1)

- **Idle ask** (solo session, lead): idle 40 min and ≥ 40 %. It is **not** gated by +10 any more: a session that timed out at 70 % and then went idle still gets its idle path (RR §10.4).
  - Attended: a card with a **5-min** life (was 10).
  - Unattended: no card.
- **First half.** When the idle card times out (attended), or at once (unattended), the mod runs the relay's first half:
  - the handoff-writing turn, ending at state `written`, without `/clear`;
  - the op closes as `half` (stored as `cancelled` with reason `half`; the op's `mode` says what it was for); the session keeps its context.
  - It must start before Claude Code's idle compaction (~54 min): 40 + 5 leaves ~9 min, and the turn itself resets Claude Code's idle timer.
  - It spends **no quota**: nothing was cleared.
- **What the person sees.**
  - Terminal: a status line plus one message: 「快取即將過期，已先寫好接力檔（<path>）。要接力請打 /relay 或 /relay now，會直接接上後半：清空並由新對話接手，不用重寫接力檔；不需要就直接關掉這個 session，或照常繼續。」
  - App: the session row shows 「已寫好接力檔」 (88), for solo sessions as well as team rows (§4.8).
- **Fresh handoff.** A `half` handoff stays fresh while the session has had **no new turn** since it was written. Any later relay of that session, whatever the trigger, reuses it and goes straight to clear + take over:
  - `/relay`;
  - an approved 70 % ask;
  - an unattended auto-approval;
  - `pdx kill --handoff`.

  Any new turn makes it stale; the next relay writes a new one. A person who comes back and just types carries on in the old context (U-4.2).
- **Below 40 %**: no idle ask; Claude Code's idle compaction happens as before (U-4).

### 4.4 Members: idle and the lead's decision (U-5.2, U-7)

- **Intercept.** When Claude Code tries to idle-compact a member, the mod's `session.compact` hook answers `{skip}` and the daemon tells the lead once:
  「member <address>「<title>」閒置，快取快過期，context N%。請決定：接力（pdx relay _ref）、交接後關閉（pdx kill _ref --handoff）、直接關閉（pdx kill _ref）。」

  Recognising an idle compaction: trigger `auto`, no main turn running, context below the context-full point. **Gate M-1** (§6) must confirm this first.
- If the lead does nothing, the member stays as it is: not compacted, cache cold.
- This replaces, for members, the 40-min idle notice (notice_idle.go) and the 70 %-idle notice (notice_usage.go). A member's lead hears about it at most twice: its 70 % / 80 % / 90 % asks and this intercept (RR §10.12).
- Members never get the idle first half on their own (§4.3 is for solo sessions and leads).

### 4.5 Unattended mode (U-0, U-7.1, U-7.2, U-7.3)

- **Scope.** Unattended decides relays only.
- **Relays at ≥ 70 %** (solo, lead, member): auto-approved while the chain's quota lasts (quota rule on), or always (rule off). With the quota used up → pause (cause `quota`, as today). When unattended is switched off, every paused session re-asks (today's reask control).
  - Solo and lead: as today.
  - **Member: new.** Today a member's ask only notifies its lead. In unattended mode the daemon accepts the ask itself, exactly as the lead's `pdx relay` would, against the lead's member pool; pool 0 → the member is paused (cause `quota`). The lead is told. Attended, the lead decides (U-6.1).
- **Idle**: the first half at once (§4.3).
- **Lead / member applications are refused** with 「無人值守模式不開放申請，請等待模式解除後，依情況重新申請」:
  - an agent's own `pdx lead request` (exit 13 with code `unattended_refused`);
  - an agent's `pdx adopt`;
  - a spawn that would need a resource card (§4.6).

  A spawn that passes the resource check is not an application and goes ahead. Person-typed commands (§4.1) are never refused.
- **Switch-on.** Open lead and adopt cards are refused with the same text (they are no longer auto-approved); open relay cards follow the relay rules above. The Mac and iOS confirm dialog shows all of it first (§4.8).
- **Member relay pool** (the lead's quota for its members) stays in the unattended panel; the team cap and folders move to the team panel (U-0, 88).

### 4.6 Spawn resource check (U-3)

- Before a spawn launches anything, the daemon reads the host zone (lease v2 D-1′) through `resources.HostReader` (#2765; the host-metrics spec keeps this seam as everyone's read of the zone).
  - **normal** → spawn.
  - **warning or danger** → attended: a card `spawn_resources` for the person, showing the zone, memory %, pressure, load, running heavy leases and the queue, with 放行 / 不放行. Unattended: refused (§4.5). An unknown zone counts as normal (fail open, as leases do).
- `/team spawn` typed by the person goes through the same check and the same card (U-3, "強起").
- Card answer exits: approved → spawn proceeds; denied or timed out (**480 s**, so it closes before `pdx spawn`'s 9-min wait gives up) → exit 10 / 11, as other cards.

### 4.7 Agent-initiated applications, attended (unchanged except §4.5)

An agent's own `pdx lead request` and `pdx adopt` open a card as today; the person decides. The skill text says that in unattended mode they are refused.

### 4.8 Interface (88; contract in the plan)

- Remove the 「這個 session 不再詢問」 checkbox, Mac and iOS (U-8.2). `/relay off` remains the only switch.
- Unattended confirm dialog, Mac and iOS (U-8.1), from a new `GET /api/team/unattended/preview`. It lists:
  - sessions whose quota is 0 (they would pause at their next relay);
  - open relay cards and what each would become;
  - open lead and adopt cards that would be refused;
  - sessions already paused.
- Team cap and folders move to the team panel (U-0).
- Card kinds: `spawn_resources` (new), and `self_relay` with `idle` (5 min).
- Pause causes: `relay_unanswered` (new, with its text) next to `quota` and `lead`.
- The session row shows 「已寫好接力檔」 while a fresh half handoff exists: `GET /api/relay/fresh` and the host event `relay.fresh` for every session (solo included), and the roster for team rows.
- iOS: a `member_relay` card where one still exists (G-1). Badge per U-8.3 (88 has taken it).

## 5. Phases (A line unless noted; one PR each ≤ 800 lines / 20 files)

| phase | content |
|---|---|
| RT-1 | person-typed commands: the `/team` family and the `/lead` alias, the person token, direct `/relay` (no card, no quota, overrides self-relay off, members included); unattended refusals (§4.5) and switch-on refusal of lead/adopt cards; skill texts |
| RT-2 | the ask ladder: lastAskPct only on an answer; `pdx relay --deny`; the member ask follows the ladder with the lead's timeout; pause cause `relay_unanswered` after the last ask; the idle ask freed from +10 |
| RT-3 | first half and fresh handoff: op outcome `half`, the 5-min idle card, unattended immediate half, reuse by any relay, terminal message, `pdx kill --handoff` |
| RT-4 | member idle intercept and notice consolidation (after M-1) |
| RT-5 | spawn resource check and the `spawn_resources` card (after lease v2 P1b provides zones) |
| RT-6 | `GET /api/team/unattended/preview`; capability flags for 88 |
| UI-* | 88: §4.8 on Mac and iOS |

## 6. Gates

- **M-1 (before RT-4):** in a throwaway session with a small `CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS`, confirm that:
  - the mod's `session.compact` hook sees the idle compaction;
  - it can tell it apart from a context-full one;
  - `{skip}` really prevents it;
  - Claude Code does not retry at once.

  If it fails, RT-4 falls back to a member idle timer at 50 min that notifies the lead, and compaction then happens if the lead does nothing.
- **M-2 (in RT-3):** a first half written by an idle session is reused by a later `/relay` with no rewrite (fresh); a new turn makes it stale.

## 7. Out of scope

Badge (88, U-8.3); host metrics (separate spec, U-9); quota refills; remote-member pause (RR §10.8).
