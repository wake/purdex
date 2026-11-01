# 對話檔、回合摘要與 recap（取代工作簿）— plan

> 2026-11-01，Opus。實作 spec `docs/specs/2026-11-01-session-dialogue-log-spec.md`（以下稱 spec，使用者 11-01 確認）。
> 流程：全審。plan 帶 spec 給 codex 審一次 → Sonnet 逐 task TDD → 每張 PR 合併前攻擊方＋對照 spec 的 Sonnet 各一次。
> 測試分層照 CLAUDE.md：開發中只跑改到的套件（`pdx test go-changed <套件>`）；PR 前改到的套件＋`pdx test lint`，動到鎖或 goroutine 的套件加 `-race`；SPA 用 `vitest related`，tsc 送 mserv。完整套件留給發版。
> 盤點：`internal/module/workbook` 的保留／拿掉歸類與模組外依賴，見附錄 A（盤點子代理 11-01，file:line 已核對）。
>
> **修訂 1**（codex plan review `task-mvxmh6vy-un0w2a`，9 條全數採納）：
> - 游標加「最後一個已寫 item 的 id」；還有工具在跑的回合先不寫（§1-3，第 1 條）；
> - 事件不合併，逐個依序處理（§1-7，第 2 條）；
> - T7 補兩個測試檔（第 3 條）；
> - T8 補結果狀態分類、重試與 Stop 收斂（第 4、5 條）；
> - T9 截斷改在字詞邊界、`lines.go` 連測試整檔搬（第 6、7 條）；
> - T11 白名單測試寫明（第 8 條）；
> - Reuse §4.1 補 `outcome.go`（第 9 條）。
>
> 修正都是針對單條 finding 的局部補充，不再送審。
>
> **修訂 2**（PR 1 合併前 codex 攻擊方，11-01）：§1-9 每個 Stop 只寫到自己的回合；§1-10 不再修改前任檔案；新增 §1-13 session id 驗證。

## 0. 範圍與 PR

| PR | 內容 | 能單獨驗收的結果 |
|---|---|---|
| 1 | daemon：對話檔（新模組 `dialogue`） | 每個 Claude Code tmux session 結束一回合後，`<data_dir>/dialogue/<session_id>/NNN.md` 出現這一回合；壓縮分界開下一段；工作簿照常運作 |
| 2 | daemon：回合摘要＋recap，拿掉工作簿與日誌後端 | 推播照常（內容改由回合摘要提供）；對話檔出現 `turn-summary` 與 `recap`；新 API 可讀；`/api/info` 不再有 `workbook.v1`、`journal.*` |
| 3 | App：recap 檢視（原型先給使用者確認） | 標題列與團隊面板的工作簿入口換成 recap；日誌元件拿掉 |

PR 依序合併。PR 1 與 PR 2 分開的理由：PR 1 是新增、不碰工作簿，可以先上線累積對話檔；PR 2 是替換加大量刪除。PR 3 等原型確認。

## 1. plan 層的設計決定（spec 沒寫死、這裡定）

1. **新模組 `internal/module/dialogue`**，不在工作簿模組裡改。PR 2 把工作簿的派工通道搬進來，再刪掉工作簿模組。資料庫另開 `<data_dir>/dialogue.db`，不碰 `workbook.db`。
2. **讀 transcript：擴充 `convturns.Reader`**，加 `TurnsFrom(ctx, provider, sessionID, fromTurnID)`：
   - 回傳從 `fromTurnID` 那一回合（含）起的所有回合、`Found`（`fromTurnID` 是否還在），以及 transcript 的實際路徑（`<source:>` 用）；
   - `fromTurnID` 為空或找不到時回傳整份對話。
   - 現有 `LastTurns(n)` 做不到「從游標起」，也拿不到路徑。
3. **游標＝（回合 id，該回合已寫的 item 數，最後一個已寫 item 的 id）**。每次都從游標那一回合重讀。原因是 Claude Code 會把後來的東西接在舊回合上：
   - 閒置壓縮的分界會接在最後一回合（`ccnorm/system.go:82-101`，壓縮不開新回合）；
   - Stop hook 先到、最後一段 agent 文字晚寫進檔案時，晚到的 item 也接在同一回合。
   - 新 id 的 item 一律接在回合尾端；已有 id 的 item 原地替換（`ccnorm/normalizer.go:307-330` `upsert`）。所以「已寫幾個」能定位新 item，但已寫過的 item 之後被原地更新（工具結果補上狀態、AskUserQuestion 的答案、子代理結果，`ccnorm/steps.go:175-193`）不會補寫。對策：
     - 回合裡還有 `Status=running` 的工具時先不寫這一回合（等 transcript 跟上的重試內），逾時才照現況寫。Stop 在工具迴圈結束後才觸發，所以已結束的回合正常不會有 running 的工具；
     - 寫入前核對 `items[已寫數-1].ID` 等於記下的 id，不等就整個 session 重建（§1-6）；
     - 已寫 item 之後的原地更新不補寫，這是有意的取捨：會發生的只剩上面逾時的情況。
4. **第一次記錄一個 session 時，從 transcript 開頭補寫**，含之前的壓縮分段，各段照分界拆開。歷史回合只寫內容，不補產回合摘要（spec §2.1，寫 plan 時補進 spec）。成本是一次性的解析，convfeed 本來就把整份對話放在記憶體。
5. **回合編號**在回合第一次寫進對話檔時配發，存在 `dlg_turns`（session、turn id → 編號）。重建時沿用已配的編號，所以 recap 的 `turns a–b` 不會變。
6. **當機與重建**：
   - 每段記錄「已確認寫入的位元組數」。下次寫入前，檔案比它長就截回去（上次寫了檔、還沒更新游標就當機），比它短或檔案不見就整個 session 重建。
   - 游標那一回合在 transcript 裡找不到（`Found=false`，transcript 被改寫或換檔），也整個 session 重建。
   - 重建＝刪掉這個 session 資料夾裡的段檔，從頭寫一次；PR 2 起把資料庫裡的回合摘要與 recap 依 turn id 放回原位。
   - spec §2.3 原寫「convfeed 開新 epoch 時重建」，寫 plan 時改成上面兩個條件（spec 已同步）：Turn.ID 取自回合第一列的 uuid（`ccnorm/turns.go:30-48` `openTurn`），epoch 換了 id 照樣找得到，就不必重建；真的找不到才重建。
