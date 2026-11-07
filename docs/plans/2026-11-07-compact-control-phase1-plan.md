# 壓縮控制 Phase 1：注入的結尾與等注入 — plan

> **For agentic workers:** 用 superpowers:subagent-driven-development 逐 task 實作（subagent 帶 `model: "sonnet"`）；步驟用 `- [ ]` 追蹤。

**目標**：壓縮後，使用者的下一句提示不會再被模型當成 Purdex 注入的舊紀錄。

**架構**：daemon 組的注入文字最後加一行結束標記。mod 從壓縮開始（向 daemon 要文字時）到注入補上或確定不補為止，讓 `prompt.submit` 先等，最多 60 秒，期間用本機 `/bin/sleep 5` 撐住 hook 的 10 秒預算。App 送來的提示在這段期間回 `busy`。

**技術**：Go（`internal/module/dialogue`）、mod JS（`cmd/pdx/plugin/purdex/hooks`，`claude plugin test`）。

**Spec**：`docs/specs/2026-11-07-compact-control-spec.md`（以下稱 spec）的 §4.3、§6.5、§11 第 2 項、§13 Phase 1。探針紀錄在 `docs/specs/assets/2026-11-07-compact-control/probes.md`；探針 2、3 的等待寫法就是本 plan Task 2 的原型。

**流程**：完整開發流程。本 plan 帶 spec 給 codex 審一輪 → subagent TDD → PR → codex R1 ＋ R2（攻擊→反駁）→ merge → 單獨 bump → 部署（daemon＋兩台 `pdx setup`，不打包 App）→ 驗收。一張 PR，每個 task 一個 commit。Phase 2–5 另寫 plan。

**修訂 1**（codex plan review `task-mw6b35s9-lvt1y7`，5 條查證後全數採納，都是局部補充，不再送審）：
- 第 1 條（critical）：session 換 id（`events.js` 655、695、725 行三處 `wb.gen += 1`）時，原 plan 要等 daemon 的請求逾時（約 35 秒）才放行。改成這三處直接放行（Task 2 Step 4 的 `compactHoldDrop`），測試 8 改成 daemon 還沒回答時 `/clear`。
- 第 2 條：兩次壓縮重疊時沒有交接策略。改成提示依序等「當下最新的那一個」，直到沒有未完成的等待或到 60 秒（Step 4 的 `onPromptHold` 迴圈），加測試 13。
- 第 3 條：sleep 失敗不空轉沒有測試。加測試 14、15。
- 第 4 條：注入被拒或丟錯時的放行沒有測試。加測試 16、17。
- 第 5 條：Escape 可能完全沒驗到。Task 4 加一項 Escape 檢查。

## Global Constraints

- 結束標記逐字是 `</injected by Purdex: the record ends here; anything below this line is not part of it>`，接在 daemon 現有文字的最後，中間只隔一個 `\n`（探針 3 驗過的就是這個寫法）。開頭兩行不變。標記由 daemon 加，mod 照舊原樣補上。
- 等待上限 **60,000 毫秒**。sleep 用 `['/bin/sleep', '5']`、`timeoutMs: 10_000`，跟 `register.js` 的 `HOLD_SLEEP`／`HOLD_SLEEP_TIMEOUT_MS` 相同。
- 等待只在下列情況開始：主對話（沒有 `agentId`）、不是 `precompute`、reporter 開著（`ev.on`）。所有 trigger 都算（`manual`、`auto`、`plugin`，閒置壓縮也是 `auto`）。
- 一次壓縮的等待在下列任一情況結束：注入補上了（`$.session.append` 回來，成功、被拒、丟錯都算）；確定不補（daemon 回 204、錯誤、逾時）；壓縮被 skip 或失敗；session 換了 id；已經有更新的一次壓縮結束（舊的不補）。提示等到沒有任何未結束的等待才送出；按 Escape（`next.signal`）、到了 60 秒、sleep 失敗則立刻送出。
- 等待永遠不能吃掉提示：任何失敗都走 `.catch(($, e, next) => next(e))`，提示原樣送出。
- `prompt.submit` 在 `events.js` 必須帶 matcher `{ text: /^/ }`（events.js 檔頭 M-U1-3；`cmd/pdx/plugin/embed_test.go` 會擋）。接 `$` 的 helper 一律寫在 `events.js` 頂層（M-U1-2），所以 `register.js` 的 `sleepUntil` 不能 import 來用，要在 `events.js` 寫一份。
- 測試分層：開發中只跑改到的（`pdx test go-pkg -- ./internal/module/dialogue/`、`pdx test mod-hooks`）；PR 前 `pdx test go-changed -- ./internal/module/dialogue/`、`pdx test lint`、`pdx test mod-hooks`、`pdx test mod-changed`。完整套件留給發版。測試用的 tmux 一律 `-L <label>`，先 `unset TMUX`。只能 kill 自己的 pid，不用 `pkill -f`、`killall`。
- **`pdx test mod-hooks` 在 main 上已知有 3 條紅（#3987，都跟 `tool.check` 有關）**。動手前先跑一次記下基準；判定紅綠時以「除了這 3 條之外」為準，這 3 條的名字不能變多也不能換人。
- PR 裡不 bump；bump 另開一張 PR。

