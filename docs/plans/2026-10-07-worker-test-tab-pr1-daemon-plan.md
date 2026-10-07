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
- 測試：upsert 覆寫、空名拒絕、截斷在 rune 邊界、sid 大小寫正規化、`All`；**`seen_at` 在 conflict update 時更新**（codex #7）：store 提供僅供測試用的 `seenAt(ctx, sid)` accessor（unexported，同 package 測試讀），先 Upsert(t0) 再 Upsert(t1>t0) 同名，斷言 seen_at=t1、名字不變。

### T3 peers module 寫入（活著時記名）
- 掛點：`internal/module/peers/module.go` 的 inventory 建構，緊接 `m.warnNewerCCVersions(entries)` 之後呼叫 `m.observeNames(entries)`（該處已有本週期的 registry entries）。
- 規則：entry 有有效 UUID 的 `SessionID` 且 `ipeers.RoutableName(Name)` ⇒ 候選。記憶體 map `sid → {name, writtenAt}`；**name 與記憶值不同、或距上次寫入 > 1 小時**才 Upsert。
- **鎖（codex #3）**：inventory 可並行，map 與節流狀態放在 `nameWriter` 結構內以 `sync.Mutex` 保護；Upsert 本身在鎖外執行前先「預留」不行（失敗要能重試），故流程為：鎖內決定要寫哪些 → 解鎖 → 逐筆 Upsert → 成功的才鎖內回寫 `{name, writtenAt}`。
- **失敗不推進節流狀態（codex #4）**：Upsert 失敗只 log，**不**更新該 sid 的 name／writtenAt，下一輪 inventory 自然重試。
- 以注入的 `NameSink interface{ Upsert(ctx, sid, name string, nowMs int64) error }` 接線；`peersmod.New(audit, titles)` 之後加 `.WithNameSink(sink)`（nil＝不記錄，既有測試不變）。**production wiring**：`cmd/pdx/main.go` 在 `c.AddModule(peersmod.New(audit, titles))` 處，`meta != nil` 時 `.WithNameSink(meta.ConversationNames())`。
- 測試：首次寫入、同名 1 小時內不重寫、改名立刻寫、超過 1 小時重寫、unroutable name／無 session id 跳過、nil sink 不 panic；**store 失敗後下一輪重試成功**（第一輪 sink 回錯 → 狀態不推進 → 第二輪再呼叫並成功，之後同名才節流）；**並行測試**：N 個 goroutine 同時 observeNames（含改名與相同名），`go test -race` 無競態且每個 (sid,name) 寫入次數符合節流規則。

### T4 標題退路新階（含 nex 讀取的 production wiring）
- `internal/module/nex/conversations.go`：新常數 `titleFromRegistry = "registry"`；`conversationInputs` 加 `Names map[string]string`（sid→name）；`conversationTitle(s, row, nexenTitle, registryName)` 在 nexen 之後、prompt 之前插入；`conversationRow.TitleSource` 註解補 `"registry"`。
- **讀取 wiring（codex #2）**：nex Module 新增 `convNames ConversationNameReader`（`interface{ All(ctx) (map[string]string, error) }`）與 setter `WithConversationNames(r)`；`collectConversations` 在讀 index 之後呼叫 `m.convNames.All(ctx)`（nil＝空 map；失敗＝空 map＋log，**不讓整個 snapshot 失敗**——名字只是退路）。`cmd/pdx/main.go` 在既有 `nexMod.WithConversationIndex(meta.Conversations())` 的 `meta != nil` 區塊內加 `.WithConversationNames(meta.ConversationNames())`。
- 名字在 snapshot 建立時併入（snapshot 內的 title 已含 registry 階）；名字的新鮮度受 `convReuse` 影響，可接受（標題只作退路顯示）。
- 測試：`conversationTitle` 順序案例（有名無 prompt→registry；有名有 prompt→registry；有 ai→ai；有 custom→custom；有 nexen→nexen；名字僅空白→略過；無名→與現況相同，既有表格測試不動）；**整合測試（HTTP）**：以真的 `store.MetaStore`（臨時 DB）先 `ConversationNames().Upsert` 一個名字、index 放一列無標題無 prompt 的 ended 對話，打 `GET /api/nex/conversations?state=ended`，斷言回應 `title=<name>`、`title_source="registry"`；names reader 失敗時對話仍列出且退回 prompt／8 碼。

