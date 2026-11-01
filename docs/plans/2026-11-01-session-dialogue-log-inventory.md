# 工作簿模組拆除盤點（唯讀）

範圍：`/Users/wake/Workspace/wake/purdex/.claude/worktrees/session-dialogue-log`（分支 worktree-session-dialogue-log，HEAD 16990529d）。
路徑除特別註明外，皆相對於 repo 根。`WB` = `internal/module/workbook`。「未確認」表示沒有逐行驗證。

## 1. internal/module/workbook 非測試 .go 檔歸類

共 36 個非測試 .go、45 個測試 .go、prompts/ 6 檔、testdata/ 1 檔。歸類統計：保留 1、要改 14、拿掉 21。

### 1.1 保留

| 檔 | 行 | 歸類 | 理由 |
|---|---|---|---|
| lines.go | 123 | 保留（資料來源要接新儲存） | `PushLines`（:26）、`Await`（:87）、`wake`/`intakeDone`/`snapshot` 都是推播等待邏輯，與 entries 無關。唯一依賴 Store 的是 :96 `p.store()` 與 :100 `st.ClosestTurn`，以及 :105-111 讀 `Entry.PushReadyAt/Push/Thing/State/ConvKey/ID` 組 Line；新儲存要提供等價查詢。測試：lines_test.go |

### 1.2 要改（派工通道與工作簿資料混在一起）

| 檔 | 行 | 保留部分 | 拿掉部分 |
|---|---|---|---|
| module.go | 277 | `Name`:49、`Init` 註冊 `JobsKey`:57 與 `workbooklines.Key`:59-60；`live`:105、`Start`:117 的 engine 啟動路徑、`startEngine`:159（Turns/Lineage/Seats/Capable 接線:162-179）、`SubscribeTurnEnd` 訂閱:201-208、`Jobs`:218、`Stop`:239、`Close`:268 | `journalRun/journalOff/gitInterval/projectNameFn/remoteURLFn/skipMu/skipLogged` 欄位:28-45；`Dependencies` 的 `peers`（只為 journal）:53；`Status` 的 journal 旗標:86-91；`journalDisabled`:94；`RefreshSessions`:181-188、`OnAvailability`:190、`PersonName`:191-195、`prepareJournal`:197、`jr.start/stop`；`SetObserver(m.announce)`:76 與 `broadcast`:70-75；`WritePromptFiles`:142（見 prompt.go）；`FailPending`:121（entries 專用，新儲存自訂） |
| engine.go | 147 | `Deps` 的 Store/Turns/Lineage/Seats/Capable/HostID/Now/After/Logf（:44-50,66-68）；常數 catchUpWindow..maxWaitingTurns、defaultCallCap、leaseSlack、completeTimeout、reapEvery（:28-38,40）；Engine 的 life/qmu/intakeMu/waiting/convs/leases/seq/hour*/callCap（:73-94）；`waiter`/`SetWaiter`:98,125；`notifyLine`:127；`capable`:137；`convKey`:142 | `journalTimeout`:39；`Deps.Journal/OnJournalResult/PersonName`:52-59；`RefreshSessions/OnAvailability`:61-64；`rf refreshState`:100；`orphans/omu`:84-85（見 settle.go）；`NewEngine` 的 `EnableJournalOutbox`:114-116；`pushLine`/`SetPushLineHook`:95-97,121（未見外部使用，未確認）；`Reason*` 中 Model/Refused 等看新設計取捨（:14-23） |
| queue.go | 561 | `SystemBlock/Complete`:30-43；`Job`:52-57、`MarshalJSON` 的 complete 分支:73-77；`Result`:81-89；`JobSource`:93-100；`job`/`lease`/`convQ`:103-143（去掉欄位）；`newJobID`:147、`queueOf`:153、`signal`:162、`enqueue`:168-203（含 maxWaitingTurns 丟舊）、`isFresh`:207、`admit`:236（每主機每小時上限）、`Next`:264、`release*`:419-451、`reap`:454、`Result`:481、`Stop`:512、`RunReaper`:545 | `JobRewrite/JobRefresh/JobJournal`:16-23；`Fork`:46-49；`MarshalJSON` journal→turn 轉換:63-65 與 refresh 分支:66-72；job 欄位 `entry/out/usage/lat/then/jctx/person`:112-125（usage/lat 若新設計仍要記才留）；`convQ.noCapSince`:142；`finishUnrun` 的 journal/refresh/rewrite 分支:211-221；`finishCut`:227；`capReached`/`admitHourRoll`:252-261（只被 apply 的 rewrite 用）；`Next` 內 refresh/journal 分支:277,289-291,293,299-303,325-337；`buildCall` refresh 部分:367-382；`buildComplete` 的 journal/rewrite/entries-todos 取料:386-415；`reap` 內 `refreshWaiting/failStaleRefreshHeads`:456-458；`RunReaper` 內 `OnAvailability`:554-558 |
| intake.go | 335 | 全檔以 TurnEndEvent→picks 為主，邏輯可沿用：`OnTurnEnd`:28、`catchUp`:49-138、`pickTurns`:142、`adoptEvent`:235、`lastWords/summarisable/turnEndedAt/fallbackPick`:290-335 | 寫入端綁 entries：`Entry{...PromptVer}`:110、`SeatOf` 填 TeamID/Role:111-117、`InsertPending`:121、`Finish(...ReasonNoMod)`:130；這幾處要換成新儲存 |
| input.go | 160 | 輸入限制常數 maxUserPrompt/maxAssistantText/maxTools:14-18；`buildTurnPart`:91-114（redact 後的當回合文字）、`cutEllipsis`:154；`BuildTurnInput`:60 骨架 | `turnInput` 的 previous_status/recent_entries/open_todos/dropped_titles:32-38；`recentEntry/openTodo`:40-49；`TurnSource.PreviousStatus/Recent/Open`:24-29；`numberTodos`:116；`BuildRefreshInput`:131-152；`maxRecentEntries/maxPromptTodos`:19-20 |
| prompt.go | 98 | `writeOwnerOnly`:72（若新 prompt 仍要寫檔；`m.prompts` 在 module.go 之外無使用，見 §7）、`//go:embed` 機制 | `RewritePrompt`:22、`RefreshPrompt`:25、V1 兩個:31,34；`PromptFiles`:37、`WritePromptFiles`:43 寫三份檔；`SystemPrompt`:19（內容含 todo/status 規則，新 prompt 要重寫）；`PromptVersion`:12 |
| validate.go | 308 | `objectOf`:55、`hasDuplicateMember`:185、`stripFence`:210、`ParseModelJSON`:102 骨架（單一 JSON 物件、不得重複欄位、不得多餘 token）、`cutRunes/cutAtSentenceEnd/isSentenceEndAt/isPunctAt`:264-308、`repairPush`:251、`Summary` 的 skip/thing/push（:36-45）、`maxThing/maxPush`:16-17、`redact.String` 用法 | `ModelTodos`:29、`checkTodos`:71、`RefreshResult`:48、`ParseRefreshJSON`:130、`RepairRefresh`:151、`redactTodos`:157、`ResolveTodos`:167、`Repair` 的 entry/status/rewrite:235-245、`CutEntry`:247、`maxEntry/maxStatus`:18-19、`Summary.Entry/Status/ThingDone/Todos`、七欄必填檢查:107-111 |
| outcome.go | 154 | `apply` 骨架（usage/latency 累計:8-11、`!Answered`→`failedCall`:21、`ParseModelJSON`:24、`skip`）、`failedCall`:94-121、`formatFailure`:124（重試一次 attempt 2）、`logKind`:144 | `JobRewrite/JobRefresh/JobJournal` 分派:12-20、`FinishSkippedV2`/`SetPushLineV2` 寫 entries/status/todos:29,44、`journalAfter`:39,59,67,70、rewrite 分支:57-70、`finishOK`:73、`applyRewrite`:81、`logTodos`:134；`nothing-to-fork`:114 |
| jobs_service.go | 100 | `JobsKey`:12、`CapV2`:15、`jobsService`:19-30（`Ready/NextJob/JobResult/JobWaiting`:32-79 的 NextJob/JobResult/JobWaiting 部分）、`Engine.JobWaiting`:84-100 的佇列檢查 | `RequestRefresh`:64-80（對應 `modevents.WorkbookService.RequestRefresh`，見 §3）；`JobWaiting` 內 `refreshCapable`:91、`q.waiting[0].kind != JobRefresh`:99 |
| settle.go | 43 | `orphan/retryOrphans` 的概念（結果寫入失敗要重試） | 目前呼叫 `Store.Finish`/`ReasonStore`:3-6,14,37；跟 entries 狀態機綁死，新儲存要重寫（`retryOrphans` 被 `reap`:455 呼叫） |
| store.go | 437 | `Store`/`OpenStore`:88-126（sqlite、0600、WAL）、`wmu` 序列化寫入、`scanner` 等小工具 | `Entry`:32、`Output`:71、`StatusRow`:79、`InsertPending/SetPushLine/Finish/FailPending/Entry/NewestTurn/EntrySessions/FirstTurnAt/ClosestTurn/Conversation/HasEntries/Known/Entries/RecentForPrompt/SetStatus/Status`:164-437；`journalOn`:95、`obs/failRepoint/afterCommit`:92-94；`schemaVersion = 4`:19 |
| migrate.go | 185 | 遷移框架：`migrate`:31、`retryBusy`:50、`runStep`:86、拒絕較新版本（測試 `TestMigrate_RefusesANewerVersion`）；`init()`:25-29 會 panic 若 `len(migrations) != schemaVersion` | `migrateV1`:128（wb_entries/wb_status）、`migrateV2`:164（kind/usage 欄與 wb_todos）。舊資料不刪不遷移，所以步驟必須保留才讀得回舊檔；若新功能另開新檔則整個 migrate*.go 可一起拿掉（設計決定，未確認） |
| migrate_v3.go | 32 | — | journal 用表：wb_journal_outbox/units/cursors、wb_todos.seq；若沿用同一個 workbook.db 就必須保留版本鏈 |
| migrate_v4.go | 12 | — | 放寬 outbox 唯一索引；同上 |

