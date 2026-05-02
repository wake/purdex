# W6-6 codex permission-reply ScreenChange ProbeIntent — Implementation Plan

> **Status**：v1 final（codex plan review 0 finding；thread `019de532-08a2-73f3-9f03-fc7d4c49aa28` 2026-05-02）
> **依賴 spec**：`docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md` v6.1 final（7 輪 review，round 7 0 finding）
> **Worktree**：`.claude/worktrees/lights-w6-6-codex-screen-change` / branch `worktree-lights-w6-6-codex-screen-change`
> **Base**：`origin/main` @ alpha.281（J3 PR #797 `56b3ba55` + bump #798 `5736f87e` 之後）
> **拆分**：Phase 1（interface + provider + detector）→ Phase 2（module wiring + drift gate）→ Phase 3（integration + race + mlab）→ PR

---

## 0. Plan 總覽

### 0.1 範圍重申（per spec §1.1）

10 task 分三 phase（spec §6 phase 拆分 1:1 transcribe）：

- **Phase 1**：5 task — `ProbeIntentKindScreenChange` const + codex `StartScreenChangeDetector`（v6.1 — `armed` + `emitted` + mutex-protected `closed bool` + 雙重 `isCodexAlive` check）+ `Provider.ProbeIntents()` 第二筆 + `onScreenChange` mapper + 單元 test 表驅動 11 case
- **Phase 2**：3 task — `module.go` startDetector switch case + supportedKinds entry + drift test 擴 + codex wire test 擴
- **Phase 3**：2 task — integration test 8 case（lifecycle + truth table + identity race + retry）+ mlab live verify §1-§6（含 A9/A10 ship gate）

**規模**：~290 行 production / ~330 行 test / 預估 PR diff ~620 行（per kickoff）。

### 0.2 估計

- Production code：~290 行
- Test code：~330 行（unit 11 case + drift 1 fixture + wire 1 case + integration 8 case）
- 預估 PR diff：~620 行
- 預估時間：4-6 小時 subagent 工作（不含 mlab live verify）+ 0.5-1 小時 mlab live verify（user 操作；per kickoff §6 ship gate 不影響開發路徑，可放最後）

### 0.3 不可越界（per `feedback_phase_skip_threshold`）

W6-6 PR **只動 codex provider + module agent**：

- ❌ 不動 W6-3 既有 ProcessDead detector（spec §9）
- ❌ 不動 `Provider.ProbeIntents()` 第一筆 entry / `Signal` struct / `ProbeIntent` struct
- ❌ 不動 J3 dispatcher（reject race 由 J3 generic pre-grace cover；W6-6 detector 仍是 dumb emit；spec §0.5 + §8 anchor）
- ❌ 不動 `IsAliveFor` PanePID vs ActivePanePID 一致性（pre-existing infra；用 `FirstAliveAgentInTree` 規避；開 follow-up issue）
- ❌ 不擴 `ProbeIntent` / `Signal` struct 多欄位（spec §8 drift signal）
- ❌ 不引入 sustained-change counter / glyph 比對 / always-on framework（spec §9 + fix-spec §3）
- ❌ 不為 ScreenChange 特化 pre-grace timer（spec §8 drift signal — 與 W6-3 §9.14 anchor 對齊）
- ❌ 不 generalize 為跨 agent ScreenChangeProfile（fix-spec §3 撤回）
- ❌ 不動 SPA 端（status 切換 daemon 內部事；SPA 已認 running）

### 0.4 鎖序與不變式（per spec §2.1）

實作 detector 必持守：

- callback 不持 m.mu（dispatcher 鎖序）
- detector 內部用 `sync.Mutex mu` + `closed bool`（mutex-protected）+ `armed atomic.Bool` + `emitted atomic.Bool` + `emittedCh chan struct{}`
- mutex 鎖序保證：cb 進 mutex 第一 check `if closed { return }`；main goroutine 在 return 前 mutex 鎖內 set `closed=true`；wrap goroutine `close(out)` 與 cb send 互斥（spec §4.3 完整解釋）
- `armed.Store(true)` monotonic（多次 ScreenStable idempotent）
- emit 前 mutex 內 double-check `isCodexAlive()`（修 R3 F2 set→emit race）
- `emitted.Store(true)` 必在 select case `out<-sig` 成功後（ctx 贏不 store；可 retry）

---

## 1. Phase 1：Interface + provider + detector（單元層）

### P1-T1 — `ProbeIntentKindScreenChange` const（spec §3）

