# Phase 6 — Trace Viewer

> Spec 對照：§7.1、§7.2、§7.3、§7.4、§8.6
> 依賴：Phase 1（trace schema 已上）；純 SPA 的 PR-6b 依 PR-6a
> PR：PR-6a（Trace read API）+ PR-6b（SPA Trace viewer UI）

提供前端可視化的 trace viewer — DAP-style 流程圖 + DecisionPort 子節點 + 按需 inspector，讓 Purdex 的「為什麼這燈變綠」變成可追查的操作。PR-6a 從 Phase 1 合後就可並行於 2-5；PR-6b 純 SPA，等 API 穩定（至少 Phase 2 完成）再上，避免 trace schema 調整時 UI 重工。

## 主架構

### 1. Trace read API（REST，§7.4）

- [ ] 測試：`GET /api/agent/traces/:sessionId` 回 session 所有 events（分頁 cursor）
- [ ] 測試：`GET /api/agent/traces/:sessionId/events/:eventId` 含 `decision_ports[]` 全欄位
- [ ] 測試：`GET /api/agent/traces/:sessionId/state/:ref` on-demand 拉 scopes / variables
- [ ] 測試：未知 sessionId / eventId 回 404，不 panic

### 2. Trace WS tail（§7.4）

- [ ] 測試：WS 接上後收到 live event（live tail）
- [ ] 測試：高壓下 WS 走 batching 不阻塞主 Arbitrator
- [ ] 測試：斷線重連帶 `last_event_id` 續傳，不丟事件
- [ ] 測試：WS close 後 writer goroutine 不 leak

### 3. Flow graph UI（§7.2）

- [ ] 測試：三層顯示（source / decision / outcome）渲染正確
- [ ] 測試：DecisionPort 子節點可展開收合
- [ ] 測試：1000+ events virtualization 不卡（vitest 行為測試 + 人工 perf 驗證）
- [ ] 測試：node / edge click 事件正確 dispatch

### 4. DAP-style inspector + filter（§7.2、§7.3）

- [ ] 測試：node click → `stopped` → 按需拉 scopes / variables（lazy load）
- [ ] 測試：filter（source_kind / phase / outcome / decision_port_id）分別與複合條件正確過濾
- [ ] 測試：time-range selector 正確裁剪 event stream

### 5. `startup_id` 著色切換標記（§8.6）

- [ ] 測試：daemon restart 前後 `startup_id` 不同，viewer 顯示切換分界線
- [ ] 測試：同 `startup_id` event 共用顏色 band
- [ ] 測試：切換 marker 可被 filter 開關

## 驗收

- [ ] 大量 event（1000+）UI 不卡
- [ ] v3 使用者 5 點目標（§1.2）可透過 viewer 重現任一個場景
