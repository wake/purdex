# Spec — Issue #61: handleCreate Concurrency Lock

## Problem

`internal/module/session/handler.go:51 handleCreate` 執行以下序列但**無任何鎖**：

1. `m.tmux.HasSession(req.Name)` — duplicate check
2. `m.tmux.NewSession(req.Name, req.Cwd)` — actual create
3. `m.tmux.ListSessions()` — look up tmux ID
4. `m.meta.SetMeta(s.ID, ...)` — persist metadata

兩個同名 POST 同時進來時，(1) 和 (2) 之間可能 interleave，導致：
- FakeExecutor: `sessions` map 被第二次 NewSession 覆蓋，但 `sessionOrder` 出現兩筆相同 name；`ListSessions` 會回傳重複 entry
- 真實 tmux: 第二次 `NewSession` 會被 tmux 拒絕，handler 回傳 500 而非預期的 409

## Scope

僅修改 `internal/module/session/`：
- `module.go` — 新增 `createMu sync.Mutex` 欄位
- `handler.go` — `handleCreate` 取 lock 包住 critical section
- `handler_test.go` — 新增並發創建回歸測試

**不**修改 rename/delete/switch-mode handler（它們操作既有 session by code，race 風險較小；若需要補鎖另立 issue）。

## Invariants

1. **互斥**：同一時間最多一個 goroutine 正在執行 `handleCreate` 的 critical section（從 HasSession 到 SetMeta）。
2. **No double-create**：對任何 `(name)` ，若 N 個並發請求同時帶該名字到 `/api/sessions`，最多 1 個回 201，其餘回 409，且底層 `ListSessions()` 回傳該 name 恰好一筆。
3. **No deadlock**：`createMu` 只在 `handleCreate` 內使用，不與任何其他鎖（`CfgMu`、watcher `mu`、meta store 內鎖、tmux executor 內鎖）有 nested 取得順序，因此不會產生循環等待。
4. **No new fields on SessionModule beyond one mutex**：避免擴大改動面。

## Approach

**Single global per-module mutex**（非 per-name）。理由：
- `handleCreate` 是極低頻操作（人手動建 session）
- 單一鎖簡單，明顯正確，沒有 sync.Map 加 keyed lock 的複雜度
- 若未來 create 頻率變高再升級為 per-name lock

**Critical section 範圍**：從 `HasSession` 檢查開始，到 `SetMeta` 完成結束。涵蓋整個 read-modify-write 序列。輸入驗證（JSON decode、name regex、mode validation）在 lock 外，避免惡意請求佔用鎖。

```go
// pseudo-code
if err := validateInput(...); err != nil { return 400 }

m.createMu.Lock()
defer m.createMu.Unlock()

if m.tmux.HasSession(req.Name) { return 409 }
if err := m.tmux.NewSession(...); err != nil { return 500 }
sessions, _ := m.tmux.ListSessions()
// find + SetMeta ...
```

Lock 持有時間：期望 ≤ 10ms（tmux shell-out 延遲主導），人類操作頻率 << 100Hz，無吞吐量問題。

**與 watcher 的關係**：`internal/module/session/watcher.go` 的 `watchSessions` goroutines 也會呼叫 `m.tmux.ListSessions()` 做 broadcast，但它們**不取 `createMu`**。watcher 看到的 session 列表可能與 `handleCreate` 的中間狀態交錯（例如 NewSession 已成功但 SetMeta 還沒跑），這是可接受的：watcher 會在下一輪再廣播一次正確狀態，不會造成資料損壞。

## Test Plan

新增 `TestHandlerCreateSessionConcurrentSameName`：

**Setup**：
- `newTestModule` 取得 mod + fake
- 啟動 **N = 50** 個 goroutine，每個發送相同 body `{"name":"dup","cwd":"/tmp"}`（FakeExecutor 的 `HasSession`/`NewSession` 各自取得短暫鎖，handler 層 race window 窄，取樣 N 需要夠大才能穩定觀察到 fix 前的失敗）
- 使用 `sync.WaitGroup` + close-on-start channel barrier 確保所有 goroutine 在同一瞬間進入 handler

**Assertions**：
- 所有 N 個回應中，`http.StatusCreated` 恰好 1 個
- 其餘 N-1 個為 `http.StatusConflict`
- 沒有其他狀態碼（把未預期狀態視為測試失敗）
- `mod.ListSessions()` 回傳長度 1（該 name 只存在一筆）
- FakeExecutor 的 `sessionOrder` 在 fix 後不會出現重複 name（間接由 ListSessions 長度驗證）

**期望：**
- Fix 前：測試失敗（`ListSessions` 長度 > 1 或 successes > 1，或 503 非預期狀態）
- Fix 後：測試穩定通過

**Race detector**：CI / local 應跑 `go test -race`。即使 FakeExecutor 內部有鎖（不會 fire race），handler 層外部的重複 append 仍會被邏輯斷言抓到。

## Edge Cases

- **不同名稱並發**：N 個 POST 帶不同 name，不應互相阻塞超過必要時間。因為 critical section 短，單鎖不會成為 bottleneck。未測試（out of scope）。
- **Lock release on panic**：用 `defer m.createMu.Unlock()` 確保 panic 也釋放。
- **meta.SetMeta 失敗**：原邏輯直接回 500，留下已建的 tmux session 與未寫入的 meta。Lock 不解決此「失敗清理」問題，out of scope（另立 issue 追蹤）。

## Out of Scope

- rename/delete/switch-mode 的並發保護
- meta.SetMeta 失敗時的 tmux rollback
- Per-name fine-grained lock（N=1 global lock 足夠）
- 把 `HasSession + NewSession` 降到 tmux 原生 atomic API（tmux 本身無此保證）
