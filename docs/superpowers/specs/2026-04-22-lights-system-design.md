# 燈號系統 — 統一設計 Design Spec

**Date**: 2026-04-22
**Status**: Design approved, ready for plan
**Revisions**:
- v1 (2026-04-22): 初版
- v2 (2026-04-22): 吃進 codex review v1（20 項 blocker）
- v3 (2026-04-22): 吃進 codex review v2（10 項 blocker + 7 follow-up）。核心哲學釐清：**「沒有反應」是預設，只有明確 evidence 才起反應** → unknown 一律不建 actor、不改 status，只進 trace。PR 數 15→17。

**Inputs**:
- `docs/research/2026-04-21-lights-system-alignment.md`（現況分析 + 目標落差）
- 10 份 codex web research 調查（架構主流對照 + pid tree 驗證）
- memory kickoff：`kickoff_lights_spec.md`
- codex review `task-mo8w4hyl-ys0v5e`（v1 審閱）
- codex review `task-mo8xbpd3-anz4np`（v2 審閱）

## 1. 背景與目標

### 1.1 現況

Purdex daemon 已有燈號骨架（hook provider chain + probe 三類 + sweep 掃描 + frame store），但抽象層與主從規則屬**隱含**。`docs/research/2026-04-21-lights-system-alignment.md` 對 5 點使用者目標評分 🟡 **2/5**：

| 目標 | 分數 | 主要缺口 |
|---|---|---|
| 1. 傳遞過程可監控 | 🔴 2/5 | trace schema 只涵蓋 `hook_post`；probe / sweep / handoff 不寫 trace |
| 2. Hook 主、Probe 輔 | 🟡 3/5 | 主從關係隱含；activity watcher 可能覆寫非 error 的 hook 狀態 |
| 3. Subagent type | 🔴 1/5 | `[]string` 只存 id 不存 type；OpenCode hook 帶 `agent_type` 但被 projection 丟棄 |
| 4. Flow graph | 🟡 2/5 | chain + step 有欄位，但 UI 是 JSON inspector 不是流程圖；缺 decision-port schema |
| 5. 三 agent 對稱 | 🔴 2/5 | cc 完整 / opencode 缺 readiness+operator+typed subagent / codex 多態缺席 |

### 1.2 使用者 5 點目標

1. **燈號傳遞過程可被監控**（先 daemon 側；SPA 只是輔助觀測用，不監控內部狀態傳遞）
2. **架構**：主要 agent type + 多種/多個 subagent + subagent type
3. **Agent 原生 hook 事件為主要依歸，probe 為輔助修正判斷**
4. 傳遞 by session + event（hook / probe 兩種）紀錄，**每次 event 用流程圖呈現每一個判斷端口的 input-reason-output**
5. 支援三種 agent：**cc / codex / opencode**

### 1.3 本 spec 解決的問題

- 把隱含的主從規則升級成 **Arbitrator 單寫者** 架構（hook/probe/sweep 全走 Observation → Arbitrator）
- Trace schema 從 `hook_post` 專用擴成 **通用 event envelope + decision-port 結構**（OTel + ECS + DAP 混合，再加「每個判斷端口的 input/branches/selected」）
- Frame 從「single primary + `[]string` subagents」升級成 **multi-actor model**（primary / proxy / subagent，type-aware）
- 三 agent 差異用 **Capability bits + Optional services contract** 表達，不追求齊頭式對稱
- 補強 pid tree role 判斷（ancestor walk + retry + pending 視窗）解決 shim-hop 錯判
- 明確定義 Arbitrator 的並發所有權、generation 邊界、sweep 先後、restart 恢復、trace back-pressure

---

## 2. 術語

### 2.1 兩種燈號 + 投影規則

**燈號定義**：

| 燈號 | 屬於 | 狀態空間 |
|---|---|---|
| **主燈號**（primary light） | primary actor（有獨立 hook 流） | `running / waiting / idle / error / clear` |
| **次要燈號**（secondary light） | proxy actor 或 subagent | proxy: `running / waiting / idle / error / clear`；subagent: `active / inactive` |

**UI 投影規則**：

| 投影位置 | 呈現對象 | 視覺呈現 |
|---|---|---|
| **Primary color** | 主燈號（session 唯一） | session bar 主色（cc / codex / opencode 各有 palette） |
| **Secondary badge** | 每個 proxy / subagent | session bar 右側 agent-type icon + 小圓點顏色表狀態 |
| **Trace-only** | 未成為 actor 的 Observation（role 判不出、probe timeout、sweep 被拒等） | UI 不呈現，僅進 trace viewer |

投影規則為硬約定：**一個 session frame UI 上只有一個主色（主燈號）+ N 個 badge**。Proxy 不競爭主色位置。

**設計哲學**：「沒有反應」比「錯誤反應」更好。Daemon 在 role 判不出或 status 無法確定時，**不建 actor、不改 status**，僅進 trace。UI 呈現維持現狀（已有 primary 就保留，沒有就維持純 terminal）。

### 2.2 Subagent vs Proxy agent

使用者明確定義，兩者資訊來源與 process 邊界不同：

| 類型 | process 邊界 | 是否觸發 hook | 資訊來源 |
|---|---|---|---|
| **subagent** | 同一 parent process | ❌ 不觸發 | parent 的 `SubagentStart / SubagentStop` hook detail |
| **proxy agent** | 獨立 process（可能不同 agent type） | ✅ 觸發自己的 hook 流 | 自己的完整 hook 流 |

舉例：
- cc 的 Task tool 啟動 general-purpose **subagent**（同 cc process 內）
- cc 呼叫 codex CLI → codex 是 **proxy agent**（獨立 process，自己的 hook）
- codex 呼叫 cc → cc 是 **proxy agent**（獨立 process，自己的 hook）

**重要**：subagent/proxy 對稱於 frame 結構（都是 Actor），只是 role 不同 + 狀態空間不同 + UI 投影位置不同。

### 2.3 Probe 三層職責

| 模式 | 觸發時機 | 典型範例 |
|---|---|---|
| **情境式（OnDemand）** | hook 給了開始，probe 確認結束 | cc `ask` hook 起 → probe 偵測畫面變化確認 waiting 結束 |
| **補充式（Supplementary）** | hook 失效、沒觸發、不涵蓋 | process 崩潰、session 意外死、沒 hook 的 agent（codex 部分） |
| ~~填補式~~ | ~~已廢棄~~ | 改為 per-agent probe catalog 明確宣告 |

**觸發語意**：probe **按需觸發為主**（hook 起點 → 短期輪詢 → 拿到答案或 timeout fallback），**不常駐**。但**架構保留常駐能力**（`Continuous` lifecycle），每 agent 實作一個 `self_detection` probe 但預設 **disabled**，未來需要可開啟。

### 2.4 Actor / Frame / Observation

- **Actor**：frame 內的一個角色（primary / proxy / subagent），獨立燈號
- **Frame**：一個 session 的當下快照，包含 actors + trace 資訊
- **Observation**：對 frame 狀態變化的一個**建議**（hook / probe / sweep 共用介面）
- **Arbitrator**：唯一的 frame writer，消費 Observation，決定 frame 的新狀態
- **Generation**：每次 `SessionStart` 開新 generation；old generation 的 observation 一律拒收

### 2.5 Condition

借用 K8s `conditions` 概念：每個 status 是多個 condition 的 summary。例如：
- `Ready` = `ProcessAlive ∧ (HookSignal = idle ∨ ProbeSignal = idle)`
- `Waiting` = `HookSignal = waiting ∧ ProbeSignal ≠ screen_changed`

Arbitrator 依 condition 組合算 status，而不是靠單一 event 觸發。

---

## 3. 核心架構

### 3.1 整體資料流

```
+-----------+      +-----------+      +----------------+      +-------+      +-------+
| Hook POST | ---> | Hook      | ---> |                |      |       |      |       |
+-----------+      | Collector |      |                |      |       |      |       |
                   +-----------+      |                |      |       |      |       |
+-----------+      +-----------+      |                |      |       |      |       |
| Probe     | ---> | Probe     | ---> |  Observation   | ---> | Arbi- | ---> | Frame |
| Signal    |      | Collector |      |     Channel    |      | trator|      | Store |
+-----------+      +-----------+      |                |      |       |      |       |
                                      | (single        |      | (one  |      |       |
+-----------+      +-----------+      |  writer, one   |      | goroutine) |      |
| Sweep     | ---> | Sweep     | ---> |  consumer)     |      |       |      |       |
| Tick      |      | Collector |      +----------------+      +-------+      +-------+
+-----------+      +-----------+              |                   |             |
                                              v                   v             v
                                         Trace Writer        Reconciler     Broadcast
                                         (所有 obs)        (delta computed)  (SPA subs)
```

**三條原則**：

1. **Observation 單寫者**：hook/probe/sweep 都**不**直寫 frame，只產 Observation 進 channel
2. **Arbitrator 唯一仲裁**：單一 goroutine 消費 channel + 依 condition 組合 → 寫 frame；pending buffer、sweep ordering 都由 Arbitrator 同 goroutine 擁有
3. **所有 Observation 進 trace**：trace 不只是 hook 的 debugging 管道，是跨 source 的統一 event ledger

### 3.2 Frame Multi-Actor Model

```go
type SessionFrame struct {
    SessionID          string
    TmuxPaneID         string
    Generation         int64                 // 每次 SessionStart +1；late obs < current 拒收
    Actors             []Actor               // 主 + proxy + subagents
    HookTsMicro        int64
    ObservedGeneration int64                 // K8s 思路：已觀測到的 frame 版本
    Scenario           string                // 如 current_event_name
    UpdatedAt          time.Time
}

// ActorKey 為複合鍵，防止跨 generation 的 agent_id reuse 衝突
type ActorKey struct {
    SessionID  string
    Generation int64
    ActorID    string   // hook 提供的 id（session_id 或 agent_id）
}

type Actor struct {
    Key                ActorKey
    AgentType          string                // cc / codex / opencode / general-purpose (subagent)
    Role               ActorRole             // primary | proxy | subagent（無 unknown；判不出的 Observation 不建 actor）
    Status             string                // primary/proxy: running/waiting/idle/error/clear
                                             // subagent: active/inactive
                                             // （無 unknown；status 不確定時 Arbitrator 不更動，保留前值）
    PID                int                   // subagent 時 = parent pid（無獨立 process）
    PidAncestry        []int                 // 從 pane root 往下的 pid 鏈
    ParentActorKey     *ActorKey             // proxy 指向 parent primary；subagent 指向 parent
    Lifecycle          Lifecycle             // started_at / ended_at / ended_reason / last_activity
    ObservedGeneration int64                 // 本 actor 最後觀察到的 generation
    Detail             map[string]any        // agent-specific 資料層（UI 不看，debug / module 看）
}

type ActorRole string
const (
    RolePrimary  ActorRole = "primary"
    RoleProxy    ActorRole = "proxy"
    RoleSubagent ActorRole = "subagent"
)

type Lifecycle struct {
    StartedAt    time.Time
    LastActivity time.Time   // 每次 committed observation 更新；reconcile stale detection 用
    EndedAt      *time.Time
    EndedReason  string      // hook_stop / process_exit / probe_timeout / replaced_by_new_primary / ...
}
```

