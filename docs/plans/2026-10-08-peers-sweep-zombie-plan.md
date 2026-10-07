# peers 啟動掃描：殭屍 helper 回收（#1767 修正 (1)）— 計畫（含迷你 spec）

日期：2026-10-08　負責：purdex-37　審查：purdex-d3
背景與量測：issue #1767（重啟 5 s → 16 s → 28 s；其中 8 s 是兩個殭屍 helper 讓 peers 啟動掃描每個空等約 4 s）。

## 1. 問題（已查證）

- `helperManager.Sweep`（`internal/module/peers/sweep.go`）在 `Start` 時對 `proxies.json` 每筆紀錄：`ClassifyProc` 判為 `ProcSame` → SIGTERM → `waitGone`（最多 `termGrace`）→ 仍在則 SIGKILL → 再 `waitGone`；仍判為「活著且是我們的」就保留紀錄、不清檔案。
- 實測（#1767）：兩筆紀錄的 pid（56212、58129）都是 **defunct 的僵屍，父行程就是 daemon 自己**（`pdx serve`，exec 重啟保留 pid）。僵屍對 `kill(pid, 0)` 仍回成功，所以 `pidAlive` 認為它活著；SIGTERM／SIGKILL 對僵屍無效；`ps lstart` 仍回得出啟動時間，所以身分判為 `ProcSame`。結果每次重啟都付 `2 × termGrace`（約 8 s／兩筆），紀錄永遠保留。
- 僵屍是 daemon 自己的子行程：只有 daemon 對它 `wait4` 才會消失。啟動端（`proxyhelper.ExecStarter` → `execProc.Wait`、`handle.Stop`）正常路徑本來就會 Wait；殭屍出現在 **exec 重啟邊界**：舊 image 的 Wait goroutine 隨 exec 消失，之後才死的 helper 沒人收。所以修在「新 image 的 sweep」最通用（也涵蓋 crash 後遺留的情況）；啟動端不需要改（見 §4）。

## 2. 修法

新增可注入的 `reap` 縫隙，Sweep 在**已證明身分為 `ProcSame` 的 pid** 上嘗試回收：

- `helperManagerConfig.Reap func(pid int) bool`；`nil` 用 `defaultReap`：`syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)`，**回傳 `r == pid && err == nil` 才算回收成功**（`ECHILD`＝不是我們的子行程、`r == 0`＝還活著且是我們的子行程，兩者皆 false）。
- `sweepRecord` 的 `ProcSame` 分支，**在送 SIGTERM 之前**先 `m.reap(pid)`：成功 ⇒ 該行程已死（是殭屍）⇒ 不送任何訊號、不等待，直接走「已死」路徑（`unlinkOwned`）。
- `waitGone` 的輪詢迴圈每次先 `m.reap(pid)`（再做原本的 `identity` 檢查）：涵蓋「SIGTERM 之後才變殭屍」的情形（子行程死了但沒人 Wait），不必等滿 `termGrace`。
- 不改 `ClassifyProc`、不改 signal 次序、不改「無法證明已死就保留紀錄」的保守規則；`reap` 只會讓「證明已死」更早成立。

### 安全性論證（為什麼不會誤收別人的子行程）
- 只在 `ProcSame`（pid 與紀錄的 `proc_start` 相符）之後才 reap；pid 在殭屍期間不會被重用，所以 `wait4(pid)` 收到的就是紀錄那個 helper。
- `Sweep` 在 `Start`、HTTP server 開始接受之前執行，且只執行一次（`swept`），此時 daemon 自己的 helper 還沒啟動；對非本 daemon 子行程 `wait4` 回 `ECHILD`，無副作用。
- `reap` 失敗（任何 errno）一律當 false，等同現狀。

## 3. 任務（TDD：先紅）

### Z1 `reap` 縫隙與 sweep 行為（單元，fake）
- 測試：(a) 殭屍（`alive` 為真、`reap` 成功後轉為 dead）→ **未送任何訊號**、Sweep 耗時遠小於 `termGrace`、紀錄清除、檔案依既有 `unlinkOwned` 規則處理；(b) SIGTERM 後才變殭屍（reap 在 `waitGone` 輪詢中成功）→ 只送 SIGTERM、不送 SIGKILL、不等滿 grace；(c) `reap` 永遠 false 且行程不死 ⇒ 與現狀一致（`TestSweep_IgnoresSignalsRetained` 不動、仍綠）；(d) `ProcDifferent`／`ProcUnknown` 時不呼叫 `reap`。
- 測試 harness（`newTestManager` 的 `tm.os`）加 `reap` 假件，預設 false。

### Z2 真實殭屍的整合測試
- 在測試行程裡 `exec` 一個立刻結束的子行程（`sh -c 'exit 0'`），**不 Wait**（製造真的殭屍，父＝測試行程），確認 `kill(pid,0)` 仍成功、`ps` 狀態為 Z；以真實的 `pidAlive`／`procStart`／`reap`／`signal` 建 `helperManager`，寫入該 pid 的 `proxies.json` 紀錄（用真的 `procStart`），呼叫 `Sweep`：耗時 < 1 s、`kill(pid,0)` 之後回 ESRCH（殭屍被收掉）、紀錄被清。變異：把 `reap` 換成永遠 false → 測試因等滿 grace 而紅（用較短的 `TermGrace` 讓失敗也快）。

### Z3 日誌
- 回收成功時記一行：`peers: sweep: reaped zombie pid %d (identity same)`，之後重啟日誌可直接看到清除（也是部署後的驗收依據）。

## 4. 不做（含 d3 要求的「helper 啟動端 Wait」之評估）
- 啟動端不改：`execProc.Wait`／`Stop` 已 Wait；殭屍來自 exec 邊界與 crash 遺留，由新 image 的 sweep 一次回收，比在啟動端加第二個 Wait goroutine（exec 時仍會消失）更直接。若 d3 仍要啟動端加保險，我另開一個 PR，但需要先定義「關機時等 helper 結束的上限」，那會拉長關機，不在本 PR。
- 不處理 #1767 的 5 s 基線與負載造成的變動（那是 (3) 的耗時日誌要量的）。

## 5. 驗收
- 合併部署後重啟一次：日誌應出現 `reaped zombie pid 56212` 與 `58129`、不再有 `survived SIGTERM/SIGKILL`，`ps` 看不到那兩個 defunct，重啟耗時少 8 s；`proxies.json` 不再含這兩筆。
- 回滾：單一 PR revert；紀錄格式不變。

## 6. 規模
約 120 行（含測試）；一個 PR，改 `sweep.go`、`helpers.go`（config 欄位與預設）與測試。
