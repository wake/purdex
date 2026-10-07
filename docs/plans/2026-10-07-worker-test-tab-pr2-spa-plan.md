# Worker「測試用」分頁與 registry 標題退路 — PR-2（SPA）實作計畫

日期：2026-10-07　負責：purdex-37　審查：purdex-d3
Spec：`docs/specs/2026-10-07-worker-test-tab-and-registry-titles-spec.md`（§3、§4.2 的 SPA 部分、§7 PR-2）
前置：PR-1（daemon，#1757／alpha.552）已部署 mlab。只動 SPA。

## 0. 與 spec 的兩處差異（已回報 d3，採建議做法）

1. **「舊 daemon 時測試用分頁不註冊」做不到**：分頁在 module 載入時靜態註冊且為 `hostScoped`（多台 host、各台 daemon 版本不同）；而且 SPA 目前不保存 daemon `/api/info.capabilities`。做法：
   - 分頁永遠註冊；`nex-host` store 的 entry 多存 `daemonCapabilities?: string[]`（optional，現有測試的 entry literal 不用改）；selector `selectConversationsScope(hostId)`＝該 host 的 `daemonCapabilities` 含 `conversations.scope.v1`。
   - 沒有該 capability 的 host：測試用分頁只顯示一行說明（不打 API）；Workers 分頁不套用測試過濾；已退出／Gone 照送 `scope=normal`（舊 daemon 忽略＝維持現狀）。
2. **「執行中」段的搜尋**：列是 `ExecutionSummary`，`matchesConversationQuery` 吃 `ConversationRow`。做法：新增 `matchesExecutionQuery`（比對 cwd〔含 `~` 顯示形〕、brief、id、provider，不分大小寫），已退出／Gone 兩段仍用 `matchesConversationQuery`；三段共用一個關鍵字。

## 1. 任務（每個獨立 commit，TDD；測試先紅）

### S1 `isTestCwd` ＋ title_source 型別
- `spa/src/lib/nex/test-cwd.ts`：`isTestCwd(cwd?: string): boolean`，語意同 daemon `IsTestCwd`：空→否；POSIX normalize（解 `.`／`..`、合併 `//`、去尾斜線；不用 Node `path`，自寫）；開頭整段 `/tmp` 正規化成 `/private/tmp`；等於 `/private/tmp` 或以 `/private/tmp/` 開頭為是；相對路徑否。
- 測試讀**同一份**案例表 `internal/conversations/testdata/test-cwd-cases.json`（vitest 以相對路徑 import json），逐案斷言。
- `lib/nex/conversations-api.ts`：`ConversationRow.title_source` 加 `'registry'`；`ConversationState` 不變；`listConversations(hostId, state, scope?)` 多一個可選 `scope: 'test' | 'normal'`（有值才帶 `&scope=`；`all` 不帶）。測試：URL 組裝。

### S2 daemon capability 進 nex-host store
- `lib/nex/nex-host-effects.ts` 的 fetch：`data.capabilities` 為陣列時取其中的字串，否則 `[]`；寫進 `Loaded`／`NexHostEntry.daemonCapabilities`（optional）。`useNexHostStore.ts` 加 `selectConversationsScope(hostId)`。
- 測試：capabilities 有／無／非陣列／含非字串元素；selector 在 phase 非 ready 時回 false；既有 reducer 測試不動。

### S3 `useConversations` 帶 scope、已退出／Gone 改送 `scope=normal`
- `useConversations(hostId, state, scope?)`；pending 去重 key 與 hook key 都含 scope；`WorkerExitedTab`／`WorkerGoneTab` 傳 `'normal'`。測試：兩個分頁都以 `scope=normal` 發請求；同一 host 同一 state 不同 scope 不共用 pending。

### S4 `HostWorkerRows.filter`
- 新 prop `filter?: 'normal' | 'test'`；在 `liveEntityRows` 之後以 `isTestCwd(row.cwd)` 過濾（`normal` 排除、`test` 只留）；不帶 `filter` 行為完全不變（New Tab 的 Workers、活動列不受影響——測試鎖定）。`WorkerLiveTab` 在 `selectConversationsScope` 為真時傳 `filter="normal"`，否則不傳。
- 注意 `useRowExit(hostId, live)` 與空狀態、loading 判斷都用過濾後的 `live`。
- 測試：有 `/tmp/x` 與一般 cwd 的列→normal 只留一般、test 只留測試；無 filter 兩者皆在；空狀態文案在過濾後為空時出現。

