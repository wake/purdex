# PR-1b-1b — Arbitrator goroutine + channel admission + apply pipeline

> Phase：1（Schema + 雙寫過渡）
> 依賴：PR-1b-1a（#575, alpha.204）— Observation 型別、TraceIDRegistry、arbmode Manager
> 後續：PR-1b-1c（hook/probe/sweep 實際產 Observation + divergence 寫入 + #569 Monitor API）
> Spec 對照：§3.4（Arbitrator 仲裁規則）、§3.4.1-§3.4.5、§3.5.1（Back-Pressure）、§8.3（AGENT_ARB_MODE）
> 關聯 Issue：#578（TraceIDRegistry Minter/Lookup 介面拆分）、#579（arbmode Apply epoch 化）

## Context

PR-1b-1a 落地 Observation 值型別、`TraceIDRegistry`、`arbmode.Manager` 與 `GET /api/agent/arbitrator/mode` API；但所有型別**尚無 caller**。`TraceIDRegistry.Mint()` 沒人呼叫、`Manager.ApplyAtSessionStart()` 沒人呼叫，整條 observation pipeline 只有骨架。

PR-1b-1b 落地**執行層**：單一 Arbitrator goroutine，單方擁有 pending buffer / idempotency cache / per-session generation；透過 `in chan Observation` 吸入三來源的 observation，走 9 步 apply pipeline 計算 proposal，passthrough 模式下只落 trace 不寫 frame（authoritative 要等 Phase 2）。同時處理 R2 review 遺留的兩個延後 issue：

- **#578**（防守方 review）：`TraceIDRegistry` 對外暴露 `Mint` 與 `Get` 同型別，doc comment 說「no MintOrGet」但沒有型別系統強制；任何持有 `*TraceIDRegistry` 的 caller 都能在 `Get` miss 後 `Mint`，切裂 trace。1b-1b Arbitrator 是唯一 Mint caller，PR-1b-1c 的 hook/probe 是 Get caller — 現在是介面拆分的自然時機。
- **#579**（攻擊方 review）：`Manager.OnConfigChange` 與 `ApplyAtSessionStart` 的 lock 競態會讓 kill-switch 延後一個 SessionStart 才生效。1b-1b wire-up `ApplyAtSessionStart` 時必須加 epoch 線性化。

規模 ~1200 LOC 含測試（含 #578 refactor、#579 fix、admission helper、9 步 pipeline、pending window、reconcile loop、module 接線、測試 harness）。

## Goals

1. **Arbitrator goroutine**（§3.4）：單一 owner，`Run(ctx)` 透過 `select` 消費 `in / retryCh / ticker.C`，context cancel 乾淨停止
2. **Apply pipeline 9 步**（§3.4.1）：generation gate / watcher identity / idempotency / pending window routing / source priority / monotone lifecycle / invariant check / mode branch / trace emission
3. **Pending window**（§3.4.2）：`map[ActorKey]*PendingEntry`，per-session cap 8（evict oldest）、per-observation coalescing cap 16（drop oldest）、2s deadline（flushPendingDue 於 reconcile tick）
4. **Reconcile loop**（§3.4.5）：5s tick，`flushPendingDue` + stale actor detection 只進 trace 不改 status
5. **Channel admission**（§3.5.1）：`Module.SubmitObservation` helper，committed 100ms blocking + timeout，proposed/others 非阻塞 drop + metric
6. **#578 fix**：`TraceIDRegistry` 拆成 `TraceIDMinter` / `TraceIDLookup` 兩介面，具體型別改為 unexported，constructor 回傳兩視圖；Arbitrator 拿 Minter，module 把 Lookup 留給 1b-1c 路徑
7. **#579 fix**：`Manager` 加 `configEpoch int64` 單調計數器，`OnConfigChange` bump epoch + 寫 pending、`ApplyAtSessionStart` 讀到自己那一刻的 epoch 並以此決定 current；race test 驗證「最後 OnConfigChange 的目標值必定被下一個 ApplyAtSessionStart 套用」
8. **Module 接線**：`Start` 啟動 Arbitrator goroutine，`Stop` 透過 context cancel 等 goroutine 結束
9. **測試**：§9.2 / §9.3 相關測項覆蓋 9 步 pipeline、admission、pending、reconcile、#578/#579 各自的 race / boundary 測試

## Non-Goals（明確排除）

- ❌ hook/probe/sweep 實際呼叫 `Module.SubmitObservation()` — **PR-1b-1c**（hook path 保留 `trace_id == chain_id` aliasing 直到 1b-1c 汰換）
- ❌ Divergence 表寫入（passthrough 的 mode branch 只 emit trace，不 call `SaveFrameDivergence`） — **PR-1b-1c**
- ❌ Authoritative mode frame 寫入（frame schema 尚未含 Generation + Actors JSON，Phase 2 重構） — **Phase 2**
- ❌ Monitor API envelope 穿透（#569） — **PR-1b-1c**
- ❌ SPA Arbitrator-related 顯示變更 — **Phase 1c / Phase 2**
- ❌ 新 metrics 子系統（現況只用 `log.Printf` + TODO；metrics counter 先以 package var 累計，足以寫測試） — 後續 phase 補

## 設計決策

### D1. Arbitrator Package 組織

**決策**：新增 `internal/module/agent/arbitrator/` package；與 `observation` / `arbmode` 同層。

**理由**：
- Arbitrator 對 `observation` / `arbmode` / `FramesStore` / `TraceStore` 都是**取用者**，反過來不行
- 若放在 `agent` 主 package 會讓 module.go 膨脹；獨立 package 測試隔離
- Package 名 `arbitrator` 直接對應 spec 用語

