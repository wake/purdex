# W6-3 codex error ad-hoc ProbeIntent — Implementation Plan

> **Status**：v2（codex plan review 4 finding 全採納）
> **依賴 spec**：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md` v6 final（5 輪 review 12 finding 採納）
> **Worktree**：`.claude/worktrees/lights-w6-3-codex-error` / branch `worktree-lights-w6-3-codex-error`
> **Base**：`origin/main` @ alpha.261
> **拆分**：Phase 1（interface + dispatcher plumbing；TDD subagent）→ Phase 2（codex detector + verify；TDD subagent）→ PR

---

## 0. Plan 總覽

### 0.1 範圍重申（per spec §1.1）

W6-3 + W6-4 合併 first PR，14 task 分兩 phase：

- **Phase 1**：6 task — `ProbeIntentProvider` interface + `applyProbeGuards` mechanical extraction + dispatcher plumbing（`applyStatus` / `reconcileSessionActive` / `applyIntentLifecycle` / `consumeSignals`）+ replay recovery；codex provider 暫用 stub detector 跑 lifecycle test
- **Phase 2**：8 task — `tmux.HasPane` + codex `StartProcessDeadDetector` + provider wiring + drift test + dev log + integration test + mlab live verify

### 0.2 估計

- 總 production code：~600-700 行（spec §3 / §5 程式碼草稿是約等量）
- 總 test code：~800-1000 行（5 lifecycle case + 4 reconcile + probe-applied teardown + replay race + multi-pane + integration）
- 預估 PR diff：~1500-1700 行
- 預估時間：8-12 小時 subagent 工作（包含 mlab live verify）

### 0.3 鎖序與不變式

per spec §2.1 必守清單，實作時必持守：
- 鎖序：m.mu only（無 cross-lock）
- ProbeIntent state（`activeProbeIntents`）由 m.mu 保護
- detector goroutine 不持 m.mu
- channel buffer = 1
- generation token uint64 純遞增

---

## 1. Phase 1：Interface + dispatcher plumbing + replay

### P1-T1 — `ProbeIntent` types in `internal/agent/provider.go`

**目標**：落 `ProbeIntentProvider` interface + `ProbeIntent` / `ProbeIntentKind` / `Signal` types。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/provider.go` | 附加 ProbeIntentProvider interface + ProbeIntent struct (Kind / OnEntryStatus / OnSignal) + ProbeIntentKind string type + ProbeIntentKindProcessDead const + Signal struct (Kind / PaneAlive / PaneID / SenderPID) |
| `internal/agent/provider_test.go` | 新增 unit test：interface 簽名 + Signal struct 完整性 + Kind const |

**Test**：
- `TestProbeIntent_StructFields` — Signal 4 fields 都存在 + 型別正確
- `TestProbeIntentKind_ProcessDead` — const 值是 "process_dead"
- `TestProbeIntentProvider_OptionalNil` — fakeProvider 不實作 → 仍滿足 AgentProvider

**Acceptance**：`go test ./internal/agent/...` 全綠；`go build ./...` 全綠。

**估計**：~50 行 production / ~100 行 test。

**依賴**：none（最先做）。

---

### P1-T2 — `applyProbeGuards` mechanical extraction

**目標**：從 `probe_orchestrator.go interpretScreenEvent` 抽出 free function `applyProbeGuards`；行為零變動；ScreenChange 路徑改用新 helper；新加 strategy injection point（Mapping callback + StaleCheck closure）。

**1:1 mapping table**（per plan-review P2-2 — 強制 mechanical extraction，可重複驗證）：

