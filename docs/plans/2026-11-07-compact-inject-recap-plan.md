# 壓縮時注入 recap — plan

> **For agentic workers:** 用 superpowers:subagent-driven-development 逐 task 實作（subagent 帶 `model: "sonnet"`）；步驟用 `- [ ]` 追蹤。

**目標**：每次壓縮後，agent 自動讀到最新 recap、recap 之後的內容與對話檔位置，畫面、對話檔、回合編號都不受影響。

**架構**：mod 在 `session.compact` 開始時向 daemon 要注入文字（不等回應），daemon 用既有 job 佇列產 recap、最多等 20 秒、組好文字回傳；mod 在壓縮 hook 回傳至少 1 秒後用 `$.session.append` 補進對話。

**技術**：Go（`internal/module/dialogue`、`internal/modevents`）、mod JS（`cmd/pdx/plugin/purdex/hooks`，`claude plugin test`）、SPA（vitest）。

**Spec**：`docs/specs/2026-11-07-compact-inject-recap-spec.md`（以下稱 spec）。

**流程**：完整開發流程。本 plan 帶 spec 給 codex 審一輪 → subagent TDD → PR → codex R1 ＋ R2（攻擊→反駁）→ merge → bump → 部署。一張 PR，每個 task 一個 commit。

**修訂 1**（codex plan review `task-mw5smcl4-4st74c`，5 條全數採納，都是局部補充，不再送審）：
- Task 3 用 `TurnNos` 過濾回合，recap 對應的回合找不到時才不會把舊回合當成「之後的內容」（第 1 條）；
- Task 2 補實際佇列的各種結束路徑（第 2 條）；
- Task 5 補執行器忙碌時的測試（第 3 條），並把定時器要呼叫的頂層函式寫明（第 4 條）；
- Task 8 補舊 mod 相容的驗證（第 5 條）。

**修訂 2**（PR #3986 的 codex R1 與 R2，取代下面相衝突的文字）：
- mod 在建立 job 時固定 `stream`、`sid`，送出前先確認 `wb.gen` 沒變（R1-1）；
- 有更新的一次壓縮已經結束時，舊的 job 就不補，Review Focus 1 的「兩次都各補各的」改成「最後留下的一定是最新的」（R1-2）；
- 路由不占 `activePolls`，用自己的上限 16，超過回 429（A-1）；
- 注入文字加第二行，講明是紀錄、不是新指示（A-2 的工具輸出部分經 critic 查證不成立：`renderStep` 不收工具結果；第二行處理的是過時的 `next` 和引用其他 session 的話）。

## Global Constraints

- 注入文字第一行固定是 `<injected by Purdex after compaction: not the user's words>`。
- daemon 等 recap 最多 **20 秒**；之後的內容上限 **8,000 字元**（rune），超過留最新並加一行 `<earlier turns omitted: see the dialogue file>`。
- mod 在壓縮 hook 回傳後**至少 1,000 毫秒**才補；hook 絕不等 daemon。
- recap 字數（提示／截斷）：`done`、`decided`、`waiting` 每條 60／80；`now` 120／160；`next` 120／160；`headline` 60／60。
- trigger 字串：`compact`。路由：`POST /mod/v1/dialogue/compact-context`。
- 只處理主對話（沒有 `agentId`）、非 `precompute`、reporter 開著（`ev.on`）。
- 測試分層：開發中只跑改到的（`pdx test go-pkg -- <套件>`、`pdx test spa-file -- <檔>`）；PR 前 `pdx test go-changed -- <套件>`、動到 goroutine 的 `pdx test go-race -- <套件>`、`pdx test lint`、`pdx test mod-hooks`、`pdx test mod-changed`、SPA `pdx test spa-related`＋`pdx test tsc`。完整套件留給發版。**tsc 不在 mlab 跑**（`pdx test tsc` 會送 mserv）。測試用的 tmux 一律 `-L <label>`，先 `unset TMUX`。只能 kill 自己的 pid。

## Review Focus

spec 沒寫明、但最可能出事的情況。每一條都在對應的 task 裡有測試：

