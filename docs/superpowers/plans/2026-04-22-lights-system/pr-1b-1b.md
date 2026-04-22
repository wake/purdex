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
- ❌ Authoritative mode frame 寫入（1b-1b **fail-closed**：`AGENT_ARB_MODE=authoritative` 下 apply 全部 reject + reason_code=`AuthoritativeNotSupportedPhase1`） — **Phase 2 才真接** — 見 D6 step 8 / P2-2 修訂
- ❌ Monitor API envelope 穿透（#569） — **PR-1b-1c**
- ❌ SPA Arbitrator-related 顯示變更 — **Phase 1c / Phase 2**
- ❌ `retryCh` + `scheduleRetry` + `attemptRetry` 3-strike（spec §5.4；verify 流程 producer 在 hook path）— **PR-1b-1c**（見 D2 P1-2 修訂）
- ❌ Sampling（sweep/synthetic proposed 1/10；§3.5.1 line 567）— **PR-1b-1c**（上游有流量後才 meaningful；見 D13 P1-3 修訂）
- ❌ Trace retention 24h per-session TTL — **Phase 2**（見 D13）
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
    minter       observation.TraceIDMinter   // #578: Minter view, not full registry
    arbmode      ArbModeSnapshotApplier      // interface over *arbmode.Manager
    traceWriter  TraceSubmitter              // writer façade (D10)

    inCh      chan observation.Observation

    // Single-owner state (no mutex; accessed only on Arbitrator goroutine)
    frames    *frameState                 // per-session generation + actor lifecycle
    pending   map[observation.ActorKey]*PendingEntry
    idem      *idemCache                  // lastIdemKey → seq watermark

    // Config
    pendingDeadline    time.Duration  // default 2s
    perSessionCap      int            // default 8
    perEntryObsCap     int            // default 16
    reconcileInterval  time.Duration  // default 5s
    hookStormWindow    time.Duration  // default 10ms
    hookStormCap       int            // default 50 obs / session / window

    // Clock seam for deterministic tests
    now func() time.Time
}

func NewArbitrator(opts Options) *Arbitrator { ... }
func (a *Arbitrator) Run(ctx context.Context) { ... }
// Returns a send-end handle for Module.SubmitObservation; nil before Run starts.
func (a *Arbitrator) InCh() chan<- observation.Observation { ... }
```

**Lifetime**：
- Arbitrator 在 `Module.Start` 建立 + `go arb.Run(ctx)`；ctx 由 `Stop` cancel
- `Run` 退出時 drain `inCh` 到空再呼叫 `traceWriter.Shutdown(ctx)`（由 TraceWriter 內部決定 flush / 丟棄，見 D10）
- Channel 讀寫均在 Run goroutine；`InCh()` 是唯一對外 send 端，上游透過 `SubmitObservation` helper 呼叫 send

**retryCh 移除 1b-1b scope**（P1-2 修訂）：spec §5.4 的 `scheduleRetry([100,250,500]ms)` + `attemptRetry` + 3-strike trace-only drop 牽涉 verify 流程的 producer，而 verify 流程的 hook path 汰換在 **PR-1b-1c** 範圍。1b-1b 不放空殼 retry channel 避免「有介面無 producer」假骨架；`tryPromoteToActor` 仍在 1b-1b 定義完整契約（D7），以便 pending deadline flush 直接走真實 promote 路徑。RetryCh 與 scheduleRetry/attemptRetry 整組延到 1b-1c。

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

### D4. #579 — arbmode Apply 線性化（Plan review P0-2 修訂：改用 published snapshot）

**問題**（issue #579）：`OnConfigChange` 與 `ApplyAtSessionStart` 的 lock race 下 Apply 可能讀到**舊** pending 值；新 pending 要等「下下一次 SessionStart」才生效。

**初版方案的瑕疵**（Plan review P0-2 指出）：只加 `configEpoch` 計數器仍未解 race — 若 `ApplyAtSessionStart()` 先拿到 lock，它仍會套用舊 pending；之後 `OnConfigChange` 才更新 pending/epoch，最終效果與舊版一樣延後一個 SessionStart，「加計數」並沒有改變讀寫次序。

**修訂方案 — published snapshot via `atomic.Pointer`**：把 pending publish 跟 Apply read 拆到**無鎖**的原子 publish-subscribe 通道上：

```go
type Manager struct {
    mu         sync.Mutex                      // protects current / envLocked write path only
    current    ArbMode
    envLocked  bool
    envValue   ArbMode

    // published is the immutable "next-SessionStart target". OnConfigChange
    // constructs a new *modeTarget and atomic-swaps it in. ApplyAtSessionStart
    // atomic-loads it and promotes its value into current. No lock is held
    // across the publish ↔ apply boundary, so the only race is the trivial
    // "last publish wins" ordering, which is the contract we want.
    published  atomic.Pointer[modeTarget]
}

