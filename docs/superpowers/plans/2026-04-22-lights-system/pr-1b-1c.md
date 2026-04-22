# PR-1b-1c — hook/probe/sweep dual-write + divergence + Monitor API passthrough

> Phase：1（Schema + 雙寫過渡）收尾
> 依賴：PR-1b-1b（#583, alpha.205）— Arbitrator goroutine、admission、9-step apply、TraceWriter、AppendSteps、TraceIDMinter/Lookup、arbmode atomic published
> 後續：Phase 2（PR-2a/b/c — frame schema 升級 + authoritative mode + 刪 legacy direct-write）
> Spec 對照：§3.4.2 pending（production entry）、§3.4.3 sweep vs pending、§5.4 retry + pending window、§8.1 divergence 落地、§3.5.1 sampling、#569 Monitor API、#568 trace_id end-to-end、#584 pending production entry
> 關聯 Issue：#568（trace_id correlation end-to-end）/ #569（Monitor API envelope）/ #584（pending production entry，1b-1b 延後項）

## Context

PR-1b-1b 落地 Arbitrator 的執行層但**沒有上游**：hook / probe / sweep 仍走 legacy direct-write path，Arbitrator 的 `in` channel 只在測試裡被送料；`Module.traceLookup` 建好放著沒人用；pending 視窗 production 路徑無入口（#584）。`hookTraceCollector` 繼續用 `trace_id == chain_id` aliasing（#568 仍未關）；monitor API 與 SPA 對新 22 欄 envelope 無知（#569 仍未關）。

PR-1b-1c 把這些缺口一次收完，讓 Arbitrator 真的吃到三來源流量、divergence table 真的被寫、Monitor UI 能看到 envelope 欄位、#568 / #569 / #584 全關。**沒有 frame schema 變動**（留 Phase 2）；passthrough 期間 FramesStore 與 WS broadcast **仍走 legacy path**，Arbitrator 只負責計算 proposal + 寫 divergence + 寫 trace。

**alpha.205 之前已經 ship 的契約全部不動**；本 PR 只新增流量來源 + divergence writer + Monitor envelope 穿透 + Retry + Pending production trigger。

預估 ~1100 LOC 含測試（+ SPA 小段 type 同步）。

## Goals