7. **每個 session 一條工作 goroutine，事件不合併**。訂閱者只把事件放進該 session 的佇列（上限 16，同工作簿 `maxQueued`，`intake.go:43`；滿了丟掉新的並記錄），worker 依到達順序**逐個**處理，處理完閒置一段時間自行結束。不合併的原因：PR 2 的推播配對要用每個 Stop 自己的 (At, Seq)，而且每個事件處理完都要各自通知推播等待（工作簿 `intake.go:58-66`、`lines.go:49-83`）。等 transcript 跟上的重試（100 ms × 15）在 worker 內做，不卡住訂閱者。
8. **等 transcript 跟上**沿用工作簿 `adoptEvent` 的判斷，但不把 hook 的文字塞進回合。原因：塞進去的假 item 會讓「已寫 item 數」對不上，真文字晚到時會重複寫。逾時就照檔案現況寫，晚到的部分下次從游標補上。回合摘要（PR 2）要用 hook 文字當備援時，另外傳給 job。
9. **只寫已結束的回合，而且每個 Stop 只寫到它自己的回合**：
   - 遇到第一個仍在跑的回合就停；事件指到的那一回合即使還顯示 running，也當成已結束；
   - 找得到事件指到的回合時，寫到它（含）就停，之後的回合留給它們自己的 Stop（PR 1 合併前審查加的：否則前一個 Stop 會把後面的回合一起寫掉，PR 2 的推播配對就配不到）；找不到時才一路寫到不能寫為止；
   - 游標回合的晚到 item 照樣補寫。
10. **接力**：新 session 開第一段時，`<prev:>` 指向前任（`team.PredecessorReader`）的最後一段；前任沒記錄過就寫 `<prev: none>`。**不修改前任的檔案**（原本要在前任最後一段補 `<next:>`，PR 1 合併前審查發現前任之後若再寫入會讓連結錯位、補寫與記錄之間失敗會讓連結被當成當機殘留截掉，拿掉；往前追溯靠 `<prev:>` 就夠）。
13. **session id 先驗證再進路徑**：只接受小寫 UUID（同 `internal/modevents/wire.go:131` `sidRe`），intake 入口與 writer 各驗一次，段檔路徑清理後必須仍在 `<data_dir>/dialogue` 底下；前任 id 也驗。（PR 1 合併前審查：hook 送來的 session id 沒驗證，`../` 能跳出資料夾。）
11. **路徑一律寫絕對路徑**，方便之後 agent 直接用 Read 打開（spec 範例裡的 `~` 只是示意）。
12. **涵蓋範圍**跟工作簿一樣，只有走 tmux hook 的 Claude Code session：turn-end hub 只發這些（`internal/module/agent/turn_end_hub.go:38-41`）。

## 2. Reuse

| Need | Existing path:symbol | Decision | Reason | Evidence |
|---|---|---|---|---|
| 回合結束事件 | `internal/module/agent/terminal_sessions.go:58` `TerminalSessions.SubscribeTurnEnd` | reuse | 工作簿就是這樣訂閱的 | `internal/module/workbook/module.go:202-208` |
| 從游標讀回合＋路徑 | `internal/convturns/convturns.go:20` `Reader.LastTurns`；`internal/convfeed/copy_turns.go:13` `Entry.CopyLastTurns` | extend | 只能取最後 n 回合、沒有路徑；加 `TurnsFrom`／`CopyTurnsFrom` | `internal/module/conversation/turns.go:13-28`；路徑在 `refreshOnce` 的 `src.Path`（`internal/module/conversation/http.go:363-378`） |
| 等 transcript 跟上 Stop | `internal/module/workbook/intake.go:233` `adoptEvent` | new（參考改寫） | 工作簿版會把 hook 文字塞進回合，對話檔的 item 游標不能這樣做（§1-8）；工作簿在 PR 2 刪除 | `intake.go:296-303` |
| 一行工具摘要 | `internal/convmodel/item.go` `Step.Summary`（`ccnorm/kinds.go` `summaryOf`，≤200 字） | reuse | spec 指定用現成摘要 | `ccnorm/kinds.go:31-33` |
| 前任 session | `internal/team/seat.go:57` `PredecessorReader.PredecessorOf` | reuse | 接力的 lineage 已在這裡 | `seat.go:52-59` |
| SQLite 開檔 | `internal/module/workbook/store.go:100` `OpenStore` | new（照樣式寫） | 各模組各開自己的 db（`internal/store/meta.go:33`、`agent_event.go:35` 同樣式），沒有共用 helper | 同左 |
| 壓縮分界 | `internal/convmodel/item.go` `SystemCompacted`（`Detail.trigger`） | reuse | 正規化已產生，摘要已放進 Detail、不會變成使用者訊息 | `ccnorm/system.go:82-101`、`ccnorm/u3.go:200-220` |
| 轉錄測試資料 | `testdata/conversation/v1/cc-transcript/{compact,ask-question,peer-message,source-kinds,subagent,slash-and-local-command,queued-mid-turn}` | reuse | 真實錄製、已去識別，含壓縮、提問、peer、子代理 | 各資料夾 README |

（PR 2、PR 3 的 Reuse 列在各自段落。）

## 3. PR 1：對話檔

會動到的檔案：新增 `internal/module/dialogue/`；改 `internal/convturns/convturns.go`、`internal/convfeed/copy_turns.go`、`internal/module/conversation/turns.go`、`internal/module/conversation/http.go`（`refreshOnce` 回傳路徑）、`cmd/pdx/main.go`（註冊模組）。

### T1. `convturns.Reader.TurnsFrom`

- `convfeed.Entry.CopyTurnsFrom(fromID string) (turns []convmodel.Turn, found bool)`：在 entry 的鎖內找 `fromID`，深拷貝它與之後的回合；找不到或空字串就拷貝全部、`found=false`。拷貝方式同 `CopyLastTurns`。
- `convturns.Reader` 加方法 `TurnsFrom(ctx, provider, sessionID, fromTurnID string) (TurnsFromResult, error)`，`TurnsFromResult{Turns, Found, Path}`。錯誤沿用 `LastTurns` 的（`ErrUnsupportedProvider`、convfeed 的 `ErrNotFound`／`ErrBusy`）。
- 實作在 `conversation/turns.go`：`refresh` 與 `refreshOnce` 多回傳 resolver 的 `src.Path`，其他呼叫端忽略它。
- 介面多一個方法，會讓既有的測試替身編譯失敗，一起補（`rg "LastTurns\(ctx" --type go` 找替身）。
- 測試清單：
  1. `CopyTurnsFrom`：id 在中間時回傳它與之後；id 是最後一回合時回傳一回合；id 不存在時回傳全部且 `found=false`；空字串同不存在；回傳值改了不影響 entry。
  2. `Module.TurnsFrom`：provider 不是 claude 回 `ErrUnsupportedProvider`；用 compact fixture 回傳的 `Path` 是 fixture 檔的路徑。
- 驗收：上述測試先紅後綠；`go build ./...`。

### T2. 渲染：回合 → 對話檔文字（純函式）

- `render.go`：`renderItems(items []convmodel.Item, opts) []block`，每個 block 是一段文字，或一個「壓縮分界」記號（交給 writer 換段）。不碰檔案與資料庫。
- 對照表（spec §2.2）：