1. **回合中連續壓縮**（實測 12–26 秒就一次）：第一次的回應還沒到，第二次已開始。兩次都要各補各的，不丟例外、不卡住（Task 5）。
2. **recap 排在待做的回合摘要後面**：等待要涵蓋前面那些 job，不是只看 recap 自己（Task 2）。
3. **單一段極長的 agent 文字**：上限在同一行中間切，中文不能切壞，recap 不受上限影響（Task 3）。
4. **第一次被記錄就壓縮**：`dlg_sessions` 還沒有這個 session（例如 `/resume` 舊對話）：沒有 recap、讀整份 transcript，只給最新內容（Task 3）。
5. **member 的閒置壓縮被 relay 擋下**（`register.js` 回 `skip`）：不補（Task 5）。

---

## Task 0：補實測（lead 自己做，不派 subagent）

手動、回合中自動、mod 觸發都已用 1 秒實測通過（紀錄在 `docs/specs/assets/2026-11-07-compact-inject-probe/`，spec §4.2）。

- [x] 閒置壓縮：探針閒置 68 分鐘沒有觸發，spec §4.2 已寫明改由部署後真實紀錄驗證（Task 9 的 3 天比對要分開算 `Compacted while idle`）。

## Task 1：放寬 recap 上限與 prompt

**Files:**
- Modify: `internal/module/dialogue/recap_validate.go`（常數與 `//go:embed`）
- Create: `internal/module/dialogue/prompts/recap-v2.txt`（內容＝本 plan 附錄 B-2）；Delete: `prompts/recap-v1.txt`
- Test: `internal/module/dialogue/recap_validate_test.go`

**Interfaces:** Produces 常數 `maxRecapItem = 80`、`maxRecapNow = 160`、`maxRecapNext = 160`（`maxRecapHeadline`、`maxRecapDone` 不變）。

- [ ] **改測試先紅**：`repairRecap` 的既有上限測試（第 65、68 行的 40）改成 80，補 `now` 160、`next` 160、`headline` 仍 60 的斷言；`planCodeBlock` 改讀本 plan（`../../../docs/plans/2026-11-07-compact-inject-recap-plan.md`），`TestRecapPrompt_MatchesPlanAppendix` 的失敗訊息改成 recap-v2。
- [ ] `pdx test go-pkg -- ./internal/module/dialogue/`，確認紅。
- [ ] 改常數、換 prompt 檔、改 `repairRecap` 的註解（「cut to 40」）。
- [ ] 跑同一指令，確認綠。
- [ ] commit：`feat(dialogue): longer recap limits, the prompt asks for less than the cut`

## Task 2：等 recap 完成

**Files:**
- Modify: `internal/module/dialogue/recap.go`、`internal/module/dialogue/store.go`
- Test: `internal/module/dialogue/recap_test.go`、`store_test.go`

**Interfaces:**
- Produces `func (r *recapper) wait(ctx context.Context, sid string, d time.Duration) bool`：這個 session 正在排隊或執行的 recap 結束（成功或失敗）就回 `true`；沒有在跑的也立刻回 `true`；`d` 到或 `ctx` 結束回 `false`。job 不受影響，照常跑完。
- Produces `func (s *Store) TurnIDByNo(sid string, no int) (string, bool, error)`。
- `request` 的 trigger 註解加上 `compact`。

做法：`inflight` 旁加 `done map[string]chan struct{}`，`request` 設定 inflight 時建立，`release` 時關閉並刪除。

- [ ] **寫測試先紅**（用 `newRecapRig`）：
  - `TestRecapWait_NothingInFlight`：回 `true`，不等。
  - `TestRecapWait_EndsWhenAnswered`：`request`→背景 `wait`→`take`＋`answer`→`wait` 回 `true`，之後 `LatestRecap` 查得到。
  - `TestRecapWait_EndsWhenTheJobFails`：job 失敗或被略過（`finish`）也會回 `true`。
  - `TestRecapWait_TimesOutAndTheJobGoesOn`：`d` 很短回 `false`；之後 `answer`，recap 照樣存進去。
  - `TestRecapWait_ContextEnds`：`ctx` 取消回 `false`。
  - `TestRecapWait_AfterTurnJobsAheadOfIt`（Review Focus 2）：佇列裡先有回合摘要 job、後有 recap，答完前者時 `wait` 還沒結束，答完 recap 才結束。
  - `TestRecapWait_SecondRecapHasItsOwnWait`：第一則結束後再 `request`，新的 `wait` 會等第二則。
  - 實際佇列的結束路徑，每條都要讓 `wait` 回 `true`，不直接呼叫 `finish`：`TestRecapWait_ChannelStop`（`ch.Stop`）、`TestRecapWait_CapabilityLost`（`setCapable(false)` 後 `sweepIncapable`）、`TestRecapWait_LeaseRunsOut`（取走不回，時鐘往前、`reapLeases`）、`TestRecapWait_HourlyCapDrops`（`callCap` 用完，`Next` 略過它）、`TestRecapWait_ApplyGivesUp`（`apply` 一直失敗到 `maxApplyRetries`）。
  - `TestStore_TurnIDByNo`：有、沒有。