**資料層 vs 顯示層分離**：
- `Actor.Detail` 保存 agent-specific 完整資料（例：codex reasoning tokens、cc permission scope），供 debug 與未來 module 引用
- UI 層只讀 `Status + Role`，依 §2.1 投影規則決定位置

**Invariant**：
- 同一 frame 內 `Role = primary` 的 actor 至多一個（若新 primary 取代舊 primary，舊 primary 必須先 `EndedAt` 設定後再 emit 新 actor）
- `Actor.Key.Generation == frame.Generation`（不存 cross-generation actor）

### 3.3 Observation 介面

```go
type Observation struct {
    TraceID            string
    SpanID             string
    ParentSpanID       string
    SessionID          string
    ObservedGeneration int64                   // 當 observation 產生時看到的 frame generation
    SourceKind         SourceKind              // hook | probe | sweep | reconcile | synthetic
    WatcherToken       string                  // probe 產生時帶自己的 identity；sweep/hook 可空
    Action             string                  // 做什麼（event.action）
    Phase              ObsPhase                // proposed | committed | rejected
    Proposal           StateProposal           // 對哪個 actor 提議什麼狀態變化
    ReasonCode         string                  // 低基數 CamelCase，如 ProcessExitDetected
    ReasonText         string                  // 人類可讀理由
    DecisionPorts      []DecisionPort          // 目標 #4 的流程圖節點資料
    Evidence           []EvidenceRef           // 如 pid / screen_hash / hook_payload_ref
    ObservedAt         time.Time
    Seq                int64
}

type StateProposal struct {
    ActorKey       ActorKey                    // 目標 actor
    SuggestStatus  string                      // 建議 status；Arbitrator 可採納/拒絕
    EndLifecycle   bool                        // subagent/proxy 結束
    EndReason      string
}

type SourceKind string
const (
    SourceHook       SourceKind = "hook"
    SourceProbe      SourceKind = "probe"
    SourceSweep      SourceKind = "sweep"
    SourceReconcile  SourceKind = "reconcile"
    SourceSynthetic  SourceKind = "synthetic"  // timeout 之類內部推導
)

// DecisionPort：Observation 內部的「判斷端口」
// 一個 Observation 可能有 0 或多個 decision port（hook 單純上報可能 0；probe interpret 或 arbitrator apply 有多個）
type DecisionPort struct {
    PortID        string                       // "verify.ancestor_walk" / "arbitrator.source_priority"
    InputRefs     []EvidenceRef                // 判斷輸入
    Branches      []Branch                     // 所有可能分支
    Selected      string                       // 被選到的 branch.id
    Reason        string                       // 選這條的原因
}

type Branch struct {
    ID          string                         // "hit_known_actor" / "fall_through_to_primary"
    Condition   string                         // 判斷條件（人類可讀）
    Outcome     string                         // matched | rejected | skipped
}
```

**注意**：`DecisionPorts` 是目標 #4（每個判斷端口的 input-reason-output 流程圖）落地的資料結構。UI viewer 以 DecisionPort 為節點，Branches 為邊，`Selected` 上色呈現選到的路徑。

**DecisionPort 上限**（trace 體積控制）：每個 Observation `len(DecisionPorts) ≤ 16`。超過 16 視為程式錯誤（panic in dev / log warning + truncate in prod）。單一判斷鏈過長代表該抽成多個 Observation 用 `parent_span_id` 串接。

**WatcherToken 儲存**：
- Probe 實例建立時生成唯一 `watcher_token`（uuid），其 `Identify()` 方法回傳此 token
- Frame 內每個 actor 的 `Detail["current_watcher_tokens"]` 存 `map[probe_id]token`（arbitrator apply `Observation` 時更新）
- 新 watcher 啟動（重新 arm 同一 probe）會 rotate token；老 watcher 的 late callback 因 token 不符被 Arbitrator reject（§3.4.1 第 2 步）
- Daemon restart 後所有 token 失效（pending buffer 清空時一併重置）

### 3.4 Arbitrator 仲裁規則

Arbitrator 是**單一 goroutine** + **condition-based reducer**：

```go
type Arbitrator struct {
    mode           ArbMode                     // Passthrough | Authoritative
    in             <-chan Observation          // cap=1024；滿載行為見 §3.5.1
    retryCh        <-chan retryTick            // cap=256
    frames         FrameStore
    pending        map[ActorKey]*PendingEntry  // 由 Arbitrator 獨占
    lastIdemKey    map[string]int64            // key = hash(actor_key|source|action|evidence_hash)
    traceOut       chan<- TraceRecord          // cap=4096；滿載行為見 §3.5.1
}

func (a *Arbitrator) Run(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)  // reconcile loop
    for {
        select {
        case obs := <-a.in:
            a.apply(obs)
        case t := <-a.retryCh:
            a.attemptRetry(t)
        case <-ticker.C:
            a.reconcile()
        case <-ctx.Done():
            return
        }
    }
}
```

#### 3.4.1 Apply 流程

```
Observation 進來
  ↓
1. Generation gate
   - 若 observation.observed_generation < frame.generation → reject (ReasonCode=StaleGeneration)
   - 若 > frame.generation：
       • 僅 SourceKind=hook 且 Action=SessionStart 可推進 generation
         → frame.generation = observation.observed_generation；清空 pending buffer；emit SessionRestartCleared trace
       • 其他任何 source/action → reject (ReasonCode=UnauthorizedGenerationBump)
  ↓
2. Watcher identity（probe 限定）
   - 比對 observation.watcher_token == frame.actor[observation.proposal.actor_key].detail["current_watcher_tokens"][probe_id]
   - 不符 → reject (ReasonCode=StaleWatcher)
  ↓
3. Idempotency
   - 計算 idem_key = hash(actor_key, source_kind, action, evidence_hash)
   - 若 lastIdemKey[idem_key] 存在且 observation.seq ≤ lastIdemKey[idem_key] → reject (ReasonCode=DuplicateObservation)
   - 否則寫入 lastIdemKey[idem_key] = observation.seq
  ↓
4. Pending window（role 判不出時）
   - 見 §3.4.2
  ↓
5. Source priority
   - hook > probe > sweep > synthetic > reconcile
   - 但有 override clause：probe 的 `error` 可 override hook 的 `waiting`（probe 偵測崩潰）
  ↓
6. Monotone lifecycle
   - 若 actor.Lifecycle.EndedAt != nil → reject (ReasonCode=ActorEnded)
   - 例外：synthetic replaced_by_new_primary 可 end actor
  ↓
7. Invariant check
   - proposal 會建立第二個 primary → 先發 SyntheticEndLifecycle 給舊 primary（reason=replaced_by_new_primary），再 apply
  ↓
8. Mode branch
   - Passthrough（Phase 1）: 計算 proposal 但不寫 frame；寫 divergence table
   - Authoritative（Phase 2 後）: 寫 frame + 更新 actor.Lifecycle.LastActivity + broadcast
  ↓
9. Trace: 該 observation 含完整 DecisionPorts 落 trace
```

**Generation gate 設計理由**：只允許 `hook.SessionStart` 推進 generation 可阻止 late probe / retry / sweep 誤觸 generation 清 pending 的副作用。這是 v2 未寫死、v3 補強的條件。

#### 3.4.2 Pending Window — Role 判定視窗

**設計哲學（v3）**：Pending timeout 或 evict 後 **drop proposal，不建 actor**。Daemon 維持現狀（有 primary 就保留；沒有就維持純 terminal）。完整 trace 紀錄留存可在 trace viewer 重建「發生了什麼但沒反應」。

> 「沒有反應」比「錯誤反應」更好。建一個 role 判不出的 unknown actor 會干擾 primary 的唯一性、污染 UI、並讓後續 observation 的 parent attribution 更不穩。

```go
type PendingEntry struct {
    Key              ActorKey
    FirstSeen        time.Time
    LastSeen         time.Time
    Observations     []Observation           // 按 seq 堆疊，coalescing 後上限 16
    CoalescedCount   int                      // 超過上限時累積計數 + metric
    Deadline         time.Time                // FirstSeen + 2s
}

// Coalescing：同 ActorKey 只保留一份 entry
// 新 Observation 進來 → append 到 entry.Observations
// 超 16 筆 → drop oldest；都只進 trace，無需區分 committed
func (a *Arbitrator) addPending(obs Observation) {
    entry, ok := a.pending[obs.Proposal.ActorKey]
    if !ok {
        // Per-session 上限 8 entries
        if a.pendingCountForSession(obs.SessionID) >= 8 {
            a.dropPendingOldest(obs.SessionID)
            metrics.Inc("lights_pending_evicted")
        }
        entry = &PendingEntry{
            Key:       obs.Proposal.ActorKey,
            FirstSeen: obs.ObservedAt,
            Deadline:  obs.ObservedAt.Add(2 * time.Second),
        }
        a.pending[obs.Proposal.ActorKey] = entry
    }
    entry.LastSeen = obs.ObservedAt
    if len(entry.Observations) >= 16 {
        entry.CoalescedCount++
        metrics.Inc("lights_pending_coalesced_dropped")
        entry.Observations = entry.Observations[1:]  // drop oldest
    }
    entry.Observations = append(entry.Observations, obs)
}

// reconcile tick 檢查 deadline
func (a *Arbitrator) flushPendingDue(now time.Time) {
    for key, entry := range a.pending {
        if now.After(entry.Deadline) {
            // 嘗試最後一次 verify
            if a.tryPromoteToActor(entry) {
                delete(a.pending, key)
                continue
            }
            // v3：drop proposal，不建 actor
            a.emitTraceOnly(entry, "PidTreeUnresolvable")
            delete(a.pending, key)
        }
    }
}

// Evict（per-session 超過 8 entries）時也走 emitTraceOnly，不建 actor
func (a *Arbitrator) dropPendingOldest(sessionID string) {
    oldest := a.findOldestPending(sessionID)
    a.emitTraceOnly(oldest, "PendingEvicted")
    delete(a.pending, oldest.Key)
}
```