type modeTarget struct {
    Mode     ArbMode
    Revision int64 // monotonic; bumped on each publish
}
```

**契約**：

- `NewManager(env, configVal)`：走現行 precedence 決定初始 `current`；同時 `published.Store(&modeTarget{Mode: current, Revision: 0})`
- `OnConfigChange(configVal string) bool`：若 envLocked 或 value invalid → 現行 fallback；否則建 `next := &modeTarget{Mode: cv, Revision: prev.Revision + 1}`，`published.Store(next)`；回傳 `next.Mode != prev.Mode`。**不持 mu**
- `ApplyAtSessionStart()`：`target := published.Load()`；若 `target.Mode == current` no-op；否則 `mu.Lock(); current = target.Mode; mu.Unlock()`
- `Snapshot()`：`target := published.Load(); mu.RLock(); cur := current; locked := envLocked; mu.RUnlock(); return {Current: cur, Pending: target.Mode, EnvLocked: locked}`

**為何此方案真的線性化 #579 case**：

1. `OnConfigChange` 只有一個動作：`published.Store(newTarget)`。這是 single-atomic-word publish，下一個 `published.Load()` 一定讀得到
2. `ApplyAtSessionStart` 先 `published.Load()`、再 `mu.Lock` 改 current。即使順序是：
   - `Apply` 先 Load（拿到舊 target）→ `OnConfigChange` Store 新 target → `Apply` 取得 lock 改 current — **此時 published 已是新 target**，當下 Apply 套用的是 Load 時點的 target（舊值），但下一次 SessionStart 會讀到新 target
   - 關鍵是 spec 的契約是「 `SessionStart` 時點讀到的那個 publish 就是生效值」，不是「最後 PUT 的值在任意後續 SessionStart 必定生效」；published snapshot 精準對應 spec §8.3「在 SessionStart 當下讀取當下 published 的 pending」
3. 對比初版 `configEpoch`：初版 Apply 仍讀 mutex-protected `pending`（live field），持 lock 跟 OnConfigChange 互斥，但先拿 lock 的 Apply 會讀到舊值 — 這是 true race。換成 atomic.Pointer，Apply 的讀取點本身就是 publish 的 linearization point，不會錯過任何 complete publish

**可測試性**：
- `TestManager_OnConfigChange_PublishesSnapshot` — 白盒 helper `loadPublishedForTest()` 回 `*modeTarget`；OnConfigChange(A) 後 Load 回 A + Revision=prev+1
- `TestManager_Apply_ReadsLatestPublished` — `OnConfigChange(A) → OnConfigChange(B) → ApplyAtSessionStart()` 後 Current==B、published Revision 繼續保留（Apply 不 bump Revision）
- `TestManager_Apply_RacesOnConfigChange_NewlyPublishedWinsNextSessionStart` — 可控 interleave（不是 100 goroutine 混戰）：
  - **Case A**：`OnConfigChange(A)` 開始 → atomic.Store 前 `ApplyAtSessionStart()` → Apply 讀到**舊** published（initial）→ `OnConfigChange` 完成 → 第二次 `ApplyAtSessionStart()` → Current==A ✅
  - **Case B**：`OnConfigChange(A)` 完成 → `ApplyAtSessionStart()` → Current==A ✅
  - **Case C**：`OnConfigChange(A)` → Apply（先 Load 拿 A → mu.Lock 改 current 前） + 併發 `OnConfigChange(B)` 完成 → Apply mu.Lock 改 current=A → 再一次 `ApplyAtSessionStart()` → Current==B ✅（B 是新 published）
  - Case C 是 P0-2 最初指的 race — atomic.Pointer 方案在 Case C 下雖然第一次 Apply 套用的是 A 不是 B，但 B **必定在下一次 SessionStart 被 Apply**；而初版 epoch 方案會把 B 寫到 pending 但 appliedEpoch 已被 A 更新到最新，根據 plan 初版邏輯下一次 SessionStart 可能不會再重跑 promote → 一樣延後一個 SessionStart
  - 不過 Case C 的語意爭議在於：如果 OnConfigChange(B) 發生在 Apply 取完 lock 之後，B 本身的契約也是「套下一次 SessionStart」；所以 B 在下一次 SessionStart 被 apply 就是正確行為。atomic.Pointer 方案的優點是**任何 B 的 publish 都確定能被下一次 SessionStart 讀到**，無漏
- `TestManager_Snapshot_NotTorn` — OnConfigChange 跑過程中 Snapshot 讀到的 `{Current, Pending, EnvLocked}` 必為有效快照（Current 來自 mu 保護、Pending 來自 atomic published、EnvLocked 不變），可容許 Pending 領先 Current（正是 API 契約）

**Snapshot 不暴露 Revision / internal target**；仍是 `{Current, Pending, EnvLocked}` 三欄回傳型別，API 穩定。

### D5. Frame state（兩種 mode 都更新；Plan review P0-1 修訂）

Phase 1 frame schema 尚未含 Generation / Actors JSON（Phase 2）；Arbitrator 要做 generation gate / watcher identity / monotone lifecycle / single-primary invariant / 30s stale reconcile 必須自行維護記憶體狀態。

**關鍵修訂（P0-1）**：`frameState` 是 Arbitrator 的**內部 reducer state**，**不是** FramesStore 的影子。兩層各自獨立：

| 層 | 內容 | passthrough 是否更新 | authoritative 是否更新 |
|---|---|---|---|
| **frameState（in-memory reducer）** | generation / actor lifecycle / watcher tokens / LastActivity | ✅ | ✅ |
| **FramesStore（SQLite 持久 frame）** | pane / agent_type / status / subagents | ❌ | ✅ (Phase 2 才接；1b-1b 全不碰) |
| **frame_divergences（SQLite）** | projection diff | ❌（1b-1b 全不碰；1b-1c 接） | ❌ |
| **WS broadcast** | frame update 推 SPA | ❌ | ✅ (Phase 2) |

**passthrough 的意義** ≠ 「Arbitrator 內部什麼都不做」，而是「不落地到外部可觀察 state」。frameState 仍必須演進，否則後續步驟無資訊做判斷。

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
    EndedAt       *time.Time
    LastActivity  time.Time
    Status        string                       // "active" | "waiting" | "error" — reducer inference
    WatcherTokens map[string]string            // probe_id → current token（rotate 時覆寫）
    IsPrimary     bool                         // single-primary invariant check 用
}
```

- Arbitrator 單 goroutine 讀寫，無 mutex
- 只存「決策所需的最小 footprint」—— not a shadow frame store（無 pane / pid / process_start_time 等 FramesStore 專屬欄位）
- Restart：Phase 1 不持久化此 state；1b-1c/Phase 2 再補 replay

**測試應斷言的行為**（對照 Task 6 apply_test）：
- passthrough mode + `SessionStart` → frameState.sessions[session].Generation 推進 + pending clear；但 FramesStore 未變（用 fake store spy）
- passthrough mode + probe token rotate → `actorSummary.WatcherTokens` 覆寫；舊 token 的後續 probe obs 走 watcher gate 被 reject
- passthrough mode + new primary → frameState 的 IsPrimary 轉移到新 actor + 舊 primary `EndedAt` 設置；FramesStore / broadcast / divergence 均未動

### D5b. SessionStart boundary helper（Plan review P1-1 修訂）

Generation gate 遇 `hook.SessionStart` 不只 bump generation；spec §3.4.4 + §3.4.1 實際要求的 side effects：

1. `frameState.sessions[sessionID].Generation = newGen`
2. 舊 generation 的所有 `actorSummary` 強制 `EndedAt = now` + `Status = "ended"`（若還沒 end），reason=`session_restart`
3. 舊 generation 的 WatcherTokens 全清除（token 跨 generation 必然失效）
4. Pending buffer 中所有 key.Generation < newGen 的 entry 一併清空，**每筆 observation 產一筆 `phase=rejected, outcome=skipped, reason_code=SessionRestartCleared` 的 trace**
5. `minter.Mint(sessionID, newGen)` 取得新 trace_id（供後續 observation 用）
6. `minter.PruneSessionBefore(sessionID, newGen)` 清舊 generation trace_id
7. `arbmode.ApplyAtSessionStart()` 推進 pending → current（#579 published snapshot 消費點）
8. emit synthetic trace `source_kind=synthetic, action=session.generation_advanced, reason_code=SessionRestartCleared, attrs={"cleared_actors":N, "cleared_pending":M}`