| 既有 `interpretScreenEvent` step | 抽出後位置 | 變動類型 |
|---|---|---|
| step 1 (line 240-247): stale-callback guard fast-path（read activeWatchers + agentType match） | `applyProbeGuards` step 1：caller-supplied `StaleCheck closure` 在 m.mu 內 invoke；ScreenChange 路徑 closure 內容 = 既有 read 邏輯 | mechanical（read 路徑包進 closure）|
| step 2 (line 249-259): graceWindow check（取 graceMu lock + lastHookAt）| `applyProbeGuards` step 2：identical 區塊 | identical |
| step 3 (line 261-303): Kind → status mapping（含 ScreenStable 的 dead-PID sweep 分支） | **保留在 `interpretScreenEvent` 上層** — 完成 mapping 後將 `newStatus` 傳入 `applyProbeGuards` 作為已 mapped 結果（透過 `Mapping closure`） | reorganization：mapping 移到 caller layer |
| step 4 (line 312-325): final critical section + stale guard re-check + ErrorGuard + transition gate | `applyProbeGuards` step 4：identical（用同一 StaleCheck closure 在 m.mu 內 re-check）| identical |
| step 5 (line 326-353): mutate currentStatus + setProjectionTopStatus + broadcastToSession | `applyProbeGuards` step 5：identical | identical |

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_orchestrator.go` | `interpretScreenEvent` step 1+2+4+5 抽到 `applyProbeGuards`；step 3 mapping 留 caller；ScreenChange 路徑 caller adaptor 把 ScreenChange-specific Kind→status 包成 Mapping closure；StaleCheck closure 包 activeWatchers read |
| `internal/module/agent/probe_orchestrator_test.go` | 既有 test 全綠（zero regression assertion） |
| `internal/module/agent/probe_intent_dispatcher.go`（新檔，骨架） | 預留 `probeGuardArgs` struct + `applyProbeGuards` signature；本 task 只填 ScreenChange caller |

**Test**（含 table-driven regression — per plan-review P2-2）：

新加 `applyProbeGuards` 直接測試：
- `TestApplyProbeGuards_StaleCheckGuard_FastPath` — StaleCheck closure 回 false → applied=false（不進 graceWindow / mapping）
- `TestApplyProbeGuards_GraceWindow` — recordHookAt 後 2s 內 → applied=false
- `TestApplyProbeGuards_Mapping_NewStatusEmpty_Drops` — Mapping returns "" → applied=false
- `TestApplyProbeGuards_ErrorGuard` — currentStatus=error → applied=false
- `TestApplyProbeGuards_TransitionGate` — currentStatus=newStatus → applied=false
- `TestApplyProbeGuards_HappyPath_MutatesAndBroadcasts` — 所有 guard pass → mutate + broadcast + applied=true
- `TestApplyProbeGuards_FinalCriticalSectionReCheck` — fake interruptBeforeFinalLockFn 模擬 step 1 unlock 後 stop → step 4 lock 內 StaleCheck 為 false → 取消 mutate

ScreenChange path table-driven regression（per plan-review P2-2 — 機械驗證 5 類觀察 output 與 pre-extraction 一致）：

```go
// TestScreenChangePath_RegressionMatrix iterates a 5-case table and asserts
// each case's externally observable output (broadcast / metric counter /
// dev log line) matches the pre-extraction baseline fixture verbatim.
//
// Cases (5; 對應 step 1-5 各一個觀察點)：
//   1. stale_active_watchers_drop  — activeWatchers 缺 entry → 無 broadcast、無 metric、無 dev log
//   2. grace_window_drop           — recordHookAt 後 1s → 無 broadcast、graceWindow counter +1
//   3. error_guard_drop            — currentStatus=error → 無 broadcast、無 transition counter
//   4. transition_gate_drop        — currentStatus 已是 target → 無 broadcast、無 mutation
//   5. happy_path_apply            — 全 pass → broadcast 一次 + transition counter +1 + dev log
//
// 每 case 跑 pre-extraction (git stash 模擬) + post-extraction，assert output 字串相等。
```

**Acceptance**：
- `go test ./internal/module/agent/...` 全綠
- 既有 `TestInterpretScreenEvent_*` zero regression
- `TestScreenChangePath_RegressionMatrix` 5 case 全 pass — output 字串級對齊 pre-extraction baseline
- 1:1 mapping table 在 PR description 中明示，codex review 對 mapping table 二次 verify
- ProbeIntent 路徑骨架（Mapping / StaleCheck）暫無 caller，但簽名 finalize

**估計**：~120 行 production（含 free function + ScreenChange caller adaptor）/ ~350 行 test（含 regression matrix fixture）。

**依賴**：P1-T1（用到 ProbeIntent types — 但 P1-T2 ScreenChange 路徑暫不用）。實際 P1-T1 + P1-T2 可平行做。

---

### P1-T3 — `Module.lookupTopFrameForSessionLocked` helper

**目標**：在 m.mu 內讀 frame projection top frame `(paneID, pid)`，給 dispatcher 用。**提前到 P1-T3**（per plan-review P1-1 修法 — dispatcher core 編譯前置依賴）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/module.go` | 新加 `lookupTopFrameForSessionLocked(session) (paneID string, pid int, ok bool)` — caller 已持 m.mu；走既有 `projectionForSessionLocked`（如有）/ 或 `m.frames` 直接 lookup top frame |
| `internal/module/agent/module_test.go` | 加 unit test |

**Test**：
- `TestLookupTopFrameForSessionLocked_HappyPath`
- `TestLookupTopFrameForSessionLocked_NoSession_NotOk`
- `TestLookupTopFrameForSessionLocked_NoTopFrame_NotOk`

**Acceptance**：`go test ./internal/module/agent/...` 全綠。

**估計**：~30 行 production / ~80 行 test。

**依賴**：none。

> 註：若既有 `projectionForSession` 已是 m.mu 內 lock-free read，可直接 reuse — 本 task 退化為 wrapper 對齊 dispatcher 預期 contract（`paneID, senderPID, ok`）。實作時驗證。

---