**檔案規劃**（粗粒度）：
```
internal/module/agent/arbitrator/
  arbitrator.go       — Arbitrator struct + Run() + 共用 helpers
  arbitrator_test.go
  apply.go            — Apply pipeline 9 步（apply + 子步驟）
  apply_test.go
  pending.go          — PendingEntry + 相關 pending-window 邏輯
  pending_test.go
  reconcile.go        — reconcile + flushPendingDue + stale detection
  reconcile_test.go
  admission.go        — SubmitObservation helper + drop-priority 計算
  admission_test.go
  frame_state.go      — 記憶體 frame state（generation / actor lookup）— passthrough 用
  frame_state_test.go
  idempotency.go      — lastIdemKey hash + check + (seq watermark)
  idempotency_test.go
  metrics.go          — 暫用 atomic counter；後續 phase 改真 metrics
```

### D2. Arbitrator struct 與生命週期

```go
type Arbitrator struct {
    minter    observation.TraceIDMinter   // #578: Minter view, not full registry
    arbmode   ArbModeSnapshotApplier      // interface over *arbmode.Manager

    inCh      chan observation.Observation
    retryCh   chan retryTick
    traceOut  chan<- TraceRecord

    // Single-owner state (no mutex; accessed only on Arbitrator goroutine)
    frames    *frameState                 // per-session generation + actor lifecycle
    pending   map[observation.ActorKey]*PendingEntry
    idem      *idemCache                  // lastIdemKey → seq watermark

    // Config
    pendingDeadline    time.Duration  // default 2s
    perSessionCap      int            // default 8
    perEntryObsCap     int            // default 16
    reconcileInterval  time.Duration  // default 5s

    // Clock seam for deterministic tests
    now func() time.Time
}

type retryTick struct {
    Key      observation.ActorKey
    Deadline time.Time
}

func NewArbitrator(opts Options) *Arbitrator { ... }
func (a *Arbitrator) Run(ctx context.Context) { ... }
// Returns a send-end handle for Module.SubmitObservation; nil before Run starts.
func (a *Arbitrator) InCh() chan<- observation.Observation { ... }
```

**Lifetime**：
- Arbitrator 在 `Module.Start` 建立 + `go arb.Run(ctx)`；ctx 由 `Stop` cancel
- `Run` 退出時 drain `inCh` 到空再 `close(traceOut)`（避免 trace writer 漏寫）— 若 trace writer 在上游（外部）不 close，改用 TraceRecord channel owner 語意：Arbitrator 不 close，只 stop sending
- Channel 讀寫均在 Run goroutine；`InCh()` 是唯一對外 send 端，上游透過 `SubmitObservation` helper 呼叫 send

**採 struct 自有欄位而非 map/slice 全域**：所有狀態單 goroutine 存取；不加 mutex，避免「是否該鎖」每次都要判斷

### D3. #578 — TraceIDRegistry Minter/Lookup 拆分

**決策**：`observation.TraceIDRegistry` 由 public struct 改為 unexported `traceIDRegistry`；export 兩個 interface + 一個 constructor。

```go
// observation/trace_id.go（1b-1b 重構）
type TraceIDMinter interface {
    Mint(sessionID string, generation int64) string
    PruneSessionBefore(sessionID string, generation int64) int
    PruneSession(sessionID string) int
}

type TraceIDLookup interface {
    Get(sessionID string, generation int64) (string, bool)
}

// unexported concrete type keeps internal fields
type traceIDRegistry struct { /* current fields */ }

// Constructor returns two views of the same registry; no way to get both
// capabilities except through this factory.
func NewTraceIDRegistry() (TraceIDMinter, TraceIDLookup)
```

- Arbitrator 建構子收 `TraceIDMinter`；PR-1b-1c 的 hook/probe 路徑收 `TraceIDLookup`
- `Module` 保留兩個欄位：`traceMinter TraceIDMinter`、`traceLookup TraceIDLookup`，兩者由同一 `NewTraceIDRegistry()` 產生指向同一 concrete instance
- 既有 1b-1a callers：本 repo 目前只在 `trace_id_test.go` 直接用 `NewTraceIDRegistry`；測試檔改為接兩個回傳值
- **不保留 backwards-compatible alias**（`type TraceIDRegistry = ...`）；alpha 階段無外部 consumer，直接切

**測試（新增）**：
- `TestNewTraceIDRegistry_TwoViews_SameUnderlying` — Minter.Mint 後 Lookup.Get 命中
- `TestTraceIDMinter_NoGetMethod_Compile` — interface 只含 Mint/Prune methods（編譯時驗證即可）
- `TestTraceIDLookup_NoMintMethod_Compile`
- 既有 test（`trace_id_test.go`）拆成操作 Minter / Lookup 兩組

### D4. #579 — arbmode Apply epoch 線性化

**問題**（issue #579）：`OnConfigChange` 與 `ApplyAtSessionStart` 的 lock race 下 Apply 可能讀到**舊** pending 值；新 pending 要等「下下一次 SessionStart」才生效。

**方案**（Option A，#579 推薦）：Manager 加 `configEpoch int64` 與 `pendingAtEpoch int64`；`OnConfigChange` bump epoch + 更新 pending；`ApplyAtSessionStart` 在持 lock 狀態下一次性讀 pending + epoch snapshot，再寫 current + 記錄 `appliedEpoch`。

```go
type Manager struct {
    mu              sync.RWMutex
    current         ArbMode
    pending         ArbMode
    envLocked       bool
    envValue        ArbMode
    configEpoch     int64   // bumped each time pending changes (via OnConfigChange)
    appliedEpoch    int64   // epoch at last ApplyAtSessionStart
}
```

- `OnConfigChange(configVal)`：持 Lock；若 pending 值確實變化 → `pending = newVal; configEpoch++`；返回 changed
- `ApplyAtSessionStart()`：持 Lock；讀 pending + configEpoch，`current = pending; appliedEpoch = configEpoch`
- `Snapshot()` 不暴露 epoch（內部細節）；保持現有 `{Current, Pending, EnvLocked}` 回傳型別不變
- **語意保證**：「最後一次 OnConfigChange 之後的第一次 ApplyAtSessionStart」必定套用那次 OnConfigChange 的目標值（spec §8.3 contract）

