# Phase 1 — Schema + 雙寫過渡

> Spec 對照：§3.3、§3.4、§3.5、§3.5.1、§8.1
> 依賴：Phase 0
> PR：PR-1a（schema）+ PR-1b（observation passthrough）

在不改變使用者可見行為的前提下，把新 trace schema 鋪到 SQLite，讓 hook / probe / sweep 在維持直寫 frame 的同時把同一事件轉成 `Observation` 送進 Arbitrator 的 passthrough 路徑。Arbitrator 此階段只計算 proposal、下投影到舊 schema 與 direct-write frame 比對，差異寫入 `frame_divergences`。這是 Phase 2 切換 writer 前的觀察視窗，上線後至少留一個 alpha bump 週期收集 divergence。

## 主架構

### 1. Trace schema 升級（§3.5）

- [ ] 測試：新欄位（`source_kind` / `action` / `reason_code` / `outcome` / `scenario_key` / `observed_generation` / `decision_ports[]`）寫入後可 query 回原值
- [ ] 測試：舊 consumer 讀取新 schema 不炸（unknown 欄位忽略）
- [ ] 測試：hook path 呼叫下新欄位全部填上非 null

### 2. `frame_divergences` 表（§8.1）

- [ ] 測試：insert/select roundtrip 正確
- [ ] 測試：高並發寫入無 deadlock / lost update
- [ ] DDL migration（alpha 階段可 drop-recreate）

### 3. `Observation` 型別（§3.3）

- [ ] 測試：`DecisionPort` cap 16 — 超過第 17 筆拒收不 panic
- [ ] 測試：`WatcherToken` 儲存 roundtrip
- [ ] 測試：必填欄位缺失時 Observation 建構器回 error（不 silent pass）

### 4. Arbitrator 骨架 + channel admission control（§3.4、§3.5.1）

- [ ] 測試：`arb_in` cap 1024 — proposed 滿載 drop non-blocking、committed 滿載 blocking 100ms timeout
- [ ] 測試：`retryCh` cap 256 滿載 drop retry tick 但 pending entry 仍在 buffer
- [ ] 測試：`traceOut` cap 4096 滿載按 drop priority 丟（committed > proposed > trace-only）
- [ ] 測試：Arbitrator 單 goroutine 擁有 pending buffer；三來源並發寫入無 race（go test `-race`）

### 5. 雙寫 passthrough + divergence 比對（§8.1）

- [ ] 測試：hook path 送進 Arbitrator 的 proposal 投影到舊 schema 後與 direct-write frame 逐欄等值（無 divergence）
- [ ] 測試：人為注入不一致 proposal → `frame_divergences` 有對應 row
- [ ] 測試：probe/sweep 雙寫 path 同樣覆蓋（三來源都能產生 Observation）

## 驗收

- [ ] Mini dev server 跑 ≥1 alpha bump 週期，divergence 表人工檢視在可接受閾值
- [ ] 使用者可見行為無變化（既有 frame UI 不動）