### P1-T4 — Dispatcher core: `applyStatus` / `reconcileSessionActive` / `applyIntentLifecycle` / `consumeSignals`

**目標**：實作 dispatcher 核心邏輯；用 stub detector（永不 emit）跑 lifecycle 5 case + 4 reconcile case + probe-applied teardown 全部 test。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher.go` | 完整實作：probeIntentDispatcher struct / activeIntent / lifecyclePlan / applyStatus / reconcileSessionActive / applyIntentLifecycle / consumeSignals / makeProbeIntentStaleCheck / nextProbeIntentGeneration / stopAll |
| `internal/module/agent/module.go` | `Module` struct 加 `activeProbeIntents map[string]map[agent.ProbeIntentKind]activeIntent` + `probeIntentGen uint64` + `probeIntentDisp *probeIntentDispatcher`；`New()` 初始化；`Stop()` 呼叫 `probeIntentDisp.stopAll()` |
| `internal/module/agent/probe_intent_dispatcher_test.go` | 新檔：lifecycle 5 case test + reconcile 4 case + probe-applied teardown |
| `internal/module/agent/fakes_test.go` | 加 fakeProbeIntentProvider（測 stub detector）|

**Test list**（核心 lifecycle / reconcile / teardown）：

Lifecycle 5 case：
- `TestApplyIntentLifecycle_Case1_NotActive_NotShouldActive_Noop`
- `TestApplyIntentLifecycle_Case2_NotActive_ShouldActive_Start`
- `TestApplyIntentLifecycle_Case3_Active_NotShouldActive_Stop`
- `TestApplyIntentLifecycle_Case4_Active_ShouldActive_TargetMatch_Noop`
- `TestApplyIntentLifecycle_Case5_Active_ShouldActive_TargetMismatch_CancelAndRearm`

Reconcile 4 case：
- `TestReconcileSessionActive_UnknownAgent_DropAll`
- `TestReconcileSessionActive_ProviderHasNoIntents_DropAll`
- `TestReconcileSessionActive_AgentTypeChanged_DropOld`（codex active → newAgentType=cc）
- `TestReconcileSessionActive_KindNotInDeclared_DropOldKind`

Probe-applied teardown：
- `TestConsumeSignals_AppliedTrue_ReRunsApplyStatus`（stub detector emit 後 status=error → activeProbeIntents 該 entry 應被清）
- `TestConsumeSignals_AppliedFalse_NoTeardown`（applied=false → active entry 仍在）

Stale-callback / generation：
- `TestMakeStaleCheck_GenerationMismatch_ReturnsFalse`
- `TestMakeStaleCheck_AgentTypeMismatch_ReturnsFalse`
- `TestMakeStaleCheck_AllMatch_ReturnsTrue`

Replay race（補 by2z79ouc ATK-2 防回歸）：
- `TestApplyIntentLifecycle_StatusChangedBetweenSnapshotAndArm_NoArming`（fake hook 在 m.mu 解鎖前改 currentStatus=idle）

Concurrent rename（補 §6.4 race）：
- `TestApplyStatus_DuringRename_NewSessionGetsActiveEntry`

**Acceptance**：
- `go test ./internal/module/agent/...` 全綠
- 13+ test 全部 pass
- 無 race（用 `go test -race` 跑）
- ScreenChange path 不受影響（既有 test 全綠）

**估計**：~250 行 production / ~500 行 test。

**依賴**：P1-T1 + P1-T2 + **P1-T3**（lookupTopFrameForSessionLocked 是 dispatcher 必要前置 — per plan-review P1-1 修法）。

---

### P1-T5 — `manageActivityWatch` 接 dispatcher + rename / Stop 路徑

**目標**：`manageActivityWatch` 退化的 stop-only 版本加 `probeIntentDisp.applyStatus` 呼叫；`renameSessionLocked` / `Module.Stop` 路徑也接。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/module.go` | `manageActivityWatch` body 加 step 2（dispatch ProbeIntent applyStatus）；`renameSessionLocked` 補 `applyStatus(newName, agentType, currentStatus)` — oldName 在 m.mu 內已被先轉移到 newName，applyStatus 看到的 currentStatus 是 newName 的 |
| `internal/module/agent/module_test.go` | 加 rename / Stop 路徑 integration |

**Test**：
- `TestManageActivityWatch_StatusToRunning_ProbeIntentArmed`
- `TestManageActivityWatch_StatusToIdle_ProbeIntentTornDown`
- `TestRenameSession_OldNameProbeIntentMigratesToNew`
- `TestModuleStop_ProbeIntentDispatcherStoppedAll`

**Acceptance**：`go test ./internal/module/agent/...` 全綠。

**估計**：~30 行 production / ~120 行 test。

**依賴**：P1-T4（dispatcher core）。

---

### P1-T6 — `Module.Start` replay recovery + `replayStatus`