### 1.3 拿掉

| 檔 | 行 | 理由 |
|---|---|---|
| api.go | 481 | `/api/workbook/*` 四條路由 :152-157、conversations/todos/entries handler、`entryJSON/todoJSON`、`announce` 產生 `workbook.entry/todos/status` 事件 :448 |
| todos.go | 367 | wb_todos 全部（`Todo`、`applyTodoChanges`:136、`SetPushLineV2`:236、`FinishSkippedV2`:279、`TodoChangesByEntry`:320）。注意 `SetPushLineV2` 是目前「結果寫入」的實作，也順手寫 wb_status:248-256，新儲存要有等價的「寫推播行＋標 push_ready_at」 |
| store_refresh.go | 117 | refresh job 的 `PendingRefresh/RepointSession/FinishRefresh/RefreshLine` |
| refresh.go | 251 | refresh job 全部（`RequestRefresh`:88、`SweepAvailability`:230、`CapSession`:33、`refreshTimeout`:16） |
| observer.go | 70 | 只服務 entry/status/todos 事件廣播（`Event`、`SetObserver`、`emit*`）。若新功能不需要 host event 即可整檔拿掉；`Store.emitEntry` 被 InsertPending/Finish 呼叫，隨 store.go 改寫 |
| ingest_tasks.go | 660 | team 任務鏡像（journal） |
| journal_api.go | 474 | `/api/journal*` handlers |
| journal_drain.go / journal_drain_tasks.go | 499 / 99 | outbox 排空寫 journal 檔 |
| journal_home_check.go | 62 | journal home 檢查 |
| journal_input.go | 249 | journal job 輸入 |
| journal_job.go | 143 | journal job（`journalAfter`:45、`buildJournal`:71、`applyJournalJob`:102、`followAfterLoss`:62 —— 注意 `followAfterLoss` 被 queue.go:318,475 呼叫，見 §7）；內嵌 `prompts/journal-v1.txt` |
| journal_members.go | 129 | `/api/journal/members` |
| journal_notify.go | 74 | journal.changed 事件 |
| journal_outbox.go / journal_outbox_store.go | 163 / 96 | outbox；`turnOutbox`:118 被 todos.go:262,300、store_refresh.go:79 呼叫 |
| journal_parse.go | 172 | journal job 回答解析 |
| journal_rules.go | 320 | journal 規則 |
| journal_sessions.go | 291 | `/api/journal/sessions` |
| journal_tasks_store.go | 97 | journal 任務表 |
| journal_wire.go | 249 | `prepareJournal`:198、`journalRun`:164（drainer/mirror/git loop 啟停） |

### 1.4 測試檔、prompts、testdata

- 對應「保留」：lines_test.go。
- 對應「要改」：engine_test.go、queue_test.go（含 refresh/journal 情境，要刪減）、intake_test.go、input_test.go、outcome_test.go、validate_test.go、validate_v2_test.go、prompt_test.go（比對 `docs/specs/2026-10-10-session-workbook-prompt-v2.md` 的圍欄區塊 :30、檔名 :79,97）、module_test.go、close_test.go、store_test.go、migrate_test.go、migrate_v3_test.go、migrate_v4_test.go。
- 對應「拿掉」：api_test.go、api_v2_test.go、api_refresh_test.go、observer_test.go、todos_test.go、refresh_test.go、refresh_cursor_test.go、ingest_tasks_test.go，以及 `journal_*_test.go` 共 22 檔。
- prompts/（行數為換行數）：`system-v2.txt` 72（turn prompt，含 todo/status 規則，要改寫）、`system-v1.txt` 43（歷史，拿掉）、`rewrite-v1.txt`/`rewrite-v2.txt`（單行，拿掉）、`refresh-v2.txt` 18（拿掉）、`journal-v1.txt` 42（拿掉）。
- testdata/：`journal-prompt-doc-block.golden.txt` 42，拿掉（只被 journal_job_test.go:305 使用）。

## 2. 派工通道實際流程

### 2.1 呼叫鏈（TurnEnd → 推播 Await）

