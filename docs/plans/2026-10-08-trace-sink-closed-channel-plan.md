# hookTraceSink 關機時 `send on closed channel` — 計畫（#1189／#1767 修正 (4)；codex plan review B1–B5 已併入）

日期：2026-10-08　負責：purdex-37　審查：purdex-d3
關聯：#1189（同一堆疊，2026-09-08 起已見 6 次，2026-10-07 16:29 又一次）；#1767 順手發現。

## 1. 問題（已查證）

`internal/module/agent/trace.go`：

- `Enqueue` 先 `pending.Add(1)`，再對 `queue` 做 `select { case s.queue <- record: default: … }`；`Close` 在 `sync.Once` 內：`pending.Wait()` → `close(s.queue)` → `worker.Wait()`。`FlushForTest` 直接 `pending.Wait()`。
- 關機序列是 cancel → `StopModules` → HTTP `Shutdown` → `CloseModules`。agent 模組在 **`Stop`**（`module.go:362-370`）就 `traceSink.Close()`，而此時 HTTP server 仍在處理請求：窗口內進來的 hook 請求在 handler 尾端（`handler.go:647-661`）`trace.Finish` → `Enqueue`，對已關閉的 channel 送值——**Go 的 `select` send case 對 closed channel 也會 panic**（`default` 救不了）。net/http recover 後該請求失敗並留堆疊。
- 次要：`Close` 的 `pending.Wait()` 與窗口內 `Enqueue` 的 `pending.Add(1)` 併發（counter 為 0 時 Add 與 Wait 併發是 WaitGroup 的明確誤用）；`FlushForTest` 同理（B4）。
- 另一個 producer：probe-intent 的 `consumeSignals`／`emitStopObservability` 也會 `AppendProbeIntent`（走 `Enqueue`）；`stopAll` 只 cancel、不 join consumer，consumer 尾端仍可能在 `Stop` 之後寫 trace。既有測試 `probe_intent_dispatcher_screen_change_integration_test.go:67-73,96-107` 用「停用 ProcessDead＋50 ms sleep」迴避同一個 panic（#800）。

## 2. 修法（兩層：先治本——關得更晚；再防護——closed 時優雅丟棄）

### 2.1 治本（B1）：sink 改在 HTTP drain 之後才關
agent `Module` 實作 `core.Closer`：`Close() error { m.traceSink.Close(); return nil }`，並**從 `Stop` 移除** `traceSink.Close()`。依既有序列（`serveAndWait`：`StopModules` → `srv.Shutdown`（等進行中請求）→ 收 `Serve` → `CloseModules`），`Close` 在 HTTP 請求都 drain 之後才執行；因此一般關機下 hook 請求的 `Enqueue` 都發生在 sink 關閉之前，**trace 不會丟**。`Stop` 後 `Close` 前仍存活的 producer（`consumeSignals` 尾端、hook handler 收尾）照常入佇列，由 `Close` 的 `pending.Wait()` drain。

### 2.2 防護（保留，因為 `Shutdown` 有預算：超時會 `Close()` 強關連線，之後仍可能有殘留 handler／consumer）
`hookTraceSink` 加 `sync.RWMutex` 與 `closed bool`：
- `Enqueue`：`RLock`；`closed` ⇒ 解鎖、**丟棄**，return；否則在持有 `RLock` 時 `pending.Add(1)` 並做原有非阻塞送值，解鎖。
- `Close`（`sync.Once`）：`Lock` 設 `closed = true`、解鎖（此後沒有新的 `Add`／送值）；再 `pending.Wait()` → `close(queue)` → `worker.Wait()`。送值只發生在 `RLock` 內，`closed` 的設定需 `Lock`（等所有 `RLock` 持有者結束），所以 `close(queue)` 時不可能有送值併發；`pending.Add` 也不再與 `pending.Wait` 併發。
- `FlushForTest` 改為先 `RLock` 取得一致的檢查再 `pending.Wait()`（B4）：呼叫期間不得有新的 `Add` 與 `Wait` 併發——以 `RLock` 持有到 `Wait` 返回不可行（會阻塞 Enqueue），故改成「`Lock` 暫時擋住新 Add → `pending.Wait()` → 解鎖」，僅測試用；測試註解說明。
- **丟棄的可觀測性（B1）**：closed 丟棄不逐筆印 log；用 `atomic.Int64` 計數，並在**第一次**丟棄時記一行 `[agent][trace] sink closed: dropping trace records from now on`，`Close` 完成時若有丟棄再記一行 `dropped N record(s) after close`（一次性，不洗版）。trace 是盡力而為的觀測資料，關機窗口內的丟失可接受，已明載；**hook 事件本身的處理不受影響**（handler 不再 panic，照常回 200；以測試斷言）。
- 既有 `queue full` 丟棄路徑不變。