**測試（新增）**：
- `TestManager_ConfigChangeRacesApplyAtSessionStart_LatestWins` — 100 次並發 OnConfigChange + 100 次並發 ApplyAtSessionStart；最後 Snapshot.Current 必等於最後 OnConfigChange 目標值
- `TestManager_ApplyAdvancesAppliedEpoch` — 白盒測試；Apply 後 appliedEpoch 等於 configEpoch
- `TestManager_OnConfigChange_SameValue_NoEpochBump` — 相同值不 bump epoch（changed=false 保持 1b-1a 語意）

### D5. Frame state（passthrough 用）

Phase 1 frame schema 尚未含 Generation / Actors JSON（Phase 2）；Arbitrator 要做 generation gate 必須自行維護記憶體狀態。

```go
// internal/module/agent/arbitrator/frame_state.go
type frameState struct {
    sessions map[string]*sessionGen   // sessionID → generation + actor lifecycle summary
}

type sessionGen struct {
    Generation  int64
    Actors      map[observation.ActorKey]*actorSummary  // lifecycle tracking for monotone check
}

type actorSummary struct {
    EndedAt      *time.Time
    LastActivity time.Time
    Status       string   // "active" | "waiting" | "error" — passthrough inference
    WatcherTokens map[string]string  // probe_id → current token
}
```

- Arbitrator 單 goroutine 讀寫，無 mutex
- 只存「決策所需的最小 footprint」——not a shadow frame store
- Restart：Phase 1 不持久化此 state；1b-1c/Phase 2 再補 replay

### D6. Apply pipeline 9 步實作分工

每步獨立 pure function 接 `(state, obs)` 回 `(applyOutcome, []TraceRecord)`：

```go
type applyOutcome int
const (
    outcomeAccepted     applyOutcome = iota   // 繼續下一步
    outcomeRejected                            // reject + trace，終止
    outcomePendingStash                        // 入 pending buffer，終止（不算 reject）
    outcomeReplacedPrimary                     // invariant step 發出 synthetic，繼續 apply
)

// apply.go
func (a *Arbitrator) apply(obs observation.Observation) {
    steps := []applyStep{
        a.checkGenerationGate,    // §3.4.1 #1
        a.checkWatcher,           // §3.4.1 #2
        a.checkIdempotency,       // §3.4.1 #3
        a.checkPendingRouting,    // §3.4.1 #4
        a.checkSourcePriority,    // §3.4.1 #5
        a.checkMonotoneLifecycle, // §3.4.1 #6
        a.checkInvariant,         // §3.4.1 #7
        a.applyModeBranch,        // §3.4.1 #8
        a.emitTrace,              // §3.4.1 #9
    }
    for _, step := range steps {
        if !step(obs) { return }
    }
}
```

**每步子函式**：
- Generation gate — `frames.sessions[obs.SessionID].Generation` 比對；`hook.SessionStart` 可推進；其他 reject `UnauthorizedGenerationBump`
- Watcher — `sourceKind == probe` 才跑；比對 `frames.actors[key].WatcherTokens[probe_id]`
- Idempotency — `hash(ActorKey | SourceKind | Action | evidenceHash)` + seq watermark
- Pending routing — sweep 且 actor 在 pending → addPending（§3.4.3）；否則 proceed
- Source priority — hook > probe > sweep > synthetic；probe.error override hook.waiting 特例
- Monotone lifecycle — `EndedAt != nil` → reject；例外 synthetic replaced_by_new_primary 可 end
- Invariant — 會建第二個 primary → 先 emit `SyntheticEndLifecycle`
- Mode branch — passthrough：只 emit trace（不寫 frame，不寫 divergence）；authoritative：**1b-1b 不接**
- Trace — emit TraceRecord 攜完整 DecisionPorts（passthrough 下 phase=proposed, outcome=skipped）

### D7. Pending window 實作

```go
// pending.go
type PendingEntry struct {
    Key            observation.ActorKey
    FirstSeen      time.Time
    LastSeen       time.Time
    Observations   []observation.Observation
    CoalescedCount int
    Deadline       time.Time
}

func (a *Arbitrator) addPending(obs observation.Observation) { ... }
func (a *Arbitrator) flushPendingDue(now time.Time) { ... }
func (a *Arbitrator) emitTraceOnly(entry *PendingEntry, reasonCode string) { ... }
```

- `addPending`：per-session cap 8（超過 → `dropPendingOldest` + metric + emitTraceOnly `PendingEvicted`）；entry observations cap 16（超過 → drop oldest obs + metric `lights_pending_coalesced_dropped`）
- `flushPendingDue(now)`：遍歷 pending；`now.After(entry.Deadline)` → `tryPromoteToActor`；失敗則 `emitTraceOnly(PidTreeUnresolvable)` + delete
- `tryPromoteToActor`：passthrough 下**不真的建 actor**（無 frame store 寫），只走 emitTrace 記錄「若為 authoritative 會建 actor」— 此保守做法讓 1b-1c/Phase 2 接管寫入時不需回頭改 pending 邏輯

**Promote fail 判定**：pass-through 階段以「observations 裡最後一筆的 StateProposal 是否有足夠 evidence」為準；實際判定 logic 留 1b-1c 定義，1b-1b 只實作骨架 + 測試 happy path / deadline drop / evict drop / coalesce cap。

### D8. Reconcile loop

```go
// reconcile.go
func (a *Arbitrator) reconcile() {
    now := a.now()
    a.flushPendingDue(now)

    // Stale actor detection — only trace, do not mutate actor.status
    for key, actor := range a.frames.allActiveActors() {
        if now.Sub(actor.LastActivity) > 30*time.Second {
            a.emitReconcileStaleTrace(key)
        }
    }
}
```

- 5s tick（`time.NewTicker(a.reconcileInterval)`）
- Stale threshold 30s（spec §3.4.5 line 457）
- `emitReconcileStaleTrace` 送 TraceRecord `{SourceKind: SourceReconcile, Phase: Proposed, Outcome: Skipped, ReasonCode: ReconcileStaleNoted}` 到 `traceOut`