1. 事件來源：agent 模組在 Stop hook 被接受後 `publishTurnEnd`（`internal/module/agent/turn_end_hub.go:152`）→ hub `publish`:98 → 訂閱者；`TurnEndEvent{SessionID, Text, At, Seq, Failed}` 定義 :20-26；`SubscribeTurnEnd`:118。
2. 訂閱：`WB/module.go:201-208` `startEngine` 取 `agent.TerminalSessionsKey` 服務，`SubscribeTurnEnd(eng.OnTurnEnd)`。
3. `WB/intake.go:28` `OnTurnEnd` → `catchUp`:49。取 `convKey`:89（`engine.go:142` 經 `team.LineageRootResolver`）；`pickTurns`:94 讀 transcript（`convturns.Reader`）決定要記哪些回合（最多 `catchUpKeep`=3，`engine.go:29`）；transcript 未就緒會 `e.d.After` 重排:103。
4. 每個 pick：`InsertPending`（`intake.go:121`，`store.go:164`）→ 不可用 mod 時直接 `Finish(skipped, no_mod)`:129-135 → 否則 `enqueue(&job{kind: JobTurn, attempt: 1, turn: p.turn})`:136。
5. 入佇列：`queue.go:168` `enqueue`，產 `wbj-` job id:175，掛在 per-conv 佇列 `q.waiting`:177；`isFresh` 的 turn 超過 `maxWaitingTurns`(3) 則丟最舊的並 `finishUnrun(skipped, backlog)`:179-201。
6. 通知 mod 有 job：mod 每批 events 的回覆由 `internal/modevents/handler.go:176-179` 在 `svc.JobWaiting(sid)` 為真時回 `{"ack":…, "workbook":true}`；`JobWaiting` 實作 `WB/jobs_service.go:84-100`。另外 mod 自己在 turn.complete 時 `wbAsk($, WAIT_MS)`（`cmd/pdx/plugin/purdex/hooks/events.js:813`，`shouldAsk` 在 `workbook.js:36`）。
7. mod 取 job：`events.js` `wbLoop`（約 :347）→ `wbRequest(NEXT_URL, nextBody(...))` → `POST /mod/v1/workbook/next`；daemon `internal/modevents/workbook_routes.go:175` `workbookNext` 驗證 stream 能力 `streamMayPoll`:121 與 poll 上限 → `svc.NextJob`（`WB/jobs_service.go:32`）→ `Engine.Next`（`queue.go:264`）。
8. `Next` 內：檢查 `capable`:265、每主機每小時上限 `admit`:293（超過 → `finishUnrun(skipped, cap)`:311-313）、建租約 `lease`（timeout = `completeTimeout` 30 s + `leaseSlack` 10 s）:297-307、組 job 內容 `buildCall`:315 → `buildComplete`:386、確認租約仍有效:340-349 後回 `Job{ID, Kind, Complete}`:351。
9. mod 執行：`events.js` `wbRun`（約 :382）以 `completeRequest(job.complete)`（`workbook.js:57`）驗證後呼叫 `$.model.complete`（`events.js` 約 :404），完成後 `resultBody`（`workbook.js:114`）→ `POST /mod/v1/workbook/result`（`events.js` 約 :417）。（events.js 行號以 grep 的 `wbAsk` :313 與 :813 為準，wbLoop／wbRun 內部行號為 sed 位移推算，未逐行再核）
10. daemon 收結果：`workbook_routes.go:232` `workbookResult` → `svc.JobResult`（`jobs_service.go:44`）→ `Engine.Result`（`queue.go:481`）：核對 stream 與租約:484-488、`apply`:494。
11. `apply`（`outcome.go:6`）：未回答 → `failedCall`:22；回答 → `ParseModelJSON`:24（格式錯 → `formatFailure` 重試一次:26）；`skip` → `FinishSkippedV2`:29；否則 `Repair`:43 → `SetPushLineV2`（`outcome.go:44`；`todos.go:236`）一個交易寫 thing/push/`push_ready_at`/usage、wb_status、todos → `notifyLine(entryID, true)`:55 → 視需要排 rewrite job:61-70 或 `finishOK`:58。
12. 喚醒推播：`engine.go:127` `notifyLine` → `PushLines.wake`（`lines.go:42`）。另 `catchUp` 結束時 `waiter.intakeDone`（`intake.go:62-66`，`lines.go:51`）告知「這個事件看過了」。
13. 推播端：Stop/StopFailure push 先 `holdForWorkbook`（`internal/module/push/agent_trigger.go:255`）→ `workbookWait` 讀 `push_wait_s`:284-304、`workbookLines` 由 registry 取 `workbooklines.Key`:307-319 → `lines.Await(ctx, sid, since, deadline)`:268；`since` = frame `BroadcastTs`/1e6:266。`PushLines.Await`（`lines.go:87`）呼叫 `ClosestTurn`:100（turn_at 在 [since-2000, since+500]）→ 回 `workbooklines.Line{Thing, Push, ConvKey, EntryID}`:109。

### 2.2 (a) prompt 與輸出 schema 由誰持有

由 daemon 組好送給 mod；mod 不持有 prompt 文字，也沒有輸出 schema。
- daemon 組裝：`WB/queue.go:386-415` `buildComplete`：`Complete{Model:"haiku", MaxTokens:4096, Effort:"low", TimeoutMS:30000}`:387，`System = []SystemBlock{{Text: SystemPrompt, Cache: true}}`:412（`prompt.go:19` 內嵌 `prompts/system-v2.txt`），`Prompt` = `BuildTurnInput(...)` 的 JSON 字串:408-413（`input.go:60`；欄位 previous_status/recent_entries/open_todos/turn，:32-38）。
- 序列化送出：`Job.MarshalJSON`（`queue.go:62-78`）→ `{id, kind:"turn", complete:{model, system:[{text,cache}], prompt, max_tokens, effort, timeout_ms}}`；經 `workbook_routes.go:229` `writeJSON(... {"job": job})`。
- mod 端只做形狀轉換與界限檢查：`workbook.js:57-79` `completeRequest`（model 白名單正則:23、prompt/system 長度 ≤200000:24、max_tokens ≤4096:26、timeout_ms 1000–120000:75、effort 白名單:29）；檔頭註解明說「The daemon keeps every decision — the input, the order, the prompt, the validation」（`workbook.js:4-5`）。呼叫點 `events.js` 內 `completeRequest(job.complete)` 與 `$.model.complete(req, ...)`（`wbRun`）。
- 輸出 schema 只存在於 prompt 文字（`system-v2.txt`）與 daemon 的 `validate.go`；mod 回傳純文字 `text`（`workbook.js:117`）。

### 2.3 (b) 驗證與寫入對應

- 驗證：`validate.go:102` `ParseModelJSON`：`objectOf`:55（一個 JSON 物件，允許一層 code fence，成員名不可重複 `hasDuplicateMember`:185）→ 七欄必填 `skip/thing/push/entry/status/thing_done/todos`:107-111 → `checkTodos`:71 → 不得有多餘 token → 非 skip 時 thing、entry 不可空:122。`Repair`:235-245：每欄過 `redact.String`；thing 截 16 字（`maxThing`:16）、push 超過 40 字在最後標點截斷或丟棄（`repairPush`:251）、status 截 200 字於句尾（`maxStatus`:19）、entry 超過 150 字 → 回 `rewrite=true` 觸發 rewrite job。
- 欄位對應寫入（`outcome.go:44-45` → `todos.go:236-285`，同一交易）：
  - `thing`、`push` → `wb_entries.thing/push`、`push_ready_at = now`、usage 三欄（`todos.go:244-246`）；
  - `status` → `wb_status` upsert（`todos.go:255-259`）；
  - `todos.done/dropped/add` → `ResolveTodos`（`validate.go:167`，把 n 對應回 todo id，map 由 lease.ids 帶，`queue.go:343`）→ `applyTodoChanges`（`todos.go:136`）寫 wb_todos；
  - `entry`、`thing_done` 在 rewrite 之後由 `Finish(StateOK)` 寫入（`outcome.go:58` `finishOK`；`store.go:214-232`）。
  - `skip=true` → `FinishSkippedV2`（`todos.go:279`）：entry 變 skipped（reason `model`），仍套用 todos。

