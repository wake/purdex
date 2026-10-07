# Worker「測試用」分頁與 registry 名稱標題退路（spec）

日期：2026-10-07　統籌：purdex-d3　狀態：使用者已定案（見 §1），待 plan／實作
前置：conversation-entity spec（§9 分頁、§13 清單、R-4-10 標題）

## 1. 使用者定案（2026-10-07，不翻案）

- **U1**：cwd 在 `/private/tmp` 底下的對話，放進 設定 → Worker 的獨立「測試用」分頁。**不是**在「已退出」裡隱藏＋開關。
- **U2**：「測試用」分頁收這類對話的**所有狀態**（執行中、已退出、gone），顯示方式維持正常（同一般清單的列與動作）。
- **U3**：標題只有 session id 前 8 碼的對話（多半是靠 peer 訊息驅動、長時間跑的 session），在 session 還活著時由 Purdex 從 registry 記下它的 name，結束後拿來當標題退路。

## 2. 名詞與現況（已查證）

- 分頁註冊：`spa/src/lib/register-modules/index.tsx:268-271` 以 `registerWorkerSettingsTab` 註冊 appearance／workers(10)／exited(20)／gone(30)；分頁為 `hostScoped`、依 `order` 排序。
- 「Workers」分頁（`WorkerLiveTab` → `HostWorkerRows`）列的是**執行中的 execution**（`useHostExecutions`＋`liveEntityRows`），資料型別 `ExecutionSummary` 有 `cwd`。
- 「已退出／Gone」分頁列的是 daemon `GET /api/nex/conversations?state=ended|gone`（`internal/module/nex/conversations.go`），列有 `cwd`、`title`、`title_source`。
- 標題退路（R-4-10，`conversationTitle`）：custom → ai → nexen → prompt 首行 → session id 前 8 碼。
- 對話索引 `conversation_index`（`internal/store/conversation_index.go`）由 `internal/conversations/scan.go` 掃 transcript 後整列 `UpsertBatch` 寫入。

## 3. 「測試用」分頁

### 3.1 判定規則

一個對話屬於「測試用」，若其 cwd 經 `filepath.Clean` 後等於 `/private/tmp` 或以 `/private/tmp/` 開頭。同時視 `/tmp`（macOS 符號連結）為同一處：比對前先把開頭的 `/tmp` 正規化成 `/private/tmp`。不含 `/var/folders/…`（使用者定案只指 tmp）。

- cwd 為空或不存在者**不**屬於測試用。
- 判定函式只寫一份：daemon 端 `isTestCwd(cwd string) bool`，SPA 端 `isTestCwd`（`lib/nex/test-cwd.ts`）同語意，兩邊各自的單元測試用同一組案例表（含 `/tmp/x`、`/private/tmp`、`/private/tmpfoo`〔否〕、`/private/tmp/a/../../etc`〔否，Clean 後在 tmp 外〕、空字串〔否〕）。

### 3.2 分流（每一列只出現在一處）

| 來源 | 一般分頁 | 測試用分頁 |
|---|---|---|
| execution（執行中） | Workers：排除 `isTestCwd(cwd)` 的列 | 只含 `isTestCwd(cwd)` 的列 |
| ended | 已退出：排除 | 只含 |
| gone | Gone：排除 | 只含 |

- 排除發生在**呈現層**：daemon 的 `?state=` 回應不變，另加查詢參數 `?scope=test|normal|all`（預設 `all`，保持舊 client 行為）。已退出／Gone 分頁改打 `scope=normal`，測試用分頁打 `scope=test`。`total`、`truncated`、`unknown_owner` 都以過濾後為準。
- 執行中的列沒有 daemon 端過濾（list store 是共用的）：`HostWorkerRows` 加 `filter?: 'normal' | 'test'` prop，在 `liveEntityRows` 之後以 `isTestCwd` 過濾。**其他用到 `HostWorkerRows`／`liveEntityRows` 的畫面（New Tab 的 Workers、活動列）不變**，仍顯示全部——測試用 worker 在別處照常看得到，只有設定頁的 Workers 分頁排除它。
- 一列的狀態以其主體現況為準（`liveEntityRows` 對應到的 conversation 若同時出現在 ended，不重複計；沿用現有 §4.2 規則，本 spec 不改狀態判定）。

### 3.3 分頁本體

- 新分頁 id `test`、`labelKey: settings.worker.tabs.test`（「測試用」）、`order: 40`（Gone 之後）、`hostScoped: true`。
- 內容＝三段式清單，依序「執行中」「已退出」「已不見（gone）」，每段沿用該狀態一般分頁的列元件與動作（退出、重建…、開啟），**不新增動作**。段內為空時該段不顯示；三段皆空時顯示空狀態文案。
- 搜尋框沿用 `matchesConversationQuery`，三段共用一個關鍵字。
- 載入、錯誤、truncated、root_error 各段各自處理，與一般分頁相同。
- i18n：新增 `settings.worker.tabs.test`、`settings.worker.test.empty`、三段標題三個鍵；zh-TW 與英文都補。

## 4. registry 名稱當標題退路

### 4.1 取得與儲存

- daemon 在 conversation 掃描（scan）與 peer registry 讀取時，對每個**活著**且 registry 有 `session_id` 與可 routable 的 `name` 的 session，upsert 一筆到新表：