- [ ] `pdx test go-pkg -- ./internal/module/dialogue/`，確認紅。
- [ ] 實作。
- [ ] 跑同一指令＋`pdx test go-race -- ./internal/module/dialogue/`，確認綠。
- [ ] commit：`feat(dialogue): wait for a session's recap to end`

## Task 3：組注入文字

**Files:**
- Create: `internal/module/dialogue/compact_context.go`、`compact_context_test.go`
- Modify: `internal/module/dialogue/module.go`（`Start` 把 `turns` 存進 `m.turns`，`Stop` 清掉）、`jobs.go`（`jobsService.CompactContext`）

**Interfaces:**
- Consumes `recapper.wait`、`Store.TurnIDByNo`（Task 2）；既有 `renderItems`、`recapText`、`recapOfRow`、`Store.LatestRecap`、`convturns.Reader.TurnsFrom`。
- Produces：
  ```go
  const (
      compactRecapWait   = 20 * time.Second
      compactReadTimeout = 8 * time.Second
      compactTailMax     = 8000 // runes
  )
  func (m *Module) compactContext(ctx context.Context, sid string, wait time.Duration) (string, bool)
  func capTail(s string, max int) string                 // 超過時留最新、從下一行開頭算起；整段沒有換行就硬切（以 rune 為單位）；前面加省略標記
  func (s jobsService) CompactContext(ctx context.Context, sid string) (string, bool) // 呼叫 m.compactContext(ctx, sid, compactRecapWait)
  ```

`compactContext` 的順序：
1. `id0 := 目前最新 recap 的 id`；`rc.request(sid, "compact")`；回 `queued` 才 `rc.wait(ctx, sid, wait)`。模組沒在跑（`m.rc`、`store` 或 `turns` 為 nil）回 `("", false)`。
2. `LatestRecap`。有的話 `fromID, _, _ := TurnIDByNo(sid, row.ToNo)`（找不到就是空字串，讀整份）。
3. `ctx` 加 `compactReadTimeout` 呼叫 `TurnsFrom(ctx, "claude", sid, fromID)`。失敗只記 log，之後的內容當作空的，recap 照給。
4. **用 `Store.TurnNos(sid)` 過濾回合，不靠 `Found`**：編號已知且 ≤ `to_no` 的丟掉，編號未知（還沒寫進對話檔）或 > `to_no` 的留下。這樣 `Found` 為 true 時 recap 的最後一回合會被丟掉，為 false 時（`TurnsFrom` 回整份）也不會把舊回合當成之後的內容。逐回合 `renderItems(t.Items)`，略過 `block.compact`，文字用空行接起來，再 `capTail`。
5. recap 與之後的內容都沒有就回 `("", false)`。否則組成：固定第一行 → 空行 → `recapText(row, out, false)`（有才放）→ 空行 → `<after recap | from turn N>`（N＝`to_no+1`；沒有 recap 時是 `<recent turns>`）＋內容（有才放）→ 空行 → `<dialogue: <w.root>/<sid>/>` ＋下一行 `編號最大的檔是最新一段，每段開頭有前一段的路徑。要查原話或舊決定時讀這裡。`
6. 記一行 log：`[dialogue] compact context session=%s recap=new|old|none waited=%dms tail=%d text=%d`（`new`＝最新 recap 的 id 跟 `id0` 不同）。

路徑用絕對路徑（跟對話檔的 `<prev:>` 一樣）。`ctx` 已結束時照樣回傳手上有的內容，由路由決定要不要送。