### 2.4 (c) push-lines 的 Line 來源

來自 SQLite 的 `wb_entries`（`store.go:337` `ClosestTurn`：`kind='turn'` 且 `turn_at` 在 [at-2000, at+500]、依 `ABS(turn_at-?)` 取最近）。Line 欄位 = `Entry.Thing/Push/ConvKey/ID`（`lines.go:109`）；判斷規則：`push_ready_at>0` 且 push 非空才回 Line:105-109；state failed/skipped 回 false:110；沒有 entry 但該事件 `intakeDone`:112 回 false。記憶體結構只有 `PushLines.changed`/`intakeAt`（`lines.go:30-31`），Line 本身不在記憶體。

## 3. 模組外依賴（不含 WB）

### 3.1 Go

| 位置 | 說明 | 歸類 |
|---|---|---|
| `cmd/pdx/main.go:51,544-546` | import workbookmod、`c.AddModule(workbookmod.New())` | 保留（模組仍存在，內容改） |
| `cmd/pdx/main.go:130-131` | `case "journal": runJournal`；`cmd/pdx/journal.go`（`pdx journal path`，import `internal/journal`:12）、`cmd/pdx/journal_test.go` | 拿掉 |
| `cmd/pdx/device_scope.go:82-92` | device token 白名單：`GET /api/workbook/conversations/...`、`/entries`、`/todos`（:83-85）、`/api/journal`、`/tasks`、`/roadmap`、`PUT /api/journal/confirm`（:87-90）、`/api/journal/members`（:92）；測試 `cmd/pdx/device_scope_test.go:79-81` | 拿掉 |
| `internal/core/info_handler.go:144-153` | `/api/info` 能力：`workbook.v1`:145、`workbook.v2`:146、`journal.v1/sessions.v1/confirm.v1`:148-150、`journal.members.v1`:153，條件 `moduleReady("workbook")`/`moduleFlag(..., "journal")` | 拿掉（mod 宣告給 daemon 的 `workbook.v2` 與 /api/info 的同名字串是兩回事，見 §7） |
| `internal/module/modevents/module.go:103,146-155` | `WithWorkbook(m.workbookService)`；以字串 `"workbook.jobs"` 查 registry，轉型 `modevents.WorkbookService` | 保留 |
| `internal/modevents/workbook_routes.go` | 路由常數 :15-17（next/result/refresh）、`WorkbookResult`:36、`WorkbookService`:53（含 `RequestRefresh`）、`workbookRefresh`:74、`WithWorkbook`:110、`CapWorkbookV2`:116、`CapWorkbookRefresh`:117、`streamMayPoll`:121、`workbookNext`:175、`workbookResult`:232 | next/result/V2 保留；refresh 路由、`CapWorkbookRefresh`、`RequestRefresh` 拿掉（但見 §7-6） |
| `internal/modevents/handler.go:44,98-107,176-179` | handler 欄位 `workbook`、路由分派、`workbook:true` 提示 | 保留（:104-105 refresh 分派拿掉） |
| `internal/modevents/wire.go:33`、`registry.go:273,316,355` | `Caps` 欄位、`SessionCapable/CapableSessions/StreamCapable` | 保留（`CapableSessions` 只見 refresh 用，module.go:183，可拿掉，其他使用者未確認） |
| `internal/module/push/module.go:42,93,95` | import `workbooklines`；`wbHolds`、`wbLines` seam | 保留 |
| `internal/module/push/agent_trigger.go:15-16,98-111,248-326` | `holdForWorkbook`:255、`workbookWait`:284（讀 `workbooksettings.Key`:291）、`workbookLines`:307（讀 `workbooklines.Key`:314）、`wbHoldSet` | 保留 |
| `internal/push/agent_content.go:26-36,173-200`、`internal/push/content.go:59-62,324-326` | `WorkbookLine`、推播 body/title 使用 push/thing；payload `purdex.workbook = {conv_key, entry_id}` 供手機開啟（:325）；entry_id 來自 `Line.EntryID` | 保留（payload 的 entry_id 語意要跟 recap 入口對齊，見 §7） |
| `internal/workbooklines/workbooklines.go` | `Key = "workbook.push-lines"`:14、`Line`:17、`Lines.Await`:25 | 保留 |
| `internal/workbooksettings/workbooksettings.go` | `Key = "hostconfig.workbook-settings"`:7、`Settings.PushWaitS`、預設 8、上限 30、`Reader` | 保留 |
| `internal/module/hostconfig/module.go:16,62-63,91`、`workbook.go`（全檔）、`handler.go:84-85,110`、`read.go:109` | `KeyWorkbook = "workbook"`、`PUT /api/hostconfig/workbook`、`WorkbookSettings()`、`normalizeWorkbook`（只允許 push_wait_s 0–30） | 保留 |
| `internal/module/team/module.go:580-592`、`seat_reader.go`、`internal/team/seat.go` | 註冊 `LineageRootKey`:581、`SeatReaderKey`:582（intake 用）；`JournalFeedKey`:592 | Lineage/Seat 保留；`JournalFeed` 註冊（`team/module.go:592`）、`internal/team/journalfeed.go`、`internal/module/team/journalfeed_store.go` 拿掉（僅 ingest_tasks／journal_members 使用，其他消費者未逐一確認） |
| `internal/module/conversation/module.go:210` | 註冊 `convturns.Key` 供工作簿讀回合 | 保留 |
| `internal/module/agent/turn_end_hub.go:20,98,118,146`；`terminal_sessions.go:57-58` | TurnEndEvent 與訂閱 | 保留 |
| `internal/module/team/last_turn.go:166,172,181` | team 自己也訂閱 TurnEnd，只有註解提到 workbook | 保留（無依賴） |
| `internal/config/journal.go`（全檔）、`config.go:325-332,359-360` | `[journal]` 設定：enabled/push_projects/person_name；`Cfg.JournalPersonName()` 被 module.go:193 使用；`config/watch.go:8` 僅註解 | 拿掉（連同 module.go:190-195 與 `internal/config` 相關測試） |
| `internal/journal/*`（project/read/names/gitpush/gitwt/markdown/tasks/…） | 純函式半邊的 journal 套件，rg 顯示只被 WB 與 `cmd/pdx/journal.go` import；另 `internal/module/peers/personas_pool_test.go` 的測試 import 它 | 拿掉；`personas_pool_test.go` 會受影響（用途未確認） |
| `internal/personas/personas.go:2`、`internal/peers/poolreg.go:14`、`internal/fleet/local.go:2`、`internal/module/fleet/session.go:2`、`internal/store/frames_roots_cache.go:9`、`internal/redact/redact.go:1`、`internal/promptq/queue.go:77`、`internal/convmodel/ccnorm/u3.go:13` | 只在註解提到 workbook/journal | 保留（註解可後續清理）；`internal/redact` 是 `validate.go`／`input.go` 在用，保留 |
| `internal/convturns/convturns.go` | `Key`、`Reader` | 保留 |
| `cmd/pdx/plugin/purdex/hooks/*` | 見 §5 | 保留（本次不改 mod） |
| electron/ | `rg -i 'workbook|journal' electron` 沒有結果 | 無 |