### D9. Channel admission helper

```go
// admission.go (Module layer)
func (m *Module) SubmitObservation(obs observation.Observation) {
    priority := admissionPriority(obs)
    select {
    case m.arbitrator.InCh() <- obs:
        return
    default:
    }

    if priority == AdmissionCommitted {
        // 100ms blocking retry
        timer := time.NewTimer(100 * time.Millisecond)
        defer timer.Stop()
        select {
        case m.arbitrator.InCh() <- obs:
        case <-timer.C:
            metrics.Inc("lights_arb_in_dropped", "priority=committed")
            log.Printf("[agent][arbitrator] arb_in full: committed obs dropped %s", obs)
        }
        return
    }
    metrics.Inc("lights_arb_in_dropped", "priority=proposed")
}

func admissionPriority(obs observation.Observation) AdmissionPriority {
    if obs.Phase == observation.PhaseCommitted { return AdmissionCommitted }
    return AdmissionProposed
}
```

- 公開在 `internal/module/agent/` package（module 層）；呼叫者之後會是 1b-1c 的 hook/probe/sweep 路徑
- 測試 harness 也走這個 entry（不讓測試繞過 admission 直接寫 channel）
- Metric 實作於 `arbitrator/metrics.go`，package-level `atomic.Int64` counter（後續 phase 改真 metrics）
- Retry channel buffer `retryCh cap 256` 由 Arbitrator 內部 hookup；上游 SubmitObservation 不碰

### D10. Trace writer hookup（1b-1b 範圍）

Spec §3.5.1 的 trace writer 是 Phase 1 的既有 `hookTraceSink`。但現況 `trace.go` sink 簽名綁 hook context；Arbitrator 產的 TraceRecord 結構會不同。

**決策**：Arbitrator 先輸出到**獨立的** `traceOut chan<- TraceRecord`（cap 4096），由一個新的 `TraceWriter` goroutine 寫入 `TraceStore`（1b-1b 內建）。批次 100 / 100ms（§3.5.1 table line 566）。

- `TraceWriter` 新增 `internal/module/agent/arbitrator/trace_writer.go`，只收 Arbitrator 流量；hook path 既有 sink 不動
- 滿載時按 Drop Priority（§3.5.1 line 554-558）丟棄；metric `lights_trace_dropped{priority}`
- Alpha 階段 retention 24h per-session（§3.5.1 line 568）— 此 prune 留 1b-1c 做（FramesStore 層還沒加 `started_at/trace_id` 索引）

**Batching 寫入格式**：`TraceRecord` → `internal/store/trace.go` 現有 SaveChain 型 API。但 Arbitrator 的 record 不是 chain 結構（§3.5 Envelope 是 flat）— 為避免 1b-1b 碰 store 層 schema，採「**轉成最小 valid row** 寫入 agent_trace_steps」策略：
- `chain_id = observed session trace_id`（來自 registry）
- `step_id = observation.SpanID`
- 其他 envelope 欄位對應 1b-0 的新 column
- 缺 step 必填欄（如 step_kind）用 synthetic 值填

**爭議**：此寫法與 1b-1c 的 hook path 雙寫是否衝突？— 不衝突，**只要雙方 trace_id 不切裂**；因為 trace_id 都來自同一 `TraceIDRegistry`，1b-1c hook 改 aliasing 時已有 Registry 契約保護。

### D11. Idempotency cache

```go
// idempotency.go
type idemCache struct {
    entries map[string]int64   // idem_key → last seen seq
    // Periodic prune by reconcile; unbounded growth otherwise
}

func idemKey(obs observation.Observation) string {
    // hash(actor_key | source_kind | action | evidence_hash)
    // evidence_hash = fnv64a over stable json(evidence slice)
}
```

- `evidence_hash`：stable JSON 後 FNV64a；Evidence 的 `any` Value 欄位 marshal 時以 `encoding/json` 規則（不可序列化 key 測試時會炸）
- Prune：每次 reconcile 清掉 5 分鐘沒寫入的 entry（簡單 sweep；不做 LRU）
- 初版不考慮 collision；hash 衝突屬極低機率，真發生只會多 reject 一次（upstream 重送即可）

## Tasks（staged parallelism）

PR-1b-1b 拆 8 個 task，以下執行順序與 pr-1b-1a.md 相同哲學：依賴清楚 staged，每個 task subagent 交付後經 spec reviewer + code quality reviewer 雙審。

| Stage | 並行 Task | 依賴 |
|---|---|---|
| **Stage 1** | T1（#578 interface 拆分）, T2（#579 epoch fix）, T3（metrics + common types） | 無 |
| **Stage 2** | T4（frame state + idempotency）, T5（pending window + reconcile）, T6（apply pipeline 9 步） | T1/T2/T3 |
| **Stage 3** | T7（Arbitrator struct + Run + trace writer） | T4/T5/T6 |
| **Stage 4** | T8（Module wiring + SubmitObservation helper + admission） | T7 |

Controller 嚴格依 Stage 派發，不可越 Stage 並行。

### Task 1 — #578 TraceIDRegistry Minter/Lookup 介面拆分

**檔案**：
- `internal/module/agent/observation/trace_id.go`（改 — struct 改 unexported、新增兩 interface、改 constructor 簽名）
- `internal/module/agent/observation/trace_id_test.go`（改 — 適配新簽名、加 two-views 測試）