- [ ] **寫測試先紅**（`compact_context_test.go`，用 `newRecapRig` 加一個假 `turnsFromReader`）：
  - `TestCompactContext_NewRecapThenTheTurnsAfterIt`：seed 3 則摘要、transcript 有 t1–t5；背景答 recap。文字依序含固定第一行、`<recap … | turns 1–3>`、`<after recap | from turn 4>`、t4、t5 的內容、`<dialogue: …/<sid>/>`；不含 t3 的內容。
  - `TestCompactContext_UpToDateQueuesNothing`：recap 已涵蓋全部，沒有新 job，沒有 after 區塊。
  - `TestCompactContext_NotReadyInTimeUsesTheLastOne`：舊 recap 1–2，新的沒人答、`wait` 很短；文字是舊 recap，after 從 turn 3 起。
  - `TestCompactContext_UnavailableUsesTheLastOne`：`setCapable(false)`，立刻回，不等。
  - `TestCompactContext_NoRecapAtAll`：沒有 recap 區塊，有 `<recent turns>` 與內容。
  - `TestCompactContext_FirstTimeThisSessionIsSeen`（Review Focus 4）：`dlg_sessions` 沒有這個 session、transcript 很長：有 `<recent turns>`，長度在上限內。
  - `TestCompactContext_RecapTurnGoneFromTheTranscript`：有 recap 1–3，假 reader 回 `Found: false` 與整份 t1–t5：只有 t4、t5。
  - `TestCompactContext_RecapTurnNotInTheStore`：有 recap 但 `TurnIDByNo` 找不到：一樣只有 `to_no` 之後的回合。
  - `TestCompactContext_NothingToSay`：沒有回合、沒有 recap，回 `false`。
  - `TestCompactContext_RunningTurnIsIncluded`：最後一回合 `OutcomeRunning`、有還在跑的工具，內容照樣出現。
  - `TestCompactContext_SkipsTheBoundary`：回合裡有 `SystemCompacted`，輸出沒有 `<compacted`。
  - `TestCompactContext_ReaderFailsStillGivesTheRecap`。
  - `TestCompactContext_ModuleNotRunning`：回 `false`。
  - `TestCapTail`（Review Focus 3）：未超過不變；超過留最新且從行首開始；一整行 20,000 個中文字：結果 ≤ 上限加標記、`utf8.ValidString`、結尾是原文結尾；recap 不算在上限內（放進 `TestCompactContext_…` 斷言 recap 全文都在）。
- [ ] `pdx test go-pkg -- ./internal/module/dialogue/`，確認紅。
- [ ] 實作。
- [ ] 跑同一指令＋`pdx test go-race -- ./internal/module/dialogue/`，確認綠。
- [ ] commit：`feat(dialogue): what a compaction hands the agent`

## Task 4：路由

**Files:**
- Create: `internal/modevents/compact_route.go`、`compact_route_test.go`
- Modify: `internal/modevents/handler.go`（`NewHandler` 的 switch）

**Interfaces:**
- Produces：
  ```go
  const CompactContextPath = "/mod/v1/dialogue/compact-context"
  const compactContextDeadline = 35 * time.Second // 20 s 等 recap ＋ 8 s 讀 transcript ＋餘裕；server 的 WriteTimeout 只有 10 s
  type CompactContextService interface {
      CompactContext(ctx context.Context, sessionID string) (string, bool)
  }
  ```
- 沿用 `h.service()`，再 `svc.(CompactContextService)` 取介面，這樣不用多一條接線，也不用動現有的假服務。

行為：`POST {stream, session_id}`。`200 {"text": …}`；沒有內容、stream 不是這個 session 自己的、或 `ctx` 已結束都回 `204`；格式錯 `400`；`service()` 為 nil 或沒實作介面 `503`；超過 `maxPolls` `429`。用 `pollContext`、`SetWriteDeadline(now + compactContextDeadline)`、算進 `activePolls`，**不經 `polls` gate**：同一個 stream 的 `next` 長輪詢要同時進行，job 才拿得到。

- [ ] **寫測試先紅**（`compact_route_test.go`）：
  - `TestCompactContext_Validation`：壞 stream、壞 session id、非 POST。
  - `TestCompactContext_OnlyTheSessionsOwnStream`：別的 stream 回 204，服務沒被呼叫（照 `TestWorkbookNext_OnlyTheSessionsOwnCapableStreamMayPoll`）。
  - `TestCompactContext_TextOr204`。
  - `TestCompactContext_ServiceWithoutItIs503`：只實作 `WorkbookService` 的假服務。
  - `TestCompactContext_RunsBesideAWaitingNext`：同一 stream 有 `next` 卡著時也會進服務。
  - `TestCompactContext_TheWriteDeadlineIsExtended`：服務等超過 10 秒的情境，照 `TestWorkbookNext_TheWriteDeadlineIsExtended` 的做法。
  - `TestCompactContext_StopEndsTheWait`：`WithStop` 關閉時服務的 `ctx` 結束，回 204。
