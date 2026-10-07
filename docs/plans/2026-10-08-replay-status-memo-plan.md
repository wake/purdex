# replayStatus 一輪內只算一次投影與 pane→session（#1767 修正 A）— 計畫

日期：2026-10-08　負責：purdex-37　審查：purdex-d3
背景：#1767（alpha.559 的耗時日誌：`agent` 模組 Start 6391 ms，其中 `replayStatus` 5618 ms，其餘模組都 < 25 ms）。熱路徑那一半另開 #1777（B），本 PR 不碰。

## 1. 問題（已查證，推論自程式與數字）

`probeIntentDispatcher.replayStatus`（`probe_intent_dispatcher.go:887`）：

1. `snapshotStatuses()`（`module.go:706`，持 `m.mu`）對 `currentStatus` 的**每個 session** 呼叫 `projectionForSession`；
2. 對每個 session `applyStatus` → `applyIntentLifecycle`；宣告了 probe intent 的 provider（codex）會再呼叫 `lookupTopFrameForSessionLocked` → 又是一次 `projectionForSession`。

`projectionForSession`（`frame_ops.go:1420`）每次都：`liveFrameProjections()`（`frames.ListAll` + `filterProjectionFrames` + `BuildSessionProjections`），再 `selectSessionProjection`（`frame_ops.go:1444`）對**每個** projection 呼叫 `resolvePaneSession`（`module.go:665`）＝ `tmux.PaneSessionName`（一次 `exec tmux display-message`）**加上 `resolveSessionCode(name)`**（`handler.go:799`：先走 session 模組的 1 s TTL `LookupCodeByName`，**miss 時退回 `ListSessions`＝1+7×S 次 tmux 子行程**）。`selectSessionProjection` 把回傳的 code 丟掉（`name, _ :=`），所以這份成本完全白付。

所以 replay 的成本約為 sessions 數 × live projection 數 P 次 `PaneSessionName`，外加冷快取時的 `ListSessions` 展開。mlab 實測：19 frame／17 pane、單次 tmux 呼叫 5–20 ms，`replayStatus` 5.6 s，吻合 S×P。（我**沒有**直接計數 tmux 呼叫；本 PR 的 T0 先加計數以便驗證修正前後。）

## 2. 修法（只動 replay 這一輪；快取不跨輪、不被其他 goroutine 看到）

新增一個**由 replay 這個 goroutine 自己持有、以參數傳遞**的 `replayProjectionCache`（不放在 `Module` 欄位，所以 sweep ticker、hook handler 等其他 goroutine 完全看不到它，沒有跨 goroutine 的過期問題）：

```go
type replayProjectionCache struct {
    projections []SessionProjection // liveFrameProjections() 一次
    loaded      bool
    err         error
    paneName    map[string]string   // paneID -> tmux session name，每個 pane 最多解析一次
}
```

- `m.projectionForSessionWith(session, rc)`：`rc == nil` ⇒ 與現在完全相同（呼叫原本的 `projectionForSession`）；`rc != nil` ⇒ 第一次用時載入 `liveFrameProjections()`，之後直接用；pane→session 以 `rc.paneName` 記憶，只呼叫 `m.tmux.PaneSessionName`（**不呼叫 `resolveSessionCode`**，反正結果被丟棄）。`selectSessionProjection` 抽出一個接受 resolver 函式的版本 `selectSessionProjectionBy(session, projections, nameOf func(paneID) string)`，原版以現有 `resolvePaneSession` 包成 resolver，行為不變。
- `snapshotStatuses(rc)`、`lookupTopFrameForSessionLocked(session, rc)` 多一個參數（`nil`＝現狀）；既有呼叫點傳 `nil`。
- `applyStatus` 拆成 `applyStatus(session, agentType, status)`（＝`applyStatusWith(..., nil)`，所有既有呼叫者不變）與 `applyStatusWith(..., rc)`；`applyIntentLifecycle` 同理多一個 `rc` 參數（內部傳給 `lookupTopFrameForSessionLocked`）。
- `replayStatus()`：建立一個 `rc`，`snapshotStatuses(rc)`、逐 session `applyStatusWith(..., rc)`；函式返回即丟棄 `rc`。