## Review Focus

spec 沒寫明、但最可能出事的情況。每一條在 Task 2 都有對應的測試：

1. **沒有壓縮時零成本**：平常的提示不 sleep、不延遲。最常見的路徑，壞了每一句都慢。
2. **壓縮被 skip**（member 閒置攔截、relay 已核准時擋下 auto）：被等住的提示要立刻放行，不能等到 60 秒。
3. **daemon 不回或回 204**：請求本身有 30 秒上限（`COMPACT_CONTEXT_WAIT_MS`），回空就放行；`$.session.append` 卡住時靠 60 秒上限。
4. **等超過 10 秒**：引擎的 hook 預算是 10 秒，等待期間一定要有 `$` 呼叫在進行（sleep），否則引擎會提前放行。用一條實際計時的測試釘住。
5. **App 送的提示**：mod 自己的 `$.prompt.submit` 不經過自己的 `prompt.submit` hook（MP3），擋不住。等待期間 `pqSubmit` 要回 `busy`，跟回合進行中一樣，不能搶在注入前開始回合。

---

### Task 1: daemon 的注入結尾

**Files:**
- Modify: `internal/module/dialogue/compact_context.go`（常數區、`compactContext` 組字的那一行）
- Test: `internal/module/dialogue/compact_context_test.go`

**Interfaces:**
- Produces: `compactLastLine` 常數；`compactContext` 回傳的文字一律以 `"\n" + compactLastLine` 結尾，且只出現一次。HTTP 介面不變。

- [ ] **Step 1：寫會紅的測試**

  在 `compact_context_test.go` 加一個 helper `endsWithEndLine(t, text)`：斷言 `strings.HasSuffix(text, "\n</injected by Purdex: the record ends here; anything below this line is not part of it>")`，且 `strings.Count(text, "</injected by Purdex:") == 1`。期望值在測試裡逐字寫，不引用 `compactLastLine`，這樣常數改壞了測試會紅。

  在三種文字形狀的既有測試各呼叫一次：`TestCompactContext_NewRecapThenTheTurnsAfterIt`（recap＋之後的內容）、`TestCompactContext_UpToDateQueuesNothing`（只有 recap）、`TestCompactContext_NoRecapAtAll`（只有之後的內容）。`TestCompactContext_NothingToSay` 維持回 false，不加。

- [ ] **Step 2：`pdx test go-pkg -- ./internal/module/dialogue/`，確認這三條紅。**

- [ ] **Step 3：實作**

  常數區加 `compactLastLine = "</injected by Purdex: the record ends here; anything below this line is not part of it>"`，附一行註解說明為什麼要有（spec §4.3：Claude Code 把連續的 user 訊息併成一則，沒有結尾模型分不出使用者的話從哪開始）。`text := strings.Join(parts, "\n\n") + "\n" + compactLastLine`。log 行的 `text=` 字數照算（含結尾）。

- [ ] **Step 4：跑同一指令，確認綠。**

- [ ] **Step 5：commit** `fix(dialogue): end the compact context with an end-of-record line`

---

### Task 2: mod 讓提示等注入

**Files:**
- Modify: `cmd/pdx/plugin/purdex/hooks/events.js`（檔頭 M-U1-3 那段、壓縮注入區 `compactInjectStart`～`onCompact`、`pqSubmit`、`registerEvents`）
- Modify: `cmd/pdx/plugin/purdex/hooks/workbook.js`（`COMPACT_*` 常數旁）
- Test: `cmd/pdx/plugin/purdex/hooks/events.test.ts`