- [ ] `pdx test go-pkg -- ./internal/modevents/`，確認紅。
- [ ] 實作。
- [ ] 跑同一指令，確認綠。
- [ ] commit：`feat(modevents): the compact-context route`

## Task 5：mod 注入

**Files:**
- Modify: `cmd/pdx/plugin/purdex/hooks/workbook.js`（純函式與常數）、`events.js`（用 `$` 的部分，規則見檔頭 M-U1-2）
- Test: `cmd/pdx/plugin/purdex/hooks/events.test.ts`

**Interfaces:**
- Produces（`workbook.js`）：`COMPACT_CONTEXT_URL = 'http://pdx/mod/v1/dialogue/compact-context'`、`COMPACT_CONTEXT_WAIT_MS = 30_000`、`COMPACT_APPEND_DELAY_MS = 1000`、`compactContextBody(stream, sessionId)`、`parseCompactContext(res) → string`（不是 200、JSON 壞、`text` 不是非空字串都回 `''`）。
- Produces（`events.js`，都是**頂層函式**；M-U1-2 規定定時器的 arrow 只能呼叫一個本檔頂層函式）：
  - `compactInjectStart($, e)`：主對話、非 `precompute` 才動作，否則回 `null`。建立 `job = { gen: wb.gen, text: Promise<string>, settle }`（`settle` 是 Promise 的 resolve），排 `$.clock.after(0, () => compactFetchTick($, job))`，回傳 `job`。
  - `compactFetchTick($, job)`：先 `wbAsk($, WAIT_MS)` 讓執行器去取 recap job，再 `void compactFetch($, job).catch(...)`。
  - `async compactFetch($, job)`：`wbRequest($, COMPACT_CONTEXT_URL, body, COMPACT_CONTEXT_WAIT_MS)`，把 `parseCompactContext` 的結果交給 `job.settle`；任何失敗都 `settle('')` 並記 log。
  - `compactInjectLater($, job)`：只排 `$.clock.after(COMPACT_APPEND_DELAY_MS, () => compactAppendTick($, job))`。
  - `compactAppendTick($, job)`：`void compactAppend($, job).catch(...)`。
  - `async compactAppend($, job)`：`await job.text`，`text` 非空、`job.gen === wb.gen`、`ev.on` 才 `$.session.append({ message: { type: 'user', content: [{ type: 'text', text }] } })`。被拒或丟例外只記 log，不重試。
- `onCompact` 改動：`compactStarted` 之後呼叫 `compactInjectStart`；`next(e)` 回來且 `ok` 才呼叫 `compactInjectLater`。**回傳值原封不動，hook 不等 `job.text`。**

測試世界 `evWorld` 要加：`ctxReqs`、`compactContext: (body, n) => Answer | Promise<Answer>`（預設 204）、`appends`（`on('session.append', …)` 記下來）、`http.fetch` 的 `/mod/v1/dialogue/` 分支。

- [ ] **寫測試先紅**：
  - `a compaction asks for the context and appends it a second after the hook returned`：`advance(999)` 還沒有、`advance(1)` 之後有；形狀與文字正確。
  - `the compaction does not wait for the daemon`：daemon 不回應時 `await c` 照樣完成；之後放行才補。
  - `an answer that comes before the compaction ends still waits the second`。
  - `the hook hands back what the compaction returned, unchanged`。
  - `a subagent's compaction and a precompute ask for nothing`。
  - `a compaction skipped beneath appends nothing`。
  - `a member's idle compaction stopped by the relay appends nothing`（Review Focus 5；hello 回 `role: member`、`idle_intercept: 1`）。
  - `204, an error status, broken JSON and a refused fetch append nothing and the compaction stands`。
  - `a session that moved on before the second is up appends nothing`（`end($, 'clear')`）。
  - `with the reporter off nothing is asked`（`PDX_JSON_OLD`）。
  - `the executor is told to look for the recap job`：壓縮開始後有 `workbook/next` 請求。
  - `a busy executor does not hold the compaction or the append`：壓縮開始時執行器正在跑一個模型 job（`w.model` 不回）：壓縮照常完成；daemon 回舊 recap 後照常補；模型 job 結束後執行器會再問一次 `workbook/next`（recap job 才拿得到）。
  - `a refused append is logged and not tried again`。
  - `two compactions in a row each append their own`（Review Focus 1）：第一次的回應在第二次開始後才到。
