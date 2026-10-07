# peers 啟動掃描：殭屍 helper 回收（#1767 修正 (1)）— 計畫（含迷你 spec）

日期：2026-10-08　負責：purdex-37　審查：purdex-d3（codex plan review 4 條已併入）
背景與量測：issue #1767（重啟 5 s → 16 s → 28 s；其中 8 s 是兩個殭屍 helper 讓 peers 啟動掃描每個空等約 4 s）。

## 1. 問題（已查證）

- `helperManager.Sweep`（`internal/module/peers/sweep.go`）在 `Start` 時對 `proxies.json` 每筆紀錄：`ClassifyProc` 判為 `ProcSame` → SIGTERM → `waitGone`（最多 `termGrace`）→ 仍在則 SIGKILL → 再 `waitGone`；仍判為「活著且是我們的」就保留紀錄、不清檔案。
- 實測（#1767）：兩筆紀錄的 pid（56212、58129）都是 **defunct 的僵屍，父行程就是 daemon 自己**（`pdx serve`，exec 重啟保留 pid）。僵屍對 `kill(pid, 0)` 仍成功，`pidAlive` 因此認為活著；SIGTERM／SIGKILL 對僵屍無效；`ps lstart` 仍回得出啟動時間，身分判為 `ProcSame`。結果每次重啟都付 `2 × termGrace`，紀錄永遠保留。
- 僵屍是 daemon 自己的子行程：只有 daemon 對它 `wait4` 才會消失。啟動端（`proxyhelper.ExecStarter` → `execProc.Wait`、`handle.Stop`）正常路徑本來就會 Wait；殭屍出現在 **exec 重啟邊界**（舊 image 的 Wait goroutine 隨 exec 消失）與 crash 遺留。

## 2. 修法（review 後版本）

新增三個可注入縫隙，與一個保守的回收條件：

- `procState func(pid int) (state string, ppid int, err error)`：預設 `ps -o stat=,ppid= -p <pid>`（與既有 `ccuds.DefaultProcStart` 同一套 `ps` 做法；`state` 取第一個欄位）。
- `reap func(pid int) bool`：預設 `syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)`，**只有回傳 `r == pid && err == nil` 才算成功**（`r == 0`＝仍在跑的直系子行程、`ECHILD`＝不是子行程，皆 false）。
- `zombieSettle time.Duration`：預設 200 ms（測試縮短）。
- `ownPID int`：預設 `os.Getpid()`。

**回收條件（`m.tryReapZombie(r)`，全部成立才 `reap`）**：
1. 身分仍為 `ProcSame`（沿用既有判斷，不放寬）；
2. `procState` 的狀態以 `Z` 開頭（確為殭屍）**且** `ppid == ownPID`（是本 daemon 的子行程）；
3. 等 `zombieSettle` 後 **複查** 仍是 `Z` 且 ppid 不變。仍是 Z 代表此刻沒有任何 waiter 在收（若是有 `Cmd.Wait` 在等的子行程，它在毫秒內就會把它收掉，狀態不會停在 Z 超過 200 ms）。

任何一項不成立 → **完全退回原本的 SIGTERM → grace → SIGKILL 路徑**，行為與現狀相同。成功 → 視為已死，不送訊號、不等待，直接走既有的「已死」路徑（`unlinkOwned`），並記一行日誌。

- 呼叫點：`sweepRecord` 的 `ProcSame` 分支、**送 SIGTERM 之前**；以及 `waitGone` 的每輪輪詢（SIGTERM 之後才變殭屍的情形；為避免每 10 ms 都 fork 一次 `ps`，只在 `waitGone` 偵測到「alive 且 ProcSame」的第一輪與之後每 `zombieSettle` 才檢查一次）。
- 不改 `ClassifyProc`、不改訊號次序、不改「無法證明已死就保留紀錄」的保守規則。

