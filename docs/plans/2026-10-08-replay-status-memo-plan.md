# replayStatus 一輪內只算一次投影與 pane→session（#1767 修正 A）— 計畫（codex plan review 7 條已併入）

日期：2026-10-08　負責：purdex-37　審查：purdex-d3
背景：#1767（alpha.559 的耗時日誌：`agent` 模組 Start 6391 ms，其中 `replayStatus` 5618 ms，其餘模組都 < 25 ms）。熱路徑的快取設計另開 #1777（B），本 PR 不做。

## 1. 問題（已查證；成本為由程式與數字推論，T0 加計數驗證）

`probeIntentDispatcher.replayStatus`（`probe_intent_dispatcher.go:887`）：

1. `snapshotStatuses()`（`module.go:706`，持 `m.mu`）對 `currentStatus` 的**每個 session** 呼叫 `projectionForSession`；
2. 對每個 session `applyStatus` → `applyIntentLifecycle`；宣告了 probe intent 的 provider（codex）會再呼叫 `lookupTopFrameForSessionLocked` → 又一次 `projectionForSession`。

`projectionForSession`（`frame_ops.go:1420`）每次都：`liveFrameProjections()`（`frames.ListAll` + `filterProjectionFrames` + `BuildSessionProjections`），再 `selectSessionProjection`（`frame_ops.go:1444`）對**每個** projection 呼叫 `resolvePaneSession`（`module.go:665`）＝ `tmux.PaneSessionName`（一次 `exec tmux display-message`）**加上 `resolveSessionCode(name)`**（`handler.go:799`）。`resolveSessionCode` 走 session 模組的 `LookupCodeByName`（**TTL 250 ms**；冷快取時先做一次 `ListSessions` 建立完整名稱映射，合法名稱通常 hit，只有真 miss 才走完整 fallback；呼叫本身有刷新 name cache、orphan cleanup 與失敗日誌的副作用）。`selectSessionProjection` 把回傳的 code 丟掉（`name, _ :=`），所以那份成本白付。

mlab 實測：19 frame／17 pane、單次 tmux 呼叫 5–20 ms，`replayStatus` 5.6 s，吻合 S×P（sessions × live projection）。

## 2. 修法

### 2.1 replay 專用快取（只動 replay 這一輪；以參數傳遞，不放 Module 欄位）
```go
type replayProjectionCache struct {
    projections []SessionProjection // liveFrameProjections() 成功一次後才記
    loaded      bool
    paneName    map[string]string   // paneID -> tmux session name，僅記「成功解析」的
}
```
- `m.projectionForSessionWith(session, rc)`：`rc == nil` ⇒ 原本的 `projectionForSession`（所有既有呼叫點傳 nil，零行為差異）。`rc != nil`：第一次用時 `liveFrameProjections()`；**失敗不快取、不 poison 整輪**（錯誤照現況回給呼叫者，下一次呼叫重試）；pane→session 只在 `PaneSessionName` **成功**時記入 `paneName`；失敗不記（不快取成空名，下一次重試，同現況）。`selectSessionProjection` 抽出 resolver 版 `selectSessionProjectionBy(session, projections, nameOf)`，原版以 `resolvePaneSession` 的名稱部分包成 resolver。
- `snapshotStatuses(rc)`、`lookupTopFrameForSessionLocked(session, rc)`、`applyStatusWith(session, agentType, status, rc)`（`applyStatus` ＝ `applyStatusWith(..., nil)`）、`applyIntentLifecycle(..., rc)` 多一個參數；`replayStatus()` 建 `rc` 並於返回後丟棄（區域變數，不跨輪、不跨 goroutine）。

