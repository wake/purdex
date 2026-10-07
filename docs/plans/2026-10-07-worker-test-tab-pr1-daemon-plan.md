# Worker「測試用」分頁與 registry 標題退路 — PR-1（daemon）實作計畫

日期：2026-10-07　負責：purdex-37　審查：purdex-d3（帶 spec 一起審）
Spec：`docs/specs/2026-10-07-worker-test-tab-and-registry-titles-spec.md`（§7 PR-1）
範圍：只做 daemon。SPA（PR-2）等本 PR 部署後。

## 0. spec 與現況對不上的地方（需 d3 定奪，我不自行改設計）

1. **§4.1「清除：gone 且超過現有保留期時一併刪」——現況沒有保留期。**
   `conversation_index` 的列**永不刪除**（`internal/store/conversation_index.go` 型別註解：Rows are never deleted），scan 也沒有清理流程。
   本計畫的處理：`conversation_names` **同樣不刪**（與索引同生命期；一列約一個 UUID＋≤120 runes 名字，量級＝對話數）。若要真的清除，需要先有索引的保留期設計，那是另一件事。→ 請確認「不刪」OK。
2. **§7 PR-1 清單沒有列 capability，但 §7 末段要求 PR-2 以 `conversations.scope` 啟用。**
   `internal/core/info_handler.go` 的規則是「新增名稱要在出貨該功能的同一個變更加進 `capabilities`」。本計畫在 PR-1 加 `"conversations.scope.v1"`（沿用現有 `transcript.v1` 的命名風格）。→ 請確認名稱；若堅持 `conversations.scope` 我就照改。
3. **§4.1「在 scan 與 peer registry 讀取時寫入」——兩處在不同 module。**
   scan 在 nex module（`internal/module/nex/conversations_http.go`、`internal/conversations`），registry entries（含 `SessionID`、`Name`）在 peers module（`LiveEntries`／`internal/peers/registry.go`）。nex 不應 import peers module。
   本計畫的處理：**只在 peers module 寫入**（它本來就在每次 inventory 週期讀 registry），nex 只**讀** `conversation_names`（兩邊共用 `store.MetaStore` 的新 `ConversationNames()`）。scan 不寫名字，因此「scan 整列覆蓋洗掉名字」的風險天然不存在（仍保留變異驗證測試）。

## 1. 任務（每個獨立 commit，TDD：先寫失敗測試）

### T1 `isTestCwd`（daemon）
- 新檔 `internal/conversations/testcwd.go`：`IsTestCwd(cwd string) bool`。規則見 spec §3.1：空字串否；`filepath.Clean` 後，開頭 `/tmp` 正規化為 `/private/tmp`（只對「整段 `/tmp` 或 `/tmp/…`」，`/tmpfoo` 不算）；等於 `/private/tmp` 或以 `/private/tmp/` 開頭為是；不含 `/var/folders`。
- 共用案例表放 `internal/conversations/testdata/test-cwd-cases.json`（`[{"cwd":…,"want":bool}]`），**PR-2 的 SPA 單元測試讀同一份**（以相對路徑 import）。案例：`/tmp/x`✓、`/tmp`✓、`/private/tmp`✓、`/private/tmp/a/b`✓、`/private/tmpfoo`✗、`/tmpfoo`✗、`/private/tmp/a/../../etc`✗、`/private/tmp/../tmp/x`（Clean 後 `/private/tmp/x`）✓、`""`✗、`/var/folders/ab/T/x`✗、`/Users/w/tmp`✗、相對路徑 `tmp/x`✗。

### T2 `conversation_names` 表與 store
- `internal/store/conversation_names.go`：`migrateConversationNames`（`CREATE TABLE IF NOT EXISTS`，欄位如 spec §4.1）、`ConversationNames()` 取得 store、`Upsert(ctx, sessionID, name, nowMs)`（正規化：lowercase UUID、TrimSpace、空字串拒絕、截 120 runes）、`All(ctx) (map[string]string, error)`（sid→name）。接進既有 migrate 流程（與 `migrateConversationIndex` 同處）。
- 測試：upsert 覆寫、空名拒絕、截斷在 rune 邊界、sid 大小寫正規化、`All`。

