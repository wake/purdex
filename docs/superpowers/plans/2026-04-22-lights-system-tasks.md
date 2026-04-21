# Lights System — Task Tracker

> 對應 spec：[`2026-04-22-lights-system-design.md`](../specs/2026-04-22-lights-system-design.md)（v3 封版，1510 行）
> 封版 PR：[#548](https://github.com/wake/purdex/pull/548)
> Kickoff memory：`kickoff_lights_spec.md`
> 建立於：2026-04-22（alpha.199）

本文件追蹤整個燈號系統重構開發進度。每個 PR 對應一筆追蹤條目，實作順序遵照 spec §10 的依賴關係與時序指引（§10.4）。

---

## 狀態圖例

| 圖例 | 意義 |
|---|---|
| ⬜ | 未開始 |
| 🟡 | plan 撰寫中 / codex review |
| 🔵 | 實作中（feature branch） |
| 🟣 | PR open / review 中 |
| 🟢 | merged |
| ⚪ | 部分完成（有子項目未收斂） |
| 🔴 | 阻塞 / 需討論 |

---

## 整體進度

| Phase | PR 數 | 已合 | 進度 |
|---|---|---|---|
| 0 輕清理 | 1 | 0 | ⚪ 部分（cleanup 已做，capability bit placeholder 未做）|
| 1 Schema + 雙寫過渡 | 2 | 0 | ⬜ |
| 2 Arbitrator 切換 | 3 | 0 | ⬜ |
| 3 抽象層重構 | 2 | 0 | ⬜ |
| 4 三 agent 對齊 | 3 | 0 | ⬜ |
| 5 硬編拆除 + SPA 對齊 | 4 | 0 | ⬜ |
| 6 Trace viewer | 2 | 0 | ⬜ |
| **合計** | **17** | **0** | — |

**前置 task**（不算正式 PR）：

- ⬜ **T-plan-A**：整體大綱 plan（覆蓋 17 PR 的高階 plan；先 codex review 校準全局）
- ⬜ **T-plan-B**：PR-0 細 plan
- ⬜ **T-review-plan**：plan codex review（類似 spec 3 輪收斂）

---

## Phase 0 — 輕清理

### PR-0 — 分支清理 + Codex readiness capability bit placeholder

| 欄位 | 內容 |
|---|---|
| **狀態** | ⚪ 部分完成 |
| **依賴** | — |
| **Spec 對照** | §10 PR 表第 1 列；§6.4 Codex 提升 |
| **尺寸** | 極小 |

**子項目**

- [x] close PR [#486](https://github.com/wake/purdex/pull/486)（於 spec kickoff session 已做）
- [x] 刪除分支 `feat/agent-watch-alive`（於 spec kickoff session 已做）
- [ ] Codex `readiness` 加 capability bit placeholder（只加 bit，不補邏輯 — 邏輯留 PR-4a）
- [ ] CHANGELOG + VERSION bump（獨立 bump PR）

**驗收**

- Codex agent spec 有 `HasReadiness` capability bit 宣告位（預設 false，PR-4a 翻成 true）
- PR #486 在 GitHub 顯示 closed 且分支移除

---

## Phase 1 — Schema + 雙寫過渡

### PR-1a — Trace schema 升級 + `frame_divergences` 表

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-0 |
| **Spec 對照** | §3.5（Trace envelope）、§3.5.1（back-pressure）、§8.1（Phase 1 雙寫） |
| **尺寸** | 中 |

**子項目**

- [ ] SQLite schema：trace 表加一級欄位（`source_kind / action / reason_code / outcome / scenario_key / observed_generation / decision_ports[]` 等）
- [ ] 新增 `frame_divergences` 表 DDL
- [ ] hook path 填值（確保新欄位在舊 path 即可被寫入）
- [ ] Schema migration（alpha 階段可接受 breaking drop；照 `feedback_no_alpha_migration`）

**驗收**

- Trace 寫入能用新 schema，舊 consumer 能正確忽略未知欄位
- `frame_divergences` 表存在且可寫入
- 既有 regression 測試綠

---

### PR-1b — Observation + Arbitrator passthrough + channel admission control

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-1a |
| **Spec 對照** | §3.1（資料流）、§3.3（Observation）、§3.4（Arbitrator）、§3.5.1（admission control）、§8.1 |
| **尺寸** | 中大 |

**子項目**

- [ ] 定義 `Observation` 型別（含 DecisionPort cap 16、source_kind、scenario_key 等）
- [ ] Arbitrator 骨架 goroutine + 三 channel（`arb_in` cap 1024 / `retryCh` cap 256 / `traceOut` cap 4096）
- [ ] Channel admission control（proposed drop non-blocking；committed blocking 100ms timeout；滿載 drop 策略）
- [ ] Hook / probe / sweep **雙寫**：既直寫 frame，也送 Observation → Arbitrator
- [ ] Arbitrator passthrough 模式：以新 multi-actor schema 計算 proposal，下投影到舊 schema 比對，寫 `frame_divergences`
- [ ] Sweep → Observation 整合（開放問題 §11 追蹤項）

**驗收**

- `AGENT_ARB_MODE=passthrough` 預設運行
- divergence 表有資料可觀察（用 Mini dev server 跑一陣子驗證）
- 不改變使用者可見行為

---

## Phase 2 — Arbitrator 切換

### PR-2a — Frame multi-actor + pid tree role + retry/pending + handler 同步/非同步邊界重構

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-1b |
| **Spec 對照** | §3.2（multi-actor）、§3.3、§3.4（apply pipeline）、§5.3（ancestor walk）、§5.4（retry/pending）、§5.4.1（handler 邊界）、§5.5（role 判斷） |
| **尺寸** | 大 |
| **風險** | 尺寸易膨脹；plan 層切 sub-diff（verify → observation → arbitrator）分別審 |

**子項目**

- [ ] `Frame` schema 改 multi-actor（`Actors []Actor`，複合鍵 `ActorKey{SessionID, Generation, ActorID}`）
- [ ] `Actor` struct（Role、Status、Lifecycle 四欄位）
- [ ] Role 由 pid tree ancestor walk 判定（§5.3）
- [ ] Retry + Pending window（§5.4 並發所有權版）
- [ ] Generation gate（只 `hook.SessionStart` 可推進；future-gen observation reject）
- [ ] WatcherToken 儲存於 Observation（§3.3 cap 16 一併）
- [ ] Idempotency key（apply pipeline §3.4.1 step 2）
- [ ] `Lifecycle.LastActivity` 欄位 + 更新規則
- [ ] Handler 同步/非同步邊界重構（§5.4.1 — verify/applyFrameEvent/watcher/broadcast 拆法由 plan 決定）
- [ ] SyntheticEndLifecycle 於 primary 替換時發出（§3.2 invariant）

**驗收**

- Arbitrator 產出 frame 與舊 path 投影一致率 > 閾值（spec 定；否則 block merge）
- pid tree 測試覆蓋 §9.1 + §9.2 全部案例
- 仍在 `passthrough` 模式運行（不切換 writer）

---

### PR-2b — Arbitrator 升為 authoritative + `AGENT_ARB_MODE` 注入

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-2a |
| **Spec 對照** | §8.2、§8.3 |
| **尺寸** | 中大 |
| **風險** | 所有 frame 寫入路徑改動，regression 面積大；flag 可回退 |

**子項目**

- [ ] 切換 frame writer = Arbitrator 唯一（legacy direct write 保留但不啟用）
- [ ] `AGENT_ARB_MODE` env/config 注入（`passthrough` / `authoritative`）
- [ ] Hot reload：新 mode 延到下個 SessionStart 才生效（§10.2 關鍵決定）
- [ ] Baseline regression 測試（§9.5 整合測試 scope）

**驗收**

- 預設 `AGENT_ARB_MODE=authoritative` 運行一週無 regression（§10.4 時序：觀察一週後再送 PR-2c）
- 可透過 config 即時切回 `passthrough`

---

### PR-2c — 移除 legacy direct write + 刪 legacy 測試

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-2b（穩定運行 1 alpha 週期） |
| **Spec 對照** | §8.2 |
| **尺寸** | 中 |
| **風險** | 不可逆；需 revert commit 備案 |

**子項目**

- [ ] 移除 hook / probe / sweep 的 direct frame write path
- [ ] 刪除 legacy divergence 比對邏輯
- [ ] 刪除 legacy 測試（divergence 測試、passthrough 測試）
- [ ] 只保留 `authoritative` 模式（env 仍留作 kill switch）

**驗收**

- 所有 frame 寫入都經 Arbitrator
- 測試不再有 legacy path reference

---

## Phase 3 — 抽象層重構

### PR-3a — AgentSpec 五層 + Capability bits + Legacy Compat Adapter

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-2c |
| **Spec 對照** | §4.1（五層分離）、§3.6（capability bits）、§4.5（Registry 顯式註冊）、§4.6.1（Compat Adapter） |
| **尺寸** | 中大 |
| **風險** | 既有 hard-coded `cc` 呼叫端（`stream/module.go:39`、`handler.go:311/381/539`）若無 adapter 會 nil panic |

**子項目**

- [ ] `AgentSpec = Descriptor + Provider + ProbePolicy + Optional services` 介面定義
- [ ] `Capability` bits 11 個（§3.6 擴張版）
- [ ] `Registry` 一致性驗證（§4.5：Register 回 error，`NewDefaultRegistry` panic）
- [ ] `RegisterBuiltinAgents` 顯式註冊（三 agent）
- [ ] **Legacy Provider Compat Adapter**（§4.6.1）
- [ ] 既有 hard-coded `cc` 呼叫端改走 compat adapter（暫緩動）

**驗收**

- Registry 啟動時能 panic 報出配置錯誤
- 既有 stream / handler 行為無回歸
- 三 agent 皆透過 registry 取得

---

### PR-3b — ProbePolicy + Scheduler + common_probes + self_detection（disabled）

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-3a |
| **Spec 對照** | §4.2（ProbePolicy）、§4.3（Scheduler）、§4.4（common_probes）、§6.5（self_detection disabled） |
| **尺寸** | 中大 |

**子項目**

- [ ] `ProbePolicy` 介面
- [ ] `ProbeBinding`（`OnDemand` / `Continuous`）
- [ ] Probe Scheduler（fan-out、cancellation、錯誤隔離）
- [ ] `common_probes`：motion probe + 彩虹字 probe（§11 彩虹字 palette 可能需 spike）
- [ ] `self_detection` probe（disabled by default）

**驗收**

- 三 agent 現有 probe 能透過 ProbePolicy 運作（不啟動新邏輯）
- Scheduler 不造成 goroutine leak（§9.2 壓測）

---

## Phase 4 — 三 Agent 對齊

> §10.1：PR-4a + PR-4b 可 parallel（依 `internal/agent/<agent>/` 獨立檔）；PR-4c 先合（schema 先動），4a/4b rebase。

### PR-4a — Codex ProbePolicy + Capability bits

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-3b |
| **Spec 對照** | §6.2、§6.4 |
| **尺寸** | 中 |

**子項目**

- [ ] Codex ProbePolicy 實作（liveness + activity + readiness）
- [ ] Codex readiness 補真實邏輯（取代 stub；參考 CC CapturePaneContent 模式；codex prompt marker 查證）
- [ ] Capability bits 標註（`HasReadiness`、`HasStreamResumer` 等）
- [ ] `HasReadiness` 從 placeholder false 翻 true

**驗收**

- Codex agent 黃燈救援路徑生效（`project_lights_current_status` 風險 #1 清除）
- Capability bits 對外匯出正確

---

### PR-4b — OpenCode ProbePolicy + readiness 補齊 + `HasReadiness=true`

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-3b |
| **Spec 對照** | §6.2、§6.3 |
| **尺寸** | 中 |

**子項目**

- [ ] OpenCode ProbePolicy 實作
- [ ] OpenCode readiness 補齊（既有 mount 只有 identifier 未註冊 readiness — 見 `project_lights_current_status`）
- [ ] Capability bits 標註
- [ ] `HasReadiness` = true

**驗收**

- OpenCode 與 CC / Codex 對稱矩陣（§6.1）拉平
- 三 agent 對稱測試（§9.5）通過

---

### PR-4c — Subagent typed model（`SubagentRef{id, type}`）

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-2a |
| **Spec 對照** | §5.6（Subagent 歸屬）、§2.2（Subagent vs Proxy） |
| **尺寸** | 中 |
| **風險** | Schema 三層同步；migration 可接受重置（`feedback_no_alpha_migration`） |

**子項目**

- [ ] `SubagentRef{id, type}` type 定義
- [ ] Frame / Projection schema 升級
- [ ] OpenCode typed（從 `detail` 欄位提升為一級）

**驗收**

- SPA 可吃 typed subagent（DATA_FIELDS 對齊）
- 跨 agent subagent 歸屬規則（§5.6）測試覆蓋

---

## Phase 5 — 硬編拆除 + SPA 對齊

> §10.1：PR-5a0/5a1/5a2/5b 必須串行（daemon metadata → orchestrator → API → SPA）。

### PR-5a0 — Session metadata 中性化（`CCSessionID` → `ResumeToken`）+ back-pressure 實作

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-3a |
| **Spec 對照** | §6.6（Stream handoff 泛化）、§3.5.1（back-pressure） |
| **尺寸** | 中 |

**子項目**

- [ ] DB schema 把 `CCSessionID` 改名為 `ResumeToken`
- [ ] 所有讀寫點更名
- [ ] Trace back-pressure policy 實作（batching / sampling / drop priority）

**驗收**

- 無對外行為改變
- Trace 高壓下不會 OOM / goroutine leak

---

### PR-5a1 — Stream orchestrator 泛化（走 `spec.Operator()` / `StreamResumer()`）

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-5a0 |
| **Spec 對照** | §6.6 |
| **尺寸** | 中 |

**子項目**

- [ ] Stream orchestrator 硬編 `cc.operator` / `ccOps` 走 `spec.Operator()` / `spec.StreamResumer()` 動態分派
- [ ] 移除 compat adapter 中 stream 相關路徑

**驗收**

- CC stream handoff 無回歸
- 為 Codex / OpenCode 未來加 `HasStreamResumer` 預留路徑

---

### PR-5a2 — API 層 capability 分派 + 移除剩餘 compat adapter

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-5a1 |
| **Spec 對照** | §6.6 |
| **尺寸** | 中 |

**子項目**

- [ ] `/api/agent/status` 改走 `spec.Statusline()`
- [ ] Statusline installer 改走 `spec.Descriptor().Capabilities` 判斷
- [ ] 移除剩餘 compat adapter

**驗收**

- 三 agent API 行為對稱
- Compat adapter 完全移除（code 不留空殼）

---

### PR-5b — SPA 側拆 cc 硬編 + 燈號 UI 體質

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-5a2 |
| **Spec 對照** | §2.1（UI 投影規則）、`project_lights_current_status` 風險 #3/#4/#5 |
| **尺寸** | 中大（純 SPA） |

**子項目**

- [ ] SPA 拆 cc 硬編（icon list / detect list / metadata → registry 化）
- [ ] `AgentStatus` union 加 `clear`（現漏）
- [ ] 顏色 SOT palette 整併（三組：dot hex / badge tailwind / aggregate ActivityBarNarrow）
- [ ] SessionsSection chip 4 色對齊（加 waiting 黃 + idle 灰）
- [ ] §2.1 UI 投影規則實作（主色 / badge / trace-only 三類）
- [ ] UI drop 規則：trace-only event 不更動 frame badge

**驗收**

- `project_lights_current_status` 風險 #3/#4/#5 清除
- SPA test（vitest）綠；所有 AgentStatus 分支覆蓋

---

## Phase 6 — Trace Viewer

> §10.1：PR-6a 可從 PR-1a 合後並行；PR-6b 純 SPA。

### PR-6a — Trace read API（REST + WS）

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-1a（可與 Phase 2-5 並行） |
| **Spec 對照** | §7.4 |
| **尺寸** | 中 |

**子項目**

- [ ] `GET /api/agent/traces/:sessionId`
- [ ] `GET /api/agent/traces/:sessionId/events/:eventId`
- [ ] `GET /api/agent/traces/:sessionId/state/:ref`
- [ ] WS tail endpoint
- [ ] 過濾：`source_kind / phase / outcome / decision_port_id / time-range`

**驗收**

- API contract 測試覆蓋
- 高壓下不阻塞主 arbitrator（trace store 分離）

---

### PR-6b — SPA Trace Viewer UI（flow graph + DAP inspector）

| 欄位 | 內容 |
|---|---|
| **狀態** | ⬜ |
| **依賴** | PR-6a |
| **Spec 對照** | §7.1-7.3、§8.6（startup_id 著色） |
| **尺寸** | 大（純 SPA） |

**子項目**

- [ ] Flow graph（§7.2 三層顯示）
- [ ] DecisionPort 子節點
- [ ] DAP-style inspector（`stopped` 事件 → 按需拉 scopes/variables）
- [ ] Filter UI（source_kind / phase / outcome / decision_port_id）
- [ ] Time-range selector
- [ ] `startup_id` 著色切換標記（daemon restart 視覺化）

**驗收**

- 大量 event 下 UI 不卡（virtualization）
- 能重現 v3 設計目標 #1-#5 的使用者場景

---

## 里程碑（§10.4 時序指引）

| 里程碑 | 條件 |
|---|---|
| **M0** | PR-0 merged → alpha bump（起點 alpha.199）|
| **M1** | Phase 1（PR-1a + PR-1b）merged → 至少一個 alpha bump 週期觀察 divergence log |
| **M2a** | PR-2a merged（仍 passthrough）|
| **M2b** | PR-2b merged（切 authoritative）→ 觀察一週後再送 PR-2c |
| **M2c** | PR-2c merged → legacy path 永久移除 |
| **M3** | Phase 3（3a + 3b）merged → AgentSpec 抽象落地 |
| **M4** | Phase 4（4a + 4b + 4c）merged → 三 agent 對稱拉平 |
| **M5** | Phase 5（5a0/5a1/5a2/5b）merged → 硬編完全拆除 |
| **M6** | Phase 6（6a + 6b）merged → trace viewer 可用 |

Phase 3 以後視團隊容量 / 使用者回饋節奏調整。

---

## 風險追蹤（§10.3）

| PR | 風險 | 緩解 | 狀態 |
|---|---|---|---|
| PR-2a | Handler 邊界重構 + frame schema 同 PR，易膨脹 | plan 層切 sub-diff（verify → observation → arbitrator）；compat adapter 暫存 | ⬜ 待 plan 層處理 |
| PR-2b | 所有 frame 寫入路徑改動 | PR-1b 雙寫留 divergence log；flag 可回退 | ⬜ |
| PR-2c | legacy path 刪除不可逆 | revert commit 備案；PR-2b 穩定 1 alpha 週期後再送 | ⬜ |
| PR-3a | hard-coded `cc` 呼叫點若無 adapter 會 nil panic | Legacy Compat Adapter（§4.6.1）；Registry panic 早期發現 | ⬜ |
| PR-4c | Schema 三層同步 migration | Alpha 階段可重置（`feedback_no_alpha_migration`）| ⬜ |
| PR-5a0/5a1/5a2 | 涉及 stream / session / agent 三 module | 拆三子 PR；每步 compat adapter 保護；e2e 串流測試覆蓋 | ⬜ |

---

## 開放問題 / 延後項目（§11）

| 主題 | 時程 | 追蹤 |
|---|---|---|
| Codex hook 缺 `agent_id / agent_type` | 上游補齊後開 `CanSubagent` | OpenAI codex issue #16226 |
| Sweep → Observation 整合 | PR-1b 完成 | — |
| `common.rainbow_text` 偵測規則 | PR-3b 規劃時定（可能 per-agent palette） | spike |
| Trace snapshot 歷史保留策略 | alpha 24h TTL；GA 前再議 | — |
| Trace DB schema 增量升級 | PR-1a 初版；alpha 可 breaking drop | — |
| `AGENT_ARB_MODE` 全域 console 介面 | GA 前（Settings → Dev 加 mode 切換）| future |
| Restart recovery pending loss 補償 | alpha 不補；GA 前視需要加 `hook_retry_on_startup` | future |
| Role Unknown 補檢查 probe | 未來 on-demand probe `role_rediscover` | future |
| DecisionPort 過量 sampling | cap 16 現可；日後觸頂再補 | future |
| Handler 同步/非同步邊界具體 diff | PR-2a plan 層決定 | plan 層 |

---

## 已落地基礎（spec 之前的現況）

> 來源：`project_lights_current_status.md`（2026-04-21）

- Probe 三層完整（Liveness / Activity / Readiness interface）
- CC + Codex probe 掛載（OpenCode 只掛 identifier）
- Stream handoff / CC operator / Activity watcher 呼叫點完整
- 黃燈救援路徑（`onActivityDetected` → `sweepOnce`）
- Agent identity + liveness convergence phases 1-7（PR #489 @ alpha.191）

這些作為 PR-1a 的 baseline，新 Arbitrator 將替換其中「誰寫 frame」的部分，但 probe 邏輯延用。

---

## 流程檢查單（每 PR 共通）

依 CLAUDE.md 專案流程：

1. [ ] plan 寫入 `docs/superpowers/plans/pr-<n>-<slug>.md`
2. [ ] codex 審 plan（`/codex:review --background`）
3. [ ] `EnterWorktree` 建隔離 worktree
4. [ ] 先寫測試（TDD）
5. [ ] 每 task 獨立 commit
6. [ ] PR open → 兩輪 codex review（標準 + 3-parallel 攻擊/防守/體質）
7. [ ] 問題彙整表（嚴重性信心 / 關聯度 / 複雜度）+ 高優先修 + 低優先開 issue
8. [ ] merge → `ExitWorktree`
9. [ ] 獨立 bump PR（VERSION + CHANGELOG）
10. [ ] 更新 main branch 對齊 origin/main

---

## 更新紀錄

| 日期 | 變更 |
|---|---|
| 2026-04-22 | 初版建立（對應 spec v3 封版；PR #548 pending merge） |