| item | 輸出 |
|---|---|
| User，Source user／queued | `user: <全文>` |
| User，Source peer | `peer(<From.Name>): <全文>`；沒有名字時 `peer(unknown)` |
| User，Source scheduled | `scheduled: <全文>` |
| User，Source slash | `<slash | 第一行>` |
| User，Source bash | `<shell | 第一行>` |
| User，Source task | `<subagent done | <summary> 的內容>`；沒有 `<summary>` 標籤時取第一行 |
| User 帶圖 | 文字後另起一行 `<image ×N>` |
| AgentText | `agent: <全文>`（空白略過） |
| Thinking | 略過 |
| Step | `<tool <Tool> | <Summary>>`；有 `Question` 時摘要用第一個問題，已回答再加 `<answer | <各題答案，以「；」連接>>`；`Children`（子代理內部）略過 |
| System compacted | 分界記號，帶 trigger（auto／manual；沒有就 auto） |
| 其他 System | 略過 |

- 排版：`user:`、`peer(…):`、`scheduled:`、`agent:` 這類文字段前後空一行；連續的單行標記（tool、answer、slash…）相鄰不空行。文字被正規化截斷（`Truncated`）時，結尾加 `…（截斷）`。
- 測試清單（表格測試，每列一個 item 型別），再加 golden：fixture `source-kinds`、`peer-message`、`ask-question`、`subagent`、`queued-mid-turn` 經 ccnorm 正規化後渲染，比對 `testdata/dialogue/<fixture>.md`。golden 第一次由測試產生後**人工逐行核對**再提交（lead 核對，不交給實作者自己核）。
- 驗收：表格測試與 golden 先紅後綠；對 render 的每個分支做突變，至少一個測試失敗。

### T3. store：`dialogue.db`

- 照 `workbook/store.go:100-124` 的樣式開檔（0600、WAL、busy_timeout、sidecar 權限）。
- 表：
  - `dlg_sessions(session_id PK, segment INT, seg_bytes INT, cursor_turn TEXT, cursor_items INT, cursor_item_id TEXT, next_turn_no INT, source_path TEXT, created_at, updated_at)`
  - `dlg_turns(session_id, turn_id, turn_no INT, segment INT, ended_at INT, PRIMARY KEY(session_id, turn_id))`
- 方法：`Session(sid)`、`Commit(sid, state, newTurns)`（一個交易內更新游標、位元組數、配發的編號）、`TurnNo(sid, turnID)`、`ResetForRebuild(sid)`（清游標與段號，保留 `dlg_turns`）。
- migration 用 `user_version`，從 1 開始（之後 PR 2 加表用 2）。
- 測試清單：開新檔建表；重開不重建；Commit 的交易性（中途失敗不留半套）；ResetForRebuild 後編號仍在；檔案權限 0600。
- 驗收：測試先紅後綠。

### T4. writer：段檔、換段、當機修復、重建　**需要判斷**

- `writer.go`：`Write(sid string, res convturns.TurnsFromResult, target string) error`，依 §1-3～§1-6：
  1. 讀 session 狀態；沒有狀態＝第一次，從 `001.md` 開始寫檔頭，然後寫全部回合。
  2. 段檔比 `seg_bytes` 長就截回 `seg_bytes`；比較短或不存在、`cursor_turn` 有值但 `Found=false`、或游標回合第 `cursor_items` 個 item 的 id 不等於 `cursor_item_id`，就重建。
  3. 從游標回合的第 `cursor_items` 個 item 接著寫，之後的回合從頭寫；遇到第一個仍在跑、且不是 `target` 的回合就停；回合裡還有 running 的工具時也停（§1-3，逾時由 intake 傳 `force` 進來才照寫）。
  4. 遇到壓縮分界：舊段寫 `<compacted | trigger>` 與 `<next: 新段路徑>`，開新段，寫 `<prev: 舊段>`、`<source: …>`（PR 2 起再加 `<carried recap>`）。分界之後的 item 寫進新段。
  5. 每寫完一回合就 Commit（游標、位元組數、新回合的編號）。
- 檔頭：第一段 `<prev:>` 依 §1-10；`<source:>` 用 `TurnsFromResult.Path`。
- 寫檔：`O_APPEND`；資料夾 0700、檔案 0600（內容是對話原文）。
- 給 PR 2 的接點：`Hooks{Carried func(sid string, beforeNo int) string; Placed func(sid, turnID string) []string}`，PR 1 都回空。`beforeNo` 是發生壓縮分界的那個回合的編號，`Carried` 回傳 `to_no` 不大於它的最新一則 recap（T4 審查後改，重建時才帶得對）。intake 另有 `onWritten`、`onSettled` 兩個接點給 T9（見 T5）。
- 測試清單：
  1. 第一次寫（含 compact fixture → 兩段、各有 prev／next）；
  2. 同一事件重複送不重寫；
  3. 游標回合後來多了 item（模擬閒置壓縮接在舊回合）→ 換段且新回合進新段；
  4. 當機模擬（檔案比 seg_bytes 長）→ 截回後照寫；
  5. 檔案被刪 → 重建且編號不變；`Found=false` → 重建；游標 item id 對不上 → 重建；
  6. running 回合停住、target 例外；
  7. 回合裡有 running 的工具 → 不寫；同一回合的工具結果到了（item 原地更新成 done、帶答案）→ 寫出來且有 `<answer | …>`；`force` 時照現況寫；
  8. 接力：前任有記錄時前任最後一段補 `<next:>`、新段 `<prev:>` 指過去。
- 「需要判斷」的理由：換段、截回、重建三者的交錯，以及「先寫檔、後 Commit」的順序，錯了會靜默重複或遺失內容。task 完成後派新 context 的 Sonnet 審。
- 驗收：測試先紅後綠；對換段與截回的條件做突變，都有測試失敗。

### T5. intake 與模組殼　**需要判斷**

- `intake.go`：訂閱 turn end；每個 session 一個 worker（§1-7）；worker 呼叫 `TurnsFrom`（`ErrBusy` 重試 3 次、間隔同工作簿 `busyDelay`），做「等 transcript 跟上」（§1-8：100 ms × 15），再交給 writer。
- `module.go`：`Name()="dialogue"`；`Dependencies()` ＝ agent、conversation、team；`Init` 開 `dialogue.db`（失敗只記錄、模組不就緒，不擋 daemon，同工作簿）；`Start` 訂閱；`Stop` 退訂並等 worker 結束；`Close` 關 db。`Status()` 回 `ready`、`init_error`。
- `cmd/pdx/main.go` 註冊模組（工作簿旁邊）。
- 測試清單：假的 turn-end 來源送兩個 session 的事件 → 各自寫檔、互不阻塞；同 session 在 worker 忙碌時連續到 5 個事件 → 5 個都依到達順序處理、各自帶原本的 (At, Seq)（PR 2 會在每個事件後通知推播等待，這裡先用測試替身記錄呼叫）；佇列滿 16 時新事件被丟掉且有記錄；`ErrBusy` 重試後成功；transcript 落後 Stop（最後回合 running、沒有文字）→ 等到跟上才寫；逾時照現況寫，後來補上的文字下次寫進同一回合；Stop 後模組不再寫檔（`-race`）。
- 「需要判斷」的理由：goroutine 生命週期與關機順序。task 完成後派新 context 的 Sonnet 審。
- 驗收：測試先紅後綠，`-race` 跑 dialogue 套件。