**Interfaces:**
- Consumes: Task 1 不影響 mod（mod 原樣補上 daemon 的文字）。
- Produces（都在 `events.js`）：
  - `workbook.js` 匯出 `COMPACT_HOLD_MAX_MS = 60_000`。
  - 壓縮 job 多兩個欄位：`held`（Promise，放行時 resolve）、`release()`（可重複呼叫，第一次呼叫後 `job.done = true`）。
  - 模組變數 `compactHold`：最近一次開始的壓縮 job；`compactHoldOpen()` 回傳 `!!compactHold && !compactHold.done`。
  - `holdUntil($, target, ms, signal)`：回傳 `'settled' | 'abandoned' | 'deadline' | 'failed'`。
  - `onPromptHold($, e, next)`，註冊成 `on('prompt.submit', { text: /^/ }, onPromptHold).catch(($, e, next) => next(e))`。

- [ ] **Step 1：測試環境補假的 sleep**

  `evWorld` 的 `process.run` 現在把所有 argv 交給 `w.pdx`，`/bin/sleep` 會立刻回 0，等待迴圈在 mock clock 上會空轉。照 `relay.test.ts` 的做法（約 163–233 行）：`W` 加 `sleeps: { argv: string[]; timeoutMs?: number }[]` 與可選的 `sleep?: (argv) => Promise<{ exitCode }>`；`argv[0] === '/bin/sleep'` 時記進 `w.sleeps`，預設 `w.clock.sleep(Number(argv[1]) * 1000)` 後回 `{ exitCode: 0 }`；其他照舊交給 `w.pdx`。再加一個實際計時的 `realSleep`（照 `relay.test.ts` 194 行）。

  跑 `pdx test mod-hooks`：除了 #3987 的 3 條，結果要跟改之前一樣。

- [ ] **Step 2：寫會紅的測試**

  放在 `events.test.ts` 的 compact context 區塊後面，用既有的 `compactOf`、`CTX_OK`、`APPENDED`。打字用 `const typed = ($, text) => $.prompt.submit({ text, wait: false, origin: { kind: 'composer' } })`（同 `relay.test.ts` 1554 行）。每條都要釘住「提示送到底下」（`w.submitCalls`）與「注入補上」（`w.appends`）的先後：在 `submit` 與 `append` 兩個假 hook 裡各推一筆到同一個 `order` 陣列。

  1. `a prompt typed while the compaction runs goes on after the recap is appended, unchanged`：壓縮用一個手動放行的 Promise 卡住；打字；`advance(100)` 後 `submitCalls` 是空的；放行壓縮；`advance(999)` 仍是空的；`advance(1)` 之後 `order` 是 `['append', 'submit']`，`submitCalls[0].text` 等於打的字，`appends` 是 `[APPENDED('RECAP')]`。
  2. `with no compaction open a prompt goes on at once and sleeps nothing`：沒有壓縮直接打字，`w.clock.settle()` 後 `submitCalls` 長度 1，`w.sleeps` 是空的。再做一次「壓縮已經補完之後才打字」，一樣不 sleep。
  3. `a compaction skipped beneath lets a held prompt go at once`：底下的 compact 回 `{ skip: 'no' }`，但先用手動放行卡住；打字；放行；`settle()` 後 `submitCalls` 長度 1，`appends` 是空的，時間不必推到 1000。
  4. `a member's idle compaction stopped by the relay holds nothing`：照既有 `a member’s idle compaction stopped by the relay appends nothing` 的 hello；壓縮被攔之後打字，`settle()` 就送出、`w.sleeps` 是空的。
  5. `an empty answer lets a held prompt go as soon as it is in`：`compactContext` 回 204，但用手動放行卡住；壓縮卡住期間打字；放行 daemon 的回答（壓縮仍卡住）；`settle()` 後 `submitCalls` 長度 1。
  6. `an append that never answers holds the prompt at most 60 s`：`w.append` 回一個永遠不 resolve 的 Promise；壓縮後打字；`advance(59_000)` 仍是空的；`advance(1_000 + 5_000)` 之後長度 1（最後一個 sleep 要跑完才會發現過了期限，所以多推一個 sleep 的長度）。另外斷言 `w.logs` 有一行包含 `held` 與 `deadline`。
  7. `a daemon that never answers lets the prompt go when the request gives up`：`compactContext` 回永遠不 resolve 的 Promise；壓縮後打字；在 `COMPACT_CONTEXT_WAIT_MS` 加上 `REQUEST_DEADLINE_MS` 之前仍是空的，之後（再加一個 sleep 的長度）長度 1，`appends` 是空的。兩個常數從 `workbook.js` import，不寫死。
  8. `a session that moved on lets a held prompt go at once`：`compactContext` 回永遠不 resolve 的 Promise（daemon 還沒回答）；壓縮後打字；照既有 `a session that moved on before the second is up appends nothing` 的流程 `/clear`（`end($, 'clear')`、換 `w.sid`、`classic.SessionStart`）；`settle()` 後 `submitCalls` 長度 1、`appends` 是空的，時間不推。
  9. `while the recap is pending an App submit is reported busy`：照 `while a turn runs a submit is reported busy at once` 的 `gated(PJOB())`；壓縮卡住時 `open()`；`settle()` 後 `submitCalls` 是空的、`pqResult(w)[0].body.status` 是 `'busy'`。另一條同樣的測試讓 job 在注入補上之後才 `open()`，回 `accepted`、`submitCalls` 長度 1（證明放行後不會一直回 busy）。
  10. `with the reporter off nothing is held`：`pdxJSON: PDX_JSON_OLD`，壓縮卡住時打字，`settle()` 就送出。
  11. `the hold outlives the 10 s hook budget on its local sleeps`（實際計時，`{ timeoutMs: 30_000 }`，照 `relay.test.ts` 1710 行）：`sleep: realSleep`；`w.append` 用 `setTimeout` 在實際 11.5 秒後才回來；打字；斷言提示在 append 回來之後才送到底下、文字不變、`w.sleeps.length >= 3`。
  12. Escape：先在 `claude-code/testing` 的型別裡找能不能放棄一個進行中的 `prompt.submit`（grep `abandon`、`signal`）。有就加 `an abandoned prompt is let go at once`；沒有就在 report 寫明，這條靠 code review 與 Task 4 的 Escape 檢查。
  13. `a prompt held over two compactions goes on after the second recap`：兩次壓縮各用手動放行卡住，`compactContext` 依第幾次回 `CTX_OK('R1')`、`CTX_OK('R2')`；第一次壓縮進行中打字；放行第一次、推 1000 毫秒（R1 補上），此時在第一次的補上之前讓第二次壓縮開始；推到第二次補上。`order` 是 `['append', 'append', 'submit']`，提示只送一次。
  14. `a sleep that fails lets the held prompt go and does not spin`：`w.sleep` 回 `{ exitCode: 1 }`；壓縮卡住時打字；`settle()` 後 `submitCalls` 長度 1、`w.sleeps.length === 1`。
  15. `a sleep that rejects lets the held prompt go`：`w.sleep` 回 rejected Promise；同上斷言。
  16. `a refused append lets the held prompt go`：`w.append` 回 `{ deny: 'no' }`；壓縮後打字；推 1000 毫秒後 `submitCalls` 長度 1。
  17. `an append that throws lets the held prompt go`：`w.append` 丟例外；同上斷言。

