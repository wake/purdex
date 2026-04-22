# Lights Rebuild Spec

- **Date**: 2026-04-23
- **Worktree**: `lights-rebuild-spec`（branch `worktree-lights-rebuild-spec`）
- **Context**: `docs/research/2026-04-22-lights-rebuild-discussion.md`（需求蒐集、現況盤點、骨架尺寸收斂過程、TraceStore reality check、tightened 決議）

## 1. 背景

Lights 子系統曾於 2026-04-22 回退一版 ~9000 行的膨脹重構。本 spec 採 **tightened 路線**：骨架最小、其他層沿用既有 infra（TraceStore / Monitor API / frame_ops / probe），分六個獨立 PR 小步推進。每 Phase 獨立 spec 節、獨立 merge、獨立 bump alpha、獨立 codex 兩輪 review。

## 2. 核心概念

### 2.1 Status 對齊矩陣

貫穿全 spec 的共用宣告資料。不是新 runtime 結構，是**宣告層查詢結果**，回答：

> 每一個已註冊的 agent provider，宣告自己支援哪些 `agent.Status`（running / waiting / idle / error / clear）？

由 Phase 0 建立骨架、Phase 1 填滿、Phase 5 呈現於 Inspector。其他 Phase 不修改它。

### 2.2 Probe 生命週期（現況）

系統已存在三層 probe，本 spec 的修改僅在指定 Phase，其餘保持現況：

| Probe 層 | 啟動條件（出場） | 退場條件 |
|---|---|---|
| **Liveness**（`IsAliveFor`） | 按需同步呼叫 — 用於 sweep、provider.IsAlive、無持續生命週期 | 單次呼叫即結束 |
| **Activity**（`StartWatch`） | hook 處理完且 `result.Valid=true`，且新 status ∈ {waiting, running, idle} → 啟動前先停舊 watcher | (a) 下一個 hook 觸發 `manageActivityWatch` 時被停 (b) 一次 callback 觸發後自然結束（one-shot 設計） (c) session 改名 (d) daemon shutdown |
| **Readiness**（`CheckReadiness`） | **模組層目前無 callsite**（已註冊、未被呼叫） | — |

Activity probe callback（`onActivityDetected`）將 signal 映射為 status：
- `Running` / `Idle` → 更新 status
- `ShellPrompt` + top frame PID 已死 → 觸發 `sweepOnce`、不更新 status
- `ShellPrompt` + top frame PID 存活 → 更新為 Idle
- 受 Error Guard 保護：不覆蓋 `StatusError`
- 完成後 broadcast `probe:activity` normalized event

Readiness callsite 缺失處理方向在 Open Questions 第 1 題，本 spec 預設**不主動修復**，由 Phase 4b 視需要整合。

### 2.3 TraceStore 作為跨 concern 的事實 SOT

三個 concern（hook → status、frame model、probe）在 runtime 層保持獨立演化；TraceStore 提供統一的歷史事實紀錄；Phase 5 Inspector 由 trace 跨 concern 拼接呈現，而不由 runtime 強制統一。

## 3. Phase Roadmap

| Phase | 範圍 | 風險 | 依賴 |
|---|---|---|---|
| 0 | Status 對齊骨架 | 低 | — |
| 1 | L2 狀態層對齊（宣告 + codex 事件補齊 + opencode icon + 未知事件追蹤） | 低 | Phase 0 |
| 2 | L3 Subagent 結構升級 + proxy 偵測 + frame idle sweep | 中 | Phase 1 |
| 3 | L1 邊界補強（daemon 後啟動補回 + 沒 parent 以 agent_type 為基準顯式化） | 中 | Phase 2 |
| 4a | Activity probe 內部強化（彩虹字 / spinner） | 低 | Phase 3 |
| 4b | 可選 `ProbeIntentProvider`（依 Phase 1-3 觀察決定是否做） | 中 | Phase 4a |
| 5 | Dev Inspector UI | 低 | Phase 4 |

每 Phase 節固定四段：**目的 / 改動觸及 / 驗收條件 / 備註**。