### T6. PR 1 驗收

- 端對端測試：真的 conversation 模組＋compact fixture＋假 turn end → 對話檔內容等於 golden（同 T2 的人工核對）。
- `/api/info` 不變（PR 1 不加 capability：沒有對外 API）。
- PR 前：`pdx test go-changed ./internal/module/dialogue/... ./internal/convfeed/... ./internal/convturns/... ./internal/module/conversation/...`、dialogue 套件 `-race`、`pdx test lint`。
- 合併前審查：codex 攻擊方（重點：游標與換段的邊界、當機修復、goroutine 結束、檔案權限）＋新 context 的 Sonnet 對照 spec §2。

## 4. PR 2：回合摘要、recap，拿掉工作簿與日誌

### 4.0 PR 2 的設計決定

1. **mod 不改**（spec §8）。盤點確認 prompt、輸入、驗證都在 daemon，mod 只轉形狀、檢查界限、呼叫模型（盤點 §2.2，`cmd/pdx/plugin/purdex/hooks/workbook.js:4-5,57-79`）。所以：
   - 線上的路由、能力字串、registry key 全部沿用：`/mod/v1/workbook/next|result`、`workbook.v2`、`workbook.jobs`、`workbook.push-lines`、`hostconfig.workbook-settings`；
   - mod 只接受 `turn`／`rewrite`（complete）與 `refresh`（fork）三種 kind，其他一律拒絕。recap job 在線上也標成 `turn`，跟現在的 journal job 一樣（`internal/module/workbook/queue.go:59-65`），daemon 靠租約記得真正的種類。
2. **順序：先整包刪工作簿，再在 dialogue 裡建派工通道**，不在工作簿裡逐段拆。工作簿的 journal、rewrite、refresh 跟通道核心交纏（盤點 §7-2～§7-5）；在原地拆要改十幾個檔，新寫一份精簡版比較乾淨。新程式照基準 commit `16990529d` 的 `internal/module/workbook/{queue,engine,outcome,settle,lines,jobs_service,validate,input,intake}.go` 改寫，PR 說明附「保留了哪些行為、拿掉哪些」的對照。
3. **`/workbook refresh`**：路由留著，一律回 409 `{"error":"disabled"}`。mod 會顯示「目前沒有可執行重整的 session」（`workbook.js:103`）。`CapWorkbookRefresh` 常數保留，因為 `streamMayPoll` 靠它判斷只宣告 refresh 的舊 mod 能不能輪詢（盤點 §7-6，`internal/modevents/workbook_routes.go:121-124`）。`WorkbookService.RequestRefresh` 從介面拿掉。指令本身在第二個 change 改 mod 時拿掉。
4. **推播**：
   - `Line.Thing`＝回合摘要的 `title`，`Line.Push`＝`push`，`Line.ConvKey`＝lineage root（同工作簿，`engine.go:142`），`Line.EntryID`＝回合摘要的 id；
   - 推播 payload 的 `purdex.workbook` 形狀不變（`internal/push/content.go:324-326`）；
   - 手機點推播打開的工作簿畫面會是空的，不在這個 change 處理（iOS 不在本 repo，第二個 change 前再看）。
5. **recap 只在 session 還活著、mod 能接 job 時產生**。job 在 session 自己的程序裡跑（`capable`，`engine.go:137`），session 已結束時，打開或手動觸發都回 `{"state":"unavailable"}`，畫面顯示最後一則。
6. **`/api/info`**：拿掉 `workbook.v1`、`workbook.v2`（HTTP API 那個；modevents 的同名常數保留，盤點 §7-7）、`journal.*`；新增 `dialogue.v1`（dialogue 模組 ready 時）。SPA 看不到 `workbook.v1`／`journal.*` 時已經會自己藏起工作簿與日誌介面（盤點 §7-8），所以 PR 2 單獨部署時 App 不會壞，只是暫時沒有 recap 入口。
7. **對話檔裡的摘要位置**：
   - 回合摘要寫好時，接在目前那一段的檔尾（spec §2.3：可能排在下一回合內容之後，靠回合編號對應）；
   - recap 同樣接在檔尾；
   - 重建時，回合摘要放回它那一回合之後，recap 放在 `to_no` 那一回合的摘要之後；
   - 寫檔與換段共用同一個 session 鎖。
8. **prompt 由 Opus 寫在附錄 B**，實作照抄進 `prompts/`，不另外改寫。

### 4.1 Reuse（PR 2）

| Need | Existing path:symbol | Decision | Reason | Evidence |
|---|---|---|---|---|
| 派工佇列、租約、每小時上限、reaper | `internal/module/workbook/queue.go` `Engine.enqueue／admit／Next／Result／reap`（:168-561） | new（照基準改寫，§4.0-2） | 原檔混著 journal／refresh／rewrite | 盤點 §1.2、§7-3～§7-5 |
| 結果驗證骨架 | `workbook/validate.go` `objectOf`／`hasDuplicateMember`／`stripFence`／`repairPush`／`cutRunes`（:55-308） | new（搬入改寫） | 欄位不同（7 → 4），骨架照用 | 盤點 §1.2 validate 列 |
| 未回答的結果分類與格式重試 | `workbook/outcome.go` `failedCall`（:94-121）、`formatFailure`（:124-131）；理由常數 `engine.go:14-23` | new（搬入，拿掉 `nothing-to-fork`） | spec §3 要求格式錯重試一次；分類決定摘要的 state／reason | `outcome.go:94-131` |
| Stop 收斂 | `workbook/queue.go` `Engine.Stop`（:512-543） | new（搬入） | leased → failed(stopped)、queued → skipped(stopped)、喚醒長輪詢、等進行中的結果，推播等待才不會等到逾時 | 同左 |
| 回合輸入 | `workbook/input.go` `buildTurnPart`（:91-114）、上限常數（:14-18） | new（搬入） | spec §3：上限與遮蔽沿用 | 同左 |
| 推播等待 | `workbook/lines.go` `PushLines`（:26-123） | new（搬入，資料源換成 `dlg_summaries`） | 等待邏輯不變 | 盤點 §1.1 |
| mod 路由與能力判定 | `internal/modevents/workbook_routes.go` `WorkbookService`、`workbookNext`、`workbookResult`、`streamMayPoll` | reuse（介面拿掉 `RequestRefresh`） | mod 不改 | 盤點 §3.1 |
| 遮蔽秘密 | `internal/redact` `redact.String` | reuse | 工作簿就用它 | `workbook/input.go:60-114` |
| host 事件廣播 | `core.Events.Broadcast`（`workbook/module.go:70-75` 的用法） | reuse | 同樣式 | 同左 |
| 推播設定 | `internal/workbooksettings` `PushWaitS` | reuse | spec §5 保留 | 盤點 §3.1 |