- [ ] **Step 3：`pdx test mod-hooks`，確認新測試紅（第 2、4、10 條可能本來就綠，沒關係，它們是護欄）。**

- [ ] **Step 4：實作**

  - `workbook.js`：`export const COMPACT_HOLD_MAX_MS = 60_000 // a prompt waits at most this long for the compact context (spec §6.5)`。
  - `compactInjectStart`：建 job 時一起建 `held` 與 `release`；設 `compactHold = job`；`text.then((t) => { if (!t) job.release() })`，回空時不必等到 1 秒的補上時間。
  - `compactAppendTick`：`compactAppend(...)` 的鏈尾加 `.finally(() => job.release())`。`compactAppend` 的每個提早 return（沒文字、gen 變了、reporter 關了、不是最新那次）因此都會放行。
  - `onCompact` 的 `finally`：`ok` 為假且有 job 時 `job.release()`。
  - `holdUntil`：照 `register.js` 的 `sleepUntil` 寫，但回傳原因字串，讓 log 看得出為什麼放行。註解寫明為什麼不能共用（M-U1-2）。
  - `compactHoldDrop()`：`compactHold` 未完成就 `release()`。在三處 `wb.gen += 1`（`startReporter`、`sessionSwitch`、`sessionEnd`）旁各呼叫一次：session 換了，舊 session 的注入不會再補。
  - `onPromptHold`：`ev.on` 為假或 `!compactHoldOpen()` 直接 `return next(e)`，不呼叫任何 `$`。否則記下開始時間與期限（開始＋`COMPACT_HOLD_MAX_MS`），迴圈：`while (compactHoldOpen())`，用剩下的時間 `await holdUntil($, compactHold.held, remaining, next.signal)`，結果不是 `'settled'` 就跳出（放棄、到期、sleep 失敗都直接放行）。這樣兩次壓縮重疊時，提示會依序等到最新那次的注入。結束後 `log($, 'prompt held <ms> ms for the compact context: <why>')`，再 `return next(e)`，`e` 原樣。
  - `pqSubmit`：`if (ev.turnId || compactHoldOpen()) return pqOut('busy')`，函式註解補一句：注入補上前送出的提示會排在注入前面（spec §4.3）。
  - `registerEvents`：`on('prompt.submit', { text: /^/ }, onPromptHold).catch(($, e, next) => next(e))`，放在 `session.compact` 那行後面。
  - 檔頭 M-U1-3 段落：「here those carry a matcher」那串事件已經包含 `prompt.submit`，補一句說明 events.js 現在也註冊它（帶 matcher，等注入）。壓縮注入區的區塊註解補一段：等待從什麼時候開始、哪些情況放行、上限多久。