**TDD checklist**：
- [ ] `TestNewTraceIDRegistry_TwoViews_ShareUnderlying` — Minter.Mint("s1", 1) 後 Lookup.Get("s1", 1) 回 same id + true
- [ ] `TestNewTraceIDRegistry_MinterPruneReflectsInLookup` — Minter.PruneSession 後 Lookup.Get miss
- [ ] `TestTraceIDMinter_InterfaceSurface_Compile` — compile-time `var _ TraceIDMinter = (*traceIDRegistry)(nil)` 編譯通過；無 `Get` method（`minter.Get(...)` 編譯失敗以 `// expected compile error` 註解）
- [ ] `TestTraceIDLookup_InterfaceSurface_Compile` — 同上；無 `Mint` method
- [ ] 既有測試 `TestTraceIDRegistry_Mint_*` / `TestTraceIDRegistry_Get_*` 改為 setup 時拆接兩 interface，各自操作對應視圖
- [ ] Watermark test 保留（§PR-1b-1a R2 修正）— 透過 Minter 測試

**Subagent 交付標準**：
- `go test -race ./internal/module/agent/observation/...` 全綠
- `go vet ./...` 無新 warning
- 無跨 package 變動（僅 observation 內部 + 測試）

### Task 2 — #579 arbmode Apply epoch 線性化

**檔案**：
- `internal/module/agent/arbmode/manager.go`（改 — 加 configEpoch/appliedEpoch 欄位、OnConfigChange bump、ApplyAtSessionStart 寫 appliedEpoch）
- `internal/module/agent/arbmode/manager_test.go`（改 — 加 race test + epoch 白盒測試）

**TDD checklist**：
- [ ] `TestManager_ConfigChangeRacesApplyAtSessionStart_LatestWins` — 100 次並發 OnConfigChange 交替 {authoritative, passthrough} + 100 次並發 ApplyAtSessionStart；最後 Snapshot.Current 必等於**最後一次 OnConfigChange 的目標值**
- [ ] `TestManager_OnConfigChange_BumpsEpoch` — 白盒測試；透過未 export 但測試可達 helper `configEpochForTest()` 讀 epoch；每次 pending 改變 epoch +1
- [ ] `TestManager_OnConfigChange_SameValue_NoEpochBump` — 相同值 changed=false 且 epoch 不動
- [ ] `TestManager_ApplyAtSessionStart_NoPendingDiff_StillAdvancesAppliedEpoch` — current==pending Apply 後 appliedEpoch 仍對齊 configEpoch（避免下一次同值 OnConfigChange 被誤判 skip）
- [ ] `TestManager_ApplyAtSessionStart_AdvancesToLatestPending` — OnConfigChange(A) → OnConfigChange(B) → Apply()：Current == B（不是 A）
- [ ] 既有 `TestManager_OnConfigChange_*` / `TestManager_ApplyAtSessionStart_*` 全部保留，行為不變

**實作注意**：
- `configEpochForTest()` / `appliedEpochForTest()` helper 放 `manager_export_test.go`（Go test-only file convention）
- Race test 用 `sync.WaitGroup` + `t.Parallel()`
- 不改 `Snapshot` struct shape（對 handler/API 透明）

**Subagent 交付標準**：
- `go test -race -count=10 ./internal/module/agent/arbmode/...` 10 次跑綠
- 手動驗證：修改 config `arb_mode` 10 次後 SessionStart apply 必套最後值

### Task 3 — Arbitrator package skeleton + metrics + common types

**檔案**：
- `internal/module/agent/arbitrator/types.go`（新 — `retryTick`, `AdmissionPriority`, `TraceRecord`, `applyOutcome` enum, sentinel reason codes）
- `internal/module/agent/arbitrator/metrics.go`（新 — `atomic.Int64` package-var counters + `Inc(name, tags...)` helper）
- `internal/module/agent/arbitrator/types_test.go`
- `internal/module/agent/arbitrator/metrics_test.go`

**TDD checklist**：
- [ ] `TestAdmissionPriority_Committed` — `admissionPriority(obs{Phase:PhaseCommitted}) == AdmissionCommitted`
- [ ] `TestAdmissionPriority_Proposed` — `PhaseProposed → AdmissionProposed`
- [ ] `TestAdmissionPriority_Rejected` — `PhaseRejected` 視為 AdmissionProposed（不升級為 committed）
- [ ] `TestReasonCodes_Constants` — 所有 spec §3.4.1 提到的 reason_code `StaleGeneration / UnauthorizedGenerationBump / StaleWatcher / DuplicateObservation / ActorEnded / PidTreeUnresolvable / PendingEvicted / HookStormDropped / ReconcileStaleNoted` 有對應 exported const
- [ ] `TestMetrics_Inc_NoTags` / `TestMetrics_Inc_WithTag` — increment + Value() 正確
- [ ] `TestMetrics_ResetForTesting` — test helper

**Subagent 交付標準**：
- 所有新常數有 godoc
- metrics package 是自己的 sub-file 但在 arbitrator package 命名空間內（不另開 package）

### Task 4 — frameState + idempotency cache

**檔案**：
- `internal/module/agent/arbitrator/frame_state.go`（新）
- `internal/module/agent/arbitrator/frame_state_test.go`
- `internal/module/agent/arbitrator/idempotency.go`（新）
- `internal/module/agent/arbitrator/idempotency_test.go`

**TDD checklist (frame_state)**：
- [ ] `TestFrameState_ZeroSession_Generation0`
- [ ] `TestFrameState_BumpGeneration_SessionStart` — 只 SessionStart 可推進
- [ ] `TestFrameState_ActorLifecycle_Track` — 加 actor、更新 LastActivity、endedAt set
- [ ] `TestFrameState_WatcherToken_Rotation` — 新 token 寫入後 lookup 回新值
- [ ] `TestFrameState_AllActiveActors_FiltersEnded` — EndedAt != nil 的 actor 不回
- [ ] `TestFrameState_SingleOwner_NoMutex` — 結構驗證（reflection / compile-time）