### 3.2 registry key 彙整

保留：`workbook.jobs`（`WB/jobs_service.go:12`）、`workbook.push-lines`（`internal/workbooklines/workbooklines.go:14`）、`hostconfig.workbook-settings`（`internal/workbooksettings/workbooksettings.go:7`）。拿掉：`team.journal-feed`（`internal/team/journalfeed.go:7`）。

## 4. SPA（spa/src）

完整檔案清單與行數附在文末附錄 A（`rg -i 'workbook|journal'` 命中共 198 檔，其中 95 個檔名含 test）。很多命中只是註解提到 journal v5（例如 TabBar.tsx:31,65、useShortcuts.ts:72、useTabDisplay.ts:147、open-watch-area.ts:2），不是依賴。實質分組：

| 群組 | 路徑 | 備註 |
|---|---|---|
| 日誌元件 | `components/journal/*`（CardStack、CardFrame、JournalCard、JournalButtons、JournalStream、JournalRow、JournalHeader/Filters/Pages、ConfirmBand/ConfirmDetail、TasksCard、RoadmapCard、PersonMark、RefChips、KindChip、ItemCheck、useJournalTarget、useSingleFlight、useStreamScroll、card-layout…） | 含測試約 50 檔 |
| 日誌 lib | `lib/journal/*`（api.ts、journal-loader.ts、journal-ws.ts、merge、parse、stream、tasks、refs、types、use-journal-host、view-memory、use-view-memo、filters、fixtures…） | 約 36 檔 |
| 日誌 store | `stores/useJournalStore.ts`、`useJournalMembersStore.ts`、`useJournalUiStore.ts`、`useJournalHoverStore.ts` | 4 src＋多個 test |
| 工作簿元件 | `components/team/`：`TeamSeatWorkbookView.tsx`、`OwnWorkbookPanel.tsx`、`WorkbookLog.tsx`、`WorkbookEntryRow.tsx`、`WorkbookStatus.tsx`、`WorkbookTodos.tsx`、`WorkbookToolbar.tsx`、`useWorkbookViewState.ts`、`seat-workbook.ts`、`own-workbook.ts` 及對應測試（`TeamSeatWorkbookView.*.test.tsx` 5 個、`own-workbook*.test.tsx`、`seat-workbook*.test.tsx`、`TeamPanel.workbook.test.tsx`） | 見附錄 |
| 工作簿 lib | `lib/workbook/*`（api.ts、types.ts、parse.ts、merge.ts、retry.ts、view-model.ts、view-memory.ts、workbook-loader.ts、workbook-ws.ts、fixtures.ts 與測試） | 約 20 檔 |
| 工作簿 store | `stores/useWorkbookStore.ts`（＋`.test.ts`、`.v2.test.ts`）；`lib/team/__tests__/workbook-fixture.ts` | |
| WS／事件 | `lib/host-events.ts:24-28`（`workbook.entry/status/todos/refresh_available`、`journal.changed`）；`hooks/useMultiHostEventWs.ts:35-36,341-347,379-384`（`isWorkbookEvent/handleWorkbookEvent`、`isJournalEvent/handleJournalEvent`）；測試 `useMultiHostEventWs.workbook.test.ts`、`.journal.test.ts` | |
| 能力偵測 | `lib/team/unattended-support.ts:19-21,42-44,62-64,87-91,108-119`（`workbook.v1/v2`、`journal.*` 能力、fence/setSupport/forgetHost）；`lib/team/roster-forget.ts:9,26` | |
| i18n | `locales/zh-TW.json` 109 行、`locales/en.json` 110 行含 workbook/journal 字樣（含 `team.panel.workbook`，`PersonalCard.tsx:64`）；`lib/journal/i18n-keys.test.ts` 驗 key | |
| 其他引用 | `hooks/useRelayBrief.ts:6-8,40-41`（用 `lib/workbook/api` 的 `fetchConversation/fetchTodos` 與 `selectWorkbookSupport` 組接力 brief）；`lib/storage/keys.ts:75-76`（journal UI 的 device-local key）；`styles/themes.css`；`features/workspace/generated/icon-meta.json`（自動產生）；`components/fleet/CountBadge.tsx`、`FleetCells.tsx`（只命中字串，內容未確認）；`lib/notify/notify-title.ts:16-37`（`workbookTitle`：推播標題加「・{thing}」，對應 push line，應保留）；`stores/useTeamUiStore.ts:40`（`teamDrill`） | |

### 4.1 掛載點（之後要換成 recap 入口）

- 標題列按鈕：`components/TitleBar.tsx:18` import `JournalButtons`；:119 `const jt = useJournalTarget()`；:204 `{jt.journalHost ? <JournalButtons target={jt} /> : team ? <TeamNotebookButton team={team} /> : personal && <PersonalButton />}`（`TeamNotebookButton/PersonalButton` 來自 `./team/TeamTitleBar`，:16）。
- 日誌卡片堆：`App.tsx:24` import `PanelAreas` from `./components/journal/CardStack`；:270 `<PanelAreas />`。
- 啟動載入器：`main.tsx:21-22,74,76`（`startWorkbookLoader()`、`startJournalLoader()`）；測試 `main.test.tsx`。
- 團隊面板下鑽工作簿：`components/team/TeamPanel.tsx:26`（import）、:68 `<TeamSeatWorkbookView ... />`；`components/team/panel-view.ts:14,22-27`（`kind: 'workbook'` 視圖，`journalHost` 時關閉）；`TeamPanelArea.tsx:17,35-42`（`useIsJournalHost`、`noDrill`）。
- 個人面板：`components/team/PersonalPanel.tsx:14,98`（`<OwnWorkbookPanel .../>`）；`PersonalCard.tsx:17-18,57-68`（工作簿按鈕，`team.panel.workbook`）；`usePersonalPanel.ts:22-23,124-131`（狀態行：journal host 用日誌最新一行，否則 `useLoadedSeatWorkbook().conv.status`）。
- 團隊列：`TeamPanelRow.tsx:17-21,50,65`（`useSeatWorkbook`、`useJournalStore` 的 `leadLastLine`、`member` 的 JournalMember 行）；`TeamEndedGroup.tsx:5-6,17-23`（已結束 seat 可否下鑽）；`member-lines.ts`（lead／member 行）。
- 無人值守面板：`UnattendedPanel.tsx` 與 `lib/team/unattended-support.ts` 取能力；`unattended-reader.ts` 註解提及 journal v5 §3.9，實際是否讀 journal API 未確認。
- 接力 brief：`hooks/useRelayBrief.ts:6-8,40-41` 依賴 workbook v1/v2 與 `lib/workbook/api`。

## 5. mod 端（`cmd/pdx/plugin/purdex/hooks/`）

本次不改，列出位置。