**Bounded protection（hook storm）**：
- Per-session pending 上限 **8 entries**；超出 drop 最舊 entry（只進 trace） + `metrics.Inc("lights_pending_evicted")`
- 單 session 10ms 內 > 50 observation → drop（只進 trace） + `metrics.Inc("lights_hook_storm_dropped")`

**`emitTraceOnly` 行為**：把 pending entry 的所有 observation 展開成 trace record，`phase=rejected, outcome=skipped, reason_code={PidTreeUnresolvable|PendingEvicted|HookStormDropped}`。UI 不收到任何 frame 變化事件。

#### 3.4.3 Sweep vs Pending 勝負

Sweep 掃出「這個 actor 應該 end」的 Observation 進入 apply，但 actor 仍在 pending buffer 時：

```go
// apply() 第 6 步 Monotone lifecycle 之前加：
if obs.SourceKind == SourceSweep && a.isPending(obs.Proposal.ActorKey) {
    // Sweep 不得覆寫仍未 verified 的 actor
    // 做法：把 sweep observation 累積到 pending entry，等 pending 解決後 replay
    a.addPending(obs)
    return
}
```

**原則**：Sweep 永遠弱於 hook 起源的 verify 結果。
- 若 pending timeout → drop proposal（§3.4.2），累積的 sweep observation 一併 drop 進 trace（沒有 actor 可供 sweep 結束）
- 若 pending promote 為 actor → replay 累積的 sweep observation；若仍判 `actor should end`，正常走 monotone lifecycle

#### 3.4.4 Generation 邊界

每次 `SessionStart` hook 觸發：
1. Generation +1
2. 舊 generation 的所有 actor 強制 `EndedReason=session_restart`（若未 end）
3. Pending buffer 清空（entry 帶不上 new generation；已處理過的 obs 進 trace 記 `reason_code=SessionRestartCleared`）
4. 新 actor 進 frame 帶新 generation

#### 3.4.5 Reconcile Loop（每 5s）

**v3 設計哲學**：reconcile **不主動改變 actor.status**（status 變化只能由明確 evidence 驅動：hook / probe 的確定信號 / sweep 的 process 死偵測）。

```go
func (a *Arbitrator) reconcile() {
    now := time.Now()
    a.flushPendingDue(now)

    for _, actor := range a.frames.AllActive() {
        // Stale detection：30s 沒活動 → 只進 trace（phase=proposed, outcome=skipped），不 apply status 變化
        if now.Sub(actor.Lifecycle.LastActivity) > 30*time.Second {
            a.traceOut <- TraceRecord{
                SourceKind:  SourceReconcile,
                Action:      "actor.stale_detected",
                Phase:       PhaseProposed,
                Outcome:     OutcomeSkipped,
                ReasonCode:  "ReconcileStaleNoted",
                ReasonText:  "actor 30s 無活動，但 reconcile 不主動改 status",
                // 不進 apply pipeline，不變 frame
            }
        }
    }
}
```

**解決 stale actor 的正確做法**：
- `common.motion` probe 持續偵測（未來常駐模式）發現 pane 無畫面變化 → 產明確 proposal
- `liveness` probe 發現 process 死 → 產 `EndLifecycle` proposal
- Sweep 發現 pane pid 不存在 → 產 `EndLifecycle` proposal
- Reconcile 只做**觀察者**，不自己下判斷

### 3.5 Trace Envelope（OTel + ECS + DAP 混合 + Decision Ports）

所有 Observation 都落 trace。一級欄位：

```
trace_id           — per session per generation
session_id         — 對應 frame
event_id           — globally unique
span_id / parent_span_id
name               — 如 "hook.post.SessionStart"
kind               — internal | server | client (OTel)
source_kind        — hook | probe | sweep | reconcile | synthetic
watcher_token      — probe 的 watcher identity (nullable)
phase              — proposed | committed | rejected
status             — success | failure (OTel: 執行成功/失敗)
outcome            — matched | rejected | skipped | emitted (ECS 語意)
action             — 做什麼
reason_code        — CamelCase 低基數
reason_text        — 人類可讀
decision_ports     — []DecisionPort（§3.3 定義；空陣列 = 無判斷）
scenario_key       — 當前的 event 名或狀態組合
input_refs         — 引用輸入資料的 ref（payload 大時用 ref）
output_refs        — 引用輸出資料的 ref
state_before_ref   — actor 狀態 before
state_after_ref    — actor 狀態 after
evidence_refs      — 如 pid / screen_hash / hook_payload_ref
attrs              — 額外 flat attribute map
started_at / ended_at / seq
observed_generation
```

**design 對照**：
- `status` 單純表「這次執行成功/失敗」（OTel 語意）
- `outcome` 表「分支結果是否落實」（ECS 語意）。例如 probe 觀測成功但 Arbitrator 拒絕 → `status=success, outcome=rejected`
- `reason_code` 低基數適合 metrics 聚合；`reason_text` 人類可讀（debug 流程圖節點 tooltip）
- `decision_ports` 是目標 #4 主載體（§3.3）

#### 3.5.1 Back-Pressure Policy

**問題**：現況 trace sink queue 256，滿則 drop（`internal/module/agent/trace.go:26`）；全量 observation 後寫入量 10× 以上。v3 需同時定義 **Arbitrator 入口**（`inCh / retryCh`）與 **Trace 出口**（`traceOut`）的滿載策略。

**三層策略**：

| 層 | Channel | 容量 | 滿載行為 | Metric |
|---|---|---|---|---|
| **Arbitrator in** | `Arbitrator.in` | 1024 | 上游（hook handler / probe / sweep）`select + default` 走 non-blocking send；滿則 drop proposed + 記 metric；committed 改成 blocking send with 100ms timeout（timeout 後 drop + emergency log）| `lights_arb_in_dropped{priority}` |
| **Retry** | `Arbitrator.retryCh` | 256 | 滿則 drop retry tick + 記 metric（原 observation 已進 pending buffer，錯過的是 retry timer 觸發）| `lights_arb_retry_dropped` |
| **Trace writer** | `Arbitrator.traceOut` | 4096 | Batching 100/100ms；滿則按 Drop Priority 丟棄 | `lights_trace_dropped{priority}` |

**Arbitrator 入口 submit helper**（上游統一使用）：

```go
func (m *Module) submitObservation(obs Observation) {
    priority := obs.admissionPriority()  // committed > proposed
    if priority == AdmissionCommitted {
        // Blocking with 100ms timeout（committed 不能丟）
        timer := time.NewTimer(100 * time.Millisecond)
        defer timer.Stop()
        select {
        case m.arbIn <- obs:
        case <-timer.C:
            metrics.Inc("lights_arb_in_dropped", "priority=committed")
            log.Error("arb_in full, committed observation dropped", obs)
        }
        return
    }
    // Proposed 非阻塞
    select {
    case m.arbIn <- obs:
    default:
        metrics.Inc("lights_arb_in_dropped", "priority=proposed")
    }
}
```

**Drop Priority**（queue 滿時）：

```
hook committed > reconcile committed > probe committed >
hook proposed > probe proposed > sweep proposed > synthetic proposed
```

**Batching / Sampling / Retention**：

| 層 | 策略 |
|---|---|
| **Batching** | Arbitrator → trace writer 走 100-event batch，每 100ms 或滿額即 flush |
| **Sampling** | Sweep/synthetic 的 proposed（未改變 phase）只取 1/10；committed 全留 |
| **Retention** | Alpha 階段不保留；trace DB per-session TTL 24h（reconcile tick 清 expired） |

**Metrics**：
- `lights_trace_queue_depth`
- `lights_trace_dropped{priority=<level>}`
- `lights_arb_in_dropped{priority=<committed|proposed>}`
- `lights_arb_retry_dropped`
- `lights_trace_batch_flush_ms`

### 3.6 Capability Bits + Optional Services

不追求齊頭對稱。每 agent 聲明 **capability bits + optional services**。

#### 3.6.1 Capability Bits（擴張版）

| Capability | 含義 | cc | codex | opencode |
|---|---|---|---|---|
| `CanWait` | 支援 `waiting` 狀態 hook | ✅ | ❌ | ✅ |
| `CanError` | 支援 `error` 狀態 hook | ✅ | ❌ | ✅ |
| `CanClear` | 支援 `SessionEnd / clear` hook | ✅ | ❌ | ✅ |
| `CanPermissionRequest` | `PermissionRequest` hook | ✅ | ❌ | ✅ |
| `CanSubagent` | `SubagentStart/Stop` hook | ✅ | ❌（issue #16226）| ✅ |
| `HasReadiness` | 可從 pane content 判 readiness | ✅ full | ❌ stub | ❌（PR-4b 補）|
| `HasOperator` | `Interrupt / Exit` 外部控制 | ✅ | ❌ | ❌ |
| `HasStatusline` | statusline wrapper | ✅ | ❌ | ❌ |
| `HasHistory` | 可回讀 session history | ✅ | ❌ | ❌ |
| `HasHookInstaller` | 自動安裝 hook script | ✅ | ❌ | ✅ |
| `HasStreamResume` | 可 resume 中斷 session（stream 恢復） | ✅（CCSessionID）| ❌ | ❌ |

Core 使用 capability 決定：UI 是否顯示特定狀態、rescue 規則是否啟用、handoff / operator / statusline / history 等功能是否開放。

#### 3.6.2 Optional Services Contract

僅有 bits 不足。Capability 為 true 時，對應 service 必須提供可呼叫實作：