**目標**：`internal/agent/provider.go` 加 1 行 `ProbeIntentKind` 常數，**不改 struct**。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/provider.go` | 加 `ProbeIntentKindScreenChange ProbeIntentKind = "screen_change"` const + GoDoc 註解（W6-6 用途、PaneAlive=true contract） |

**Test**：

無新 test（const 僅是 string；不需 unit）。`TestProbeIntent_StructFields` 等既有 W6-3 test 自然 cover。

**Acceptance**：`go build ./...` 全綠。

**估計**：~5 行 production / 0 行 test。

**依賴**：none（最先做）。

---

### P1-T2 — `StartScreenChangeDetector`（spec §4.3）

**目標**：新檔 `internal/agent/codex/probe_intent_screen_change.go`，落 v6.1 detector：2-phase + 2-case truth table + close-race fix。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/codex/probe_intent_screen_change.go` | 新檔；`screenWatcher` interface（Watch / StopWatch）+ `screenChangeTopLines` const = 10 + `StartScreenChangeDetector(ctx, prober, isCodexAlive, paneID, senderPID, out)`；內部 `armed atomic.Bool` + `emitted atomic.Bool` + `sync.Mutex` + `closed bool`（mutex-protected）+ `emittedCh chan struct{}`；main goroutine return 前 mutex 鎖內 set `closed=true`（v6.1 round 6 P1 close-race fix） |

**實作對齊 spec §4.3 程式碼草稿**（直接 transcribe；不可改 signature、不可改鎖序）：

- callback `ScreenStable` → `armed.Store(true)`（idempotent）
- callback `ScreenChanged` 4-step gate：
  1. `if !armed.Load() { return }`
  2. `if !isCodexAlive() { return }`（first check，short-circuit dialog noise）
  3. `mu.Lock(); defer mu.Unlock()`
  4. `if closed { return }`（v6.1 round 6 P1 fix — 鎖序保證 wrap close(out) 與 cb send 互斥）
  5. `if emitted.Load() { return }`（idempotent — 另一個 callback 已 emit）
  6. `if !isCodexAlive() { return }`（second check inside mutex — 修 R3 F2 set→emit race）
  7. `select case out<-sig: emitted.Store(true); close(emittedCh); case <-ctx.Done():`（ctx 贏 emitted 仍 false 可 retry）
- main goroutine：`prober.Watch(paneID, WatchOptions{TopLines: 10}, cb)` → `select <-emittedCh | <-ctx.Done()` → `prober.StopWatch(paneID)` → `mu.Lock(); closed = true; mu.Unlock()` → return

**GoDoc 必含 v6.1 anchor 註解**（spec §4.3 已寫好；transcribe 進來防 drift）：

- "Why atomic.Bool + sync.Mutex instead of sync.Once"（v4 round 4 F1 retired）
- "Why two isCodexAlive checks"（v3 round 3 F2）
- "Why FirstAliveAgentInTree"（v3 round 3 F3 — 讀 spec §4.3 line 430-441）
- "Why mutex-protected closed bool"（v6.1 round 6 P1 — 讀 spec §4.3 line 443-469）
- "Why atomic.Bool insufficient for closed"（v6.1 round 6 P1 — 同上）

**Test**：見 P1-T3。

**Acceptance**：

- `go build ./internal/agent/codex/...` 全綠
- import path 正確（`internal/agent` + `internal/agent/probe`）

**估計**：~80 行 production（含 GoDoc anchor 註解）/ 0 行 test 本身（test 在 P1-T3）。

**依賴**：P1-T1（用 `ProbeIntentKindScreenChange`）。

---

### P1-T3 — Detector unit tests（spec §6.1 P1-T3 11 case）