- `workbook.js`（217 行）：
  - 通道（保留）：`NEXT_URL/RESULT_URL`:9-10、`CAPS`:12、`WAIT_MS`:13、`REQUEST_DEADLINE_MS`:14、`REASONS`:15（含 `nothing-to-fork`）、`MODEL_SLACK_MS`:16、`DEFAULT_TIMEOUT_MS`:18、`MAX_JOBS_PER_DRAIN`:20、`shouldAsk`:36、`nextBody`:41、`parseNext`:46、`completeRequest`:57、`usageOf`:107、`resultBody`:114、`refusedBody`:125、`moreOf`:130。
  - refresh（日後可拿，本次不動）：`REFRESH_URL`:11、`DEFAULT_FORK_TIMEOUT_MS`:19、`MAX_FORK_TIMEOUT_MS`:28、`forkRequest`:83、`refreshBody`:90、`refreshNotice`:96。
  - 不屬工作簿但住在同檔：prompt queue（`PROMPT_*` 常數:137-150、`promptNextBody`:154、`parsePromptJob`:160、`fillRefusal`:184、`cutUtf8`:195、`promptResultBody`:210）；`CAPS` 內的 `prompt.v1`（:12）也在此宣告。
- `events.js`（1075 行）：import workbook.js:35；`wb` 狀態:59；`caps: CAPS`:171；`hintOf`:193-197 與其使用:222（`workbook:true` 提示 → `wbAsk`）；工作簿 job 區標題 :309，`wbAsk`:313；`wbLoop`／`wbRun`／`wbOrphaned` 在 :309-430 區段內；`/workbook refresh` 指令區 :601 起，`WORKBOOK_USAGE`:603、`workbookCommand`:608-616（呼叫 `REFRESH_URL`）；指令註冊 :727-728（`$.command.register({name:'workbook', argumentHint:'refresh'})`）、分派 `on('command.run', {command:'workbook'}, workbookCommand)`:1074；turn.complete 觸發 `shouldAsk(e)` → `wbAsk($, WAIT_MS)`:813；`ORPHAN_MAX_MS`:51（fork 孤兒）。
- `register.js`：`rg -c -i workbook` 為 0，沒有工作簿程式碼（只有 events.js 有 17 處）。
- journal 相關：mod 端沒有 journal 程式碼（hooks/ 內無命中；plugin 目錄只在 `skills/pdx-watch/SKILL.md:60` 有 `launchctl／journal` 字樣，與本功能無關）。
- 測試：`relay.test.ts:221` 對 `/mod/v1/workbook/` 回 204（mock）；`events.test.ts` 內的 workbook 測試未逐一確認。
- `hooks.json`：無 workbook 字樣。

## 6. 資料表

`WB/migrate.go`、`migrate_v3.go`、`migrate_v4.go`，`schemaVersion = 4`（`store.go:19`）；檔案 `<data_dir>/workbook.db`（`module.go:64`，0600，`store.go:17`）。

- v1（`migrate.go:128-161`）：
  - `wb_entries`：id, conv_key, host_id, provider, session_id, turn_id, turn_at, turn_seq, state, reason, thing, push, entry, thing_done, push_ready_at, team_id, role, ref, prompt_ver, latency_ms, created_at, updated_at；UNIQUE(session_id, turn_id)；索引 `wb_entries_conv`(conv_key,id)、`wb_entries_turn_at`(turn_at)。
  - `wb_status`：conv_key PK, status, entry_id, session_id, updated_at。
  - `schema_version(version)`（`migrate.go:158` 插入 version 1；建表處在 `migrate()` 內，未逐行確認）。
- v2（:164-185）：`wb_entries` 加 kind(預設'turn')、usage_in、usage_out、usage_cache_read；新表 `wb_todos`：id, conv_key, title, detail, state, added_entry_id, closed_entry_id, closed_by, created_at, closed_at, updated_at；索引 `wb_todos_conv`(conv_key,state,id)。
- v3（`migrate_v3.go:6-32`）：`wb_journal_outbox`(id, entry_id, unit_key, kind, payload, created_at, done_at；索引 undone、唯一 (entry_id,kind))、`wb_journal_units`(unit_key PK, kind, project_id, wt_path, label, lead_conv, created_at)、`wb_journal_cursors`(name PK, value)；`wb_todos` 加 seq 並回填、唯一索引 `wb_todos_conv_seq`。
- v4（`migrate_v4.go:6-12`）：重建 `wb_journal_outbox_entry_kind` 為 `WHERE entry_id > 0`。
- journal 檔案：`<data_dir>/journal/`（`internal/journal/gitwt.go:24` `Dir string // <data dir>/journal`；`cmd/pdx/journal.go:57` 用 `filepath.Join(cfg.DataDir, "journal")`），內含 `<worktree>/.purdex` 月檔 `<YYYY-MM>.md`（`internal/journal/markdown.go:84`）、tasks.md、roadmap.md（`sidefile.go`）；git 分支 `purdex/journal`（`gitwt.go:16`）。prompt 檔：`<data_dir>/workbook/prompt-v2.txt`、`rewrite-v2.txt`、`refresh-v2.txt`（`prompt.go:58-60`；每次 Start 重寫）。
- 保留不動：以上全部（不刪、不遷移）。只要新功能仍開同一個 workbook.db，`OpenStore` 的版本檢查與 `init()` 的 `len(migrations) != schemaVersion` panic（`migrate.go:25-29`）仍要求版本鏈完整。

## 7. 拿掉會牽連到保留部分的耦合點