**目標**：`Module.Start` 在 `replayFromDB + startSweep` 後呼叫 `probeIntentDisp.replayStatus()`；對所有 session 重新評估 ProbeIntent gating + 啟動 detector。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/module.go` | `Start()` 加 `m.probeIntentDisp.replayStatus()` 呼叫（`startSweep` 之後）|
| `internal/module/agent/probe_intent_dispatcher.go` | 加 `replayStatus()` method — 走 `m.snapshotStatuses()` 拿 session 列表 + agentType + status → 對每個 session 走 `applyStatus` |
| `internal/module/agent/probe_intent_dispatcher_test.go` | 加 replay integration test |

**Test**：
- `TestReplayStatus_RunningSession_DetectorArmed`（fixture：projection top frame + status=running → Start 後 dispatcher.activeProbeIntents 含對應 entry）
- `TestReplayStatus_IdleSession_DetectorNotArmed`
- `TestReplayStatus_StaleFrame_DetectorEmitsImmediately`（fixture：projection top frame.pid 已死 + status=running → Start 後 detector 第一次 poll 立即 emit + broadcast error）— 此 test 用 stub detector 模擬 emit；real 場景在 Phase 2 P2-T8

**Acceptance**：`go test ./internal/module/agent/...` 全綠。

**估計**：~40 行 production / ~150 行 test。

**依賴**：P1-T4 + P1-T5。

---

### Phase 1 完成檢核

- [ ] `go test ./internal/agent/... ./internal/module/agent/...` 全綠
- [ ] `go test -race ./internal/module/agent/...` 全綠（race 檢查）
- [ ] `go build ./...` 全綠
- [ ] ScreenChange path zero regression（既有 probe_orchestrator_test.go / probe_orchestrator_integration_test.go 全綠）
- [ ] dispatcher / lifecycle test list 全部 pass（30+ tests）
- [ ] `pnpm lint` / `pnpm build` clean（SPA 不受影響）

完成後可進 Phase 2。

---

## 2. Phase 2：codex detector + drift test + mlab live verify

### P2-T1 — `tmux.Executor.HasPane(paneID) bool`

**目標**：tmux helper 新方法 — `tmux list-panes -a -F '#{pane_id}'` + scan paneID。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/tmux/executor.go` | 新加 `HasPane(paneID string) bool` 方法（包 list-panes -a；scan output；包含 paneID 即 true） |
| `internal/tmux/executor_test.go` | 加 unit test（mock command output） |

**Test**：
- `TestHasPane_PaneExists_ReturnsTrue`
- `TestHasPane_PaneNotInList_ReturnsFalse`
- `TestHasPane_TmuxCommandError_ReturnsFalse`（conservative — 任何 error 都當 not found）
- `TestHasPane_EmptyPaneID_ReturnsFalse`

**Acceptance**：`go test ./internal/tmux/...` 全綠。

**估計**：~25 行 production / ~80 行 test。

**依賴**：none。**必須在 P2-T2 detector 之前**（per by2z79ouc Defense — detector 編譯需要 HasPane 存在）。

---

### P2-T2 — codex `StartProcessDeadDetector` + `isPidAliveFn` injection

**目標**：實作 codex-side detector。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/codex/probe_intent_process_dead.go`（新檔） | per spec §4.2 完整偽碼：`isPidAliveFn = probe.IsPidAlive` package var + `processDeadPollInterval = 1 * time.Second` package var + `StartProcessDeadDetector(ctx, paneLister, paneID, senderPID, out)` + `tmuxPaneLister` interface |
| `internal/agent/codex/probe_intent_process_dead_test.go`（新檔） | 4 種 (pidAlive×paneAlive) 組合 + multi-pane fixture |

**Test list**：
- `TestStartProcessDeadDetector_BothAlive_KeepsPolling`（2 ticks 後 ctx cancel；channel 無 emission）
- `TestStartProcessDeadDetector_PidDead_PaneAlive_EmitsErrorPath`（PaneAlive=true → error）
- `TestStartProcessDeadDetector_PidDead_PaneGone_EmitsClearPath`（PaneAlive=false → clear）
- `TestStartProcessDeadDetector_PidAlive_PaneGone_TreatedAsPaneGone`（process re-parented edge case → PaneAlive=false 路徑）
- `TestStartProcessDeadDetector_CtxCancel_ExitsImmediately`
- `TestStartProcessDeadDetector_EmitsOnceThenReturns`（dead-confirmed 後不再 poll）
- `TestStartProcessDeadDetector_PidAliveFn_OverrideAndCleanup`（test override + cleanup pattern）
- `TestStartProcessDeadDetector_MultiPane_UsesCorrectPaneID`（fixture：tmux list-panes 回 [%5, %7]，detector 看 %5 是否還在；不被 %7 影響）

**Acceptance**：
- `go test ./internal/agent/codex/...` 全綠
- `go test -race` 全綠
- 4 個 (pidAlive×paneAlive) 組合都驗證
- `isPidAliveFn` override + cleanup pattern 正確

**估計**：~70 行 production / ~220 行 test。

**依賴**：P1-T1 + P2-T1。

---

### P2-T3 — codex `Provider.ProbeIntents()` + `onProcessDead` mapper

**目標**：codex provider 實作 ProbeIntentProvider；OnSignal 區分 PaneAlive=true→error / =false→clear。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/codex/provider.go` | 加 `ProbeIntents() []agent.ProbeIntent` method + `onProcessDead(sig agent.Signal) agent.Status` |
| `internal/agent/codex/provider_test.go` | 加 unit test |