**TDD checklist (idempotency)**：
- [ ] `TestIdemKey_Determinism_SameInputs_SameKey`
- [ ] `TestIdemKey_EvidenceOrderInvariant` — Evidence slice 順序不同但內容同 → same key（stable JSON 保證；若 spec 未要求 order-invariant，改成「順序敏感」並 document）
- [ ] `TestIdemCache_FirstSeen_Accepts`
- [ ] `TestIdemCache_DuplicateLowerSeq_Rejected`
- [ ] `TestIdemCache_DuplicateEqualSeq_Rejected` — seq 相等視為重複
- [ ] `TestIdemCache_HigherSeq_Accepts_UpdatesWatermark`
- [ ] `TestIdemCache_PruneStale_5Min` — mock clock；5 分鐘未更新的 entry 被 prune
- [ ] `TestIdemCache_EvidenceUnmarshalable_ReturnsError` — chan/func value → hash error（caller 決定行為）

**Subagent 交付標準**：
- 所有測試用顯式 clock seam（`now func() time.Time`）
- 無全域狀態

### Task 5 — Pending window + reconcile loop

**檔案**：
- `internal/module/agent/arbitrator/pending.go`（新）
- `internal/module/agent/arbitrator/pending_test.go`
- `internal/module/agent/arbitrator/reconcile.go`（新）
- `internal/module/agent/arbitrator/reconcile_test.go`

**TDD checklist (pending)**：
- [ ] `TestAddPending_NewEntry_InitsDeadline2s`
- [ ] `TestAddPending_ExistingEntry_AppendsObs_UpdatesLastSeen`
- [ ] `TestAddPending_PerSessionCap8_EvictsOldest` — 第 9 筆觸發 evict，metric `lights_pending_evicted` +1
- [ ] `TestAddPending_PerEntryObsCap16_DropsOldestObs` — 17 筆 obs 進同 entry；entry.Observations 長度 16，CoalescedCount=1
- [ ] `TestFlushPendingDue_DeadlinePassed_DropsProposal` — mock now; entry.Deadline=now-1ms → emitTraceOnly `PidTreeUnresolvable`
- [ ] `TestFlushPendingDue_DeadlineNotPassed_Noop`
- [ ] `TestEmitTraceOnly_AllObservationsBecomeTraceRecord` — 3 obs 的 entry → 3 TraceRecord 送到 traceOut，phase=rejected, outcome=skipped
- [ ] `TestDropPendingOldest_FindsOldestByFirstSeen`

**TDD checklist (reconcile)**：
- [ ] `TestReconcile_FlushesPending`
- [ ] `TestReconcile_StaleActor_EmitsTraceOnly` — actor.LastActivity = now-31s → emit ReconcileStaleNoted trace；**不改** actor.Status / LastActivity
- [ ] `TestReconcile_FreshActor_NoTrace`
- [ ] `TestReconcile_NoActors_Noop`
- [ ] `TestReconcile_CalledBy5sTicker` — run Arbitrator 15s with mock ticker；reconcile 呼叫 3 次（此測試可延到 T7 Arbitrator Run 時一起寫）

**Subagent 交付標準**：
- Pending / reconcile 模組對外介面只接受 `frameState*` / `now()` / `traceOut chan<-` 三依賴，便於 T6/T7 組合
- Reconcile **絕不** mutate actor.Status（單元測試斷言 before/after 相等）

### Task 6 — Apply pipeline 9 步

**檔案**：
- `internal/module/agent/arbitrator/apply.go`（新 — 主 apply() + 9 個 step 子函式）
- `internal/module/agent/arbitrator/apply_test.go`（table-driven）

**TDD checklist（按 step 分類）**：

**Step 1 Generation gate**：
- [ ] `TestApply_GenStaleBelowFrame_RejectStaleGeneration`
- [ ] `TestApply_GenEqualFrame_Proceed`
- [ ] `TestApply_GenAboveFrame_HookSessionStart_AdvancesGen` — frames.sessions[].Generation 變成 obs.ObservedGeneration；pending buffer 清空（§3.4.4）
- [ ] `TestApply_GenAboveFrame_NonSessionStart_RejectUnauthorizedBump`
- [ ] `TestApply_GenAboveFrame_HookNonSessionStart_RejectUnauthorizedBump`
- [ ] `TestApply_SessionStart_PrunesTraceIDsBeforeNewGen` — 驗 Minter.PruneSessionBefore 被呼叫

**Step 2 Watcher identity**：
- [ ] `TestApply_Probe_WatcherTokenMatches_Proceed`
- [ ] `TestApply_Probe_WatcherTokenMismatch_RejectStaleWatcher`
- [ ] `TestApply_Probe_WatcherTokenNotRegistered_RejectStaleWatcher` — frame 無 token 對此 probe → reject
- [ ] `TestApply_Hook_NoWatcherCheck` — hook 不走 watcher gate
- [ ] `TestApply_Sweep_NoWatcherCheck`

**Step 3 Idempotency**：
- [ ] `TestApply_FirstObservation_Accepts`
- [ ] `TestApply_DuplicateSameActorSourceActionEvidence_RejectDuplicate`
- [ ] `TestApply_DifferentActor_NotDuplicate`

**Step 4 Pending routing**：
- [ ] `TestApply_SweepWhileActorPending_StashesToPending` — obs source=sweep + actor 在 pending → addPending，return，不進後續 step
- [ ] `TestApply_NonSweepWhileActorPending_Proceed`
- [ ] `TestApply_SweepWhileActorNotPending_Proceed`

**Step 5 Source priority**：
- [ ] `TestApply_SourcePriority_Hook_GtProbe_GtSweep_GtSynthetic` — 交替三來源相同 actor；higher priority 贏
- [ ] `TestApply_ProbeError_OverridesHookWaiting` — spec 特例；probe `error` + hook `waiting` → probe 贏

**Step 6 Monotone lifecycle**：
- [ ] `TestApply_ActorEnded_NewProposal_RejectActorEnded`
- [ ] `TestApply_SyntheticReplacedByNewPrimary_AllowedEndEnded`

**Step 7 Invariant**：
- [ ] `TestApply_NewPrimaryWhenExistingPrimary_EmitsSyntheticEndThenApply` — trace 有兩筆：SyntheticEndLifecycle for old primary + 新 proposal
- [ ] `TestApply_NewPrimaryNoExistingPrimary_NoSynthetic`