---

## 4. Phase 0 — Status 對齊骨架

### 目的

補上 agent 層「誰宣告支援哪些 Status」的宣告介面與查詢 helper，作為 Phase 1 的著力點。骨架完成時宣告矩陣為空（三家尚未實作），這正是 Phase 1 起點。

### 改動觸及

- `internal/agent/provider.go`：新增 optional capability interface `StatusSupporter`
  - 契約：`SupportedStatuses()` 回傳 Status slice
  - 設計同型於既有 `HookInstaller` / `StatuslineInstaller`（optional，type assertion 判斷）
- `internal/agent/coverage.go`（新檔）：`CoverageRow` 型別 + `Coverage(Registry)` helper
  - `CoverageRow` 欄位：agent type、是否宣告（布林）、宣告內容（Status slice，未宣告時為 nil）
  - `Coverage` 掃 registry 所有 provider，以 agent type 字母序回傳 row slice
- `internal/agent/coverage_test.go`（新檔）：TDD 測試，涵蓋空 registry、未實作 provider、實作 provider 三情境 + 排序穩定性
- 零改動：既有 provider / handler / module / probe / store

### 驗收條件

- `go build ./...` 與 `go test ./internal/agent/...` 綠
- PR diff 僅涉及上述三檔
- Coverage 對未實作 `StatusSupporter` 的 provider 回傳明確「未宣告」標記，不是 nil 歧義

### 備註

`SupportedStatuses()` 刻意不在 Phase 0 於任一 provider 實作 — 保持骨架純度，避免 Phase 0 與 Phase 1 責任混淆。

---

## 5. Phase 1 — L2 狀態層對齊

### 目的

填滿 Phase 0 的對齊矩陣、補齊 codex 事件覆蓋、補 opencode icon、把未知事件從丟棄改為追蹤不處理。一次對齊「說好支援的」與「實際實作的」。

### 改動觸及

- 三家 provider（`cc` / `codex` / `opencode`）實作 `SupportedStatuses()`
- `internal/agent/codex/status.go` 補齊五個事件的 DeriveStatus：
  - `Notification`、`PermissionRequest`、`SubagentStart/Stop`、`SessionEnd`、`StopFailure`
  - 對照 cc 的既有 mapping 為參考
- `spa/src/lib/agent-icons.tsx` 補 opencode brand icon
- `internal/module/agent/handler.go` 的 `Valid=false` 早退分支改寫：
  - 新行為：寫一筆 trace step，kind 維持 verify 層，reason 標示 `event_not_in_catalog`，payload 保留 raw
  - 不更新 status（保留「追蹤不處理」精神）
- 新增 declared-vs-implemented drift 測試：
  - 對每個 provider，驗證 `SupportedStatuses()` 宣告的每個 Status 都能由其 `DeriveStatus` 在某 event 下實際產出
  - 使用 table-driven 格式 + 現有 cc hook fixture 集合

### 驗收條件

- Coverage 回傳三 row 皆為「已宣告」，且 drift 測試全綠
- codex 五個事件在對應 hook fixture 下產出預期 status
- opencode icon 在 SPA TabIcon 顯示正確（手動驗證：啟動 opencode session 看 tab）
- 未知事件可由 `SELECT ... FROM agent_trace_steps WHERE reason = 'event_not_in_catalog'` 查到
- `pnpm run lint && pnpm run build` 綠、`go test ./...` 綠

### 備註

drift 測試是宣告驅動的 — 若未來 provider 宣告 `StatusWaiting` 但沒實作對應產出路徑，測試應失敗。這彌補了 tightened 骨架的留白 trade-off。

---

## 6. Phase 2 — L3 Subagent 升級 + Proxy + Frame Idle Sweep

### 目的

三件事一起做：
1. `frame.Subagents` 從字串 ID list 升級為結構化 ref（含 Type、StartedAt、IsProxy）
2. 偵測同 pane 內跨 agent type 的 proxy 情境，掛進 parent 的 Subagents 而非建新 frame
3. 清理殭屍 frame（實際觀察：codex-companion 不 auto exit，幾小時內會累積）