**目標**：表驅動 unit test 覆蓋 v6.1 truth table + close-race fix。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/codex/probe_intent_screen_change_test.go` | 新檔；fake `screenWatcher` 注入 callback；fake `isCodexAlive` 可程式控制 |

**Fake screenWatcher 設計**：

- `fakeWatcher` struct 持有 `cb probe.ScreenChangeCallback`、`stopped bool`、`mu sync.Mutex`
- `Watch(target, opts, cb)` 把 cb 存起來；test 用 `fw.fire(ev)` 主動驅動 callback
- `StopWatch(target)` 設 stopped=true（用於 verify teardown）
- 不真跑 prober 內部 ticker（unit 隔離）

**11 case 全列**（per spec §6.1 P1-T3）：

| # | Test 名 | 場景 | 驗證 |
|---|---|---|---|
| 1 | `TestStartScreenChangeDetector_Case1Happy_EmitsOnce` | `ScreenStable` → `ScreenChanged` + alive=true | out 收到 1 個 Signal（Kind=ScreenChange / PaneAlive=true / PaneID/SenderPID 對齊）；emittedCh closed |
| 2 | `TestStartScreenChangeDetector_Case2NoStable_AllDrop` | armed=false 期間 `ScreenChanged` 連 5 次 | out 0 signal；emittedCh 未 close；emitted=false |
| 3 | `TestStartScreenChangeDetector_Case1IdentityFalse_Drop` | `ScreenStable` → `ScreenChanged` + alive=false | out 0 signal；emitted 仍 false（可 retry） |
| 4 | `TestStartScreenChangeDetector_Case1IdentityRace_DropAtSecondCheck` | first check true、mutex 內 second false | out 0 signal；emitted 仍 false |
| 5 | `TestStartScreenChangeDetector_Case1MultipleChanges_EmitsOnlyOnce` | Phase B 多次 `ScreenChanged` + alive=true | out 收到恰好 1 個 Signal（mutex 內 emitted check 生效） |
| 6 | `TestStartScreenChangeDetector_ScreenStableIdempotent` | 多次 `ScreenStable` | armed 持續 true；無副作用；後續 `ScreenChanged` 仍正常 emit |
| 7 | `TestStartScreenChangeDetector_RetryAfterTransientFalse_EmitsOnRecovery` | alive=false drop → alive 恢復 true → `ScreenChanged` | 第二次 `ScreenChanged` 成功 emit（驗證 v5 修 round 4 F1，sync.Once 不適用） |
| 8 | `TestStartScreenChangeDetector_CtxCancelBeforeStable_TeardownClean` | ctx cancel 在任何 ScreenStable 之前 | main goroutine `<-ctx.Done()` → StopWatch + closed=true + return；fakeWatcher.stopped=true |
| 9 | `TestStartScreenChangeDetector_CtxCancelDuringEmit_NoStore` | mock select case `<-ctx.Done()` 贏 | emitted 不 store；emittedCh 不 close（用 unbuffered out + ctx 即將 cancel + busy waiter 強制觸發 ctx 分支） |
| 10 | `TestStartScreenChangeDetector_OtherKindIgnored` | fire `probe.ScreenChangeKind` 非 Stable / Changed 的 Kind | out 0 signal；無狀態變化 |
| 11 | `TestStartScreenChangeDetector_NoCloseRace` | cb 進 mutex 走到 closed flag check 之前；main goroutine ctx cancel + StopWatch + 等 mutex；cb 完成 send（out 未 close）→ 退出 mutex；main 拿 mutex set closed → return；wrap goroutine close(out) 之後 cb 再次 fire 進 mutex 看 closed=true 直接 return | go test -race 全綠（無 panic on closed channel）；最後 fire 的 cb 沒有送進 out |

**Test 11 (NoCloseRace) 細節**：

- 用 `chan struct{}` 同步 cb 進 mutex 的時點與 main goroutine ctx cancel 的時點
- mock fakeWatcher 把 callback 序列化（用 mutex 確保 callback 順序）
- 主 goroutine 在 detector return 後手動 close(out)（模擬 dispatcher wrap goroutine）
- 最後驗 cb 再次 fire 不 panic

**Acceptance**：

- `go test ./internal/agent/codex/... -run TestStartScreenChangeDetector` 全綠
- `go test -race ./internal/agent/codex/... -run TestStartScreenChangeDetector` 全綠
- 11 case 全部存在且名稱對齊上表

**估計**：~180 行 test（含 fakeWatcher helper ~30 行 + 11 case 各 ~12 行）。

**依賴**：P1-T2（detector 已落地）。

---

### P1-T4 — `Provider.ProbeIntents()` 第二筆 + `onScreenChange` mapper（spec §4.1 + §4.2）

**目標**：codex provider `ProbeIntents()` 加第二筆 entry + 新 `onScreenChange` mapper（與 W6-3 `onProcessDead` 對稱）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/codex/provider.go` | `ProbeIntents()` return slice 加第二筆 `{Kind: ProbeIntentKindScreenChange, OnEntryStatus: []agent.Status{agent.StatusWaiting}, OnSignal: onScreenChange}`；新 file-level function `onScreenChange(sig agent.Signal) agent.Status`：`if sig.Kind != ProbeIntentKindScreenChange { return "" }; return StatusRunning` |

**實作對齊 spec §4.1 + §4.2 程式碼草稿**（直接 transcribe）：

- ProbeIntents 第二筆 `OnEntryStatus = {StatusWaiting}`（不重疊 ProcessDead 的 {Running, Waiting}；spec §4.1 已論證為什麼不含 Running）
- `onScreenChange` mirror W6-3 `onProcessDead` 防禦 dispatcher misuse：非 ScreenChange Kind → return ""
- 不檢 `PaneAlive`（spec §4.2 已論證為 dead code 噪音）

**Test**：見 P1-T5。

**Acceptance**：`go build ./internal/agent/codex/...` 全綠。

**估計**：~25 行 production / 0 行 test 本身。

**依賴**：P1-T1（Kind const）+ P1-T2（detector 在；雖然 provider 不直接呼叫 detector，但概念連動）。

---

### P1-T5 — Provider unit tests 擴 fixture（spec §6.1 P1-T5）