### 2.3 行為變化小結
一般關機：不再丟 trace（2.1）；`Shutdown` 超時的極端情況：殘留 producer 的 trace 被丟棄並計數（2.2），不 panic；hook 事件處理與回應不變。

## 3. 任務（TDD：先紅）

- **T1 重現（紅）**：`Close()` 之後 `Enqueue`（真 `store.TraceStore` 的臨時 DB）：現況 panic；期望不 panic、`FlushForTest` 立即返回、一次性日誌出現、丟棄計數＝1。
- **T2 確定性交錯（B2）**：在 `Enqueue` 的「closed 檢查之後、送值之前」放**測試鉤子**（`var enqueueAfterCheckHook func()`，非 nil 才呼叫，production 為 nil）。用鉤子讓 `Enqueue` 停在該點，同時呼叫 `Close()`，斷言 `Close` **阻塞直到 `Enqueue` 完成**（因 `RLock`），且完成後該筆紀錄已被寫入 DB、無 panic；不靠 scheduler 或 `-race`。對照變異（拿掉 `RLock`）→ 此測試 panic／紅。
- **T3 並行壓力（`-race`）**：多 goroutine 持續 `Enqueue` 同時 `Close()`；斷言無 panic、`Close` 返回、DB 內紀錄數＝成功入佇列數（`ListChains` 設足夠 `Limit` 或走 cursor，B5；預設只回 100）、丟棄計數＋寫入數＝送出數。
- **T4 真實關機 producer 回歸（B3）**：移除 `probe_intent_dispatcher_screen_change_integration_test.go:67-73,96-107` 的迴避（停用 ProcessDead＋50 ms sleep），改用**真實關機順序**（`StopModules` → HTTP `Shutdown` → `CloseModules`，或其測試等價物：`Module.Stop` 後讓 consumer 仍有尾端 trace，再 `Module.Close`），斷言不 panic、該 consumer 的尾端 trace 被寫入（2.1：Close 在 Stop 之後）。若該測試因故不能用真實序列，至少斷言「Stop 後 Close 前入列的紀錄會被寫入」。
- **T5 Module 級（2.1）**：`Module.Stop` 之後 `sink` 仍開著（`Enqueue` 成功入列、不被丟）；`Module.Close` 之後才 closed；`Close` 冪等（`sync.Once`）；`traceSink == nil` 安全。既有測試若依賴「`Stop` 就 flush 並關 sink」需改成 `Stop` + `Close`（列出受影響的測試並說明）。
- **T6 hook 請求在關閉窗口內不受影響**：先 `Close` sink，再對 `handleEvent` 送一個 hook 請求：回 200、狀態更新照常（currentStatus／frame 投影），僅 trace 被丟（計數 +1），不 panic。
- **T7 `FlushForTest`（B4）**：背景 goroutine 持續 `Enqueue` 的同時反覆 `FlushForTest`，`-race` 下無 WaitGroup 誤用警告。
- **變異驗證**：拿掉 `closed` 檢查；`Close` 的 `Lock` 換成不加鎖；`Enqueue` 不持 `RLock`；`Close` 仍在 `Stop` 內（不實作 2.1）→ T5／T4 紅。

## 4. 不做
- 不改關機序列（`serveAndWait`）；不把 listener 先關；不處理 hook 事件在關機窗口內其他可能的丟失（只修 panic 與 trace 丟失）。
- 不為 `consumeSignals` 加 join（`stopAll` 的語意不動；2.1 讓它的尾端 trace 在 `Close` 前入列即可）。

## 5. 驗收
- 單元／並行測試（`-race`）。部署後關機日誌不再出現該堆疊；極端情況（`Shutdown` 超時）才會有一行 `sink closed: dropping trace records` 與 `dropped N record(s) after close`（可 grep）。
- 回滾：單一 PR revert。

## 6. 規模
約 200–300 行（含測試）；一個 PR，改 `trace.go`、`module.go`（`Stop`／新增 `Close`）與測試；關閉 #1189。