### 語意與取捨（請審）
- **一致性**：replay 期間改用「一輪開始時的 frame 快照」，而不是每次呼叫都重讀。replay 本來就是「`snapshotStatuses` 一次快照 → 逐 session 套用」，且 `applyIntentLifecycle` 在 `m.mu` 內會**重新驗證 `currentStatus`**（spec §5.4 ATK-2）——這個驗證保留不變。只有 frame／pane 資料變成一輪內固定。
- **replay 期間若 frame 或 pane 變動**（sweep 的 2 s ticker 可能 `clearFrame`；tmux session 改名）：最壞情況是用一輪開始時的 pane／pid 武裝了一個 detector，而該 frame 剛被清除。後果與「武裝之後 frame 才被清除」這種既有的正常時序相同：detector 在 `ctx` 取消或訊號被 `applyProbeGuards` 擋下時自行結束，且之後的狀態事件／rename 重新評估（`captureProbeIntentReevalLocked`）會校正。HTTP 在 replay 完成之前不接受請求（Start 同步），所以 hook 事件不會與 replay 交錯。這是已知的、可接受的窗口，明載於此。
- **快取不跨輪**：`rc` 是 `replayStatus` 的區域變數，函式結束即釋放；下一次任何呼叫（含再次 replay）都是新的 `rc`。
- 不改啟動順序（replayFromDB → startSweep → replayStatus 依序，agent `Start` 註解的約束不動）、不延後、不非同步。

## 3. 任務（TDD：先紅）

- **T0 計數 fake（紅的基礎）**：用 `tmux.FakeExecutor`（已有 `SetPaneSessionName`）包一層計數的 executor，記錄 `PaneSessionName` 呼叫次數；建立 fixture：N 個 session × P 個 live pane 的 frames（含 codex 以觸發 intent 路徑）。現況對這個 fixture 的呼叫次數 ≈ S×P（斷言「>P」→ 先紅）。
- **T1 呼叫次數 ≤ P**：`replayStatus` 之後 `PaneSessionName` 呼叫次數 ≤ P（每 pane 最多一次）；另斷言 `ListSessions`／`LookupCodeByName` 在 replay 中**不被呼叫**（用計數的 session provider 假件）。
- **T2 語意等價**：同一個 fixture，分別以「舊路徑」（逐 session `applyStatus`，`rc == nil`）與「新路徑」（`replayStatus`）執行，比較最終的 `activeProbeIntents`（session→kind→{agentType, paneID, senderPID}）與 `currentStatus`，兩者**相等**；覆蓋案例：codex 在 shouldActive 狀態（武裝）、codex 不在 shouldActive（不武裝）、cc（無 intent）、session 無 top frame、同一 pane 多 frame（選 projectionSortGreater 的那個）、pane 解析失敗（`PaneSessionName` 回錯）。
- **T3 快取不跨輪**：兩次連續 `replayStatus` 之間改變 pane→session 對應（fake 的 `SetPaneSessionName`），第二次反映新對應（證明沒有殘留快取）；另一個 goroutine 同時呼叫原本的 `projectionForSession` 得到的是即時結果（不受 `rc` 影響，`-race`）。
- **T4（選配，請 d3 決定）**：`selectSessionProjection`（所有路徑共用）目前把 `resolvePaneSession` 的 session code 丟掉卻仍付 `resolveSessionCode` 的成本；改為只取名稱的版本是**行為完全等價**的小改動，且同時讓熱路徑少付 `LookupCodeByName`（冷快取時的 `ListSessions` 展開）。因為它動到熱路徑（雖然等價），預設**不含在本 PR**，d3 若同意再加為獨立 commit；否則留給 #1777。
- **變異驗證**（暫時破壞確認翻紅）：`rc` 不載入快取（每次重算）→ T1 紅；`rc` 放到 `Module` 欄位且不清除 → T3 紅；快取 pane 名稱時忽略 error（把失敗當成空名永久快取）→ T2 的「解析失敗」案例紅；`replayStatus` 傳 `nil` → T1 紅。

## 4. 不做
- 不延後／不非同步 replay（agent `Start` 註解要求的順序）；不改 `projectionForSession` 的熱路徑行為（#1777）；不加 TTL 快取；不改任何日誌格式（alpha.559 的耗時行保留，正好用來驗收）。

## 5. 驗收
- 單元／等價測試（`-race`）。部署重啟後 `[agent] start:` 的 `replayStatus=` 預期由 ~5600 ms 降到 < 500 ms，`startup: ready in` 相應下降；貼回 #1767。
- 回滾：單一 PR revert。

## 6. 規模
約 150–200 行（含測試）；一個 PR，改 `module.go`、`frame_ops.go`、`probe_intent_dispatcher.go` 與測試。