```go
type AgentSpec interface {
    Descriptor() AgentDescriptor
    Provider() AgentProvider
    ProbePolicy() ProbePolicy

    // Optional services — 回傳 nil 即 capability 缺席
    Operator() Operator
    Statusline() StatuslineInstaller
    History() HistoryProvider
    HookInstaller() HookInstaller
    StreamResumer() StreamResumer
}

// 呼叫端模式
if op := spec.Operator(); op != nil {
    op.Interrupt(ctx, session)
}
```

Core 註冊時驗證 capability bits 與 service 一致性：`CapabilitySet.HasOperator == (spec.Operator() != nil)`。不一致 → 註冊失敗。

---

## 4. 抽象層

### 4.1 AgentSpec 五層分離

```go
type AgentSpec interface {
    Descriptor() AgentDescriptor      // ID / DisplayName / Icon / DetectHints / Capabilities
    Provider() AgentProvider          // hook 側（已有 DeriveStatus）
    ProbePolicy() ProbePolicy         // probe 側（新增）

    // Optional services — 回傳 nil 即 capability 缺席
    Operator() Operator
    Statusline() StatuslineInstaller
    History() HistoryProvider
    HookInstaller() HookInstaller
    StreamResumer() StreamResumer
}

type AgentDescriptor struct {
    ID           string
    DisplayName  string
    Icon         string              // phosphor icon name
    DetectHints  []DetectHint        // pane content / cmdline 偵測 hint
    Capabilities CapabilitySet       // 見 §3.6.1
}

type AgentProvider interface {
    Identify(ctx context.Context, h ProcessHint) (bool, error)
    NormalizeHook(raw HookPayload) (NormalizedEvent, error)
    DeriveStatus(prev Actor, ev NormalizedEvent) StateProposal
}
```

### 4.2 ProbePolicy

```go
type ProbePolicy interface {
    ProbeCatalog() []ProbeDefinition
    PlanProbesForHook(h HookKind) []ProbeBinding
    ContinuousProbes() []ProbeBinding          // 目前全 disabled
    Interpret(sig ProbeSignal, ctx SignalContext) []Observation
}

type ProbeDefinition struct {
    ID           string              // "cc.self_detection" / "common.motion"
    Description  string
    Produces     []string            // 產生的 reason_code
}

type ProbeBinding struct {
    ID         string
    Lifecycle  ProbeLifecycle        // OnDemand | Continuous
    Enabled    bool                  // lifecycle 與 enable 分開
}

type ProbeLifecycle interface { isLifecycle() }

type OnDemand struct {
    TriggerHook  HookKind            // 哪個 hook 觸發
    InitialDelay time.Duration
    PollInterval time.Duration       // 短期輪詢間隔
    Timeout      time.Duration       // 視窗逾時
}
func (OnDemand) isLifecycle() {}

type Continuous struct {
    Interval time.Duration           // 長期輪詢間隔
}
func (Continuous) isLifecycle() {}
```

### 4.3 Probe Scheduler 行為

**OnDemand 觸發流程**：

```
hook fires ──► ProbePolicy.PlanProbesForHook(hook_kind)
              │
              ├─ returns [ProbeBinding{id, OnDemand{trigger, delay, interval, timeout}}, ...]
              │
              ▼
Scheduler arms probe：
  t0 = now + InitialDelay
  schedule tick every PollInterval until:
    - probe returns definitive signal → cancel + emit Observation（含 proposal，Arbitrator apply）
    - timeout 觸及 → cancel + emit trace-only record（不產 proposal，不進 apply pipeline）
    - 對應 actor ended → cancel（no observation）
```

**v3 修正：Probe timeout 不產生狀態變化 proposal**。原因：timeout 代表「這次沒偵測到確定信號」，不等於「進入 error」或「變成 unknown」；依設計哲學「沒有反應」優於「錯誤反應」，只進 trace（`reason_code=ProbeTimeout`，`phase=proposed, outcome=skipped`），Arbitrator 不 apply 改變 status。後續若該 actor 仍需明確狀態判斷，由下一次 hook 或重新 arm 的 probe 驅動。

Probe 產 Observation 時必須帶 `WatcherToken = probe.IdentityToken()`，Arbitrator 依 `WatcherToken` 鑑別 stale callback（§3.4.1 第 2 步）。Probe 被 cancel 或 timeout 時 rotate token，舊 callback 自動被 reject。

**Continuous 行為（目前 disabled）**：

```
module startup ──► foreach agent.ContinuousProbes() where Enabled=true
                   schedule long-interval tick

每 tick：
  執行 probe.Run() → signal → Interpret → Observation
```

### 4.4 Common Probes

橫向共用給所有 agent 的底線能力：

| Probe ID | 行為 | 用途 |
|---|---|---|
| `common.motion` | 比對 pane 抓屏 hash 是否變化 | 情境式：hook 起 probe 確認 waiting / idle 結束 |
| `common.rainbow_text` | 偵測彩虹字（spinner）出現 | 許多 CLI 的 loading indicator |

Agent `ProbePolicy` 可在 `PlanProbesForHook` 返回 `common.motion` binding，不需 per-agent 重新實作。

### 4.5 Registry — 顯式註冊

避免 `init()` 副作用與 Go `plugin` 的 ABI 脆弱性：

```go
// internal/agent/registry.go
type Registry struct {
    specs map[string]AgentSpec
}

func RegisterBuiltinAgents(r *Registry) {
    r.Register(cc.NewSpec())
    r.Register(codex.NewSpec())
    r.Register(opencode.NewSpec())
}

func (r *Registry) Register(spec AgentSpec) error {
    // 驗證 capability bits 與 optional services 一致
    caps := spec.Descriptor().Capabilities
    if caps.HasOperator != (spec.Operator() != nil) {
        return fmt.Errorf("capability/service mismatch: HasOperator")
    }
    // ... 其他 bits 同樣檢查
    r.specs[spec.Descriptor().ID] = spec
    return nil
}

func NewDefaultRegistry() *Registry {
    r := &Registry{specs: map[string]AgentSpec{}}
    if err := RegisterBuiltinAgents(r); err != nil {
        panic(err)  // dev 時必須修正
    }
    return r
}
```

### 4.6 Backward compatibility

**舊 `AgentProvider` 介面不破壞**：

- PR-3a 引入 `AgentSpec` 包裝層，`Spec.Provider()` 轉呼既有 provider
- 舊的 hook path 仍透過 provider 走（到 PR-3b 才從 hook collector 改寫走 observation bus）
- PR-4a/4b 逐個 agent 補 `ProbePolicy`，未補的 agent 使用 `NoopProbePolicy`

#### 4.6.1 PR-3a Legacy Provider Compat Adapter

**Daemon 起不來風險**：現有 `internal/module/stream/module.go:39` 直接讀 `cc.operator`；`internal/module/agent/handler.go` 多處對 provider 做 type assertion（`handler.go:311/381/539`）。若 PR-3a 直接換成 `AgentSpec` 介面，未升級的呼叫端會 nil panic。

**Compat Adapter 設計**：

```go
// PR-3a 引入：legacy call site 透過 adapter 繼續拿 concrete provider
type LegacyProviderAdapter struct {
    spec AgentSpec
}

// 讓 spec.Provider() 實作既有 concrete provider 介面（或提供相容 shim）
// 保留時間：PR-3a 合併 → PR-5a 全面切換走 optional services → 移除 adapter
func (r *Registry) LegacyProvider(agentID string) concrete.Provider {
    spec, ok := r.specs[agentID]
    if !ok { return nil }
    return &LegacyProviderAdapter{spec: spec}.AsLegacy()
}
```

**PR-3a 不動的呼叫端**（靠 adapter 保持舊行為）：
- `stream/module.go:39` → 透過 `registry.LegacyProvider("cc")` 拿 `cc.operator` 等效物件
- `agent/handler.go` 的 provider type assertion → 透過 adapter 還原介面
- 其他 `*stream/orchestrator.go` 對 `CCSessionID / CCOperator` 的直取 → 暫時保留（PR-5a 才解耦）

**PR-5a 移除 adapter**：
- 所有 call site 改用 `spec.Operator() / spec.Statusline() / spec.StreamResumer()` 動態分派
- Adapter 與 legacy provider 介面一併刪除

**Registry 啟動時一致性驗證**：
`Registry.Register()` 檢查 capability bits 與 optional services 一致（§4.5），若 cc/codex/opencode 任一不一致 → `panic` 阻止 daemon 啟動。這是 **dev-time 保護**；production deployment 前必定會觸發過測試環境的 registry validation。

---

## 5. Pid Tree Role 判斷

### 5.1 Pid tree 驗證結論

**可靠性評級：🟡 黃燈**（需要補強才到綠）

- 「驗 pane-locality」**夠硬**（`PidAncestorIncludes` 可靠判斷 hook 是否屬於 pane）
- 「穩定分 primary/proxy」**還差兩塊**：
  1. Parent attribution 要用 **ancestor walk**（修 `cc → node shim → codex` 這類多跳錯判）
  2. **Retry + pending 視窗**（降低 3-10% hook 落 unknown 的比例）

### 5.2 嚴格策略（D1=C）

判不出就**不認**，在 pending buffer 暫存。Pending timeout 後 **drop proposal**（不建 actor，不改 frame），只保留完整 trace（§3.4.2）。設計哲學：「沒有反應」比「錯誤反應」更好。

### 5.3 Ancestor Walk

**問題**：現況 `frame_ops.go:113-119` 只看直接 PPID：
```go
parent, err := m.frames.FindByPanePID(req.TmuxPaneID, info.PPID)
```
`cc → node shim → codex` 這種多跳 wrapper 會找不到 parent frame → proxy 被當 orphan。

**修正**：從 hook pid 往上 walk，找到最近一個**已 verified**的 actor 作為 parent：

```go
func (m *Module) findParentActor(pane string, hookPID int) (*Actor, error) {
    ancestors, err := liveness.WalkAncestors(hookPID)  // [self, parent, grandparent, ...]
    if err != nil {
        return nil, err
    }

    for _, pid := range ancestors[1:] {  // 跳過 self
        if shim.IsKnownShim(pid) {
            continue  // node / bash wrapper 跳過
        }
        if actor := m.frames.FindActorByPID(pane, pid); actor != nil {
            return actor, nil
        }
    }
    return nil, ErrNoAncestorActor
}
```