**Test**：
- `TestProvider_ProbeIntents_DeclaresProcessDead`
- `TestProvider_ProbeIntents_OnEntryStatusContainsRunningWaiting`
- `TestOnProcessDead_PaneAliveTrue_MapsToError`
- `TestOnProcessDead_PaneAliveFalse_MapsToClear`
- `TestOnProcessDead_WrongKind_ReturnsEmpty`（防 dispatcher misuse）

**Acceptance**：`go test ./internal/agent/codex/...` 全綠。

**估計**：~25 行 production / ~80 行 test。

**依賴**：P1-T1 + P2-T2。

---

### P2-T4 — Dispatcher 路由 `ProcessDead` Kind 到 codex detector（生產 wiring）

**目標**：`probe_intent_dispatcher.go` 的 `applyIntentLifecycle` switch on Kind 加 ProcessDead case，呼叫 `codex.StartProcessDeadDetector`。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher.go` | 在 `applyIntentLifecycle` 內 lifecycle plan execution 段，switch on intent.Kind：`case agent.ProbeIntentKindProcessDead: codex.StartProcessDeadDetector(...)` |
| `internal/module/agent/probe_intent_dispatcher_test.go` | 加 integration test |

**Test**：
- `TestApplyStatus_RealCodexDetector_ArmsAndStops`（用 fake `tmux.Executor` + override `isPidAliveFn` → emit signal → 驗 lifecycle teardown）

**Acceptance**：`go test ./internal/module/agent/...` 全綠 + 整合 codex detector 跑通。

**估計**：~10 行 production / ~120 行 test。

**依賴**：P1-T4（dispatcher core）+ P2-T2 + P2-T3。

---

### P2-T5 — Drift test

**目標**：iterate registry 找所有 `ProbeIntentProvider`；對每個逐一驗證 dispatcher 路由完整性。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_drift_test.go`（新檔） | 走 registry → for each provider 實作 ProbeIntentProvider → for each declared Kind → assert dispatcher case 存在 + OnSignal 不為 nil + OnEntryStatus 非空 + Signal 4 fields 都被 detector 寫入 |

**Test**：
- `TestProbeIntentDriftCoverage`：registry sweep + assertions
- `TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase`
- `TestProbeIntentDrift_OnSignalNonNil_OnEntryStatusNonEmpty`

**Acceptance**：drift test 失敗時 error message 清楚指出哪個 provider / kind 缺實作。

**估計**：~80 行 test。

**依賴**：P2-T4。

---

### P2-T6 — Observability: dev log + TraceStore step + expvar metrics（per plan-review P2-1）

**目標**：spec §8.4 三件事都做：(a) dev log 4 類 line / (b) TraceStore `[probe-intent]` step kind（chain log 第 6 條）/ (c) expvar metrics counter。

**(a) dev log**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/trace.go` | package comment 補 `[probe-intent]` step（包含 start / signal / stop / drop 4 個 sub-step）|
| `internal/module/agent/probe_intent_dispatcher.go` | 4 類 dev log（在 `isDevMode()` 條件下）：<br>• `[probe-intent] start session=X agent=codex kind=process_dead pane=%5 pid=12345 generation=N`<br>• `[probe-intent] signal session=X kind=process_dead PaneAlive=true newStatus=error applied=true`<br>• `[probe-intent] stop session=X kind=process_dead reason=lifecycle-applyStatus\|reconcile\|stop-all`<br>• `[probe-intent] drop session=X kind=process_dead reason=stale-callback\|grace\|error-guard\|transition-gate` |

**(b) TraceStore step**：spec §8.4 #2 — chain log 第 6 條 `[probe-intent]` 與 `[hook] / [derive] / [handler] / [broadcast] / [verify_passed]` 對齊：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/trace.go` | 加 `TraceStepProbeIntent` 常數 + step 寫入 helper（reuse 既有 `traceSink.Append` 路徑） |
| `internal/module/agent/probe_intent_dispatcher.go` | `applyIntentLifecycle` start/stop 路徑 + `consumeSignals` applied 路徑 寫入 trace step（含 chain id — sourced 自 hook 觸發或 replay context）|