**helper 簽名**：
```go
// apply.go
func (a *Arbitrator) applySessionStart(obs observation.Observation) {
    sid := obs.SessionID
    newGen := obs.ObservedGeneration
    sg := a.frames.getOrCreateSession(sid)
    oldGen := sg.Generation

    endedActors := a.frames.forceEndOldGenActors(sid, oldGen, a.now())   // step 2-3
    clearedPending := a.clearPendingForGeneration(sid, oldGen)            // step 4 (emit traces)
    sg.Generation = newGen
    a.minter.Mint(sid, newGen)                                            // step 5
    a.minter.PruneSessionBefore(sid, newGen)                              // step 6
    a.arbmode.ApplyAtSessionStart()                                       // step 7 (no-op when no pending diff)
    a.emitSyntheticBoundaryTrace(sid, newGen, endedActors, clearedPending)// step 8
}
```

**測試**（Task 6 apply_test + Task 4 frame_state_test 分擔）：
- `TestSessionStart_BumpsGeneration`
- `TestSessionStart_EndsOldGenActors_WithReasonSessionRestart`
- `TestSessionStart_ClearsWatcherTokens`
- `TestSessionStart_ClearsPending_EmitsPerObsSessionRestartClearedTrace` — N 筆舊 gen pending obs → N 筆 trace，每筆 reason_code=SessionRestartCleared
- `TestSessionStart_MintsNewTraceID_PrunesOld`
- `TestSessionStart_CallsArbmodeApply` — fake arbmode 記錄 ApplyAtSessionStart 被呼叫一次
- `TestSessionStart_EmitsBoundarySyntheticTrace`
- `TestSessionStart_LateProbe_WithOldGeneration_RejectedByGate` — gate step 1 的邊界（舊 gen obs 進來還是被 reject 不 bypass）

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
- Generation gate — `frames.sessions[obs.SessionID].Generation` 比對；`hook.SessionStart` 可推進（呼叫 `applySessionStart(obs)` helper，見 D5b）；其他 reject `UnauthorizedGenerationBump`
- Watcher — `sourceKind == probe` 才跑；比對 `frames.actors[key].WatcherTokens[probe_id]`
- Idempotency — `hash(ActorKey | SourceKind | Action | evidenceHash)` + seq watermark
- Pending routing — sweep 且 actor 在 pending → addPending（§3.4.3）；否則 proceed
- Source priority — hook > probe > sweep > synthetic；probe.error override hook.waiting 特例
- Monotone lifecycle — `EndedAt != nil` → reject；例外 synthetic replaced_by_new_primary 可 end
- Invariant — 會建第二個 primary → 先 emit `SyntheticEndLifecycle`
- **Mode branch — passthrough**：`frameState` 已更新（前 7 步過程中），此步決定「要不要外部落地」。
  - `passthrough`：**不寫** FramesStore / 不寫 frame_divergences（1b-1c 接）/ 不 WS broadcast；繼續走 step 9 emit trace
  - `authoritative`：**fail-closed**（P2-2 修訂）— emit trace `phase=rejected, outcome=skipped, reason_code=AuthoritativeNotSupportedPhase1`；不寫 FramesStore、不推進 actor 外部狀態。Phase 2 才真正接 FramesStore writer；1b-1b / 1b-1c 期間 `AGENT_ARB_MODE=authoritative` 純粹是 no-op-with-visibility，避免使用者誤設下系統靜默半功能
- Trace — emit TraceRecord 攜完整 DecisionPorts（passthrough 下 accept path `phase=proposed, outcome=skipped`；reject path 視 step 1-7 的 reason 決定）

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