- [ ] **Step 5：`pdx test mod-hooks`＋`pdx test mod-changed`，確認綠（#3987 的 3 條除外）。`mod-changed` 含 `embed_test.go` 的註冊規則。**

- [ ] **Step 6：commit** `fix(mod): hold a prompt until the compact context is in`

---

### Task 3: PR、review、合併、bump、部署

- [ ] PR 層測試：`pdx test go-changed -- ./internal/module/dialogue/`、`pdx test lint`、`pdx test mod-hooks`、`pdx test mod-changed`。記下 job id 與通過數。
- [ ] 開 PR，標題 `fix: end the compact context and hold prompts until it is in`，內文附 spec 段落、測試證據、#3987 的已知紅燈。
- [ ] codex R1（`review --base origin/main`）＋ R2 攻擊方 → critic，focus 點名：放行條件有沒有漏（Review Focus 2、3）、`release` 在所有路徑都會被呼叫、`pqSubmit` 的 `busy` 會不會讓 App 卡住、`holdUntil` 在 sleep 失敗時不空轉。
- [ ] 修完、增量 re-review、merge（merge commit），刪遠端分支。
- [ ] 單獨的 bump PR：`VERSION`、`package.json`、`spa/package.json`、`CHANGELOG.md`。不派 codex。
- [ ] 部署：照 `reference_pdx_daemon_runtime.md` 與 `feedback_deploy_gate_before_pdx_setup.md`。查閘門與停 daemon 分開做；`relays_active=0` 在重啟前與 `pdx setup` 前各查一次。mlab 與 air26 一起升，各留一份舊版備份。兩台都跑 `pdx setup`。
- [ ] 部署後確認：兩台 daemon 版本正確；mod 的 `events.js` 已經是新版（`~/.config/pdx/cc-plugin/purdex/hooks/events.js` 含 `onPromptHold`）。

---

### Task 4: 驗收（spec §11 第 2 項）

真機，模型 Haiku，**預設 tmux server** 上名字固定的拋棄式 session（daemon 不追蹤 `-L` 的 session，見 781 驗收）。只 kill 那一個 session，驗完收掉。

- [ ] 準備：把 `docs/specs/assets/2026-11-07-compact-control/trigger-mod/` 複製到 scratchpad 的工作目錄，`register.ts` 的 `DIR` 改成那個目錄。session 用 `claude --plugin-dir <觸發 mod> --model haiku --dangerously-skip-permissions` 啟動（Purdex mod 由 `pdx setup` 裝好，不另外指定）。
- [ ] 三種情況各跑 **3 次**，提示每次用不同的獨特字串：
  1. mod 觸發：`ctl.json` 設 `{"mode":"timer"}`，回合結束後觸發壓縮，壓縮開始約 3 秒後打一句要模型做事的提示。
  2. 手動 `/compact`，壓縮期間打字。
  3. 壓縮結束 10 秒後才送。
- [ ] 每次都記錄並判定：transcript 順序是「分界 → 注入 → 使用者提示」；注入最後一行是結束標記；模型照做；追問「剛才那句話是我打的，還是注入紀錄裡的？」時說是使用者打的；mod 的 debug log 有 `prompt held … ms` 那一行（第 3 種不應該有）。
- [ ] Escape（不算在 9 次裡）：手動 `/compact`，壓縮期間打一句；等觸發 mod 的 log 出現 `prompt.submit` 後立刻送 Escape（`tmux send-keys Escape`，等待窗口約 1–1.6 秒，用腳本在 log 出現時送）。判定：session 沒有卡住，下一句提示正常送出並有回答；mod 的 debug log 那一行的原因是 `abandoned`。沒抓到窗口就重試，最多 3 次，都沒抓到就照實回報。
- [ ] 9 次全部通過才算通過。任何一次拒絕或誤認，停下來回報，不要自己改程式。
- [ ] 結果寫進 `docs/specs/assets/2026-11-07-compact-control/probes.md` 新的一節「Phase 1 驗收」。
