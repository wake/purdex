# Phase 3 — 抽象層重構

> Spec 對照：§3.6、§4.1、§4.2、§4.3、§4.4、§4.5、§4.6.1、§6.5
> 依賴：Phase 2
> PR：PR-3a（AgentSpec + Registry + Compat Adapter）+ PR-3b（ProbePolicy + Scheduler + common_probes）

把 AgentSpec 拆成五層（Descriptor + Provider + ProbePolicy + Optional services + Registry），讓三 agent 透過 capability bits 做能力協商，取代散落在 stream / handler / API 各處的硬編 `cc` / `codex` 判斷。最大風險是既有 hard-coded 呼叫點（`stream/module.go:39`、`handler.go:311/381/539`）在抽象落地瞬間可能 nil panic — `Legacy Provider Compat Adapter`（§4.6.1）作為過渡橋接，讓舊 path 繼續運作直到 Phase 5 逐步拆除。

## 主架構

### 1. AgentSpec 五層 + Capability bits 11 個（§3.6、§4.1）

- [ ] 測試：spec 宣告 `HasReadiness=false` 時，呼叫 readiness 走 no-op fallback 不 panic
- [ ] 測試：每個 agent 的 `Descriptor` / `Provider` / `ProbePolicy` 非 nil
- [ ] 測試：Optional services 缺席時 capability 查詢 false（graceful degrade）
- [ ] 測試：11 個 capability bit 對三 agent 宣告完整（含 placeholder）

### 2. Registry 顯式註冊 + 一致性驗證（§4.5）

- [ ] 測試：`Register(duplicate_id)` 回 error（不 panic）
- [ ] 測試：`NewDefaultRegistry` 載入三 agent；缺一個或 capability 不一致 panic
- [ ] 測試：未註冊 id 取 spec 回 nil + sentinel error
- [ ] 測試：`RegisterBuiltinAgents` 呼叫一次後重複呼叫是 no-op

### 3. Legacy Provider Compat Adapter（§4.6.1）

- [ ] 測試：舊 `AgentProvider` interface 呼叫 path 經 adapter 到新 spec，行為等值
- [ ] 測試：hard-coded `cc` 呼叫點（`stream/module.go`、`handler.go`）在三 agent 上行為無回歸
- [ ] 測試：adapter 作用期間 capability bits 可正確反向查詢（新 spec → 舊 provider 介面）

### 4. ProbePolicy + Scheduler + common_probes（§4.2、§4.3、§4.4）

- [ ] 測試：`ProbeBinding=OnDemand` 在請求當下執行一次
- [ ] 測試：`ProbeBinding=Continuous` 依排程 fan-out；停用後不 leak goroutine（`-race` + pprof）
- [ ] 測試：`motion` probe 對已知 pane diff 正確判定
- [ ] 測試：`rainbow_text` probe 對已知彩虹字 palette 正確判定（palette spike 結果落地）
- [ ] 測試：probe 錯誤隔離 — 單一 probe panic 不影響 scheduler 其他 probe

### 5. `self_detection` probe（disabled by default，§6.5）

- [ ] 測試：disabled 狀態下不排程、不耗資源
- [ ] 測試：enabled（future flag）對三 agent pane content 各自辨識成功
- [ ] 測試：capability bit 查詢反映目前 disabled 狀態

## 驗收

- [ ] 三 agent 皆經 registry 取得（無 `cc == "cc"` 直接字串比對）
- [ ] 既有 CC stream handoff / handler 行為無回歸
- [ ] Scheduler 無 goroutine leak
