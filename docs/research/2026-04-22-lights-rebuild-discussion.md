# 燈號系統重建 — 討論記錄

**Date**: 2026-04-22
**Status**: 討論中（尚未定稿為 spec）
**Context**: Lights 子系統在 2026-04-22 全部 revert（PR #594），回到 legacy 實作。重新以小步快跑方式規劃對齊。

本文件是討論過程的記錄，**不是正式 spec**。若要進入實作，每個 Phase 需另寫正式 design spec 並經 codex review。

---

## 使用者提出的目標架構

### 1. Agent type
影響顯示 icon，需支援 **cc / codex / opencode**。

### 2. 主狀態
- `idle`（啟動、執行完畢…）
- `running`（執行中，如 cc UserPromptSubmit）
- `waiting`（permission / question）
- `closed`（session end）

### 3. 副狀態：unread
執行完畢時的 hook 產生。

### 4. 副狀態：subagent
兩種情境：

- **(1) 自呼叫的 subagent**（如 cc 自己觸發 subagent hook）
  需對齊進出的 subagent uid，避免遺留燈號。

- **(2) proxy agent**（如 cc 中呼叫 codex）
  此時 codex 自身的 hook 也會被觸發；需視為 parent agent 的 subagent，不是獨立 parent。

### 處理原則

1. **Hook 觸發 → 沒有 parent 時，hook 訊號決定 agent type 基準**
   - 例外破口：daemon 在 agent 之後啟動，此時 agent proxy agent，判定點誤把 subagent 視為 parent。

2. **Hook 缺乏的部分：**
   - (1) **沒有變化永遠比錯誤變化好** — 無法偵測的事件 / unknown 事件一律追蹤傳遞但不處理（保留資料以後使用）。
   - (2) **事件的事件**（before_idle / after_idle）讓 probe 可以導入輔助事件修正。
     - 範例：cc 在 question 後即使使用者已輸入也不觸發 hook → 持續黃燈。
       → 在 ask 事件後啟動 probe 監控畫面變化 → 一旦發生變化恢復執行中。

3. **Probe 兩個維度：**
   - [1] 統一所有 agent 可用的偵測行為（畫面變化中、畫面停止、彩虹字判斷）。
   - [2] 依據各家 agent 模組各自實作的特殊 probe。

### 資料蒐集
每個判斷點需作資料蒐集，後台 development 區可監測所有事件變化，核對是否判斷正確。

---

## 現況探索摘要

### Backend 已存在

| 元件 | 位置 | 狀態 |
|---|---|---|
| `AgentProvider` interface | `internal/agent/provider.go` | ✅ |
| cc / codex / opencode provider | `internal/agent/{cc,codex,opencode}/` | 三家都註冊 |
| Probe：Liveness / Activity / Readiness | `internal/agent/probe/` | ✅ 含 `activity.go` 的 screen-hash diff + `shell_prompt.go` |
| `frame_ops.go` — Frame + ParentFrameID + Subagents | `internal/module/agent/frame_ops.go` | Subagents 是 `[]string` |
| Hook ingest `/api/agent/event` | `internal/module/agent/handler.go:68` | ✅ |
| Trace pipeline `beginHookTrace` / `TraceStore` | `internal/module/agent/trace.go` | ✅ 記錄 verify/derive/apply |
| Probe 已掛到 status 轉移 | `internal/module/agent/module.go:410` `manageActivityWatch` | 進入 waiting/running/idle 即啟動 watcher |

### SPA 已存在

| 元件 | 位置 | 狀態 |
|---|---|---|
| `useAgentStore` | `spa/src/stores/useAgentStore.ts` | 含 statuses / agentTypes / models / subagents / unread / oscTitles / ccStatus |
| `TabStatusIndicator` | `spa/src/components/TabStatusIndicator.tsx` | 含 breathe / error diamond |
| `TabIcon` 組合器 | `spa/src/components/TabIcon.tsx` | 整合 icon + status + unread + SubagentDots |
| `getAgentIcon()` | `spa/src/lib/agent-icons.tsx` | cc ✅ / codex ✅ / **opencode ❌** |
| CC / Codex icon variant | Settings UI | ✅ |