### 2.2 一致性風險與處置（A1／A3／A4 的結論）
- **A1（會武裝已被 sweep 刪除 row 的 frame）**：sweep 的 `clearFrame` 先刪 DB row、之後才取 `m.mu` 同步 `currentStatus`；舊路徑每次重讀 DB，會 frame-miss 而不武裝。新路徑若用快取的 top frame 武裝，可能武裝一個 row 已不存在的 frame（PID 重用時 ProcessDead detector 還可能永久輪詢）。**處置**：`applyIntentLifecycle` 在 `m.mu` 內、**武裝之前**，若 `rc != nil` 且要武裝，對「要武裝的那個 top frame」以 `frames.GetByIdentity(paneID, pid, processStartTime)`（單筆 DB 查詢，非 tmux）重讀一次；row 不存在（或身分不符）⇒ 視為 frame-miss（與 `lifecycle-frame-miss` 同一路徑：不武裝，若已有 active 則停止）。tmux 次數不受影響（只多 ≤ 武裝數次單筆 SQL）。
- **A3（snapshot 時 Idle，sweep 隨後移除 top frame 露出 Running 的 frame，漏武裝）**：已查證會被校正——sweep 的 `afterFrameCleared` 只同步 status、本來就**不**呼叫 `applyStatus`（舊路徑同樣不會因 sweep 重新武裝）；而該 session **之後任何一個 valid hook 事件**都經 `handler.go:605` 的 `manageActivityWatch` → `applyStatus` 以最新 top frame／status 重新評估（rename 也有 `captureProbeIntentReevalLocked`）。所以漏武裝只持續到該 session 的下一個 hook 事件；明載為**可接受窗口**（本 PR 與舊路徑的差別僅在 replay 這一輪內舊路徑會重讀到較新的 frame），並加交錯測試（T2f）。
- **A4（外部 tmux rename 在快取載入後、apply 前發生）**：與現況「武裝後才 rename」同一類時序，由 rename 的 `captureProbeIntentReevalLocked` 重新評估校正；寫為已知窗口，補 T3 的 happens-before barrier 案例。
- **更正 §先前敘述**：replay 期間**不是**「HTTP 前不會交錯」——Start 期間 sweep ticker 已在跑，tmux session 的 hook／watcher 也可能觸發（只是它們不呼叫 agent 的 re-evaluation）。replay 與這些的交錯只靠上面三條處置保證正確性，不靠「沒有並行」。
- **不改啟動順序**（replayFromDB → startSweep → replayStatus，agent `Start` 註解的約束不動）、不延後、不非同步。

### 2.3 T4：`selectSessionProjection` 不再為被丟棄的 session code 付 `resolveSessionCode`（d3 已裁定納入，獨立 commit）
- 所有路徑共用的 `selectSessionProjection` 改成只取名稱（`m.paneSessionName(paneID)`＝ `m.tmux.PaneSessionName`，`m.tmux == nil` 或錯誤時回 `""`，與原本 `resolvePaneSession` 的名稱部分一致）。
- **等價性定義**：「**投影選擇結果等價**」（回傳的 `*SessionProjection` 與原本逐一相同；nil tmux 與 `PaneSessionName` error 的結果不變）。**不是**「行為完全等價」：被省略的 `resolveSessionCode` 有三個副作用——刷新 `LookupCodeByName` 的 name cache、orphan cleanup、失敗日誌。**處置**：這些副作用在 `selectSessionProjection` 的每次呼叫上純屬附帶；其他路徑（`resolvePaneSession` 的其他呼叫者：`sweep.go:288/460/528`、`frame_ops.go:1475` 的 `liveSessionProjections`、hook 路徑自己的 `resolveSessionCodeFromHook`）**不改**，仍會刷新同一份快取；replay 路徑不依賴這些副作用（確認：`rg` 無 replay 路徑讀 name cache 的程式）。`resolvePaneSession` 原樣保留給仍需要 code 的呼叫者。

## 3. 任務（TDD：先紅）

