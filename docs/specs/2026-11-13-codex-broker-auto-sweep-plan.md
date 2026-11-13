# Plan — codex broker 自動清理

Spec：`docs/specs/2026-11-13-codex-broker-auto-sweep-spec.md`。全部在 `internal/codexbroker/`，預估 3～5 檔、含測試 150～250 行，一張 PR。
TDD：每個 task 先寫會紅的測試再實作；每個 task 一個 commit。

## T1 — 抽出 sweep 迴圈＋auto 規則＋過期即停（`sweep.go`）

1. 把 `HandleSweep` 從 `resp := SweepResponse{...}` 到迴圈結束的部分抽成
   `func (h *SweepHandler) evaluate(ctx context.Context, brokers []BrokerRecord, mode, brokerKey string, auto bool) SweepResponse`；
   `HandleSweep` 改呼叫 `evaluate(ctx, brokers, mode, brokerKey, false)`，其餘（method、mode 驗證、ApplyDisabled、timeout、鎖、scan、writeJSON）不動。
2. kill 判斷 switch 在 `case brokerKey == "" && isForeign:` **之前**加：
   `case auto && isForeign && decision.OverrideE1: shouldKill = true; auditReason = "workspace_gone"`。
   `!decision.Kill` 那列維持在最前面（存活保護優先）。
3. 迴圈每輪開頭：`if ctx.Err() != nil` → 在 `resp.Errors` 加 `fmt.Sprintf("sweep deadline reached: %d brokers not evaluated", 剩餘數)` 後 `break`。
   剩餘數＝這輪起尚未處理、且符合 brokerKey 過濾的筆數。

測試（`sweep_test.go`，沿用 `newSweepHandlerForTest`、`fakeScanner`、`fakeKiller`、`stubDecisionEvaluator`；registry 用空的讓它是 foreign）：
- auto ∧ Kill ∧ foreign ∧ OverrideE1 → killer 被叫、key 在 `Applied`、`Evaluated[i].Decision.Reason == "workspace_gone"`。
- auto ∧ Kill ∧ foreign ∧ ¬OverrideE1 → 不殺。
- auto ∧ ¬Kill ∧ foreign ∧ OverrideE1 → 不殺。
- 手動 HTTP 未過濾 apply ∧ Kill ∧ foreign ∧ OverrideE1 → 不殺（守住舊行為）。
- ctx 已取消時呼叫 `evaluate`（2 筆）→ 不叫 EvalFn、不殺、`Errors` 含 `2 brokers not evaluated`。
- 既有 sweep 測試全部照舊通過。

## T2 — `AutoSweep`（`sweep.go`）

```go
// AutoSweep runs one unfiltered apply under the auto rule (spec §2.2). ran is false when apply is disabled.
func (h *SweepHandler) AutoSweep(ctx context.Context) (resp SweepResponse, ran bool, err error)
```

- `h.ApplyDisabled != ""` → `return SweepResponse{}, false, nil`（不掃描）。
- `lockWriteCtx(ctx, &h.globalApplyMu)`；失敗回 err；成功 `defer Unlock`。
- `h.ScanFn(ctx)` 失敗回 err。
- `return h.evaluate(ctx, brokers, "apply", "", true), true, nil`。

測試：
- ApplyDisabled 非空 → ran=false、ScanFn 未被呼叫。
- 正常 → ran=true、foreign＋Kill＋OverrideE1 的被殺。
- 全域鎖被占住（測試先拿寫鎖）＋短 ctx → 回 err、ScanFn 未被呼叫。

## T3 — 背景迴圈（`module.go`、`doc.go`）

- `Module` 加欄位 `autoEvery, autoBudget time.Duration`（零值用常數 `DefaultAutoSweepEvery = 10 * time.Minute`、
  `DefaultAutoSweepBudget = 3 * time.Minute`）與 `cancel context.CancelFunc`、`done chan struct{}`。
- `Start(ctx)`：`m.sweepHandler == nil` → 只印原本的 log、回 nil。否則照 `internal/module/notify/module.go` 的寫法：
  `context.WithCancel(ctx)`、goroutine 先跑一輪、再 `time.NewTicker(every)` 每輪跑；`defer close(done)`。
- 一輪＝`context.WithTimeout(runCtx, budget)` → `AutoSweep`；err 非 nil 印 `[codexbroker] auto sweep: %v`；
  `ran` 且 `Applied`／`Errors`／`Quarantined` 任一非空時印一行摘要（各自的數量與 key，killed 的附 reason）。
- `Stop`：有 cancel 就 cancel 並 `<-done`。
- `Start` 的 log 改成 `[codexbroker] inventory + sweep endpoint + auto sweep every 10m0s enabled`（用實際值）。
- `doc.go`：補一句 auto sweep 在 module.go、規則見 2026-11-13 spec。

測試（`module_test.go`）：
- `Module{sweepHandler: <fake scanner 計數>, autoEvery: 20ms, autoBudget: time.Second}`，Start 後在 1 秒內 scanner 被呼叫 ≥ 2 次；
  Stop 回來後再等 100ms 呼叫次數不再增加。
- `sweepHandler == nil` 時 Start／Stop 不 panic、不起 goroutine。

## 測試分層

- 開發中：`pdx test go-pkg ./internal/codexbroker/`。
- PR merge 前：`pdx test go-race ./internal/codexbroker/`（有 goroutine 與鎖）＋`pdx test lint`。不跑 `go test ./...`。
- 不新增 `exec.Command`，不需要 execstat guard。