### T7. 拿掉工作簿與日誌（刪除）

- 刪除：
  - `internal/module/workbook/` 整個資料夾（含 prompts、testdata）；
  - `internal/journal/`；`cmd/pdx/journal.go`、`journal_test.go` 與 `cmd/pdx/main.go:130-131` 的 `journal` 子命令；`cmd/pdx/main.go:51,544-546` 的工作簿模組註冊；
  - `internal/config/journal.go` 與 `config.go:325-332,359-360` 的引用、相關測試；
  - `internal/team/journalfeed.go`、`internal/module/team/journalfeed_store.go` 與兩個測試、`internal/module/team/module.go:592` 的註冊。
- 改：
  - `internal/core/info_handler.go:144-153` 拿掉 `workbook.v1`、`workbook.v2`、`journal.*`（`dialogue.v1` 在 T11 加）；
  - `cmd/pdx/device_scope.go:82-92` 拿掉工作簿與日誌路由，`device_scope_test.go:79-81` 跟著改；
  - `internal/modevents/workbook_routes.go`：`WorkbookService` 拿掉 `RequestRefresh`；`workbookRefresh` 一律回 409 `disabled`（§4.0-3）；`ErrNotLive`、`ErrRefreshPending` 沒有使用者就刪；
  - `internal/module/peers/personas_pool_test.go:134` 改用 `ipeers.PoolNameOf`＋`ipeers.PoolDisplayName` 斷言，語意不變；
  - `cmd/pdx/workbook_register_test.go`（斷言 `workbook.db` 與 `/api/info` 的 `workbook.v1`）刪除，T11 另寫 dialogue 的同類測試；
  - `internal/modevents/workbook_refresh_test.go` 的假 service 與 202／`not_live`／`refresh_pending` 斷言，改成「一律 409 `disabled`」，並保留「只宣告 `workbook.refresh` 的 stream 仍可輪詢 `next`」的斷言；
  - `internal/module/team/last_turn.go`、`internal/personas`、`internal/redact` 等只有註解提到工作簿的地方不動。
- 這個 commit 之後的狀態：沒有人註冊 `workbook.jobs` 與 `workbook.push-lines`，mod 的 `next` 拿到 503、推播走沒有工作簿時的原文（`internal/module/push/agent_trigger.go:307-319`）。中間 commit 不部署。
- 驗證：
  - `go build ./...`；
  - `rg -n "module/workbook|internal/journal|JournalFeed|journal\.v1|workbook\.v1" --type go` 沒有結果（`modevents.CapWorkbookV2` 不算）；
  - 跑受影響的套件：`./internal/core/... ./cmd/pdx/ ./internal/modevents/... ./internal/module/modevents/... ./internal/module/team/... ./internal/module/peers/... ./internal/config/... ./internal/module/push/...`；
  - 推播在沒有 push-lines 時照舊送原文：既有測試要能證明，沒有就先補一個。
- 驗收：上述指令全綠；`git show --stat` 只有刪除與列出的改動。

### T8. 派工通道搬進 dialogue　**需要判斷**

- 新增 `internal/module/dialogue/{queue,jobs,outcome,settle}.go`，照基準改寫（§4.0-2），保留：
  - 每個 session 一個佇列（工作簿是每個 lineage 一個；改成 session 是因為 job 只在該 session 自己的程序裡跑）；session 不再能接 job（capability 過期）時，reaper 把它排隊中的 job 結成 skipped(no_mod) 並清掉佇列（T8 審查後加：否則接力後舊 session 的 job 永遠沒人領）；
  - 只有 `turn-summary` 種類受「最多 3 個等待、丟最舊」限制（`queue.go:179-201`）；
  - 每主機每小時 300 次上限（`admit`，`queue.go:236`）；
  - 租約＝timeout＋10 s、過期回收 → failed(lost)；
  - 結果寫入失敗的重試（`settle.go`）：只由 reaper 重試、同一答案同時最多一個在跑，最多 12 次，用完 failed(store)（T8 審查後定）；
  - 種類處理分兩段：`apply` 只持久化（以 pending→done 的 CAS 為閘門，冪等），`after` 在 apply 成功後恰好呼叫一次（接對話檔、喚醒推播、排 recap）；`finish` 只把仍是 pending 的那筆改成終態（T8 審查後定）；
  - `Next` 的長輪詢、結果核對 stream 與租約；
  - `Complete{Model:"haiku", MaxTokens, Effort:"low", TimeoutMS:30000}`；
  - 未回答的結果分類（`outcome.go:94-121`）：`empty-reply` 與格式錯重試一次（attempt 2，同種類），再錯 failed(format)；`api-error` 帶 `authentication_failed` → failed(auth)，其他 → failed(api)；`aborted` 依耗時分 failed(timeout)／failed(stopped)；`refused` → failed(refused)；其他 → failed(api)；
  - Stop 收斂（`queue.go:512-543`）：未在處理中的租約 → failed(stopped)，排隊中 → skipped(stopped)，喚醒所有長輪詢，之後的結果找不到租約，等已開始的結果與回收做完才返回。
- 拿掉：rewrite、refresh、journal、fork、`then`／`followAfterLoss`、`OnAvailability`。
- job 種類：`turn-summary`（線上 `turn`）、`recap`（線上 `turn`）。`Job.MarshalJSON` 兩者都寫成 `turn`。
- `jobs.go`：在 registry 註冊 `workbook.jobs`，實作 `modevents.WorkbookService`（`NextJob`、`JobResult`、`JobWaiting`）與 `Ready()`；`capable` 用 modevents registry 的 `SessionCapable(sid, "workbook.v2", CapsFresh)`。
- 這個 task 只建通道與「把結果交給種類處理函式」的分派，兩個處理函式在 T9、T10 寫，這裡先用測試替身。
- 測試清單（多數從工作簿的 `queue_test.go`、`engine_test.go` 挑出仍適用的改寫）：
  1. 兩個種類在線上都是 `kind:"turn"`；
  2. 第 4 個等待中的 turn-summary 讓最舊的變 skipped（backlog），recap 不受影響；
  3. 每小時上限到了，`Next` 把 job 結成 skipped（cap）；
  4. 別的 stream 回結果得到 `ErrNotLeased`；
  5. 租約過期被回收，job 結成 failed（lost）；
  6. 結果處理函式寫入失敗，reaper 重試；
  7. mod 不 capable 的 session 不排 job；
  8. 結果分類：`empty-reply` 第一次排 attempt 2、第二次 failed(format)；`authentication_failed`、其他 `api-error`、`aborted`（逾時與未逾時）、`refused`、未知理由各自得到上面的 reason；
  9. Stop：排隊中的 → skipped(stopped)、已租出的 → failed(stopped)、等待中的長輪詢立刻返回、Stop 之後送來的結果得到 `ErrNotLeased`、Stop 等到進行中的結果寫完才返回、之後 `Next` 立刻回空、沒有 goroutine 留下（`-race`）。
