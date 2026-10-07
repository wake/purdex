# 關機耗時日誌＋exec 計數器 — 計畫（#1767 修正 (5)(6)）

日期：2026-10-08　負責：purdex-37　審查：purdex-d3（已同意範圍，純觀測、不派 codex；我自己 merge plan 後動工）
背景：#1767。alpha.562 重啟實測：新 image `startup: ready` 4.1 s，但「重啟請求→回來」12.5 s，表示**舊 image 關機約 8 s**（18:34:43 restart requested → 18:34:51 exec），日誌沒有關機分階段行，無法歸因；`[agent] start:` 剩 sweepOnce 1.8 s／replayStatus 1.0 s，也不知道是不是 fork。目標：下次重啟直接從日誌讀出時間花在哪。**純觀測，不改任何行為、順序、timeout。**一份 plan、一個 PR、兩個獨立 commit。

## 1. (5) 關機耗時日誌

### 1.1 core：StopModules／CloseModules 每模組耗時彙總（與啟動那組對稱）
- `core.StopModules` 與 `CloseModules` 量每個模組的 wall time（monotonic），寫一行彙總：`shutdown: stop 14 modules in 7120ms: session=6900ms agent=120ms …`、`shutdown: close 14 modules in 31ms: …`；格式沿用 `formatModuleTimings`（降序、< 5 ms 併 `others`），單一模組 ≥ 1 s 另記 `shutdown: slow module stop: session took 6900ms`。錯誤處理與回傳值不變（`errors.Join` 照舊）。重用 `startup_timing.go` 的純函式與 `Core.now`／`Core.logf` 注入。

### 1.2 serveAndWait：各階段耗時
- `cmd/pdx/shutdown.go` 的 `serveAndWait` 在序列結束時記一行：`shutdown: stop-modules=Xms http-shutdown=Yms serve-return=Zms close-modules=Wms total=Tms` 加上 `http-forced-close` 標記（`srv.Shutdown(ctx)` 回錯、改走 `srv.Close()` 時）。`restart` 路徑在 `exec` 之前由呼叫端再記 `shutdown: done, restarting after Tms`（`runServe` 內已知 `restartRequested` 處；只加一行）。
- 量時用 `time.Now()`／`time.Since`（monotonic）；不改 `target` 介面（`shutdownTarget` 的 `StopModules`／`CloseModules`），計時包在 `serveAndWait` 內部，既有測試與 fake 不動。

### 1.3 進行中請求的可見度（回答「HTTP drain 在等什麼」）
- 外層 handler（`newOuterHandler`）加一個極輕的 in-flight 追蹤：`inflightTracker`（`mu` 保護 `map[string]int`，key＝`METHOD + " " + 路徑前兩段`，**不含 query、不含 token、不含完整路徑參數**），請求進入 `Add`、返回 `Done`。只在關機時才讀。
- `serveAndWait` 在開始 `srv.Shutdown` 前記一行：`shutdown: in-flight requests: 3 [GET /api/events 1, GET /ws/terminal 2]`（最多列 10 個 key），`Shutdown` 返回後再記剩幾條。hijacked 連線（WS）不在 `net/http` 的追蹤內，這個計數器看得到的是進入 handler 之後尚未返回者，**包含**升級後仍在跑的 WS handler（handler 未返回），所以可看到終端 WS 是否是那 3 s 的來源。
- 成本：每個請求一次 mutex 加鎖兩次（進、出）；hook 事件頻率為每秒個位數到數十，可忽略。若實測有熱路徑疑慮，改為分片或 `atomic` 計數（只對已知長連線前綴）——先照簡單版做。

## 2. (6) exec 計數器（tmux／ps fork 的次數與總耗時）