**目標**：`provider_test.go` 既有 fixture 加 ScreenChange 對齊 case。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/codex/provider_test.go` | 擴 ProbeIntents 長度 assertion 從 1 → 2；第二筆 Kind/OnEntryStatus/OnSignal 對齊；onScreenChange 三 case test |

**3 case**（per spec §6.1 P1-T5）：

| # | Test 名 | 場景 | 驗證 |
|---|---|---|---|
| 1 | `TestOnScreenChange_MatchKind_ReturnsRunning` | sig.Kind = ProbeIntentKindScreenChange | return StatusRunning |
| 2 | `TestOnScreenChange_NonScreenChangeKind_ReturnsEmpty` | sig.Kind = ProbeIntentKindProcessDead | return "" |
| 3 | `TestOnScreenChange_NilOrZeroKind_ReturnsEmpty` | sig.Kind = "" zero value | return "" |

**ProbeIntents fixture 擴**：

- `TestProvider_ProbeIntents_HasScreenChangeAsSecondEntry`：len = 2；第二筆 `Kind=ProbeIntentKindScreenChange / OnEntryStatus={StatusWaiting} / OnSignal != nil`

**Acceptance**：

- `go test ./internal/agent/codex/... -run "TestOnScreenChange|TestProvider_ProbeIntents"` 全綠
- 既有 W6-3 ProbeIntents test 不破

**估計**：~40 行 test。

**依賴**：P1-T4（provider.go ProbeIntents 第二筆 entry 在）。

---

### Phase 1 完成檢核

- [ ] P1-T1 const ✅
- [ ] P1-T2 detector ✅（spec §4.3 1:1 transcribe + GoDoc anchor）
- [ ] P1-T3 11 unit case 全綠（含 race 檢測）
- [ ] P1-T4 provider 第二筆 + onScreenChange ✅
- [ ] P1-T5 3 case 全綠
- [ ] `go test -race ./internal/agent/codex/...` 全綠
- [ ] `go build ./...` 全綠

---

## 2. Phase 2：Module wiring + drift gate

### P2-T1 — `module.go` startDetector switch + supportedKinds（spec §5.1）

**目標**：`internal/module/agent/module.go` 在 `m.probeIntentDisp.startDetector` switch 加 ScreenChange case；`supportedKinds` map 加 entry。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/module.go` | startDetector switch 加 `case agentpkg.ProbeIntentKindScreenChange:` block：定義 `isCodexAlive` closure 用 `mod.prober.FirstAliveAgentInTree(paneID)`（return err==nil && t == "codex"）→ 呼 `codex.StartScreenChangeDetector(ctx, mod.prober, isCodexAlive, paneID, senderPID, out)`；supportedKinds map 加 `agentpkg.ProbeIntentKindScreenChange: {}` entry |

**實作對齊 spec §5.1 程式碼草稿**（直接 transcribe）：

- switch case 與 ProcessDead 對稱
- closure 內 comment：why FirstAliveAgentInTree（內部用 ActivePanePID 對 paneID %N exact resolve；vs IsAliveFor 用 PanePID first-pane-only）
- supportedKinds 兩 entry：ProcessDead + ScreenChange

**Test**：見 P2-T2 + P2-T3。

**Acceptance**：`go build ./internal/module/agent/...` 全綠。

**估計**：~20 行 production / 0 行 test 本身。

**依賴**：P1-T2（detector 在）+ P1-T4（provider 第二筆 entry 在）。

---

### P2-T2 — Drift test 擴 fixture（spec §6.2 P2-T2）

**目標**：`probe_intent_dispatcher_drift_test.go` 既有 W6-3 drift fixture 擴：switch case ↔ supportedKinds map ↔ codex provider ProbeIntents() Kind 集合三邊相等。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_drift_test.go` | declaredKinds / supportedKinds / startDetector switch 三邊 Kind 集合用 `assertSetsEqual` 驗 = `{ProcessDead, ScreenChange}` |

**Drift test 必須抓到的故意改動**：

- 撤掉 supportedKinds entry 但 switch case 還在 → drift test fail
- 撤掉 switch case 但 supportedKinds entry 還在 → drift test fail
- ProbeIntents 加第三筆 entry 但 switch / supportedKinds 沒擴 → drift test fail

**Acceptance**：

- `go test ./internal/module/agent/... -run TestProbeIntentDispatcher_DriftGate` 全綠
- 故意註解 supportedKinds ScreenChange entry → drift test fail（手動驗 1 次留 commit message 記錄）

**估計**：~15 行 test。

**依賴**：P2-T1（switch + supportedKinds 在）。

---

### P2-T3 — Codex wire test 擴 ScreenChange path（spec §6.2 P2-T3）

**目標**：`probe_intent_dispatcher_codex_wire_test.go` 加 ScreenChange wire test：fake prober 注入 stable → change → 驗 status waiting → running + active intent teardown。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_codex_wire_test.go` | 加 `TestDispatcher_CodexScreenChangeWire_WaitingToRunning` 1 case；用既有 fakeProber（W6-3 引入）or 擴 fakeProber 支援 Watch / StopWatch fire；模擬 PdxPermissionRequest hook → status=waiting → fire ScreenStable → fire ScreenChanged → 驗 status=running + ScreenChange intent teardown（dispatcher 5-case lifecycle entry exit） |

**fakeProber 擴**（如既有不支援 ScreenChange）：

- 既有 fakeProber 已有 `IsProcessAliveFor` / `FirstAliveAgentInTree`；W6-6 額外要 `Watch` / `StopWatch`
- 加 `WatchCalls map[string]probe.ScreenChangeCallback` + `StopWatchCalls []string`
- `fire(target, ev)` helper 主動驅動 callback
- 確認既有 W6-3 wire test 無 regression（fakeProber struct 擴不破舊 case）

**Test**：

- `TestDispatcher_CodexScreenChangeWire_WaitingToRunning`：
  1. setup module + dispatcher + fakeProber + codex provider
  2. fire PdxPermissionRequest → status=waiting → dispatcher arm 兩個 intent（ProcessDead + ScreenChange）
  3. assert fakeProber.WatchCalls[paneID] != nil（ScreenChange detector 已 wire）
  4. fire ScreenStable → armed=true（不可觀察）
  5. fire ScreenChanged → out 收到 Signal → applyStatus(running)
  6. assert currentStatus = running
  7. assert fakeProber.StopWatchCalls 含 paneID（5-case OnEntryStatus 退出 → teardown）