- 「需要判斷」的理由：鎖、租約、goroutine。完成後派新 context 的 Sonnet 對照基準檔審，重點是有沒有漏掉保留的行為。
- 驗收：測試先紅後綠；dialogue 套件 `-race`；`./internal/modevents/...` 綠。

### T9. 回合摘要　**需要判斷**

- store migration 2：`dlg_summaries(id PK, session_id, conv_key, turn_id, turn_no, turn_at, turn_seq, state, reason, title, push, summary, push_ready_at, usage_in, usage_out, usage_cache_read, latency_ms, created_at, updated_at, UNIQUE(session_id, turn_id))`，索引 `(session_id, turn_at)`。`dlg_sessions` 加 `since_recap INT`。
- 誰要產摘要（intake 寫完對話檔之後決定，規則照工作簿 `pickTurns`，`intake.go:142-231`）：
  - 只看這次新寫進對話檔、已結束、有實質內容（`summarisable`）的回合；
  - 第一次記錄的 session 只取事件指到的那一回合與它之後的，不補歷史（§1-4）；
  - 一次最多 3 則，取最新的；
  - 事件指到的回合用事件的 (At, Seq) 當 `turn_at`／`turn_seq`（推播靠它配對），其他用回合自己的結束時間；
  - session 的 mod 不 capable 時直接記 skipped（no_mod），不排 job。
- 事件處理完（不論有沒有產摘要）通知推播等待「這個 session 這次看過了」（工作簿的 `intakeDone`，`intake.go:62-66`）。
- 輸入：`{"recent_titles": [...前 3 則 done 的 title], "turn": {user_prompt, assistant_text, tools}}`，turn 部分沿用 `buildTurnPart` 與上限、`redact.String`。transcript 沒跟上、hook 文字比較完整時，assistant_text 用 hook 的文字（§1-8）。
- prompt：附錄 B-1，存成 `prompts/turn-summary-v1.txt`，`//go:embed`。
- 驗證：
  - 單一 JSON 物件，允許一層 code fence，四欄必填（`skip`、`title`、`push`、`summary`），不得有重複欄位或多餘內容；
  - 非 skip 時 `title`、`summary` 不可空；
  - 每欄先 `redact.String`；
  - 超過上限一律在字詞邊界截斷（spec §3）：新增 `cutAtWord(s, n)`，先取前 n 個字，若切點落在連續的英數字、`_`、`.`、`/`、`-` 中間，就退到那一串的開頭；退完剩不到 n 的一半才在 n 硬切。中文字之間都算字詞邊界；
  - `title` 16 字，用 `cutAtWord`，不加省略號；`push` 用工作簿的 `repairPush`（40 字，在最後一個標點截，截不出來就丟掉、推播用原文）；`summary` 150 字，先找 150 字內最後一個句號，沒有就 `cutAtWord` 加「…」；
  - 格式錯重試一次（attempt 2），再錯記 failed（format），沿用 T8 的分類。
- 寫入照 T8 的兩段契約：
  - `apply`（一個交易，以 `state='pending'` 為條件的 UPDATE 當閘門）：state done／skipped、四欄、`push_ready_at`、用量；done 且非 skip 時 `since_recap + 1`。閘門沒過（已不是 pending）就回 nil、什麼都不做；
  - `after`（apply 成功後恰好一次）：
    1. 對話檔接上 `<turn-summary N · HH:MM | title>` 與內文（N＝`dlg_turns.turn_no`，HH:MM＝`turn_at` 的本地時間），用 writer 的 session 鎖；
    2. 喚醒推播等待；
    3. `since_recap ≥ 30` 時排一個 recap（T10）。
  - `finish`：只把仍是 pending 的那筆改成 skipped／failed 與 reason，並喚醒推播等待（讓它立刻回 false）。
- 推播等待：基準的 `lines.go` 與 `lines_test.go` **整檔搬入**，只把 store 換成查 `dlg_summaries` 的介面（`ClosestTurn`：`turn_at` 在 [since−2000, since+500]，取最近）。其餘不改，包括：
  - intake 窗口 `intakeBeforeMS=1500`；
  - 每 session 最多 8 個 intake 時間、最多 64 個 session；
  - 先 snapshot 再查 store，避免漏掉喚醒（`lines.go:15-20,49-100`）。
  - 註冊在 `workbook.push-lines`。搬入後 `lines_test.go` 原有案例照過，再加查 `dlg_summaries` 的案例。
- `Start` 把上次留下的 pending 標成 failed（stopped），否則重啟後推播要等到逾時（盤點 §7-13）。
- 重建：writer 的 `Placed` 接點回傳該回合的摘要文字（§4.0-7）。
- 測試清單：
  1. 驗證的表格測試：四欄齊全、少一欄、多一欄、重複欄位、code fence、skip、空 title、各欄超長截斷、push 截不出來被丟掉；`cutAtWord` 對英文單字、路徑、PR 編號、中英混合、整串英數字超過一半長度各一列；
  2. 第一次記錄的 session 只產事件那一回合；
  3. 一次 5 回合只產最新 3 則；
  4. 不 capable 記 skipped；
  5. 假的 mod 回結果 → 對話檔出現摘要區塊、編號正確；
  6. 推播 `Await` 拿到 title／push；skipped 立刻回 false；沒有產摘要的事件在 `intakeDone` 後立刻回 false；
  7. 重啟後 pending 變 failed；
  8. 重建後摘要排在該回合之後。
- 「需要判斷」的理由：推播配對的時間窗與「看過了」的通知時機，錯了推播會變慢或用錯回合。完成後派新 context 的 Sonnet 審。
- 驗收：測試先紅後綠；對驗證與配對的條件做突變，都有測試失敗。

### T10. recap

- store migration 3：`dlg_recaps(id PK, job_id UNIQUE, session_id, conv_key, at, from_no, to_no, trigger, headline, body_json, usage_in, usage_out, latency_ms, created_at)`，索引 `(session_id, id)`。
- 觸發（spec §4）：
  1. 回合摘要寫入後 `since_recap ≥ 30`；
  2. App 打開或手動：`POST /api/dialogue/{sid}/recap`（T11）。
  - 共同規則：沒有比上一則 recap 的 `to_no` 更新的 done 摘要就不產；同一 session 已有排隊或執行中的 recap 就不再排。