**延後項（PR-1b-1c）— production path 第一筆 pending entry 的 trigger logic（issue [#584](https://github.com/wake/purdex/issues/584)）**：1b-1b 只實作 sweep-override pending 與 addPending 骨架；spec §3.4.3 entry trigger（「role 判不出 / evidence 不足 ⇒ 先 stash 到 pending」）要等 hook/probe 實際產 obs 且 pid-tree 判定流程就位時才能定義 → 整組併入 1b-1c。

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

### D10. TraceWriter（Plan review P0-3 修訂：priority buffer + AppendSteps store API）

**初版方案瑕疵**（P0-3）：
1. FIFO channel 滿載時只能決定「丟新進來的這筆」，無法按 Drop Priority 淘汰**既有** queue 內較低優先級項目；spec §3.5.1 的 drop priority table 無法實作
2. `TraceStore.SaveChain()` 是 whole-chain replace API — 同一 `chain_id` 後寫會覆蓋前寫。Arbitrator 想把 flat observation 逐筆送出，放同一 chain 會互蓋、每筆開新 chain 又破壞 trace_id aggregation

**修訂 — 明確拆開 submit 界面、priority ring、以及 store append API**：

#### D10.1 TraceWriter 介面

```go
// internal/module/agent/arbitrator/trace_writer.go

// TraceSubmitter is the only way Arbitrator hands records to the writer.
// Submit never blocks; it performs priority-based admission internally.
type TraceSubmitter interface {
    Submit(record TraceRecord)          // sync enqueue; admission handled inside
    Shutdown(ctx context.Context) error // flush remaining
}

// TraceRecord is the flat Arbitrator-side envelope (§3.5). One record =
// one step row in agent_trace_steps.
type TraceRecord struct {
    TraceID            string
    SpanID             string
    ParentSpanID       string
    SessionID          string
    ObservedGeneration int64
    SourceKind         observation.SourceKind
    Action             string
    Phase              observation.ObsPhase
    Status             string
    Outcome            string
    ReasonCode         string
    ReasonText         string
    DecisionPorts      []observation.DecisionPort
    Evidence           []observation.EvidenceRef
    StartedAt          time.Time
    EndedAt            time.Time
    Seq                int64
    // Fields for Drop-Priority ordering
    DropPriority       int // 0 = highest; hook-committed → 0, probe-committed → 1, ...
}
```

#### D10.2 Priority ring buffer（非 channel）

```go
type writer struct {
    mu         sync.Mutex
    buf        []TraceRecord // bounded capacity = 4096
    cap        int
    flushTick  *time.Ticker  // 100ms
    flushSize  int           // 100
    store      AppendSteps   // store layer (see D10.3)
    shutdownCh chan struct{}
}

// Submit: O(1) 平均；滿時以 drop priority 淘汰 **既有** 最低優先級項目
func (w *writer) Submit(r TraceRecord) {
    w.mu.Lock()
    defer w.mu.Unlock()
    if len(w.buf) < w.cap {
        w.buf = append(w.buf, r)
        return
    }
    // 找 buf 裡 DropPriority 最大（= 優先級最低）且 > r.DropPriority 的 slot 覆寫
    // 若沒有比 r 更低的，drop r 本身 + metric
    worstIdx, worstPrio := -1, r.DropPriority
    for i := range w.buf {
        if w.buf[i].DropPriority > worstPrio {
            worstIdx, worstPrio = i, w.buf[i].DropPriority
        }
    }
    if worstIdx >= 0 {
        metrics.Inc("lights_trace_dropped", "priority="+strconv.Itoa(w.buf[worstIdx].DropPriority))
        w.buf[worstIdx] = r
        return
    }
    metrics.Inc("lights_trace_dropped", "priority="+strconv.Itoa(r.DropPriority))
}

func (w *writer) run(ctx context.Context) {
    for {
        select {
        case <-w.flushTick.C:
            w.flush()
        case <-ctx.Done():
            w.flush()
            return
        }
    }
}

func (w *writer) flush() {
    w.mu.Lock()
    batch := w.buf
    w.buf = make([]TraceRecord, 0, w.cap)
    w.mu.Unlock()
    if len(batch) == 0 { return }
    if err := w.store.AppendSteps(toStoreSteps(batch)); err != nil {
        log.Printf("[agent][arbitrator][trace_writer] append_steps failed: %v", err)
    }
}
```

**Drop Priority 對照表**（spec §3.5.1 line 554-558）：
| Record | DropPriority |
|---|---|
| hook committed | 0 |
| probe committed | 1 |
| hook proposed | 2 |
| probe proposed | 3 |
| sweep proposed | 4 |
| synthetic proposed | 5 |
| reconcile proposed | 6 |

- `flushSize=100`（滿 100 筆強制 flush；目前實作裡以 flushTick 為主要 trigger，滿額 flush 可延後補）
- Flush 頻率 100ms（`time.NewTicker`）
- 滿載換出時 metric `lights_trace_dropped{priority=X}` 記錄被**丟出去**那筆的 priority（不是新進來那筆），便於觀察 queue saturation 是來自哪個優先級

#### D10.3 Store 層新 API — `TraceStore.AppendSteps`

現行 `SaveChain(TraceRecord)` 是 chain-level replace，不適合 flat step batch。新增 append API：

```go
// internal/store/trace.go (new method)

// AppendSteps inserts the given steps. Steps are bucketed by chain_id; for
// each chain that doesn't yet exist, a minimal chain summary row is created
// (StartedAt=min(steps.StartedAt), derived fields from first step).
// Existing chains are left untouched — step rows are INSERT OR IGNORE by
// step_id primary key (idempotent on retry).
func (s *TraceStore) AppendSteps(steps []TraceStep) error
```

**實作要點**（避免 schema change）：
- 不改表結構 — 現行 `agent_trace_chains` + `agent_trace_steps` 與 PR-1b-0 補完的欄位足夠
- 以 `step.ChainID` = `step.TraceID` 為聚合單位（trace_id per-session-per-generation）
- 若該 chain row 不存在 → INSERT 一筆最小 chain summary（`StartedAt = step.StartedAt`，其他 `TerminalStatus=""` 等欄位留 empty；後續 chain summary rehydrate 可走 read-time aggregation，本 PR 不做）
- Step 插入使用 `INSERT OR IGNORE` 避免 `step_id` 衝突（idempotent：同 step_id 第二次進來忽略）
- 整批在單一 transaction 提交（`BEGIN / batch INSERT / COMMIT`）

**測試（store 層新增）**：
- `TestTraceStore_AppendSteps_NewChain_AutoCreates`
- `TestTraceStore_AppendSteps_ExistingChain_AppendsSteps`
- `TestTraceStore_AppendSteps_DuplicateStepID_Ignored`
- `TestTraceStore_AppendSteps_MultiChain_SingleTx` — 單次呼叫跨多個 chain
- `TestTraceStore_AppendSteps_Rollback_OnError`
- `TestTraceStore_AppendSteps_ConcurrentCallers_NoCorruption` — go -race

#### D10.4 與 1b-1c hook 雙寫的關係

- Arbitrator writer 寫 step rows 以 `trace_id` (source_kind in {hook,probe,sweep,reconcile,synthetic}) 為主
- 1b-1c 的 hook 實際 call SubmitObservation 後，同一 `trace_id` 的 hook direct-write（既有 `hookTraceSink`）與 Arbitrator-side observation write **會共存**
- 共存無衝突條件：step_id 全域唯一（uuidv4），`INSERT OR IGNORE` 確保重複送達 idempotent；chain 行由最早 writer 建立
- divergence table 的 idempotency key（session_id, trace_id, event_id, observed_generation）獨立；1b-1c 實際寫 divergence 時基於 projection diff，1b-1b 不碰

### D11. Idempotency cache（Plan review P2-1 修訂：canonical bytes + evidence order 契約）

**初版疑義**（P2-1）：既要測 `EvidenceOrderInvariant` 又寫「若 spec 未要求可改順序敏感」；collision 直接接受。Contract 不定會讓 duplicate 語意在實作時漂移。

**修訂契約**：

```go
// idempotency.go
type idemCache struct {
    // entries[key] = (seq, lastTouched)
    entries map[idemKey]idemEntry
}

type idemKey struct {
    // Canonical bytes of hash inputs, not a pre-hashed int.
    // Using raw-key map (string of bytes) rather than hash map eliminates
    // hash-collision false-positives entirely (Go map handles its own
    // internal hashing — even if two idemKeys collide at map bucket level,
    // Go map compares full key bytes for equality).
    canonical string
}

type idemEntry struct {
    Seq         int64
    LastTouched time.Time
}

func makeIdemKey(obs observation.Observation) (idemKey, error) {
    // canonical = stable JSON of:
    //   { ActorKey: {...}, SourceKind: "hook", Action: "...", Evidence: <sorted> }
    //
    // Evidence 排序規則（order-invariant，P2-1 契約）：
    //   按 EvidenceRef.Key 字典序排序後序列化。Key 相同時按 Value 的 JSON 序列化
    //   字串做 secondary sort。重複 Key+Value 保留，不去重（caller 負責品質）。
    //
    // 回錯當且僅當 Evidence[i].Value 無法 JSON marshal（chan/func）。caller
    // 決定是否 reject obs 還是 bypass idempotency。
}
```

- **Hash collision**：採 canonical-bytes-as-map-key（`string([]byte)` 轉成可比較型別）；Go map 的 bucket collision 由 runtime 處理，不會退化為 false-positive duplicate reject
- **Evidence 順序**：**order-invariant**（P2-1 契約）。makeIdemKey 先排序再序列化
- **Seq 語意**：duplicate 的判定是 `newSeq <= prevSeq`（equal 也算重複，上游應保證 Seq 單調）
- Prune：每次 reconcile 清掉 5 分鐘沒寫入的 entry（簡單 sweep）
- **Value marshal 失敗**：回 sentinel error `ErrEvidenceUnmarshalable`；apply 層 log + **允許過 idempotency 進入下步**（避免單個壞 evidence 吃掉整批 obs）

**測試**：
- `TestIdemKey_Determinism_SameInputs_SameKey`
- `TestIdemKey_EvidenceOrderInvariant_SortedBeforeSerialization` — `[{K:"b",V:2},{K:"a",V:1}]` 與 `[{K:"a",V:1},{K:"b",V:2}]` 產同 key
- `TestIdemKey_EvidenceDuplicateKeyValue_NotDeduped` — 兩個 `{K:"pid",V:1}` 仍保留兩筆（canonical 相同則 key 相同，但這是預期行為：dedup 不是 idemKey 責任）
- `TestIdemKey_ActorKeyEvidence_ShapeIncluded` — Value 為 ActorKey struct 的 evidence 序列化含 session_id/generation/actor_id
- `TestIdemKey_UnmarshalableEvidence_ReturnsError` — chan/func Value → sentinel error
- `TestIdemCache_FirstSeen_Accepts`
- `TestIdemCache_DuplicateLowerSeq_Rejected`
- `TestIdemCache_DuplicateEqualSeq_Rejected` — 明確記錄為 duplicate
- `TestIdemCache_HigherSeq_Accepts_UpdatesWatermark`
- `TestIdemCache_PruneStale_5Min` — mock clock

### D12. Hook-storm throttle（Plan review P1-3 部分修訂）

Spec §3.4.2 line 416「單 session 10ms 內 > 50 observation → drop（只進 trace） + `metrics.Inc("lights_hook_storm_dropped")`」是 Arbitrator 自保機制（避免失控 hook 把 inCh 灌滿導致 reconcile tick 都進不來）。**納入 1b-1b scope**。

**實作**：

```go
// hook_storm.go
type hookStormGuard struct {
    windowSize  time.Duration // 10ms
    cap         int           // 50
    counters    map[string]*hookStormCounter // sessionID → sliding counter
}

type hookStormCounter struct {
    windowStart time.Time
    count       int
}

// ShouldDrop returns true when the per-session rate exceeds cap within the
// current window. Caller drops obs, emits trace-only reason_code=HookStormDropped.
func (g *hookStormGuard) ShouldDrop(sessionID string, now time.Time) bool
```

- 位置：apply pipeline **step 1** 之前（gen gate 之前）——不讓 storm 的 obs 擠到 idem / pending
- SessionID 以外不再細分；storm 是 session 級現象
- 單 goroutine 內呼叫，無 mutex
- 每筆 drop 仍產 trace（`phase=rejected, outcome=skipped, reason_code=HookStormDropped`）便於後驗

**測試**（Task 6 apply_test 分擔）：
- `TestHookStorm_51stObservationIn10ms_Dropped_TraceEmitted`
- `TestHookStorm_SessionIsolated_OneBurstDoesntAffectOther`
- `TestHookStorm_WindowReset_AllowsNewObs`

### D13. Back-pressure 的 1b-1b / 1b-1c 分工明示（Plan review P1-3 修訂）

| 項目 | 規範位置 | 歸屬 |
|---|---|---|
| `Arbitrator.in` cap 1024 + admission priority | §3.5.1 | **1b-1b**（D9） |
| Drop Priority table 實作 | §3.5.1 line 554-558 | **1b-1b**（D10.2） |
| Hook-storm 10ms / 50 obs throttle | §3.4.2 line 414-416 | **1b-1b**（D12） |
| Batching 100/100ms flush | §3.5.1 line 566 | **1b-1b**（D10.2） |
| Sampling（sweep/synthetic proposed 1/10） | §3.5.1 line 567 | **1b-1c**（hook/probe 實際產 obs 時才有流量需要 sample；1b-1b 無上游） |
| Trace retention 24h per-session TTL | §3.5.1 line 568 | **1b-1c 或 Phase 2**（需先有 hook path 產 real 流量估算 TTL 壓力） |

Phase-1 plan (§4 TDD checklist line 97-99) 原列 sampling / retention，本 PR 範圍明示**不含**此兩項；phase-1.md 同步加備註。1b-1c plan 寫作時補 sampling；Phase 2 retention。

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
- [ ] Compile-time interface assertion（**P3-1 修訂**）：在 `trace_id_test.go` 加 `var _ TraceIDMinter = (*traceIDRegistry)(nil)` 與 `var _ TraceIDLookup = (*traceIDRegistry)(nil)` 兩行 package-level 宣告（不是 test function，是 compile-time check）。Negative 方向（Minter 不該有 Get / Lookup 不該有 Mint）不以 comment-style expected-compile-error 驗證（go test 不支援），改由 interface 型別本身的 method set 限制（只要 interface 裡沒宣告該 method，caller 就無法呼叫；測試只需示範「以 Minter 型別收到的變數無法編譯呼叫 Get」留在 plan/doc，不寫為 test）
- [ ] 既有測試 `TestTraceIDRegistry_Mint_*` / `TestTraceIDRegistry_Get_*` 改為 setup 時拆接兩 interface，各自操作對應視圖
- [ ] Watermark test 保留（§PR-1b-1a R2 修正）— 透過 Minter 測試

**Subagent 交付標準**：
- `go test -race ./internal/module/agent/observation/...` 全綠
- `go vet ./...` 無新 warning
- 無跨 package 變動（僅 observation 內部 + 測試）

### Task 2 — #579 arbmode Apply 線性化（published snapshot）

**檔案**：
- `internal/module/agent/arbmode/manager.go`（改 — 去 `pending ArbMode` 欄位 + 加 `published atomic.Pointer[modeTarget]`；OnConfigChange atomic.Store；ApplyAtSessionStart atomic.Load；Snapshot 拼接 current + published.Load()）
- `internal/module/agent/arbmode/manager_test.go`（改 — 可控 interleave 測試取代 100-goroutine 混戰）
- `internal/module/agent/arbmode/manager_export_test.go`（新 — `loadPublishedForTest()` helper）

**TDD checklist**：
- [ ] `TestManager_OnConfigChange_PublishesSnapshot` — OnConfigChange(A)；loadPublishedForTest 回 `{Mode:A, Revision:prev+1}`
- [ ] `TestManager_OnConfigChange_SameValue_NoRevBump` — 相同值 changed=false 且 published.Revision 不動
- [ ] `TestManager_OnConfigChange_EnvLocked_NoPublish` — env-locked 下不改 published
- [ ] `TestManager_Apply_ReadsLatestPublished` — OnConfigChange(A) → OnConfigChange(B) → Apply()：Current==B
- [ ] `TestManager_Apply_CurrentEqualsPublished_Noop` — published.Mode==current Apply no-op
- [ ] `TestManager_Snapshot_NotTorn` — 併發 OnConfigChange 時 Snapshot 讀到 `{Current, Pending, EnvLocked}` 任何時刻都是 valid self-consistent（Pending 可能領先 Current，此屬契約）
- [ ] `TestManager_Apply_RacesOnConfigChange_CaseA` — Controlled：`OnConfigChange(A)` 開始 → atomic.Store 前 `ApplyAtSessionStart()` → Apply 讀到**舊 published**（initial）→ OnConfigChange 完成 publish(A) → 第二次 Apply → Current==A
- [ ] `TestManager_Apply_RacesOnConfigChange_CaseB` — Controlled：`OnConfigChange(A)` 完成 publish → Apply → Current==A（單純 happy path）
- [ ] `TestManager_Apply_RacesOnConfigChange_CaseC` — Controlled：Apply 先 Load（讀到 A）→ 併發 `OnConfigChange(B)` publish(B) 完成 → Apply 寫 current=A → 第二次 Apply → Current==B（B 必在下一 Apply 生效，不會漏）
- [ ] 既有 `TestManager_OnConfigChange_*` / `TestManager_ApplyAtSessionStart_*` / `TestManager_Default*` / `TestManager_EnvLocked_*` 全保留

**實作注意**：
- `atomic.Pointer[modeTarget]` 使用 Go 1.19+ generic atomic pointer
- `Snapshot()` 讀 current 走 `mu.RLock()`；讀 pending 走 `published.Load()`；兩者 interleaved 可接受（spec §8.3 contract 允許 pending 領先 current）
- `NewManager` 建構子結束前必須 `published.Store(&modeTarget{Mode: current, Revision: 0})`，避免 nil pointer
- Case A/B/C 不用 time.Sleep；用 `runtime.Gosched()` + WaitGroup 設 sync point 或直接用 channel 同步 goroutine 啟動時序

**Subagent 交付標準**：
- `go test -race -count=10 ./internal/module/agent/arbmode/...` 10 次跑綠
- 手動驗證：快速連續 PUT `arb_mode` 10 次 → 下一次 SessionStart Apply 必套用最後 PUT 值

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

### Task 4 — frameState + idempotency cache + hook-storm guard

**檔案**：
- `internal/module/agent/arbitrator/frame_state.go`（新）
- `internal/module/agent/arbitrator/frame_state_test.go`
- `internal/module/agent/arbitrator/idempotency.go`（新）
- `internal/module/agent/arbitrator/idempotency_test.go`
- `internal/module/agent/arbitrator/hook_storm.go`（新 — 10ms/50 obs throttle per D12）
- `internal/module/agent/arbitrator/hook_storm_test.go`

**TDD checklist (frame_state)**：
- [ ] `TestFrameState_ZeroSession_Generation0`
- [ ] `TestFrameState_BumpGeneration_SessionStart` — 只 SessionStart 可推進；其他 source/action obs 不直接 bump（由 applySessionStart helper 單路推進）
- [ ] `TestFrameState_ForceEndOldGenActors_SetsEndedAt_SessionRestart` — helper 把所有 oldGen actor 的 `EndedAt` 設 now、reason 記 session_restart，回傳被 end 的 ActorKey list
- [ ] `TestFrameState_ActorLifecycle_Track` — 加 actor、更新 LastActivity、EndedAt set
- [ ] `TestFrameState_WatcherToken_Rotation` — 新 token 寫入後 lookup 回新值
- [ ] `TestFrameState_WatcherToken_ClearOnSessionStart` — 推進 generation 時舊 gen actor 的 WatcherTokens 被清空
- [ ] `TestFrameState_IsPrimary_Transfer` — 新 primary 設置時舊 primary 的 IsPrimary=false 且 EndedAt set
- [ ] `TestFrameState_AllActiveActors_FiltersEnded` — EndedAt != nil 的 actor 不回
- [ ] `TestFrameState_AllActiveActors_FiltersOldGeneration` — generation 推進後舊 gen actor 不在 active list
- [ ] `TestFrameState_SingleOwner_NoMutex` — 結構驗證（reflection / compile-time）

**TDD checklist (idempotency)**（對照 D11 修訂契約）：
- [ ] `TestIdemKey_Determinism_SameInputs_SameKey`
- [ ] `TestIdemKey_EvidenceOrderInvariant_SortedBeforeSerialization` — 由 `[{K:"b",V:2},{K:"a",V:1}]` 與 `[{K:"a",V:1},{K:"b",V:2}]` 產同 key
- [ ] `TestIdemKey_EvidenceDuplicateKeyValue_NotDeduped` — 兩個相同 EvidenceRef 在 key 內保留兩筆（不去重，但因 sort 後 canonical 一致所以 key 相同；test 明示行為）
- [ ] `TestIdemKey_ActorKeyEvidence_ShapeIncluded` — ActorKey struct 為 evidence value 時 canonical 含 session_id/generation/actor_id 三鍵
- [ ] `TestIdemKey_UnmarshalableEvidence_ReturnsSentinelError` — chan/func Value → `ErrEvidenceUnmarshalable`
- [ ] `TestIdemKey_CanonicalBytesAreMapKey_NoHashCollisionFalseDup` — 白盒：兩個不同 canonical string 在 map 裡不會彼此 shadow（Go map 內部 hash collision 不影響 equality）
- [ ] `TestIdemCache_FirstSeen_Accepts`
- [ ] `TestIdemCache_DuplicateLowerSeq_Rejected`
- [ ] `TestIdemCache_DuplicateEqualSeq_Rejected` — seq 相等視為重複
- [ ] `TestIdemCache_HigherSeq_Accepts_UpdatesWatermark`
- [ ] `TestIdemCache_PruneStale_5Min` — mock clock；5 分鐘未更新的 entry 被 prune
- [ ] `TestIdemCache_EvidenceUnmarshalable_BypassesIdempotency_NotReject` — 依 D11 契約：壞 evidence 不吃掉 obs；idem 層 bypass + log

**TDD checklist (hook_storm)**（對照 D12）：
- [ ] `TestHookStorm_UnderCap_Allows` — 49 筆 obs 在 10ms 內皆允許
- [ ] `TestHookStorm_51stObservationIn10ms_Drops`
- [ ] `TestHookStorm_SessionIsolated_OneBurstDoesntAffectOther`
- [ ] `TestHookStorm_WindowReset_AllowsNewObs` — 時間推進超過 windowSize 後 counter 重置

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

**Step 8 Mode branch**（Plan review P0-1 + P2-2 修訂）：
- [ ] `TestApply_PassthroughMode_FrameStateUpdates` — frameState 在 step 1-7 已更新（generation / actor lifecycle / watcher token）；step 8 passthrough 僅控制外部落地，不 rollback frameState
- [ ] `TestApply_PassthroughMode_NoFramesStoreWrite` — 用 fake FramesStore spy；任何 Upsert/Delete 呼叫次數 == 0
- [ ] `TestApply_PassthroughMode_NoDivergenceWrite` — fake DivergencesStore 呼叫次數 == 0（1b-1c 才接）
- [ ] `TestApply_PassthroughMode_NoWSBroadcast` — fake broadcaster 呼叫次數 == 0
- [ ] `TestApply_PassthroughMode_TraceEmitted` — TraceSubmitter 收到 1 筆 record，phase=proposed or committed
- [ ] `TestApply_AuthoritativeMode_FailClosed_EmitsRejectedTrace` — `ArbModeAuthoritative` → emit TraceRecord `phase=rejected, outcome=skipped, reason_code=AuthoritativeNotSupportedPhase1`；FramesStore / divergence / broadcast 均 0 call；frameState 仍正常更新（reducer 層不因模式而退化）
- [ ] `TestApply_AuthoritativeMode_LateModeFlipBackToPassthrough_ResumesNormalAccept` — Manager 從 authoritative 切回 passthrough（下一 SessionStart）後 apply 回歸正常 accept 路徑

**Step 9 Trace**：
- [ ] `TestApply_TraceRecordHasCompleteDecisionPorts` — emit 的 record 保留原 obs 的 DecisionPorts
- [ ] `TestApply_TraceRecordPhaseProposed_WhenAcceptedPassthrough`
- [ ] `TestApply_TraceRecordPhaseRejected_WhenRejected` — reject path 寫 `phase=rejected`
- [ ] `TestApply_TraceRecordAssignsReasonCode` — `reject_*` / hook-storm / session-restart-cleared 各自 reason_code

**SessionStart helper (D5b) 驗證**（apply_test 補 6 case）：
- [ ] `TestApplySessionStart_EndsOldGenActors_WithSessionRestartReason`
- [ ] `TestApplySessionStart_ClearsWatcherTokens`
- [ ] `TestApplySessionStart_ClearsPending_EmitsSessionRestartClearedTracePerObs`
- [ ] `TestApplySessionStart_CallsMinterMintAndPrune`
- [ ] `TestApplySessionStart_CallsArbmodeApply`
- [ ] `TestApplySessionStart_EmitsBoundarySyntheticTrace`

**Hook-storm pre-gate**（在 step 1 之前）：
- [ ] `TestApply_HookStorm_51stObsDropped_BeforeGate` — 第 51 筆 obs 根本不進 step 1（gate 計數不變）
- [ ] `TestApply_HookStorm_EmitsHookStormDroppedTrace`

**Subagent 交付標準**：
- `go test -race -cover ./internal/module/agent/arbitrator/...` coverage ≥80%
- 9 步每步至少一個 happy + 一個 reject 測試
- Mock trace writer 採 `TraceSubmitter` interface（fake 實作記錄收到的 records，提供 `Submitted()` 回 slice）；不再用 channel

### Task 7 — Arbitrator struct + Run() + TraceWriter (priority buffer) + TraceStore.AppendSteps

**檔案**：
- `internal/module/agent/arbitrator/arbitrator.go`（新 — struct + Run + NewArbitrator + InCh）
- `internal/module/agent/arbitrator/arbitrator_test.go`
- `internal/module/agent/arbitrator/trace_writer.go`（新 — priority buffer + 100ms flush；非 FIFO channel）
- `internal/module/agent/arbitrator/trace_writer_test.go`
- **`internal/store/trace.go`（改 — 新 `AppendSteps` 方法，不改 schema）**
- **`internal/store/trace_test.go`（改 — AppendSteps 測試）**

**TDD checklist (arbitrator)**：
- [ ] `TestArbitrator_NewWithDefaults_FieldsWiredCorrectly`
- [ ] `TestArbitrator_Run_ConsumesInCh` — push 1 obs；assert apply 被呼叫
- [ ] `TestArbitrator_Run_ReconcileTickerFires` — fake clock；15s tick 3 次 reconcile
- [ ] `TestArbitrator_Run_ContextCancel_StopsGoroutine` — `ctx.Cancel()` 後 Run 在 100ms 內返回；goroutine leak 檢查
- [ ] `TestArbitrator_InCh_ReturnsSendOnlyChannel`
- [ ] `TestArbitrator_Run_Shutdown_FlushesTraceWriter` — Stop 前 Submit 的 N 筆 record flush 到 store（經 TraceWriter.Shutdown）
- [ ] `TestArbitrator_SingleOwner_NoDataRace` — `go test -race -count=10` 穩定
- [ ] **retryCh 相關測試全部不寫**（scope 移出，見 D2）

**TDD checklist (trace_writer)**（對照 D10.2 priority ring）：
- [ ] `TestTraceWriter_Submit_UnderCap_Buffers`
- [ ] `TestTraceWriter_FlushTick_100ms_DrainsBuffer`
- [ ] `TestTraceWriter_Submit_BufferFull_EvictsLowestPriority` — 預先塞滿 sweep_proposed（DropPriority=4），再 Submit hook_committed（DropPriority=0）→ sweep_proposed 被覆寫、hook_committed 進 buffer
- [ ] `TestTraceWriter_Submit_BufferFull_NoLowerPriority_DropsIncoming` — buffer 已滿且全為 hook_committed；Submit 一筆 sweep_proposed → 丟新進來那筆 + metric `lights_trace_dropped{priority=4}`
- [ ] `TestTraceWriter_Submit_NonBlocking_Always` — mutex 可能短暫持有但不 block I/O
- [ ] `TestTraceWriter_Shutdown_FlushesRemaining`
- [ ] `TestTraceWriter_Shutdown_ContextCancelled_ExitsWithoutFlush` — ctx cancelled in shutdown → best-effort flush 可被放棄
- [ ] `TestTraceWriter_WriteFailure_LoggedNotPanic`
- [ ] `TestTraceWriter_MetricCounter_PerPriority` — 不同 drop priority 各自累計

**TDD checklist (TraceStore.AppendSteps)**（對照 D10.3）：
- [ ] `TestTraceStore_AppendSteps_NewChain_AutoCreatesChainRow` — 原本無此 chain_id → chain + step 都 INSERT
- [ ] `TestTraceStore_AppendSteps_ExistingChain_AppendsSteps_ChainSummaryUntouched`
- [ ] `TestTraceStore_AppendSteps_DuplicateStepID_Ignored` — 第二次送同 step_id 不錯不插入
- [ ] `TestTraceStore_AppendSteps_MultiChain_SingleTransaction`
- [ ] `TestTraceStore_AppendSteps_Rollback_OnError` — mock DB exec 回 error；整批 rollback
- [ ] `TestTraceStore_AppendSteps_ConcurrentCallers_NoCorruption` — 2 goroutine 各 1000 step；go -race 10 count
- [ ] `TestTraceStore_AppendSteps_EnvelopeFields_Roundtrip` — PR-1b-0 的 trace_id / attrs / input_refs 等欄位寫入 + SELECT 回原值

**實作注意**：
- `TraceStore.AppendSteps` 使用 `INSERT INTO agent_trace_chains ... ON CONFLICT(chain_id) DO NOTHING` + `INSERT INTO agent_trace_steps ... ON CONFLICT(step_id) DO NOTHING`
- 整批單一 transaction；失敗 rollback
- Chain summary 欄位缺資訊用 empty string / zero；後續 1b-1c 的 hook path 若以 `SaveChain` 覆蓋同 chain_id，semantic 仍以 hook path 為準（append 只是佔位）
- **Schema 不動**（PR-1b-0 已補完所有需要的 step column）
- TraceWriter cap = 4096（`writer.cap`）；Arbitrator 建構時傳 DI
- Arbitrator.Run Shutdown 走 `traceWriter.Shutdown(ctx)`；不 close channel（沒有 channel）
- Goroutine leak detection：手寫 `verifyNoGoroutineLeak` 或引入 `goleak` 看現有依賴（repo 現況是否有 goleak 依賴先確認；無則自實作 sleep+`runtime.NumGoroutine` 比對）

**Subagent 交付標準**：
- `go test -race -count=10 ./internal/module/agent/arbitrator/... ./internal/store/...` 10 次綠
- manual: integrator test — push 1000 obs、reconcile 3 次、context cancel 後 process 完成，DB 內 agent_trace_steps 有對應 N 筆

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
| `internal/module/agent/arbmode/manager.go` | 改（#579 published snapshot） | 2 |
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
| `internal/module/agent/arbitrator/hook_storm.go` | 新（D12） | 4 |
| `internal/module/agent/arbitrator/hook_storm_test.go` | 新 | 4 |
| `internal/module/agent/arbitrator/pending.go` | 新 | 5 |
| `internal/module/agent/arbitrator/pending_test.go` | 新 | 5 |
| `internal/module/agent/arbitrator/reconcile.go` | 新 | 5 |
| `internal/module/agent/arbitrator/reconcile_test.go` | 新 | 5 |
| `internal/module/agent/arbitrator/apply.go` | 新（含 D5b applySessionStart helper） | 6 |
| `internal/module/agent/arbitrator/apply_test.go` | 新 | 6 |
| `internal/module/agent/arbitrator/arbitrator.go` | 新 | 7 |
| `internal/module/agent/arbitrator/arbitrator_test.go` | 新 | 7 |
| `internal/module/agent/arbitrator/trace_writer.go` | 新（priority buffer + AppendSteps 客戶端） | 7 |
| `internal/module/agent/arbitrator/trace_writer_test.go` | 新 | 7 |
| **`internal/store/trace.go`** | 改（新 AppendSteps 方法） | 7 |
| **`internal/store/trace_test.go`** | 改（AppendSteps 測試） | 7 |
| `internal/module/agent/admission.go` | 新 | 8 |
| `internal/module/agent/admission_test.go` | 新 | 8 |
| `internal/module/agent/module.go` | 改 | 8 |
| `docs/superpowers/plans/2026-04-22-lights-system/phase-1.md` | 改（D13 sampling/TTL 1b-1c 分工註記） | 任一 Stage |

**預估**：~1300 LOC 含測試（T1 ~80 / T2 ~120 / T3 ~200 / T4 ~320（加 hook_storm）/ T5 ~220 / T6 ~340（加 SessionStart helper + authoritative fail-closed）/ T7 ~360（新 AppendSteps store API） / T8 ~200）。比原估 ~1200 多 ~100，主要來自 store 層 AppendSteps + hook_storm + SessionStart helper。

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
| 5 | retryCh + `scheduleRetry([100,250,500]ms)` + `attemptRetry` 3-strike（spec §5.4） | PR-1b-1c（verify 流程接入時一併） |
| 6 | Sampling（sweep/synthetic proposed 1/10；§3.5.1 line 567） | PR-1b-1c（上游有流量後才有意義） |
| 7 | 真 metrics 系統（Prometheus / expvar）取代 atomic package var | Phase 1d / Phase 2 |
| 8 | Authoritative mode frame 寫入（目前 1b-1b fail-closed reject） | Phase 2 |
| 9 | Frame schema 加 Generation + Actors JSON | Phase 2 |
| 10 | Trace retention 24h per-session prune | Phase 2 |
| 11 | Daemon restart frame / actor state replay | Phase 2 |
| 12 | `SaveChain` 與 `AppendSteps` 的 chain summary 合併寫入策略（目前 AppendSteps 建最小 placeholder chain，1b-1c hook 寫 `SaveChain` 會覆蓋） | PR-1b-1c 收尾 |

**#578 / #579 何時可關**：本 PR merge 後即可關（interface 拆分 + published-snapshot 線性化皆已落地 + 測試覆蓋）。