**Acceptance**：

- `go test ./internal/module/agent/... -run TestDispatcher_CodexScreenChangeWire` 全綠
- 既有 W6-3 wire test 全綠

**估計**：~60 行 test（含 fakeProber 擴 ~20 行）。

**依賴**：P2-T1（switch wire 在）。

---

### Phase 2 完成檢核

- [ ] P2-T1 switch + supportedKinds ✅
- [ ] P2-T2 drift fixture ✅（含手動 break 一次驗證 fail）
- [ ] P2-T3 wire test ✅
- [ ] `go test ./internal/module/agent/...` 全綠
- [ ] `go test -race ./internal/module/agent/...` 全綠
- [ ] `go build ./...` 全綠

---

## 3. Phase 3：Integration + race + mlab

### P3-T1 — Integration test 端到端 lifecycle（spec §6.3 P3-T1）

**目標**：`probe_intent_dispatcher_integration_test.go` 擴 8 case 端到端覆蓋 v5 truth table + identity race + retry。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_integration_test.go` | 擴既有 W6-3 integration test 檔（如已存）or 新檔；fake prober 支援同時 fire ScreenStable + ScreenChanged 序列；fake clock 支援 9 秒虛擬時間（dialog render 模擬）；8 case |

**8 case 全列**（per spec §6.3 P3-T1）：

| # | Test 名 | 場景 | 驗證 |
|---|---|---|---|
| a | `TestIntegration_Case1Happy` | waiting hook → arm intent → fake ScreenStable → ScreenChanged + alive=true | emit → status=running + ScreenChange entry teardown |
| b | `TestIntegration_LongDialogNaturalCover` | waiting → 連發 ScreenChanged 模擬 dialog 渲染 → drop（armed=false）→ ScreenStable → 9 秒虛擬時間後 ScreenChanged → emit → running ✓ | 不依賴 grace gate（detector v5 移除時間判斷） |
| c | `TestIntegration_Case2FastWithOutput` | waiting → 連發 ScreenChanged（無 ScreenStable）→ idle hook（PdxStop）| no emit + status=idle（PdxStop hook cover；by-contract case 2，跳過 running phase） |
| d | `TestIntegration_WaitingToIdle_TeardownNoEmit` | waiting hook → arm intent → idle hook（不經過 ScreenStable）| no emit；ScreenChange entry teardown |
| e | `TestIntegration_CrossProviderSwitchReconcileTeardown` | codex waiting → switch active provider → reconcile teardown | ScreenChange watcher StopWatch 被呼 |
| f | `TestIntegration_Case1IdentityFalse` | fake isCodexAlive=false 全程 | drop；emitted 仍 false；無 status 變化 |
| g | `TestIntegration_Case1IdentityRace` | 注入 isCodexAlive 第一次 true、mutex 內 second false | 不 emit；emitted 仍 false |
| h | `TestIntegration_RetryAfterTransientFalse` | alive=false drop → alive 恢復 true → 後續 ScreenChanged → 成功 emit | 驗 v5 修 round 4 F1（sync.Once 永久熔斷不會發生） |

**Fake prober 設計確認**（per spec §11 #6 open question）：

- 既有 W6-3 wire test fake prober 是否能 fire ScreenStable + ScreenChanged 序列？
- 若否，本 task 重寫 fake；spec line 745 已標 "若重寫，需在 plan 階段標 task 規模"
- 估計 fake 擴 ~50 行（加 `WatchCalls` map + `fire(target, ev)` helper + clock seam）

**Acceptance**：

- `go test ./internal/module/agent/... -run TestIntegration_` 全綠
- `go test -race ./internal/module/agent/...` 全綠
- 8 case 全部存在且名稱對齊上表

**估計**：~150 行 test（含 fake 擴 ~50 行 + 8 case 各 ~12 行）。

**依賴**：P2-T1 + P2-T3（fake prober + dispatcher wire 在）。

---

### P3-T2 — mlab live verify（spec §6.3 P3-T2 / kickoff §6 ship gate）

**目標**：mlab 真機跑 6 場景驗 production behavior；A9/A10 為 ship gate。

**🚨 此 task 不派 subagent；user 操作；可放 PR review 收斂後再跑**（per kickoff §6）。

**6 場景**：

| # | 場景 | 預期 | 證據 |
|---|---|---|---|
| §1 | approval reply（case 1 happy path）：codex 進 waiting → 用 enter 批准 permission dialog | dev log 5-step chain `[hook] PdxPermissionRequest` → `[derive] waiting` → `[handler]` → `[probe-intent] start kind=screen_change` → `[probe-intent] signal kind=screen_change applied=true` → `[derive] running`；lights 翻 running | dev log + screenshot |
| §2 | reject reply：codex 進 waiting → 用 ESC 拒絕 dialog | dev log `[hook] PdxStop` 進來；J3 dispatcher hold 300ms `probeIntentPreGraceWindow` → `classifyAsHookRace` cover；lights 不閃 running | dev log + screenshot |
| §3 | close pane during waiting：codex 進 waiting → tmux kill-pane | ProcessDead detector 觸發 → status=clear；ScreenChange entry teardown（StopWatch 被呼） | dev log |
| §4 | quick-approval（case 2 known limitation）：codex 進 waiting → user 在 1.5s ScreenStable 之前批准（reaction time < IdleStableTicks=3） | armed=false → ScreenChanged 全 drop；status 不切 running；後續 PdxStop hook → idle；lights waiting → idle 跳過 running phase（**by-contract，PR body §test plan 預先標註觀察行為**） | dev log + screenshot |
| §5 | **A9** 30 次 reject 量化（J3 ship 時 deferred 的 ship gate） | 閃 running 次數 < 3/30 為 PASS | dev log per-event timestamp 表 |
| §6 | **A10** 30 次 approve latency 量化 | P95 latency ≤ 500ms（capture-pane tick 上限）+ 300ms（J3 graceWindow）= 800ms | dev log per-event timestamp 表 |

**A9 fail handling**（per kickoff + J3 R13 fail-fast）：

- A9 ≥ 3/30 → 開 followup issue 加 per-event trace 定位漏網類別
- 不 block ship；known limitation 標進 PR body

**Acceptance**：

- §1-§4 全 PASS（§4 為 by-contract 觀察）
- §5 A9 < 3/30
- §6 A10 P95 ≤ 800ms
- dev log + screenshot 證據附 PR body §test plan

**估計**：0.5-1 小時 user 操作。

**依賴**：PR open（merge 前 user 跑）。

---

### Phase 3 完成檢核

- [ ] P3-T1 8 integration case 全綠 + race 全綠
- [ ] P3-T2 mlab §1-§6 全 PASS（A9 < 3/30, A10 P95 ≤ 800ms）
- [ ] go test ./... 24 packages 全綠
- [ ] vet / lint clean

---

## 4. Subagent 派發策略（per `feedback_subagent_tdd_priority`）

### 4.1 派發數量

**1 個 subagent**（per kickoff §4 — "1 個 subagent ~290 行"）：

- 範圍：P1-T1 ~ P1-T5 + P2-T1 ~ P2-T3 + P3-T1（production code + unit + drift + wire + integration test）
- 排除：P3-T2 mlab live verify（user 操作；不派 subagent）

10 task 順序執行；每 task 獨立 commit（per CLAUDE.md「每個 task 獨立 commit」）。

### 4.2 Subagent prompt 模板

```
你是 W6-6 PR 的 TDD 實作 subagent。

