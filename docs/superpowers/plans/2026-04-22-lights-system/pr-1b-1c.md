# PR-1b-1c — hook/probe/sweep dual-write + divergence + Monitor API passthrough

> Phase：1（Schema + 雙寫過渡）收尾
> 依賴：PR-1b-1b（#583, alpha.205）— Arbitrator goroutine、admission、9-step apply、TraceWriter、AppendSteps、TraceIDMinter/Lookup、arbmode atomic published
> 後續：Phase 2（PR-2a/b/c — frame schema 升級 + authoritative mode + 刪 legacy direct-write）
> Spec 對照：§3.4.2 pending（production entry）、§3.4.3 sweep vs pending、§5.4 retry + pending window、§8.1 divergence 落地、§3.5.1 sampling、#569 Monitor API、#568 trace_id end-to-end、#584 pending production entry
> 關聯 Issue：#568（trace_id correlation end-to-end）/ #569（Monitor API envelope）/ #584（pending production entry，1b-1b 延後項）

## Context

PR-1b-1b 落地 Arbitrator 的執行層但**沒有上游**：hook / probe / sweep 仍走 legacy direct-write path，Arbitrator 的 `in` channel 只在測試裡被送料；`Module.traceLookup` 建好放著沒人用；pending 視窗 production 路徑無入口（#584）。`hookTraceCollector` 繼續用 `trace_id == chain_id` aliasing（#568 仍未關）；monitor API 與 SPA 對新 22 欄 envelope 無知（#569 仍未關）。

PR-1b-1c 把這些缺口一次收完，讓 Arbitrator 真的吃到三來源流量、divergence table 真的被寫、Monitor UI 能看到 envelope 欄位、#568 / #569 / #584 全關。**沒有 frame schema 變動**（留 Phase 2）；passthrough 期間 FramesStore 與 WS broadcast **仍走 legacy path**，Arbitrator 只負責計算 proposal + 寫 divergence + 寫 trace。

**alpha.205 之前已經 ship 的契約全部不動**；本 PR 只新增流量來源 + divergence writer + Monitor envelope 穿透 + Pending production trigger（**無 active retry scheduler** — Non-Goals）。

預估 ~1250 LOC 含測試（+ SPA 小段 type 同步；probe 擴 Watcher API 相較原估 +150 LOC）。

## Goals

1. **Hook path dual-write**：`handleEvent` 在 `verifyEventFn` 結果之後（不論 accept / reject）都 build `observation.Observation` 送 `Module.SubmitObservation`；legacy frame path（applyFrameEvent / broadcast / 202 early-return）全部不變
2. **Hook trace_id 汰換**：
   - hook path **在送 Arbitrator 前自產 provisional UUID**（每筆 hook 事件獨立 mint，無需讀狀態），做為 `Observation.TraceID` 值以繞過 builder 非空驗證（`builder.go:142`）
   - Arbitrator apply `SessionStart` 時改走 **`AdoptTraceID(sid, nextGen, seed)`**——沿用 observation 帶來的 provisional UUID 做為該 (session, gen) 的公共 trace_id；非 SessionStart 事件透過 `TraceIDLookup.Get(session, generation)` 讀已 adopted 值
   - `hookTraceCollector` 的 chain trace_id 同步改：先試 `traceLookup.Get`，miss 才用 chain_id fallback 並記 warn（transient bootstrap 期）→ **完成 #568**