**Shim 清單**（初始版）：
- `node` / `node22` / `node24`
- `bash -c` / `sh -c` / `zsh -c`
- `env` wrapper

由 `CC_HOOK_DEBUG=1` 環境變數可觀察 shim-hop trace，後續可擴充。

### 5.4 Retry + Pending Window（並發所有權版）

**並發規則**：
- Retry **不在 hook handler goroutine** 內 `time.Sleep`（否則 handler 被阻塞）
- Hook handler 同步做第一次 verify；失敗 → 把 observation 送入 Arbitrator 的 pending buffer（by channel），由 Arbitrator 單一 goroutine 排程 retry

```go
// hook handler 端（每個 hook 一個 goroutine）
func (m *Module) handleHook(ev hookEvent) {
    res := m.verify.Run(ev)  // 同步第一次 verify
    obs := m.buildObservation(ev, res)
    m.arbitratorCh <- obs     // 送給 Arbitrator；即使 pending 也由 Arbitrator 決定
}

// Arbitrator 端（單一 goroutine）
func (a *Arbitrator) apply(obs Observation) {
    // ... 前面步驟
    if needsRetry(obs) {
        a.addPending(obs)
        a.scheduleRetry(obs, [100, 250, 500]ms)
        return
    }
    // 正常 apply
}

func (a *Arbitrator) scheduleRetry(obs Observation, delays []time.Duration) {
    // 用 timer 排程，不阻塞 goroutine
    for i, d := range delays {
        time.AfterFunc(d, func() {
            a.retryCh <- retryTick{obs: obs, attempt: i+1}
        })
    }
}
```

Arbitrator main loop 多加一個 select case（§3.4 已列）：
```go
case t := <-a.retryCh:
    a.attemptRetry(t)
```

**Coalescing + Bounded**（§3.4.2 已寫）：
- 同 `ActorKey` 合成單一 pending entry
- Per-session ≤ 8 entries；超出 evict 最舊
- Hook storm（10ms > 50 obs）drop（只進 trace）

#### 5.4.1 Handler 同步/非同步邊界（plan 層定 diff）

Spec 只定**介面與語意**：
- Hook handler goroutine 負責：(a) 同步第一次 verify、(b) build Observation、(c) submit 給 arbitrator（走 §3.5.1 submit helper，滿載退讓）、(d) HTTP 回 `202 Accepted`
- Arbitrator goroutine 負責：pending buffer 管理、retry 排程、trace 寫入、frame broadcast

**Plan 層需決定的具體切點**（不在 spec 規範）：
- 現有 `internal/module/agent/handler.go:68` `handleEvent()` 內的 `verify` / `applyFrameEvent` / watcher 管理 / broadcast 如何拆
- `verify.Run()` 在 handler 層回傳的 retry signal 如何序列化成 retry Observation
- `broadcast` 從 handler 移入 Arbitrator 後，對外 SSE / WS 的 ordering 保證
- 原 tests 若 stub 了 `verifyEventFn` 與 `applyFrameEvent` → plan 層決定 rewrite 或新測試檔

**工作量提醒**：此處變更涉及**重切 handler 同步/非同步語意 + trace path + 測試假設**，plan 會比 spec 字面看起來大。PR-2a 分量估計應納入此邊界重構。

### 5.5 Role 判斷表 + Primary 替換規則

| 情境 | pane pid 驗證 | 直接 PPID 命中 actor | ancestor walk 命中 actor | 結果 |
|---|---|---|---|---|
| hook 來自 pane 內且是 session root process | ✅ | N/A | N/A | 建 actor，`role=primary` |
| hook 來自 pane 內、PPID 是另一 actor process | ✅ | ✅ | ✅ | 建 actor，`role=proxy`（parent = 那 actor） |
| hook 來自 pane 內、PPID 是 shim、祖父是 actor | ✅ | ❌ | ✅ | 建 actor，`role=proxy`（parent = 祖父 actor） |
| hook 來自 pane 內、無任何祖先是 actor | ✅ | ❌ | ❌ | 見下「新 primary 替換規則」 |
| hook 不在 pane tree（pending 3 retry + timeout）| ❌ | N/A | N/A | **drop proposal**（不建 actor），`emitTraceOnly` + `reason_code=PidTreeUnresolvable`；frame 維持現狀 |

**新 primary 替換規則**（修 v1 §5.5 衝突）：

當「無祖先 actor」情境發生：
1. 檢查 frame 是否已有 verified primary：
   - **有**：發 `SyntheticEndLifecycle` 給舊 primary（`EndedReason=replaced_by_new_primary`），再 emit 新 primary actor
   - **無**：直接 emit 新 primary actor
2. 保證 invariant：同一 frame 內同時至多 1 個 primary

**注意**：subagent 不由 pid tree 判，由 parent 的 `SubagentStart` hook detail 宣告。

### 5.6 Subagent 歸屬

當 parent 的 `SubagentStart` hook 觸發時，parent provider 產 Observation：

```go
Observation{
    SourceKind: SourceHook,
    Action:     "subagent.start",
    Proposal: StateProposal{
        ActorKey: ActorKey{
            SessionID:  sessionID,
            Generation: currentGeneration,
            ActorID:    hookDetail["agent_id"].(string),
        },
        SuggestStatus: "active",
    },
    Evidence: []EvidenceRef{
        {Key: "parent_actor_key", Value: parentActor.Key},
        {Key: "subagent_type",    Value: hookDetail["agent_type"]},
    },
}
```

Arbitrator 在 apply 時：
1. 讀 `parent_actor_key` 找 parent actor（必須已 verified）
2. 新增 `Actor{Role: RoleSubagent, AgentType: hookDetail["agent_type"], PID: parent.PID, ParentActorKey: &parent.Key}`
3. 放入同 frame

---

## 6. 三 Agent 現況與目標

### 6.1 現況對稱矩陣

| 面向 | cc | codex | opencode |
|---|---|---|---|
| Provider Identify | 完整 | 完整 | 完整 |
| Provider IsAlive（過渡殼） | 完整（轉呼 probe）| stub | stub |
| Hook status 支援 | running / waiting / idle / error / clear | running / idle（**缺 waiting / error / clear**）| running / waiting / idle / error / clear |
| Readiness Checker | 可判三態 | **stub 永遠 running** | **無 checker（未註冊）** |
| Operator (Interrupt/Exit) | 完整 | 無 | 無 |
| Hook event 豐富度 | 9 種（含 Notification） | 3 種（SessionStart / UserPromptSubmit / Stop）| 8 種（plugin 已 map） |
| SPA icon / metadata | 完整 | 完整 | 部分 |

### 6.2 Capability 對照（擴張版）

| Capability | cc | codex | opencode |
|---|---|---|---|
| `CanWait` | ✅ | ❌ | ✅ |
| `CanError` | ✅ | ❌ | ✅ |
| `CanClear` | ✅ | ❌ | ✅ |
| `CanPermissionRequest` | ✅ | ❌ | ✅ |
| `CanSubagent` | ✅ | ❌ | ✅ |
| `HasReadiness` | ✅ | ❌ | ❌→✅（PR-4b） |
| `HasOperator` | ✅ | ❌ | ❌ |
| `HasStatusline` | ✅ | ❌ | ❌ |
| `HasHistory` | ✅ | ❌ | ❌ |
| `HasHookInstaller` | ✅ | ❌ | ✅ |
| `HasStreamResume` | ✅（CCSessionID）| ❌ | ❌ |

### 6.3 OpenCode 提升

- 實作 `ReadinessChecker`（平行於 cc 的 `Allow+Deny → waiting`）→ 開 `HasReadiness`
- `ProbePolicy` 補齊 `common.motion` binding
- Subagent typed：plugin 已在 hook 帶 `agent_type`，只需 core 在 frame schema 保留即可（PR-4c）

### 6.4 Codex 提升

- **保持多數 capability=false**（不補 waiting/error/clear），UI 降級呈現
- `ProbePolicy` 補齊 `common.motion`（補 idle→running 邊界偵測）
- Readiness stub 明示 capability `HasReadiness=false`（PR-0 已做）

### 6.5 三 agent self_detection 常駐 probe（disabled）

| Agent | Probe ID | 行為 | 預設 |
|---|---|---|---|
| cc | `cc.self_detection` | `PidAncestorIncludes` + identify 確認 pane 內有 cc process | disabled |
| codex | `codex.self_detection` | 同上 for codex | disabled |
| opencode | `opencode.self_detection` | 同上 for opencode | disabled |

未來若 `watchAlive` 形式的常駐監測確需啟用，啟動單一 probe 而非重寫架構。

### 6.6 Stream Handoff 泛化（PR-5a 拆 3 個子 PR）

現況 stream orchestrator 硬依 `CCSessionID` 與 `CCOperator`（`internal/module/stream/orchestrator.go:70`、`165`）；`internal/module/session/provider.go:15` 的 session metadata 也直接長 `CCSessionID` 欄位。

**工作量**：涉及 `stream` / `session` / `agent` 三個 module。PR-5a 實際拆成 **三個子 PR**：

| 子 PR | 範圍 | 尺寸 |
|---|---|---|
| **PR-5a0** | Session metadata 中性化：`CCSessionID` → `ResumeToken` 之類中性欄位；資料庫 schema + 所有讀寫點改名 | 中 |
| **PR-5a1** | Stream orchestrator 改造：硬編 `cc.operator` / `ccOps` 走 `spec.Operator()` / `spec.StreamResumer()` 動態分派 | 中 |
| **PR-5a2** | API 層 capability 分派：`/api/agent/status`、statusline installer 改走 `spec.Statusline()` / `spec.Descriptor().Capabilities` 判斷 | 中 |

**介面設計**：

- 新介面 `StreamResumer`（§3.6.2 Optional services）
  ```go
  type StreamResumer interface {
      ResumeHint(session Session) (ResumeParams, bool)
      BuildCommand(params ResumeParams) []string
  }
  ```
- cc 實作讀 `CCSessionID`；codex/opencode 回 `(ResumeParams{}, false)` 表示不支援
- Stream orchestrator 改為 `if spec.StreamResumer() != nil { ... }` 動態分派
- Operator 同此模式（`spec.Operator()`）

---