### 改動觸及

- `internal/store/frame.go`
  - `SubagentRef` 欄位擴充：agent ID（既有）+ agent type + started at + is proxy（新增三項）
  - `Frame.Subagents` 欄位型別同步升級為 `[]SubagentRef`
  - alpha 階段允許直接破壞式升級，不寫 migration（依靠重建 DB）
- `internal/module/agent/frame_ops.go`
  - 新增 proxy 偵測分支，條件為：
    - 同 pane 已存在一個 parent frame
    - 新 hook 的 agent_type 與該 parent 不同
  - 命中時將 ref 附加到 parent.Subagents 並標 `IsProxy=true`，不建新 frame
- Frame idle sweep：
  - 既有 `sweepOnce` 路徑補一條「無 hook 活動超過閾值」規則
  - 閾值預設 1 小時，以 Configurable const 表達（非 runtime flag）
  - 清除 frame 時，若該 session 有 active activity watcher，同步呼叫 `StopWatch`
- `spa/src/stores/useAgentStore.ts` subagents 欄位升級為結構化型別
- `spa/src/components/SubagentDots.tsx` 依 agent type 顯示顏色 / 圖示差異

### 驗收條件

- 手動場景：單一 pane 內 cc → `/codex:*` 啟動 codex，codex frame 以 proxy 掛進 cc parent 的 Subagents，非獨立 frame
- 殭屍清理：手造超過閾值的閒置 frame，sweep 執行後 frame 被清除且有 log
- SPA SubagentDots 可視覺區分 proxy vs native subagent
- `go test ./...` 全綠；`frame_ops` 新路徑有 table-driven 測試（native subagent / proxy / 跨 pane 不算 proxy 三情境）

### 備註

sweep 與 activity watcher 職責切分：
- sweep 清的是**frame 狀態**（stale data）
- activity watcher 清的是**watcher 自己**（one-shot 自然退場）
- 兩者有重疊時（sweep 清了一個有 watcher 的 frame），sweep 呼叫 `StopWatch` 處理，避免 orphan watcher

---

## 7. Phase 3 — L1 邊界補強

### 目的

處理兩個邊界：
1. daemon 後啟動時，既有 tmux session 內的 frame 樹無法由 live hook 重建 — 需用 tmux process tree + existing sessions 推回
2.「沒有 parent 時以 hook agent_type 為基準」的精神目前隱含在 fallback，需顯式化為具名分支（方便 trace + Inspector 呈現）

### 改動觸及

- `internal/module/agent/frame_ops.go`
  - `FindByPanePID` 路徑新增「無 frame 命中」的補回分支
  - 補回 helper：走 tmux process tree + existing sessions，推斷 agent_type + parent 關係，建立 frame 樹
  - 補回時一併寫入 Phase 2 升級後的 SubagentRef 新欄位（Type / StartedAt / IsProxy）
- 顯式 `no-parent` 判斷分支：進入前寫一筆 trace step，reason 標示 `no_parent_fallback`，利於 Inspector 統計

### 驗收條件

- daemon 重啟後在既有 session 觸發 hook，frame 樹重建、subagent 不漏
- `no_parent_fallback` trace step 在對應情境可查
- 手動測試清單（daemon 重啟 / 中途接回 / 冷啟動三情境）逐項通過，附 log 截圖於 PR
- `go test ./...` 綠，補回路徑有 table-driven 測試

### 備註

本 Phase 依賴 Phase 2 的 `SubagentRef` 結構 — 補回時需一併填新欄位。Phase 2 未 merge 前不可開工。

---

## 8. Phase 4 — Probe 延伸

Phase 4 分兩小步，4b 是條件執行（依 Phase 1-3 的實際觀察決定）：

### 8.1 Phase 4a — Activity probe 內部強化（彩虹字 / spinner）

#### 目的