- **T0 計數 fake**：包一層計數的 `tmux.FakeExecutor`（記 `PaneSessionName` 次數，支援 fail-once／指定 pane 失敗）與計數的 session provider（`ListSessions`、`LookupCodeByName`）、可注入錯誤的 frames 存取（`ListAll` fail-once）。fixture：S 個 session × P 個 live pane（含 codex 以走 intent 路徑）。現況斷言「呼叫次數 > P」→ 先紅。
- **T1 次數**：成功路徑下 `replayStatus` 的 `PaneSessionName` ≤ P（每 pane 最多一次）；replay 中 `ListSessions`／`LookupCodeByName` 不被呼叫（T4 之後）。**「≤1 次」限定成功路徑**。
- **T2 語意等價（oracle，A5）**：用**兩個獨立且完全相同的 Module**（相同 fixture、相同 fake、各自的 DB）。舊基線 Module：先做 `snapshotStatuses`（首次 agentType projection，`rc == nil`）再逐 session `applyStatus`（`rc == nil`）；新 Module：`replayStatus()`。detector 使用「受控、不發訊號」的版本（`startDetector` 只阻塞等 ctx），並在兩邊比較前以明確收斂點（`activeProbeIntents` 的狀態穩定、detector goroutine 已啟動）避免 target-match no-op／goroutine 改寫造成假陽性。比較：最終 `activeProbeIntents`（session→kind→{agentType, paneID, senderPID}）與 `currentStatus`。案例：(a) codex 在 shouldActive 狀態（武裝）、(b) codex 不在 shouldActive、(c) cc（無 intent）、(d) session 無 top frame、(e) 同 pane 多 frame（`projectionSortGreater`）、(f) **交錯**：snapshot Idle 之後移除 top frame 露出 Running（A3，斷言漏武裝且之後模擬一個 valid hook 事件的 `applyStatus` 會校正）、(g) **fail-once**：`ListAll` 失敗一次後成功、`PaneSessionName` 失敗一次後成功（A2：失敗不 poison、不快取空名，結果最終與基線相同）。
- **T2h A1 屏障測試**：以測試鉤子**強制序列**「sweep 刪除 frame row」→「replay 取得 `m.mu` 並走 `applyIntentLifecycle`」→「sweep 才同步 `currentStatus`」；斷言 replay **不武裝**該 frame（`activeProbeIntents` 無該 session 的 entry），且不重新武裝已刪除的 pid。變異：拿掉武裝前的 `GetByIdentity` 重讀 → 此測試紅。
- **T3 快取不跨輪與 rename（A4）**：兩次連續 `replayStatus` 之間改變 pane→session 對應，第二次反映新對應；另一個 goroutine 同時呼叫原 `projectionForSession` 得到即時結果（`-race`）；用 happens-before barrier 讓 rename 發生在快取載入之後、apply 之前，斷言現行已知窗口行為（使用快取的舊名稱）並在 rename 之後呼叫 `captureProbeIntentReevalLocked` 路徑得到校正。
- **T4 先紅測試（A6）**：直接執行 `projectionForSession`／`selectSessionProjection`：(i) 投影結果與改動前逐欄相同（對照用的是改動前的快照式 golden：以舊實作的輸出為期望）；(ii) `LookupCodeByName`／`ListSessions` 呼叫次數歸零；(iii) `m.tmux == nil` 與 `PaneSessionName` 回錯時的結果不變。
- **變異驗證**（暫時破壞確認翻紅）：`rc` 不載入快取（每次重算）→ T1 紅；`rc` 放到 `Module` 欄位且不清除 → T3 紅；`PaneSessionName` 失敗也被快取成空名 → T2g 紅；`ListAll` 失敗被快取 → T2g 紅；拿掉 A1 的重讀 → T2h 紅；`replayStatus` 傳 `nil` → T1 紅。

## 4. 不做
- 不延後／不非同步 replay；不加熱路徑的 TTL 快取（#1777）；不改日誌格式（alpha.559 的耗時行正好用來驗收）；不改 sweep 的 `afterFrameCleared`（不讓它呼叫 `applyStatus`，那是另一個行為變更）。

## 5. 驗收
- 單元／等價／屏障測試（`-race`）。部署重啟後 `[agent] start:` 的 `replayStatus=` 預期由 ~5600 ms 降到 < 500 ms；貼回 #1767。
- 回滾：單一 PR revert（T4 為獨立 commit，可單獨 revert）。

## 6. 規模
約 250–350 行（含測試）；一個 PR，改 `module.go`、`frame_ops.go`、`probe_intent_dispatcher.go` 與測試。若超過 800 行 diff／20 檔就把 T4 拆成第二個 PR。