**(c) expvar metrics**：spec §8.4 #3 — 沿既有 `internal/agent/MetricProbeXxx` pattern：

| 檔案 | 改動 |
|---|---|
| `internal/agent/metrics.go`（既有） | 加 `MetricProbeIntentStarted` / `MetricProbeIntentStopped` / `MetricProbeIntentSignalEmitted` / `MetricProbeIntentApplied` / `MetricProbeIntentDroppedStale` / `MetricProbeIntentDroppedGrace` / `MetricProbeIntentDroppedErrorGuard` / `MetricProbeIntentDroppedTransitionGate`（8 個 counter）|
| `internal/module/agent/probe_intent_dispatcher.go` | 對應路徑 `Metric*.Add(1)` |

**Test**：
- `TestProbeIntent_DevLog_StartLine`
- `TestProbeIntent_DevLog_SignalLine`
- `TestProbeIntent_DevLog_DropReason_StaleCallback`
- `TestProbeIntent_TraceStore_StartStep`（fake traceSink + assert Append 被呼叫含 `TraceStepProbeIntent` + chain id）
- `TestProbeIntent_TraceStore_SignalStep`
- `TestProbeIntent_Expvar_StartedCounterIncrement`（counter delta test：fire start → counter +1）
- `TestProbeIntent_Expvar_DroppedCounterByReason`（4 種 drop reason 各對應 counter +1）

**Acceptance**：
- `PDX_DEV_MODE=1` 下 4 類 log 在對應路徑被印出
- TraceStore 中可 query 出 `TraceStepProbeIntent` step entries（與 hook chain 對齊）
- `/debug/vars` JSON 內 8 個 `probe_intent_*` counter 反映 dispatcher 動作

**估計**：~80 行 production / ~250 行 test。

**依賴**：P2-T4。

---

### P2-T7 — Integration test: codex 進程 alive→dead 兩條 mapping path

**目標**：mock codex 進程 alive→dead；驗 status running → error / running → clear 兩條完整 broadcast。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_integration_test.go`（新檔） | 整合 codex detector + dispatcher + applyProbeGuards + broadcast；mock tmux + override isPidAliveFn 控制 process state |

**Test**：
- `TestIntegration_CodexProcessDead_PaneAlive_BroadcastsError`
- `TestIntegration_CodexProcessDead_PaneGone_BroadcastsClear`
- `TestIntegration_CodexNormalExit_NoErrorBroadcast`（status=idle 期間 process dead → ProbeIntent 不 armed → 不 broadcast）
- `TestIntegration_CodexRestart_ActiveTargetMismatch_ReArms`（同 pane 內換 pid → case 5 觸發）
- `TestIntegration_AgentSwitchCodexToCC_OldDetectorTornDown`（reconcile cleanup 驗證）
- `TestIntegration_ReplayWithRealDetector_PaneGone_BroadcastsClear`（per plan-review P1-2：replayStatus 用 real ProcessDead detector，fixture：projection top frame.pid 已死 + HasPane=false → restart 後立即 emit clear → 對應 §8.1 #5 的 clear 路徑）
- `TestIntegration_ReplayWithRealDetector_PaneAlive_BroadcastsError`（同上但 HasPane=true → emit error；對應 §8.1 #5 error 路徑）

**Acceptance**：`go test ./internal/module/agent/...` 全綠 + integration test list 全 pass。

**估計**：~300 行 test。

**依賴**：P2-T4 + P2-T6。

---

### P2-T8 — mlab live verify

**目標**：手動驗證 4 個關鍵場景；產出證據（log + screenshot）。

**步驟**：

1. **Build worktree binary**：
   ```
   cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-w6-3-codex-error
   go build -o /tmp/pdx-w6-3 ./cmd/pdx
   ```
2. **Stop existing daemon**：`pkill -f "/tmp/pdx serve"`
3. **Start W6-3 daemon**：
   ```
   PDX_DEV_MODE=1 /tmp/pdx-w6-3 serve > /tmp/pdx-w6-3.log 2>&1 &
   ```
4. **Reinstall codex hooks**（若 sender_pid 路徑有變動）：
   ```
   /tmp/pdx-w6-3 install --reinstall --agent codex
   ```

**4 個場景**：

| § | 場景 | 期望 | 驗證 |
|---|---|---|---|
| §1 codex SIGKILL | codex running 中 → `kill -9 <codex_pid>` | ≤2s lights 變 error | `[probe-intent] signal ... PaneAlive=true newStatus=error applied=true` log + SPA tab icon 紅 |
| §2 codex pane close | codex running 中 → `tmux kill-pane -t %X` | ≤2s lights 變 clear | `[probe-intent] signal ... PaneAlive=false newStatus=clear applied=true` log + SPA tab icon 灰 |
| §3 codex /exit | codex running → `/exit` | lights → idle 後 ProbeIntent 停；後續 pane close 不誤觸 | `[probe-intent] stop session=X kind=process_dead reason=lifecycle-applyStatus` log；後續 pane close **無** signal log |
| §4 daemon restart 後 codex 已死（pane 仍存在）→ error | codex running 中 stop daemon → kill -9 codex → restart daemon | restart 後 ≤2s lights 變 error | `[probe-intent] start ...` (replay arm) → 立刻 `[probe-intent] signal ... PaneAlive=true ... applied=true` log |
| §5 daemon restart 後 codex 已死（pane 也消失）→ clear（per plan-review P1-2 補測） | codex running 中 stop daemon → `tmux kill-pane` 將 codex pane 連同 process 移除 → restart daemon | restart 後 ≤2s lights 變 clear | `[probe-intent] start ...` (replay arm) → 立刻 `[probe-intent] signal ... PaneAlive=false newStatus=clear ... applied=true` log |

**附加場景（可選）**：

| § | 場景 | 期望 |
|---|---|---|
| §6 codex restart 換 pid | codex running → /exit → 重新啟新 codex（同 pane）→ 新 status=running 後 kill 新 pid | lights 變 error，舊 detector 不誤觸 |
| §7 multi-pane | 同 window 兩個 pane 都跑 codex → kill 其中一個 | 該 pane lights 變 error，另一個 pane 不受影響 |
| §8 cross-provider | codex running → user 切到該 session 跑 cc → cc 啟動後 kill 原 codex pid | 不誤觸 error（codex active entry 已被 reconcile 清掉）|

**Acceptance**：5 個必驗場景全 PASS（含 §5 clear 路徑 — per plan-review P1-2），留下 log evidence；不必驗證附加場景（test 已覆蓋）。

**估計**：1-2 小時 manual。

**依賴**：P2-T1-T7 全部 commit。

---

### Phase 2 完成檢核

- [ ] `go test ./...` 全綠
- [ ] `go test -race ./...` 全綠
- [ ] `pnpm lint && pnpm build` clean
- [ ] drift test 通過 + 失敗訊息有意義
- [ ] dev log 4 類 line 都產出
- [ ] mlab live §1-§5 全 PASS（含 daemon-restart pane-gone clear 路徑）

---

## 3. Subagent 派發策略（per `feedback_subagent_tdd_priority`）

每個 task 派 subagent 跑 TDD：
1. 讀 spec 對應段落 + plan task description
2. 寫 failing test 先
3. 寫最小 production code 讓 test pass
4. refactor / cleanup
5. 跑 `go test -race`（dispatcher 相關）+ `go build`
6. commit（per CLAUDE.md「每個 task 獨立 commit」）

Subagent prompt 模板：

```
你是 W6-3 PR Phase X 的 P{X-Y} task 實作 subagent。