3. **Probe path produce observation**：`probe/liveness`、`probe/activity`、`probe/readiness` callback 透過 adapter 轉 `Observation` 送 Arbitrator；**probe package 增 start/stop + token rotation 契約**讓 Module 能顯式 rotate watcher token；legacy `applyProbe*` path 保留
4. **Sweep path produce observation**：`sweepOnce` 對每個 detect-to-end 的 frame 送一筆 `SourceSweep` + `SuggestStatus=ended` observation；legacy `clearFrame` 仍同步跑
5. **Pending production entry (#584 + spec §5.4 pending window)**：
   - Arbitrator apply 增「role-resolution 未定」分支：observation evidence 帶 `role_resolution: RoleRetryableUnresolved` 時 addPending（first entry or coalesce）
   - `tryPromoteToActor` 在 `flushPendingDue` 觸發時檢查 entry.Observations，最後一筆含 `role_resolution: RoleResolved` + 合法 proposal → 走 apply 後續 step 5-9；否則 deadline drop
   - **不做** retry scheduler：pending 只靠 `flushPendingDue` 的 deadline 驅動 + 後續 observation coalescing promote；active retry 延後到後續 PR（Non-Goals）
6. **Divergence 落地（§8.1）**：Arbitrator 在 passthrough 下把 **primary-only** proposal 投影回舊 schema → 與 legacy frame 比對 → 有差異寫 `frame_divergences` row（subagent / proxy divergence 延後到 Phase 2）
7. **Monitor API envelope passthrough (#569 backend)**：`MonitorStep` / `monitorStepFromStore` / `MonitorChainSummary` 穿透 PR-1b-0 新 22 欄；加 `schema_version` 提示（defender 建議）；單一 normalize 點處理 `""`/`{}`/`[]` default
8. **SPA type sync (#569 frontend)**：`spa/src/types/` 或對等型別檔增加新欄位；既有 trace list consumer 若有則同步 shape
9. **Sampling（§3.5.1 line 567）**：sweep/synthetic `proposed` 取 1/10；committed 全留。在 apply 的 emit trace 前套，**無鎖**（apply 單 goroutine owner）

## Non-Goals（明確排除）

- ❌ Frame schema 改 Generation + Actors JSON — **Phase 2 (PR-2a)**
- ❌ Authoritative mode 實際寫 frame — **Phase 2 (PR-2b)**
- ❌ 刪 hook/probe/sweep direct-write legacy path — **Phase 2 (PR-2c)**
- ❌ Trace retention 24h per-session TTL — **Phase 2**
- ❌ Arbitrator daemon restart replay — **Phase 2**
- ❌ Trace viewer SPA UI（decision ports 流程圖）— **PR-6b（trace viewer 專 PR）**
- ❌ Host module observation wiring（host 沒有 hook lifecycle）— **不在 Lights 範圍**
- ❌ **Retry scheduler**（`scheduleRetry` / `attemptRetry` / `retryCh` / `retryTick` / `RetryDelays`）— 延後到後續 PR；本 PR 的 pending 只靠 deadline flush + coalescing promote。TODO 留在 apply.go 與 spec §5.4 註記
- ❌ **Subagent / proxy divergence projection** — Phase 2；本 PR divergence 僅對 `Proposal.ActorKey.ActorID` 開頭為 `"primary:"` 者寫入

## 設計決策

### D1. Hook path dual-write 架構

**現況**：`handleEvent` 在 `handler.go:68` → `verifyEventFn` → （accept 分支）`applyFrameEvent` → `hookTraceCollector` → broadcast；reject 分支直接 202 early-return。Trace 寫 store 走 `collector.complete()` 把 `chain + steps` 整包 SaveChain。

**改動要點**（P0-1 / P0-2 / P0-3 整合）：

#### D1.1 Dual-write 位置（P0-2）

Submit observation 移到 **`verifyEventFn` 結果之後的共用邊界**，也就是說：

```
handleEvent:
    verifyResult := verifyEventFn(req)
    obs := buildHookObservation(req, verifyResult)      // ★ 共用邊界
    m.SubmitObservation(obs)                             // ★ accept / reject 都送
    if !verifyResult.Accepted {
        // 原 reject 分支（202 + legacy trace reject row）
        return 202
    }
    applyFrameEvent(...)  // legacy 不動
    hookTraceCollector.complete(...)
    broadcast(...)
```

- reject 分支也建 + submit observation；evidence 帶 `verify_reason`（drop/reject 原因）
- accept 分支 observation `Phase = PhaseCommitted`；reject 分支 `Phase = PhaseProposed`
- 若 `SubmitObservation` inCh 滿載 drop（1b-1b 行為），legacy 仍跑 — 使用者可見行為不變，D9 細節不變

#### D1.2 TraceID 來源（P0-1 — hook 端產 provisional UUID，Arbitrator adopt）

`Observation.Builder.Build()`（`builder.go:142`）強制 `TraceID != ""`，不改 builder。改 hook 端自帶 seed：

- **所有 hook observation 都在建構時 mint 一個 provisional UUID 做 `Observation.TraceID`**（`uuid.NewString()`，每筆獨立；不讀任何狀態，保證非空）
- Arbitrator apply `SessionStart` 的 SessionStart helper **改用 `AdoptTraceID(sid, nextGen, seed)`**：
  - 若該 (sessionID, nextGen) 的 trace_id 已存在 → 直接回舊值（冪等；例如 idempotent replay）
  - 否則以 `seed` 為值寫入 registry，回寫的 trace_id 即 seed
  - 實作位置：`internal/module/agent/observation/trace_id.go`（既有）— 在 `TraceIDMinter` interface 加 method，private `traceIDRegistry` 實作：
    ```go
    // TraceIDMinter 加新 method
    type TraceIDMinter interface {
        Mint(sessionID string, generation int64) string
        AdoptTraceID(sessionID string, generation int64, seed string) string  // new
        PruneSessionBefore(sessionID string, generation int64)                // existing
    }

    // traceIDRegistry.AdoptTraceID：seed 非空且 key 未存在 → 以 seed 寫入並回 seed；否則回已存在值（冪等）；seed 空字串 → panic 或回退 Mint（實作決定，tests 鎖行為）
    func (r *traceIDRegistry) AdoptTraceID(sessionID string, generation int64, seed string) string
    ```
  - Arbitrator SessionStart helper 內原先呼叫 `minter.Mint(sid, nextGen)` 改成 `minter.AdoptTraceID(sid, nextGen, obs.TraceID)`；boundary synthetic trace 用 adopted 值
- 非 SessionStart hook 事件：Observation.TraceID 帶 provisional UUID **與 (session, gen) 的公共 trace_id 無關**；但 `hookTraceCollector` 的 chain trace_id 另走 lookup：
  - 先試 `traceLookup.Get(req.SessionCode, generation)`；hit → 用它；miss → fallback 到 chain_id 並 log warn（transient bootstrap；Arbitrator 正常時不該發生）
  - 這條路徑**完成 #568 的 end-to-end 汰換**：同 (session, gen) 所有 hook chain 共享同一 trace_id
- 不需要改 `observation.Builder`；P0-1 解

> 注意：observation.TraceID（個別 observation 層）與 hook chain trace_id（hookTraceCollector 聚合層）是**兩條獨立資料流**：前者用於 Arbitrator apply / divergence / emit trace row 的 trace_id 欄；後者用於 SaveChain 寫 `agent_trace_chains`。SessionStart 透過 `AdoptTraceID` 把兩者的 (session, gen) 公共值綁成同一個 UUID。

#### D1.3 ObservedGeneration 與 gen gate（P0-3）

SessionStart 的 generation 推進由 hook observation 發起 —— **SessionStart observation 的 `ObservedGeneration = arbitrator.CurrentGeneration(sid) + 1`**（不是 == current）：

```go
// buildHookObservation — SessionStart 分支
if req.EventName == "SessionStart" {
    observedGen = arbitrator.CurrentGeneration(req.SessionCode) + 1  // 推進一代
    proposal.ActorKey.Generation = observedGen
} else {
    observedGen = arbitrator.CurrentGeneration(req.SessionCode)       // 當前代
    proposal.ActorKey.Generation = observedGen
}
```

Arbitrator apply 的 generation gate 對應改寫 —— 只在 **`obs.ObservedGeneration > frameState.sessions[sid].Generation`** 時走 SessionStart helper（force-end 舊代 / clear pending / AdoptTraceID / PruneSessionBefore / ApplyAtSessionStart / boundary synthetic trace）；`==` 視為同代事件，`<` 則為 stale observation drop。

非 SessionStart hook 的 ObservedGeneration 若與當前 generation 不符：
- `observedGen < current` → apply step 1 gen gate reject（`ObservedGenerationStale`）
- `observedGen > current` 且 `obs.Action != "SessionStart"` → reject（`ObservedGenerationAhead`，防非 SessionStart 推 gen）

#### D1.4 Observation 建構（其餘欄位）

- `SourceKind: SourceHook`
- `Action: req.EventName`（e.g. "SessionStart" / "PostToolUse"）
- `Phase: PhaseCommitted` 若 verifyResult.Accepted；否則 `PhaseProposed`
- `Proposal`：`ActorKey{SessionID: sid, Generation: observedGen, ActorID: "primary:" + agentType}`；SessionStart 設 `SuggestStatus: active`；SessionEnd 設 `EndLifecycle: true, EndReason: "session_end"`；其他 hook 依 projection 決定 status（legacy projection 由 applyFrameEvent 產出，此處同樣以 req 欄位推斷）
- `Evidence`（**P0-5 / D5.2 divergence identity 必備三欄**）：
  - `{Key: "pid", Value: req.SenderPID}`（int64）
  - `{Key: "pane_id", Value: req.TmuxPaneID}`（string）
  - `{Key: "start_time", Value: req.ProcessStartTime}`（string，對應 Frame.ProcessStartTime；若 hook req 無此欄位，由 Module 在 submit 前透過 `FramesStore.GetByIdentity` 回填，仍查不到則標 `"unknown"`）
  - `{Key: "event_name", Value: req.EventName}`
  - `{Key: "role_resolution", Value: "RoleResolved" | "RoleRetryableUnresolved" | "RoleTerminalUnresolved"}`（**P1-8 常數化，見 D4.1**）
  - verify_reason if !Accepted（舊格式保留，但不再驅動 pending 路由 — 見 D4.1）
- `DecisionPorts`：legacy hook 不帶；留空 slice
- `ObservedAt: time.Now()` / `Seq: monotonic per session`（`time.Now().UnixNano()`）
- `TraceID: uuid.NewString()` — 保證非空（D1.2）

### D2. Probe adapter + probe package 契約（P1-7）

**現況**：`probe/activity`、`probe/liveness`、`probe/readiness` 是同步函式 / 單次 callback 形式，沒有 watcher token 外部介面；provider（`agentcc / codex`）在 register 時注入 callback，watcher 生命週期全部由 provider 自行管（probe 對外無 start / stop 對應物）。

**改動 scope 擴張**：T7 納入 probe package 修改，加上「顯式 start / stop + token rotation」契約。Probe 升為 stateful watcher：

#### D2.1 probe package 新 API shape

於 `internal/agent/probe/` 各模組（以 `liveness` 為例）：

```go
// internal/agent/probe/liveness/liveness.go

// Watcher 代表一個持續監測實體；每個實體擁有獨一 Token
type Watcher interface {
    // Token 回目前 watcher identity；rotate 後回新值
    Token() string
    // Rotate 重置 token（caller 先 Rotate 再重建狀態）；回新 token
    Rotate() string
    // Stop 結束監測；可多次呼叫冪等
    Stop()
}

// Start 啟動 liveness 監測並回傳 Watcher handle
// - onOutcome 每次 probe fire 被呼叫，帶 outcome + evidence
// - onOutcome 內部可產 Observation 並 submit
func Start(ctx context.Context, spec Spec, onOutcome Callback) Watcher

type Callback func(outcome Outcome, token string, evidence []EvidenceRef)
type Outcome string // "alive" | "dead" | "active" | "idle" | "ready" | "error" ...
```

對等地 `activity` / `readiness` 各自 expose `Start(ctx, spec, onOutcome) Watcher`。

#### D2.2 Module 使用模式

```go
// internal/module/agent/module.go — provider register 時的典型 pattern
watcher := liveness.Start(ctx, spec, func(outcome liveness.Outcome, token string, ev []probe.EvidenceRef) {
    obs := buildProbeObservation(
        sessionID, generation, agentType,
        probeID, token, "liveness", string(outcome), ev,
    )
    m.SubmitObservation(obs)
    // legacy applyProbe* path 保留（由現有 dispatcher 執行）
})
m.storeWatcher(sessionID, "liveness", watcher)
```

當 session 換代或 agent teardown：
```go
old := m.takeWatcher(sessionID, "liveness")
if old != nil { old.Stop() }
```

Rotation 語意：
- `Rotate()` 由 **caller（Module / provider）** 顯式呼叫，意圖是「保留 watcher 但重建 token identity」（如 agent type 切換、re-register）
- 舊 token 的 probe callback 若還在排隊，到達 Arbitrator 時會被 step 2 watcher check reject（token 不符）
- 這解決舊設計「兩個 probe 實例並發會互相覆蓋」的問題：每個 watcher 自持 token；rotate 前 caller 必須完成上下文搬遷

#### D2.3 buildProbeObservation helper

```go
// internal/module/agent/observation_builders.go
func buildProbeObservation(
    sessionID string,
    generation int64,
    agentType string,
    probeID string,
    probeToken string,
    probeKind string,            // "liveness" | "activity" | "readiness"
    probeOutcome string,         // "alive" | "dead" | "active" | "idle" | ...
    evidence []observation.EvidenceRef,
) observation.Observation
```

- `SourceKind: SourceProbe`
- `WatcherToken: probeToken`
- `Action: "probe." + probeKind + "." + probeOutcome`
- `Proposal.SuggestStatus`：outcome 映射（`dead → ended` / `active → active` / `idle → waiting` / `ready → ready` / `error → error`）
- `Proposal.EndLifecycle`：`dead` 設 true，`EndReason: "probe_dead"`
- `Proposal.ActorKey.ActorID`：`"primary:" + agentType`
- `TraceID: uuid.NewString()` — provisional；apply 階段不 adopt（只 SessionStart adopt）
- `Evidence`（D5.2 divergence identity 必備三欄）：`pid` / `pane_id` / `start_time` + callback 傳入的 evidence
- `Evidence` 額外帶 `role_resolution` 常數（probe path 的 role 一般為 `RoleResolved`；若 callback 報 `identify_mismatch` / `sender_uncertain` 則帶 `RoleRetryableUnresolved` 並觸發 pending）

#### D2.4 Apply step 2 watcher check 行為（1b-1b 既有）

保留 1b-1b 現有 watcher check 邏輯：`frameState.watcherToken(actorKey, probeID)` 回目前 token；observation 帶的 token 不符 → reject。不改 apply 面。

**不引入** `UpdateWatcherToken` public API — watcher identity 由 probe package 自持，Module 透過 `watcher.Rotate()` 推進；Arbitrator 只被動 observe。

### D3. Sweep path

**現況**：`sweep.go` `sweepOnce()` 對每個 `!isPidAliveFn(frame.PID)` 或 `startTime != frame.ProcessStartTime` 的 frame 呼叫 `clearFrame`。

**改動**：`clearFrame` 前呼叫 `m.SubmitObservation(buildSweepObservation(frame, reason))`；legacy clearFrame 仍跑。Sampling（§3.5.1 line 567）在 emit trace 階段處理（D7）。

Observation shape：
- `SourceKind: SourceSweep`
- `Action: "sweep.frame_cleared"`
- `Phase: PhaseProposed`（sweep 永遠 proposed，不升 committed）
- `Proposal.ActorKey`: `"primary:" + frame.AgentType`（D5.2 的 primary-only projection 會對此判斷）
- `Proposal.EndLifecycle: true; EndReason: reason`
- `ObservedGeneration: arbitrator.CurrentGeneration(frame.SessionCode)`（sweep 不推進 gen）
- `TraceID: uuid.NewString()` — provisional；apply 不 adopt
- `Evidence`（D5.2 identity 三欄 + D4.1 role_resolution 必備）：
  - `{Key: "pid", Value: frame.PID}`
  - `{Key: "pane_id", Value: frame.PaneID}`
  - `{Key: "start_time", Value: frame.ProcessStartTime}`
  - `{Key: "reason", Value: reason}`（pid_dead / pid_reused / ...）
  - `{Key: "role_resolution", Value: "RoleResolved"}` — sweep 已確認 frame identity（若查不到帶 `RoleTerminalUnresolved`，D4.1 已涵蓋）

### D4. Pending production entry (#584 + spec §5.4) — no active retry

> **P0-4 決策**：本 PR 不引入 retry scheduler。Pending entry 只靠 `flushPendingDue` deadline drop + 後續 observation coalescing promote。此決策在 Non-Goals 已明列；spec §5.4 文字保留，1b-1c 落地路徑以 TODO 註記等待後續 PR。

#### D4.1 Evidence 常數化（P1-8）

在 `internal/module/agent/observation/` 新增（或擴充既有 constants file）：

```go
// package observation

const EvidenceKeyRoleResolution = "role_resolution"

type RoleResolutionState string

const (
    RoleResolved              RoleResolutionState = "RoleResolved"              // positive signal
    RoleRetryableUnresolved   RoleResolutionState = "RoleRetryableUnresolved"   // pending
    RoleTerminalUnresolved    RoleResolutionState = "RoleTerminalUnresolved"    // drop (terminal)
)

// RoleResolutionFromEvidence 掃 evidence，回 state + ok；未帶 key 回 "" + false
func RoleResolutionFromEvidence(ev []EvidenceRef) (RoleResolutionState, bool)
```

所有 source（hook / probe / sweep）build observation 時 **必帶** `{Key: EvidenceKeyRoleResolution, Value: <state>}`：
- hook path：verifyResult.Accepted → `RoleResolved`；reject 且原因屬 `pane_unresolvable / sender_uncertain / identify_mismatch` → `RoleRetryableUnresolved`；reject 且終端原因（e.g. `session_not_found`）→ `RoleTerminalUnresolved`
- probe path：預設 `RoleResolved`；probe outcome 含 `identify_mismatch` → `RoleRetryableUnresolved`
- sweep path：預設 `RoleResolved`（sweep 已確認 pid dead）；若查不到 frame identity → `RoleTerminalUnresolved`

`verify_reason` 仍保留供 debug / trace display，但 **apply 層路由只認 `role_resolution` + enum**，不再掃 `verify_reason` 字串。

#### D4.2 Pending production trigger

Arbitrator apply step 4 原本只有「sweep 且 actor 已 pending → addPending」的 sweep-override 分支。增加 role-based 分支：

```go
// apply.go step 4 revised
func (d *applyDeps) routeToPendingIfUnresolved(obs observation.Observation) (stashed bool) {
    // existing sweep override (keep)
    if obs.SourceKind == observation.SourceSweep && d.pending.isPending(obs.Proposal.ActorKey) {
        d.pending.addPending(obs, d.now(), ...)
        return true
    }
    // role-based routing
    state, ok := observation.RoleResolutionFromEvidence(obs.Evidence)
    if !ok {
        // 缺 role_resolution → 視為 malformed，走一般 reject path（不 pending）
        return false
    }
    switch state {
    case observation.RoleRetryableUnresolved:
        // first entry 或 coalesce 進現有 entry
        d.pending.addPending(obs, d.now(), ...)
        // TODO(retry-scheduler, post-1b-1c): 此處原規劃 scheduleRetry([100ms, 250ms, 500ms])，
        // 後續 PR 接入。當下只靠 deadline flush + 後續 observation coalescing promote。
        return true
    case observation.RoleTerminalUnresolved:
        // 終端未解 — 直接 drop + emit trace reason_code=RoleTerminalUnresolved；不 pending
        d.emitTraceOnly(obs, "RoleTerminalUnresolved")
        return true
    case observation.RoleResolved:
        // 正常流程 — 若該 actor 目前 pending 中，coalesce 後 flushPending 會嘗試 promote
        if d.pending.isPending(obs.Proposal.ActorKey) {
            d.pending.addPending(obs, d.now(), ...)
            // 不在此處 promote；由下一個 reconcile tick 的 flushPendingDue 驅動（若 deadline 已到）
            // 亦可主動觸發 tryPromoteNow；本 PR 採後者以縮短 latency：
            _ = d.tryPromoteToActorNow(obs.Proposal.ActorKey)
            return true
        }
        return false
    }
    return false
}
```

#### D4.3 tryPromoteToActor 契約（1b-1b 佔位，1b-1c 定義 — 只認 positive signal）

`pendingStore.flushPendingDue` 的 `tryPromote func(*PendingEntry) bool` 以及 D4.2 的 `tryPromoteToActorNow(ActorKey)` 共用邏輯：

```go
func (d *applyDeps) tryPromoteFromEntry(entry *PendingEntry) bool {
    // 必要條件：entry.Observations 內至少一筆 role_resolution == RoleResolved
    // 取最新的 RoleResolved observation
    var winner *observation.Observation
    for i := len(entry.Observations) - 1; i >= 0; i-- {
        obs := &entry.Observations[i]
        if state, ok := observation.RoleResolutionFromEvidence(obs.Evidence); ok && state == observation.RoleResolved {
            winner = obs
            break
        }
    }
    if winner == nil {
        return false  // 無 positive signal — 等 deadline drop
    }
    // 必要條件：winner 有明確 proposal（SuggestStatus != "" 或 EndLifecycle == true）
    if winner.Proposal.SuggestStatus == "" && !winner.Proposal.EndLifecycle {
        return false
    }
    // 走 apply 後續 step 5-9（coalesce actor → projection → emit → divergence）
    d.applyResolvedProposal(*winner)
    return true
}
```

關鍵變化（對照 review）：
- 不再看「evidence 不含未解 key」，改看 `role_resolution == RoleResolved` 正面判斷 — 沒發現「沒 role_resolution」這個 evidence key 就不 promote（conservative default）
- `tryPromoteToActorNow` 縮短 latency：`RoleResolved` observation 進來時若該 actor 正 pending → 立即嘗試 promote，不等下個 reconcile tick

**TODO（post-1b-1c）**：接入 active retry scheduler（re-verify + exponential backoff）。當前 coalesce-on-arrival 模式對穩定下游是 OK 的（後續 observation 會帶更 fresh 的 role_resolution），但對「pane 永遠 unresolvable 但 pid 活著」的 edge case 需要 deadline drop 接住。

### D5. Divergence writer（§8.1）

#### D5.1 已存在的 divergences API（sanity 確認 — 無需新增）

`internal/store/divergences.go`（alpha.192 PR-1a 已 ship）實際簽章：

```go
type DivergenceStore struct { ... }   // 單數，非 "DivergencesStore"

func (s *DivergenceStore) Insert(d FrameDivergence) (int64, error)
func (s *DivergenceStore) Get(id int64) (*FrameDivergence, error)
```

Duplicate 行為：INSERT OR IGNORE，回 `(0, nil)`。FrameDivergence struct 已涵蓋 §8.1 schema 所有欄位（session_id / trace_id / event_id / observed_generation / old_state_ref / proposal_state_ref / diff_summary / matched / reason_code / created_at）。

**本 PR 不改 divergences store**；T3 只做回歸測試 + sanity 驗收。

#### D5.2 Arbitrator 整合（P0-5：identity 三欄 + primary-only projection）

`applyDeps` 加 `divergences DivergencesWriter` 與 `frames LegacyFramesView`（從 FramesStore 讀當前 legacy frame state）依賴：

```go
type DivergencesWriter interface {
    Insert(row store.FrameDivergence) (int64, error)  // 對齊 alpha.192 PR-1a 既有簽章
}

type LegacyFramesView interface {
    GetByIdentity(paneID string, pid int, startTime string) (*store.Frame, error)
}
```

**Identity 三欄強制**：`buildHookObservation / buildProbeObservation / buildSweepObservation` 三者產出的 evidence 都必須帶 `{pid, pane_id, start_time}` 三個 key（D1.4 / D2.3 / D3 已註記）。Apply step 8 入口若 evidence 缺任一欄 → 跳過 divergence + metric `lights_divergence_identity_missing{source}`。

**Projection primary-only**：只處理 `Proposal.ActorKey.ActorID` 開頭為 `"primary:"` 的 observation；非 primary → 跳過 divergence + metric `lights_divergence_non_primary_skipped{actor_kind}`。subagent / proxy 的 projection 需要新欄位（Phase 2 PR-2a frame schema 升 Actors JSON 後才可行），當前 legacy Frame 只有 primary 語意。

Apply 的 step 8 mode branch — passthrough 路徑加 divergence 寫入：
```go
// apply.go step 8 (passthrough branch)
func (d *applyDeps) writeDivergenceIfAny(obs observation.Observation) {
    if obs.Proposal.ActorKey.ActorID == "" {
        return  // no proposal to project
    }
    // P0-5 (b): primary-only projection
    if !strings.HasPrefix(obs.Proposal.ActorKey.ActorID, "primary:") {
        metrics.Inc("lights_divergence_non_primary_skipped", "actor_kind="+actorKindOf(obs.Proposal.ActorKey.ActorID))
        return
    }
    // P0-5 (a): identity triple required
    paneID, panePresent := evidenceString(obs.Evidence, "pane_id")
    pid, pidPresent := evidenceInt64(obs.Evidence, "pid")
    startTime, stPresent := evidenceString(obs.Evidence, "start_time")
    if !panePresent || !pidPresent || !stPresent {
        metrics.Inc("lights_divergence_identity_missing", "source="+string(obs.SourceKind))
        return
    }
    legacyFrame, err := d.frames.GetByIdentity(paneID, int(pid), startTime)
    if err != nil || legacyFrame == nil {
        metrics.Inc("lights_divergence_legacy_missing")
        return
    }
    projected := projectProposalToLegacy(obs.Proposal, *legacyFrame)  // 帶 legacy base + proposal overlay
    if framesMatch(projected, *legacyFrame) {
        metrics.Inc("lights_divergence_total", "matched=1")
        return  // no divergence — 可選擇是否寫 matched=1 row；本 PR 採「只寫 matched=0」降量
    }
    _, _ = d.divergences.Insert(store.FrameDivergence{
        SessionID: obs.SessionID,
        TraceID: obs.TraceID,
        EventID: obs.SpanID,
        ObservedGeneration: obs.ObservedGeneration,
        OldStateRef: mustJSON(legacyFrame),       // json.RawMessage
        ProposalStateRef: mustJSON(projected),    // json.RawMessage
        DiffSummary: humanDiff(legacyFrame, projected),
        Matched: false,                            // bool — 僅在有 divergence 時寫 row
        ReasonCode: obs.ReasonCode,
        CreatedAt: d.now().UnixNano(),
    })
    metrics.Inc("lights_divergence_total", "matched=0")
}
```

> 實際 `store.FrameDivergence.Matched` 型別是 `bool`（Insert 時 driver 轉為 INTEGER column：true=1 / false=0）；`OldStateRef` / `ProposalStateRef` 是 `json.RawMessage`（存為 TEXT），`mustJSON(v)` 回 `json.RawMessage`。

Divergence 寫失敗只 log，不影響 apply（passthrough 本身就是 observability 層）。

**Helper**：`actorKindOf("subagent:xxx")` → `"subagent"`；`"proxy:xxx"` → `"proxy"`；`"primary:xxx"` → `"primary"`（unreachable due to prefix gate，但保留以備未來）。

### D6. Monitor API envelope (#569 backend)

`monitor.go` `MonitorStep` 加 16 個新欄位（對應 `store.TraceStep` 的 lights envelope fields）。`monitorStepFromStore` 同步填。

新增欄位清單（與 `store.TraceStep` 對齊）：`source_kind` / `action` / `reason_code` / `outcome` / `scenario_key` / `observed_generation` / `decision_ports`（JSON string）/ `phase` / `status` / `watcher_token` / `trace_id` / `reason_text` / `attrs` / `input_refs` / `output_refs` / `state_before_ref` / `state_after_ref` / `evidence_refs` / `started_at` / `ended_at` / `otel_kind`

**Normalize**：`""` 保留 `""`；JSON 欄位（attrs/input_refs/output_refs/evidence_refs/decision_ports）空時 `monitorRawJSON` 回 `"null"` — 保留現有行為；**加 `schema_version: "1.0.0-lights-1b"` 欄位到 chain summary response**（defender 建議）便於 SPA 偵測 store rollback。

### D7. Sampling（§3.5.1 line 567）— 單 goroutine reducer state（P2-9）

Sampler 在 apply pipeline 內被呼叫，apply 為單 goroutine（Arbitrator Run owned）—— 無需 mutex。counter 改為 per-session map，避免 cross-session 交叉影響：

```go
// sampler.go — reducer state；只由 apply goroutine 讀寫
type sampler struct {
    // counts[sessionID][sourceKind] = 已 emit 累計（modulo 10）
    counts map[string]map[observation.SourceKind]int
}

func newSampler() *sampler {
    return &sampler{counts: make(map[string]map[observation.SourceKind]int)}
}

// ShouldEmit: committed 全 true；sweep/synthetic proposed 每 10 取 1；其他 proposed 全 true
// 呼叫方保證單 goroutine invocation（apply pipeline owner）
func (s *sampler) ShouldEmit(sessionID string, source observation.SourceKind, phase observation.ObsPhase) bool {
    if phase == observation.PhaseCommitted {
        return true
    }
    if source != observation.SourceSweep && source != observation.SourceSynthetic {
        return true
    }
    sessCounts, ok := s.counts[sessionID]
    if !ok {
        sessCounts = make(map[observation.SourceKind]int)
        s.counts[sessionID] = sessCounts
    }
    c := sessCounts[source]
    sessCounts[source] = c + 1
    return c%10 == 0
}

// ClearSession — SessionStart helper 推進 generation 時呼叫，回收舊 session 的 counter
func (s *sampler) ClearSession(sessionID string) {
    delete(s.counts, sessionID)
}
```

被 sample 掉的仍計 metric `lights_trace_sampled_dropped{source,phase,session}`。

**單元測試**需涵蓋 cross-session 獨立計數（session A 的 sweep 不干擾 session B 的 sweep）。**Race 測試**覆蓋面仍由 T5 apply pipeline 測試承擔（single-goroutine invariant），不對 sampler 跑 `-race` 多 goroutine 測試（違反使用契約）。

### D8. SPA type sync (#569 frontend)

`spa/src/` 搜 `MonitorStep` type / `agent/monitor` fetcher。更新 interface 含新欄位（optional string 為主）。若有 `useMonitorChain` / `useMonitorStep` hook 顯示舊 shape，**1b-1c 只 sync type，不動 UI rendering**（trace viewer UI 是 PR-6b）。

### D9. Hook path dual-write 的失敗語意

若 `m.SubmitObservation` 因 inCh 滿載 drop：legacy frame path 仍跑，使用者可見行為不變；只是 Arbitrator 少一筆 observation → divergence table 少一筆比對。metric `lights_arb_in_dropped` 已在 1b-1b 記錄。可接受。

若 legacy frame path 失敗而 observation 已送：Arbitrator 會計算 proposal；divergence 寫入時讀 legacy frame state 可能是舊值或 nil → diff_summary 顯示「legacy_write_failed」這類標記即可。接受。

### D10. CurrentGeneration thread-safety（P0-6 — 全 RWMutex）

`Arbitrator.CurrentGeneration(sessionID string) int64` — 新增 thread-safe public method。跨 goroutine 呼叫，而 `frameState.sessions` map 原本是 Arbitrator Run goroutine 單 owner（無 lock）。

**決策（P0-6）**：**不混合 atomic + map 不保護**的做法（先前版本）。統一用 `sync.RWMutex` 保護 `frameState.sessions` 整張 map 以及每個 sessionGen 的所有欄位：

```go
// frame_state.go
type frameState struct {
    mu       sync.RWMutex                    // protects sessions + sessionGen inner fields
    sessions map[string]*sessionGen
}

type sessionGen struct {
    Generation int64                          // guarded by frameState.mu
    Actors     map[observation.ActorKey]*actorSummary
}

// Arbitrator Run goroutine (writer) — 所有寫路徑取 Lock
func (fs *frameState) advanceGeneration(sid string, nextGen int64) {
    fs.mu.Lock()
    defer fs.mu.Unlock()
    s := fs.getOrCreateLocked(sid)
    s.Generation = nextGen
    // clear actors / watchers / per-gen state
}

// 外部（hook / probe / sweep / monitor）reader — RLock
func (a *Arbitrator) CurrentGeneration(sessionID string) int64 {
    a.frameState.mu.RLock()
    defer a.frameState.mu.RUnlock()
    s, ok := a.frameState.sessions[sessionID]
    if !ok {
        return 0
    }
    return s.Generation
}
```

**統一規則**：
- `frameState.mu` RWMutex 保護整個 `sessions` map 以及每個 `sessionGen` 的所有欄位
- Arbitrator Run goroutine 所有寫路徑必須 `Lock()`（包含 step 1-9 內部對 sessions / actors / watcher 的寫）
- 外部讀（跨 goroutine）必須 `RLock()`
- **不用 `atomic.Int64`** — 避免 atomic + map-access 的混合模型讓後續維護者誤判 invariant

若 session 不存在 → 回 0（等同「尚未 SessionStart」）。

**Race 覆蓋**：T1 新增 `TestArbitrator_CurrentGeneration_ConcurrentRead_NoRace` 以 `-race` 跑 N goroutine 並發讀 + 寫（Run 內部 advanceGeneration），確認無 race。

## Tasks（staged parallelism）

| Stage | Task | 依賴 |
|---|---|---|
| **Stage 1** | T1（Arbitrator CurrentGeneration + frameState RWMutex + AdoptTraceID）, T2（sampler 單 goroutine + per-session map）, T3（divergences store 介面確認/補）, T3b（observation role_resolution 常數 + helper） | 無 |
| **Stage 2** | T4（observation builder helpers: hook / probe / sweep — 含 identity 三欄 + role_resolution）, T5（apply pipeline: pending production trigger + tryPromoteFromEntry/Now + divergence writer primary-only + sampler 接入 + SessionStart adopt trace_id + gen gate 改寫） | T1/T2/T3/T3b |
| **Stage 3** | T6（hook path wiring: handler.go + trace.go + provisional UUID + traceLookup fallback）, T7（probe package 擴 Start/Stop/Rotate API + Module provider rewire）, T8（sweep wiring） | T4/T5 |
| **Stage 4** | T9（Monitor API envelope + schema_version）, T10（SPA type sync） | 無（與 Stage 1-3 可 parallel） |

### Task 1 — Arbitrator CurrentGeneration + frameState RWMutex + AdoptTraceID

**檔案**：
- `internal/module/agent/arbitrator/arbitrator.go`（改 — 加 CurrentGeneration public method）
- `internal/module/agent/arbitrator/arbitrator_test.go`（改）
- `internal/module/agent/arbitrator/frame_state.go`（改 — 整張 map + sessionGen 欄位改由 `sync.RWMutex` 保護；寫路徑 Lock、讀路徑 RLock）
- `internal/module/agent/arbitrator/frame_state_test.go`（改）
- `internal/module/agent/observation/trace_id.go`（改 — `TraceIDMinter` interface 加 `AdoptTraceID(sid, gen, seed) string`；`traceIDRegistry` 實作冪等）
- `internal/module/agent/observation/trace_id_test.go`（改）

**契約**：
```go
// arbitrator.go
func (a *Arbitrator) CurrentGeneration(sessionID string) int64  // RLock-based read

// observation/trace_id.go — 加到 TraceIDMinter interface；traceIDRegistry 實作
// AdoptTraceID：以 seed 做 (sid, gen) 公共 trace_id；已存在則回舊值（冪等）
AdoptTraceID(sessionID string, generation int64, seed string) string
```

**移除**（對照 P0-4）：原規劃的 `RetryDelays` / `RetryChCap` / `retryTick` 全數拿掉。

**TDD**：
- `TestArbitrator_CurrentGeneration_UnknownSession_ReturnsZero`
- `TestArbitrator_CurrentGeneration_AfterSessionStart_ReturnsNewGen`
- `TestArbitrator_CurrentGeneration_ConcurrentRead_NoRace` — 多 reader + 單 writer `-race` pass
- `TestFrameState_RWMutex_WriteExcludesReads` — Lock 下 RLock 需 block
- `TestFrameState_RWMutex_MultipleReadersConcurrent` — RLock 可並發
- `TestTraceIDRegistry_AdoptTraceID_NewPair_UsesSeed`
- `TestTraceIDRegistry_AdoptTraceID_ExistingPair_ReturnsOld` — 冪等
- `TestTraceIDRegistry_AdoptTraceID_EmptySeed_Error`（行為由實作定：回錯或退回 Mint；測試驗契約）

### Task 2 — sampler（單 goroutine owner + per-session map）

**檔案**：
- `internal/module/agent/arbitrator/sampler.go`（新）
- `internal/module/agent/arbitrator/sampler_test.go`

**TDD**：
- `TestSampler_Committed_AlwaysEmits`
- `TestSampler_Proposed_Hook_AlwaysEmits` — hook/probe proposed 不 sample
- `TestSampler_Proposed_Sweep_1In10`
- `TestSampler_Proposed_Synthetic_1In10`
- `TestSampler_CrossSession_IndependentCounters` — session A sweep 累計不影響 session B sweep
- `TestSampler_ClearSession_ResetsCounts`
- （不跑多 goroutine race 測試，因 single-owner 契約；race 覆蓋由 apply pipeline 測試承擔）

### Task 3 — DivergenceStore 介面 sanity（無改動，僅驗收）

**檔案**：
- `internal/store/divergences.go`（sanity check — 不改）
- `internal/store/divergences_test.go`（sanity check — 不改；若既有 test 不夠可在 T5 apply_test 使用真 store integration 補足）

**驗收項**（D5.1 已確認）：
- `DivergenceStore`（單數）與 `Insert(d FrameDivergence) (int64, error)` 已存在（alpha.192 ship）
- FrameDivergence struct 已涵蓋 §8.1 schema
- INSERT OR IGNORE 語意正常

若以上驗收失敗再補 Task 3a（schema migration + Insert 實作）；目前預期不會發生。

### Task 3b — observation role_resolution 常數 + helper

**檔案**：
- `internal/module/agent/observation/role_resolution.go`（新）— `EvidenceKeyRoleResolution` 常數 + `RoleResolutionState` enum + `RoleResolutionFromEvidence(ev)` helper
- `internal/module/agent/observation/role_resolution_test.go`

**TDD**：
- `TestRoleResolutionFromEvidence_Resolved`
- `TestRoleResolutionFromEvidence_RetryableUnresolved`
- `TestRoleResolutionFromEvidence_TerminalUnresolved`
- `TestRoleResolutionFromEvidence_MissingKey_ReturnsFalse`
- `TestRoleResolutionFromEvidence_InvalidEnum_ReturnsFalse`

### Task 4 — Observation builder helpers（含 identity 三欄 + role_resolution）

**檔案**：
- `internal/module/agent/observation_builders.go`（新）— `buildHookObservation` / `buildProbeObservation` / `buildSweepObservation`
- `internal/module/agent/observation_builders_test.go`

**TDD**：
- `TestBuildHookObservation_FullShape_IncludesIdentityTriple_AndRoleResolution`
- `TestBuildHookObservation_ProvisionalTraceID_NonEmpty` — TraceID 永遠非空，builder 不會被 `ErrMissingRequiredField` reject
- `TestBuildHookObservation_SessionStart_ObservedGenerationIsCurrentPlusOne`
- `TestBuildHookObservation_VerifyFailed_RoleResolutionRetryable` — 驗 pane_unresolvable / sender_uncertain 映射
- `TestBuildHookObservation_VerifyFailed_RoleResolutionTerminal_IfTerminalReason`
- `TestBuildProbeObservation_OutcomeMapping_DeadToEnded`
- `TestBuildProbeObservation_WatcherTokenSet_IdentityTriplePresent`
- `TestBuildSweepObservation_PidDead_ProposalEndLifecycle_IdentityTriplePresent_RoleResolved`
- `TestBuildSweepObservation_FrameMissing_RoleTerminalUnresolved`

### Task 5 — Apply pipeline 擴充

**檔案**：
- `internal/module/agent/arbitrator/apply.go`（改 — role-based pending routing + tryPromoteFromEntry + tryPromoteToActorNow + divergence writer primary-only + sampler wiring + SessionStart helper 改走 AdoptTraceID + gen gate 只在 `>` 時推進）
- `internal/module/agent/arbitrator/apply_test.go`（補 tests）
- `internal/module/agent/arbitrator/arbitrator.go`（改 — 只加 CurrentGeneration；**不** 動 Run select）
- `internal/module/agent/arbitrator/pending.go`（改如需 — `flushPendingDue` 的 `tryPromote` callback 對應新契約）

**TDD（新增；retry-scheduler 相關 case 全數移除）**：
- `TestApply_HookRoleResolutionMissing_NotPending` — 無 role_resolution key → 不 addPending
- `TestApply_HookRetryableUnresolved_CreatesFirstPendingEntry_NoRetryScheduled` — addPending 被呼叫；retryCh/AfterFunc 完全不被呼叫（不再有 retry）
- `TestApply_HookTerminalUnresolved_EmitsTraceOnly_NotPending`
- `TestApply_HookResolved_WhilePending_TriggersTryPromoteToActorNow` — 活人 signal 立刻嘗試 promote
- `TestApply_TryPromoteFromEntry_NoPositiveSignal_ReturnsFalse_WaitsForDeadline`
- `TestApply_TryPromoteFromEntry_HasResolvedObs_Promotes`
- `TestApply_TryPromoteFromEntry_ResolvedButNoProposal_ReturnsFalse`
- `TestApply_GenGate_ObservedEqualCurrent_NotSessionStart_AppliesNormally`
- `TestApply_GenGate_ObservedGreaterCurrent_SessionStart_RunsHelper_Adopts`
- `TestApply_GenGate_ObservedGreaterCurrent_NotSessionStart_Rejects` — ObservedGenerationAhead
- `TestApply_SessionStart_AdoptTraceID_UsesObservationSeed`
- `TestApply_SessionStart_AdoptTraceID_Idempotent_WhenReplayed`
- `TestApply_Passthrough_DivergenceWritten_WhenLegacyDiffers_Primary`
- `TestApply_Passthrough_DivergenceSkipped_WhenActorIsSubagent_MetricIncremented`
- `TestApply_Passthrough_DivergenceSkipped_WhenIdentityTripleMissing_MetricIncremented`
- `TestApply_Passthrough_NoDivergenceWrite_WhenMatches`
- `TestApply_Sampler_Sweep_DropsNineInTen_PerSession`

### Task 6 — Hook path wiring

**檔案**：
- `internal/module/agent/handler.go`（改 — handleEvent 在 verifyEventFn 結果之後的共用邊界 SubmitObservation；accept / reject 兩分支都送）
- `internal/module/agent/trace.go`（改 — `hookTraceCollector` 的 `traceID` 先試 `TraceIDLookup.Get`，miss 才 fallback 到 chain_id 並 log warn）
- `internal/module/agent/handler_test.go`（改）

**實作要點**：
- observation 在 build 時 mint provisional UUID 做 `Observation.TraceID`（D1.2）
- identity triple（`pid / pane_id / start_time`）由 Module 回填：若 hook req 缺 `ProcessStartTime` 欄位 → 以 `FramesStore.GetByIdentity(paneID, pid, "")` 反查；查不到則填 `"unknown"` 並標 metric `lights_hook_identity_unknown`
- SessionStart 的 `ObservedGeneration = arbitrator.CurrentGeneration(sid) + 1`（D1.3）
- 非 SessionStart 的 `ObservedGeneration = arbitrator.CurrentGeneration(sid)`
- role_resolution 常數化映射：accept → `RoleResolved`；reject/retryable reason → `RoleRetryableUnresolved`；reject/terminal reason → `RoleTerminalUnresolved`

**TDD**：
- `TestHandleEvent_Accept_SubmitsObservation` — accept 分支 observation Phase=Committed
- `TestHandleEvent_Reject_SubmitsObservation` — reject 分支 observation Phase=Proposed、RoleResolution=Retryable/Terminal
- `TestHandleEvent_SessionStart_ObservationObservedGenerationIsCurrentPlusOne`
- `TestHandleEvent_NonSessionStart_ObservationObservedGenerationIsCurrent`
- `TestHandleEvent_ProvisionalTraceID_NonEmpty` — 永遠非空繞過 builder
- `TestHandleEvent_IdentityTriple_StartTimeBackfilled_WhenReqMissing`
- `TestHookTraceCollector_TraceIDFromLookup_WhenAvailable`
- `TestHookTraceCollector_TraceIDFallsBackToChainID_WhenMinterNotYetCalled`
- `TestHandleEvent_SubmitObservationDrop_LegacyPathStillRuns` — inCh 滿載；legacy broadcast + trace 仍跑

### Task 7 — Probe package 擴 API + Module provider rewire（P1-7）

**檔案**（**probe package 可改，scope 擴張**）：
- `internal/agent/probe/liveness/liveness.go`（改 — 加 `Watcher` interface + `Start(ctx, spec, onOutcome) Watcher`）
- `internal/agent/probe/liveness/liveness_test.go`（改）
- `internal/agent/probe/activity/activity.go`（改）
- `internal/agent/probe/activity/activity_test.go`（改）
- `internal/agent/probe/readiness/readiness.go`（改）
- `internal/agent/probe/readiness/readiness_test.go`（改）
- `internal/module/agent/module.go`（改 — provider register 改用 `watcher := liveness.Start(...)`；`m.storeWatcher(sessionID, kind, watcher)`；session 換代 / teardown 時 `watcher.Stop()`）
- `internal/module/agent/module_test.go` 或對應（改）

**新 API 形狀**（詳 D2.1 / D2.2）：
```go
type Watcher interface {
    Token() string
    Rotate() string
    Stop()
}

type Callback func(outcome Outcome, token string, evidence []EvidenceRef)

func Start(ctx context.Context, spec Spec, onOutcome Callback) Watcher
```

**TDD**：
- `TestLivenessWatcher_Start_FiresOutcome_WithStableToken`
- `TestLivenessWatcher_Rotate_ReturnsNewToken_OldToken_BecomesInvalid`
- `TestLivenessWatcher_Stop_Idempotent`
- `TestActivityWatcher_Start_FiresOnActivity` / `TestReadinessWatcher_Start_FiresOnReady`
- `TestModule_ProbeCallback_SubmitsObservation_WithIdentityTriple_AndRoleResolved`
- `TestModule_ProbeLivenessDead_SubmitsEndLifecycleObservation`
- `TestModule_SessionTeardown_StopsAllWatchers`
- `TestModule_ProbeOutdatedToken_RejectsAtArbitratorWatcherCheck` — 走 apply step 2 既有 watcher check（整合測）

### Task 8 — Sweep path wiring

**檔案**：
- `internal/module/agent/sweep.go`（改 — clearFrame 前 submit observation）
- `internal/module/agent/sweep_test.go`（改）

**TDD**：
- `TestSweep_PidDead_SubmitsObservation_BeforeClearFrame_IdentityTriplePresent_RoleResolved`
- `TestSweep_PidReused_SubmitsObservationWithReusedReason`
- `TestSweep_FrameIdentityMissing_RoleTerminalUnresolved`
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
| `internal/module/agent/arbitrator/frame_state.go` | 改（RWMutex） | 1 |
| `internal/module/agent/arbitrator/frame_state_test.go` | 改 | 1 |
| `internal/module/agent/observation/trace_id.go` | 改（AdoptTraceID） | 1 |
| `internal/module/agent/observation/trace_id_test.go` | 改 | 1 |
| `internal/module/agent/arbitrator/sampler.go` | 新 | 2 |
| `internal/module/agent/arbitrator/sampler_test.go` | 新 | 2 |
| `internal/store/divergences.go` | 不改（sanity） | 3 |
| `internal/store/divergences_test.go` | 不改（sanity） | 3 |
| `internal/module/agent/observation/role_resolution.go` | 新 | 3b |
| `internal/module/agent/observation/role_resolution_test.go` | 新 | 3b |
| `internal/module/agent/observation_builders.go` | 新 | 4 |
| `internal/module/agent/observation_builders_test.go` | 新 | 4 |
| `internal/module/agent/arbitrator/apply.go` | 改 | 5 |
| `internal/module/agent/arbitrator/apply_test.go` | 改 | 5 |
| `internal/module/agent/arbitrator/pending.go` | 改如需 | 5 |
| `internal/module/agent/handler.go` | 改 | 6 |
| `internal/module/agent/trace.go` | 改 | 6 |
| `internal/module/agent/handler_test.go` | 改 | 6 |
| `internal/agent/probe/liveness/liveness.go` | 改（Watcher / Start） | 7 |
| `internal/agent/probe/liveness/liveness_test.go` | 改 | 7 |
| `internal/agent/probe/activity/activity.go` | 改 | 7 |
| `internal/agent/probe/activity/activity_test.go` | 改 | 7 |
| `internal/agent/probe/readiness/readiness.go` | 改 | 7 |
| `internal/agent/probe/readiness/readiness_test.go` | 改 | 7 |
| `internal/module/agent/module.go` | 改（watcher 管理） | 7 |
| `internal/module/agent/module_test.go` 或對應 | 改 | 7 |
| `internal/module/agent/sweep.go` | 改 | 8 |
| `internal/module/agent/sweep_test.go` | 改 | 8 |
| `internal/module/agent/monitor.go` | 改 | 9 |
| `internal/module/agent/monitor_test.go` | 改 | 9 |
| `spa/src/types/monitor.ts`（或 grep 確認） | 改 | 10 |

**移除**（對照 P0-4）：`internal/module/agent/arbitrator/types.go` 的 retryTick 新增、Options 的 RetryDelays / RetryChCap 全不做。

**預估**：~1250 LOC（Go ~1150 + SPA ~100；probe 擴 API 多 ~150，退款 retry ~200）。

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
4. `sqlite3 <db> "SELECT COUNT(*) FROM frame_divergences"` — 若 projection 與 legacy 完全對齊可能為 0 row（D5.2 只寫 matched=0；matched=1 走 metric counter 不寫 row）；若出現 row，讀 `diff_summary` 確認是預期 drift 還是 bug
5. Arbitrator log 有 `[agent][arbitrator] Run started`，reconcile tick 固定觸發
6. 連續 PUT `/api/config '{"agent":{"arb_mode":"authoritative"}}'` + 觸發 SessionStart → 所有 hook obs 都被 fail-closed reject（trace 含 `AuthoritativeNotSupportedPhase1`）；無 frame_divergences 寫入（authoritative path 不走 divergence）

### 手動驗證 SPA
- Open Monitor page（若已實作）→ trace list 顯示新欄位（或至少 type 不 crash；UI rendering PR-6b 接）

## Rollout / Rollback

- **Rollout**：merge 後 daemon 會開始 dual-write，divergences 表開始寫。使用者可見行為不變（legacy path 仍主導 FramesStore 與 broadcast）
- **Rollback**：revert 整 PR；legacy direct-write path 未動，立即回復舊行為
- **Monitor**：`sqlite3 <db> "SELECT COUNT(*) FROM frame_divergences"` 觀察 matched=0 divergence 絕對量；配合 metric `lights_divergence_total{matched=0/1}` 看比率。matched=0 比例過高（> 5%）代表 projection 邏輯或 legacy 讀取點有 bug（trigger rollback 或 iterate projection code）。`lights_divergence_non_primary_skipped` 與 `lights_divergence_identity_missing` 需為 0（若非 0 表示 observation builder 漏欄）

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
| 8 | **Pending retry scheduler**（`scheduleRetry` / `attemptRetry` / `retryCh` / exponential backoff） | post-1b-1c（獨立小 PR；本 PR 以 coalesce-on-arrival + deadline drop 取代） |
| 9 | **Subagent / proxy divergence projection**（需 frame schema 升級後才可行） | Phase 2 (PR-2a) |

**#568 / #569 / #584 何時可關**：本 PR merge 後即可關。**新增 issue 候選**：retry scheduler follow-up 建議 merge 後 `gh issue create`。