- 輸入：最近 30 則 done 且非 skip 的回合摘要 `{"turns":[{"n", "at":"HH:MM", "title", "summary"}]}`，舊到新。
- prompt：附錄 B-2，存成 `prompts/recap-v1.txt`。
- 驗證：
  - 六欄必填：`headline` 字串、`done` 陣列、`now` 字串、`decided` 陣列、`waiting` 陣列、`next` 字串；
  - `headline` 截 60 字；`done` 超過 6 條取前 6 條；每條截 40 字；`now` 截 80 字；`next` 截 60 字；全部先 `redact.String`；
  - 格式錯重試一次。
- 寫入照 T8 的兩段契約：
  - `apply`（一個交易）：以 job id 為唯一鍵新增 `dlg_recaps`（`job_id` 欄位 UNIQUE，已存在就回 nil、什麼都不做），同交易 `since_recap = 0`；
  - `after`（恰好一次）：
    1. 對話檔接上 `<recap HH:MM | turns a–b>` 與結構化內文（格式見 spec §2.2 範例，欄名英文、內文繁中）；
    2. 廣播 host 事件 `dialogue.recap`，值 `{"session_id","recap_id"}`；
  - `finish`：recap 沒有預先建立的列，只需清掉「這個 session 有 recap 在排隊或執行中」的記錄，讓下次觸發能再排。
- 換段時 writer 的 `Carried` 接點回傳這個 session 最新一則 recap 的文字，寫在新段的 `<carried recap HH:MM | turns a–b>` 之下（spec §2.2）。
- 失敗（模型沒回、格式錯兩次）：不寫、計數不歸零，下一則摘要寫入時會再觸發。
- 測試清單：
  1. 驗證的表格測試；
  2. 第 30 則摘要觸發、第 29 則不觸發；
  3. 沒有新摘要時 POST 不排 job；
  4. 已有排隊的 recap 不重複排；
  5. 假的 mod 回結果 → 對話檔出現 recap、`since_recap` 歸零、廣播一次；
  6. 換段時新段帶上一則 recap；
  7. recap 失敗計數不歸零。
- 驗收：測試先紅後綠。

### T11. API、capability、裝置白名單

- 路由（dialogue 模組註冊在 core 的 mux，樣式照工作簿的 `api.go:152-157`）：
  - `GET /api/dialogue/{session_id}/recaps?limit=`：新到舊，預設 20、上限 100；每則 `{id, at, from_no, to_no, trigger, headline, done, now, decided, waiting, next}`；
  - `GET /api/dialogue/{session_id}/turn-summaries?from=&to=`：依回合編號，舊到新，一次最多 100 則；每則 `{turn_no, at, title, summary, push, state}`；
  - `POST /api/dialogue/{session_id}/recap`：回 `{"state":"queued"|"up_to_date"|"unavailable"}`。
- session_id 格式不合回 400；模組沒就緒回 503。
- `/api/info`：dialogue 模組 ready 時宣告 `dialogue.v1`。測試要用真的模組註冊再打 `/api/info`（不是替身）。
- `cmd/pdx/device_scope.go`：三條路由加進白名單（T7 已拿掉舊的）。
- 測試清單：
  1. 各路由的正常、空、分頁上限、400、503；POST 三種 state；
  2. `/api/info` 有 `dialogue.v1`、沒有 `workbook.v1`、`workbook.v2`、`journal.*`（真模組註冊，取代 T7 刪掉的 `workbook_register_test.go`）；
  3. device token：三條 dialogue 路由放行；T7 拿掉的每一條工作簿與日誌路由都被拒（逐條列）。
- 驗收：測試先紅後綠。

### T12. PR 2 驗收

- 端對端測試：真的 conversation 模組＋compact fixture＋假 turn end＋假 mod（直接呼叫 `NextJob`／`JobResult` 回固定 JSON）→ 對話檔有回合摘要；連送 30 回合 → 出現 recap；換段後新段帶 recap；推播 `Await` 拿到 title／push。
- PR 前：改到的套件（T7 列的加 `./internal/module/dialogue/...`）、dialogue 套件 `-race`、`pdx test lint`；mod 沒改，不跑 mod 層。
- 合併前審查：codex 攻擊方（重點：刪除後仍存在的呼叫端、被一起刪掉的推播行為、派工通道的租約與重試、prompt 輸入的遮蔽）＋新 context 的 Sonnet 對照 spec §3–§5、§8。

## 5. PR 3：App 的 recap 檢視

### T13. 原型　**需要判斷**

- Opus 做，先取真 App 畫面當基準（標題列、團隊面板、個人面板的截圖），再在 `docs/pages/` 做互動原型給使用者看：
  - 列表上一行：最新 recap 的 headline＋「截至 HH:MM」；
  - 點開是 recap 歷史，新的在上面；每則可展開全文，再往下展開那個範圍的回合摘要；
  - 「重新產生」按鈕；session 已結束時按鈕停用並說明。
- 原型確認後才進 T14。

### T14. recap 入口與檢視

- 新增 `spa/src/lib/dialogue/`（api、types、WS 事件處理）與 store。能力偵測：host 宣告 `dialogue.v1` 才顯示。
- 換掉的掛載點（盤點 §4.1）：
  - `TitleBar.tsx:204` 的 `JournalButtons`／`TeamNotebookButton`／`PersonalButton` → recap 入口；
  - `TeamPanel.tsx:68`（`TeamSeatWorkbookView`）、`panel-view.ts:14,22-27` 的 workbook 視圖 → recap 歷史；
  - `TeamPanelRow.tsx:17-21,50,65` 的工作簿／日誌行 → 最新 headline；
  - `PersonalPanel.tsx:98`、`PersonalCard.tsx:57-68`、`usePersonalPanel.ts:124-131` → recap；
  - `useRelayBrief.ts:6-8,40-41` 原本用工作簿的狀況與待辦組接力 brief → 改用最新一則 recap（沒有就不附）。
- 打開 recap 歷史時呼叫 `POST …/recap`（有新摘要才會產），收到 `dialogue.recap` 事件就重抓。
- 測試：元件測試照原型的狀態（有 recap、沒有 recap、產生中、session 已結束）；`vitest related` 跑改到的檔案。

### T15. 拿掉工作簿與日誌的 App 程式