**Step 8 Mode branch (passthrough)**：
- [ ] `TestApply_PassthroughMode_NoFrameMutation` — 只 emit trace；frameState.allActiveActors() 不變
- [ ] `TestApply_PassthroughMode_NoDivergenceWrite` — 驗證沒有 divergence store call（1b-1c 才接）
- [ ] `TestApply_AuthoritativeMode_SkippedInThisPR` — `ArbModeAuthoritative` → emit TraceRecord 但不寫 frame + log TODO（Phase 2 實作）

**Step 9 Trace**：
- [ ] `TestApply_TraceRecordHasCompleteDecisionPorts` — emit 的 record 保留原 obs 的 DecisionPorts
- [ ] `TestApply_TraceRecordPhaseProposed_WhenRejected` — reject path 寫 `phase=rejected`
- [ ] `TestApply_TraceRecordAssignsReasonCode` — `reject_*` path 各自 reason_code

**Subagent 交付標準**：
- `go test -race -cover ./internal/module/agent/arbitrator/...` coverage ≥80%
- 9 步每步至少一個 happy + 一個 reject 測試
- Mock trace writer 用 `chan TraceRecord` cap 100，測試 assert 收到條件

### Task 7 — Arbitrator struct + Run() + trace writer

**檔案**：
- `internal/module/agent/arbitrator/arbitrator.go`（新 — struct + Run + NewArbitrator + InCh 等）
- `internal/module/agent/arbitrator/arbitrator_test.go`
- `internal/module/agent/arbitrator/trace_writer.go`（新 — batching goroutine）
- `internal/module/agent/arbitrator/trace_writer_test.go`

**TDD checklist (arbitrator)**：
- [ ] `TestArbitrator_NewWithDefaults_FieldsWiredCorrectly`
- [ ] `TestArbitrator_Run_ConsumesInCh` — push 1 obs；assert apply 被呼叫
- [ ] `TestArbitrator_Run_ReconcileTickerFires` — fake clock；15s tick 3 次 reconcile
- [ ] `TestArbitrator_Run_RetryChDelivers` — 用測試 hook 送 retryTick；assert attemptRetry 被呼叫
- [ ] `TestArbitrator_Run_ContextCancel_StopsGoroutine` — `ctx.Cancel()` 後 Run 在 100ms 內返回；goroutine leak 檢查
- [ ] `TestArbitrator_InCh_ReturnsSendOnlyChannel`
- [ ] `TestArbitrator_RunBeforeStop_DrainsInCh` — stop 前 5 筆 pending 都被處理
- [ ] `TestArbitrator_SingleOwner_NoDataRace` — `go test -race -count=10` 穩定

**TDD checklist (trace_writer)**：
- [ ] `TestTraceWriter_Batch100OrTimeout_Flushes`
- [ ] `TestTraceWriter_QueueFull_DropsByPriority` — spec §3.5.1 Drop Priority
- [ ] `TestTraceWriter_MetricIncOnDrop`
- [ ] `TestTraceWriter_ShutdownFlushesRemaining`
- [ ] `TestTraceWriter_WriteFailure_LoggedNotPanic` — mock store 回 error；writer 繼續跑

**實作注意**：
- `TraceStore` 寫入走最小 row（現有 `SaveStep` 或 `SaveChain` API）；避開 schema 層修改
- `traceOut chan<- TraceRecord`：`cap = 4096`
- Arbitrator 與 TraceWriter goroutine 透過 context 共同 cancel
- Goroutine leak detection: 用 `goleak.VerifyNone(t)` 或 sleep-and-check pattern

**Subagent 交付標準**：
- `go test -race -count=10 ./internal/module/agent/arbitrator/...` 10 次綠
- manual: integrator test — push 1000 obs，reconcile 3 次，context cancel 後 process 完成

### Task 8 — Module wiring + SubmitObservation + admission

**檔案**：
- `internal/module/agent/module.go`（改 — 加 Arbitrator 欄位 + Init build + Start go + Stop cancel）
- `internal/module/agent/admission.go`（新 — SubmitObservation helper + admissionPriority）
- `internal/module/agent/admission_test.go`
- `internal/module/agent/module_test.go` 或 `fakes_test.go`（改 — 新增 module lifecycle test）

**TDD checklist**：
- [ ] `TestModule_Init_BuildsArbitrator`
- [ ] `TestModule_Start_StartsArbitratorGoroutine` — 觀察 Arbitrator.Run 在 goroutine 跑
- [ ] `TestModule_Stop_CancelsArbitrator`
- [ ] `TestModule_SubmitObservation_Committed_BlocksUpTo100ms` — channel full → timer 到時 drop + metric
- [ ] `TestModule_SubmitObservation_Committed_NoBlock_WhenSpace`
- [ ] `TestModule_SubmitObservation_Proposed_NonBlocking_DropsWhenFull`
- [ ] `TestModule_SubmitObservation_Flush_SessionStartTriggersMinter` — end-to-end：SubmitObservation(SessionStart) → Arbitrator apply → TraceIDRegistry.Minter.Mint 被呼叫
- [ ] `TestModule_SubmitObservation_SessionStartTriggersApplyAtSessionStart` — end-to-end：同上 → arbmode.Manager.ApplyAtSessionStart 被呼叫（#579 epoch 路徑一併覆蓋）

**實作注意**：
- Module.Init 建 `traceMinter, traceLookup = NewTraceIDRegistry()`；`arbitrator.NewArbitrator(traceMinter, arbmodeMgr, ...)`
- `traceLookup` 存 Module 欄位，留給 1b-1c 的 hook/probe callback 使用（1b-1b 有欄位但無 caller）
- SubmitObservation 不直接讀 arbitrator 內部 channel；透過 `arbitrator.InCh()` helper
- admission.go 放 Module package；metrics 透過 `arbitrator.Metrics` export