### 各 agent DeriveStatus 覆蓋率

| Event | CC | Codex | OpenCode |
|---|:-:|:-:|:-:|
| SessionStart | ✅ | ✅ | ✅ |
| UserPromptSubmit | ✅ | ✅ | ✅ |
| Notification | ✅ | ❌ | ❌ |
| PermissionRequest | ✅ | ❌ | ✅ |
| Stop | ✅ | ✅ | ✅ |
| StopFailure | ✅ | ❌ | ✅ |
| SessionEnd | ✅ | ❌ | ✅ |
| SubagentStart/Stop | ✅ | ❌ | ✅ |

---

## 目標 vs 現況誤差表

| # | 需求項目 | 現況 | 誤差 |
|---|---|---|---|
| 1 | **Type：cc / codex / opencode icon** | cc ✅、codex ✅、opencode ❌ | 小 — 補一個 svg |
| 2 | **主狀態：idle / running / waiting / closed** | `StatusRunning / Waiting / Idle / Error / Clear`（多 error） | 對齊（多 error bonus） |
| 3 | **副狀態：unread** | `useAgentStore.unread` + SPA pip/dots 完整 | 已實作 |
| 4 | **副狀態：subagent (1) 自呼叫 uid 配對** | `frame.Subagents []string` append/remove by agent_id | 中 — 無 type、無 startedAt、未離場無 sweep |
| 5 | **副狀態：subagent (2) proxy agent** | 同 pane 不同 agent_type 會建獨立 frame | **大 — 架構未設計 proxy 情境** |
| 6 | **原則 1：沒 parent 時以 hook agent_type 為基準** | 一律以 `req.AgentType` 為基準，有 `FindByPanePID` 找 parent | 精神對齊，但**無顯式「沒 parent」判斷路徑** |
| 7 | **原則 1 例外：daemon 後啟動誤把 subagent 當 parent** | 完全未處理 | **大** |
| 8 | **原則 2(1)：未知事件追蹤不處理** | `Valid=false` 早退丟掉 | 中 — 精神對齊但**未追蹤 raw payload** |
| 9 | **原則 2(2)：before/after_idle 事件 bus** | 無形式化 event bus；probe 是 one-shot | 中 — 功能對齊但設計模式不同 |
| 10 | **Probe 維度 [1]：畫面變化 + shell_prompt + 彩虹字** | hash diff ✅ + shell_prompt ✅ + **彩虹字 ❌** | 中上對齊 — 彩虹字缺 |
| 11 | **Probe 維度 [2]：各 agent 專屬 readiness** | `internal/agent/*/readiness.go` 全家都有 | 對齊 |
| 12 | **判斷點資料蒐集（後端）** | `TraceStore` + `beginHookTrace` 三階段 | 已有 |
| 13 | **後台 Development Inspector UI** | `/api/dev/*` 只有 build/update/rebuild | **大 — 全缺** |

### 量級總結

- **對齊**：主狀態、unread、probe 基礎設施、per-agent readiness、後端 trace
- **小誤差（1–3 檔）**：opencode icon、彩虹字、未知事件追蹤記錄
- **中誤差**：subagent 結構升級、before/after_idle event bus 形式化、daemon 後啟動補回
- **大誤差**：proxy agent 模型、Dev Inspector UI

**整體誤差沒有想像大**。上次 Lights 膨脹是想重建整個 arbitrator / trace envelope 抽象層，但現有 trace + probe + frame 已涵蓋約 70% 需求。真正從零做的只有 proxy agent 模型 + Dev Inspector UI。

---

## Phase 拆法（使用者選擇層層對齊；Inspector 置後）

每個 Phase 獨立 PR、獨立 merge、獨立 bump alpha。每個 Phase 先寫正式 spec + codex review 再進實作。

