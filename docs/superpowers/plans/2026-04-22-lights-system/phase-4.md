# Phase 4 — 三 Agent 對齊

> Spec 對照：§5.6、§6.1、§6.2、§6.3、§6.4、§9.5
> 依賴：Phase 3（PR-3b scheduler 已落地）；PR-4c 額外依賴 PR-2a 的 Frame schema
> PR：PR-4a（Codex）+ PR-4b（OpenCode）+ PR-4c（Subagent typed model）

把 Codex / OpenCode 在 capability matrix 上拉平到 CC — 現況 `project_lights_current_status` 記錄的 Codex readiness stub（風險 #1）、OpenCode 只註冊 identifier 不註冊 readiness、SPA 漏消費 OpenCode typed detail（風險 #7）在此一併收斂。PR-4a 與 PR-4b 可 parallel（獨立檔）；PR-4c 改 schema 影響前兩者，需先合（alpha 階段接受 drop-recreate migration，照 `feedback_no_alpha_migration`）。

## 主架構

### 1. Codex ProbePolicy + readiness 真實邏輯（§6.4）

- [ ] 測試：Codex CLI prompt marker 出現時 readiness `running → asking` 正確轉（參考 CC `CapturePaneContent` 模式）
- [ ] 測試：`HasReadiness` capability 從 Phase 0 的 placeholder false 翻 true
- [ ] 測試：黃燈救援路徑（`onActivityDetected` → `sweepOnce`）對 Codex session 生效
- [ ] 測試：Codex prompt marker 查證並寫入 fixture（非 `❯` — 需實測）

### 2. OpenCode ProbePolicy + readiness 補齊（§6.3）

- [ ] 測試：OpenCode pane content 判斷 readiness 三態（running / waiting / idle）正確
- [ ] 測試：OpenCode 於 registry 註冊 readiness provider（現況只有 identifier）
- [ ] 測試：`HasReadiness=true` 宣告

### 3. Subagent typed model `SubagentRef{id, type}`（§5.6、§2.2）

- [ ] 測試：`Frame.Actors` 攜帶 typed subagent ref（含 type 欄位）
- [ ] 測試：SPA DATA_FIELDS 可直接消費 typed subagent，不需從 detail 欄位自行解析
- [ ] 測試：OpenCode `detail.subagents` 升為一級欄位，既有 SPA 行為（subagent dots）無回歸
- [ ] DB schema migration（alpha drop-recreate）

### 4. 三 agent 對稱驗證（§6.1、§9.5）

- [ ] 測試：三 agent 各層 probe（liveness / activity / readiness）皆有實作（3×3 矩陣無 stub）
- [ ] 測試：三 agent capability bits 宣告完整，無 undefined
- [ ] 測試：同一 hook event 三 agent 產出 Observation 結構對稱（schema 一致）

## 驗收

- [ ] `project_lights_current_status` 風險 #1（Codex readiness stub）+ #7（SPA 漏消費 OpenCode detail）清除
- [ ] §6.2 Capability 對照表全部落地
- [ ] 三 agent 對稱 integration test 綠