## 7. Trace Viewer UI

（屬 SPA，Phase 6 實作）

### 7.1 設計目標

- Session 時間軸：主視覺是 **event 流程圖**（節點 = DecisionPort / 邊 = Branch / 轉接 = actor frame 變化）
- 每節點顯示 `port_id / selected_branch / reason` 摘要
- 點擊節點展開 DAP-style inspector：完整 branches + input_refs + evidence

### 7.2 三層顯示

1. **Timeline bar**（水平）
   顯示 session 的 event 時間軸，顏色 = `source_kind`（hook 藍 / probe 綠 / sweep 橘 / synthetic 灰）

2. **Flow graph**（中央）
   actor 為 lane（swim-lane 佈局），Observation 為節點。Observation 內含多個 DecisionPort 時，子節點展開。連線表示 `parent_span_id → span_id`。
   每節點小卡片：`name / source_kind / status:outcome / reason_code`。
   DecisionPort 子節點：`port_id / selected / reason`，邊著色 selected=綠 / rejected=灰。

3. **Inspector**（右側，lazy-load）
   展開一個節點時才查 detail API：`input_refs / output_refs / state_before_ref / state_after_ref / evidence_refs / attrs / decision_ports[] with branches[]`。

### 7.3 過濾條件

- `source_kind` multi-select
- `phase ∈ {proposed, committed, rejected}`
- `outcome ∈ {matched, rejected, skipped, emitted}`
- `decision_port_id` filter（只看特定判斷端口）
- 時間範圍（start_ts ~ end_ts）
- Actor filter（只看某個 actor 的 observation）

### 7.4 API 設計

```
GET /api/agent/traces/:sessionId?start=<ts>&end=<ts>&source=<kind>&limit=1000
    → { events: [Observation...], actors: [Actor...], totalCount }

GET /api/agent/traces/:sessionId/events/:eventId
    → Observation 完整內容（含 refs 展開）

GET /api/agent/traces/:sessionId/state/:ref
    → state_before_ref / state_after_ref 的內容（可能大）

WS /ws/agent/traces/:sessionId
    → 新 Observation 串流（live tail 用）
```

API 實作歸屬 **PR-6a（backend）**，UI 歸屬 **PR-6b（SPA）**。

---

## 8. Migration Strategy

### 8.1 Phase 1 雙寫過渡（Schema 對齊版）

**關鍵修正 v1**：PR-1b 的 Arbitrator passthrough 一開始就用**新 multi-actor schema** 計算 proposal。比對時把新 schema proposal 向下 project 成舊 schema 結構再比：

```
hook/probe/sweep → 直寫舊 frame（legacy path，維持行為）
                ↘
                  Arbitrator(passthrough) → 新 schema proposal
                                          → project_down_to_old_schema(proposal)
                                          → compare with legacy frame state
                                          → write divergence table
```

這樣 PR-2a 升 frame schema 到 multi-actor 時，compare 已經是新 schema，不需重比。

#### Divergence 落地 Schema

```sql
CREATE TABLE frame_divergences (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    trace_id TEXT NOT NULL,
    event_id TEXT NOT NULL,           -- 對應觸發的 Observation
    observed_generation INTEGER NOT NULL,
    old_state_ref BLOB NOT NULL,      -- JSON: legacy frame state snapshot
    proposal_state_ref BLOB NOT NULL, -- JSON: arbitrator proposal (projected to old schema)
    diff_summary TEXT NOT NULL,       -- 人類可讀 diff（如 "status: idle → waiting"）
    matched INTEGER NOT NULL,         -- 0=divergent, 1=match
    reason_code TEXT,                 -- 若 divergent 的理由標籤
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_divergence_session ON frame_divergences(session_id, created_at);
CREATE INDEX idx_divergence_matched ON frame_divergences(matched);
```

**指標**：`lights_divergence_total{matched,reason}`；Arbitrator passthrough 期間上線看板。

### 8.2 Phase 2 切換單一 writer

**PR-2b + PR-2c（v2 拆分）**：

- **PR-2b**：Arbitrator 從 passthrough 切到 authoritative（開始實際寫 frame）+ 同步 regression test baseline
- **PR-2c**：移除 hook/probe/sweep 的 direct write path + 刪 legacy 測試

保留 feature flag `AGENT_ARB_MODE`；若 regression 嚴重可回 passthrough（見 §8.3）。

### 8.3 AGENT_ARB_MODE 注入與切換

**來源優先**：
1. Env: `AGENT_ARB_MODE=passthrough|authoritative`（啟動時讀取，**不支援 hot reload**）
2. Config: `[agent] arb_mode = "passthrough"|"authoritative"`（`config.toml`，支援 hot reload）
3. 預設：`passthrough`（PR-1b ~ PR-2a 期間）；`authoritative`（PR-2b 起）

**切換語意**：
- Daemon boot 時：env 若有設 → 使用 env 值並鎖定；否則讀取 config 值
- `OnConfigChange` 監聽 `[agent] arb_mode` 變化（`internal/core/core.go:117` 現有 hot reload 機制）
  - **若 env 已設**：忽略 config 變化，僅 log warning `arb_mode overridden by env, hot reload ignored`
  - **若 env 未設**：更新 `pendingMode`
- 切換時**不立即生效**；記錄 `pendingMode`，在下一次 `SessionStart` 時套用（避免 mid-session 切換導致 frame 狀態撕裂）
- API 可讀當前 mode + pending mode：`GET /api/agent/arbitrator/mode → { current, pending, env_locked }`
  - `env_locked=true` 時 SPA 顯示「mode 由 env 鎖定，hot reload 已停用」

### 8.4 Phase 3 抽象層重構

**PR-3a**：把 provider 包進 `AgentSpec`，`AgentSpec.Provider()` 轉呼既有 provider；同時加 Optional services contract（`Operator() / Statusline() / ...`）。舊代碼無感。

**PR-3b**：`ProbePolicy` + Scheduler 上線。舊 probe 路徑保留一段時間，新 probe binding 優先。

### 8.5 回退路徑

| 階段 | 回退策略 |
|---|---|
| PR-1a trace schema | 新欄位與舊欄位雙寫，SPA 讀 fallback；若 SPA 依新欄位 crash → revert schema |
| PR-1b Observation bus | Arbitrator passthrough 不寫 frame，關掉 bus 不影響 |
| PR-2a Frame schema 升級 | `agent_frames` 表 DDL 加 `generation / actors` JSON 欄位時保留舊 `status` / `subagents` 欄位；舊 binary 讀新行透過 view / fallback projection。revert binary = 直接 rollback；revert DDL = 留新欄為 NULL 不刪 |
| PR-2b 切 writer | `AGENT_ARB_MODE=passthrough` 回退；下次 SessionStart 生效 |
| PR-2c 刪 legacy | 需 revert commit（legacy path 已刪）|
| PR-3a AgentSpec | 保留 legacy provider compat adapter（§4.6.1）；切換點少，revert 易 |
| PR-3b ProbePolicy | 新 probe path 可關閉，legacy probe 仍註冊 |

### 8.6 Daemon Restart 恢復

**問題**：daemon 重啟時 frame 恢復 + generation 連續性 v1 未寫。

**方案**：

1. **Frame 恢復**：
   - Daemon 啟動時 `replayFromDB` 從 `frames` 表重建記憶體 frame store
   - 每個 session 的 `generation` 直接從 DB 讀取（不 +1；restart 不視為新 generation）
   - 若 `frames` 表沒這個 session（rare），下一個 `SessionStart` 以 DB 最大 generation +1 起

2. **Session 重新連結**：
   - 啟動後對所有活著的 pane 發 `SyntheticRestart` trace record（不產生可 apply 的 proposal；phase=proposed, outcome=skipped）
   - Primary actor 的 `Detail["restarted_at"]` 欄位由 replay 時直接寫入 frame，**不透過 Arbitrator proposal**
   - 若 30s 內收到 hook → 正常流程接手；若沒收到 → reconcile 只記 trace（§3.4.5 哲學：不主動改 status）

3. **Trace 恢復**（`generation ≠ trace_id` 的 UI 標記）：
   - `trace_id` 不跨 restart 續用（daemon memory span IDs 不延續）
   - 每次 restart 為 active session 開新 `trace_id`；舊 trace 保留但不再寫入
   - DB schema 記錄 `startup_id`（UUID）欄位區分哪個 daemon lifetime 寫的
   - Generation **不因 restart bump**（generation 由 `SessionStart` 專屬推進，§3.4.1）；所以同一 session 可能多個 `trace_id`（restart N 次）但仍在同一 generation 下
   - **UI 標記**：SPA trace viewer 顯示「此 session 期間 daemon 重啟 N 次」加 `startup_id` 切換標記（每個 startup_id 顏色區分）
   - 調查時需同時看 `generation`（hook lifetime）與 `startup_id`（daemon lifetime）

4. **Pending 清空**：
   - restart 後 pending buffer 清空；所有 WatcherToken 失效（§3.3）
   - 影響：短暫的 restart 期間 hook / probe callback 可能遺失
   - 接受風險：daemon restart 不常見，alpha 階段不補償（見 §11 `hook_retry_on_startup` 延後 issue）

---

## 9. 測試策略

### 9.1 Pid tree 覆蓋缺口

現況 `handler_test.go:25-49` `newTestModule()` stub 掉：
- `verifyEventFn` → 永遠 accept
- `readProcessInfoFn / processStartTimeFn / isPidAliveFn` → 永遠成功

實際 pid tree race **未真正覆蓋**。

### 9.2 必須補的測試

| 測試 case | 覆蓋 | 位置 |
|---|---|---|
| `proxy_first_then_primary_later` | proxy 的 hook 先到，primary 晚到 → role 不被覆蓋 | `frame_ops_test.go` |
| `agent_shim_proxy_multi_hop` | cc → node → codex → role 仍正確判為 proxy | `verify_test.go` |
| `sender_dies_between_verify_stages` | verify 第一階段成功、第二階段 sender 死 → 落 pending → retry OK | `handler_test.go` |
| `pane_respawn_reattach` | pane pid 變更 → 新 pid 為準 | `verify_test.go` |
| `pid_reuse_same_lstart_second` | lstart 同秒 pid reuse → pending drop | `verify_test.go` |
| `codex_agent_id_future` | Codex 補 `agent_id` 後能分 main/sub | `codex/hooks_test.go` |
| `observation_idempotent` | 同 `(actor_key, source, action, evidence_hash)` 不重複入 frame | `arbitrator_test.go` |
| `monotone_lifecycle` | ended actor 的 late observation 被拒 | `arbitrator_test.go` |
| `probe_error_overrides_hook_waiting` | probe 的 error 可以 override hook 的 waiting | `arbitrator_test.go` |
| `retry_pending_drop_trace_only` | 3 次 retry 全失敗 → drop proposal + 寫 trace，frame 不變 | `arbitrator_test.go` |