Worktree: /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-w6-6-codex-screen-change
Spec: docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md（v6.1 final）
Plan: docs/specs/2026-05-02-w6-6-codex-screen-change-probe-plan.md

任務：依 plan §1 §2 §3.P3-T1 順序完成 9 個 task（P1-T1 → P3-T1；P3-T2 mlab 由主 session 處理不派你）。

務必：
1. 每個 Bash 命令前綴 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-w6-6-codex-screen-change && `（per feedback_subagent_cwd_enforcement）
2. Edit/Write 用絕對路徑帶 `.claude/worktrees/lights-w6-6-codex-screen-change/` 前綴（per feedback_worktree_absolute_path）
3. TDD：每 task 先寫 failing test、再寫 minimal impl、refactor、commit
4. detector 實作 spec §4.3 程式碼草稿 1:1 transcribe（含 GoDoc anchor 註解；不可改 signature、不可改鎖序、不可改 closed flag 機制）
5. spec §8 drift signal 表全列為實作禁區；任一徵兆出現停手 surface
6. 每 task 跑 `go test -race ./...` + `go build ./...` 確認無 regression 才 commit
7. 每 task commit message: "<type>(<scope>): <task name>"，例 "feat(codex): W6-6 P1-T2 StartScreenChangeDetector"
8. 不主動 push（push 由主 session 控制）
9. 完成後報告：所有改動的檔案 + 跑過的 test list + 任何 spec 解讀疑問

不要做：
- ❌ 不動 W6-3 既有 ProcessDead detector / ProbeIntents 第一筆 / Signal struct / ProbeIntent struct
- ❌ 不動 J3 dispatcher（pre-grace 已 ship at alpha.281）
- ❌ 不修 IsAliveFor PanePID 一致性（pre-existing infra；用 FirstAliveAgentInTree 規避）
- ❌ 不擴 ProbeIntent / Signal struct 多欄位
- ❌ 不引入 sustained-change counter / glyph 比對 / always-on framework
- ❌ 不為 ScreenChange 特化 pre-grace timer
- ❌ 不動 SPA 端
- ❌ 不跑 mlab live verify（P3-T2 主 session 處理）
```

### 4.3 主 session 職責

- 監控 subagent 進度
- 驗證每 task commit 內容（`git diff` 檢查；spec 對照）
- 跨 task integration regression 檢查
- P3-T2 mlab live verify（user 操作；可 PR review 收斂後再跑）
- PR open + 兩輪 codex review 驅動 + finding 收斂

---