### 安全性論證與剩餘窗口
- **為什麼不盲目 wait4**：`ProcSame` 只證明 pid 與紀錄的啟動時間相符，不證明 daemon 自己沒有 `Cmd.Wait` 在等那個 pid（例如 stale 紀錄剛好指到 session module 的 tmux `wait-for` 等子行程）；`wait4` 搶走退出狀態會讓原 Wait 得到 `ECHILD`。所以前置條件改成「狀態確為 Z、ppid 是本 daemon、200 ms 後仍是 Z」。
- **剩餘窗口**：一個子行程恰好在複查那一刻之前 ≥200 ms 內都處於 Z，且 daemon 內某個 `Cmd.Wait` 之後才要收它——正常的 `Cmd.Wait` goroutine 在子行程退出時就已在 `wait4` 阻塞，不會讓它停在 Z 這麼久；只有「已經沒有 waiter」才會如此，這正是我們要回收的對象。極端的 Wait goroutine 被長時間排程餓死（>200 ms）時理論上可能被搶，其後果是該 Wait 回 `ECHILD`（`os/exec` 視為一般錯誤、不會 panic），且該行程本來就已退出；可接受並在此明載。
- **為什麼本 image 內不會有該 pid 的 `Cmd.Wait`（對「200 ms 不是所有權證明」的補強，codex R2）**：`Sweep` 只處理 `proxies.json` 的**前一個 image 寫下的紀錄**，而回收前提是該 pid 的行程啟動時間（秒級 `lstart`）與紀錄相符（`ProcSame`，且在 settle 後再驗一次）。本 image 內任何 `Cmd.Start` 出來的子行程都晚於本 image 啟動，其 pid 要等於紀錄的 pid 又同一秒啟動，必須在不到一秒內讓 pid 空間繞一圈，實務上不可能；所以對 `ProcSame` 的 pid，本 image 沒有 waiter。200 ms 複查是對「這個推論失靈」的第二道保險，不是唯一依據；排程餓死 Wait goroutine 而被搶的情形因此需要同時滿足一個本來就不成立的前提。
- settle 之後、`wait4` 之前**再驗一次身分**（`identity` 仍為 `ProcSame`）：避免原行程被收走、pid 回收給另一個也呈 Z 的子行程（codex R2 A1）。
- `reap` 失敗（任何 errno）一律當 false，等同現狀。

### 單次執行（review #3）
- 「只跑一次」目前不是 `Sweep` 自身的 invariant：依賴 `runServe` 同步呼叫 `Start`、且在 HTTP listen 前完成。本 PR 加 **claim-before-scan，且並行呼叫等待並共用同一次掃描的結果**（codex R2 A3）：進入 `Sweep` 時在鎖內，已 `swept` ⇒ nil；已有進行中的掃描（`sweepDone` 非 nil）⇒ 等它結束並回傳**它的結果（失敗也一樣）**；否則自己 claim。掃描結束（成功或失敗）都釋放 claim（維持「Sweep 失敗後可重試」，`TestModuleStart_SweepErrorReturned`）。因此第二個呼叫既不會重跑掃描，也不會把未完成或失敗的掃描當成功。
- plan 明記：正常路徑仍是 `Start` 同步呼叫一次；claim 只是把這個前提變成程式保證。

## 3. 任務（TDD：先紅）

### Z1 sweep 行為（單元，fake 縫隙）
- (a) 殭屍（`procState` 回 `Z`、ppid＝ownPID、複查仍 Z、`reap` 成功後 `alive` 轉 false）→ **未送任何訊號**、紀錄清除、檔案依既有 `unlinkOwned` 規則；(b) SIGTERM 之後才變 Z（`waitGone` 路徑）→ 只送 SIGTERM、不送 SIGKILL、不等滿 grace；(c) `reap` 永遠 false 且行程不死 ⇒ 與現狀一致（`TestSweep_IgnoresSignalsRetained` 不動、仍綠）；(d) `ProcDifferent`／`ProcUnknown` 時不呼叫 `procState`／`reap`；(e) 狀態非 Z、或 ppid ≠ ownPID、或複查時已不是 Z／ppid 改變 ⇒ 不呼叫 `reap`，走原 SIGTERM 路徑（四個子案例，每個一個測試）。
- 測試 harness（`newTestManager` 的 `tm.os`）加 `procState`／`reap` 假件，預設「非 Z」／false，既有測試不動。