activity probe 目前靠畫面 hash diff 判定，但 cc 提問後若使用者在鍵盤打字 / spinner 畫面仍會被判為 Running（反之使用者停頓時可能誤判 Idle）。本 Phase 在既有 activity probe 的畫面判定中加入字元層偵測，**不改變 probe 出場條件**。

#### 改動觸及

- `internal/agent/probe/activity.go`
  - 在畫面 hash diff 之外新增字元層辨識：
    - 彩虹 ANSI 序列（指示 codex / cc 進行中）
    - 常見 spinner 字元（braille 系列 `⠋⠙⠹...` 與旋轉系列 `/─\|`）
  - 命中時 signal 維持 `Running` 語意，但避免 3 次穩定穩定計數被誤判觸發 `Idle`
- `internal/agent/probe/activity_test.go` 擴充：兩種字元樣式 + 純靜態畫面的對照測試

#### 驗收條件

- 彩虹字 / spinner 情境下 probe 不把畫面判為 idle
- 既有 activity probe 測試全綠
- 效能：字元偵測在現有 probe 週期（500ms）內可吸收、不新增 goroutine

#### 備註

本 Phase 僅動 activity probe 內部判定邏輯，**不改** `manageActivityWatch` 的出場條件（仍是 hook 後 + status ∈ {waiting, running, idle}）。

### 8.2 Phase 4b — 可選 `ProbeIntentProvider`（條件執行）

#### 目的

讓 agent 宣告自己期望的 probe 觸發條件與 signal mapping，讓 probe 出場條件**從寫死擴充為 agent 驅動**，並順便整合 readiness（解決現況 callsite 缺失）。僅在 Phase 1-3 過程中出現明確需求時執行。

#### 改動觸及

- `internal/agent/provider.go` 新增 optional interface `ProbeIntentProvider`
  - 契約：`ProbeIntents()` 回傳 ProbeIntent slice
  - ProbeIntent 三欄位（表格描述，非 code）：

    | 欄位 | 語意 |
    |---|---|
    | `on_entry_status` | 進入哪個 Status 時啟動此 intent |
    | `detector` | agent 自訂的偵測器（tmux pane 內容 → signal） |
    | `on_signal` | signal 對應到 Status 的 mapping |
- `internal/module/agent/module.go` 的 `manageActivityWatch`：
  - 既有預設路徑完全保留（未實作 `ProbeIntentProvider` 的 agent 行為不變）
  - 若 agent 實作 `ProbeIntentProvider`，於 status 變更時檢查並啟動對應 intent
- 整合 readiness：cc 的 `CheckReadiness`（看 Allow/Deny / ❯ 提示）以 `ProbeIntent` 形式表達為「進入 waiting 時檢查 permission prompt pattern」

#### 驗收條件

- 至少一家 provider 使用 `ProbeIntentProvider`（建議 cc，整合現有 readiness）
- drift 測試：宣告 intent vs 實際 probe 呼叫路徑
- 未使用 `ProbeIntentProvider` 的 agent 行為完全不變（regression 測試覆蓋既有 Phase 1 場景）

#### 備註

執行門檻：Phase 1-3 結束後查 `agent_trace_steps`，若發現以下任一情境頻繁出現，才執行 Phase 4b：
- 彩虹字強化（4a）仍無法覆蓋的誤判
- 多 agent 需要差異化 probe 策略
- readiness callsite 缺失成為實際 bug

否則 readiness 現況問題另開 gh issue 追蹤，Phase 4 在 4a 後結案。

---

## 9. Phase 5 — Dev Inspector UI

### 目的

Settings → Development 區塊新增 Event / Trace / Frame / Coverage 四視圖 Inspector，消費既有 `/api/agent/monitor/chains`、`/chains/{id}`、`/projection` 三 endpoint，並新增 Coverage 矩陣視圖消費 Phase 0 的 `Coverage()` 結果。後端資料已齊，本 Phase 純讀取側。

### 改動觸及