### T5 `?scope=test|normal|all`（snapshot 不可變）
- **不改 `buildConversations` 的輸入與快取語意（codex #1）**：snapshot 跨 request 共用、排程掃描沒有 request scope，所以 scope **只在 handler 對副本過濾**，`snap.res.Ended/Gone` 本身不被修改、不被排序、不被重新切片後寫回。
- 為了讓 `unknown_owner` 也能依 scope 計：`buildConversations` 額外把每個 unknown-owner 的 cwd（`firstNonEmpty(index[s].Cwd, latest[s].Cwd)`）記進結果的新欄位 `UnknownOwnerCwds []string`（`json:"-"`，與 scope 無關、純資料，snapshot 建立時一次算好）。
- handler：解析 `scope`（預設 `all`；其他值 → 400 `bad_scope`）；`scope=all` 走原路徑（零行為差異）；否則在**新配置的 slice**（`make` 後逐列 append，不得 `rows[:0]` 之類共用底層陣列）上以 `conversations.IsTestCwd(r.Cwd)` 過濾，再套 cap；`total`、`truncated` 以過濾後為準；`unknown_owner` 以 `UnknownOwnerCwds` 同規則計。
- capability：`internal/core/info_handler.go` 加 `"conversations.scope.v1"`＋對應測試（沿用該檔既有 capability 測試）。
- 測試（spec §6＋codex #6）：同一資料集（含 `/tmp/x`、`/private/tmp/y`、一般路徑、空 cwd、unknown_owner 兩側）三種 scope 互斥、聯集＝all；`total`／`truncated` 在 cap 邊界以過濾後為準（test 側超 cap、normal 側未超）；非法值 400；缺省＝all；**同一份快取上依序查 test→normal→all→test 結果互不影響、`snap.res` 前後逐列相等；並行（多 goroutine 交錯三種 scope，`-race`）結果各自穩定**。

### T6 變異驗證與整合測試
- **scan 覆蓋不洗掉名字（codex #5）**：明列測試——先 `ConversationNames().Upsert(sid, name)`，再對同一 sid 做 `ConversationStore.UpsertBatch`（整列覆蓋，模擬 scan），斷言 `ConversationNames().All` 仍含該名字；另有一個走完整 `conversations.Scan` 的版本（Scan 寫入同 sid 後名字仍在）。
- 變異（寫進 PR 描述）：(a) 把名字改存進 `conversation_index` 欄位並讓 `UpsertBatch` 覆蓋 → 上面的測試翻紅；(b) `IsTestCwd` 去掉 Clean → `../` 案例翻紅；(c) scope 過濾移到 cap 之後 → total/truncated 測試翻紅；(d) 標題階層順序對調 → 順序測試翻紅；(e) 過濾改成就地修改 `snap.res` → 不可變測試翻紅；(f) 失敗時仍推進節流狀態 → 重試測試翻紅；(g) 拿掉 nameWriter 的鎖 → `-race` 並行測試翻紅。
- 全套：`go vet ./...`、`go test ./internal/... ./cmd/... -count=1`，並對 `internal/module/peers`、`internal/module/nex`、`internal/store`、`internal/conversations` 跑 `go test -race`。

## 2. 不做
- 不改 ended/gone 的現有 R-4-* 規則、不加新動作、不回填歷史名字、不收 `/var/folders`、不碰 SPA（PR-2）。
- 不改 `conversation_index` schema。

## 3. 風險與回滾
- 新表為 `IF NOT EXISTS`，舊 binary 回滾時表留著無害。
- `scope` 預設 `all`＝舊行為，無 PR-2 時零影響。
- 寫入在 peers inventory 路徑上：以注入 sink＋失敗只 log 隔離，不得讓 inventory 變慢（寫入頻率受 1 小時／改名門檻限制；單次 upsert 為單列）。