- 新 package `internal/execstat`（無依賴）：`var Tmux, PS Counter`；`Counter{n, ns atomic.Int64}`；`func (c *Counter) Observe(d time.Duration)`（兩次 `Add`，**原子、無鎖**）；`Snapshot() (n int64, d time.Duration)`；`Reset()` 僅測試用。不記指令參數、不記輸出。
- 掛點：
  - **ps**：`internal/agent/process_info.go:18`（單一收斂點，`psOutput`）與 `internal/peers/ccuds/virtual_peer.go:76`（`DefaultProcStart`）。各包一層計時。
  - **tmux**：`internal/tmux/executor.go` 有約 28 個 `exec.Command("tmux", …)` 呼叫點。做法：加一個小 helper `tmuxOutput(args ...string) ([]byte, error)`／`tmuxRun(...)`，計時並 `execstat.Tmux.Observe`，把**機械式**的呼叫點改用它；若某些呼叫點的 `Stdin`／`Env`／`Context` 使用方式不適合 helper，維持原樣（回報哪些沒納入）。允許縮範圍：至少涵蓋 agent 啟動路徑用到的（`PaneSessionName`、`ListPanes`／`ListAllPanes`、`ListSessions` 系列、`display-message`）。**不為埋點重構**。
- 輸出：`agent.Start` 在既有 `[agent] start:` 那行之後再記一行 `[agent] start exec: tmux=N(Xms) ps=M(Yms)`（以 `Start` 開始與結束的 snapshot 差值，只含 Start 期間）；`runServe` 在 `startup: ready` 行加上整個啟動期間的累計 `exec: tmux=N(Xms) ps=M(Yms)`（同一行尾端，不另起行）。
- 熱路徑成本：每次 fork 兩個 `atomic.Add` 加兩次 `time.Now`，相對 fork 本身（毫秒級）可忽略；無鎖、無配置；**日誌不含指令參數**。

## 3. 任務（TDD：先紅）

### Commit A：(5) 關機耗時
- A1 core：假時鐘＋假模組，`StopModules`／`CloseModules` 各記一行彙總（排序、`others`、slow 警示、某模組回錯時照記且回傳的 error 不變）。
- A2 `serveAndWait`：假 target／假 server（既有 harness `newHarness`），斷言序列結尾有一行 `shutdown: stop-modules=… http-shutdown=… close-modules=… total=…`；`Shutdown` 回錯時含 `http-forced-close`；既有所有 `serveAndWait` 測試不動、全綠。
- A3 inflight：`inflightTracker` 單元測試（Add/Done 對稱、key 只含方法與前兩段路徑、query 與 token 不出現、上限 10 個 key）；在 `newOuterHandler` 套用後，並行請求下 `-race` 無警告；`serveAndWait` 的「in-flight requests」行（以 fake tracker 注入）。
- 變異驗證：排序反向、`http-forced-close` 標記拿掉、tracker 的 `Done` 漏呼叫（計數洩漏）→ 各自對應測試紅。

### Commit B：(6) exec 計數器
- B1 `execstat`：`Observe`／`Snapshot`／`Reset` 單元測試，並行 `-race`。
- B2 ps 掛點：以注入的假 ps 命令（或實際 `ps -p $$`）執行，斷言 `PS` 計數加一、耗時 > 0；錯誤路徑也計（耗時照記）。
- B3 tmux 掛點：以現有 `tmux` fake 不適用（fake 不 fork）——改為對 helper `tmuxOutput` 以一個「存在的無害命令」（例如把 binary 名稱經由測試 seam 指到 `true`／`sh -c`）驗證計數；執行路徑列表中凡納入 helper 的方法，各有一個對 fake binary 的呼叫測試。
- B4 輸出行：`agent.Start` 結尾的 `[agent] start exec:` 行（假時鐘與假計數器）；`startup: ready` 行格式含 `exec: tmux=… ps=…`。
- 變異驗證：helper 漏呼叫 `Observe`、`Start` 的 snapshot 差值取錯（取絕對值）→ 對應測試紅。

## 4. 不做
- 不改任何 timeout、順序、`ShutdownBudget`；不改行為；不對 hook 熱路徑額外做事；不記指令參數或輸出；不新增 endpoint／設定。
- 不解釋「為什麼慢」——本 PR 只產生證據；根因修復另開。

## 5. 驗收
- 單元／`-race`；部署後重啟（用 562 當舊 image）日誌應出現 `shutdown: stop N modules in …`、`shutdown: in-flight requests …`、`shutdown: stop-modules=… http-shutdown=…`、`[agent] start exec: tmux=… ps=…`。貼回 #1767 定位 8 s 與 sweepOnce／replayStatus 的 fork。
- 回滾：單一 PR revert，兩個 commit 可各自 revert。

## 6. 規模
約 350–550 行（含測試），兩個 commit；超過 800 行／20 檔就拆成兩個 PR（(5)、(6)）。