1. **Hook path dual-write**：`handleEvent` 除既有 `hookTraceCollector` 外，再 build `observation.Observation` 送 `Module.SubmitObservation`；legacy frame path 不變
2. **Hook trace_id 汰換**：`hookTraceCollector` 用 `TraceIDLookup.Get(session, generation)` 取 per-(session, generation) trace_id；miss 則保留 chain_id aliasing + 記 warn（避免 bootstrap regression）。SessionStart hook observation 由 Arbitrator Mint；後續 hook 事件讀取同 gen 的 trace_id → **完成 #568**
3. **Probe path produce observation**：`probe/liveness`、`probe/activity`、`probe/readiness` 的 callback 改為透過 adapter 轉 `Observation` 送 Arbitrator（保留 legacy `applyProbe*` path）
4. **Sweep path produce observation**：`sweepOnce` 對每個 detect-to-end 的 frame 送一筆 `SourceSweep` + `SuggestStatus=ended` observation；legacy `clearFrame` 仍同步跑
5. **Retry + Pending production entry (#584 + spec §5.4)**：
   - Arbitrator apply 增「role 判不出 / evidence 不足」分支：將 observation addPending + `scheduleRetry([100ms, 250ms, 500ms])`
   - `retryCh` 重新納入 1b-1c scope（1b-1b 時延後的）；Arbitrator Run 的 select 加 `case <-retryCh`
   - 3 strike 後 `emitTraceOnly` drop（reason_code=`RetryExhausted`）
6. **Divergence 落地（§8.1）**：Arbitrator 在 passthrough 下把 proposal 投影回舊 schema → 與 legacy frame 比對 → 有差異寫 `frame_divergences` row；已存在 `store.SaveFrameDivergence` 等 API（alpha.192 PR-1a 已 ship divergences 表）—— 確認介面可用，否則補
7. **Monitor API envelope passthrough (#569 backend)**：`MonitorStep` / `monitorStepFromStore` / `MonitorChainSummary` 穿透 PR-1b-0 新 22 欄；加 `schema_version` 提示（defender 建議）；單一 normalize 點處理 `""`/`{}`/`[]` default
8. **SPA type sync (#569 frontend)**：`spa/src/types/` 或對等型別檔增加新欄位；既有 trace list consumer 若有則同步 shape
9. **Sampling（§3.5.1 line 567）**：sweep/synthetic `proposed` 取 1/10；committed 全留。在 apply 的 emit trace 前套

## Non-Goals（明確排除）

- ❌ Frame schema 改 Generation + Actors JSON — **Phase 2 (PR-2a)**
- ❌ Authoritative mode 實際寫 frame — **Phase 2 (PR-2b)**
- ❌ 刪 hook/probe/sweep direct-write legacy path — **Phase 2 (PR-2c)**
- ❌ Trace retention 24h per-session TTL — **Phase 2**
- ❌ Arbitrator daemon restart replay — **Phase 2**
- ❌ Trace viewer SPA UI（decision ports 流程圖）— **PR-6b（trace viewer 專 PR）**
- ❌ Host module observation wiring（host 沒有 hook lifecycle）— **不在 Lights 範圍**

## 設計決策

### D1. Hook path dual-write 架構

**現況**：`handleEvent` 在 `handler.go:68` → `verifyEventFn` → `applyFrameEvent` → `hookTraceCollector` → broadcast。Trace 寫 store 走 `collector.complete()` 把 `chain + steps` 整包 SaveChain。

**改動**：在 `applyFrameEvent` 之後、`broadcast` 之前加一個 `buildObservationFromHook(req, verifyResult, projection) observation.Observation` helper，產 observation 送 `m.SubmitObservation(obs)`。

**TraceID 來源**：
- 如果 `req.EventName == "SessionStart"` → observation 走 Arbitrator apply 路徑會**由 Arbitrator 的 SessionStart helper 執行 Mint**（1b-1b 已實作），此時 Observation.TraceID 可空，Arbitrator apply 後 boundary trace 會帶新 trace_id
- 其他 hook → `traceID, ok := m.traceLookup.Get(req.SessionCode, generation)`；miss 表示 Arbitrator 尚未 Mint 過 → 降級回舊 chain_id 作 traceID，並 log warn（transient bootstrap 期，Arbitrator 正常運作下不該發生）
- Legacy `hookTraceCollector` 的 trace_id 來源同步改：先試 `traceLookup.Get`，miss 才用 chain_id fallback（**完成 #568 的 end-to-end 汰換**）

**Observation 建構**：
- `SourceKind: SourceHook`
- `Action: req.EventName`（e.g. "SessionStart" / "PostToolUse"）
- `Phase: PhaseProposed` 預設；若 verifyResult.Accepted == true 則 `PhaseCommitted`
- `ObservedGeneration`：目前 legacy path 沒有 generation 概念（Phase 2 加欄位）；**暫用 per-session in-memory counter**：`frameState` 已在 Arbitrator 維護，hook observation 設為 `observedGeneration = frameState.sessions[sid].Generation`（需要 hook path 能讀到 — 提供 `Arbitrator.SnapshotGeneration(sid) int64` method 或走 Arbitrator 單向 inference：hook 送 obs 時 ObservedGeneration 留 0 或複製自 Arbitrator 當前 generation —— **選後者**：builder 不強制對齊；generation gate 會自行處理。實務上 observation builder 的 ActorKey.Generation 必須對齊 ObservedGeneration（§pr-1b-1a D3 契約），所以此處需有 generation source）
- **決策**：新增 `Module.currentGenerationFor(sessionID)` helper，從 `frameState` 透過一個公開 snapshot 方法讀當前 generation（thread-safe：atomic.Int64 per session OR mu-guarded map）。或用更簡單的方式：Arbitrator 開放 `CurrentGeneration(sid) int64` method（內部 lock 或 sync.Map）——**選此方案**，Arbitrator 新增 `CurrentGeneration(sid) int64` 只讀 API，hook path 透過 `m.arbitrator.CurrentGeneration(...)` 取
- `Proposal`：`ActorKey{SessionID: sid, Generation: gen, ActorID: "primary:" + agentType}` + `SuggestStatus: projection-inferred-status` + `EndLifecycle: eventName == "SessionEnd"` + `EndReason: "session_end"`
- `Evidence`：`{Key:"pid", Value: req.SenderPID}` + `{Key:"pane_id", Value: req.TmuxPaneID}` + `{Key:"event_name", Value: req.EventName}` + verify reason if failed
- `DecisionPorts`：legacy hook 不帶；留空 slice
- `ObservedAt: time.Now()` / `Seq: monotonic per session`（簡化：time.Now().UnixNano()）

### D2. Probe adapter

**現況**：`probe/activity`、`probe/liveness`、`probe/readiness` 皆有 callback 形式；主要由 `agentcc / codex` 等 provider 在 register 時注入 callback。`module.go` 的 `onActivityDetected` 是 hook 之一。

**改動**：建立 `internal/module/agent/arbitrator/probe_observation.go`（或 `internal/module/agent/observation_builders.go` 放 Module package）— 提供 helper：
```go
func buildProbeObservation(
    sessionID string,
    generation int64,
    agentType string,
    probeID string,
    probeToken string,
    probeKind string,            // "liveness" | "activity" | "readiness"
    probeOutcome string,         // "alive" | "dead" | "active" | "idle" | "ready" | ...
    evidence []observation.EvidenceRef,
) observation.Observation
```

Probe callback 在 fire 時呼叫此 helper 產 observation → `m.SubmitObservation`。

- `SourceKind: SourceProbe`
- `WatcherToken: probeToken`（probe 實例化時 uuid.NewString，存進 provider 自身 state；同一 probeID 的 rotate 由上層 caller 管）
- `Action: "probe.<probeKind>.<probeOutcome>"`
- `Proposal.SuggestStatus` 視 outcome 映射（dead → `ended`、active → `active`、idle → `waiting`、error → `error`）

**WatcherToken rotate**：Arbitrator `frameState.rotateWatcherToken(key, probeID, newToken)` 目前只在測試用；PR-1b-1c 需在 probe 實例化時呼叫（透過 Arbitrator 新增 public API `UpdateWatcherToken(actorKey, probeID, token)` 或更乾淨的做法：probe 產 observation 帶 `WatcherToken`，apply 層的 step 2 watcher check 改為**先 upsert 新 token**再驗證）— **選後者**：簡化 API 面積，watcher identity 由 observation 的 WatcherToken 欄位單向推進（舊 token 的 probe callback 到 Arbitrator 已經晚，reject 正確）。但這破壞 rotation 語意：若兩個 probe 實例並發，舊 probe token 會取代新 token。**實務上 rotate 是 caller 顯式 stop old watch + start new** 的動作，兩實例並發只在 bug 下發生；接受此風險，在 `frame_state.rotateWatcherToken` 的註解寫清楚 Phase 1 語意。

### D3. Sweep path

**現況**：`sweep.go` `sweepOnce()` 對每個 `!isPidAliveFn(frame.PID)` 或 `startTime != frame.ProcessStartTime` 的 frame 呼叫 `clearFrame`。

**改動**：`clearFrame` 前呼叫 `m.SubmitObservation(buildSweepObservation(frame, reason))`；legacy clearFrame 仍跑。Sampling（§3.5.1 line 567）在 emit trace 階段處理（D7）。

Observation shape：
- `SourceKind: SourceSweep`
- `Action: "sweep.frame_cleared"`
- `Phase: PhaseProposed`（sweep 永遠 proposed，不升 committed）
- `Proposal.ActorKey`: 以 `primary:<agent_type>` 或 sessionCode 派生
- `Proposal.EndLifecycle: true; EndReason: reason`
- `Evidence: [{Key:"pid", Value:frame.PID}, {Key:"reason", Value:reason}]`

### D4. Retry scheduler (§5.4) + Pending production entry (#584)

#### D4.1 Pending production trigger

Arbitrator apply step 4 原本只有「sweep 且 actor 已 pending → addPending」的 sweep-override 分支。增加新分支：

```go
// apply.go step 4 revised
func (d *applyDeps) routeToPendingIfUnresolved(obs observation.Observation) (stashed bool) {
    // existing sweep override (keep)
    if obs.SourceKind == observation.SourceSweep && d.pending.isPending(obs.Proposal.ActorKey) {
        d.pending.addPending(obs, d.now(), ...)
        return true
    }
    // new: role-unresolved trigger
    if obs.SourceKind == observation.SourceHook && needsRoleResolution(obs) {
        if d.pending.count() == 0 || !d.pending.isPending(obs.Proposal.ActorKey) {
            d.pending.addPending(obs, d.now(), ...)  // first entry for this actor
            d.scheduleRetry(obs)                      // schedule [100ms, 250ms, 500ms]
            return true
        }
        d.pending.addPending(obs, ...)  // coalesce into existing entry
        return true
    }
    return false
}

// needsRoleResolution — evidence-based judgment
func needsRoleResolution(obs observation.Observation) bool {
    // pid_tree_unresolvable / pane_unresolvable / sender_uncertain → yes
    for _, e := range obs.Evidence {
        if e.Key == "verify_reason" {
            if s, ok := e.Value.(string); ok {
                switch s {
                case "pid_not_in_pane_tree", "pane_unresolvable", "sender_uncertain", "identify_mismatch":
                    return true
                }
            }
        }
    }
    return false
}
```

#### D4.2 Retry scheduler

```go
// arbitrator.go
type retryTick struct {
    Obs      observation.Observation
    Attempt  int  // 1..3
}

// Options 新增
type Options struct {
    ... existing ...
    RetryCh chan retryTick  // cap 256
    RetryDelays []time.Duration  // default [100ms, 250ms, 500ms]
}

// Arbitrator Run select 加 retryCh case
func (a *Arbitrator) Run(ctx context.Context) {
    ticker := time.NewTicker(a.opts.ReconcileEvery)
    defer ticker.Stop()
    for {
        select {
        case obs := <-a.inCh:
            a.deps.apply(obs)
        case tick := <-a.opts.RetryCh:
            a.deps.attemptRetry(tick)
        case <-ticker.C:
            a.reconciler.reconcile()
        case <-ctx.Done():
            // drain
            ...
        }
    }
}
```

**scheduleRetry** 用 `time.AfterFunc` 排程（不阻塞 caller）：
```go
func (d *applyDeps) scheduleRetry(obs observation.Observation) {
    for i, delay := range d.retryDelays {
        attempt := i + 1
        time.AfterFunc(delay, func() {
            select {
            case d.retryCh <- retryTick{Obs: obs, Attempt: attempt}:
            default:
                metrics.Inc("lights_arb_retry_dropped")
            }
        })
    }
}
```

**attemptRetry**：
```go
func (d *applyDeps) attemptRetry(tick retryTick) {
    entry, ok := d.pending.getPendingEntry(tick.Obs.Proposal.ActorKey)
    if !ok { return }  // pending 已被清（SessionStart or deadline flush）
    // re-verify（此處 1b-1c 用 hook path 的 verify 結果，透過 observation Evidence 帶回）
    // 若仍未解 → wait next attempt
    // 若已達 attempt=3 仍未解 → flushPendingDue 的 deadline 會走 drop path (PidTreeUnresolvable)
    // Promote 邏輯：Arbitrator 收到後續 observation 若對同 ActorKey 有 clear evidence → 正常 apply 時會 coalesce
    // 實際 "promote" 靠後續 observation trigger — attemptRetry 本身只是 heartbeat
    // 簡化：attemptRetry 記 trace reason_code=RetryAttempt{N}; 不做 promote logic
}
```

#### D4.3 tryPromoteToActor 契約（1b-1b 佔位，1b-1c 定義）

`pendingStore.flushPendingDue` 的 `tryPromote func(*PendingEntry) bool`：
- 1b-1b 永遠回 false（走 drop path）
- 1b-1c 改為：取 entry.Observations 最後一筆，若該 obs 有明確 proposal（`Proposal.SuggestStatus != ""` 或 `EndLifecycle == true`）且 evidence 不含未解 key（不含 pane_unresolvable/sender_uncertain）→ promote：直接套用 proposal 到 frameState（走 apply 後續 step 5-9）+ return true

### D5. Divergence writer（§8.1）

#### D5.1 已存在的 divergences API

`internal/store/divergences.go`（alpha.192 PR-1a 已 ship）應已有 `SaveFrameDivergence` 或對等方法；本 PR 確認介面可用。若尚無 append API，補一個：
```go
func (s *DivergencesStore) Insert(row FrameDivergence) error
```

FrameDivergence shape 對應 spec §8.1 schema（session_id / trace_id / event_id / observed_generation / old_state_ref / proposal_state_ref / diff_summary / matched / reason_code / created_at）。

#### D5.2 Arbitrator 整合

`applyDeps` 加 `divergences DivergencesWriter` 與 `frames LegacyFramesView`（從 FramesStore 讀當前 legacy frame state）依賴：

```go
type DivergencesWriter interface {
    Insert(row store.FrameDivergence) error
}

type LegacyFramesView interface {
    GetByIdentity(paneID string, pid int, startTime string) (*store.Frame, error)
}
```

Apply 的 step 8 mode branch — passthrough 路徑加 divergence 寫入：
```go
// apply.go step 8 (passthrough branch, after pre-gate mode check moved to step 0b)
func (d *applyDeps) writeDivergenceIfAny(obs observation.Observation) {
    if obs.Proposal.ActorKey.ActorID == "" { return }  // no proposal to project
    // project Arbitrator proposal down to legacy Frame shape
    projected := projectProposalToLegacy(obs.Proposal)
    // read legacy frame
    legacyFrame, err := d.frames.GetByIdentity(paneID, pid, startTime)  // pane/pid/startTime from evidence
    if err != nil || legacyFrame == nil { return }
    if framesMatch(projected, *legacyFrame) { return }  // no divergence
    // write divergence row
    _ = d.divergences.Insert(store.FrameDivergence{
        SessionID: obs.SessionID,
        TraceID: obs.TraceID,
        EventID: obs.SpanID,
        ObservedGeneration: obs.ObservedGeneration,
        OldStateRef: mustJSON(legacyFrame),
        ProposalStateRef: mustJSON(projected),
        DiffSummary: humanDiff(legacyFrame, projected),
        Matched: 0,
        ReasonCode: obs.ReasonCode,
        CreatedAt: d.now().UnixNano(),
    })
    metrics.Inc("lights_divergence_total", "matched=0")
}
```

Divergence 寫失敗只 log，不影響 apply（passthrough 本身就是 observability 層）。

### D6. Monitor API envelope (#569 backend)

`monitor.go` `MonitorStep` 加 16 個新欄位（對應 `store.TraceStep` 的 lights envelope fields）。`monitorStepFromStore` 同步填。

新增欄位清單（與 `store.TraceStep` 對齊）：`source_kind` / `action` / `reason_code` / `outcome` / `scenario_key` / `observed_generation` / `decision_ports`（JSON string）/ `phase` / `status` / `watcher_token` / `trace_id` / `reason_text` / `attrs` / `input_refs` / `output_refs` / `state_before_ref` / `state_after_ref` / `evidence_refs` / `started_at` / `ended_at` / `otel_kind`

**Normalize**：`""` 保留 `""`；JSON 欄位（attrs/input_refs/output_refs/evidence_refs/decision_ports）空時 `monitorRawJSON` 回 `"null"` — 保留現有行為；**加 `schema_version: "1.0.0-lights-1b"` 欄位到 chain summary response**（defender 建議）便於 SPA 偵測 store rollback。

### D7. Sampling（§3.5.1 line 567）

`apply.go` emitAcceptedTrace / emitRejectedTrace 前加 sampler：
```go
type sampler struct {
    mu      sync.Mutex
    counts  map[observation.SourceKind]int  // modulo 10 counter
}

// ShouldEmit: committed 全 true；sweep/synthetic proposed 每 10 取 1；其他 proposed 全 true
func (s *sampler) ShouldEmit(source observation.SourceKind, phase observation.ObsPhase) bool {
    if phase == observation.PhaseCommitted { return true }
    if source != observation.SourceSweep && source != observation.SourceSynthetic { return true }
    s.mu.Lock()
    defer s.mu.Unlock()
    c := s.counts[source]
    s.counts[source] = c + 1
    return c % 10 == 0
}
```

被 sample 掉的仍計 metric `lights_trace_sampled_dropped{source,phase}`。

### D8. SPA type sync (#569 frontend)

`spa/src/` 搜 `MonitorStep` type / `agent/monitor` fetcher。更新 interface 含新欄位（optional string 為主）。若有 `useMonitorChain` / `useMonitorStep` hook 顯示舊 shape，**1b-1c 只 sync type，不動 UI rendering**（trace viewer UI 是 PR-6b）。

### D9. Hook path dual-write 的失敗語意

若 `m.SubmitObservation` 因 inCh 滿載 drop：legacy frame path 仍跑，使用者可見行為不變；只是 Arbitrator 少一筆 observation → divergence table 少一筆比對。metric `lights_arb_in_dropped` 已在 1b-1b 記錄。可接受。

若 legacy frame path 失敗而 observation 已送：Arbitrator 會計算 proposal；divergence 寫入時讀 legacy frame state 可能是舊值或 nil → diff_summary 顯示「legacy_write_failed」這類標記即可。接受。

### D10. ObservedGeneration 來源

`Arbitrator.CurrentGeneration(sessionID string) int64` — 新增 thread-safe public method。內部讀 `frameState.sessions[sessionID].Generation`；需要 **加一個 atomic read path**（frameState 是單 goroutine owner，無 lock；此方法跨 goroutine 呼叫）。

**實作**：frameState 改為**每個 sessionGen 的 Generation 用 `atomic.Int64`**，讓外部只讀操作免持 lock：

```go
type sessionGen struct {
    Generation atomic.Int64   // publicly readable via CurrentGeneration
    Actors     map[observation.ActorKey]*actorSummary
}
```

Arbitrator goroutine 內寫 Generation 用 `Store`；hook path 讀用 `Load`。其他 actorSummary 欄位仍單 owner 無鎖。

若 session 不存在 → 回 0（等同「尚未 SessionStart」）。

## Tasks（staged parallelism）

| Stage | Task | 依賴 |
|---|---|---|
| **Stage 1** | T1（Arbitrator 新 public API：CurrentGeneration + RetryCh + retryTick）, T2（sampler）, T3（divergences store 介面確認/補） | 無 |
| **Stage 2** | T4（observation builder helpers: hook / probe / sweep）, T5（apply pipeline: pending production trigger + scheduleRetry + attemptRetry + tryPromote 實作 + divergence writer + sampler 接入） | T1/T2/T3 |
| **Stage 3** | T6（hook path wiring: handler.go + trace.go + trace_id lookup）, T7（probe wiring: probe.go + provider register callback）, T8（sweep wiring） | T4/T5 |
| **Stage 4** | T9（Monitor API envelope + schema_version）, T10（SPA type sync） | 無（與 Stage 1-3 可 parallel） |

### Task 1 — Arbitrator 新 public API

**檔案**：
- `internal/module/agent/arbitrator/arbitrator.go`（改）
- `internal/module/agent/arbitrator/arbitrator_test.go`（改）
- `internal/module/agent/arbitrator/frame_state.go`（改：Generation → atomic.Int64）
- `internal/module/agent/arbitrator/frame_state_test.go`（改）
- `internal/module/agent/arbitrator/types.go`（改：加 retryTick）

**契約**：
```go
// arbitrator.go
func (a *Arbitrator) CurrentGeneration(sessionID string) int64

// Options
type Options struct {
    ... existing ...
    RetryDelays []time.Duration  // default [100ms, 250ms, 500ms]
    RetryChCap int               // default 256
}

// types.go
type retryTick struct {
    Obs     observation.Observation
    Attempt int
}
```

**TDD**：
- `TestArbitrator_CurrentGeneration_UnknownSession_ReturnsZero`
- `TestArbitrator_CurrentGeneration_AfterSessionStart_ReturnsNewGen`
- `TestArbitrator_CurrentGeneration_ConcurrentRead_NoRace`
- `TestFrameState_Generation_AtomicReadWrite`

### Task 2 — sampler

**檔案**：
- `internal/module/agent/arbitrator/sampler.go`（新）
- `internal/module/agent/arbitrator/sampler_test.go`

**TDD**：
- `TestSampler_Committed_AlwaysEmits`
- `TestSampler_Proposed_Hook_AlwaysEmits` — hook/probe proposed 不 sample
- `TestSampler_Proposed_Sweep_1In10`
- `TestSampler_Proposed_Synthetic_1In10`
- `TestSampler_ConcurrentCounters_NoRace`

### Task 3 — Divergences store 介面確認

**檔案**：
- `internal/store/divergences.go`（改如需）
- `internal/store/divergences_test.go`（改如需）

**檢查項**：
- `DivergencesStore` 是否有 `Insert(row FrameDivergence) error` 方法？若無則補
- `FrameDivergence` struct 是否涵蓋 §8.1 schema 所有欄位？若缺則補

**TDD**（若新增 Insert）：
- `TestDivergencesStore_Insert_Roundtrip`
- `TestDivergencesStore_Insert_IdempotencyKey_NoDuplicate`（session_id, trace_id, event_id, observed_generation tuple DO NOTHING）

### Task 4 — Observation builder helpers

**檔案**：
- `internal/module/agent/observation_builders.go`（新）— `buildHookObservation` / `buildProbeObservation` / `buildSweepObservation`
- `internal/module/agent/observation_builders_test.go`

**TDD**：
- `TestBuildHookObservation_FullShape`
- `TestBuildHookObservation_SessionStart_EmptyTraceID_ArbitratorMints`
- `TestBuildHookObservation_VerifyFailed_EvidenceContainsReason`
- `TestBuildProbeObservation_OutcomeMapping_DeadToEnded`
- `TestBuildProbeObservation_WatcherTokenSet`
- `TestBuildSweepObservation_PidDead_ProposalEndLifecycle`

### Task 5 — Apply pipeline 擴充

**檔案**：
- `internal/module/agent/arbitrator/apply.go`（改 — pending production trigger + scheduleRetry + attemptRetry + tryPromote + divergence writer + sampler wiring）
- `internal/module/agent/arbitrator/apply_test.go`（補 tests）
- `internal/module/agent/arbitrator/arbitrator.go`（改 — Run select 加 retryCh case + drain 時同步處理 retryCh 剩餘）

**TDD（新增）**：
- `TestApply_HookUnresolved_CreatesFirstPendingEntry_SchedulesRetry` — hook obs 帶 verify_reason=pane_unresolvable → addPending + scheduleRetry 呼叫（用 fake retryCh）
- `TestApply_HookResolved_NotPending` — verify reason not unresolved → 不 addPending
- `TestApply_AttemptRetry_PendingPromotedOnSuccess`
- `TestApply_AttemptRetry_PendingStillUnresolved_NoPromoteWaitDeadline`
- `TestApply_TryPromote_SuccessfulPromotion_CallsApplyProposalPath`
- `TestApply_TryPromote_NoEvidence_ReturnsFalse`
- `TestApply_Passthrough_DivergenceWritten_WhenLegacyDiffers`
- `TestApply_Passthrough_NoDivergenceWrite_WhenMatches`
- `TestApply_Sampler_Sweep_DropsNineInTen`

### Task 6 — Hook path wiring

**檔案**：
- `internal/module/agent/handler.go`（改 — handleEvent 在 applyFrameEvent 後 SubmitObservation）
- `internal/module/agent/trace.go`（改 — `traceID` 來源先試 TraceIDLookup）
- `internal/module/agent/handler_test.go`（改）

**TDD**：
- `TestHandleEvent_AfterApply_SubmitsObservation`
- `TestHandleEvent_SessionStart_SubmitsObservationWithEmptyTraceID` — Mint 在 apply 階段
- `TestHandleEvent_VerifyFailed_SubmitsObservationWithFailureEvidence`
- `TestHookTraceCollector_TraceIDFromLookup_WhenAvailable`
- `TestHookTraceCollector_TraceIDFallsBackToChainID_WhenMinterNotYetCalled`
- `TestHandleEvent_SubmitObservationDrop_LegacyPathStillRuns` — inCh 滿載；legacy broadcast + trace 仍跑

### Task 7 — Probe path wiring

**檔案**：
- `internal/module/agent/module.go`（改 — onActivityDetected / onReadinessDetected callback 產 observation + submit）
- `internal/agent/probe/*.go`（**不改 probe 本身**；只讓 Module 在收到 probe callback 時 submit）
- `internal/module/agent/module_test.go`（改）

**TDD**：
- `TestProbeActivity_SubmitsObservation`
- `TestProbeLiveness_Dead_SubmitsEndLifecycleObservation`
- `TestProbeReadiness_Ready_SubmitsWatcherTokenMatchingObservation`

### Task 8 — Sweep path wiring

**檔案**：
- `internal/module/agent/sweep.go`（改 — clearFrame 前 submit observation）
- `internal/module/agent/sweep_test.go`（改）

**TDD**：
- `TestSweep_PidDead_SubmitsObservation_BeforeClearFrame`
- `TestSweep_PidReused_SubmitsObservationWithReusedReason`
- `TestSweep_SubmitFails_ClearFrameStillRuns`

### Task 9 — Monitor API envelope (#569 backend)

**檔案**：
- `internal/module/agent/monitor.go`（改）
- `internal/module/agent/monitor_test.go`（改）

**TDD**：
- `TestMonitorStep_IncludesAllEnvelopeFields`
- `TestMonitorStep_NormalizeEmptyJSON_ToNull`
- `TestMonitorChainSummary_IncludesSchemaVersion`
- `TestMonitorProjection_UnchangedShape` — 回歸測試

### Task 10 — SPA type sync (#569 frontend)

**檔案**：
- `spa/src/types/monitor.ts`（或實際路徑 — 先用 `grep MonitorStep spa/src` 確認）
- 相關 consumer fetcher type 更新

**TDD**：
- 無 unit test（純 type）；`pnpm run build` 綠即可
- 若有 type smoke test（e.g. `spa/src/types/monitor.test.ts`）更新

## 檔案變動總覽

| 檔 | 動作 | Task |
|---|---|---|
| `internal/module/agent/arbitrator/arbitrator.go` | 改 | 1, 5 |
| `internal/module/agent/arbitrator/arbitrator_test.go` | 改 | 1, 5 |
| `internal/module/agent/arbitrator/frame_state.go` | 改 | 1 |
| `internal/module/agent/arbitrator/frame_state_test.go` | 改 | 1 |
| `internal/module/agent/arbitrator/types.go` | 改 | 1 |
| `internal/module/agent/arbitrator/sampler.go` | 新 | 2 |
| `internal/module/agent/arbitrator/sampler_test.go` | 新 | 2 |
| `internal/store/divergences.go` | 改如需 | 3 |
| `internal/store/divergences_test.go` | 改如需 | 3 |
| `internal/module/agent/observation_builders.go` | 新 | 4 |
| `internal/module/agent/observation_builders_test.go` | 新 | 4 |
| `internal/module/agent/arbitrator/apply.go` | 改 | 5 |
| `internal/module/agent/arbitrator/apply_test.go` | 改 | 5 |
| `internal/module/agent/handler.go` | 改 | 6 |
| `internal/module/agent/trace.go` | 改 | 6 |
| `internal/module/agent/handler_test.go` | 改 | 6 |
| `internal/module/agent/module.go` | 改 | 7 |
| `internal/module/agent/module_test.go` 或對應 | 改 | 7 |
| `internal/module/agent/sweep.go` | 改 | 8 |
| `internal/module/agent/sweep_test.go` | 改 | 8 |
| `internal/module/agent/monitor.go` | 改 | 9 |
| `internal/module/agent/monitor_test.go` | 改 | 9 |
| `spa/src/types/monitor.ts`（或 grep 確認） | 改 | 10 |

**預估**：~1100 LOC（Go ~1000 + SPA ~100）。

## Verification

### 單元測試
```bash
cd .claude/worktrees/lights-pr-1b-1c
go test -race -count=3 ./internal/module/agent/... ./internal/store/...
go test -race -count=1 ./...
go vet ./...
go build ./...

cd spa
pnpm run lint
pnpm run build
npx vitest run
```

### 手動驗證
1. 啟 daemon；啟 SPA；開一個 tmux pane + Claude Code session
2. 觸發 hook（SessionStart + PostToolUse）
3. `curl http://127.0.0.1:7860/api/agent/monitor/chains?limit=5 | jq` — trace step 含 trace_id 非空、source_kind=hook、對應 envelope 欄位
4. `sqlite3 <db> "SELECT COUNT(*) FROM frame_divergences"` — 有 row（matched=1 與 matched=0 都可能，端視 projection 是否對齊）
5. Arbitrator log 有 `[agent][arbitrator] Run started`，reconcile tick 固定觸發
6. 連續 PUT `/api/config '{"agent":{"arb_mode":"authoritative"}}'` + 觸發 SessionStart → 所有 hook obs 都被 fail-closed reject（trace 含 `AuthoritativeNotSupportedPhase1`）；無 frame_divergences 寫入（authoritative path 不走 divergence）

### 手動驗證 SPA
- Open Monitor page（若已實作）→ trace list 顯示新欄位（或至少 type 不 crash；UI rendering PR-6b 接）

## Rollout / Rollback

- **Rollout**：merge 後 daemon 會開始 dual-write，divergences 表開始寫。使用者可見行為不變（legacy path 仍主導 FramesStore 與 broadcast）
- **Rollback**：revert 整 PR；legacy direct-write path 未動，立即回復舊行為
- **Monitor**：`sqlite3 <db> "SELECT matched, COUNT(*) FROM frame_divergences GROUP BY matched"` 觀察 divergence rate；若 matched=0 比例過高代表 projection 邏輯有 bug（trigger rollback 或 iterate projection code）

## 已知 follow-up（1b-1c 不做）

| # | 項目 | 歸屬 |
|---|---|---|
| 1 | Frame schema 升 Generation + Actors JSON | Phase 2 (PR-2a) |
| 2 | Authoritative mode 實際寫 frame + broadcast | Phase 2 (PR-2b) |
| 3 | 刪 hook/probe/sweep direct-write legacy path | Phase 2 (PR-2c) |
| 4 | Trace retention 24h per-session TTL | Phase 2 |
| 5 | Daemon restart frame / actor state replay | Phase 2 |
| 6 | Trace viewer SPA UI（decision ports 流程圖）| PR-6b |
| 7 | `AGENT_ARB_MODE=authoritative` 真 frame write（1b-1c 仍 fail-closed）| PR-2b |

**#568 / #569 / #584 何時可關**：本 PR merge 後即可關。