### S5 「測試用」分頁
- `components/settings/WorkerTestTab.tsx`：三段（執行中＝`HostWorkerRows filter="test"`、已退出＝`useConversations(host,'ended','test')`、已不見＝`useConversations(host,'gone','test')`），沿用已退出／Gone 的列元件與動作（`ConversationRow`、`rebuildConversation`——把 `WorkerExitedTab` 內的 `rebuildConversation` 抽出共用，不複製）。段內為空不顯示該段；三段皆空＋非 loading 顯示 `settings.worker.test.empty`；載入／錯誤／truncated／root_error 各段各自處理（沿用既有 testid 命名，前綴 `worker-test-live|exited|gone`）。
- 一個搜尋框（`worker-test-search`），關鍵字傳給三段；執行中段用 `matchesExecutionQuery`（新增 `lib/nex/execution-search.ts`＋測試）。
- 沒有 `selectConversationsScope` 的 host：只顯示 `settings.worker.test.unsupported`，不呼叫 API。
- 註冊：`register-modules/index.tsx` 加 `registerWorkerSettingsTab({ id: 'test', labelKey: 'settings.worker.tabs.test', order: 40, hostScoped: true, component: WorkerTestTab })`；`register-modules.test.ts` 加案例（順序在 gone 之後）。
- i18n（en／zh-TW 兩邊）：`settings.worker.tabs.test`（測試用／Test）、`settings.worker.test.empty`、`settings.worker.test.unsupported`、`settings.worker.test.section.live|exited|gone` 三個段標題。locale 一致性測試要綠。
- 測試：三段各自顯示與各自空；三段皆空的空狀態；關鍵字同時過濾三段；不支援 capability 的 host 不發請求；rebuild 動作與已退出分頁一致（抽出的共用函式有測試）。

### S6 標題來源顯示（若有 title_source 的 UI 分支）
- `rg title_source spa/src` 檢查是否有依 `title_source` 分支的元件（排序、圖示、tooltip）；`'registry'` 需有與 `'prompt'`／`'nexen'` 同等的處理或明確 fallthrough。沒有分支則只需型別（S1）。

### S7 spec 同步與「舊 daemon 不變」測試（d3 要求）
- 修改 `docs/specs/2026-10-07-worker-test-tab-and-registry-titles-spec.md`：§3.3 的搜尋一行改成「已退出／gone 兩段沿用 `matchesConversationQuery`，執行中段用 `matchesExecutionQuery`（比對 cwd、brief、id、provider），三段共用一個關鍵字」；§7 末段「測試用分頁不註冊」改成「測試用分頁永遠註冊；host 的 `/api/info.capabilities` 沒有 `conversations.scope.v1` 時顯示說明、不打 API，Workers 分頁不套用測試過濾，已退出／Gone 送 `scope=normal`（舊 daemon 忽略，行為不變）」。
- 測試：在**沒有** `conversations.scope.v1` 的 host 上，(a) Workers 分頁列出全部執行中的列（含 `/tmp/x`）、不傳 `filter`；(b) 已退出與 Gone 分頁照常渲染 daemon 回的所有列（請求帶 `scope=normal` 但回應不被客戶端再過濾）；(c) 測試用分頁只顯示說明、`listConversations` 與執行列表都沒被呼叫。

## 2. 不做
- 不改 daemon；不新增動作；不改 New Tab／活動列的 Workers；不改狀態判定。
- 不處理「registry 名稱」的產生（PR-1 已做）。

## 3. 驗收（部署後，Mac App）
- 設定 → Worker 出現「測試用」分頁；用 `/private/tmp` 開一個 worker 與一個終端對話，只在「測試用」出現（執行中段），Workers 分頁不含；其結束後在「測試用」的已退出段；一般分頁不含。
- registry 名稱：靠 peer 訊息驅動的 session 結束後，已退出清單顯示其名稱而非 8 碼（名稱要等 session 活著被 daemon 記到之後才有）。

## 4. 風險與回滾
- 只加不減：舊行為在沒有 capability 時完全不變；回滾＝revert 單一 PR。
- 規模預期：約 400–600 行（含測試），一個 PR。