### Phase 1 — L2 狀態層對齊
- Codex `DeriveStatus` 補齊（Notification / PermissionRequest / Subagent / SessionEnd / StopFailure）
- OpenCode brand icon 補上
- 未知事件 / `Valid=false`：從「丟棄」改「追蹤不處理」（存 raw payload 到 trace，不動狀態）
- **觸及**：3 個 `status.go` + `agent-icons.tsx` + handler 的 `Valid=false` 分支
- **風險**：低（加法為主）

### Phase 2 — L3 Subagent 模型升級
- `frame.Subagents: []string` → `[]SubagentRef{ID, Type, StartedAt}`
- Proxy agent 判斷：同 pane 有現存 parent frame 時，**不同 agent_type 的 hook 掛進 parent 當 subagent**（非建新 frame）
- SPA `subagents` store 升級顯示 type
- **觸及**：frame_ops.go + store schema + useAgentStore + SubagentDots
- **風險**：中（動 DB schema，alpha 階段無 migration）

### Phase 3 — L1 邊界補強
- daemon 後啟動補回機制：hook 進來時若無 frame，用 tmux process tree + existing sessions 推回 frame 樹
- 「沒有 parent 時以 hook agent_type 為基準」顯式化為判斷分支
- **觸及**：frame_ops.go 的 `FindByPanePID` 路徑 + 新補回 helper
- **風險**：中（邊界情況多）

### Phase 4 — L4 Probe 補強（可選）
- 彩虹字 / spinner 偵測加入 `activity.go`（避免 CC question 後使用者打字誤判 running）
- before_idle / after_idle event bus 形式化 — **Phase 1-3 走完後若發現需要才做**；若現有 one-shot watcher 夠用則砍掉（避免再次膨脹）
- **觸及**：probe/activity.go + 可能 module.go
- **風險**：低-中

### Phase 5 — L6 Dev Inspector
- Settings → Development 加 Event / Trace / Frame Inspector 面板
- 消費現有 `TraceStore` + 新 `/api/dev/events` endpoint
- **觸及**：dev module 新 handler + 新 SPA 頁
- **風險**：低（純讀取側）
- **不為前四個 Phase 預設 schema**，等前面穩定後依真實需求設計

---

## 原則（避免重蹈膨脹覆轍）

1. 每個 Phase 獨立 PR、獨立 merge、獨立 bump alpha
2. 每個 Phase 先寫 spec + codex review（依 CLAUDE.md 開發流程）
3. Phase 4 的 event bus 標「可選」，不預先實作抽象層
4. Inspector 置後，不為前 Phase 挖坑
5. 能走現有基礎設施就不新增模組（trace / probe / frame 已涵蓋 70%）

---

## Open Questions（尚待討論）

- Phase 2 是否拆 2a（schema 升級）+ 2b（proxy agent 邏輯）？使用者表示「到時候再拆」。
- Phase 4 event bus 是否真的需要？待 Phase 1-3 完成後評估。
- Phase 5 Inspector 的觀察粒度（per-event / per-transition / per-frame）待設計。
- proxy agent 判定條件細節：同 pane + 不同 agent_type 即算？還是需加時間窗 / PPID 驗證？

---

## 2026-04-23 補：骨架範圍收斂過程

### 迭代軌跡

1. **第一版**：提議完整骨架 — Status FSM (資料化) + Hook Handler Registry + State Entry Probe Registry + Combined Coverage。使用者質疑「是否又膨脹成前次 Lights」。
2. **第二版（bloat 圖）**：
   ```
   Status FSM（骨架）—— states + transitions
     ├─ 註冊 A：Hook Handler (hook, agent) → transition
     └─ 註冊 B：State Entry Probe (state, agent) → auxiliary signal → transition
   ```
   使用者再次質疑膨脹；也質疑「事件骨架」的必要性（各 agent 的 hook 本就不同，canonical event 強加 CC 的 ontology 給其他 agent）。
