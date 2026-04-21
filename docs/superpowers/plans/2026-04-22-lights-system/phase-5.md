# Phase 5 — 硬編拆除 + SPA 對齊

> Spec 對照：§2.1、§3.5.1、§6.6；`project_lights_current_status` 風險 #3 / #4 / #5
> 依賴：Phase 3 抽象層 + Phase 4 三 agent（建議）
> PR：PR-5a0（metadata + back-pressure）→ PR-5a1（stream orchestrator）→ PR-5a2（API capability 分派）→ PR-5b（SPA）

拆除 `cc.operator` / `CCSessionID` / `ccOps` 等 CC-only 硬編路徑，改走 spec-dispatched capability 查找。4 個子 PR 嚴格串行（5a0 DB metadata 中性化 → 5a1 stream → 5a2 API → 5b SPA），每步保留 compat adapter 直到本 phase 結束才整批移除。SPA 體質一次清完：顏色 SOT 分散 5+ 處整併、`AgentStatus` union 補 `clear`、§2.1 UI 投影規則與 UI drop 規則上線。Trace back-pressure policy 實作在 5a0 一併，避免高壓情境把 Arbitrator 拖垮。

## 主架構

### 1. Session metadata 中性化：`CCSessionID` → `ResumeToken`（§6.6）

- [ ] 測試：新 schema 下 resume token 讀寫 roundtrip
- [ ] 測試：alpha drop-recreate migration 後既有 CC session 可 resume
- [ ] 測試：`CCSessionID` symbol 在 codebase 無 reference（grep 驗證）

### 2. Trace back-pressure policy 實作（§3.5.1）

- [ ] 測試：高壓下 `traceOut` batching 100 筆 / 100ms 正確打包
- [ ] 測試：滿載按 drop priority 丟（committed > proposed > trace-only）
- [ ] 測試：sampling rate 觸發時可觀察 drop 比例
- [ ] 測試：back-pressure 啟動不阻塞 Arbitrator 主 loop

### 3. Stream orchestrator + API capability 分派（§6.6）

- [ ] 測試：stream handoff 走 `spec.Operator()` / `spec.StreamResumer()` 動態分派（三 agent 對稱）
- [ ] 測試：`/api/agent/status` 走 `spec.Statusline()`
- [ ] 測試：statusline installer 走 `spec.Descriptor().Capabilities` 分派
- [ ] 測試：PR-5a2 後 Legacy Compat Adapter 移除，無 dangling reference

### 4. SPA cc 硬編拆除（§2.1、風險 #3 / #4）

- [ ] 測試：`AgentStatus` union 含 `clear`（5 值全匹配）—— daemon 送 `clear` event 時 SPA 不炸
- [ ] 測試：icon list / detect list / metadata 從 registry 動態取得（不再有 `cc` 字串常數）
- [ ] 測試：顏色 SOT 整併 — `TabStatusIndicator` / `SessionStatusBadge` / `ActivityBarNarrow` / `SubagentDots` 共用一組 palette
- [ ] 測試：palette 分三層對齊（dot hex / badge tailwind / aggregate bar），不強制單一格式

### 5. UI 投影規則 + UI drop 規則（§2.1、風險 #5）

- [ ] 測試：主色類 observation 改主燈
- [ ] 測試：badge 類 observation 疊加 badge
- [ ] 測試：trace-only 類 observation 只進 trace viewer、不改 frame badge
- [ ] 測試：`SessionsSection` chip 4 色對齊（running 綠 / waiting 黃 / idle 灰 / clear 中性）

## 驗收

- [ ] 三 agent API 行為對稱（grep 無 `=== "cc"` 直接字串比對）
- [ ] `project_lights_current_status` 風險 #3 / #4 / #5 清除
- [ ] Compat adapter 全部移除（code 不留空殼）
