# hookTraceSink 關機時 `send on closed channel` — 計畫（#1189／#1767 修正 (4)）

日期：2026-10-08　負責：purdex-37　審查：purdex-d3
關聯：#1189（同一堆疊，2026-09-08 起已見 6 次，2026-10-07 16:29 又一次）；#1767 順手發現。

## 1. 問題（已查證）

`internal/module/agent/trace.go`：

- `Enqueue` 先 `pending.Add(1)`，再對 `queue` 做 `select { case s.queue <- record: default: … }`。
- `Close` 在 `sync.Once` 內：`pending.Wait()` → `close(s.queue)` → `worker.Wait()`。
- `Close` 由模組 `Stop`／`Close` 呼叫（關機序列：cancel → StopModules → HTTP `Shutdown` → CloseModules），而 HTTP server 在 `StopModules` 到 `Shutdown` 期間仍在接收 `POST /api/agent/event`。這段窗口內進來的 hook 請求走到 `hookTraceCollector.Finish`（trace.go:423）→ `Enqueue`，對已關閉的 channel 送值：**Go 的 `select` 的 send case 對 closed channel 也會 panic（default 分支救不了）**。net/http recover 後該請求失敗、該 hook 事件的 trace 與整個請求處理被中斷，日誌出現堆疊。
- 另有次要的 WaitGroup 誤用風險：關閉流程的 `pending.Wait()` 與窗口內另一個 `Enqueue` 的 `pending.Add(1)` 併發（counter 為 0 時 `Add` 與 `Wait` 併發是明確的誤用）。

## 2. 修法

在 sink 上加一把 `sync.RWMutex` 與 `closed bool`：

- `Enqueue`：`RLock`；若 `closed` → 解鎖、**丟棄並記一行** `[agent][trace] drop chain %s: sink closed`（與既有 `queue full` 的丟棄風格一致），return；否則在持有 `RLock` 時 `pending.Add(1)` 並做原有的非阻塞送值，解鎖。
- `Close`：`Lock` 設 `closed = true`、解鎖（此後沒有新的 `Add`／送值）；再 `pending.Wait()` → `close(queue)` → `worker.Wait()`。因為送值只發生在 `RLock` 內，而 `closed` 的設定要 `Lock`（等所有持有 `RLock` 的送值結束），所以 `close(queue)` 時不可能有送值併發；`pending.Add` 也不再與 `pending.Wait` 併發。
- `AppendProbeIntent` 等其他入口都經由 `Enqueue`（已檢查：`rg -n "queue <-|\.Enqueue\(" internal/module/agent`），不需另改；實作時再確認沒有直接寫 `queue` 的第二個地方。
- 行為變化：只有關機窗口內的 trace 紀錄被丟棄（原本是 panic＋請求失敗）；hook 請求本身照常處理完成。不改任何持久化內容或 API。

## 3. 任務（TDD：先紅）

- **T1 重現（紅）**：`Close()` 之後呼叫 `Enqueue`（`newHookTraceSink` 配真的 `store.TraceStore` 的臨時 DB）：現況 panic；期望不 panic、回呼後 `pending` 計數歸零（`FlushForTest` 立即返回）、日誌含 `sink closed`。
- **T2 並行（紅→綠，`-race`）**：多個 goroutine 持續 `Enqueue`，同時呼叫 `Close()`；斷言無 panic、`Close` 能返回、關閉前成功入佇列的紀錄都有被寫入 DB（`ListChains` 數量 = 成功送值數，用 `Enqueue` 之前的計數與 drop 計數對帳：實作需讓測試能取得 drop 數——以日誌收集或一個測試用的計數欄位，擇小者）。
- **T3 既有行為**：`queue full` 丟棄路徑、`Close` 的冪等（`sync.Once`）、nil sink 安全，既有測試不動、全綠。
- **變異驗證**（暫時破壞，確認 T1／T2 翻紅）：拿掉 `closed` 檢查；把 `Close` 的 `Lock` 換成不加鎖；`Enqueue` 不持 `RLock` 就送值。

## 4. 不做
- 不改關機順序（不把 listener 先關）：該序列有 `serveAndWait` 的既有測試與語意（#1569 剛動過），改它風險遠大於在 sink 端防護；issue 列的兩個方向取「sink 端」。
- 不處理 hook 事件在關機窗口內其他可能的丟失（只修 panic）。

## 5. 驗收
- 單元／並行測試（`-race`）。部署後日誌不再出現該堆疊；關機窗口內的 trace 丟棄會留 `drop chain … sink closed` 一行（可 grep）。
- 回滾：單一 PR revert。

## 6. 規模
約 100 行（含測試）；一個 PR，改 `trace.go` 與測試。關閉 #1189。