**Subagent 交付標準**：
- `go test -race ./internal/module/agent/...` 全綠
- 手動驗證：
  - Daemon 起，看 log 有 `[agent][arbitrator] Run started`
  - `curl` daemon shutdown endpoint 或 `SIGTERM`，log 有 `[agent][arbitrator] Run stopped (context cancelled)` + goroutine 清空

## 檔案變動總覽

| 檔 | 動作 | Task |
|---|---|---|
| `internal/module/agent/observation/trace_id.go` | 改（#578 拆介面） | 1 |
| `internal/module/agent/observation/trace_id_test.go` | 改 | 1 |
| `internal/module/agent/arbmode/manager.go` | 改（#579 epoch） | 2 |
| `internal/module/agent/arbmode/manager_test.go` | 改 | 2 |
| `internal/module/agent/arbmode/manager_export_test.go` | 新 | 2 |
| `internal/module/agent/arbitrator/types.go` | 新 | 3 |
| `internal/module/agent/arbitrator/types_test.go` | 新 | 3 |
| `internal/module/agent/arbitrator/metrics.go` | 新 | 3 |
| `internal/module/agent/arbitrator/metrics_test.go` | 新 | 3 |
| `internal/module/agent/arbitrator/frame_state.go` | 新 | 4 |
| `internal/module/agent/arbitrator/frame_state_test.go` | 新 | 4 |
| `internal/module/agent/arbitrator/idempotency.go` | 新 | 4 |
| `internal/module/agent/arbitrator/idempotency_test.go` | 新 | 4 |
| `internal/module/agent/arbitrator/pending.go` | 新 | 5 |
| `internal/module/agent/arbitrator/pending_test.go` | 新 | 5 |
| `internal/module/agent/arbitrator/reconcile.go` | 新 | 5 |
| `internal/module/agent/arbitrator/reconcile_test.go` | 新 | 5 |
| `internal/module/agent/arbitrator/apply.go` | 新 | 6 |
| `internal/module/agent/arbitrator/apply_test.go` | 新 | 6 |
| `internal/module/agent/arbitrator/arbitrator.go` | 新 | 7 |
| `internal/module/agent/arbitrator/arbitrator_test.go` | 新 | 7 |
| `internal/module/agent/arbitrator/trace_writer.go` | 新 | 7 |
| `internal/module/agent/arbitrator/trace_writer_test.go` | 新 | 7 |
| `internal/module/agent/admission.go` | 新 | 8 |
| `internal/module/agent/admission_test.go` | 新 | 8 |
| `internal/module/agent/module.go` | 改 | 8 |

**預估**：~1200 LOC 含測試（T1 ~80 / T2 ~100 / T3 ~200 / T4 ~240 / T5 ~220 / T6 ~300 / T7 ~260 / T8 ~200）。

## Verification

### 單元測試
```bash
cd .claude/worktrees/lights-pr-1b-1b
go test -race -count=5 ./internal/module/agent/observation/... \
                        ./internal/module/agent/arbmode/... \
                        ./internal/module/agent/arbitrator/... \
                        ./internal/module/agent/...
go vet ./...
go build ./...
```

### 整合驗證（手動）
1. 起 daemon，`curl http://127.0.0.1:7860/api/agent/arbitrator/mode` 保持 alpha.204 行為（`{passthrough, passthrough, false}`）
2. Log tail 有 `[agent][arbitrator] Run started` + 5s 一次 reconcile tick
3. `kill -SIGTERM` daemon，log 有 `[agent][arbitrator] Run stopped`；goroutine leak 檢查（`lsof -p` / daemon restart clean）
4. #579 race：迴圈 `PUT /api/config '{"agent":{"arb_mode":"authoritative"}}'` + 人為觸發 SessionStart observation；Snapshot.Current 必對齊最後 PUT 值
5. 手動觸發 SessionStart observation（透過測試 endpoint，若無則略過此步）→ 新 trace_id 在 registry 產生

### Race 長跑
- `go test -race -count=50 ./internal/module/agent/arbitrator/...` 50 次綠（pending / apply / reconcile 路徑并發最密集）
- `go test -race -count=50 ./internal/module/agent/arbmode/...` 50 次綠（#579 epoch）

## Rollout / Rollback

- **Rollout**：直接 merge 進 main；Arbitrator 啟動但**無上游**（hook/probe/sweep 仍走舊路徑），使用者不可見
- **Rollback**：revert 整個 PR；Module.Init 不建 Arbitrator、無新 channel / goroutine
- **行為影響**：
  - daemon 多一個 Arbitrator goroutine + TraceWriter goroutine + 兩個 channel buffer（約 5000 × 128 bytes ≈ 640KB 記憶體）
  - Alpha 用戶無觀察差異（trace_id 變化只在 1b-1c 接 hook path 後才出現）

## 已知 follow-up（1b-1b 不做，留後續）

| # | 項目 | 歸屬 |
|---|---|---|
| 1 | hook/probe/sweep path 實際呼叫 `SubmitObservation` | PR-1b-1c |
| 2 | Arbitrator passthrough mode 寫 `frame_divergences` | PR-1b-1c |
| 3 | Hook path `trace_id == chain_id` aliasing 汰換為 `TraceIDLookup.Get()` | PR-1b-1c |
| 4 | Monitor API envelope 穿透（#569） | PR-1b-1c |
| 5 | 真 metrics 系統（Prometheus / expvar）取代 atomic package var | Phase 1d / Phase 2 |
| 6 | Authoritative mode frame 寫入 | Phase 2 |
| 7 | Frame schema 加 Generation + Actors JSON | Phase 2 |
| 8 | Trace retention 24h per-session prune | PR-1b-1c 或 Phase 2 |
| 9 | Daemon restart frame / actor state replay | Phase 2 |
| 10 | Hook storm throttle（10ms 50 obs）落實 | PR-1b-1c 或 Phase 2 |

**#578 / #579 何時可關**：本 PR merge 後即可關（interface 拆分 + epoch 線性化皆已落地 + 測試覆蓋）。
