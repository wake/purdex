# Lead / member / team and context relay — spec

Status: **passed review by `air26/_9iwyyv` on 2026-10-06** (c358cd65 plus the released-prompt note), then revised for U5b. Paused 2026-10-07 by the user, **resumed the same day** (handed by `air26/_9iwyyv` to `mlab/_81nu3d`, purdex-b1).
- Spec writer: `mlab/purdex-4d` (`mlab/_v3o1ps`).
- Source: the brief `docs/ideas/2026-10-06-lead-team/brief.md` (untracked on mlab's main checkout), with the prototype mod `relay-mod/` and its handoff `handoff-run1.md` beside it.
- Research page: `https://pages.mlab.host/wake/purdex/context-relay.html`.
- The U5 strength question went through two answers on 2026-10-06. First U5a (human presence in v1), which U5b then withdrew: an approval is one click on any App, and the U5a layer of no CLI path, broadcast and audit stays (§2, §6.5).
- U13 (self-relay switches and approval) was added the same day. Its derivations (a)–(d) are in §8.7, and air26's review points (e) and (f) are in §8.4 and §8.3.
- U13a and U14 followed the same day: self-relay approval is one click, and the browser SPA is retired, so every client is a Purdex.app.
- U15 (2026-10-07) makes cross-host spawn a core need, with a host-selection rule. Its derivations are in §7.4, its measurements M13–M16, its phases P4b and P4c.
- U17 (2026-10-07) brings the synchronous hook decision (`pdx hook` waits for the daemon on PreToolUse and PermissionRequest) from §11 into the phases: §6.6, M17–M21, phases P2c and P8.
- U19 (2026-10-07) replaces §6.6's forwarding with **分流**: the terminal keeps its native AskUserQuestion and permission dialogs untouched, every other client (desktop or phone) gets an event card, the first answer wins and closes the rest; the Purdex mod races the native dialog against the daemon's answer (measured, M24); no per-session switch, no Mac App card; phases P8a/P8b.
- U18 (2026-10-07) fixes the model tier: spawn uses the default model; a self relay keeps model and effort (measured: `/clear` already does, M21); handoff (切換) keeping them is tracked outside this spec (§16), and P1 records `model.id` / `effort.level` for it.
- U20 (2026-10-07, after the line moved to `mlab/_7wcg1d`) replaces U18's spawn bullet: the CLI's default model is not stable, so `pdx spawn` takes optional `--model` / `--effort` (M25), and the lead is reminded at activation and at a spawn without `--model` to choose them per task (§2, §6.1, §7.2, §10; phase P4).
- U16 (2026-10-07) fixes the Chinese vocabulary: 切換 (handoff, terminal ↔ worker) and 接力 (relay, a new conversation when context runs out); 交接 is retired. This spec's prompts and the 接力檔 follow it; English identifiers do not change.

Every place where this spec departs from the brief's design draft (brief §5, D1–D8) is marked **⟲ changed from D…**, with the reason. §13 lists all of them.

## 1. Goal

The user manages the context of long tasks by hand today:

- **Parallel work.** A session A discusses the requirement and then develops it. For parallel work, A opens subagents, or the user opens extra Purdex tmux sessions and hands their addresses to A.
- **Handoff.** When any session has less than 30% context left, the user opens a new tmux session and tells the old one "hand the task over to {peer}".

This spec makes two things automatic:

1. **Self relay:** a single session hands off to itself, almost unnoticed. U13 adds one visible step: each self relay is approved by the user first (§8.7). "Almost unnoticed" covers the relay itself.
2. **Lead with a team:** a lead can open members, on the host the rule picks for the repo (U15), and can relay a member on the member's behalf.

## 2. User decisions (2026-10-06, do not reopen)

Copied verbatim from the brief §2.

| # | 決策 |
|---|---|
| U1 | 接力門檻：**已用超過 70%（剩餘低於 30%）時觸發** |
| U2 | 交接檔由 **agent 自己寫**：用 mod `$.prompt.submit` 送進對話，讓它有完整工具（git、讀檔、pdx）。**不用** `$.model.fork`（不能用工具），**不用** Haiku 代寫 |
| U3 | 定址**一律用 ref**。ref 在 `/clear` 後會變（見 F2），由 Purdex 負責讓 ref 繼續有效（設計草案用轉址鏈） |
| U4 | 詞彙改為 **lead / member / team**。PRODUCT.md §3.5 的 Role（worker / operator）與 §6.1 Operator 要配合修改。member 不能叫 worker，worker 是 Nexen 無頭執行的用詞 |
| U5 | 進入 lead 模式：由 session 判斷工作夠大、可以平行時，**向使用者申請**；**核准走 UI 層**，不能由 CLI 自己核准（cld-yolo 是 bypass 模式，模型能跑任何指令） |
| U6 | 核准要**同時推到所有 client**（瀏覽器 SPA、Purdex.app、其他裝置），**任一個 client 回應後，其他 client 的提示一起結束** |
| U7 | 申請期間要**鎖住 session**：在收到回應或逾時之前不能繼續做事。第一版採軟鎖（見 D3），**逾時視同拒絕** |
| U8 | lead 模式下，lead 能**主動開 tmux 加 session，並拿到它的 ref**（spawn） |
| U9 | member 的接力**由 lead 決定、由 lead 發動**，member 不自己觸發。**daemon 只做機械式的偵測、通知與執行，不替 lead 做決定**（包括沒有「daemon 到某條硬線就自動 relay」） |
| U10 | spawn 時**建議**用 worktree，但最後**由 lead 自己安排** |
| U11 | member 會有新的視覺，**另外獨立處理**，不在這份範圍 |
| U12 | daemon 重啟（Purdex 開發自己時常發生，**5–10 秒內恢復**）不能讓等待中的申請或 pdx 指令壞掉，見 D6 |

**Supplementary decision.** The user made it on 2026-10-06, and `air26/_9iwyyv` relayed it in answer to this spec's §6.5 question.

| # | 決策 |
|---|---|
| U5a | U5 第一版就要做**「人在場驗證」**，不能只做到「看得見」。「不提供核准路徑、所有決定廣播加稽核紀錄」照樣保留，當作附加的一層 **（已由 U5b 撤回）** |

**Second supplementary decision.** The user made it on 2026-10-06, and `air26/_9iwyyv` relayed it. Copied verbatim.

| # | 決策 |
|---|---|
| U13 | 自我接力的開關與核准<br>- 一般 session（不是 lead 也不是 member）：自我接力預設開，可以關閉。<br>- lead：自我接力預設開，可以關閉。<br>- member：自我接力預設關，接力必須由 lead 安排（和 U9 一致）。<br>- 自我接力（一般 session 與 lead）每次都要先經過人類核准，比照 lead 模式：走同一套 UI 多 client 推播與 U5a 的人在場驗證（已登記的 App 加 SE 金鑰）。**（此點已由 U13a 修正）**<br>- lead 要求 member 接力（pdx relay）不需要核准，由 lead 自己安排。 |

**Third supplementary decisions.** The user made them on 2026-10-06, and `air26/_9iwyyv` relayed them.

| # | 決策 |
|---|---|
| U13a | 修正 U13：自我接力的核准**只要按一下確認，不用 Touch ID，也不需要人在場驗證**；任一個 client 按下都算。人在場驗證（U5a）**只用在 lead 申請**。自我接力仍然走同一套多 client 推播與「任一個回應就全部結束」 |
| U14 | 瀏覽器版 SPA 退役（使用者原話：「SPA 直接退役」） |

**How this spec reads U14** (air26's reading):
- Purdex.app is the only client.
- The App still renders the same SPA code.
- The Vite dev server stays for the App's HMR.

In this spec, U14 is **a premise only**. Turning the browser version off is not in any phase here.

**Fourth supplementary decision.** The user made it on 2026-10-06, and `air26/_9iwyyv` relayed it. Copied verbatim.

| # | 決策 |
|---|---|
| U5b | lead 申請不需要 Touch ID 或人在場驗證，和自我接力一樣，任一個 App 按一下就核准。理由：cld-yolo 本來就能用 Bash 自己開 tmux 和 claude，lead 模式沒有給 agent 新的能力，核准的意義是「告知與同意」，不是安全邊界。U5a 原本那一層照樣保留：pdx 沒有核准指令、skill 明文禁止自我核准、每筆決定都廣播並寫稽核紀錄。Secure Enclave 方案移到 §11（之後硬化時再做）。 |

Also decided on the research page (§9 "已決定"): `session.compact` is a safety net, so that auto-compact cannot get in before the relay.

**Fifth supplementary decision.** The user made it on 2026-10-07, and `air26/_9iwyyv` relayed it. Copied verbatim.

| # | 決策 |
|---|---|
| U15 | 跨主機 spawn 是核心需求。起 team 時要「依需求挑合適的主機」，選擇規則依序：<br>1. 檢查哪台主機有相關的 repo；<br>2. 如果多台都有，比較各主機的 weekly usage 剩餘量；<br>3. 如果都還夠用，優先在 mlab 啟動。 |

Background the user gave: an iOS repo is about to be developed on a26; when the daemon needs a change, the lead on a26 opens a member on mlab. That is the case "lead on a26, member on mlab".

**Sixth supplementary decision.** The user made it on 2026-10-07, and `air26/_9iwyyv` relayed it. Copied verbatim.

| # | 決策 |
|---|---|
| U16 | 用詞：terminal ↔ worker 的換手，中文叫「切換」，英文維持 handoff；context 用完換新對話，中文叫「接力」，英文維持 relay。「交接」這個中文詞不再使用：spec 裡的「交接檔」改成「接力檔」，寫給模型看的提示文字（接力 prompt、接手 prompt）也一併改。程式碼的英文識別字不動（nex-handoff、HandoffDialogHost、relay_ops 照舊）。<br>已上線 UI 文案裡的「交接到 worker」等改成「切換到 worker」，這部分在 P0（PRODUCT.md 詞彙）同一個 PR 或另一個小 PR 處理，由你判斷。不影響 U1–U15。 |

**How this spec reads U16:**
- U2 is quoted verbatim above, so its 「交接檔」 stays as the user wrote it on 2026-10-06; everywhere else this spec says **接力檔**, and the file is still `<data_dir>/relay/<op id>.md` (§8.3).
- The prompts the mod sends (§8.2, §8.7) and the dialog copy (§6.3, §8.7) say 接力, never 交接. 接手 (take over) stays: `↪ 接手自 <old ref>`.
- The live UI strings (`handoff.error.*` in `spa/src/locales/zh-TW.json`, three strings on 2026-10-07) change 交接 → 切換 in a small PR of their own right after P0, so P0 stays a PRODUCT.md-only PR.
- English identifiers (`nex-handoff`, `HandoffDialogHost`, `relay_ops`, `handoff.*` i18n keys) do not change.

**Seventh supplementary decision.** The user made it on 2026-10-07, and `air26/_9iwyyv` relayed it. Copied verbatim.

| # | 決策 |
|---|---|
| U17 | 把「pdx hook 能同步等待 daemon 的決定」從 §11 拉進主線。<br>範圍（air26 在 alpha.510 盤點的事實，請重新驗證）：<br>- CC 裝了 12 種 hook，Codex 裝了 10 種，目前全部只送不等（cmd/pdx/hook.go）。CC 本來就會等每個 hook process 結束，所以延遲一直都在付；這次的改動只是讓 hook 讀取 daemon 的回覆，並輸出決定。<br>- 只有 PreToolUse 和 PermissionRequest 這兩種需要等待決定，其他維持現狀。<br>- 原則照 §11 的硬鎖：有本機旗標檔（有待決項目）時才連 daemon，沒有就照舊立刻結束；daemon 連不上一律放行，絕不能把所有 session 卡死。<br>用途（請在 spec 寫明，並決定哪些進第一版）：<br>1. lead 申請時的硬鎖（PreToolUse）：補上軟鎖「模型把指令丟背景執行」的漏洞。<br>2. member 接力時的鎖（PreToolUse）。<br>3. iOS App（另一條開發線，repo 是 wake/purdex-ios）要用：手機直接核准 PermissionRequest，以及可能透過 PreToolUse（matcher AskUserQuestion）把答案回填。後者能不能做到還沒查證，請對照當前 CC 的 hooks 文件或用 probe 實測，回報結果。這兩項是為 iOS 鋪路，daemon 端只需要「待決請求加長輪詢加回傳決定」的通用機制，手機端的 API 不在這份 spec 的範圍。<br>4. Codex 的 PreToolUse 和 PermissionRequest 能不能用同樣的方式回傳決定，也請確認。<br>請評估要放在哪個 phase（新開一個，或併進現有的），照 800 行／20 檔的上限拆分，改完回我 commit。不影響 U1–U16。 |

**How this spec reads U17** (derived in §6.6; the measurements are M17–M21):
- The facts hold, re-verified on `fb9fcbd8`: 12 CC hooks and 10 Codex hooks, all fire-and-forget, `pdx hook` never writes to stdout (M17).
- **Use 1 (lead hard lock) and use 2 (member relay lock) are in v1**, as P2c and inside P6. **Use 3 is in v1 as 分流 (U19)**: two more `approval_requests` kinds (`hook_ask`, `hook_permission`) with the same CAS, event and `decide` route the lead request uses, raised by the Purdex mod while the native dialog stays open; P8a and P8b.
- **AskUserQuestion can be answered through PreToolUse**: measured in an interactive session (M19), not only in `-p` mode as the docs describe.
- **Codex answers the same way** by its hooks documentation (M20); the plan probes it once before P8 ships.
- `pdx hook` still exits 0 on every path and still prints nothing unless a decision was obtained; an unreachable daemon means no output, which is the normal permission flow, never a forced allow.

**Eighth supplementary decision.** The user made it on 2026-10-07, and `air26/_9iwyyv` relayed it. Copied verbatim.

| # | 決策 |
|---|---|
| U18 | 模型等級：<br>- lead → member 的 spawn：固定使用預設模型，不帶 --model/--effort，維持現狀。<br>- 自我接力：接手的新對話必須和原本同模型、同 effort 等級。<br>- 切換（handoff，terminal ↔ worker 雙向）：理論上也必須同模型、同 effort。 |

air26's facts behind it (2026-10-07), re-verified here as M22: Nexen starts a worker without `--model` / `--effort`; the handoff profile passes `--setting-sources user,project,local`, so the worker takes the settings' defaults (mlab: `model: opus`, `effortLevel: high`; a26: no `model`, `effortLevel: high`). A terminal session on Opus 5.5 xhigh therefore comes back as Opus high after a switch, and a Fable session comes back as Opus.

**How this spec reads U18** (measurements M21, M22):
- **(a) Self relay needs no reset.** `/clear` keeps both the model and the effort level in the same process (M21: `claude-sonnet-5-5` / `low` survived into the new session id). The mod does nothing about them; the P5b acceptance checks the statusline before and after a relay shows the same `model.id` and `effort.level`. If a later Claude Code drops either, the mod re-applies them with `$.command.run('model …')` / `('effort …')` from values it captured at `session.start` — the plan notes this as the fallback, not as v1 code.
- **(b) The daemon learns model and effort from the statusline, not from hooks.** The statusline payload carries `model.id` and `effort.level`; the hook payloads carry neither (M21). P1's parser records both beside the context usage, per CC session id (§8.5). Today nothing in Purdex or Nexen carries them (M22).
- **(c) 切換 is outside lead/team.** It is tracked as two issues, not a phase here: Purdex `wake/purdex` (pass the session's model and effort on terminal → worker through `execution.Request`, and append `--model` / `--effort` to the worker → terminal resume and rollback commands from the execution's values) and Nexen `wake/nexen` (`Model` / `Effort` on `execution.Request`, persisted on the execution because every turn is a new process, emitted after `--resume`, with a test that the flags beat the three settings scopes). Links in §16. This spec's only part of it is P1 recording the values.
- ~~**Spawn** (§7.2) stays `claude --dangerously-skip-permissions --plugin-dir …` with no model flags.~~ **Superseded by U20:** spawn takes optional `--model` / `--effort`, and the lead is reminded to choose them. U18's self-relay and 切換 parts stand.

**Ninth supplementary decision.** The user made it on 2026-10-07, and `air26/_9iwyyv` relayed it (final version, after a first draft was withdrawn for the 攔截／分流 discussion). Copied verbatim.

| # | 決策 |
|---|---|
| U19 | 不存在「把某個 session 送上手機」，只有「用手機檢視 session」。手機是和 Mac App 對等的另一個 client，只是多一個操作介面，絕不改動 desktop 中 terminal 的作業習慣。worker 或 terminal session 觸發的 AskUserQuestion、權限詢問，都給所有 client，任一邊回答後全部關閉，和 U6 一樣。採「分流」：終端機照常出現原生選擇框，其他 client 顯示事件卡；自由填寫（「其他」輸入欄）第一版就要支援。<br>iOS 端的兩種呈現（供你理解，手機 API 不在本 spec）：<br>- 終端模式：ask 出現時顯示虛擬鍵盤（↑↓←→ Enter Esc Tab，加輸入欄），原樣送出按鍵。<br>- 對話模式：顯示事件卡，由 daemon 代理把「選第 N 項」「自由填寫：xxx」轉成實際操作。<br>請改：<br>(a) 拿掉每個 session 的轉送開關。<br>(b) Mac App 不需要另外做卡片，終端機本身就是它的回答介面。<br>(c) 用 hook 擋住、等決定的做法，只留給 lead 申請與接力的硬鎖（旗標檔機制）。AskUserQuestion 和權限詢問改用分流。<br>(d) 「一邊答了，另一邊關閉」的訊號，用 PostToolUse（或 mod 的對應事件）偵測已回答。<br>(e) 重新評估 P8 的內容與位置（含你準備好的 P8a/P8b 拆分）。<br>【重點】分流的實作機制有兩條，請先探查再定，不要直接選 send-keys：1. 優先探查 Purdex mod（tool.call(AskUserQuestion)＋$.ui.ask／session.receive 控制通道或 pdx 長輪詢，誰先到就回傳 {result}）…；2. 備案：settings hook 只回報、daemon 把意圖轉成按鍵送出（Collie 式防呆）。探查結果和你的選擇請回報給我，選定後再改 spec。不影響 U1–U18。 |

**How this spec reads U19** (the probes are M24; `air26/_9iwyyv` approved the choice on 2026-10-07 and added five points, all folded into §6.6):
- **Mechanism 1, the Purdex mod, is chosen; there is no send-keys fallback.** A `tool.call` hook on `AskUserQuestion` calls `next(e)` without awaiting it, so the engine's own dialog is drawn byte for byte; at the same time it waits for the daemon's answer. Whichever comes first wins: the terminal's answer flows out as the tool's result and the mod tells the daemon; a remote answer makes the hook return `{ result }`, which **closes the native dialog at once** (M24).
- (a) No per-session switch. (b) The Mac App shows no card: the terminal it displays is the answer surface. (c) The flag file is only for the lead and relay hard locks; AskUserQuestion and permission prompts never hold. (d) The "answered on one side, close the other" signal is the mod's own report when `next(e)` resolves; the settings-hook `PostToolUse` report is the backstop for sessions without the mod. (e) P8 is split into **P8a** (daemon kinds + the mod's AskUserQuestion path) and **P8b** (the mod's permission path, deferred until the iOS line needs it) (§12).
- **air26's five points (2026-10-07):** (1) "no client connected" becomes "no remote responder": a connected WS client **or a device registered for push**, behind one daemon interface, with only the WS half implemented in P8a; (2) `$.process.run` is capped at ten minutes, so the mod's wait is a loop of bounded long-polls; (3) the permission path leaves no trace in the terminal or the transcript the model reads (measured), and P8b is deferred because the user runs bypass mode; (4) a session without the mod degrades to a **terminal-only** card; (5) a near-simultaneous answer is decided for the terminal, and the phone's card says so.

**Tenth supplementary decision.** The user made it on 2026-10-07 directly to `mlab/_7wcg1d` (purdex-f0), then chose the shape from three options (提醒＋可指定, over 強制指定 and 提醒＋主機預設). The user's words are copied verbatim; the chosen shape follows them.

| # | 決策 |
|---|---|
| U20 | 我發現現在 claude code cli 的預設 model 不是一定的，會不穩定。調整為，啟用為 lead 時，提醒他按照需求指定 member 的模型呼叫。<br>選定形狀（提醒＋可指定）：<br>- 核准成為 lead 時印一行提醒，skill 也寫；<br>- `pdx spawn` 新增 `--model`（與 `--effort`）；<br>- 沒帶 `--model` 照樣用預設模型開，但 spawn 再提醒一次。 |

**How this spec reads U20** (M25; it replaces U18's spawn bullet and nothing else of U18):
- **(a) Spawn takes the flags.** `pdx spawn [--model <m>] [--effort <e>]` (§7.2). The daemon appends them to the launch command after `team.member_command`. `--model` must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(\[1m\])?$` and is single-quoted in the literal send (the `[1m]` suffix would glob); `--effort` must be one of M25's five levels. The CLI checks both first (exit 2); the daemon checks again and answers `400 bad_request`. Neither flag is required.
- **(b) The reminder at activation.** On approval `pdx lead request` keeps the grant JSON alone on stdout and prints on stderr: `已成為 lead。預設模型不固定：spawn member 時請依工作需求用 --model 指定（例：--model sonnet 做機械性修改、--model opus 做設計）。` (§6.1 step 4). It ships with spawn in P4, so it never names a command that does not exist yet.
- **(c) The reminder at spawn.** Without `--model`, `pdx spawn` still opens the member and prints on stderr: `提醒：沒有指定 --model，member 會用這台主機當下的預設模型。` The exit code is unchanged.
- **(d) The skill** (§10, "As a lead") says to choose each member's model and effort for its task, and why: the host default is not fixed.
- **(e) The lead sees what each member runs.** `pdx team` shows each member's model and effort from P1's statusline reading (§7.3), so a member that came up on an unexpected model is visible.
- **(f) Across hosts and relays.** P4c forwards `model` / `effort` with the spawn to the member host. A member relay (P6) keeps them, because `/clear` does (U18 (a), M21).

**How this spec reads U15** (derived in §7.4; the measurements are M13–M16):
- U15 pulls **cross-host spawn, kill and relay** out of §11 into the phases (P4b, P4c in §12). It changes none of U1–U14.
- "有相關的 repo" is decided by both the path convention and the git remote, and a docs-only sparse checkout does not count (§7.4 (a)).
- "weekly usage" is the statusline's `rate_limits.seven_day`, which is **per account**. Every host that is signed in to the same account reports the same number, so rule 2 can only separate hosts with different accounts; otherwise it is a tie and rule 3 decides (§7.4 (b), M14).
- "優先在 mlab" is the host config `team.preferred_host`, whose default is the host named `mlab`.

## 3. Facts

### 3.1 Prototype (air26, Claude Code 2.1.291, Opus 5.5 1M; brief §3)

- **F1** The whole loop works:
  1. at the threshold, `prompt.submit` asks the agent to write the handoff;
  2. `turn.complete` checks the file;
  3. `$.command.run({command:'clear'})`;
  4. `classic.SessionStart` with `source==='clear'`;
  5. `prompt.submit` sends the takeover prompt;
  6. the new conversation restates the handoff and checks it with git.

  From trigger to the end of the takeover turn: about 40 s.
- **F2** After `/clear`: same pid, same tmux pane, the pdx name is kept, **the ref changes** (it derives from the sessionId).
- **F3** `prompt.submit` and `command.run` are refused inside a `command.run` hook. Sending them from `$.clock.after(100, …)` works. The mod's own `session.start` does not fire again after `/clear`, so the takeover hooks `classic.SessionStart`.
- **F4** An empty session starts at about 60K tokens here, about 6% of a 1M window.
- **F5** The handoff carried verbal decisions, open questions, the venv path and dead ends correctly.

### 3.2 Measured for this spec (2026-10-06, Claude Code 2.1.291; labelled M to keep them apart from phases)

A probe mod was loaded into a throwaway `claude` in tmux through `CLAUDE_CODE_PLUGIN_DIRS`.

- **M1 A pdx message can be a private control channel to a mod.**
  - `pdx msg send` to the session raised `session.receive` with `origin = {"kind":"peer"}`. It is not `peer-send-message`, so a matcher must not key on that. The text was the `<cross-session-message from=… from-name=…>` envelope.
  - Returning `{consumed}` kept it out of the model: the transcript holds no trace of it.
  - From a `$.clock.after` timer, the mod then:
    1. called `$.prompt.submit` (the turn ran);
    2. called `$.command.run({command:'clear'})` from `turn.complete`;
    3. saw `classic.SessionStart{source:'clear'}` with a new session id.
  - The ref changed (`_vstjse` → `_y6vgm3`), and the name was kept.
- **M2 `CLAUDE_CODE_PLUGIN_DIRS` loads a plugin into a tmux-launched interactive `claude`**, the same as `--plugin-dir`. It can also be set in the `env` block of `~/.claude/settings.json` (Claude Code plugin docs).
- **M3 After a `/clear`, the old ref is gone.** `pdx msg send mlab/_vstjse` answers `peer_not_found`. Its hint still says a ref "never changes", which is false after `/clear`.
- **M4 The CC registry's `cwd` follows `EnterWorktree`.** `~/.claude/sessions/45325.json` reads the worktree path, while `pdx peers` shows `/Users/wake` for the same session.
- **M5 The same plugin folder given twice loads once.** `CLAUDE_CODE_PLUGIN_DIRS=X` together with `--plugin-dir X` raised `session.start` once.
- **M6 An ad-hoc signed binary can use the Secure Enclave without any entitlement.**
  - Probe: a `swiftc` build, signature `adhoc,linker-signed`, no Team ID, no entitlements.
  - It created a CryptoKit `SecureEnclave.P256.Signing.PrivateKey`, signed and verified, and reloaded the key from its 284-byte `dataRepresentation` blob.
  - The blob is a file the program keeps. Nothing goes into the keychain.
- **M7 A user-presence key could not be created on mlab.** Creating one with `[.privateKeyUsage, .userPresence]` failed with `-25308` (`errSecInteractionNotAllowed`, AKS `-536870174`), both from tmux and from a `gui/501` LaunchAgent.
  - mlab's console is locked (`CGSSessionScreenIsLocked=Yes`), which explains it.
  - **Creation on an unlocked workstation is not measured yet.** Measure it if the §11 hardening is taken up.
- **M8 Origins the UI runs on:**
  - **Purdex.app** loads `app://./index.html`: a custom secure scheme whose host is `.` (`electron/main.ts:24-27`, `electron/window-manager.ts:81`). When the dev server answers, it loads `http://100.64.0.2:5174` instead (`window-manager.ts:78`).
  - **The browser SPA** is the Vite dev server `http://100.64.0.2:5174`.
  - **`https://purdex.mlab.host`** proxies to mlab's daemon API only, and `GET /` answers 401. No SPA is served over https today; the web version is unmerged.
- **M9 a19 has Touch ID.** `MacBookAir8,1`, Apple T2 chip, `bioutil` reports biometrics on for unlock, macOS 14.8.9.
- **M10 Electron's Touch ID WebAuthn needs a real signing identity.** `app.configureWebAuthn({ touchID: { keychainAccessGroup } })` exists, but Chromium's Touch ID authenticator requires the `keychain-access-groups` entitlement and a matching provisioning profile (Electron docs).
  - Purdex.app is ad-hoc signed; the signing roadmap's Apple Developer stage is not done.
  - Keychain items created without that entitlement are reported to fail with `-34018` (Apple developer forums; not measured here).
- **M11 Every new turn passes `prompt.submit`, and a hook can hold it past 10 s.** Probe mod, idle session; prompts whose text held `HOLD15` were held:
  - **A peer message** (`pdx msg send`) raised `session.receive`, then `prompt.submit` with `origin {"kind":"peer"}`.
  - **A typed prompt** raised `prompt.submit` with `origin {"kind":"composer"}`.
  - In both cases the hook awaited `$.process.run(['/bin/sleep','15'])`, was released 15 s later, and only then did `turn.start` fire. The 10 s hook budget did not cut it.
  - While held, the typed prompt shows as sent, with the busy spinner.
- **M12 A plugin's re-submitted prompt is not the original** (2.1.291 types, `PromptSubmitArgs.asUser`).
  - `asUser: true` removes the "The <plugin> plugin sent a message" frame.
  - But "`@file` mentions and pasted images are not expanded for a plugin's prompt, `asUser` or not", and the transcript still names the plugin.

- **M13 The statusline payload carries `rate_limits` from its second refresh on** (2026-10-07, Claude Code 2.1.291, mlab, Haiku, a one-word turn). The first refresh has no `rate_limits` key and a null `used_percentage`. After the first API response the payload has:
  - `rate_limits.five_hour {used_percentage: 14, resets_at: 1791318000}` and `rate_limits.seven_day {used_percentage: 4, resets_at: 1791471600}`; `resets_at` is unix seconds;
  - also new against M-series probes of 2026-10-06: `prompt_cache`, `prompt_id`.
  - Top-level keys: `context_window, cost, cwd, exceeds_200k_tokens, fast_mode, model, output_style, prompt_cache, prompt_id, rate_limits, scratchpad_dir, session_id, thinking, transcript_path, version, workspace`.
- **M14 mlab and a26 are signed in to the same Claude account** (`~/.claude.json` `oauthAccount.emailAddress`, both `wake@protype.tw`; both run Claude Code 2.1.291). Rate limits are per account, so the two hosts report the same `seven_day` figure.
- **M15 Repo inventory under `~/Workspace/{org}/{repo}`** (2026-10-07):
  - mlab: 47 git checkouts, none sparse. Scanning all 47 for `remote.origin.url` and `core.sparseCheckout` takes 0.76 s wall.
  - a26: 10 git checkouts across orgs `ntsu, protype, tangency, wake`. One is sparse: `ntsu/istdc`, cone mode, `git sparse-checkout list` prints only `docs`. Its index still lists 1697 files (1484 outside `docs/`), so `git ls-files` cannot tell a docs-only checkout apart; `git sparse-checkout list` can.
  - `purdex` is not checked out on a26 at all.
  - Remotes are a mix of `ssh://git@lab.protype.tw:9079/<Org>/<repo>.git`, `git@github.com:wake/purdex.git` and `https://github.com/...`. Org case differs between the path (`ntsu`) and the remote (`NTSU`).
- **M16 Paired-daemon trust today:** see §7.4 (c), which cites the code.

- **M17 Hooks today** (origin/main `fb9fcbd8`): `pdx hook --agent <cc|codex> <PdxEvent>` reads stdin with `io.ReadAll` and no timeout, POSTs `/api/agent/event` with a 2 s client timeout, never reads the body, never writes stdout, swallows every failure and exits 0 (`cmd/pdx/hook.go:45,86,136-139,170-179`); the only non-zero exit is a missing `--agent` (`:69-70`). CC has **12** hooks installed with **no `matcher` and no `timeout`** (`internal/agent/cc/events.go:25-245`, `hooks.go:213-221`), PreToolUse and PermissionRequest among them; Codex has **10** in `~/.codex/hooks.json` with `"timeout": 5` (3 for SessionEnd and Interrupt; `internal/agent/codex/hooks.go:20-32,327-335`). Ours are recognised by argv shape, not a marker (`cc/hooks.go:333-348`). The daemon's `handleEvent` answers `{"status":"ok"}` at once; CC PreToolUse is observe-only (`internal/module/agent/handler.go:238,272-293,412-454`). There is no per-session flag or lock file under the data dir, and no long-poll in the daemon.
- **M18 Claude Code hooks reference** (code.claude.com/docs/en/hooks, read 2026-10-07): PreToolUse answers `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow|deny|ask|defer","permissionDecisionReason","updatedInput","additionalContext"}}`; several hooks combine as `deny > defer > ask > allow`; `defer` is honoured only in `-p`. PermissionRequest answers `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow|deny","updatedInput","updatedPermissions","message","interrupt"}}}`; "a hook that exits 2 without a `decision` object leaves the permission flow unchanged"; in a session that cannot show a prompt, "if no hook returns a decision, it denies the tool call"; with a permission host, "whichever decides first applies". Default timeout **600 s** for command hooks on these events (30 s on UserPromptSubmit, 10 s on MessageDisplay), per-hook `timeout` configurable; all matching hooks run in parallel and Claude waits; a timed-out PreToolUse hook does not block the call; exit 0 with empty stdout = no decision.
- **M19 AskUserQuestion is answered by a PreToolUse hook in an interactive session** (2026-10-07, CC 2.1.291, Haiku, bypass permissions, tmux). A `PreToolUse` hook with matcher `AskUserQuestion` printed `permissionDecision: "allow"` and `updatedInput` = the original `tool_input` plus `answers: {"<question>": "<first option label>"}`; the TUI showed `User answered Claude's questions: 喜歡紅色還是藍色？ → 紅` with no dialog, and the model replied with the answer. The docs describe this for `-p` runs; it works interactively too. The hook's stdin carried `permission_mode: "bypassPermissions"` and `effort: null`.
- **M20 Codex hooks** (learn.chatgpt.com/docs/hooks, read 2026-10-07): events include PreToolUse and PermissionRequest; both take stdout decisions with the **same JSON shapes** as Claude Code; config in `~/.codex/hooks.json` or `config.toml`; default timeout 600 s (1 s for SessionEnd and Interrupt); Codex waits unless `async: true`; payload fields `session_id`, `tool_name`, `tool_input`, `tool_use_id`, `permission_mode`, `turn_id` (matches the repo's fixtures, `internal/agent/codex/testdata/codex-0.153.4-payloads/`). **Unmeasured:** whether a changed command or `timeout` invalidates Codex's `[hooks.state] trusted_hash` and re-prompts the user; the plan probes it in P8.
- **M21 Model and effort live in the statusline, not in hooks** (2026-10-07, CC 2.1.291): the statusline payload carries `model.id` and `effort.level` (`claude-opus-5-5` / `xhigh` at start; `/model sonnet` → `claude-sonnet-5-5` / `medium`; `/effort low` → `low`); the UserPromptSubmit hook payload had `effort: null` and no `model`. After `/clear` the new session id kept **both** (`claude-sonnet-5-5` / `low`), same pid. (Used by U18.)

- **M22 Nothing carries model or effort across a switch today** (Nexen HEAD `3411bd1`; Purdex pins `nexen v0.17.0`, same argv): the worker argv is `<Bin> -p --input-format stream-json --output-format stream-json --verbose --include-hook-events --include-partial-messages [--resume <sid>] --setting-sources user,project,local --permission-mode bypassPermissions --tools default` (`nexen/adapter/claude.go:156-200`, `sandbox/profile.go:228-239`; env is `PATH HOME SHELL TMPDIR LANG` plus `CLAUDE_CODE_FORWARD_SUBAGENT_TEXT=1`, `account/env.go:15-73`). `execution.Request` (`execution/service.go:55-105`) and `adapter.StartParams` (`adapter/adapter.go:94-103`) have no model or effort; Purdex calls `Delegate(ctx, execution.Request{…})` in-process (`internal/module/nex/handoff.go:253-269`) and knows only `handoffOwner{SessionID, Cwd, TmuxPaneID}` (`handoff_steps.go:48-53`). Worker → terminal types the SPA's resume template, default `claude --resume {id}` (`takeback.go:372-373`, `spa/src/lib/resume-templates.ts:21`). The CC status derivation reads the model from a key `modelName` that only the opencode plugin sends (`internal/agent/cc/status.go:21,78`, `opencode/plugin_template.go:176`); `effort` is parsed nowhere. Settings: mlab `model: opus`, `effortLevel: high`; a26 `model` unset, `effortLevel: high`.

- **M23 The daemon knows whether any client is connected** (origin/main, 2026-10-07): `EventsBroadcaster.HasSubscribers()` (`internal/core/events.go`) reports whether the `/ws/host-events` subscriber set is non-empty. The set holds only WebSocket clients (and test subscribers); no daemon module subscribes to its own broadcaster. The SPA opens one socket per host it shows, so an App or a phone viewing this host from anywhere is a subscriber of this daemon. A phone in the background holds no socket (iOS suspends it), which is why U19's rule also counts a device registered for push.
- **M24 分流 probes** (2026-10-07, CC 2.1.291, Haiku, tmux; report `scratchpad/u19probe/REPORT.md` of session `mlab/_81nu3d`, five mods P-A…P-E):
  - **P-A** A `tool.call` hook on `AskUserQuestion` that returns `{ result: { questions, answers } }` without `next` is accepted by the output schema; the transcript row is `User answered Claude's questions: · 紅還是藍？ → 紅`, identical to a native answer, and the model reads the same text. A free-text answer not among the options and a comma-joined multi-select pass too.
  - **P-B** `const native = next(e)` **not awaited** draws the engine's own dialog unchanged; the hook meanwhile waits inside `$.process.run` (the hook's own 10 s budget does not run while a `$` call is in flight: `next.budget.remainingMs` stayed 10000 across a 26 s wait). Terminal first ⇒ the native result. Remote first ⇒ the hook returns `{ result }` and **the native dialog disappears at once**; the turn continues with the remote answer; later keys only type into the composer. Nothing else closes it: `$.ui.invalidate` redraws, `$.ui.focus` answers `not this plugin's site`, a hook that does not return keeps it open. A remote answer delivered as a peer message (`session.receive`, `{consumed}`) works the same.
  - **P-C** A mod-drawn pane works (hotkeys, Enter, Esc, free text, several questions, multi-select, remote first) but does not look native (round border, `1: 紅` not `❯ 1. 紅`, the spinner keeps turning) and element handles went stale twice. **P-D** `ui.render` on the `AskUserQuestion` site fires, but a tree may only add content *above* the engine's node and keys never reach the mod's buttons. Both rejected.
  - **P-E** `tool.check` returning `ask` shows the native permission prompt (the hook's `reason` is not drawn); holding inside `tool.check` shows only `Waiting…`. **The workable shape is `tool.call`:** its `next(e)` contains the prompt and the run; remote allow first ⇒ the hook re-issues the call with `$.tool.call` and a `tool.check` hook that answers `allow` ⇒ the prompt closes and the tool runs; remote deny ⇒ `{ deny }` closes the prompt; terminal "Yes" first ⇒ native wins. The abandoned native `next(e)` settled as `The user doesn't want to proceed…` and was discarded: **the terminal showed only the Bash row and its result, and the model did not read it** (capture `pe/cap-e4-02-after-allow.txt`).
  - **Limits read from the 2.1.291 types:** `$.process.run` `timeoutMs` defaults to 30 s and is **ten minutes at most**; `HookBudget` counts a hook's own time (10 s) and stops while a `next(e)` or `$` call is in flight. **Unmeasured:** a hold of hours (the plan for P8a measures one).
- **M25 Launch flags for model and effort** (2026-10-07, `claude --help`, CC 2.1.292): `--model <model>` takes an alias for the latest model (`fable`, `opus`, `sonnet`) or a model's full name; `--effort <level>` takes `low`, `medium`, `high`, `xhigh` or `max`. Both apply to the session they start, so a member launched with them runs that model and effort whatever the host's settings say. (Used by U20.)
- **M26 The hours-long 分流 hold** — reserved for plan v2 Task 8a.10, measured after P8a-2 is deployed.
- **M27 A never-opened cwd does not stop a launch** (2026-10-07, CC 2.1.292, tmux): `claude --dangerously-skip-permissions --model haiku` started in a freshly created directory under `/private/tmp` showed the prompt within 2 s, with no trust dialog and no bypass dialog; `~/.claude/sessions/<pid>.json` was written and the statusline showed Haiku 4.5 (so `--model` took effect). Spawn (§7.2 step 5) therefore needs no dialog handling. (Used by P4-5.)
- **M28 `session.receive` fires during a running turn** (2026-10-08, CC 2.1.292, Haiku, a probe mod logging hook times): a foreground `ping -c 45 127.0.0.1` Bash call ran 16:16:09 → 16:16:56 UTC; a `pdx msg send` at 16:16:17 fired `session.receive` (origin `{kind: "peer"}`) at 16:16:18.07, with the tool still running; the turn completed at 16:16:58. A message arriving between tool calls mid-turn fires it too. So a member's mod sees the relay control message within about a second, busy or idle; the claim itself still waits for `turn.complete` (§8.2). (Used by P6-4: the claim timeout counts from the request.) Note: Claude Code blocks a standalone `sleep 45` in Bash (the model then backgrounds it), so a long foreground probe needs another command.

### 3.3 Code (re-verified on origin/main `de37a4e5`, alpha.505)

The brief's §4 is mostly right. **Corrections** are marked ✱.

**Context usage**
- ✱ The statusline snapshot is **raw JSON, never parsed**: `json.RawMessage` (`internal/module/agent/handler.go:1231-1234`).
  - Its map is keyed by **pdx session code**, resolved from the tmux session *name* (`handler.go:1273`, `:1280`). Two CC panes in one tmux session overwrite each other.
  - It lives in memory only, and is not cleared when a session dies (`handler.go:1228-1230`, `:978-983`).
  - The payload carries `session_id`, `cwd`, `workspace.current_dir`, and `context_window.{used_percentage, context_window_size, …}`. `used_percentage` can be null early in a session.
  - Nothing reads the percentage: the SPA keeps `ccStatus` only for the statusline self-test.
- It is posted on every CC statusline refresh: a new assistant message or a mode change, with a 300 ms debounce. So an idle session does not refresh.

**Peers**
- `pdx peers` has no context column. `--json` exists and prints the `/api/peers` envelope (`cmd/pdx/peers.go:432-438`).
- **CWD bug confirmed.** `internal/tmux/executor.go:198` (`session_path`) → `internal/peers/record.go:173` `rec.Cwd = s.Cwd`.
  - `entry` rows already use the registry cwd (`record.go:344`).
  - The session-row branch ignores both the registry cwd and `owner.Cwd`, although both are in hand (`record.go:231-252`).
- **Ref** = `"_" + base36(FNV-1a-64(sessionId) mod 36^6)` (`internal/peers/ref.go:111-124`). There is no alias or predecessor anywhere.
- **Title** is stored per session id (`internal/store/peer_label.go:74-75`), so a `/clear` loses it today.
- **Delivery** goes to the registry's `messagingSocketPath`, which is per pid (`internal/peers/registry.go:42`). A `/clear` keeps the inbox; only the session id and ref change.
- **Resolution runs on the sending daemon** over the target host's rows, fetched from its `/api/peers` (`internal/module/peers/send.go:350-378`). So a field on the rows reaches remote senders with no new endpoint.
- The daemon has **no internal send API**: `deliverLocal` is HTTP-bound (`send_local.go:47-58`). The parts exist:
  - `ccuds.BuildFrame` / `WriteFrame` (`internal/peers/ccuds/frame.go:36,163`);
  - `ccuds.StartVirtualPeer` for a daemon-owned reply address (`virtual_peer.go:102`).

**Hooks and frames**
- ✱ `pdx hook` is fire-and-forget (`cmd/pdx/hook.go:86`). The 2 s is only the HTTP timeout: the tmux and `ps` steps before it have none.
  - There is no spool, queue or replay anywhere. Events during a daemon outage are lost.
  - The daemon is unreachable from the old image's listener close until the new image listens (`cmd/pdx/main.go:271-306`).
- ✱ `PreToolUse` **is** installed, with no matcher and no timeout, so it runs synchronously on every tool call (`internal/agent/cc/hooks.go:213-222`). It is observe-only: it never writes a decision.
- `/clear` gives `SessionEnd{reason:clear}` (old id), then `SessionStart{source:clear}` (new id). The pane's frame is replaced with a new `frame_id` and `session_id` (`internal/module/agent/frame_ops.go:136-173`).
- `SubscribeSessionStart` exists. Its only subscriber is nex's manual-resume (`internal/module/nex/manual_resume.go:66`).

**Sessions and launch**
- `POST /api/sessions` takes only `{name, cwd, mode}`. A duplicate name is **409**, never reused (`internal/module/session/handler.go:158-159`).
- `send-keys` with `expected_tmux_instance` sends bytes literally, behind a generation check (`internal/tmux/send_keys_conditional.go:68-100`).
- The only daemon-side "create tmux + start CC + wait" is nex take-to-terminal:
  - it waits for CC with `waitForCC`, 15 s;
  - then for a verified frame with `afterResume`, 3 s;
  - on timeout it kills the session (`internal/module/nex/handoff_steps.go:102-113`, `recent_resume.go:16-96`, `take_to_terminal.go:285-295`).
- `cld-yolo` is the user's shell alias for `claude --dangerously-skip-permissions`. The daemon never names it.
- **No worktree API** anywhere. A session's `cwd` must already exist (`internal/module/session/cwd.go:44-50`).

**Events and UI**
- `EventsBroadcaster.BroadcastEvent` sends `HostEvent{type, session, value string}` to every subscriber (`internal/core/events.go:14-22, 172-185`):
  - non-blocking, 64-deep buffers, **drop when full**, no replay;
  - `OnSubscribe` gives a module a snapshot hook per new subscriber (`events.go:216-250`).
- The SPA connects to every host (`spa/src/hooks/useMultiHostEventWs.ts:130`) and ignores unknown types (`:172-234`).
- **No precedent** for "daemon holds a request, any client answers, the rest close". The closest:
  - the store-driven singleton dialog `HandoffDialogHost`;
  - the per-nonce wait of the statusline self-test.
- Electron notifications are title + body only, with no action buttons (`electron/main.ts:157-186`).

**Restart and auth**
- `boot_id` is on `/api/health`, which needs no auth (`internal/core/info_handler.go:27`). Restart re-execs in place and keeps the pid.
- ✱ Only `POST /api/daemon/restart` answers `503 shutting_down`. Other endpoints serve during the drain and then refuse connections. `peers send` answers `503 not_ready` while stopping (`internal/module/peers/send.go:203`).
- ✱ During a restart the session module **removes the global tmux hooks** and reinstalls them on start (`internal/module/session/module.go:315-330`, `:195`).
- **One credential.** `TokenAuth` accepts the single host token, or a one-time WS ticket (`internal/middleware/middleware.go:71-86`). The SPA and every `pdx` command use the same token, from `~/.config/pdx/config.toml`.

**CLI and storage**
- The CLI has no shared client, **no retry** on refused connections, and no long-poll command.
- Exit codes are literals: 0 ok, 1 runtime/API error, 2 usage error.
- `lead`, `spawn`, `kill`, `relay` and `team` are free as top-level commands. `internal/terminal/relay.go` already uses "relay" for terminal WS, so Go identifiers must be qualified.
- SQLite files in `<data_dir>`: `meta.db`, `agent_events.db`, `host_config.db`, `profiles.db`, `backup.db`, `nex/nex.db`.
  - Migrations are per store: `CREATE IF NOT EXISTS` plus column checks.
  - **No table for pending requests or operations exists.**

**Vocabulary**
- `PRODUCT.md`: Role `worker` / `operator` is a row of the §3.5 table (`:75-76`). Operator also appears in §1 (`:15`), Law 2 (`:148`) and §6.1/§6.2 (`:203-209`).
- No `team` / `lead` / `member` concept exists in code. The CC hook `TeammateIdle` is ignored.
- Claude Code's own Agent Teams are experimental: a fixed lead, no nesting, and split panes unsupported in Ghostty (research page, "Session 與多 agent"). This spec does not build on them.

## 4. Vocabulary (U4)

| Term | Meaning |
|---|---|
| **team** | One lead and the members it opened. Lives on the lead's host. |
| **lead** | A Claude Code session the user approved for lead mode (§6). It can spawn, kill and relay its members. |
| **member** | A session a lead spawned (§7). Its relay is the lead's to decide (U9). |
| (none) | Every other session. The default; no word for it in the UI. |

- **Role in PRODUCT.md §3.5** becomes `(none)` / `lead` / `member`. `worker` stays Nexen's word for headless execution (U4).
- **`operator`** becomes **lead** in §1, Law 2's example and §6.1. §6.1 becomes "Lead / team":
  - a lead coordinates members over daemon-level message inject and the `pdx` team commands;
  - entry is by the user's approval.

  §6.2 follows the rename.
- This is one small separate PR (P0), as the brief asked. The §3.5 Mode row (`terminal / stream / agent`) is stale too, but it is not in scope here.

## 5. Architecture

**⟲ changed from D1.** The daemon is still the brain, but **the steps inside a session are executed by a Purdex Claude Code plugin** (the "Purdex mod"), not by send-keys.

**The daemon** (new module `internal/module/team`, own `team.db`) owns:
- lead requests and their decisions;
- teams, grants and members;
- spawn and relay operations, every step persisted;
- session lineage (the ref redirect chain);
- detection of member context usage, and the notices to the lead.

**The Purdex mod** runs inside each interactive Claude Code session. It is the executor for every relay of that session, its own or one its lead started:
- it asks the agent to write the handoff (`$.prompt.submit`, U2 literally);
- it checks the file;
- it clears (`$.command.run('clear')`);
- it seeds the new conversation;
- it reports each step to the daemon through `pdx`.

**Reasons for the change:**
- **M1 proves the mod can be driven by a pdx message the model never sees.**
- **send-keys `/clear` is fragile.** It types into the TUI: whatever is in the input box gets merged, and a stray key in CC's TUI has meanings (Ctrl-C twice exits, Esc-Esc opens rewind). `command.run` was proven in F1 and P1.
- **It matches U2's literal mechanism** for members too. The brief's D5 asked members through `pdx msg`.
- **A relay in flight survives a daemon restart** because the CC process drives it (U12); the daemon only records.
- **Self relay (goal 1) needs the mod anyway.** One executor serves both.

**Shipping.** The plugin (mod + skill) is embedded in the `pdx` binary and extracted to `<data_dir>/cc-plugin/purdex/` (versioned).
- It is loaded through `CLAUDE_CODE_PLUGIN_DIRS` in the `env` block of `~/.claude/settings.json` (M2).
- That entry is merged and removed by the installer that already merges the CC hooks and statusline (`internal/agent/cc/hooks.go`), and appended to any existing list.
- The mod does nothing in a headless session, i.e. a Nexen worker's `claude -p`. Worker relay goes through Nexen rebuild, not `/clear`.

## 6. Lead request and approval (U5, U6, U7, U12)

### 6.1 CLI

```
pdx lead request --reason <text> [--max-members N] [--root <dir>]... [--wait 9m]
```

1. pdx generates the request id (UUID v4, the idempotency key). It prints one stderr line: `申請 lead 中（<id>），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）`.
2. `POST /api/team/approvals {id, kind:"lead", origin_inbox, reason, max_members, roots, wait_s}`.
3. Long-poll `GET /api/team/approvals/{id}?wait=25` until the request is closed. **Each poll renews the request's lease.**
4. On approval it prints the grant on stdout (team id, max members, roots) and exits. **(U20, from P4)** It also prints one stderr line reminding the new lead to choose each member's model: `已成為 lead。預設模型不固定：spawn member 時請依工作需求用 --model 指定（例：--model sonnet 做機械性修改、--model opus 做設計）。` stdout stays the grant JSON alone.
5. On SIGINT or SIGTERM it sends `DELETE /api/team/approvals/{id}` (best effort), then exits 12.

Defaults:
- `--max-members` 3, cap 8;
- `--root` is the caller's cwd;
- `--wait` 9 min, so it stays under the Bash tool's 10 min maximum (D3).

### 6.2 Daemon

**⟲ derived (U13 (b)): one model for every approval.** Two kinds of request share one table, one state machine, one event (`approval.request`) and one dialog host:
- lead requests (this section);
- self-relay requests (§8.7).

The table is `approval_requests{id, kind: lead | self_relay, origin_session_id, payload, state, deadline_at, lease_until, decided_by, decided_at}`. What follows is the `lead` kind; `self_relay` differs only in payload and grant.

**Create** (idempotent on `id`):
- The origin must be a live, deliverable CC session on this host. The existing `findOrigin` attributes it by inbox.
- Refused with **409**:
  - `already_lead`;
  - `member_cannot_lead` — no nested teams in v1;
  - `request_open`, carrying the open request's id.
- The row is stored with:
  - `state=open`;
  - an absolute `deadline_at` = now + `wait_s`, capped at 10 min;
  - `lease_until` = now + 30 s.
- Broadcasts host event `approval.request` `{op:"opened", approval}` (the field is named `approval` because the wire type is `team.Approval`; the plan's daemon, CLI and SPA all use it).

**Close.** Exactly one of these wins, by compare-and-set on `state=open`:

| Close | When | State |
|---|---|---|
| A client decides | `POST …/{id}/decide {decision, grant, client}`, one click (U5b) | `approved` / `denied` |
| Deadline passes | sweeper | `timeout` (U7: counts as a denial) |
| The requester gives up | `DELETE`, or the lease expires, or the origin session is gone | `cancelled` / `abandoned` |

- Every close broadcasts `{op:"closed", approval}`, carrying `decided_by` and `decided_at`.
- A late `decide` gets **409 `already_decided`** with the closed request, so its client can say who handled it.
- Approval creates the team (§7.1) in the same transaction.

**Snapshot.** The module registers `OnSubscribe` and sends every open request to each new subscriber as one event `{op:"snapshot", approvals:[…]}` (`[]` when none). Late or reconnecting clients see the same set (D2).

### 6.3 SPA and App (U6)

- A global `ApprovalDialogHost`, next to `HandoffDialogHost`, driven by a store keyed `hostId + requestId`. It is fed by a new `approval.request` branch in `useMultiHostEventWs` and by the snapshot. The dialog body depends on the kind; this section describes `lead`, and §8.7 describes `self_relay`.
- The dialog shows:
  - host;
  - the session's title or name, address and ref, cwd and tmux session;
  - the reason;
  - a countdown to the deadline.

  The user can edit the grant: max members, and the allowed roots (default the requested ones). Buttons: **核准** / **拒絕**.
- **核准** and **拒絕** are one click each, on any Purdex.app (U5b).
- Several requests queue, one dialog at a time, oldest first.
- **Closed elsewhere:** the dialog closes everywhere, and other clients get a toast `<主機>：<session> 的 <lead 申請／接力申請> 已由 <client> 核准／拒絕` (U6).
- **Notification:**
  - Electron raises a system notification through the existing `showNotification` path (`<主機>：<session> 申請成為 lead`); clicking it focuses the window, where the dialog already is.
- **During a daemon restart (D6):** the dialog stays, with its buttons disabled and `daemon 重啟中…`.
  - A click while disconnected is kept locally and re-sent on reconnect. CAS makes the resend safe.
  - A 409 then closes it with the "handled by" toast.

### 6.4 The soft lock (U7, D3)

This section covers lead requests, which the agent makes through Bash. A self-relay request is made by the mod, which holds the session itself (§8.7).

`pdx lead request` blocks the session's turn on one Bash call. The skill (§10) requires the call to run:
- in the foreground;
- with `timeout: 600000`;
- never in the background.

What happens when that is not followed:

| The model … | Result |
|---|---|
| uses the default 2 min Bash timeout | The Bash tool kills pdx. pdx cancels on the signal; on a SIGKILL the lease expires within 30 s. Either way the dialog closes on every client, and the session got no approval. |
| backgrounds the call | Until P2c ships, nothing stops it. **From P2c (U17), the hard lock of §6.6 denies every tool call of that session while its request is open**, so a backgrounded `pdx lead request` leaves the model unable to do anything but wait. |

### 6.5 Who can approve (U5, U5b)

**One click on any Purdex.app, for both kinds (U5b, U13a).**
- **核准** and **拒絕** are one click each. There is no signature and no Touch ID.
- Any App's answer closes the prompt on every App (U6).

**Why that is enough** (U5b's reasoning, recorded): a cld-yolo session can already open tmux and run `claude` through Bash. Lead mode gives an agent no new capability; the approval means "told and agreed", not a security boundary.

**The layer that stays** (from U5a, kept by U5b):
- `pdx` has no approve command, and the skill forbids self-approval (§10).
- `decide` carries a `client` descriptor `{kind:"app", label}`, e.g. `Purdex.app @ air26`, and the daemon adds the remote address. These are audit labels, not a boundary.
- Every decision is broadcast and shown on every App (`已由 Purdex.app @ air26 核准`).
- Every decision is audited: in the request's `decided_by` / `decided_at` columns, and in one daemon log line per decision.

**What this protects, and what it does not:**
- **Protected:** an agent cannot approve through any path Purdex offers. An approval it fakes shows on every App, with the client label and address.
- **Not protected:**
  - One host token is shared by the SPA and `pdx` (§3.3), so a same-uid agent can call `decide` itself.
  - It can also rewrite `team.db`, or run tmux and `claude` without Purdex at all.

  The broadcast and the audit are what remain. Hardening with human presence is in §11.

**History.** A human-presence design for U5a was written on 2026-10-06: a Secure Enclave approver key per Mac in Purdex.app (this spec at commit `b9a6f239`, §6.5). U5b withdrew it the same day. The facts it rested on stay in §3.2 (M6–M10) for that hardening.

### 6.6 Hook decisions: the hard lock and forwarded prompts (U17)

**⟲ moved from §11.** `pdx hook` learns to **wait for the daemon's answer and print it** for exactly two events, `PreToolUse` and `PermissionRequest`, for both Claude Code and Codex. Every other event stays fire-and-forget (M17).

**The gate: a flag file, else nothing changes.**
- `<data_dir>/hooklocks/<agent>/<session_id>` exists ⇒ the hook asks the daemon. Otherwise it posts the event as today and exits 0 with no stdout, so a session with nothing pending pays no round trip beyond the existing one.
- Who writes the flag: `pdx lead request` while its request is open (removed on close, best effort); the Purdex mod while a relay op runs on that session (§8). **Only those two** (U19 (c)). The team sweeper deletes flags whose session is gone (registry), so a stale flag costs one answered `{}` and then disappears.
- **AskUserQuestion and permission prompts never go through this hold.** They are 分流 (below): the native dialog stays, the Purdex mod races it against the daemon.
- **An unreachable daemon, a 404, or any error ⇒ exit 0, no stdout.** That is the normal permission flow (M18: empty stdout = no decision), never a forced allow and never a block. The hook's client is the restart-aware one (§9.1) with a **5 s** grace here, not 30 s: a session must not stall on a daemon restart.

**The daemon:** `POST /api/hooks/decide {agent, event, session_id, tool_name, tool_input, tool_use_id, permission_mode, raw}` answers one of:

| Case | Answer | Where it comes from |
|---|---|---|
| The session has an **open lead request** (§6.2) | PreToolUse: `deny`, reason `lead 申請等待核准中（<id>），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理`. PermissionRequest: `{}` (the PreToolUse deny already stopped the call). | `approval_requests` by `origin_session_id`, in memory |
| The session is in a **relay op** past `claimed` (§8.1) and the tool is not the handoff write | PreToolUse: `deny`, reason `接力進行中，這一輪只寫接力檔`; a `Write` to exactly `<data_dir>/relay/<op>.md` is `allow` (§8.3) | `relay_ops` |
| A `PermissionRequest` or `PreToolUse` for `AskUserQuestion` reaches this route **without the mod** (U19 point 4) | PreToolUse/AskUserQuestion: open a `hook_ask` row flagged `terminal_only` (payload: `questions`), answer `{}` at once so the native dialog shows; PermissionRequest: the same with `hook_permission`. Clients draw a read-only card; the row closes on the session's `PostToolUse` report for that `tool_use_id`, or when the session ends. | §6.2's table, event `approval.request` |
| None of the above | `{}` | |

- The two new kinds reuse **everything** of §6.2: one table, one CAS, one `approval.request` event, one `decide` route, one snapshot. A decision on a `hook_*` kind carries the kind-specific payload (`answers`, or `behavior`/`updatedInput`/`message`) in `grant`'s place; the wire gains `HookDecision`. The Mac App draws **no card** (U19 (b)); a phone draws one from the same event and answers through `decide`.

**分流 with the Purdex mod (U19; M24).** In every interactive session the mod (P5b installs it globally) hooks `tool.call` for `AskUserQuestion` (P8a) and for tools that need permission (P8b):

1. **Ask the daemon whether anyone remote can answer.** `pdx ask begin --session <sid> --tool-use <id> --kind hook_ask --payload <json>` → `POST /api/ask/begin` (the daemon opens the `approval_requests` row of kind `hook_ask`). The daemon answers `409 no_responders` when **no remote responder** exists; then the mod simply returns `next(e)`, at no further cost, and the native dialog runs as today. A daemon that is unreachable, a 404 or any error means the same.
   - **Remote responder** = one daemon interface, `RemoteResponders.Any()`: a connected host-events subscriber (M23) **or a device registered for push** (air26 point 1: a phone in the background holds no socket and is woken by push). **P8a implements the WS half only**; the push registry and the push itself are the iOS line's, behind that interface.
2. **Race.** `const native = next(e)` is not awaited. The mod waits for the daemon with `pdx ask wait <id>` inside `$.process.run`, **in a loop**: each call long-polls at most 9 minutes and exits with `still_open`, `answered_remote {answers}` or `closed {reason}`; `$.process.run` is capped at ten minutes (M24), and the hook's own budget does not run while a `$` call is in flight, so the hold lasts as long as the native dialog stays open — hours if nobody is there. The plan for P8a measures a hold of hours once.
3. **Terminal first.** `native` resolves with the engine's result; the mod returns it unchanged and reports `pdx ask report <id> answered_local --answers <json>`. The daemon closes the row (CAS, state `answered_local`, `decided_by {kind: "terminal"}`, the answers in the payload) and broadcasts `closed`; every card closes and shows **「已在終端機回答：紅」** (air26 point 5).
4. **Remote first.** `pdx ask wait` returns `answered_remote`; the mod returns `{ result: { questions, answers } }`; the native dialog closes at once (M24). The deciding client got `200`; the others get `closed` with `decided_by` = that client.
5. **Nearly at once.** The terminal is the authority: an answer already shown in the TUI cannot be taken back. If the remote `decide` won the CAS but the native result arrived before the mod saw it, the mod still returns the native result and reports `answered_local`; the daemon records `terminal_override`, broadcasts a second `closed` with `decided_by {kind: "terminal"}` and the terminal's answers, and the remote client's card changes to 「已在終端機回答：紅」. The remote client's `200` stands as history.
6. **Dismissed.** Esc or an interrupted turn resolves `native` as a dismissal; the mod reports `dismissed`, the row closes, cards close silently.
7. **Session gone.** The sweeper's origin-liveness check (§6.2) abandons the row; the mod's loop ends with `closed`.
- **Without the mod** (`--safe-mode`, a session started before the install, Codex): the settings hook path above opens a **terminal-only** row and the card reads 「這題只能在終端機回答」; the iOS App offers its terminal mode (air26 point 4).
- **Permission prompts (P8b, deferred).** Same shape through `tool.call`: remote allow ⇒ the mod re-issues the call with `$.tool.call` and a `tool.check` hook that answers `allow`; remote deny ⇒ `{ deny }` with the client's reason; terminal first ⇒ native. The abandoned native `next(e)` leaves nothing in the terminal and nothing the model reads (M24, air26 point 3). The user runs bypass mode, where permission prompts do not occur, so **P8b waits until the iOS line needs it**.
- **Headless workers** (`claude -p`) have no TUI and the mod does nothing there; a worker's AskUserQuestion and permissions belong to Nexen's permission host, not to this spec.
- **Why 90 s and not 9 min no longer applies:** there is no wait of its own; the request lives exactly as long as the native dialog.
- Precedence inside one answer follows M18: a lock `deny` is never softened by a forwarded `allow`.
- **Hook timeouts and forwards.** The settings hooks keep their fire-and-forget shape, with two exceptions: the flag-gated lock path waits within its 5 s grace, and (U19 degradation) `PreToolUse` for `AskUserQuestion` and `PermissionRequest` are forwarded to `/api/hooks/decide` with a 2 s bound, their answer discarded, so a session without the mod still raises a terminal-only card. The installer raises Codex's `PreToolUse` timeout from 5 to 10 s and leaves Claude Code's entries alone. Codex's `trusted_hash` may re-prompt once after the change (M20, unmeasured); the plan measures it.
- **What is unchanged:** exit codes (`pdx hook` still exits 0), the event POST (it still happens — concurrently with the decision call, under the same 5 s budget; the two reach the daemon in no guaranteed order, and nothing depends on one: the decision is an answer to this hook, not an event, so observe-only consumers see the same stream), every other hook event. **The 5 s budget covers the stdin read, the event POST and the decision together**, so what this spec adds to `pdx hook` never holds an agent longer than 5 s on any path; the tmux and process lookups every hook already ran before this spec are unchanged and outside it.

**Why the flag file and not "always ask":** 12 CC hooks fire on every tool call; a daemon round trip on each would add latency to every session on the host, and a daemon restart would stall them all (M17, §9). The flag confines the cost to sessions that have something pending.

**Phases:** **P2c** ships the gate, the client path in `pdx hook`, the lock answers for lead requests, the Codex lock-path timeout, and the flag written by `pdx lead request`. **P8a** adds the two kinds and `HookDecision` on the wire, `RemoteResponders` (WS half), `pdx ask begin|wait|report`, the terminal-only degradation through the settings hooks, and the mod's AskUserQuestion race. **P8b** (deferred) adds the mod's permission race. **P6** adds the relay lock answers and the mod-written flag. Order and sizes in §12.

## 7. Team, spawn, kill (U8, U10)

### 7.1 Team

Created on approval.

- **Row:** `{id, host_id, lead_session_id, grant{max_members, roots[]}, request_id, created_at, ended_at}`.
- **The team follows the lead through its relays:** `lead_session_id` moves with the lineage (§8.4).
- **It ends when the lead's conversation ends:**
  - the lead's `SessionEnd` with any reason other than a relay's `/clear`;
  - this includes a manual `/clear`, which starts a new entity (conversation-entity spec E1) that does not know its members.
- **Members stay running when the team ends** (D4). They become ordinary sessions; team rows are kept for history.

### 7.2 Spawn

```
pdx spawn [--repo <key|org/repo>] [--host <alias>] [--cwd <dir>] [--title <t>] [--model <m>] [--effort <e>] [--brief-file <f> | --brief <text>]
```

1. pdx generates the operation id, then calls `POST /api/team/spawns {id, origin_inbox, cwd, title, model, effort}`. **(U20)** `model` and `effort` are optional and checked by the CLI first (exit 2): `model` matches `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(\[1m\])?$`, `effort` is one of `low`, `medium`, `high`, `xhigh`, `max` (M25). Without `--model` the CLI prints on stderr `提醒：沒有指定 --model，member 會用這台主機當下的預設模型。` and goes on.
2. The daemon checks:
   - the origin is the lead of a live team;
   - active members < `max_members`;
   - `cwd` resolves (symlinks evaluated) under a granted root and exists.

   A refusal is **409** with a code: `not_lead`, `team_full`, `cwd_outside_grant`.
3. **tmux name `tm-<first 10 hex of the op id>`, derived from the id** (D4). A retry after a daemon restart finds the op row and its recorded step:
   - an existing session of that name is this op's, so the daemon continues;
   - nothing opens twice.
4. **Create the tmux session and launch the member.** This goes through the session module's create path, then a generation-checked literal send to window 0. Each step is persisted.
   - The launch command is the host config `team.member_command`, default `claude --dangerously-skip-permissions` (the expansion of `cld-yolo`, because the daemon cannot rely on a shell alias).
   - **⟲ changed by U20 (was U18's "no `--model` or `--effort`"):** when the request carries them, the daemon appends `--model '<m>'` (single-quoted) and `--effort <e>`, after validating both again (`400 bad_request`). Without them the member runs the host's default model.
   - **A member always carries the Purdex mod:** the command always gets `--plugin-dir <data_dir>/cc-plugin/purdex`. Even with the global install the plugin loads once (M5), so spawn never depends on the user's settings.
5. **Wait up to 20 s for the member to register.** Same shape as take-to-terminal: a verified frame for the pane with a session id, plus a registry entry, so the ref is known.
   - On timeout: kill the tmux session, fail `member_start_timeout`. The member does not count against the limit.
6. **Store the member** (`team_members`: team, session id, pane, tmux name, title, spawn op, `state=active`) and set its title. Answer `{ref, address, tmux_session, session_id}`.

**⟲ changed from D4 — the brief is sent by the CLI, not the daemon.** After step 6, `pdx spawn` sends the brief through the existing `POST /api/peers/send`, with `origin_inbox` = the lead's inbox.
- So the member sees the message from the lead, and its replies go to the lead.
- No daemon-internal send is needed for spawn.

The text is prefixed with one line: `[pdx team] 你是 <lead address> 的 member（team <id>）。接力由 lead 決定，不要自己接力。`

**⟲ changed from D4 — no `--worktree` flag.**
- The daemon has no worktree API (§3.3), and U10 says the lead arranges it.
- The user's global rule forbids `git worktree add` outside `EnterWorktree`.

The skill tells the lead to recommend in the brief that the member run `EnterWorktree` itself, or to pass a `--cwd` it prepared.

**⟲ changed again for U15 — `--repo` and `--host`.** Without `--host`, the daemon of the lead's host picks the host by the §7.4 rule from `--repo`; with neither, the member opens on this host in `--cwd`. On another host the spawn is forwarded to that host's daemon (§7.4 (c), P4c); steps 2–6 run there, and the member row on the lead's host records `host_id`. The 2026-10-06 note "no `--host` in v1" is withdrawn by U15.

### 7.3 Kill and list

- **`pdx kill <ref>`:** only the member's own lead may run it; otherwise 409 `not_your_member`.
  - It kills the member's tmux session, so the member's CC exits.
  - Sets `state=killed`. Worktrees are the lead's business.
- **`pdx team [--json]`:** the caller's team — each member's address and ref, title, status, context %, **model and effort (U20, from P1's statusline reading; blank until the member's first statusline)**, cwd and tmux session.

### 7.4 Host selection and cross-host teams (U15)

**⟲ changed from §11 and from the D4 note "no `--host` in v1".** U15 makes the host a choice the daemon makes for the lead, by rule, and makes spawn, kill and relay work on another paired host.

**(a) "有相關的 repo": repo inventory per host.**
- Each daemon scans its **repo roots** (host config `team.repo_roots`, default `["~/Workspace"]`) two levels deep, `{org}/{repo}`, for a `.git` entry (directory or file). Per checkout it records the path, `remote.origin.url`, and whether it is **developable**.
- **Developable** means not a docs-only checkout: `core.sparseCheckout` is unset or false, **or** `git sparse-checkout list` prints at least one pattern other than `docs` or `docs/…`. M15: a26's `ntsu/istdc` prints only `docs`, so it is not developable; `git ls-files` cannot tell (it still lists 1484 files outside `docs/`), so the rule uses `sparse-checkout list` and nothing else.
- **Canonical key** = the remote URL normalised to `host/org/repo`, lowercase host, `.git` dropped, user and port dropped (`ssh://git@lab.protype.tw:9079/NTSU/istdc.git` → `lab.protype.tw/NTSU/istdc`; `git@github.com:wake/purdex.git` → `github.com/wake/purdex`). Org case is kept as the remote has it. A checkout with no remote keys on its path `org/repo`.
- **Both are looked at.** `pdx spawn --repo <x>` matches, in order: an exact canonical key; a case-insensitive `org/repo` suffix of the key; a case-insensitive `org/repo` of the path. Several checkouts of one repo on one host (clones, worktrees under `.claude/worktrees/` are **not** scanned) list all; the newest `mtime` of `.git` wins the default `cwd`.
- **Cache.** The scan runs at boot and every 10 minutes, and on demand with `GET /api/team/repos?refresh=1`. M15: 47 checkouts take 0.76 s. Results are in memory only.
- **Exposed** as `GET /api/team/repos` (this host) and inside `GET /api/team/hosts` (all paired hosts, fetched the way `/api/peers` rows are, §7.4 (c)).

**(b) "weekly usage": the account's `rate_limits.seven_day`.**
- The agent module parses `rate_limits` beside `context_window` (M13): `five_hour` and `seven_day`, each `{used_percentage, resets_at}`. It is absent on a session's first refresh.
- **It is per account, not per host or session** (M14). The daemon reads the account from `~/.claude.json` `oauthAccount.emailAddress` once at boot and every 10 minutes, and keeps **one host-level reading per account**: the newest `seven_day` any session of that account reported, with its `at`.
- **Stale after 60 minutes** without a refresh: an idle session does not refresh (§3.3), so a host whose sessions are all idle reports its last reading until then, and `unknown` afterwards.
- **"還夠用"** = `100 - used_percentage ≥ team.min_weekly_remaining`, host config, default **20**. `unknown` is ranked between "enough" and "not enough".
- **Hosts on the same account compare equal.** Today that is every host (M14), so rule 2 is a tie and rule 3 picks. The rule is still written, so it works the day a host signs in to another account.

**The selection rule**, in the daemon of the lead's host, at `pdx spawn --repo <x>` with no `--host`:
1. candidates = paired hosts (including this one) that are reachable, run a daemon with the team routes, and have a **developable** checkout matching `<x>`;
2. if none: refuse `409 no_host_for_repo` (exit 13), listing the hosts that have a docs-only checkout, so the lead can tell the user;
3. if one: that host;
4. if several: drop the hosts whose weekly remaining is below the threshold (keep `unknown`); if **all** of them are "enough", pick `team.preferred_host` (default the host named `mlab`) when it is among them, else the one with the most remaining; if some are not enough, pick the most remaining among the rest;
5. `pdx spawn --host <h>` skips the rule and only checks that `<h>` is a candidate for `<x>` when `--repo` is given.

`pdx spawn` prints the chosen host and why on stderr: `選擇 mlab：有 github.com/wake/purdex 可開發的 checkout（a26 只有 docs）；weekly 剩餘 mlab 96% / a26 96%，同帳號，依偏好選 mlab`.

**(c) Cross-host trust.** Written below from the code (M16).

**What exists (M16, re-verified on origin/main `fb9fcbd8`):**
- **Pairing is per host pair, with its own tokens.** `config.PeerHost{Alias, URL, HostID, Token, InboundToken, InboundTokenPrev, AllowBypass}` (`internal/config/config.go:46-58`): `Token` is what we present to that host, `InboundToken` what it must present to us. Nothing is signed; a token is `pdxp_` + 32 hex (`config.go:134`).
- **The receiver knows who is calling.** `/api/peers*` runs behind `PeerAuth` (`cmd/pdx/http_chain.go:26-34`; `internal/middleware/peer_auth.go:73-106`), which maps the inbound token to `Principal{Kind: host, Alias, HostID}`. `handleDeliver` refuses an unverified host, re-checks that the alias still maps to the same `HostID`, and requires `req.From.HostID == principal.HostID` (`internal/module/peers/deliver.go:143-205`).
- **A host principal may call exactly two routes:** `GET /api/peers` and `POST /api/peers/deliver` (`HostRoutePolicy`, `internal/module/peers/policy.go:37-50`). Every route outside `/api/peers/*` uses `TokenAuth`, which takes only the admin token.
- **Per-host permission flags exist in one form:** `AllowBypass` (`deliver.go:36,203`); plus the global `Peers.Deliver` switch.
- **Outbound calls** go to `entry.URL` with `Bearer entry.Token`, through a client that refuses redirects (`client.go:23-30`, `send.go:62-69`), with `InterDaemonTimeout = 10 s` (`internal/peers/wire.go:62`). Only two endpoint-specific helpers exist, `fetchRemote` (`client.go:39`) and `postDeliver` (`send.go:84`); there is no generic "call path X on host Y".
- **No capability negotiation.** `daemon_version` in the peers envelope is display-only; the only gate is `remote_too_old`, inferred from row shape (`internal/peers/address.go:380-386`).
- **Named repos per host already exist** in host config: `GET /api/hostconfig` → `projects[{id, name, slug, path}]` (`internal/module/hostconfig/validate.go:32-38`).

**The minimal viable trust (derived):**
- **Why the member host accepts a grant approved elsewhere.** Both hosts are the same user's daemons, paired by that user. The approval (§6) is the user's "told and agreed" (U5b), and the user saw it on every App, including the member host's. The member host therefore trusts **the paired lead host's daemon** to have enforced approval, in the same way it already trusts it to deliver messages: by its inbound token. The grant itself does not travel; the request carries `{team_id, lead_host_id, lead_session_id, lead_ref}` and the member host records them.
- **One new per-host flag on the member host:** `PeerHost.AllowTeam` (`allow_team`, default **false**), set by `pdx peers hosts allow-team <alias> on|off` and a toggle in Hosts → that host → 「允許 <alias> 在這台開 member」. It gates spawn, kill and relay from that host. Read-only inventory (`repos`, `usage`) needs only a verified pairing, like `GET /api/peers`.
- **Routes live under `/api/peers/team/…`**, because only `/api/peers/*` sees a host principal: `GET repos`, `GET usage`, `POST spawn`, `POST kill`, `POST relay`. `HostRoutePolicy` gains them, the two writes behind `AllowTeam`. The local admin routes of §6–§8 are unchanged; the team module forwards to `/api/peers/team/…` when the chosen host is not this one.
- **The member host keeps `remote_members{member_session_id, team_id, lead_host_id, lead_session_id, spawn_op, state}`.** A `kill` or `relay` is accepted only when `principal.HostID == lead_host_id` and the `team_id` matches. The lead's own relays move `lead_session_id` on the lead host; the member host is told through the same route (`POST /api/peers/team/lead-moved {team_id, lead_session_id}`), and a stale value is not fatal: the check is host + team, not session.
- **Idempotent by op id, like §7.2.** The lead host persists the forwarded op as `forwarding` and retries with the same id through the restart grace (§9.1 applied daemon-to-daemon, `InterDaemonTimeout` per try). The member host's spawn is CAS on the op id, so a retry opens nothing twice.
- **Capability:** a plain 404 from the member host on `/api/peers/team/spawn` means an older daemon: `409 remote_unsupported` to the lead (exit 13), with the host's `daemon_version`. `AllowTeam` off answers `403 host_not_allowed` → `409 host_not_allowed` to the lead (exit 13). Unreachable through the grace: `remote_unreachable` (exit 14).
- **The member's mod talks only to its own daemon** (`hello`, `claim`, `report`, §8.3). The relay op row and the lineage live on the member host. Notices to the lead (§8.5, §8.2 step 8) go through `POST /api/peers/send` from the member host's virtual peer to the lead's cross-host address, which works today.
- **The brief** (§7.2) is sent by `pdx spawn` on the lead's host through `/api/peers/send` to the member's cross-host address; its first line names the lead's cross-host address, so replies route back.
- **Not in this trust:** a remote host cannot start a team, approve, or read another host's `team.db`. Hardening (signed grants, per-root scopes per host) stays in §11.

**(d) Phases.** P4 keeps local spawn, kill and team. **P4b** adds the inventory, the usage reading, `GET /api/team/hosts|repos`, and the selection rule with `--repo` (still local execution, i.e. the rule may answer "this host" or refuse). **P4c** adds cross-host execution: forwarding spawn, kill and relay to the chosen host, the remote member record, and the lead's host in the member's brief. P6's relay then works across hosts because it rides on P4c. Each stays under 800 lines or 20 files (§12).

## 8. Relay (U1, U2, U3, U9, U13)

### 8.1 One operation, two kinds

Both kinds are rows in `relay_ops` and run the same steps in the session's mod.

| Kind | Started by | When |
|---|---|---|
| `self` | the session's own mod | at a turn's end with used ≥ 70% (U1), **after the user approves** (U13, §8.7) |
| `member` | the lead, with `pdx relay <ref>` | when the lead decides (U9) |

**States:** `requested → claimed → writing → written → cleared → done`, or `failed{reason}` / `cancelled`. A self op starts in `awaiting_approval` and moves to `claimed` on approval. Each transition is a report from the mod (§8.3), stored with its time.

**Self relay needs the switches and an approval (U13, §8.7).** `pdx relay begin --self` refuses with **409**, and the mod does nothing:
- `member_relay_is_leads` for a member (U9);
- `self_relay_off` when the host switch is off;
- `self_relay_paused` when the session is paused.

**No daemon, no relay.** The approval and the record both live on the daemon. With the daemon unreachable, the mod cannot ask, so nothing relays; auto-compact runs as usual (§8.7). Every relay is therefore recorded, and the lineage (§8.4) is always written.

**Loop guard:** a seeded conversation must grow by 20K tokens before it may self-relay again (prototype `MIN_GROWTH`).

### 8.2 Member relay, end to end

1. **Lead:** `pdx relay <ref>` sends `POST /api/team/relays {id, origin_inbox, target}`.
   - The daemon checks the target is an active member of the caller's team, and that the member's mod has said hello (§8.3) with a compatible version.
   - **A member without the mod is refused:** **409 `relay_unsupported`** (exit 13). The lead is told why: `<ref> 沒有載入 Purdex mod（或版本不符），無法接力；請手動接力或重開這個 member`.
   - It stores the op as `requested`.
2. **Daemon → member:** a control message `[pdx-relay:control] op=<id>` goes to the member's inbox from the daemon's own virtual peer (§8.5).
3. **Member mod:** `session.receive`, matching that text, returns `{consumed}`. The model never sees it (M1).
   - The text is only a wake-up. The mod calls `pdx relay claim <op>` with its session id. The daemon accepts only when the op targets that session, and answers with the op and the facts for the handoff.
   - A spoofed or stale control message therefore does nothing.
   - If a turn is running, the mod waits for its `turn.complete`.
4. **Writing:** `$.prompt.submit` with the prototype's write prompt (8 sections), to the op's handoff path.
   - §8 "協作關係" is filled from the claim. A member gets its lead and team id; a lead gets its roster.
   - At `turn.complete` the mod checks the file: all 8 headings, more than 200 chars. It allows two fix rounds, else `failed{handoff_incomplete}`, with no `/clear`.
5. **Clear:** `$.command.run('clear')` from a timer (F3).
6. **Cleared:** at `classic.SessionStart{source:clear}`, the mod reports `cleared` with the new session id. The daemon then records the lineage, in one transaction (§8.4).
7. **Seed:** `$.prompt.submit` with the takeover prompt. Its first line is `↪ 接手自 <old ref>`.
8. **Done:** at that turn's end, the mod reports `done`. The daemon tells the lead `[pdx team] <old ref> 已由 <new ref> 接手（接力檔 <path>）` (D5.6).

**⟲ Why refuse, rather than fall back to send-keys** (air26 asked for one of the two, with the reason):
- A send-keys executor would be a second, untested path for the same steps. It types into the TUI (§5), and it reaches the agent by `pdx msg` instead of `$.prompt.submit`, which is U2's mechanism.
- Spawn always loads the mod, so a member without it means something is broken: a failed load, version skew, or a mod error. Surfacing that beats silently running a weaker relay.
- The cost is small. The lead still has the manual path it has today.

`pdx relay` returns once the op is accepted and prints the op id. `--wait` blocks until done or failed, with the same restart-aware polling as §6.1.

**Timeouts:**
- claim: 60 s, else `failed{member_unresponsive}`;
- whole op: 15 min.

The lead is told about every failure.

**⟲ changed from D5 steps 3–5.** The brief had the daemon ask by `pdx msg`, wait for the Stop hook, and send-keys `/clear`. Reasons are in §5. The detection (D5.1) and the report to the lead (D5.6) are unchanged.

### 8.3 The mod's daemon calls

| Call | When |
|---|---|
| `pdx relay hello --session <sid>` | at `session.start` and after each clear: says this session can relay, gives its version |
| `pdx relay begin --self` | self relay; answers the op and handoff facts, or a refusal |
| `pdx relay wait <request>` | self relay: long-polls the approval; renews its lease (§8.7) |
| `pdx relay self off\|on\|status` | the per-session pause, also behind the mod's `/relay` command (§8.7) |
| `pdx relay claim <op>` | member relay |
| `pdx relay report <op> <state> [--new-session <sid>] [--error <e>]` | each transition; idempotent per (op, state) |

The mod reaches the daemon through `$.process.run` on `pdx`, as the prototype did. The calls use the restart-aware client (§9.1).
- If a `report` still fails after its 30 s grace, the mod **keeps relaying**: the session matters more.
- It re-sends the report at the next `turn.complete`. The daemon's reconciliation (§9.3) covers the gap.

**Handoff file location:** `<data_dir>/relay/<op id>.md` on the session's host. The brief and the research page left this open.
- It does not dirty the repo, survives worktree removal, and the daemon can verify it.
- For a session without bypass permissions, the mod answers `tool.check` with allow for a write to exactly that path; the plan verifies the hook's shape.
- Moving the content into a pdx memory store keyed by uuid is later (§11).

**Retention (air26 review (f): the user does not want files piling up).**
- **The daemon cleans, nobody else.** The team module's sweeper runs at boot and hourly. Neither the mod nor the agent deletes files in `<data_dir>/relay/`.
- **What it keeps:**
  - per lineage chain, the newest **3** handoff files;
  - nothing older than **14 days**;
  - the files of `failed` or `cancelled` ops for **3 days**, for debugging.
- **Only an op that has ended loses its file.** An op still in flight (awaiting approval through `cleared`) keeps its handoff however old it is: that file is the only copy of the conversation the relay carries, and after a `/clear` it is what seeds the new session. A stuck op must be ended by the state machine first, and its file then follows the rule of the state it ended in. Today that covers `awaiting_approval` (the approval's deadline, lease and boot reconciliation) and `cleared` (the mod's `done`); an op stuck in `claimed`, `writing` or `written` because its process died is ended only by **P6**'s boot reconciliation from frames (it may in fact have reached `/clear`, so it cannot simply be failed) — until then its file stays (issue #1735).
- Only `<data_dir>/relay/<op id>.md` is ever removed: the op id must be a single path element, the relay directory must be a real directory (not a symlink), and the target a regular file or a symlink (unlinked, never followed).
- The `relay_ops` row keeps the path, marked `pruned` once deleted.

### 8.4 Lineage: the ref keeps working (U3)

`session_lineage{session_id, predecessor_session_id, predecessor_ref, op_id, at}` is written when an op reaches `cleared`, in the same team.db transaction as the op's state change. With it:
- if the old session was a team's lead, `teams.lead_session_id` moves to the new session (same transaction; P4);
- if it was a member, the member row's session id moves (same transaction; P4);
- the title moves to the new session id (titles are stored per session id, §3.3). **⟲ plan v2 coordinator decision:** titles live in meta.db, and `database/sql` has no cross-database transaction, so the title move is its own idempotent meta.db transaction run right after `cleared` commits, and re-run by the boot reconciliation for every op still in `cleared` — the crash window between the two is closed at the next boot, never left open.

**Peer rows** gain `previous_refs` (newest first) for a live head: the **whole chain, uncapped.**
- **⟲ changed after air26's review (e).** A cap of 10 would break a member's oldest lead ref after the lead's eleventh relay. A ref is 7 bytes, so even a hundred relays add under 1 KB to one row.
- **When a lead relays,** the daemon also tells each active member: `[pdx team] 你的 lead 已換手：<new address> [<new ref>]（舊 ref 仍可用）`. So a member's own handoff records the current ref.

**`ipeers.Resolve` gains one tier, after a live ref:** a `_ref` that matches no live row but appears in exactly one row's `previous_refs` delivers to that row.
- Resolution runs on the sender's daemon over the target host's rows (§3.3), so this works across hosts.
- An older sending daemon ignores the field and answers `peer_not_found`, as today.

**Display:** `pdx peers` shows the address as `mlab/purdex-b0 [b3xxxx] (was _b1xxxx)`.

The `peer_not_found` hint stops saying a ref "never changes" (M3). It says a ref survives renames and relays, but not a manual `/clear`.

**Only relays write lineage.** A manual `/clear` is a new conversation (conversation-entity E1): messages to its old ref go nowhere, as today.

### 8.5 Detection and notice to the lead (U9, D5.1)

- **Parse context usage.** The agent module parses the statusline payload at ingest: `session_id`, `context_window.used_percentage`, `context_window_size`, and (U18) `model.id`, `effort.level`. It keeps the last value **per CC session id**, which also fixes the overwrite in a shared tmux session. Peers and the team module read it through an accessor.
- **Persist it for teams only.** The team module stores the last value on member and lead rows, so it survives a restart.
  - Other sessions show `-` after a restart until their next refresh.
- **Notice to the lead:**
  - when a member's usage reaches 70% and the member is idle (its `Stop`), the daemon sends the lead **one** notice: `[pdx team] member <address> [<ref>]「<title>」已用 72%，目前閒置。要接力請執行：pdx relay _<ref>`;
  - if the member is running when it crosses, the notice waits for its next `Stop`;
  - the notice re-arms only after a relay, or after usage drops below 70%.
- **The daemon decides nothing (U9).** Past 70% a member keeps working until its lead acts.
- **Daemon notices come from the daemon's own virtual peer** (`ccuds.StartVirtualPeer`). A reply to it gets one line back: `這是 pdx daemon 的自動通知，不會讀取回覆`.

**⟲ derived: auto-compact.**
- **Solo session or lead:** see §8.7. Only an already-approved relay skips the compaction.
- **Member:** never intercepted. It cannot self-relay (U9, U13), so its mod lets compaction run and reports `compacted`. The lead hears: `[pdx team] <ref> 已自動壓縮（lead 未在 70% 時接力）`.

### 8.6 Peers and the CWD fix

- **Context column.** `pdx peers` gains `CTX` (`72%`, or `-`, the dash the table already uses for an unknown AGENT). `--json` rows gain `agent.context {used_percentage, window, at}`.
- **CWD fix.** A session row's cwd prefers:
  1. the CC registry `cwd`, which follows `EnterWorktree` (M4);
  2. then the verified frame's cwd;
  3. then tmux `session_path`.

  This fixes the side bug from the brief §4; it lands in P1.

### 8.7 Self relay: switches, approval and the lock (U13)

**Who may self-relay:**

| Role | Default | Switch |
|---|---|---|
| (none) | on | host config `relay.self_solo` |
| lead | on | host config `relay.self_lead` |
| member | off | none: a member's relay is the lead's (U9, U13) |

**⟲ derived (a): where the switches live.**
- **Per host, in host config.** They are stored in `host_config.db` by the existing hostconfig module. The UI is Hosts → that host → a "接力" section with the two toggles and the line `member 的接力一律由 lead 安排`.
  - Reason: the daemon answers `begin`, and the mod on a host asks that host's daemon. A setting kept only in the SPA would not reach it. Hosts may differ.
- **Per session, a pause.**
  - The mod registers `/relay off`, `/relay on` and `/relay status`. The same is available as `pdx relay self off|on|status`, for scripts.
  - The daemon stores it per session id (`session_prefs`).
  - A session switch only narrows: `on` lifts the session's own pause, never a host switch that is off.
  - The self-relay dialog also offers **這個 session 不再詢問**, which sets the pause.
- **A member has no switch.** U13 says "預設關" and "接力必須由 lead 安排"; read with U9, that is not switchable. `/relay on` in a member answers `member 的接力由 lead 安排`.

**⟲ derived (b): approval.** U13 brings in U5 and U6; U13a and U5b make it one click.
- **Opening the request.** At a turn's end with used ≥ 70%, the mod calls `pdx relay begin --self`.
  - The daemon checks role, switch and pause (§8.1).
  - It then opens an `approval_requests` row of kind `self_relay` (§6.2) and a `relay_ops` row in `awaiting_approval`, and answers the request id.
- **The dialog shows:**
  - host;
  - the session: title, address, ref and cwd;
  - usage: `已用 72%`;
  - `核准後這個 session 會寫接力檔、清空並在原處接手（約 1 分鐘）`.
- **核准 is one click on any App (U13a), the same as a lead request (U5b).** So is **拒絕**.
  - The layer of §6.5 applies: no `pdx` approve command, the skill forbids self-approval, and every decision is broadcast and audited.
- **Deadline:** 10 minutes, absolute. The lease is renewed by the mod's wait (below). If the origin session is gone, the request is `abandoned`.
- **Approved:** the op moves to `claimed`, and the mod runs §8.2 steps 4–8: write, clear, seed. The new conversation keeps the model and the effort level, because `/clear` does (U18, M21); the acceptance checks it.
- **Denied or timed out:** the op becomes `cancelled{denied|timeout}`.

**⟲ derived (b): the lock (U7) for a request the mod makes.** The request opens at a turn's end, so nothing is running. The lock means **no new turn starts until the request closes**.
- **The hold.** The mod's `prompt.submit` hook awaits `pdx relay wait <request>` through `$.process.run`.
  - This applies to the main conversation, whatever the origin. Typed prompts and peer messages both pass this hook (M11).
  - A hook's 10 s budget counts only its own code, never a `$` call in flight (`HookBudget` in the 2.1.291 types; the mods reference: "a `next` or `$` call in flight does not count"). M11 measured a 15 s hold. So the hold lasts until the request closes.
  - **Fallback, if a later Claude Code stops routing peer deliveries through `prompt.submit`** (air26 review): consume them in `session.receive` (M1), and replay them once the request closes. The mod's tests pin the current routing, so such a change turns a test red.
- **While holding:** status line `接力等待核准中`, plus one toast naming where to approve.
- **Approved:** the held prompts go through **into the current conversation** (`next(e)`), and the relay starts right after.
  - The mod submits the write prompt once idle. It recognises its own write turn by the turn that `$.prompt.submit` started, not by "the next `turn.complete`". So queued prompts that run first do not trigger the handoff check early.
  - **⟲ changed after air26's review (3), which asked to drop and then re-submit with `asUser: true`.** A re-submitted prompt loses its `@file` mentions and pasted images, and stays attributed to the plugin (M12). Letting the person's own prompt run in the old conversation keeps it whole. That costs one turn of context, which is affordable at 70–80% used.
  - The handoff then records that turn too.
  - **A note for the model on released prompts (air26 review).** The release is `next({ ...e, context: [...(e.context ?? []), NOTE] })`. `context` reaches the model beside the prompt and is never shown to the user (2.1.291 types, `PromptSubmitResult.context`). NOTE says:
    > 接力已核准，這一輪只做簡短回應；如果這是一件新工作，不要開始做，把它寫進接力檔「下一步」的第一項，由接手後的新對話處理。

    Why: without it, the old conversation could take on a large new task at its fullest (70–80%). That defeats relaying early, and could run into auto-compact.
    - `@file` mentions and images still expand in the old conversation, so the handoff can record their paths and gist.
    - This is a soft constraint: the model may not follow it. The backstop is §8.7(c): an approved relay not yet written skips auto-compact.
  - If the plan finds the write turn cannot be told apart reliably, it falls back to air26's way: drop, then re-submit after the seed, `asUser: true` for a typed prompt and the envelope kept for a peer message. In that case, a prompt carrying `@file` mentions or images is released instead of dropped.
- **Denied or timed out:** the held prompt goes through unchanged (`next(e)`).
- **Esc:** it abandons that dispatch, so that prompt is not sent. The request stays open.
- **No prompt arrives:** the mod also waits from a timer (`$.clock.after` → `pdx relay wait`), so an approval starts the relay at once.

**⟲ derived (c): asking again, and auto-compact.**
- **Asking again.** After a denial or a timeout, the mod asks again only when usage has grown **10 more points** since the last ask: 72 → 82 → 92. At most one open self-relay request per session.
- **Auto-compact never waits for an approval.** At `session.compact{trigger:auto}`:
  - **an approved relay not yet written:** skip the compaction (`{skip}`) and start writing;
  - **anything else:** compaction runs. An open request becomes `cancelled{compacted}`, and its dialog closes on every client.
  - Reason: an approval takes a person and minutes. Holding the compaction would hang the session when it is fullest.
  - Unmeasured (brief §3): whether `{skip}` mid-turn lets the turn go on. The plan measures it. If it does not, the approved case also lets compaction run, and starts the relay at the turn's end.
- A manual `/compact` is treated the same for an **open** request (it becomes `cancelled{compacted}`); an approved relay not yet written is **not** skipped on a manual compact, because the person asked for it.
- **After a compaction**, the next ask needs usage ≥ 70% again.

**(d) No daemon.** With the daemon unreachable, the mod cannot ask, so nothing relays, and auto-compact runs (§8.1).
- `pdx relay wait` uses the restart-aware client (§9.1). A restart during the wait keeps the request.
- After `daemon_unavailable` (exit 20), the mod releases the hold and treats the request as not approved. The daemon's lease then closes it.

## 9. Daemon restart (U12, D6)

Waiting is tied to a **request or operation id**, never to a connection. All state is in `team.db`.

### 9.1 The pdx client

One shared client, `cmd/pdx/daemonclient`, is used by every new command and by the mod's calls.

**It treats these as "restarting":**
- connection refused, reset or EOF;
- `503` with `shutting_down` or `not_ready`;
- a `/api/health` `boot_id` that differs from the one first seen.

**Then:**
- It prints `daemon 重啟中，繼續等待…` once on stderr. When the daemon answers again with a different `boot_id`, it prints `daemon 已重新啟動（boot <id>）` once, so the log shows that a restart, not a pause, happened.
- It retries with backoff 0.25 s → 1 s, for a **30 s grace**: three times the usual 5–10 s.
- After the grace it exits 20, `daemon_unavailable`.
- A daemon that accepts the connection but never answers is treated the same way: after three consecutive long-polls that run out their own 35 s without an answer, the CLI exits 20 with `daemon 沒有回應` (plan P2b, decided 2026-10-07).
- Deadlines are absolute, so a restart's time counts against them.

**By kind of call:**
- reads and long-polls: retry transparently;
- writes carry their client id and retry with it;
- long flows are persisted and reconciled (§9.3).

**A 404 on a team route** means an older daemon: exit 21, `unsupported` (D6).

### 9.2 Leases

- An open lead request's lease is renewed by each poll.
- **On boot**, every open request gets `lease_until = max(lease_until, boot + 30 s)`, so its pdx can reconnect.
- **It is abandoned** when the lease runs out, or when the origin session's process is gone, whichever is first. It then closes, with a broadcast.

### 9.3 Reconciliation on boot

The daemon reads actual state; it never waits for lost hooks.

**Spawn ops not finished:**
- if the tmux session exists, continue from the recorded step: launch, or wait for registration;
- past the 20 s budget, kill it and fail.

**Open approval requests of every kind** get the lease grace above. A self-relay request's lease is renewed by the mod's `pdx relay wait`.

**Relay ops not finished:**
- `requested`, not claimed: send the control message again. The claim is CAS, so a duplicate does nothing.
- Past `claimed`: compare the op's old session id with the pane's current verified frame.
  - **A different session id** means the clear happened. Write the lineage from the frame's session id if the mod's report has not, and wait for `done`.
  - **No frame** means CC exited: `failed{member_gone}`.
- **The tmux hook gap** (§3.3) is covered the same way: every decision reads frames and the registry, never a hook that may have been lost.

### 9.4 Clients during the restart

See §6.3: the prompt stays, disabled; a click is queued and re-sent.

### 9.5 Restarting with work in flight

**⟲ changed from D6** ("respond with the list; the caller waits or adds `--force`"). `POST /api/daemon/restart` is not refused.
- Everything above survives a restart, so a 409 would only add friction.
- Instead, the restart confirm dialog (daemon-restart spec §3.2) gains lines, each shown only when non-zero: `N 個申請等待核准、N 個接力進行中（重啟後會接續）`.
- The counts come from `GET /api/team/inflight` → `{approvals_open: N, relays_active: N}`, within the dialog's existing 3 s budget; when the call fails the dialog falls back to the open requests its own store holds. P2a ships the route with `relays_active: 0`; P6 fills it.

## 10. The skill (D2's last point)

The skill ships in the plugin (`skills/pdx-team/SKILL.md`). It says:

**When to ask, and how to wait:**
- when to ask for lead mode: the work is large and parallel (U5);
- run `pdx lead request` in the foreground with Bash `timeout: 600000`; never in the background, and never approve yourself;
- treat timeout as no.

**As a lead:**
- spawn and kill;
- choose each member's model (and effort) for its task with `pdx spawn --model` / `--effort`: the host's default model is not fixed (U20); check `pdx team` to see what each member actually runs;
- recommend a worktree to members, by having them `EnterWorktree`, or prepare one (U10);
- when a `[pdx team]` notice arrives, decide whether and when to `pdx relay` (U9);
- write the team roster into your own handoff's §8.

**As a member:**
- never relay yourself;
- report to the lead's address.

**Self relay:**
- the Purdex mod asks the user on its own; the agent never asks for one and never approves one;
- `/relay off` is the user's switch, not the agent's.

## 11. Later (not in this spec's phases)

- **Hard lock (D3) — moved to §6.6 by U17.** What stays later: a hook decision for events other than PreToolUse and PermissionRequest; the push half of `RemoteResponders` (the iOS line's); P8b until the iOS line needs it.
- **Human-presence approval (hardening; U5a, withdrawn by U5b).** The design written on 2026-10-06 (commit `b9a6f239`, §6.5):
  - a CryptoKit Secure Enclave key per Mac in Purdex.app, with `.userPresence` (Touch ID, or the login password);
  - enrolled per daemon host;
  - signing a challenge bound to the request and its grant.

  Alternatives, once available: Electron WebAuthn, after a Developer ID and provisioning profile (signing roadmap Stage 3). Facts: M6–M10.
- **Cross-host hardening:** signed grants carried to the member host, per-host root scopes, and a capability handshake between daemons. The minimal trust of §7.4 (c) is in P4c; this is what comes after.
- **Adopting** an existing session as a member. Today's manual flow, where the user opens a session and hands its address to A, keeps working as plain messaging.
- **Handoff content in a pdx memory store** keyed by uuid.
- **Member visuals** (U11): a separate design.

## 12. Phases

One phase is one PR, ≤ 800 lines or ≤ 20 files; split further when larger.

| Phase | Content | Brief D8 |
|---|---|---|
| P0 | PRODUCT.md vocabulary (§4) | "separate small PR" |
| P1 | Statusline usage parsed per session id + accessor, with `model.id` and `effort.level` beside it (U18); peers `CTX` column and `agent.context`; CWD fix; `peer_not_found` hint text (§8.5, §8.6) | 1 (part) |
| P2 | `team` module skeleton and `team.db`; `daemonclient` with the restart rules (§9.1); lead requests: create, poll and lease, cancel, decide, sweeper, boot grace, `OnSubscribe` snapshot; `pdx lead request`; exit codes (§14) | 1 |
| P3 | Approval dialog host, store, event branch, one-click approve and deny (U5b), reconnect queue, notifications; restart-confirm line for open requests (§6.3, §9.5) | 1 |
| P2c | Hook decisions (U17, §6.6): flag-file gate, `pdx hook` decision path for PreToolUse / PermissionRequest (CC and Codex) with the 5 s grace, `POST /api/hooks/decide` with the lead-request lock answer, Codex `PreToolUse` timeout 5 → 10 s (Claude Code entries untouched), flag written and removed by `pdx lead request`, sweeper of stale flags | U17 (uses 1) |
| P4 | Teams and grants; `pdx spawn` / `kill` / `team` on this host, with spawn's `--model` / `--effort` and both U20 reminders, `pdx team`'s model and effort columns; spawn reconciliation; team end on the lead's exit | 1, U20 |
| P4b | Host selection (U15): repo inventory and `developable` rule, `rate_limits` parsing and the per-account weekly reading, `GET /api/team/repos|hosts`, `GET /api/peers/team/repos|usage` for paired hosts, the selection rule and `pdx spawn --repo`, `team.repo_roots` / `team.min_weekly_remaining` / `team.preferred_host` host config | U15 |
| P4c | Cross-host execution (U15): `AllowTeam` flag, CLI and Hosts toggle; `POST /api/peers/team/spawn|kill|relay|lead-moved` behind `HostRoutePolicy`; forwarding with op-id idempotency and the restart grace (the spawn carries U20's `model` / `effort`); `remote_members`; cross-host brief and notices; `remote_unsupported` / `host_not_allowed` / `remote_unreachable` | U15 |
| P5a | Daemon relay core: `relay_ops`; `session_lineage` with uncapped `previous_refs` and the Resolve tier; title, team and lead-ref moves; the `self_relay` approval kind; host switches and Hosts UI toggles; session pause; handoff retention sweeper | 2 (part), U13 |
| P5b | Plugin packaging (embed, extract, `CLAUDE_CODE_PLUGIN_DIRS` merge and uninstall) with the skill; mod self relay: `hello` / `begin` / `wait` / `report`, the prompt hold, asking again, the auto-compact rule, `/relay` | 2 (part), 4 (part), U13 |
| P6 | Member relay: `pdx relay`, the daemon's virtual peer and control message, `claim`, timeouts, boot reconciliation of relay ops; completion and failure notices; restart-confirm line for relays | 2 |
| P7 | Detection and the 70% notice to the lead; persisted usage on team rows; member auto-compact report | 3 |
| P8a | 分流 for AskUserQuestion (U17 use 3, U19): kinds `hook_ask` and `hook_permission` on `approval_requests`, `HookDecision` on the wire, `RemoteResponders` (WS half), `pdx ask begin\|wait\|report`, the `answered_local` / `terminal_override` / `dismissed` closes and their `closed` broadcasts, the terminal-only degradation through the settings hooks, the mod's `tool.call{AskUserQuestion}` race with the bounded wait loop, an hours-long hold measured once; no Mac App UI | U17 (uses 3, 4), U19 |
| P8b | 分流 for permission prompts (deferred until the iOS line needs it): the mod's `tool.call` race for permission-gated tools, `$.tool.call` re-issue with a `tool.check` allow hook, deny with the client's reason; the Codex `trusted_hash` probe | U19 (point 3) |

**Notes on the split:**
- **P5a/P5b depend on P2 and P3.**
- **Order:** P0, P1, P2, P3, P2c, P5a, P5b, P8a, P4, P4b, P4c, P6, P7, then P8b when wanted.
  - P2c needs P2 (the lead request it locks) and nothing from P3; it is small and closes the soft lock's hole before the skill (P5b) tells agents to use `pdx lead request`.
  - P8a needs P5b (the mod is packaged there) and P2's approval table; it moves earlier if the iOS line needs it. P8b is not scheduled.
  - P4b needs P4 (team rows) and P1 (the statusline parser it extends). P4c needs P4b and the peers pairing that exists.
  - P6 (member relay) rides on P4c for a member on another host: the lead host forwards `relay`, the member host runs §8.2. It also carries U17 use 2: the relay lock answers in `/api/hooks/decide` and the flag the mod writes.
  - Self relay, goal 1, ships first.
  - P4 (team, spawn) needs P3, because a lead approval comes from the dialog.
- P6 may split in two: daemon first, then mod.
- The mod has its own tests, run by `claude plugin test`.
- Daemon deploys are batched with the other daemon lines (conversation-entity D14 practice).

## 13. Changes to the brief's draft

| Draft | Change | Reason |
|---|---|---|
| D1 | The daemon stays the brain; in-session steps move to the Purdex mod | M1 proves the control channel; `command.run` beats send-keys; U2 literally; relays survive restarts; goal 1 needs the mod anyway (§5) |
| D2 | Approve and deny are one click on any App; the `client` descriptor is an audit label | U5b (U5a withdrawn); one shared host token (§3.3) |
| D2 | The grant has no host list; the host is chosen per spawn by the §7.4 rule, and the member host gates by its own `AllowTeam` flag | U15; the user picks hosts by rule, not per grant |
| D4 | No `--worktree`; `--repo` and `--host` (U15); the brief is sent by the CLI from the lead's inbox; tmux name `tm-<op>`; start timeout kills and frees the slot | No worktree API and U10; U15 reinstated the host choice; replies reach the lead; D4's own idempotency idea; the limit counts only live members |
| U18 | ~~Spawn has no model flags~~ (superseded by U20); self relay relies on `/clear` keeping model and effort; 切換 tracked in issues, P1 records the values | M21, M22 |
| U20 | Spawn takes optional `--model` / `--effort` (validated, model single-quoted); reminders at activation and at a spawn without `--model`; `pdx team` shows each member's model and effort; replaces U18's spawn bullet only | U20; M25 |
| U19 | 分流, not interception: the Purdex mod races the native dialog (`next(e)` not awaited) against the daemon; no per-session switch; no Mac App card; terminal-only degradation without the mod; terminal wins a tie and the card says so; P8a/P8b | U19; M24 probes; air26's five points |
| §11 → §6.6 (U17) | `pdx hook` waits for the daemon on PreToolUse and PermissionRequest behind a flag file; lead and relay locks in P2c/P6; forwarded prompts as two more approval kinds in P8 | U17; M17–M20; one approval model serves the iOS line too |
| §11 → §7.4 (U15) | Cross-host spawn, kill and relay are in P4b/P4c; trust = the paired lead host's inbound token plus the member host's `AllowTeam` flag; the grant does not travel | U15; `PeerAuth` already identifies the calling host; both daemons are the same user's |
| D4 | Launch command is `team.member_command`, default `claude --dangerously-skip-permissions` | The daemon cannot rely on the `cld-yolo` alias |
| D4 (air26 review) | A member is always launched with `--plugin-dir`; relay to a member without the mod is refused, not done by send-keys | Loads once even with the global install (M5); reasons in §8.2 |
| D5 3–5 | The mod writes, clears and seeds; the daemon only sends a control wake-up | §5 |
| D5 | Only relays write lineage; titles and team roles move with it | E1; titles are per session id |
| D5 | Self relay needs the daemon (`begin`) | Every relay is recorded |
| D5 (new) | A member's auto-compact is not intercepted; the lead is told | U9 forbids a member self-relay |
| D6 | No 409 / `--force` on restart; the confirm dialog lists in-flight work | Everything survives a restart (§9.5) |
| D7 | Adds 13 and 14 (§14); keeps 1 and 2 as today | Spawn, kill and relay need refusal and start-failure codes |
| D8 | More PRs than the brief's four phases | The 800-line / 20-file limit; U13 added work |
| U13 (a) | Switches per host in host config; a per-session pause by `/relay` and `pdx relay self`; members not switchable | The daemon is what answers `begin`; U9 |
| U13 (b) | One `approval_requests` table (kinds `lead`, `self_relay`); the self-relay lock holds `prompt.submit` inside a `$` wait; approve is one click (U13a, U5b) | air26 asked for a shared model; a hook's budget excludes `$` waits |
| U13 (c) | Ask again after 10 more points; auto-compact never waits, and only an already-approved relay skips it | The session must never hang |
| U13 (d) | No daemon: no ask, no relay, compaction runs | Approval lives on the daemon |
| §8.4 (review e) | `previous_refs` uncapped; members told the lead's new ref | A cap breaks old lead refs |
| §8.7 (review 3) | On approval, held prompts run in the current conversation, and the relay follows; no drop and re-submit | A re-submitted prompt loses `@file` and images, and stays attributed to the plugin (M12); air26's way is the fallback |
| §12 (review 1) | P5 depends on P2 and P3 | U13a; U5b removed P3a/P3b |
| §8.3 (review f) | Retention: 3 per chain, 14 days, 3 days for failed; the daemon cleans | The user does not want files piling up |

## 14. Exit codes (D7, extended)

| Code | Meaning |
|---|---|
| 0 | Approved / done / accepted |
| 1 | Other runtime or API error (existing convention) |
| 2 | Usage error (existing convention) |
| 10 | Denied |
| 11 | Timed out (counts as denied, U7) |
| 12 | Cancelled or abandoned |
| 13 | Refused by team rules: `not_lead`, `team_full`, `cwd_outside_grant`, `not_your_member`, `member_relay_is_leads`, `self_relay_off`, `self_relay_paused`, `relay_open` (carrying the open op), `bad_transition` (carrying the op), `relay_unsupported`, `already_lead`, `member_cannot_lead`, `request_open`, `no_host_for_repo`, `host_not_allowed`, `remote_unsupported`, `no_responders` (§6.6 step 1: nobody remote can answer; the mod lets the native dialog run alone). `409 ask_open` is **not** an exit 13: `pdx ask begin` adopts the open row it carries and exits 0 with its id (§6.6 step 1, P8a) |
| 14 | The member did not start or did not respond: `member_start_timeout`, `member_unresponsive`, `remote_unreachable` |
| 20 | Daemon unreachable through the 30 s grace |
| 21 | Daemon does not support this (404) |

## 15. Tests

**Daemon:**
- **Approvals of every kind:** they share one compare-and-set.
- **Retention:** 3 per chain, 14 days, and 3 days for failed ops; nothing outside `<data_dir>/relay/` is touched.
- **Lineage:** an uncapped chain still resolves a lead's oldest ref after 11 or more relays.
- **Lead request:** create is idempotent; exactly one close wins under concurrent decide, timeout and cancel; the 409 carries `decided_by`; the snapshot reaches a late subscriber; the lease is extended on boot; abandonment fires when the origin dies.
- **Spawn:** the limit, roots and symlink escape; retry after a mid-op restart opens nothing twice; the start timeout kills and frees the slot. **U20:** `--model` / `--effort` reach the launch command (model single-quoted, `opus[1m]` included); a model with a space, a quote, `;` or `$(` and an effort outside M25's five are exit 2 at the CLI and `400 bad_request` at the daemon; a spawn without `--model` prints the reminder on stderr and still exits 0; an approved `pdx lead request` prints the activation reminder on stderr and only the grant JSON on stdout; `pdx team` shows a member's model and effort.
- **Relay:**
  - claim is accepted only for the target session;
  - reports are idempotent;
  - lineage moves lead and member in the `cleared` transaction, and the title in its own idempotent transaction right after (re-run at boot);
  - Resolve finds an old ref in exactly one row, and a live ref wins over `previous_refs`;
  - boot reconciliation from frames, with no hooks.
- **Usage:** parsed per session id; two panes in one tmux session no longer overwrite; a null `used_percentage`; `rate_limits` absent on the first refresh, present from the second; the per-account weekly reading goes `unknown` after 60 min.
- **Host selection (U15):** a cone sparse checkout whose only pattern is `docs` is not developable; a plain checkout is; the canonical key normalises `ssh://git@host:port/Org/repo.git`, `git@host:org/repo.git` and `https://host/org/repo`; the rule refuses `no_host_for_repo` when every match is docs-only; same-account hosts tie and `preferred_host` wins; a host below the threshold loses to one above; `unknown` ranks between.
- **Cross-host (U15):** a host principal without `AllowTeam` gets `host_not_allowed`; a kill from a host other than `lead_host_id` or with another `team_id` is refused; a forwarded spawn retried with the same op id opens one tmux session; a plain 404 from the member host becomes `remote_unsupported`; the routes are in `HostRoutePolicy` and nowhere else.
- **CWD:** the precedence order.
- **Hook decisions (U17):** no flag ⇒ no daemon call and empty stdout; flag + open lead request ⇒ PreToolUse `deny` with the reason, PermissionRequest `{}`; flag + daemon unreachable ⇒ exit 0, empty stdout, within 5 s; stale flag ⇒ `{}` and the sweeper removes it; `hook_ask` and `hook_permission` rows close through the same CAS as a lead request; `no_responders` when `RemoteResponders.Any()` is false and no row is opened; `answered_local` closes with the terminal's answers and one `closed` event; a late `decide` after `answered_local` gets `409 already_decided` carrying `decided_by {kind: terminal}`; a `terminal_override` after a remote win broadcasts a second `closed` with the terminal's answers; `dismissed` closes silently; the terminal-only row opens from the settings hook and closes on the matching `PostToolUse` report.
- **Mod (U19, `claude plugin test`):** `no_responders` ⇒ plain `next(e)` and no further daemon call; terminal first ⇒ the native result is returned unchanged and `answered_local` is reported; remote first ⇒ `{ result }` built from `answers`, in the shape M24 measured; a `still_open` from `pdx ask wait` loops without returning; Esc ⇒ `dismissed`; a hold across several `pdx ask wait` rounds keeps the dialog open (measured once for hours in P8a).

**CLI:**
- `daemonclient`: refused, then a new `boot_id`, then a retry succeeds; the grace expires into exit 20; a 404 gives 21.
- `pdx lead request` cancels on SIGTERM.
- Exit codes for each terminal state.

**Mod** (`claude plugin test`):
- the control message is consumed only with the marker;
- `claim` failure does nothing;
- write → check → fix rounds → clear → seed;
- the 20K loop guard;
- a member does not self-relay;
- auto-compact is intercepted for solo and lead, and passed through for a member;
- headless does nothing;
- **self relay under U13:**
  - a prompt that arrives while a request is open waits;
  - on approval it runs in the current conversation, and the write turn is recognised by its own turn even with queued prompts ahead of it;
  - a released prompt carries NOTE in its `context`, appended after any context already attached; a prompt released after a denial or a timeout carries no NOTE;
  - on denial it passes unchanged;
  - asking again only at +10 points;
  - auto-compact runs, and cancels the request, unless the relay is already approved;
  - `self_relay_off` and `self_relay_paused` are respected;
  - `/relay on` in a member is refused.

**SPA:**
- the dialog opens from the snapshot and the event;
- 核准 and 拒絕 are one click on every App, for both kinds;
- it closes on `closed` from another client, with the toast;
- disabled while disconnected; a queued click is re-sent; a 409 closes it.

**Mutation is a deliverable:**
- dropping the CAS lets two decisions win → red;
- dropping the claim's session check lets another session claim → red;
- dropping `previous_refs` from Resolve leaves the old ref at `peer_not_found` → red;
- dropping the prompt hold lets a prompt start a turn while a self-relay request is open → red;
- treating "the next `turn.complete`" as the write turn makes a queued prompt trigger the handoff check early → red;

**Real acceptance (mlab, then air26):**
1. A session requests lead; the user approves on air26's App with one click; the dialog closes on a19's App at the same moment, showing who approved.
2. The lead spawns two members, and relays one at 70% on a test threshold (`PDX_RELAY_THRESHOLD`, as in the prototype). The old ref still reaches it.
3. Restart the daemon during a pending request and during a relay; both finish.
4. Self relay on a solo session at a test threshold: the dialog appears on every client; deny, then see it ask again at +10 points; approve with one click, no Touch ID; a message typed during the wait is answered first, intact, then the relay runs.

## 16. Not in scope

- Member visuals (U11).
- Nexen worker relay: it goes through Nexen rebuild.
- **切換 (handoff) keeping the session's model and effort (U18 (c)):** tracked as `wake/purdex` issue [#1647](https://github.com/wake/purdex/issues/1647) and `wake/nexen` issue [#131](https://lab.protype.tw/wake/nexen/issues/131); this spec only records the values (P1).
- Cross-host hardening (signed grants, per-host scopes), adoption, Electron WebAuthn, and the handoff memory store: all in §11. The hard lock is **in** scope since U17 (§6.6, P2c) and 分流 for AskUserQuestion since U19 (P8a); the iOS client, the push registry behind `RemoteResponders`, and (until the iOS line needs it) the permission race P8b are not. Cross-host spawn, kill and relay themselves are **in** scope since U15 (§7.4, P4b/P4c).
- Claude Code's built-in Agent Teams.
- **Turning the browser SPA off (U14).** Here U14 is a premise only.
  - The unmerged web version (branch `worktree-web-version`; `purdex.mlab.host` in front of the daemon) is affected.
  - The user decides separately whether to drop it, keep it as a view-only client, or fold it into the App.