```sql
CREATE TABLE IF NOT EXISTS conversation_names (
  session_id TEXT PRIMARY KEY,  -- lowercase UUID
  name       TEXT NOT NULL,     -- 非空、TrimSpace、≤120 runes
  seen_at    INTEGER NOT NULL   -- Unix ms，最近一次在 registry 看到
);
```

- **不放進 `conversation_index`**：scan 會整列 `UpsertBatch` 覆蓋，名字會被洗掉。獨立表由 `internal/store` 提供 `Upsert(ctx, id, name, nowMs)`／`All(ctx)`，建表用 `CREATE TABLE IF NOT EXISTS`（Alpha 不做 migration）。
- 寫入時機：與 peer inventory 同一個週期；只在 name 與現值不同或超過 1 小時才寫（避免每次都寫 DB）。沒有 `session_id` 的 entry 跳過。
- 名字取 registry 的 `name`（Claude Code 註冊表的對話名，即 pdx 地址的 `<name>`），**不取** `title`。
- 清除：**不清除**（2026-10-07 統籌裁定，修訂原文）。`conversation_index` 目前沒有保留期、列永不刪除，名稱表與索引同生命期；一列約一個 UUID 加 ≤120 runes，量級＝對話數。若日後索引加了保留期，名稱表再同步。
- 寫入端的選名規則（實作）：同一個 session id 在同一輪有多個 live entry 時，每輪只選一個名字——已記錄且仍 live 的優先（不來回改寫），否則取字典序最小；proxy／helper entry 不記；一個 sid 寫入進行中若又看到新名字，寫完立刻續寫（最後一次看到的為準）；節流狀態只保留當輪 live 的 session。

### 4.2 標題退路順序（修改 R-4-10）

custom → ai → nexen → **registry 名稱（新，`title_source: "registry"`）** → prompt 首行 → session id 前 8 碼。

- registry 名稱放在 prompt 之前，因為這類 session 的首則 prompt 多半是 `<cross-session-message`（已被規則排除、不當標題），即使有 prompt 也多是任務描述而非身分；名字比它更能辨認。
- `conversationTitle` 多一個參數（registry name），`title_source` 增加 `'registry'`（SPA 型別 `ConversationRow.title_source` 同步加）。
- 已經有 custom／ai／nexen 標題者不受影響。

### 4.3 邊界

- 活著時才記；沒被記到過名字的歷史對話（現有那 45 筆中已退出且從沒被 daemon 看過活著的）**無法回填**，維持 session id 前 8 碼。這是已知限制，不做回填。
- 名字會變：以最近一次看到的為準；對話結束後不再更新。**已知限制**：registry 沒有可用的更新時間，daemon 重啟後（節流狀態清空）若同一 session 同時有多個不同名字的 live entry，依 §4.1 的選名規則決定而非依新舊；罕見且只影響顯示。
- 信任邊界：registry 的 `sessionId`／`name` 是同一個 uid 的行程自行寫入的未驗證欄位，peer 路由本來就信任它們（`internal/peers/address.go`）；名稱表沿用同一個信任模型，不另做 session 所有權綁定。
- 同名不處理（標題僅顯示用，id 是主鍵）。

## 5. 不做

- 不改 execution／對話的狀態判定；不加新動作；不做「隱藏」「開關」。
- 不收 `/var/folders`、`$TMPDIR` 等其他暫存位置。
- 不回填歷史對話的名字。
- 不改 Gone 與已退出的現有 R-4-* 規則，僅其資料來源多一個 `scope`。

## 6. 測試

- `isTestCwd`：daemon 與 SPA 共用案例表（§3.1）。
- daemon：`scope=test|normal|all` 三種對同一資料集的分流互斥且聯集等於 all；`total`／`truncated` 以過濾後為準；`conversationTitle` 的順序案例（有 registry 名無 prompt、有 registry 名有 prompt、有 ai 標題時 registry 不蓋）；`conversation_names` 的 upsert 與 scan 的整列覆蓋互不影響（變異驗證：把名字放回 index 表要翻紅）。
- SPA：Workers／已退出／Gone 分頁排除測試 cwd、測試用分頁三段各自顯示；`HostWorkerRows` 不帶 `filter` 時行為不變（New Tab 的 Workers 不受影響）。
- 驗收（真機，Mac App）：用 `/private/tmp` 開一個 worker 與一個終端對話，確認它們只出現在「測試用」；一個靠 peer 訊息驅動的 session 結束後，已退出清單顯示其 registry 名稱而非 8 碼。

## 7. 切 PR

- **PR-1（daemon）**：`conversation_names` 表與寫入、`conversationTitle` 新階與 `title_source: registry`、`isTestCwd`、`?scope=`。
- **PR-2（SPA）**：`isTestCwd`、`title_source` 型別、已退出／Gone 改 `scope=normal`、`HostWorkerRows.filter`、新分頁與 i18n。
- 兩者互不依賴介面以外的程式；PR-1 先上、部署後 PR-2 才不會在舊 daemon 上讓 `scope` 被忽略（舊 daemon 忽略 `scope` 時，一般分頁會多顯示測試對話，測試用分頁會顯示全部——PR-2 因此要求 `/api/info` 的 capabilities 含 `conversations.scope.v1`（PR-1 加入，統籌裁定的名稱）才啟用，否則測試用分頁不註冊、一般分頁維持現狀）。
