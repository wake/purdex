# Host resource lease v2 — amendment A: no session ever waits on a lease (spec)

Date: 2026-10-13. Coordinator: purdex-1f (`mlab/purdex-1f-vq`). Issue: #2759.
Amends `docs/specs/2026-10-13-host-resource-lease-v2-spec.md` (P1a–P2a are merged; P2a = #2791, #2792). **Replaces** D-4′'s hold and parking expiry, the P2b row of §5, and the meaning of `deadline_s` / `park_s`; **extends** D-3′ (what "a lease is running" means) and D-8; adds V11–V22. Everything else in the v2 spec stands.
Status: **user decisions final (§1), 2026-10-13 23:xx.** Both hosts are back in mode **advise** since 22:1x (mlab hostconfig rev 25, air26 rev 3) until this ships.
The P2b branch `feat/lease-v2-p2b` (3 commits, no PR) is input to P2c: its replay fix (session compared only when the client named one) and its exit-15 / `{deny}` plumbing are kept; its "wait the hold, then queued" behaviour is replaced.

## 1. User decisions (2026-10-13 evening, add to v2 §1; do not reopen)

| # | Decision |
|---|---|
| V11 | **以「session 不會 pending／lock 在等租約」為前提。** 排不到就立刻回應、讓這一輪結束；不再先等 540 秒。（取代 V4 的「一次呼叫等不到時」。） |
| V12 | **排隊不改變 session 原本的路徑（1:1）。** 前景（下指令 → 等 → 結果）：輪到時 daemon 通知「可以發起了」，等 session 自己重下同一個指令。背景（下了就回來）：輪到時 daemon 先問要不要執行，同意就代發，跑完照原本的背景完成通知。 |
| V13 | 主機有空就照常立刻執行（「無 queue 情況」不變）。 |
| V14 | 任何 session 隨時看得到整個佇列（誰、哪條指令、哪個目錄），可以撤銷**自己發起的**請求。 |
| V15 | **一個 session 一個位置**：佇列裡排的是 session，不是一條一條指令；排隊中再下的重指令都掛在同一個位置。 |
| V16 | **位置空著超過 3 分鐘就放掉**：輪到時等它發起、以及它的指令跑完後等它接著下一個，都是同一個 3 分鐘空窗。還沒輪到時不算空窗，位置一直保留到 session 結束。 |
| V17 | **接續是自動的，不需要宣告**（多 3 分鐘無傷大雅，也省來回溝通；「主動宣告」的邊界情況太多）。session 可以 `pdx lease done` 提早放掉，但不是必要的。 |
| V18 | **只有在有人排隊時才有限制**；沒人排隊時指令數與時間都不限。 |
| V19 | 有人排隊時，一個位置**最多 3 個指令（含延續）**，不論時間；需要更多就重新排隊（連續是有需要才跑的，一般情況就是分開排隊）。 |
| V20 | 有人排隊、且這個位置連續占用**超過 30 分鐘**：正在跑的指令照常跑完，之後**再給一個延續（最後一個指令）**，並附上說明：「因為執行時間過長，這次指令執行完畢後會釋出執行排序，請自行安排執行方式與內容。」1 超時 → 1＋延續(2)；1＋2 超時 → 1＋2＋延續(3)；1＋2＋3 → 已經用完。 |
| V21 | **一定先預告才放掉**：只要還沒預告過，就一律再給一個延續（代價：一個位置最多 4 個指令，例如排隊的人在第 3 個指令跑的時候才出現）。 |
| V22 | 新設計上線前兩台維持 **advise**（22:1x 從 lease 切回）。 |

## 2. Who waits — three callers

| caller | today | new |
|---|---|---|
| **F** agent, foreground Bash (mod-managed heavy command, or a `pdx lease run` it typed or a script it runs calls) | blocks until granted (hold 540 s after P2a) | never waits: granted at once, or **queued at once** (`{deny}` text / exit 15) |
| **B** agent, background Bash (`run_in_background`) | not managed (the mod skips background calls) | the mod wraps it as `pdx lease run --ask`; it waits **inside the background process**; at its turn the session is **asked**; yes runs it, no / 3 min cancels |
| **S** no session (launchd, cron, a script no agent started) | waits until granted | unchanged |

- **How a nested call knows its caller.** The mod exports `PDX_LEASE_CALLER=fg` or `bg` into every Bash command it runs (from the tool input's `run_in_background`). `pdx lease run` reads it: `fg` → queue mode, `bg` → ask mode, unset → wait mode (S). Explicit `--queue` / `--ask` / `--wait` override it.
- **Scripts.** A script with heavy steps should be wrapped at the outermost level (`pdx lease run --kind <k> -- ./script.sh`), so the whole script is one request and is never cut in half. The skill says so. When a foreground script's inner step gets queued anyway, the queued text adds: 「這支腳本做到一半被排隊，前面的步驟已經執行；之後請用 `pdx lease run --kind <k> -- <整支腳本>` 整支重跑。」(Q1 of the draft, resolved by V15–V17.)

## 3. The position (replaces D-4′ hold and parking expiry)

A **position** is one session's place in the queue and, once its turn comes, its hold on the host. It is the queue unit (V15). Requests are its commands.

### 3.1 States

| state | meaning | ends when |
|---|---|---|
| `queued` | waiting for its turn; keeps `created_at` | the session ends (`abandoned`), the session cancels it, or its turn comes (→ `offered`) |
| `offered` | its turn came; nothing of it runs yet; the host is reserved for it | a command of the session starts (→ `running`), 3 min pass (`no_answer`), cancel, session ends |
| `running` | a command of it holds the lease | the command ends (→ `idle`, or released, §3.3) |
| `idle` | between two commands; the host stays reserved | the next command starts (→ `running`), 3 min pass (`idle_timeout`), `pdx lease done`, session ends |

- **Reserved counts as running** for D-3′: while a position is `offered`, `running` or `idle`, "a lease is running" is true, so the table decides as it does today for a held lease. A second position can still be granted at the same time when the table says grant (normal + flat, etc.); the position does not make the host exclusive.
- **Offer = grant decision.** When a pass would grant the head and the head is `queued`, it becomes `offered` and the gap (V3) starts, as for a grant. Its first command is granted at once, without a new table check (the reservation is the point of the offer).
- **Liveness.** A `queued` / `offered` / `idle` position is not polled: it lives as long as its **session's agent process** (registry, like `holder_gone`). It has no time limit while `queued` (the old `park_s` 900 s expiry is removed: a long queue must not drop it). A `running` command keeps the v1 lease (poll / holder liveness) as today.
- **One position per session per host.** Any heavy command of the session attaches to it, whatever its kind or weight (weights no longer decide admission, D-3′). P2a's re-attach (same session, kind, weight) becomes "same session".

### 3.2 F and B on the position

- **F, not granted:** the position is `queued` (created, or the existing one); answer `state:"queued"`, `position`; the mod `{deny}`s, `pdx lease run --queue` exits 15. When it becomes `offered`, the session is told 「可以發起了」(§4); its re-run of the same command starts at once.
- **B, not granted:** the background `pdx lease run --ask` keeps a long poll on its request, attached to the position. When the position is `offered` (or `idle`, see below), the session is **asked** (§4): `pdx lease confirm <id>` runs it, `pdx lease cancel <id>` or 3 min ends it (`pdx lease: 已取消，指令沒有執行`, exit 15). Its output and completion arrive as any background command's do.
- **A command of a position that is already `running` or `idle`** (the session runs two things at once, or a B command is waiting while an F command runs): it starts at once if the table grants a second command; otherwise it waits attached to the position (B) or is answered `queued` with "等你自己的上一個指令跑完" (F), and is offered when the position goes `idle`. It is still one of the position's commands for V19–V21.

### 3.3 Limits (V18–V21), checked when a command of the position ends

Counters per position, from its first command: `k` = commands that have ended, `T` = time since the first command started (idle windows included), `warned` = a final notice was given.

When a command ends:

1. **Nobody is waiting** (no other position `queued`): no limit. → `idle` (3-min window). A notice given earlier is void (it is only a warning that someone waits).
2. **Someone is waiting** and `warned` (this was the announced last command): **release** the position.
3. **Someone is waiting**, not `warned`, and the **next command must be the last**: `k + 1 ≥ 3` (V19), or `T ≥ 30 min` (V20), or `k ≥ 3` (V21, the waiter came after the third command started). → `idle`, `warned = true`, and the **final notice** goes with this command's result (§4).
4. **Someone is waiting**, otherwise: → `idle` as usual.

Results: 1 over 30 min → 1 + extension (2). 1 + 2 over 30 min → 1 + 2 + extension (3). Three short commands with someone waiting → the third is announced last. A waiter who appears during the third (or any later) command → one announced extension. Never released without a notice, except by the 3-min window, `done`, cancel or the session ending.

- The final notice texts (Chinese, wording final in the plan):
  - over 30 min: 「因為執行時間過長，這次指令執行完畢後會釋出執行排序，請自行安排執行方式與內容。」
  - three commands: 「有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。」
- **Where the notice appears.** With the result of the command that just ended: the mod adds it to that tool call's result (the release answer carries it); `pdx lease run` prints it to stderr after the command's own output. Not a separate message (user, 2026-10-13: 1 ok).
- Nothing running is ever interrupted (V8): limits only decide what happens after a command ends.

## 4. Telling the session

- **Offer (「可以發起了」), B question, `no_answer` / `idle_timeout` cancel:** between the session's turns, through its mod — the way the relay seed reaches a conversation. To verify in the plan (measure, don't infer): the mod's prompt-submit path, and what happens when the session is mid-turn (it lands when the turn ends; the 3 min still count from the offer).
- **Final notice:** with the command's result (§3.3), so the session reads it before it decides the next step.
- **Fallback** (no mod channel: not a Claude Code session, or an old mod): a member gets it from its lead's inbox (D-6 team path); anything else is a user alert naming the session.
- Texts (Chinese, wording final in the plan):
  - queued: 「主機資源忙，這個指令還沒執行：排在第 N 位。輪到你時我會通知你，先做別的事；不要改成背景執行或加 `&` 繞過排隊。」
  - offer: 「輪到你了：`<command>`（`<cwd>`）。3 分鐘內照原樣重跑就會立刻執行；之後 3 分鐘內接著下的重指令也會直接跑。不跑請 `pdx lease cancel <id>`。」
  - B question: 「輪到你的背景指令了：`<command>`（`<cwd>`）。要執行就 `pdx lease confirm <id>`，不要就 `pdx lease cancel <id>`；3 分鐘沒回應會取消。」
  - cancelled: 「3 分鐘沒有動作，你在佇列裡的位置已經放掉；之後要跑請重新下指令。」

## 5. Seeing and cancelling (V14)

- Each request stores `command` (the command line as the mod or `pdx lease run` got it, first 200 bytes) and `cwd`; env is never stored. The command text is visible to every session of the host (same user's agents).
- `pdx lease ls` shows every position: number, session (name and ref), state, and for `offered` / `idle` the time left; under it, its commands with mode, command, cwd and state; and, for `running` / `idle`, `k`, `T` and whether it is warned.
- `pdx lease cancel <id>` (a request or a position) and `pdx lease done` act on **the caller's own session** only (resolved by the P2a ancestry walk; another session's → 403). Like #2774, this guards against mistakes, not against a token holder. `pdx lease confirm <id>` is the same, for B.
- New end reasons: `cancelled_by_session`, `no_answer`, `idle_timeout`, `done`, `released_after_limit`.
- The report (D-7) adds: positions, commands per position, extensions given, and how long the 3-min windows held the host while someone waited (the cost of V17, so it can be revisited with data).

## 6. Phases (replaces the P2b row of v2 §5; P1a–P2a are merged)

| phase | content |
|---|---|
| P2b′ | daemon: positions (table, states, one per session), request `mode` / `command` / `cwd`, queue at once, offer with gap, reserved-counts-as-running, liveness by session, the §3.3 limits and the notice on release, `confirm` / `cancel` / `done` routes with the own-session check, end reasons, `pdx lease ls` / report fields. Likely two PRs (positions + states; limits + routes). |
| P2c | clients: `pdx lease run --queue / --ask / --wait` and `PDX_LEASE_CALLER`; `pdx lease confirm / cancel / done`; mod: foreground `{deny}` on queued, background rewrite into `--ask`, export `PDX_LEASE_CALLER`, the final notice in the tool result; skill text; probe M-P1. Starts from `feat/lease-v2-p2b`. |
| P2d | delivery of offer / question / cancel through the mod between turns, with the fallback; the §4 measurement first. |
| — | after P2b′–P2d are deployed on both hosts: switch both to **lease** and tell the user. |

P3a / P3b (anomaly alerts) are unchanged and come after; A5 "queue stuck" reads positions.