Worktree: /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-w6-3-codex-error
Spec: docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md（v6 final）
Plan: docs/specs/2026-04-29-w6-3-codex-error-probe-intent-plan.md

Task: P{X-Y}
目標: <copy from plan>
改動檔案: <copy from plan>
Test: <copy from plan>
Acceptance: <copy from plan>

務必：
1. 每個 Bash 命令前綴 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-w6-3-codex-error && `（per feedback_subagent_cwd_enforcement）
2. Edit/Write 用絕對路徑（per feedback_worktree_absolute_path）
3. TDD：failing test 先、minimal impl 後
4. 完成後 commit message: "<type>(<scope>): <task name>"
5. 不主動 push（push 由主 session 控制）
6. 報告：所有改動的檔案 + 跑過的 test + 任何 spec 解讀疑問
```

主 session 職責：
- 監控 subagent 進度
- 驗證每 task commit 內容（`git diff` 檢查）
- 跨 task integration regression 檢查
- mlab live verify P2-T8（不派 subagent，手動操作）

---

## 4. PR 拆 / merge 流程（per CLAUDE.md）

1. Phase 1 + Phase 2 全 commit 完，跑 `go test ./...` + `pnpm lint && pnpm build` 全綠
2. PR body 起草（含 spec / plan 連結 + test plan + 14 task summary）
3. 委派 codex round 1 標準 review（`/codex:review --base origin/main --background`）
4. 收斂 round 1 finding；要修就修，要 defer 就建 issue
5. 委派 codex round 2 三平行 adversarial（攻擊 / 防守 / 體質）— focus 文已在 spec 階段累積；prompt 強調 P1-T2 mechanical extraction 行為一致性 + lifecycle race 全 case
6. 收斂 round 2 finding 直到 0 P1
7. squash merge → main
8. 起 bump PR alpha.262（VERSION + package.json + spa/package.json + CHANGELOG.md）

---

## 5. Test plan summary