3. **第三版（縮減版）**：放棄 canonical event 層。骨架僅保留：
   - `Status` enum（已存在）
   - 各 agent 顯式宣告 `SupportedStatuses() []Status`（由使用者拍板：顯式優於隱式）
   - `Coverage(registry) []CoverageRow` helper
   - 現有 `DeriveStatus` switch 與 `manageActivityWatch` 保持不變（不 refactor）

### 尺寸差距（bloat vs 縮減）

| 指標 | bloat 圖 | 縮減版 | 倍數 |
|---|---|---|---|
| 新骨架檔行數 | ~250 | ~35 | 7× |
| 新增測試行數 | ~200 | ~30 | 7× |
| 既有檔案 refactor | ~90 行 | 0 行 | ∞ |
| 新資料結構 | 7 個 | 1 個（CoverageRow） | 7→1 |
| 新增方法 | ~10 個 | 1 個（`SupportedStatuses`） | 10→1 |

### 與 Reverted Lights 的 pattern 比對

Reverted Lights（2026-04-22 全撤 ~9000 行）：
- L1 trace envelope / divergences table
- L2 observation types
- L3 arbitrator goroutine（單寫者）
- L4 admission + 9-step apply pipeline

bloat 圖：
- L1 Status FSM 資料化
- L2 Hook Handler registry
- L3 State Entry Probe registry
- L4 Combined coverage 推導

**結構 pattern 同型**：兩者都以「把 working imperative code 轉成 declarative data + registry」為動機，且自然延伸路徑相似（priority / lifecycle / conflict resolution / 統一介面）。尺寸差 15× 僅為早期快照，動力學上 bloat 圖具備長大為 Lights 的壓力。

**縮減版打斷了五個典型 bloat pattern**：
1. 不把 working code 變 data
2. 不新增 parallel registry
3. 不以統一抽象覆蓋多種 case（probe 留原位）
4. 不 refactor 既有 working code
5. 不預埋 config flag / mode 切換

### 決議

- **Phase 0（骨架）= 縮減版**
- 各 agent `SupportedStatuses()` 實作 = Phase 1
- Probe formalization = Phase 4（可選）
- 每個 Phase 先寫正式 spec + codex review 再實作

### 正反評價（2026-04-23 新增）

**縮減版的不足**：
- 只答「誰支援什麼」，不答「怎麼達成」— 覆蓋率缺口需看 source 才知道 root cause
- 無法形式化「未知事件追蹤不處理」原則，該原則得另行 hack 進 handler.go
- Inspector (Phase 5) 只拿得到 declaration 矩陣，hook → transition 歷史仍要靠 `TraceStore` 原生資料
- declaration 與實作可能漂移（需額外測試驗證；縮減版未納入）
- Phase 2 subagent / proxy 情境零結構支持

**bloat 圖的優勢**：
- coverage 可從 handler 推導，對齊 declaration-vs-impl
- hook / probe 產生的 transition 在 Inspector 呈現統一
- 「未知事件」有天然型別路徑（`Lookup` sentinel），非 `Valid=false` overload
- Phase 2 subagent 可在 `Transition` 延伸 actor 欄位
- 支援 "declared-but-WIP" 狀態（如 codex 打算支援 waiting，未實作）

**bloat 圖的成本**：
- 1 PR ~550 行 refactor，touching `handleEvent` + `manageActivityWatch`
- 具備同 Reverted Lights 的自然延伸動力（priority / lifecycle / conflict …）
- 延伸形狀是押注 Phase 2-5 的實際需求，押錯則骨架要改 + agent 註冊跟著改（乘法工作）

**縮減版的風險不對稱**：
- 最壞：Phase 2-5 才發現需要結構，回頭補（增量工作）
- bloat 最壞：形狀押錯，骨架 + 已對齊的 agent 都要改（乘法工作）

**結論**：縮減版不是「不足」而是「留白」；bloat 圖不是「足夠小」而是「早期快照」。選擇取決於對 Phase 2-5 形狀的信心 — 若不確定則留白優於預設。