- [ ] 跑 `pdx test mod-hooks`，確認新測試紅。
- [ ] 實作。
- [ ] 跑 `pdx test mod-hooks` ＋ `pdx test mod-changed`，確認綠（`embed_test.go` 只檢查註冊與 M-U1-3）。
- [ ] 跑 `claude plugin validate cmd/pdx/plugin/purdex`，沒有新的拒絕項目。M-U1-2 測試抓不到，要靠這裡和 Task 9 的真機載入。
- [ ] commit：`feat(mod): put the compact context into the conversation after a compaction`

## Task 6：釘住 meta 列不算回合

這是 spec 選 B 做法的前提，現有程式已經符合，所以這個 task 不會先紅，改用突變驗證。

**Files:** Test: `internal/convmodel/ccnorm/plugin_submit_test.go`

- [ ] 加 `TestPluginAppend_IsNotATurn`：用 `pluginRow` 的寫法組真實形狀（`isMeta: true`、`origin: {kind: "plugin", name: "purdex"}`，形狀見 Task 0 的紀錄）。放在兩個回合之間與壓縮分界之後各一次：回合數不變、沒有任何 item 含它的文字。
- [ ] 加 `TestPluginAppend_DialogueFileHasNoTrace`（`internal/module/dialogue/writer_test.go`）：走真實解析後寫對話檔，檔案不含注入文字，`next_turn_no` 不變。
- [ ] `pdx test go-pkg -- ./internal/convmodel/ccnorm/ ./internal/module/dialogue/`，確認綠。
- [ ] 突變：暫時拿掉 `user.go` 的 meta 判斷，確認兩條都紅，再還原（一次一條、`git status` 前後為空）。
- [ ] commit：`test(ccnorm): a row a mod appends is not a turn`

## Task 7：App 標籤

**Files:**
- Modify: `spa/src/components/recap/RecapView.tsx`（第 197 行）、`spa/src/locales/zh-TW.json`、`en.json`
- Test: `spa/src/components/recap/RecapView.test.tsx`

**Interfaces:** `recap.trigger.compact`：zh-TW「壓縮時」、en `on compaction`。`every30` 用現有的 `recap.trigger.auto`。其他字串照舊顯示原文。

- [ ] **寫測試先紅**：`every30` 顯示「自動」、`compact` 顯示「壓縮時」、不認得的字串照原文。
- [ ] `pdx test spa-file -- src/components/recap/RecapView.test.tsx`，確認紅。
- [ ] 實作。
- [ ] 跑同一指令＋`pdx test spa-related`＋`pdx test tsc`，確認綠。
- [ ] commit：`fix(spa): name every recap trigger`

## Task 8：拿掉 `/workbook refresh`

**Files:** Modify: `cmd/pdx/plugin/purdex/hooks/workbook.js`、`events.js`、`events.test.ts`、`relay.test.ts`（若有引用）

拿掉：`workbookCommand`、`WORKBOOK_USAGE`、`$.command.register({ name: 'workbook' … })`、`on('command.run', { command: 'workbook' }, …)`、`REFRESH_URL`、`refreshBody`、`refreshNotice`、`forkRequest`、`wbOrphaned`、`wb.orphan`、`ORPHAN_MAX_MS`、`DEFAULT_FORK_TIMEOUT_MS`、`MAX_FORK_TIMEOUT_MS`、`wbRun` 的 fork 分支、`CAPS` 裡的 `workbook.refresh`、`REASONS` 的 `nothing-to-fork`，以及它們的測試與 `evWorld` 的 `fork`、`forkCalls`、`wbRefresh`。

保留：daemon 的 `/mod/v1/workbook/refresh` 路由與 `CapWorkbookRefresh`（舊 mod 還在跑時用）。

