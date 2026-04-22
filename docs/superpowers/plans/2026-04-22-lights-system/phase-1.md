# Phase 1 — Schema + 雙寫過渡

> Spec 對照：§3.3、§3.4、§3.5、§3.5.1、§8.1
> 依賴：Phase 0
> PR：PR-1a（schema baseline）+ **PR-1b-0（schema 補完 + discriminator）** + **PR-1b-1（observation passthrough）**

在不改變使用者可見行為的前提下，把新 trace schema 鋪到 SQLite，讓 hook / probe / sweep 在維持直寫 frame 的同時把同一事件轉成 `Observation` 送進 Arbitrator 的 passthrough 路徑。Arbitrator 此階段只計算 proposal、下投影到舊 schema 與 direct-write frame 比對，差異寫入 `frame_divergences`。這是 Phase 2 切換 writer 前的觀察視窗，上線後至少留一個 alpha bump 週期收集 divergence。

## PR 拆分修訂（2026-04-22）

PR-1a merge 後 review 發現 §3.5 envelope 欄位仍缺 12 個（#560）、legacy vs Lights row 無區分契約（#561）。原 PR-1b 範圍（Observation + Arbitrator + divergence passthrough）需這兩個前置條件。**拆分**：

- **PR-1b-0**：envelope schema 補完（#560）+ row 類別 discriminator（#561）— 中等大小 schema PR
- **PR-1b-1**：Observation 型別 + Arbitrator 骨架 + channel admission + 雙寫 passthrough + divergence 比對 — 大型 PR（原 PR-1b 範圍）

## 主架構

### 1. Trace schema 升級（§3.5） — **PR-1a ✅ + PR-1b-0**

**PR-1a 已完成（10 欄位）**：
- [x] 測試：新欄位（`source_kind` / `action` / `reason_code` / `outcome` / `scenario_key` / `observed_generation` / `decision_ports` / `phase` / `status` / `watcher_token`）寫入後可 query 回原值
- [x] 測試：舊 consumer 讀取新 schema 不炸（zero-value roundtrip）
- [x] 測試：hook path 呼叫下新欄位全部填上非 null

**PR-1b-0 補完（11 欄位 + discriminator）**：
- [ ] 測試：新欄位 `trace_id` / `reason_text` / `attrs` / `input_refs` / `output_refs` / `state_before_ref` / `state_after_ref` / `evidence_refs` / `started_at` / `ended_at` / `otel_kind` 寫入後 query 回原值
- [ ] 測試：`attrs` JSON object / `*_refs` JSON array 解析正確
- [ ] 測試：`trace_id` uuid 格式 roundtrip
- [ ] 測試：hook path 下 11 個新欄位皆填上合理值
- [ ] 測試：Legacy row（`source_kind=""`）zero-value roundtrip 仍合法（相容性）
- [ ] 測試：Lights row（`source_kind!=""`）若 `phase` / `outcome` / `action` / `trace_id` 任一為空 → `SaveChain` 回 error（row class discriminator + validation）
- [ ] 測試：Legacy row 與 Lights row 混合 in 同 chain 仍可 roundtrip

**Schema 欄位對照**（完成後）：

| spec §3.5 欄位 | 實作位置 | PR |
|---|---|---|
| `trace_id` | `agent_trace_steps.trace_id` (新) | PR-1b-0 |
| `session_id` | `agent_trace_chains.tmux_session` + `pane_id`（既有 composite）| PR-1a |
| `event_id` | `agent_trace_steps.step_id`（既有，uuid 全域唯一）| PR-1a |
| `span_id / parent_span_id` | `step_id / parent_step_id`（既有）| PR-1a |
| `name` | `event_name`（既有，裸事件名；PR-1b-1 擴充填法為 `hook.post.<Event>`）| PR-1a |
| `kind` (OTel) | `otel_kind` 新欄（避開現有 `kind` 的 step_kind 語意）| PR-1b-0 |
| `source_kind` | `source_kind` | PR-1a |
| `watcher_token` | `watcher_token` | PR-1a |
| `phase` | `phase` | PR-1a |
| `status` | `status` | PR-1a |
| `outcome` | `outcome` | PR-1a |
| `action` | `action` | PR-1a |
| `reason_code` | `reason_code` | PR-1a |
| `reason_text` | `reason_text` (新) | PR-1b-0 |
| `decision_ports` | `decision_ports` | PR-1a |
| `scenario_key` | `scenario_key` | PR-1a |
| `input_refs` | `input_refs` (新, JSON array) | PR-1b-0 |
| `output_refs` | `output_refs` (新, JSON array) | PR-1b-0 |
| `state_before_ref` | `state_before_ref` (新) | PR-1b-0 |
| `state_after_ref` | `state_after_ref` (新) | PR-1b-0 |
| `evidence_refs` | `evidence_refs` (新, JSON array) | PR-1b-0 |
| `attrs` | `attrs` (新, JSON object) | PR-1b-0 |
| `observed_generation` | `observed_generation` | PR-1a |
| `started_at` | `started_at` (新, nano epoch) | PR-1b-0 |
| `ended_at` | `ended_at` (新, nano epoch) | PR-1b-0 |
| `seq` | `seq`（既有）| PR-1a |