1. **結果寫入與 entries 同交易**：`todos.go:236-285` `SetPushLineV2` 一個交易同時寫 push_ready_at、wb_status、wb_todos；`lines.go:100-109` 以 push_ready_at 判斷「推播行已就緒」。新儲存必須提供原子的「寫 thing/push 並標 ready」，否則 Await 語意會破（`outcome.go:44,55`）。
2. **`turnOutbox` 夾在結果交易裡**：`journal_outbox.go:118` 被 `todos.go:262,300`、`store_refresh.go:79` 呼叫；拿掉 journal 時要同時拿掉這個 hook 與 `Store.journalOn`（`store.go:95`）、`EnableJournalOutbox`（`engine.go:114-116`、`journal_outbox.go:28`）。
3. **`followAfterLoss` 住在 journal_job.go:62**，但被通道核心 `queue.go:318,475` 呼叫；它處理 rewrite 的 `then`。拿掉 rewrite/journal 時兩處呼叫與 `job.then` 一起拿掉，否則通道不能編譯。
4. **`journalAfter` 穿插在 apply 的每個出口**（`outcome.go:39,59,67,70`），以及 `queue.go:211,301-303,388-389` 的 journal 分支；`Engine.Next` 的租約 timeout 選擇（`queue.go:297-305`）要簡化。
5. **佇列與 refresh 糾纏**：`Next` 在 `queue.go:277,289-293,325-337` 先讀 `refreshCapable`（`refresh.go:54`，會碰 lineage，註解 :277 特別提醒），`JobWaiting`（`jobs_service.go:91,99`）、`reap`（`queue.go:456-458`）、`RunReaper`（:554-558）都含 refresh；移除時要確認 `reap` 的 `retryOrphans`:455 仍執行。
6. **`modevents.WorkbookService` 介面含 `RequestRefresh`**（`internal/modevents/workbook_routes.go:53-69`；`jobsService.RequestRefresh` 在 `jobs_service.go:64`）；移除 refresh 要同時改介面、`workbookRefresh` 路由（:74）、`handler.go:104-105` 分派與 `CapWorkbookRefresh`（:117）。**mod 仍會宣告 `workbook.refresh` 並呼叫 `/mod/v1/workbook/refresh`（`workbook.js:11-12`；`events.js:608-616`），本次不改 mod，daemon 若直接刪路由，使用者打 `/workbook refresh` 會落到 `refreshNotice` 的 fallback（`workbook.js:103`「失敗」）；且 `streamMayPoll`:123-124 依賴 `CapWorkbookRefresh` 判定可輪詢，若移除該常數，只宣告 refresh 的舊 mod 會變成不可輪詢**。建議保留路由但回固定錯誤碼，或延到 mod 同步改。
7. **能力字串 `workbook.v2` 兩種語意**：mod 在每批 events 宣告（`workbook.js:12`），daemon 用 `CapV2`（`jobs_service.go:15` → `modevents.CapWorkbookV2`）判定；`/api/info` 也有同名 `workbook.v2`（`core/info_handler.go:146`，代表 HTTP API）。拿掉 `/api/info` 的列出時不要誤刪 modevents 的常數。
8. **`/api/info` 能力驅動 SPA**：拿掉 `info_handler.go:145-153` 後 SPA 偵測 `workbook.v1`（`lib/team/unattended-support.ts:62-64`）會全部判定不支援，工作簿／日誌 UI 自然隱藏；但 `hooks/useRelayBrief.ts:40-41` 也依賴 `workbook.v1/v2`，接力 brief 會失去工作簿內容（行為改變，需決定）。
9. **推播 payload `purdex.workbook = {conv_key, entry_id}`**（`internal/push/content.go:324-326`）：entry_id 來自 `Line.EntryID`（`lines.go:109`），手機端用來開啟工作簿檢視；拿掉 entries 後這個 id 的意義要重新定義（例如改成 recap 的 id）。iOS 端是否解析此 key 未確認（不在本 repo）。
10. **`push_wait_s` 設定**（`hostconfig/workbook.go:24-47`）命名與路由 `PUT /api/hostconfig/workbook` 保留；`normalizeWorkbook` 只接受 `push_wait_s`，不受拆除影響。
11. **`prompt.go` 的 `WritePromptFiles`**：`module.go:142-148` 寫三份 prompt 檔，`m.prompts` 在 WB 內其他地方沒有讀取（grep 只在 module.go:34,148），實際 prompt 走 job payload（§2.2）；`module.go` 註解（約 :138-139）稱 `claude --system-prompt-file` 讀它，外部是否仍讀 `<data_dir>/workbook/prompt-v2.txt` 未確認。
12. **`team.LineageRootResolver`／`SeatReader` 與 `convturns.Reader`** 是保留部分的依賴（`module.go:162-171`、`engine.go:46-48`），team 模組仍需註冊 :581-582；`Dependencies()` 的 `peers` 只為 journal（`module.go:53`），拿掉 journal 後可移除，但要確認啟動順序沒有其他隱含依賴。
13. **`FailPending` on Start**（`module.go:121`）：目前把殘留 pending entries 標 failed:stopped（`store.go:256`），新儲存若仍有 pending 狀態要有等價處理，否則 daemon 重啟後 Await 會等到 deadline。
14. **每主機每小時上限**（`queue.go:236` `admit`、`engine.go:36` `defaultCallCap=300`）是通道內部狀態，不依賴儲存；`capReached`（:252）只被 rewrite 用，可獨立拿掉。
15. **測試耦合**：`prompt_test.go:30` 讀 `docs/specs/2026-10-10-session-workbook-prompt-v2.md`；`journal_job_test.go:305` 讀 testdata golden；`internal/module/peers/personas_pool_test.go` import `internal/journal`（用途未確認）；`cmd/pdx/device_scope_test.go:79-81` 斷言白名單內容。
16. **workbook.js 與 prompt queue 同檔**：`prompt.v1` 能力與 `PROMPT_*` 在 `workbook.js:137-217`，日後若動 mod 拆除 workbook.js 時不能整檔刪。

## 附錄 A：spa/src 命中 workbook／journal 的檔案與行數（行數 路徑，相對 spa/src）