- [ ] **改測試先紅**：`/workbook` 不再註冊；`refresh` 這種 job 回 `refused`、不呼叫 `$.model.fork`；`CAPS` 只有 `workbook.v2`、`prompt.v1`。
- [ ] 跑 `pdx test mod-hooks`，確認紅。
- [ ] 刪除。`grep -n 'fork\|refresh\|orphan' cmd/pdx/plugin/purdex/hooks/events.js cmd/pdx/plugin/purdex/hooks/workbook.js` 應該沒有殘留（`job.kind` 不認得的路徑除外）。
- [ ] 跑 `pdx test mod-hooks` ＋ `pdx test mod-changed` ＋ `pdx test go-pkg -- ./internal/modevents/`，確認綠。最後一項裡的 `workbook_refresh_test.go` 釘住舊 mod（只宣告 `workbook.refresh` 的 stream）還能 poll `next`。
- [ ] commit：`refactor(mod): drop /workbook refresh`

## Task 9：PR、發版、真機驗收（lead）

- [ ] PR 層測試：`pdx test go-changed -- ./internal/module/dialogue/ ./internal/modevents/ ./internal/convmodel/ccnorm/`、`pdx test go-race -- ./internal/module/dialogue/ ./internal/modevents/`、`pdx test lint`、`pdx test mod-hooks`、`pdx test mod-changed`、`pdx test spa-related`、`pdx test tsc`。
- [ ] 開 PR；codex R1 ＋ R2（攻擊→反駁）；處理 finding；合併；獨立 bump PR 寫 CHANGELOG（daemon、mod、SPA 都有改；要跑 `pdx setup`、要重新打包 Mac App）。
- [ ] 發版閘門：#3979 若還沒修，spa-full、tsc-force 跑不成，先問使用者。
- [ ] 部署：daemon 兩台 → `pdx setup` 兩台（照既有閘門，`relays_active=0` 前後各查一次）→ 打包 Mac App。
- [ ] **真機**：用拋棄式 Haiku session（isolated tmux）各跑一次手動、自動（回合中）、mod 觸發（用探針 mod 的 `ctl.json` 觸發）：
  - transcript 裡注入列在分界之後、`isMeta: true`；
  - 請模型引用注入文字裡的 headline，引得出來；
  - 畫面看不到；對話檔沒有；`next_turn_no` 不變；
  - daemon log 有 `compact context` 那行。
- [ ] **失敗情境**：複製一份 mod，把 `pdx.json` 的 `mod_socket` 指到不存在的路徑，或指到一個只接受連線不回應的 socket，各 `/compact` 一次：壓縮照常完成、沒有注入列。
- [ ] **部署後 3 天**：一次性腳本（放 `~/.local/share/purdex-lead/deploy/`，不進 repo）比對真實 transcript，每個壓縮分界後面有沒有注入列，依來源分開算，閒置看 `Compacted while idle`。低於 95% 回頭查時機。人工看 5 個真實 session 壓縮後的第一個回應。把結果回報使用者，並問要不要關自動接力。

## 附錄 B-2 recap 提示（recap-v2）

`prompts/recap-v2.txt` 逐字等於下面這段，`TestRecapPrompt_MatchesPlanAppendix` 會比對。

### B-2 recap-v2

```
你是 recap 撰寫員。你會收到一個 AI 開發 session 最近的回合摘要（最多 30 則，舊到新）。請寫一則 recap，讓離開一陣子的人或接手的 agent 一眼看懂這段時間的近況。只根據這些回合摘要寫，摘要裡沒有的事不要推測。

輸入是一個 JSON：
- turns：每則 {n, at, title, summary}；n 是回合編號，at 是時間（HH:MM）

輸出六個欄位：
- headline（60 字以內）：一句話，這段時間最主要的事與現在的狀態
- done：這段時間做了什麼，3 到 6 條，每條 60 字以內，重要的放前面；同一件事的多個回合合成一條
- now：現在在做什麼、做到哪（120 字以內）
- decided：這段時間定下來的決定，每條 60 字以內；沒有就空陣列
- waiting：在等使用者回答或決定的事，每條 60 字以內；沒有就空陣列
- next：下一步（120 字以內）

寫法規則：
- 繁體中文。PR 編號、版本、指令名保留原文。
- 不寫 commit sha、行數、檔名、函式名。
- 提到人用角色：lead、member、使用者，或對方的名字。不用「你」「我」。
- 不寫秘密，不貼程式碼。
- 已經做完或已經放棄的事不要放進 now、waiting、next。

輸出格式：
{"headline": "…", "done": ["…"], "now": "…", "decided": ["…"], "waiting": ["…"], "next": "…"}

只輸出一個 JSON 物件，不要其他文字、不要 code fence。
```