- 刪除盤點 §4 列的 `components/journal/*`、`lib/journal/*`、`stores/useJournal*`、`components/team/` 的工作簿元件、`lib/workbook/*`、`stores/useWorkbookStore*`；`main.tsx:21-22,74,76` 的兩個 loader；`App.tsx:24,270` 的 `PanelAreas`；`lib/host-events.ts:24-28` 與 `useMultiHostEventWs.ts` 的工作簿／日誌事件；`unattended-support.ts`、`roster-forget.ts` 的工作簿／日誌能力；i18n 的工作簿與日誌字串；`lib/storage/keys.ts:75-76`。
- 保留：`lib/notify/notify-title.ts:16-37`（推播標題加「・{title}」，內容改由回合摘要提供，邏輯不變）。
- 驗證：`rg -n "workbook|journal" spa/src` 只剩推播標題、自動產生的 icon-meta 與註解；tsc 送 mserv（`tsc -b`）；`vitest related` 跑改到與刪掉檔案的相關測試。

### T16. PR 3 驗收

- 使用者在 Mac App 實際看：標題列與團隊面板的 recap 入口、歷史展開、重新產生。
- 合併前審查：codex 攻擊方＋新 context 的 Sonnet 對照 spec §6 與原型。

## 6. 部署與發版

- PR 1 可以單獨發版（只新增，不影響現有功能）。
- PR 2 與 PR 3 同一版發：PR 2 單獨上線時 App 不會壞（§4.0-6），但會暫時沒有任何入口，沒必要讓使用者經歷。
- 部署後在 mlab 實地確認：
  - 一個 lead、一個 member、一個 solo 各結束幾回合，對話檔、回合摘要、推播文字都正常；
  - 手動 `/compact` 一次，確認換段、新段帶 recap；
  - 舊的 `workbook.db` 與 `<data_dir>/journal/` 原封不動。
- 設定不動（壓縮門檻、self relay 等留給第二個 change）。

## 7. 風險與對策

| 風險 | 對策 |
|---|---|
| 對話檔重複或遺失內容（游標、換段、當機） | T4 標「需要判斷」另派審查；測試涵蓋當機與重建；PR 1 先上線觀察，再接 PR 2 |
| 推播變慢或配錯回合 | 推播等待邏輯原樣搬（T9），時間窗與通知時機不改；測試涵蓋三種回傳 |
| 刪除時漏掉呼叫端 | T7 用 `go build ./...`＋`rg` 證明；攻擊方審「仍存在的呼叫端」 |
| 舊 mod 的 `/workbook refresh` | 路由留著回 409（§4.0-3），第二個 change 拿掉指令 |
| Haiku 用量 | 每回合一次（同現在）＋約每 30 回合一次 recap；journal 的呼叫沒了 |
| 對話檔含原文（可能有秘密） | 與 transcript 同等：資料夾 0700、檔案 0600，放在 daemon 資料夾、不進 repo；送給模型的輸入照舊遮蔽 |

## 附錄 A：盤點

`docs/plans/2026-11-01-session-dialogue-log-inventory.md`（11-01，基準 `16990529d`）。

## 附錄 B：prompt

### B-1 回合摘要（`prompts/turn-summary-v1.txt`）

```
你是回合摘要員。你會收到一個 AI 開發 session 剛結束的一個回合。請只寫這一回合發生的事，不回顧前情、不寫累積進度。

輸入是一個 JSON：
- recent_titles：這個 session 前幾則回合摘要的標題（舊到新），用來讓同一件事的名稱前後一致
- turn.user_prompt：這一回合收到的要求或訊息。開頭是 `[甲 → 乙]`、`[report …]`、`[solo → …]` 或 `Agent "…" finished` 的，是別人（其他 agent 或背景工作）傳來的訊息，不是這個 session 做的事
- turn.assistant_text：這個 session 這一回合最後說的話；turn.tools：這一回合用過的工具
- 這個 session 做了什麼，只看 assistant_text 和 tools。寫到別人做的事，要寫明是誰（例如「ed-72 回報完整測試全過」）。

輸出四個欄位：
- title（16 字以內）：這件事的名字。同一件事沿用 recent_titles 裡的名字，換了事才取新名。要讓人看得出是哪件事，例如「P6-5 接力指令」「alpha.650 部署」「磁碟空間不足」。
- push（目標 25 字、上限 40 字）：手機推播的內文，標題就是 title。一句話：最重要的結果，或需要人做的事。
- summary（150 字以內、最多三句）：依序是做了什麼；最關鍵的決定或理由；結果或下一步（沒有就省略那句）。句與句之間用句號，不換行。
- skip：這一回合沒有新進展（寒暄、單純確認收到、只是在等、沒有文字的通知）就設 true，其他三欄填空字串。

寫法規則：
- 繁體中文。PR 編號、版本、指令名保留原文。
- 不寫 commit sha、行數、檔數、檔名、函式名、審查逐條細節；測試個數只有在它就是結果時才寫（例如「3 個測試失敗」）。
- push 與 summary 都不重複 title；push 不要只是 summary 的開頭截斷。
- 提到人用角色：lead、member、使用者，或對方的名字（例如 ed-72、88）。不用「你」「我」，不寫「本回合」「這一回合」「助理」「AI」「接續」「承上」。
- 不寫秘密（token、密碼、金鑰），不貼程式碼。

輸出格式：
{"skip": false, "title": "…", "push": "…", "summary": "…"}

好的例子：
- title「P6-5 接力指令」
  push「審查問題已修，等 lead 放行 merge」
  summary「審查指出 exit 12 同時代表已取消和等待被中斷，lead 可能誤判。保留 12 以與既有指令一致，改在說明寫清楚並補測試。PR #2222 等 lead 放行。」
- title「磁碟空間不足」
  push「清出 33.8 GiB，磁碟降到 83%」
  summary「Data 卷滿導致 ed-72 的 go test 失敗，主因是多個 worktree 各存一份編譯快取。刪掉兩小時前的快取，釋出 33.8 GiB。」

不好的例子：
- push「PR #2191 已開，兩輪 codex review 跑完，R1 無問題，攻擊方五條中兩條已修…」：太長，推播一眼看不完。
- summary「改了 relay.go。測試過了。」：看不出為什麼、改完怎樣。

只輸出一個 JSON 物件，不要其他文字、不要 code fence。
```

### B-2 recap（`prompts/recap-v1.txt`）

```
你是 recap 撰寫員。你會收到一個 AI 開發 session 最近的回合摘要（最多 30 則，舊到新）。請寫一則 recap，讓離開一陣子的人或接手的 agent 一眼看懂這段時間的近況。只根據這些回合摘要寫，摘要裡沒有的事不要推測。

輸入是一個 JSON：
- turns：每則 {n, at, title, summary}；n 是回合編號，at 是時間（HH:MM）

輸出六個欄位：
- headline（60 字以內）：一句話，這段時間最主要的事與現在的狀態
- done：這段時間做了什麼，3 到 6 條，每條 40 字以內，重要的放前面；同一件事的多個回合合成一條
- now：現在在做什麼、做到哪（80 字以內）
- decided：這段時間定下來的決定，每條 40 字以內；沒有就空陣列
- waiting：在等使用者回答或決定的事，每條 40 字以內；沒有就空陣列
- next：下一步（60 字以內）

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