### Unit test（隔離測試）
- ProbeIntent types（P1-T1）：3-5 tests
- applyProbeGuards 7 個 guard test + ScreenChange regression matrix 5 case（P1-T2）：12 tests
- lookupTopFrame helper（P1-T3）：3 tests
- Dispatcher lifecycle 5 case（P1-T4）：5 tests
- Reconcile 4 case（P1-T4）：4 tests
- Probe-applied teardown（P1-T4）：2 tests
- Stale-callback / generation（P1-T4）：3 tests
- Replay race（P1-T4, P1-T6）：2-3 tests
- manageActivityWatch / rename / Stop（P1-T5）：4 tests
- replayStatus（P1-T6）：3 tests
- HasPane（P2-T1）：4 tests
- ProcessDead detector（P2-T2）：8 tests
- Provider ProbeIntents + OnSignal（P2-T3）：5 tests
- Drift（P2-T5）：3 tests
- Observability — dev log + TraceStore + expvar（P2-T6）：7 tests

### Integration test（跨層）
- codex detector + dispatcher（P2-T4）：1 test
- 完整 broadcast path 兩條 mapping + replay-real-detector 兩路徑（P2-T7）：7 tests

**Test 總計**：~80+ tests。

### Live verify
- mlab §1-§5：5 必驗場景（含 daemon restart + pane gone clear path — per plan-review P1-2）

---

## 6. Known issues / risks

### 已知接受限制（non-blocking for ship）

1. **Idle session pane gone（spec §6.4 / §8.1 #4）**：codex 走完 PdxStop 進 idle 後 pane close，ProbeIntent 不 armed → 不會自動轉 clear（由 sweep / SessionEnd hook 處理）— 不在 W6-3+W6-4 scope
2. **codex /exit 不發 SessionEnd hook**：documented in audit / `docs/research/2026-03-19:159`；W6-4 處理 pane close 場景，但 codex `/exit` 後 user 不 close pane 仍是 idle 永久（非本 PR scope）

### 風險與對策

| 風險 | 機率 | 影響 | 對策 |
|---|---|---|---|
| `applyProbeGuards` mechanical extraction 不夠 mechanical（行為微變） | medium | high (regression) | P1-T2 嚴格跑既有 ScreenChange test 全綠；codex review 對 mechanical extraction 二次 verify |
| ProbeIntent lifecycle 還有 corner case 未列（spec convergence 5 → 1 trend） | medium | medium | plan / 實作 / PR codex review 三層把關；known issue 化追蹤 |
| frame projection top frame 不存在（罕見：projection rebuild 失敗）| low | low | dispatcher skip arm，下個 hook 重 hydrate 後 applyStatus 再試 — 已 spec |
| daemon restart 後 stale frame 立即 emit error（spec §6.4 — accept） | low | low (符合 #698 修復目標) | accept；mlab live §4 驗證 |
| 1Hz polling 對 idle daemon 額外 cost | very low | very low | accept（成本可忽略；spec §4.2 已論證） |

### Rollback 計畫

若 ship 後發現 regression：
1. revert merge commit
2. 起 hotfix bump
3. issue 追蹤 root cause
4. 重新 plan 修法 → 重新 PR

---

## 7. Bump PR

Phase 2 PR squash merge 後獨立 bump PR：
- VERSION: alpha.261 → alpha.262
- spa/package.json + package.json 同步
- CHANGELOG.md 加：
  ```
  ### lights rebuild
  - **W6-3 + W6-4**: codex error / clear ad-hoc ProbeIntent
    - codex 進程被 SIGKILL（pane 仍存在）→ lights 自動變紅（W5-4 修復）
    - codex pane 被關閉（process 同時退出）→ lights 自動變灰（W5-5 修復）
    - daemon restart 後仍能正確發現 codex 已死亡（issue #698 修復）
    - 引入 `ProbeIntentProvider` interface + 內部 dispatcher（lifecycle / guard / generation token）
  ```
- closes #698（W6 platform prerequisite）；標 W5-4 / W5-5 fixed
- bump PR 用獨立 worktree：`bump-alpha-262`

---

## 8. 完成檢核總表

| Phase | 檢核 | 狀態 |
|---|---|---|
| Phase 1 P1-T1~T6 | 6 task 全 commit + test 全綠 + race 全綠 + ScreenChange regression 0 | ⏳ |
| Phase 2 P2-T1~T7 | 7 task 全 commit + test 全綠 + drift test pass + dev log 完整 | ⏳ |
| Phase 2 P2-T8 | mlab live §1-§5 全 PASS（含 daemon-restart pane-gone clear 路徑）| ⏳ |
| PR + codex review 兩輪 | 0 critical/P1 + known issue 追蹤化 | ⏳ |
| Squash merge → main | branch 刪除 + worktree 清理 | ⏳ |
| Bump PR alpha.262 | merge | ⏳ |