- 新增 `/api/agent/monitor/coverage` endpoint：將 `Coverage()` 結果 JSON 化（欄位命名以 Phase 0 結構為準）
- SPA 新增 Inspector 面板：
  - **Chain List** — 依時間 / agent type / pane / reason 過濾
  - **Chain Detail** — `buildStepTree` 父子樹呈現 verify / derive / apply 三階段 step，可展開原始 payload
  - **Projection** — 目前 frame + subagent 即時快照
  - **Coverage Matrix** — 每 agent 宣告 vs 實際產出比對，高亮缺口（drift 測試失敗情境也會在此一眼可見）
- Settings 導航加入 Development → Inspector 入口
- 手動測試清單：cc / codex / opencode 各觸發一次 hook，驗證四視圖資料一致

### 驗收條件

- 三 endpoint + Coverage endpoint 在 Inspector 內正常顯示、篩選條件生效
- Coverage Matrix 高亮 drift（Phase 1 drift 測試若失敗也能在 UI 識別）
- `pnpm run lint && pnpm run build` 綠
- 效能：首次載入 < 500ms、切換 chain < 100ms

### 備註

Inspector 僅讀取不 mutation。若 Phase 2-4 在 Inspector 設計期間仍變動 frame / probe 結構，以最終形態為準，不預先鎖死 UI schema。

---

## 10. 跨 Phase 驗收策略

- **PR 策略**：每 Phase 一個 PR；CLAUDE.md 規定的 TDD + 兩輪 codex review（標準 + 3 parallel 攻防體質）流程全數適用
- **Bump 策略**：每 Phase merge 後獨立 bump alpha PR，`VERSION` / `package.json` / `spa/package.json` 三處同步
- **Known issues 管理**：review 問題以「信心 / 關聯 / 複雜」三維度彙整；低關聯 + 中高複雜可延後成 gh issue（label 按 CLAUDE.md 兩維度：type 必選一、scope 可多選）
- **Worktree 使用**：Phase 0 使用現有 `lights-rebuild-spec`；Phase 1+ 各自另起 worktree 避免 diff 混淆
- **Phase 4 條件判斷**：4a 為必做；4b 僅在 Phase 1-3 過程中出現明確需求時執行，否則 Phase 4 於 4a 結案、Readiness callsite 缺失另開 issue

## 11. Open Questions

1. **Readiness 模組層無 callsite 如何處理** — 選項：(a) Phase 4b 整合（現 spec 預設）(b) Phase 1 補齊整合 (c) 明確標註本次不處理、開 issue 追蹤
2. **Phase 2 是否拆 2a（schema 升級）+ 2b（proxy + sweep）** — 依 review 負擔決定，到時再拆
3. **Phase 4b 條件是否成立** — 以 Phase 1–3 的 trace 資料為判斷依據，非預設
4. **Phase 5 Inspector 觀察粒度** — 先以 chain（既有 `TraceChain`）為單位；per-event / per-transition 作為後續迭代
5. **Proxy 判定條件是否加時間窗 / PPID 驗證** — Phase 2 初版僅 pane + agent_type；若 Phase 3 揭露誤判再強化
6. **Frame idle sweep 閾值** — 初值 1 小時；以實際觀察校準

## 12. 相關檔案速查

- **Status**：`internal/agent/status.go`
- **Registry**：`internal/agent/registry.go`
- **Provider**：`internal/agent/{cc,codex,opencode}/provider.go`、`.../status.go`、`.../readiness.go`
- **Probe**：`internal/agent/probe/{activity,liveness,readiness,shell_prompt,probe}.go`
- **Frame**：`internal/store/frame.go`、`internal/module/agent/frame_ops.go`
- **Hook ingest**：`internal/module/agent/handler.go`
- **Trace**：`internal/store/trace.go`、`internal/module/agent/trace.go`
- **Monitor API**：`internal/module/agent/monitor.go`
- **Module lifecycle**：`internal/module/agent/module.go`（含 `manageActivityWatch` / `onActivityDetected` / `sweepOnce`）
- **SPA state**：`spa/src/stores/useAgentStore.ts`
- **SPA 視覺**：`spa/src/components/{TabIcon,TabStatusIndicator,SubagentDots}.tsx`、`spa/src/lib/agent-icons.tsx`