## 5. PR 拆 / merge 流程（per CLAUDE.md）

### 5.1 PR open 前檢核

- [ ] 9 task 全 commit（P1-T1 ~ P3-T1）
- [ ] `go test ./...` 24 packages 全綠
- [ ] `go test -race ./internal/agent/codex/... ./internal/module/agent/...` 全綠
- [ ] `go vet ./...` clean
- [ ] `gofmt -d` clean
- [ ] `pnpm lint && pnpm build` 全綠（雖然不動 SPA，但 CI 整體要綠）

### 5.2 PR body 起草

必含：

- spec / plan 連結（`docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md` v6.1 + `docs/specs/2026-05-02-w6-6-codex-screen-change-probe-plan.md`）
- 9 task summary 表
- §test plan：unit / drift / wire / integration / mlab §1-§6 checklist（mlab 待 user verify 標 ⏳）
- §test plan 預先標註：mlab §4 quick-approval by-contract 觀察行為（waiting → idle 跳過 running phase 是 contract 正確；非 bug）
- §known limitations：
  - quick-approval / fast-with-output（case 2，PdxStop hook cover）
  - reject path boundary race（J3 R13/R14 fail-fast，A9 ≥ 3 開 followup issue）
  - IsAliveFor PanePID inconsistency（pre-existing；W6-6 用 FirstAliveAgentInTree 規避；follow-up issue）

### 5.3 兩輪 codex review

**Round 1 標準**：

```
node ~/.claude/plugins/cache/openai-codex/codex/1.0.2/scripts/codex-companion.mjs review --base origin/main --background
```

- 跨模型差異化檢查
- 收斂 finding；要修就修，要 defer 就建 issue

**Round 2 三平行 adversarial**：

1. **攻擊方**：focus = "找 bug / 安全漏洞 / race / 邊界條件；特別檢查 mutex-protected closed bool 的 close-race fix 是否真能阻止 panic on closed channel；檢查 8 case integration test 是否真覆蓋 truth table case 2 path"
2. **防守方**：focus = "驗證設計合理性 / 架構一致性 / API 邊界；特別防 spec drift（per feedback_codex_pr_review_spec_alignment — round-1 不讀 spec）；對照 spec §8 drift signal 表逐項檢查；檢查 detector signature / OnEntryStatus 是否與 spec §4.3 + §4.1 對齊"
3. **體質方**：focus = "過大檔案 / SRP 違反 / 職責不清；特別檢查 detector 內部 5 個同步原語（armed / emitted / mu / closed / emittedCh）職責是否清楚；檢查 fakeProber 擴是否違 SRP"

收斂彙整表（per CLAUDE.md PR Review 兩輪制）：

| 嚴重性信心 | 關聯度 | 複雜度 | 處置 |
|---|---|---|---|
| 高 | 高 | 任意 | 修 |
| 高 | 任意 | 低 | 修 |
| 任意 | 高 | 低 | 修 |
| 中低 | 低 | 中高 | 開 issue defer |

連續 2-3 輪同類 meta-drift → 停手 surface（per `feedback_codex_meta_drift_signal`）。

### 5.4 Squash merge → main

**前提**：

- 0 critical / P1 finding
- mlab §1-§6 全 PASS（A9 < 3/30, A10 P95 ≤ 800ms）— **可放最後做**
- known limitation 已建 followup issue

merge 後：

- branch 刪除
- worktree 清理（`ExitWorktree` action=remove）
- 起 bump PR alpha.282（§7）

---

## 6. Test plan summary

### Unit test（隔離）

- ProbeIntentKindScreenChange const（P1-T1）：0 test（const 自然 cover）
- StartScreenChangeDetector（P1-T3）：**11 test**
- onScreenChange + ProbeIntents fixture（P1-T5）：**4 test**

### Drift / wire test

- Drift fixture 擴（P2-T2）：1 fixture（switch ↔ supportedKinds ↔ ProbeIntents 三邊集合）
- Codex wire test（P2-T3）：1 case（waiting → ScreenStable → ScreenChanged → running + teardown）

### Integration test（跨層）

- Dispatcher integration（P3-T1）：**8 case**（v5 truth table + identity race + retry）

### Live verify

- mlab §1-§6（P3-T2）：6 場景（含 A9 30 次 reject + A10 30 次 approve latency ship gate）

**Test 總計**：~24 test（11 unit + 4 provider + 1 drift + 1 wire + 8 integration）+ 6 mlab 場景。

---

## 7. Known issues / risks

### 7.1 已知接受限制（non-blocking for ship；by contract）

1. **case 2 known limitation**（quick-approval / fast-with-output）：armed=false → ScreenChanged 全 drop → 永無 emit → 後續 PdxStop hook → idle；lights waiting → idle 跳過 running phase。By-contract 不算 bug，PR body §test plan 預先標註觀察行為。
2. **reject path boundary race**（spec §0.5 + R14）：J3 dispatcher pre-grace + classifyAsHookRace cover；A9 < 3/30 為 acceptable threshold；fail 才開 followup issue 加 per-event trace。
3. **IsAliveFor PanePID inconsistency**（pre-existing infra；spec §11 line 744）：W6-6 用 FirstAliveAgentInTree 規避；開 follow-up issue 描述 PanePID vs ActivePanePID 行為差異 + 建議 IsAliveFor 改 ActivePanePID + 加 multi-pane test。