**Row class discriminator（#561 選項 B + validation）**：
- **Legacy row**: `source_kind == ""`（pre-PR-1a SaveChain 寫入的 row）— 允許所有 Lights 欄位 zero-value
- **Lights row**: `source_kind != ""`（PR-1b-0 起 Arbitrator / hook collector 寫入）— required 欄位 `source_kind` / `phase` / `outcome` / `action` / `trace_id` 全部非空；SaveChain normalize 時 validate，缺欄位回 error
- **不加 `schema_version` 欄位**：`source_kind` 本身即 discriminator，避免重複
- PR-1a 時期 `TestTraceStore_ZeroValueLightsFieldsRoundTrip` 測試改名為 `TestTraceStore_LegacyRowZeroValueEnvelopeRoundtrip`，明示「legacy row 才允許 zero-value」

### 2. `frame_divergences` 表（§8.1） — **PR-1a ✅**

- [x] 測試：insert/select roundtrip 正確
- [x] 測試：高並發寫入無 deadlock / lost update
- [x] 測試：idempotency key（session_id, trace_id, event_id, observed_generation）DO NOTHING on duplicate
- [x] DDL migration（alpha 階段可 drop-recreate）
- [x] BLOB → TEXT + `json.RawMessage`（避免 base64 外流）

### 3. `Observation` 型別（§3.3） — **PR-1b-1**

- [ ] 測試：`DecisionPort` cap 16 — 超過第 17 筆拒收不 panic（dev panic / prod log+truncate）
- [ ] 測試：`WatcherToken` 儲存 roundtrip（uuid rotation 下老 token reject）
- [ ] 測試：必填欄位缺失時 Observation 建構器回 error（不 silent pass）
- [ ] 測試：`SourceKind` enum 值域合法性（hook|probe|sweep|reconcile|synthetic）
- [ ] 測試：`StateProposal.ActorKey` composite key（SessionID, Generation, ActorID）roundtrip

### 4. Arbitrator 骨架 + channel admission control（§3.4、§3.5.1） — **PR-1b-1**

- [ ] 測試：`arb_in` cap 1024 — proposed 滿載 drop non-blocking、committed 滿載 blocking 100ms timeout
- [ ] 測試：`retryCh` cap 256 滿載 drop retry tick 但 pending entry 仍在 buffer
- [ ] 測試：`traceOut` cap 4096 滿載按 drop priority 丟（committed > proposed > trace-only）
- [ ] 測試：Arbitrator 單 goroutine 擁有 pending buffer；三來源並發寫入無 race（go test `-race`）
- [ ] 測試：Apply pipeline 9 步驟（generation gate / watcher / idempotency / pending / source priority / monotone lifecycle / invariant / mode branch / trace）各自覆蓋
- [ ] 測試：Generation gate — `< frame.generation` reject `StaleGeneration`；`> frame.generation` 僅 `hook.SessionStart` 可推進，其他 reject `UnauthorizedGenerationBump`
- [ ] 測試：Pending window — per-session 8 entries evict、per-observation coalescing 16 筆 drop oldest、2s deadline drop proposal 不建 actor
- [ ] 測試：Reconcile loop — 不改 actor.status，只進 trace（`outcome=skipped, reason_code=ReconcileStaleNoted`）

### 5. 雙寫 passthrough + divergence 比對（§8.1） — **PR-1b-1**

- [ ] 測試：hook path 送進 Arbitrator 的 proposal 投影到舊 schema 後與 direct-write frame 逐欄等值（無 divergence）
- [ ] 測試：人為注入不一致 proposal → `frame_divergences` 有對應 row
- [ ] 測試：probe/sweep 雙寫 path 同樣覆蓋（三來源都能產生 Observation）
- [ ] 測試：`AGENT_ARB_MODE=passthrough`（PR-1b-1 預設）Arbitrator 不寫 frame

## 驗收

- [ ] Mini dev server 跑 ≥1 alpha bump 週期，divergence 表人工檢視在可接受閾值
- [ ] 使用者可見行為無變化（既有 frame UI 不動）