```
     239 ./App.test.tsx
     324 ./App.tsx
     231 ./components/ApprovalDialogHost.relayBrief.test.tsx
      59 ./components/fleet/CountBadge.test.tsx
      27 ./components/fleet/CountBadge.tsx
      42 ./components/fleet/FleetCells.tsx
      38 ./components/journal/card-layout.ts
      92 ./components/journal/CardFrame.tsx
     164 ./components/journal/CardStack.float.test.tsx
     272 ./components/journal/CardStack.tab-switch.test.tsx
     273 ./components/journal/CardStack.test.tsx
     230 ./components/journal/CardStack.tsx
      38 ./components/journal/ConfirmBand.test.tsx
      19 ./components/journal/ConfirmBand.tsx
      61 ./components/journal/ConfirmDetail.tsx
      29 ./components/journal/ItemCheck.tsx
      76 ./components/journal/JournalButtons.count.test.tsx
     149 ./components/journal/JournalButtons.test.tsx
      85 ./components/journal/JournalButtons.tsx
     391 ./components/journal/JournalCard.sessions.test.tsx
     204 ./components/journal/JournalCard.test.tsx
     136 ./components/journal/JournalCard.tsx
     280 ./components/journal/JournalDetail.test.tsx
      55 ./components/journal/JournalFilters.test.tsx
      34 ./components/journal/JournalFilters.tsx
      90 ./components/journal/JournalHeader.test.tsx
      53 ./components/journal/JournalPages.tsx
     135 ./components/journal/JournalRow.test.tsx
     110 ./components/journal/JournalRow.tsx
     338 ./components/journal/JournalStream.test.tsx
      60 ./components/journal/JournalStream.tsx
      45 ./components/journal/KindChip.test.tsx
      31 ./components/journal/KindChip.tsx
      41 ./components/journal/no-pin-ui.guard.test.ts
     246 ./components/journal/PanelAreas.test.tsx
      38 ./components/journal/PersonMark.test.tsx
      12 ./components/journal/PersonMark.tsx
      35 ./components/journal/RefChips.test.tsx
      29 ./components/journal/RefChips.tsx
     152 ./components/journal/RoadmapCard.test.tsx
      56 ./components/journal/RoadmapCard.tsx
     274 ./components/journal/TasksCard.test.tsx
      99 ./components/journal/TasksCard.tsx
     108 ./components/journal/useJournalTarget.test.tsx
      67 ./components/journal/useJournalTarget.ts
      13 ./components/journal/useSingleFlight.ts
     194 ./components/journal/useStreamScroll.ts
     175 ./components/journal/workbook-off.test.tsx
     116 ./components/TabBar.fixed.test.tsx
     424 ./components/TabBar.team.test.tsx
     345 ./components/TabBar.tsx
      88 ./components/team/member-lines.ts
      56 ./components/team/own-workbook.retry.test.tsx
      66 ./components/team/own-workbook.test.tsx
      74 ./components/team/own-workbook.ts
      49 ./components/team/OwnWorkbookPanel.tsx
      33 ./components/team/panel-view.test.ts
      29 ./components/team/panel-view.ts
      80 ./components/team/PersonalCard.tsx
     342 ./components/team/PersonalPanel.full.test.tsx
     112 ./components/team/PersonalPanel.tsx
      47 ./components/team/seat-workbook.test.tsx
      53 ./components/team/seat-workbook.ts
      78 ./components/team/seat-workbook.viewing.test.tsx
      93 ./components/team/Steppers.tsx
      36 ./components/team/team-settings.ts
      55 ./components/team/TeamEndedGroup.tsx
      92 ./components/team/TeamPanel.fresh.test.tsx
     220 ./components/team/TeamPanel.memberLines.test.tsx
      77 ./components/team/TeamPanel.paused.test.tsx
     149 ./components/team/TeamPanel.rowV2.test.tsx
     191 ./components/team/TeamPanel.tsx
     329 ./components/team/TeamPanel.workbook.test.tsx
      74 ./components/team/TeamPanelArea.noDrill.test.tsx
     200 ./components/team/TeamPanelArea.personal.test.tsx
     923 ./components/team/TeamPanelArea.test.tsx
     107 ./components/team/TeamPanelArea.tsx
     191 ./components/team/TeamPanelRow.tsx
      88 ./components/team/TeamSeatWorkbookView.layout.test.tsx
     116 ./components/team/TeamSeatWorkbookView.log.test.tsx
     111 ./components/team/TeamSeatWorkbookView.refresh.test.tsx
     101 ./components/team/TeamSeatWorkbookView.tab-switch.test.tsx
     234 ./components/team/TeamSeatWorkbookView.todos.test.tsx
     100 ./components/team/TeamSeatWorkbookView.tsx
     206 ./components/team/TeamSettingsBlock.cap.test.tsx
     215 ./components/team/TeamSettingsBlock.test.tsx
     141 ./components/team/usePersonalPanel.ts
      31 ./components/team/useShownPanelWidth.test.ts
      19 ./components/team/useShownPanelWidth.ts
      48 ./components/team/useWorkbookViewState.ts
      49 ./components/team/WorkbookEntryRow.tsx
      72 ./components/team/WorkbookLog.tsx
      20 ./components/team/WorkbookStatus.tsx
     104 ./components/team/WorkbookTodos.tsx
      83 ./components/team/WorkbookToolbar.tsx
     260 ./components/TitleBar.tsx
     807 ./components/UnattendedPanel.test.tsx
     317 ./components/UnattendedPanel.tsx
     131 ./features/workspace/components/WatchShieldButton.test.tsx
       0 ./features/workspace/generated/icon-meta.json
     190 ./features/workspace/lib/open-watch-area.return.test.ts
      61 ./features/workspace/lib/open-watch-area.ts
     123 ./features/workspace/lib/open-watch-area.windows.test.ts
     453 ./features/workspace/store.ts
      98 ./hooks/useMultiHostEventWs.journal.test.ts
     476 ./hooks/useMultiHostEventWs.ts
     133 ./hooks/useMultiHostEventWs.workbook.test.ts
      51 ./hooks/useRelayBrief.test.ts
      52 ./hooks/useRelayBrief.ts
     206 ./hooks/useShortcuts.ts
     135 ./hooks/useShortcuts.watch.test.ts
     173 ./hooks/useTabDisplay.ts
      66 ./hooks/useTabDisplay.watch.test.ts
     158 ./lib/conversations/pane-release.ts
     167 ./lib/host-events.ts
     426 ./lib/host-reresolve.ts
      44 ./lib/journal/__tests__/journal-fixture.ts
     114 ./lib/journal/api.test.ts
      77 ./lib/journal/api.ts
      43 ./lib/journal/filters.test.ts
      21 ./lib/journal/filters.ts
      35 ./lib/journal/fixtures.ts
      71 ./lib/journal/i18n-keys.test.ts
     351 ./lib/journal/journal-loader.test.ts
     180 ./lib/journal/journal-loader.ts
      23 ./lib/journal/journal-ws.ts
      91 ./lib/journal/kind-tokens.test.ts
      28 ./lib/journal/merge.ts
      54 ./lib/journal/parse.members.test.ts
     139 ./lib/journal/parse.test.ts
     219 ./lib/journal/parse.ts
      49 ./lib/journal/person-name.test.ts
      28 ./lib/journal/person-name.ts
      64 ./lib/journal/refs.test.ts
      28 ./lib/journal/refs.ts
      23 ./lib/journal/roadmap-status.test.ts
      24 ./lib/journal/roadmap-status.ts
      30 ./lib/journal/stream.test.ts
      29 ./lib/journal/stream.ts
      43 ./lib/journal/success-token.test.ts
      44 ./lib/journal/tasks.test.ts
      33 ./lib/journal/tasks.ts
     109 ./lib/journal/types.ts
       6 ./lib/journal/use-journal-host.ts
      13 ./lib/journal/use-view-memo.ts
      79 ./lib/journal/view-memory.test.ts
     126 ./lib/journal/view-memory.ts
      53 ./lib/journal/warning-token.test.ts
      38 ./lib/notify/notify-title.ts
     112 ./lib/storage/keys.ts
      28 ./lib/team/__tests__/workbook-fixture.ts
      40 ./lib/team/lead-name.ts
     218 ./lib/team/relay-quota.test.ts
     194 ./lib/team/relay-quota.ts
      29 ./lib/team/roster-forget.ts
      57 ./lib/team/roster-ws.ts
     287 ./lib/team/unattended-reader.test.ts
     173 ./lib/team/unattended-reader.ts
     108 ./lib/team/unattended-support.journal.test.ts
     127 ./lib/team/unattended-support.ts
      96 ./lib/workbook/api.test.ts
      76 ./lib/workbook/api.ts
      10 ./lib/workbook/fixtures.ts
      63 ./lib/workbook/merge.test.ts
     100 ./lib/workbook/merge.ts
     147 ./lib/workbook/parse.test.ts
     201 ./lib/workbook/parse.ts
       8 ./lib/workbook/retry.ts
      70 ./lib/workbook/types.ts
      44 ./lib/workbook/view-memory.test.ts
      57 ./lib/workbook/view-memory.ts
      36 ./lib/workbook/view-model.test.ts
      51 ./lib/workbook/view-model.ts
     182 ./lib/workbook/workbook-loader.retry.test.ts
     189 ./lib/workbook/workbook-loader.test.ts
      79 ./lib/workbook/workbook-loader.ts
      38 ./lib/workbook/workbook-ws.ts
    3503 ./locales/en.json
    3503 ./locales/zh-TW.json
     145 ./main.test.tsx
     123 ./main.tsx
      16 ./stores/useJournalHoverStore.ts
     131 ./stores/useJournalMembersStore.test.ts
      91 ./stores/useJournalMembersStore.ts
     218 ./stores/useJournalStore.confirm.test.ts
      16 ./stores/useJournalStore.forget.test.ts
     247 ./stores/useJournalStore.sessions.test.ts
     293 ./stores/useJournalStore.test.ts
     496 ./stores/useJournalStore.ts
      42 ./stores/useJournalUiStore.test.ts
      47 ./stores/useJournalUiStore.ts
      15 ./stores/usePanelDraftStore.ts
     513 ./stores/useTeamUiStore.test.ts
     323 ./stores/useTeamUiStore.ts
     359 ./stores/useWorkbookStore.test.ts
     486 ./stores/useWorkbookStore.ts
     562 ./stores/useWorkbookStore.v2.test.ts
     209 ./styles/themes.css
```