### 7.2 風險與對策

| 風險 | 機率 | 影響 | 對策 |
|---|---|---|---|
| Detector signature drift（不符 spec §4.3） | low | high (regression on round 6 P1) | spec §4.3 程式碼草稿 1:1 transcribe + GoDoc anchor 註解強制 reviewer 對照 |
| close-race fix 不夠健壯（仍 panic on closed channel） | low | high | P1-T3 #11 NoCloseRace test + go test -race + Round 2 攻擊方專注檢查 |
| FakeProber 擴破 W6-3 既有 wire test | medium | medium | P2-T3 + P3-T1 strict 跑既有 W6-3 wire test 全綠（regression 0 assertion） |
| mlab A9 ≥ 3/30 漏網率高 | medium | medium | Followup issue 加 per-event trace；不 block ship（J3 R13 fail-fast handling） |
| mlab A10 P95 > 800ms | low | medium | 若超 → 檢查 capture-pane tick 配置 / J3 pre-grace 是否誤增加 latency；不 block ship 但需 root cause |
| Spec drift 進 PR（reviewer 沒抓到） | low | high | Round 2 防守方 focus 強制對照 spec §8 drift signal 表逐項 |

### 7.3 Rollback 計畫

若 ship 後發現 regression：

1. revert merge commit（不直接刪 detector 檔；保留 history）
2. 起 hotfix bump
3. issue 追蹤 root cause
4. 重新 plan 修法 → 重新 PR

W6-6 是 codex 端 surgical addition，revert 不影響 W6-3 ProcessDead 既有 path。

---

## 8. Bump PR

W6-6 PR squash merge 後獨立 bump PR：

- VERSION: alpha.281 → alpha.282
- spa/package.json + package.json 同步
- CHANGELOG.md 加：

```
### lights rebuild
- **W6-6**: codex permission-reply ScreenChange ProbeIntent
  - codex 進 waiting 狀態（PdxPermissionRequest hook）後，user 批准 dialog 不發 hook → lights 卡 waiting 永不轉 running
  - W6-6 detector 用 `Prober.Watch` 觀察 paneID 上方 10 行螢幕內容變化；ScreenStable 後第一個 ScreenChanged emit Signal → status 切 running
  - 2-phase + 2-case truth table：穩定 ✓ → 監控變化 → emit；不穩定 ✗ → 永久 drop（PdxStop hook cover；by-contract case 2）
  - reject path race 由 J3 dispatcher generic pre-grace cover（W6-6 detector 仍是 dumb emit）
  - 沿用 W6-3 ProbeIntentProvider interface + dispatcher 5-case lifecycle；codex provider ProbeIntents() 多一筆 entry
```

- bump PR 用獨立 worktree：`bump-alpha-282`（per `feedback_bump_base_origin_not_local` — 進 worktree 後先 `git reset --hard origin/main`）

---

## 9. 完成檢核總表

| Phase | 檢核 | 狀態 |
|---|---|---|
| Phase 1 P1-T1~T5 | 5 task 全 commit + 11 unit case + 4 provider case 全綠 + race 全綠 | ⏳ |
| Phase 2 P2-T1~T3 | 3 task 全 commit + drift fixture + wire case 全綠 + 手動 break drift 一次驗 fail | ⏳ |
| Phase 3 P3-T1 | 8 integration case 全綠 + race 全綠 | ⏳ |
| Phase 3 P3-T2 | mlab §1-§6 全 PASS（A9 < 3/30, A10 P95 ≤ 800ms） | ⏳ |
| PR + codex review 兩輪 | 0 critical/P1 + known issue 追蹤化 | ⏳ |
| Squash merge → main | branch 刪除 + worktree 清理 | ⏳ |
| Bump PR alpha.282 | merge | ⏳ |

---

## 10. 文獻

- W6-6 spec：`docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md` v6.1 final（7 輪 review，round 7 0 finding）
- W6-3 spec：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md`（interface finalize / dispatcher / drift gate / §9.14 generic-Kind 不特化 anchor）
- W6-3 plan：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-plan.md`（W6-6 plan 結構參考來源）
- J3 spec：`docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-spec.md` v7.5（dispatcher generic pre-grace；PR #797 ship at alpha.281）
- J3 plan：`docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-plan.md`
- Fix-spec：`docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3（framework 撤回約束）
- W1 audit：`docs/specs/2026-04-28-hook-status-audit-spec.md` §6 / §7 / §7.1
- Lights rebuild spec：`docs/specs/2026-04-23-lights-rebuild-spec.md` §8.2（ProbeIntent 起源）
- Probe primitives：`internal/agent/probe/probe.go` / `internal/agent/probe/activity.go`
- W6-3 detector：`internal/agent/codex/probe_intent_process_dead.go`（detector 寫作風格參考）
- Dispatcher：`internal/module/agent/probe_intent_dispatcher.go`（5-case lifecycle / applyProbeGuards / consumeSignals / replayStatus）