### Z2 真 syscall 分支測試（review #2、#4）
- **真殭屍**：測試行程 `exec` `sh -c 'exit 0'`，**不 Wait**；以有期限的 polling（`waitFor`，參考 `internal/agent/process_snapshot_test.go:238` 的既有模式）等到 `ps` 狀態以 `Z` 開頭；`t.Cleanup` 兜底 `cmd.Wait()`。以真實 `pidAlive`／`procStart`／`procState`／`reap`／`signal` 建 manager，寫該 pid 的 `proxies.json` 紀錄（真 `procStart`），`Sweep`：不以耗時為主要斷言，而是斷言 **未送 SIGTERM/SIGKILL**（用包一層記錄的 `signal`）、`kill(pid,0)` 之後回 ESRCH（殭屍已被收）、紀錄已清。
- **`defaultReap` 的三個真 syscall 分支**：(i) 仍在跑的直系子行程（`sleep 30`，`t.Cleanup` kill＋Wait）→ `reap` 回 false 且該行程仍在；(ii) 非子行程（例如 `os.Getppid()` 或 pid 1）→ `ECHILD` → false；(iii) 已退出的直系子行程（殭屍）→ true。
- **「同 pid 有 `Cmd.Wait` 在等」不被搶**（Z 複查機制）：啟動一個子行程並讓一個 goroutine `cmd.Wait()`；子行程退出的瞬間，Wait 會收走它，`procState` 的複查看不到持續的 Z → `tryReapZombie` 回 false，且 `cmd.Wait()` 回傳 nil（不是 `ECHILD`）。為了讓時序確定，以 `procState` 假件包真實 `ps`：第一次呼叫回 `Z`（模擬剛退出）、複查前讓 Wait goroutine 完成，複查回「查無此行程」。斷言 `reap` 從未被呼叫。
- **claim-before-scan**：兩個 goroutine 同時 `Sweep`，第二個在第一個進行中進入 ⇒ 第二個**等待**第一個完成並取得同一個結果（含失敗）、`proxies.json` 只被處理一次（以計數的 `identity` 假件鎖定）；第一個失敗後可重試（沿用既有測試）；結果存在 per-flight 結構（`sweepFlight`），不會被下一輪重試覆寫，並有多 caller 交錯的壓力測試。

### Z3 日誌
- 回收成功時記：`peers: sweep: reaped zombie pid %d (identity same)`；部署後重啟日誌可直接驗收。

## 4. 不做（含 d3 要求的「helper 啟動端 Wait」之評估）
- 啟動端不改：`execProc.Wait`／`Stop` 已 Wait；殭屍來自 exec 邊界與 crash 遺留，由新 image 的 sweep 一次回收，比在啟動端加第二個 Wait goroutine（exec 時同樣消失）更直接。若仍要啟動端保險，另開 PR，且需先定義「關機時等 helper 結束的上限」（會拉長關機）。
- 不處理 #1767 的 5 s 基線與負載造成的變動（那是 (3) 的耗時日誌要量的）。

## 5. 驗收
- 合併部署後重啟一次：日誌出現 `reaped zombie pid 56212` 與 `58129`，不再有 `survived SIGTERM/SIGKILL`；`ps` 看不到那兩個 defunct；重啟少 8 s；`proxies.json` 不再含這兩筆。
- 回滾：單一 PR revert；紀錄格式不變。

## 6. 規模
約 200 行（含測試）；一個 PR，改 `sweep.go`、`helpers.go`（config 欄位與預設）與測試。