### T3 peers module 寫入（活著時記名）
- peers module 在每次 inventory 建構後，對 `LiveEntries` 的每個 entry：有 `SessionID`（有效 UUID）且 `ipeers.RoutableName(Name)` ⇒ 候選。記憶體 map `sid → {name, writtenAt}`；**name 與記憶值不同、或距上次寫入 > 1 小時**才呼叫 store.Upsert；寫入失敗只 log（不影響 inventory）。
- 以注入的 `NameSink interface{ Upsert(ctx, sid, name string, nowMs int64) error }` 接線（nil ＝ 不記錄，既有測試不變）；production 接 `MetaStore.ConversationNames()`。
- 測試：首次寫入、同名 1 小時內不重寫、改名立刻寫、超過 1 小時重寫、unroutable name／無 session id 跳過、store 失敗不拋、nil sink 不 panic。

### T4 標題退路新階
- `internal/module/nex/conversations.go`：新常數 `titleFromRegistry = "registry"`；`conversationInputs` 加 `Names map[string]string`（sid→name，由 handler 從 store 讀入；讀失敗＝空 map＋log）；`conversationTitle(s, row, nexenTitle, registryName)` 在 nexen 之後、prompt 之前插入。
- 測試（spec §6）：有名無 prompt→registry；有名有 prompt→registry；有 ai 標題→ai（registry 不蓋）；有 custom→custom；有 nexen→nexen；名字僅空白→略過往下；無名→行為與現況完全相同（既有表格測試不動）。
- `conversationRow.TitleSource` 註解補 `"registry"`。

### T5 `?scope=test|normal|all`
- `handleConversations` 解析 `scope`（預設 `all`；其他值 → 400 `bad_scope`）。過濾在**排序與 cap 之前**、以 `IsTestCwd(r.Cwd)`（`r.Cwd` ＝ `firstNonEmpty(row.Cwd, stint.Cwd)`，即回應所顯示的 cwd）；`total`、`truncated` 以過濾後為準。
- `unknown_owner`：該分支在建列前 `continue`，需在計數前取得同一個 cwd 規則（`firstNonEmpty(index[s].Cwd, latest[s].Cwd)`）並套同一過濾，使三種 scope 的 unknown_owner 互斥且加總等於 all。
- 實作位置：`buildConversations` 加一個 `scope` 參數（或在其輸出上過濾並另算 unknown_owner——以測試先行決定較小的改動；兩者結果必須一致）。
- 測試（spec §6）：同一資料集（含 `/tmp/x`、`/private/tmp/y`、一般路徑、空 cwd、unknown_owner 兩側）三種 scope 互斥、聯集＝all；`total`/`truncated` 在 cap 邊界以過濾後為準（例：test 側超過 cap、normal 側未超過）；`scope` 非法值 400；缺省＝all（舊 client 行為不變）。
- capability：`internal/core/info_handler.go` 加 `"conversations.scope.v1"`＋對應測試（沿用該檔既有 capability 測試）。

### T6 變異驗證與整合
- 變異（寫進 PR 描述）：(a) 把名字改存進 `conversation_index` 欄位並讓 `UpsertBatch` 覆蓋 → 「scan 覆蓋不洗掉名字」測試翻紅；(b) `IsTestCwd` 去掉 Clean → `../` 案例翻紅；(c) scope 過濾移到 cap 之後 → total/truncated 測試翻紅；(d) 標題階層順序對調 → 順序測試翻紅。
- 全套：`go vet ./...`、`go test ./internal/... ./cmd/... -count=1`。

## 2. 不做
- 不改 ended/gone 的現有 R-4-* 規則、不加新動作、不回填歷史名字、不收 `/var/folders`、不碰 SPA（PR-2）。
- 不改 `conversation_index` schema。

## 3. 風險與回滾
- 新表為 `IF NOT EXISTS`，舊 binary 回滾時表留著無害。
- `scope` 預設 `all`＝舊行為，無 PR-2 時零影響。
- 寫入在 peers inventory 路徑上：以注入 sink＋失敗只 log 隔離，不得讓 inventory 變慢（寫入頻率受 1 小時／改名門檻限制；單次 upsert 為單列）。