### 9.3 新增測試類別

#### Phase 1 雙寫 divergence

| Test | 覆蓋 |
|---|---|
| `passthrough_match_ok` | hook 驅動的 legacy flow 與 arbitrator proposal 一致 → `divergence.matched=1` |
| `known_divergence_counter_increments` | 刻意構造已知差異（如 probe override）→ counter +1 |
| `flag_switch_no_regression` | `AGENT_ARB_MODE=passthrough→authoritative` 切換後 SessionStart 新流程接手；既有 test 全過 |
| `env_lock_ignores_hot_reload` | env 設了 mode 後，config hot reload 被忽略 + log warning |

#### Generation 邊界

| Test | 覆蓋 |
|---|---|
| `session_restart_new_generation` | `SessionStart` hook 後 old gen actor 強制 end；new gen actor 進 frame |
| `future_generation_non_session_start_rejected` | 非 SessionStart 的 future-generation observation → reject (UnauthorizedGenerationBump) |
| `late_probe_rejected_by_gate` | 新 generation 後收到舊 generation probe → reject |
| `stale_watcher_rejected` | probe watcher_token 不符 → reject |

#### Pending buffer / Drop 語意

| Test | 覆蓋 |
|---|---|
| `hook_storm_50_obs_in_10ms` | drop（只進 trace）+ metric +1 |
| `pending_bounded_coalescing` | 同 ActorKey 第 17 筆觀察合併 → 最舊丟棄 + metric +1 |
| `pending_8_entry_per_session_evict` | 第 9 個 actor 進 pending → 最舊 evict 走 `emitTraceOnly` |
| `pending_timeout_drops_no_actor_created` | pending 2s 超時 → drop proposal + trace，frame 不變（無 actor 建立）|
| `sweep_intersect_pending` | sweep 對仍 pending 的 actor 發 end → 累積到 entry；pending timeout 仍走 drop（非建 actor 再 end）|
| `no_primary_no_actor_frame_empty` | role 判不出且無 primary → frame 保持 empty（純 terminal）|
| `existing_primary_unknown_hook_no_change` | 有 primary 時判不出的 hook → 不改動既有 primary |

#### Channel admission control

| Test | 覆蓋 |
|---|---|
| `arb_in_full_proposed_drops_non_blocking` | Arbitrator in channel 滿，proposed observation non-blocking drop + metric |
| `arb_in_full_committed_blocks_100ms` | committed observation blocking send；超 100ms timeout + emergency log |
| `retry_ch_full_drops` | retryCh 滿 → drop retry tick + metric（pending entry 仍在） |

#### Daemon restart

| Test | 覆蓋 |
|---|---|
| `restart_replay_frames` | DB frame 重建 → 記憶體 state 正確 |
| `restart_new_trace_id_same_generation` | 新 `startup_id` + 新 `trace_id`，但 generation 不變 |
| `restart_pending_lost_ok` | restart 時 pending buffer 空；所有 watcher token 失效 |
| `restart_no_status_change_from_reconcile` | replay 後 reconcile 不主動改 actor.status（只記 trace） |

#### Trace back-pressure

| Test | 覆蓋 |
|---|---|
| `trace_queue_full_drop_by_priority` | queue 滿時 synthetic proposed 先被 drop；hook committed 保留 |
| `trace_batch_flush_by_size_or_time` | 100 event or 100ms flush 一次 |
| `trace_retention_ttl_cleanup` | 24h 舊 trace 被 reconcile 清掉 |
| `decision_ports_cap_16` | Observation 帶 17 個 DecisionPort → dev panic / prod truncate |

#### UI Drop 規則（SPA）

| Test | 覆蓋 |
|---|---|
| `unknown_proposal_no_frame_update` | daemon 送 drop-trace 事件 → SPA 不變 frame badge |
| `primary_only_one_at_a_time` | 多個 primary candidate → SPA 只呈現被 Arbitrator 選定的那個 |

#### Registry compat

| Test | 覆蓋 |
|---|---|
| `registry_capability_service_mismatch_panics` | `HasOperator=true` 但 `Operator()==nil` → `Register` panic（dev）|
| `registry_legacy_adapter_cc_provider` | PR-3a compat adapter 讓 `stream/module.go:39` 仍能取得 cc operator |
| `registry_noop_probe_policy` | PR-3b 前未補 ProbePolicy 的 agent 走 `NoopProbePolicy` 不 crash |

### 9.4 測試優先級

- **PR-2a 必須附**：pid race 三件（proxy_first / shim_hop / retry_pending）+ generation 邊界三件
- **PR-2b 必須附**：arbitrator 全部 case + pending 壓力
- **PR-2c 必須附**：flag switch 無 regression
- **PR-4c 必須附**：typed subagent round-trip（hook 進 → frame 保留 type → API 回傳 type）

### 9.5 整合測試 scope

- Daemon e2e：`scripts/e2e/lights-flow.sh`（啟 tmux pane → 用假 hook client 模擬 cc 發 hook → 斷言 frame 狀態 + trace event）
- Restart e2e：`scripts/e2e/lights-restart.sh`（daemon kill -9 → 啟動 → 驗 replay）
- 不涵蓋 SPA（SPA 用 vitest unit test 覆蓋燈號顯示對映）

---

## 10. Phase / PR 拆分（v3 調整版）

原則：每 PR 單一關注點、中等尺寸（500-1500 lines），合進 main 後仍可運行。共 **17 PR**（v1 為 13；v2 拆 PR-2b→b/c、PR-6→a/b = 15；v3 拆 PR-5a→5a0/5a1/5a2 = 17）。

| Phase | PR | 範圍 | 尺寸 | 依賴 |
|---|---|---|---|---|
| **0 輕清理** | **PR-0** | close PR #486；刪 `feat/agent-watch-alive`；Codex readiness 加 capability bit（不補邏輯） | 極小 | — |
| **1 Schema + 雙寫過渡** | **PR-1a** | Trace schema 升級（加 `source_kind / action / reason_code / outcome / scenario_key / observed_generation / decision_ports[]` 等一級欄位，hook path 填值）+ `frame_divergences` 表 DDL | 中 | PR-0 |
| | **PR-1b** | Observation type + Arbitrator **passthrough**（hook/probe/sweep 雙寫直寫 frame **並**送 Observation 到 Arbitrator；Arbitrator 以新 multi-actor schema 計算 proposal，下投影到舊 schema 比對，寫 divergence 表）+ Arbitrator channel admission control（§3.5.1）| 中大 | PR-1a |
| **2 Arbitrator 切換** | **PR-2a** | Frame 改 multi-actor + `ActorKey` 複合鍵 + role via pid tree + **ancestor walk** + retry/pending 視窗 + generation gate（限 SessionStart 推進）+ watcher token + idempotency key + `Lifecycle.LastActivity`；**handler 同步/非同步邊界重構**（§5.4.1）| 大 | PR-1b |
| | **PR-2b** | 切換 Arbitrator 為唯一 writer（mode=authoritative）；`AGENT_ARB_MODE` env/config 注入 + hot reload；baseline regression test | 中大 | PR-2a |
| | **PR-2c** | 移除 hook/probe/sweep direct write + 刪 legacy 測試 | 中 | PR-2b |
| **3 抽象層重構** | **PR-3a** | `AgentSpec = Descriptor + Provider + ProbePolicy + Optional services` 拆分 + `Capability` bits 擴張 + `Registry` 一致性驗證 + `RegisterBuiltinAgents` 顯式註冊 + **Legacy Provider Compat Adapter**（§4.6.1）| 中大 | PR-2c |
| | **PR-3b** | `ProbePolicy` + `ProbeBinding(OnDemand/Continuous)` + Scheduler + `common_probes`（motion + 彩虹字）+ self_detection probe（disabled） | 中大 | PR-3a |
| **4 三 agent 對齊** | **PR-4a** | Codex ProbePolicy 實作 + Capability bits 標註 | 中 | PR-3b |
| | **PR-4b** | OpenCode ProbePolicy 實作 + readiness 補齊 + `HasReadiness` 開 true | 中 | PR-3b |
| | **PR-4c** | Subagent typed model（`SubagentRef{id, type}` 升級至 Frame / Projection；OpenCode typed 從 detail 提升） | 中 | PR-2a |
| **5 硬編拆除 + SPA 對齊** | **PR-5a0** | Session metadata 中性化：`CCSessionID` → `ResumeToken`（DB schema + 所有讀寫點改名）+ Trace back-pressure policy 實作（batching/sampling/drop priority） | 中 | PR-3a |
| | **PR-5a1** | Stream orchestrator 改造：硬編 `cc.operator` / `ccOps` 走 `spec.Operator()` / `spec.StreamResumer()` 動態分派 + 移除 compat adapter 中 stream 相關路徑 | 中 | PR-5a0 |
| | **PR-5a2** | API 層 capability 分派：`/api/agent/status`、statusline installer 改走 `spec.Statusline()` / `spec.Descriptor().Capabilities` 判斷 + 移除剩餘 compat adapter | 中 | PR-5a1 |
| | **PR-5b** | SPA 側拆 cc 硬編（icon list / detect list / metadata）+ registry 化；燈號 UI 體質（`clear` 型別、顏色 SOT palette、SessionsSection 4 色對齊、§2.1 投影規則實作；UI drop 規則：收到 trace-only event 不更動 frame badge）| 中大 | PR-5a2 |
| **6 Trace viewer** | **PR-6a** | Trace read API 實作（REST + WS：`/api/agent/traces/:sessionId`、`/api/agent/traces/:sessionId/events/:eventId`、`/api/agent/traces/:sessionId/state/:ref`、WS tail）| 中 | PR-1a |
| | **PR-6b** | SPA trace viewer UI：flow graph + DecisionPort 子節點 + DAP-style inspector + filter（source_kind/phase/outcome/decision_port_id）+ time-range + `startup_id` 著色切換標記（§8.6）| 大（純 SPA） | PR-6a |

