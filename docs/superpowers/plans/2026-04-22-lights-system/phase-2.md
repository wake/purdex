# Phase 2 — Arbitrator 切換

> Spec 對照：§3.2、§3.3、§3.4、§5.3、§5.4、§5.4.1、§5.5、§8.2、§8.3
> 依賴：Phase 1
> PR：PR-2a（multi-actor + retry/pending + handler 重構）+ PR-2b（切 authoritative）+ PR-2c（刪 legacy）

把 Frame 改為 multi-actor，Arbitrator 升為唯一 frame writer。三段式降風險：PR-2a 上新 schema + 仲裁規則但仍 passthrough；PR-2b 切 writer 為 authoritative，觀察 1 alpha 週期；PR-2c 刪 legacy direct-write path。最大的風險點是 handler 同步/非同步邊界重構 — spec 只定介面，diff 由本階段切成 verify → observation → arbitrator 三個 sub-PR 分別審。`AGENT_ARB_MODE` env 作為 kill switch，hot reload 延到下個 `hook.SessionStart` 生效，避免 mid-session 撕裂 frame。

## 主架構

### 1. Frame multi-actor + `ActorKey` 複合鍵（§3.2）

- [ ] 測試：同 frame 最多一個 `primary`（invariant 驗證）
- [ ] 測試：新 primary 取代舊 primary 前 Arbitrator 發 `SyntheticEndLifecycle` event 給舊 actor
- [ ] 測試：`ActorKey (SessionID, Generation, ActorID)` 不碰撞、可排序
- [ ] 測試：`Lifecycle.LastActivity` 於每次 observation 命中時更新

### 2. Pid tree role 判斷 + ancestor walk（§5.3、§5.5）

- [ ] 測試：pane_pid 對應 process 已消失 → ancestor walk 找到 agent 祖先 → role=primary
- [ ] 測試：pane_pid 完全找不到 agent 祖先 → drop proposal + trace only（**不建 unknown actor**）
- [ ] 測試：subagent ancestor 為另一 primary → role=subagent + parent 欄位指向正確 primary ActorKey
- [ ] 測試：§9.1 + §9.2 列出的 pid tree 覆蓋缺口全部補上（逐 fixture 驗證）

### 3. Retry / pending window + generation gate + idempotency（§3.4、§5.4）

- [ ] 測試：future-generation observation 被 reject（只有 `hook.SessionStart` 可推進 generation）
- [ ] 測試：pending window timeout → drop + trace，不建 actor
- [ ] 測試：同 idempotency key 重入只 apply 一次
- [ ] 測試：`retryCh` 滿載 drop retry tick 但 pending entry 仍在，下次 tick 重試
- [ ] 測試：Reconcile observation 只寫 trace、不進 apply pipeline

### 4. Handler 同步/非同步邊界重構（§5.4.1）

- [ ] 測試：`handleEvent()` 原有行為（verify / applyFrameEvent / watcher / broadcast）功能等價（既有 integration test 全綠）
- [ ] 測試：高並發 hook 事件不產生交叉 state（`-race` + load test）
- [ ] 測試：Arbitrator 單 goroutine 擁有 pending buffer + retry 排程（不與 handler goroutine 共享 mutable state）
- [ ] 實作時把 diff 切成 verify / observation / arbitrator 三小塊分別 commit，PR 內以 commit 序列呈現

### 5. Writer 切換 + legacy 移除（§8.2、§8.3）

- [ ] 測試：`AGENT_ARB_MODE=passthrough` 仍可回退（PR-2a/2b 階段保留）
- [ ] 測試：`AGENT_ARB_MODE=authoritative` 下 Arbitrator 是唯一 writer；hook/probe/sweep 直寫 path 不執行
- [ ] 測試：mode hot reload 延到下次 `hook.SessionStart` 才生效
- [ ] 測試：PR-2c 後 legacy direct-write 在 codebase 無 symbol reference（grep 驗證）
- [ ] 測試：`AGENT_ARB_MODE` env 文件化（§8.3）

## 驗收

- [ ] PR-2b 上線後 ≥1 alpha bump 週期無 frame race / regression（`feedback_codex_review_termination`）
- [ ] §9.1 + §9.2 pid tree 覆蓋測試全綠
- [ ] PR-2c 前可隨時 revert 回 passthrough