### 10.1 可 parallel 的 PR

| PR 組 | 共享點 | coordination 需求 |
|---|---|---|
| PR-4a + PR-4b | `internal/agent/<agent>/` 各自獨立檔 | 無硬衝突；測試 fixtures（如 `internal/agent/registry_test.go`）可能 rebase |
| PR-4c + PR-4a/4b | `Frame.Actors` schema 與各 agent provider | PR-4c 先合（schema 改動在先），PR-4a/4b rebase |
| PR-6b + Phase 2-5 | 純 SPA，依賴 PR-6a | 無硬衝突 |
| PR-3a + PR-3b | `AgentSpec` interface vs `ProbePolicy` impl | 必須串行（3a 先）|
| PR-5a0/5a1/5a2 + PR-5b | daemon metadata/orchestrator/API 分層；SPA consumer | 必須串行（5a0→5a1→5a2→5b）|

### 10.2 關鍵設計點

| 決定 | 理由 |
|---|---|
| Phase 1 雙寫不改行為 + 一開始用新 schema 算 proposal | 降風險；PR-2a 升 frame schema 時 compare 不需重比 |
| PR-2b / 2c 拆分 | 切 writer 與刪 legacy 分兩次降風險；中間可 revert 2c |
| `AGENT_ARB_MODE` hot reload 延到下個 SessionStart 才生效 | 避免 mid-session 切換撕裂 frame |
| 舊 `AgentProvider` backward-compat 一段時間 | PR-3a 後仍可跑，PR-4 逐步遷移 |
| PR-5a 先 backend 再 SPA | 避免 SPA 依靠 registry 但 backend 還硬編 |
| PR-6 拆 API/UI | API 跟 trace schema 綁，可在 Phase 2-5 並行；UI 等資料穩定再上 |

### 10.3 風險點

| PR | 風險 | 緩解 |
|---|---|---|
| PR-2a | Handler 同步/非同步邊界重構 + frame schema 升級同 PR；尺寸易膨脹 | 先在 plan 層切 sub-diff 審查（verify → observation → arbitrator）；compat adapter 讓舊 code path 暫存活 |
| PR-2b | 所有 frame 寫入路徑改動，regression 面積大 | PR-1b 雙寫留下 divergence log 可比對；配合完整 regression test；flag 可回退 |
| PR-2c | legacy path 刪除不可逆 | 需 revert commit；時機放 PR-2b 穩定運行 1 alpha 週期後 |
| PR-3a | 既有 hard-coded `cc` 呼叫端（`stream/module.go:39` / `handler.go:311/381/539`）若無 compat adapter 會 nil panic | **Legacy Provider Compat Adapter**（§4.6.1）作為過渡；Registry 一致性檢查會 panic 早期發現配置錯誤 |
| PR-4c | Schema 三層同步，migration 風險 | Alpha 階段可接受重置（memory: `feedback_no_alpha_migration`） |
| PR-5a0/5a1/5a2 | 涉及 `stream` / `session` / `agent` 三 module | 拆三個子 PR，每步都有 compat adapter 保護；e2e 串流測試覆蓋 |

### 10.4 時序指引

- PR-0 → alpha bump 一次（起點 alpha.199）
- Phase 1（PR-1a + PR-1b）→ 至少一個 alpha bump 週期觀察 divergence log
- Phase 2（PR-2a + PR-2b + PR-2c）→ PR-2b 上線觀察一週後再送 PR-2c（regression window）
- Phase 3 以後視團隊容量 / 使用者回饋節奏

---

## 11. 開放問題 / 追蹤 issue

| 主題 | 狀態 | 追蹤 |
|---|---|---|
| Codex hook 缺 `agent_id / agent_type` | 上游 OpenAI codex issue #16226 open；Purdex 靠 pid tree 繞過 | 追蹤 issue 補齊後可開啟 codex `CanSubagent` |
| Sweep → Observation 整合 | PR-1b 完成 | — |
| `common.rainbow_text` 偵測規則 | PR-3b 規劃時定；可能需要 per-agent 彩虹字 palette | 延後 spike |
| Trace snapshot 歷史保留策略 | alpha 階段 24h TTL（§3.5.1）；GA 前再議（可能需要 per-user opt-in 保留）| — |
| Trace DB schema（SQLite）增量升級 | PR-1a 決定 schema 初版；alpha 階段可以 breaking drop | — |
| `AGENT_ARB_MODE` 全域 console 介面 | GA 前考慮在 Settings → Dev 頁加 mode 切換 | — |
| Restart recovery pending loss 補償 | alpha 不補；GA 前視需要加 `hook_retry_on_startup` | — |
| **Role Unknown 補檢查 probe** | v3 選「drop + trace」哲學；未來可加一個 on-demand probe（`role_rediscover`）對該 pane 重新 scan pid tree；現階段不實作（不掛空殼，留本條追蹤） | future |
| **DecisionPort 過量體積** | 已加 per-observation cap 16（§3.3）；若日後發現 hook/probe committed 普遍觸頂，可能需要 sampling per port | future |
| Handler 同步/非同步邊界具體 diff | Spec §5.4.1 只定介面；PR-2a plan 層決定 `handleEvent()` 拆法、trace path 調整、test stub 重寫 | plan 層 |

---

## 12. 附錄：主流架構對照參考

### 12.1 Status 仲裁（Codex Job 1）

| 架構 | 關鍵做法 | Purdex 對應 |
|---|---|---|
| K8s Probes | probe 失敗不覆寫 Pod status；結果進 Events + Conditions | probe 只產 Observation，不直寫 frame |
| K8s Controller | event 叫醒；`RequeueAfter` 混合 event-driven + periodic reconcile；`observedGeneration` | 取代 epoch / watch_generation |
| Temporal Activity + Heartbeat | workflow event history 是主軌；heartbeat 是輔助，超時由 server 轉成 history event 才影響 state | hook = history；probe = heartbeat；超時→產 Observation 交 Arbitrator |
| State Machine (XState / Akka / MassTransit) | probe 作為 event/timer，由 FSM 仲裁；不繞過 machine 改 context | Arbitrator 如同單一 FSM |

**推薦方向**：`Canonical hook log + Condition-based probe evidence + Single arbitrator`

### 12.2 Trace Schema（Codex Job 2）

**總結**：OTel span envelope + ECS event vocabulary + DAP 互動模型 + DecisionPort 的混合

- **OTel**：span envelope（`trace_id / span_id / parent / kind / status / attributes / events / links`）；`status` 只表執行成功/失敗
- **ECS**：`event.action / event.reason`；`source_kind / action / reason_code / reason_text` 拆分
- **DAP**：到判斷點 → `stopped` event → client 按需拉 `scopes / variables`；UI 節點摘要先顯示 `reason / selected branch / key I/O`，細節 lazy-load
- **DecisionPort**（v2 新增，對應目標 #4）：每個判斷端口的 `input / branches / selected / reason`，UI 作為流程圖節點；落到 Observation 內的 `decision_ports[]` 欄位

### 12.3 Multi-Provider 抽象（Codex Job 3）

| 架構 | 啟發 |
|---|---|
| LSP capability negotiation | `ClientCapabilities + ServerCapabilities`；缺席 = unsupported；graceful degrade |
| DAP adapter pattern | generic host + adapter-specific bridge；共用 core 處理 session / UI / protocol |
| VSCode contribution points | declarative descriptor 宣告能力；host lookup + lazy dispatch |
| Terraform provider / CSI driver | core 協議標準化 + provider impl + optional capability interfaces |
| Go 實踐 | compile-time built-in + explicit registry；避免 `init()` 與 `plugin` 可攜性問題 |

### 12.4 Codex 調查來源

**前輪現況盤點（2026-04-21）**

| Job ID | Duration | 範圍 |
|---|---|---|
| `task-mo8p3czc-ugnc39` | 3m10s | A — main Probe Chain 主線 |
| `task-mo8p4108-ufdfd0` | 2m32s | B — watchAlive 分支 5 race |
| `task-mo8p4hqi-fd2y2u` | 6m32s | C — SPA 燈號顯示 |

**目標對齊分析（2026-04-21）**

| Job ID | Duration | 範圍 |
|---|---|---|
| `task-mo8qekrw-hjj2lh` | 5m23s | D — Trace 基礎設施 |
| `task-mo8qf0qe-9tkaso` | 7m01s | E — Hook/probe 主從邊界 |
| `task-mo8qfg1q-7ye5eo` | 7m18s | F — 三 agent 對稱 + subagent |

**主流架構研究（2026-04-21 / 22）**

| Job ID | Duration | 範圍 |
|---|---|---|
| `task-mo8t2bim-893o8c` | 5m52s | Status 仲裁（K8s / Temporal / FSM） |
| `task-mo8t2q20-tqc7r3` | 4m44s | Trace schema（OTel / ECS / DAP / Jaeger / event sourcing） |
| `task-mo8t33pb-zout11` | 4m23s | Multi-provider（LSP / DAP / VSCode / Terraform / CSI） |

**Pid tree 驗證（2026-04-22）**

| Job ID | Duration | 範圍 |
|---|---|---|
| `task-mo8un5f6-p3vuib` | 7m33s | 現有 pid tree 能力 + spawn 模式 + race + 測試覆蓋 + 可靠性評估 |

**Spec v1 審閱（2026-04-22）**

| Job ID | Duration | 範圍 |
|---|---|---|
| `task-mo8w4hyl-ys0v5e` | 5m30s | Spec v1 完整審閱：8 維度 20 項 blocker，v2 已全數吃進 |

**Spec v2 審閱（2026-04-22）**

| Job ID | Duration | 範圍 |
|---|---|---|
| `task-mo8xbpd3-anz4np` | 6m09s | Spec v2 收斂審閱：10 完全修 + 8 部分修（5 真 blocker）+ 4 新 Findings + 3 新風險 / 代碼整合 blocker；本 v3 吃進核心 4 blocker + 6 細節 blocker + 7 follow-up |

---

**End of Spec**
