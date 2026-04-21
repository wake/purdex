# 燈號系統 — 統一設計 Design Spec

**Date**: 2026-04-22
**Status**: Design approved, ready for plan
**Inputs**:
- `docs/research/2026-04-21-lights-system-alignment.md`（現況分析 + 目標落差）
- 10 份 codex web research 調查（架構主流對照 + pid tree 驗證）
- memory kickoff：`kickoff_lights_spec.md`

## 1. 背景與目標

### 1.1 現況

Purdex daemon 已有燈號骨架（hook provider chain + probe 三類 + sweep 掃描 + frame store），但抽象層與主從規則屬**隱含**。`docs/research/2026-04-21-lights-system-alignment.md` 對 5 點使用者目標評分 🟡 **2/5**：

| 目標 | 分數 | 主要缺口 |
|---|---|---|
| 1. 傳遞過程可監控 | 🔴 2/5 | trace schema 只涵蓋 `hook_post`；probe / sweep / handoff 不寫 trace |
| 2. Hook 主、Probe 輔 | 🟡 3/5 | 主從關係隱含；activity watcher 可能覆寫非 error 的 hook 狀態 |
| 3. Subagent type | 🔴 1/5 | `[]string` 只存 id 不存 type；OpenCode hook 帶 `agent_type` 但被 projection 丟棄 |
| 4. Flow graph | 🟡 2/5 | chain + step 有欄位，但 UI 是 JSON inspector 不是流程圖 |
| 5. 三 agent 對稱 | 🔴 2/5 | cc 完整 / opencode 缺 readiness+operator+typed subagent / codex 多態缺席 |

### 1.2 使用者 5 點目標

1. **燈號傳遞過程可被監控**（先 daemon 側；SPA 只是輔助觀測用，不監控內部狀態傳遞）
2. **架構**：主要 agent type + 多種/多個 subagent + subagent type
3. **Agent 原生 hook 事件為主要依歸，probe 為輔助修正判斷**
4. 傳遞 by session + event（hook / probe 兩種）紀錄，**每次 event 用流程圖呈現每一個判斷端口的 input-reason-output**
5. 支援三種 agent：**cc / codex / opencode**

### 1.3 本 spec 解決的問題

- 把隱含的主從規則升級成 **Arbitrator 單寫者** 架構（hook/probe/sweep 全走 Observation → Arbitrator）
- Trace schema 從 `hook_post` 專用擴成 **通用 event envelope**（OTel + ECS + DAP 混合）
- Frame 從「single primary + `[]string` subagents」升級成 **multi-actor model**（primary / proxy / subagent，type-aware）
- 三 agent 差異用 **Capability bits** 表達，不追求齊頭式對稱
- 補強 pid tree role 判斷（ancestor walk + retry + pending 視窗）解決 shim-hop 錯判

---

## 2. 術語

### 2.1 兩種燈號

| 燈號 | 屬於 | 狀態空間 |
|---|---|---|
| **主燈號**（primary light） | primary actor（有獨立 hook 流） | `running / waiting / idle / error / clear` |
| **subagent 燈號** | subagent 或 proxy actor | `active / inactive`（啟動/結束兩態） |

一個 session frame 同時可持有 1 個主燈號 + N 個 subagent 燈號。UI 層投影時主燈號決定主色，subagent 燈號顯示為次要指示（dot / badge）。

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

**重要**：subagent/proxy 對稱於 frame 結構（都是 Actor），只是 role 不同 + 狀態空間不同。

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
| Signal    |      | Collector |      |     Bus        |      | trator|      | Store |
+-----------+      +-----------+      |                |      |       |      |       |
                                      |                |      |       |      |       |
+-----------+      +-----------+      |                |      |       |      |       |
| Sweep     | ---> | Sweep     | ---> |                |      |       |      |       |
| Tick      |      | Collector |      +----------------+      +-------+      +-------+
+-----------+      +-----------+              |                   |             |
                                              v                   v             v
                                         Trace Writer        Reconciler     Broadcast
                                         (所有 obs)        (delta computed)  (SPA subs)
```

**三條原則**：

1. **Observation 單寫者**：hook/probe/sweep 都**不**直寫 frame，只產 Observation 進 bus
2. **Arbitrator 唯一仲裁**：從 Observation bus 消費 + 依 condition 組合 → 寫 frame
3. **所有 Observation 進 trace**：trace 不只是 hook 的 debugging 管道，是跨 source 的統一 event ledger

### 3.2 Frame Multi-Actor Model

```go
type SessionFrame struct {
    SessionID         string
    TmuxPaneID        string
    Actors            []Actor               // 主 + proxy + subagents
    HookTsMicro       int64
    ObservedGeneration int64                // K8s 思路：已觀測到的 frame 版本
    Scenario          string                // 如 current_event_name
    UpdatedAt         time.Time
}

type Actor struct {
    ID                string                // hook provided id (session_id / agent_id)
    AgentType         string                // cc / codex / opencode / general-purpose (subagent)
    Role              ActorRole             // primary | proxy | subagent
    Status            string                // primary/proxy: running/waiting/idle/error/clear
                                            // subagent: active/inactive
    PID               int                   // subagent 時 = parent pid（無獨立 process）
    PidAncestry       []int                 // 從 pane root 往下的 pid 鏈
    Lifecycle         Lifecycle             // started_at / ended_at / ended_reason
    ObservedGeneration int64                // 本 actor 最後觀察到的 generation
    Detail            map[string]any        // agent-specific 資料層（UI 不看，debug / module 看）
}

type ActorRole string
const (
    RolePrimary  ActorRole = "primary"
    RoleProxy    ActorRole = "proxy"
    RoleSubagent ActorRole = "subagent"
)

type Lifecycle struct {
    StartedAt   time.Time
    EndedAt     *time.Time
    EndedReason string   // hook_stop / process_exit / probe_timeout / ...
}
```

**資料層 vs 顯示層分離**：
- `Actor.Detail` 保存 agent-specific 完整資料（例：codex reasoning tokens、cc permission scope），供 debug 與未來 module 引用
- UI 層只讀 `Status`，投影成主色 / subagent dot

### 3.3 Observation 介面

```go
type Observation struct {
    TraceID       string
    SpanID        string
    ParentSpanID  string
    SessionID     string
    SourceKind    SourceKind          // hook | probe | sweep | reconcile | synthetic
    Action        string              // 做什麼（event.action）
    Phase         ObsPhase            // proposed | committed | rejected
    Proposal      StateProposal       // 對哪個 actor 提議什麼狀態變化
    ReasonCode    string              // 低基數 CamelCase，如 ProcessExitDetected
    ReasonText    string              // 人類可讀理由
    Evidence      []EvidenceRef       // 如 pid / screen_hash / hook_payload_ref
    ObservedAt    time.Time
    Seq           int64
}

type StateProposal struct {
    ActorID        string
    SuggestStatus  string              // 建議 status；Arbitrator 可採納/拒絕
    EndLifecycle   bool                // subagent/proxy 結束
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
```

### 3.4 Arbitrator 仲裁規則

Arbitrator 是**單寫者** + **condition-based reducer**，不是 FSM。核心原則：

1. **Source priority**（同 actor 的 proposal 衝突時）：
   - `hook` > `probe` > `sweep` > `synthetic`
   - 但有 override clause：**probe 的 `error` 可以 override hook 的 `waiting`**（實務上 hook error 信號稀少，probe 看到崩潰需要能改）

2. **Monotone lifecycle**：
   - 一旦 `EndedAt` 設定，之後同 actor 的 proposal 全部拒（除非收到 `SessionStart` 新 generation）
   - 防止 late probe/sweep 覆寫已結束的 actor

3. **Pending window**（pid tree 不確定時）：
   - 前 3 次 retry 內 `role` 為 `unknown` 時，actor 存在 pending buffer，不 emit 給 broadcast
   - Retry 成功 → flush 到主 frame；timeout → drop + 記 `reason_code = PidTreeUnresolved`

4. **Observation idempotency**：
   - Seq 與 `(source_kind, action, evidence_hash)` 重複的 observation 直接丟掉（防 sweep 重掃重報）

5. **Reconcile loop**（每 5s 一次）：
   - 掃所有 actor：若 `lifecycle.ended_at == nil ∧ last_observation > staleThreshold(30s)` → emit `SyntheticStale` observation
   - 對應 K8s controller 的 `RequeueAfter` 模式

### 3.5 Trace Envelope（OTel + ECS + DAP 混合）

所有 Observation 都落 trace。一級欄位：

```
trace_id          — per session per generation
session_id        — 對應 frame
event_id          — globally unique
span_id / parent_span_id
name              — 如 "hook.post.SessionStart"
kind              — internal | server | client (OTel)
source_kind       — hook | probe | sweep | reconcile | synthetic
phase             — proposed | committed | rejected
status            — success | failure (OTel: 執行成功/失敗)
outcome           — matched | rejected | skipped | emitted (ECS 語意)
action            — 做什麼
reason_code       — CamelCase 低基數
reason_text       — 人類可讀
scenario_key      — 當前的 event 名或狀態組合
input_refs        — 引用輸入資料的 ref（payload 大時用 ref）
output_refs       — 引用輸出資料的 ref
state_before_ref  — actor 狀態 before
state_after_ref   — actor 狀態 after
evidence_refs     — 如 pid / screen_hash / hook_payload_ref
attrs             — 額外 flat attribute map
started_at / ended_at / seq
observed_generation
```

**design 對照**：
- `status` 單純表「這次執行成功/失敗」（OTel 語意）
- `outcome` 表「分支結果是否落實」（ECS 語意）。例如 probe 觀測成功但 Arbitrator 拒絕 → `status=success, outcome=rejected`
- `reason_code` 低基數適合 metrics 聚合；`reason_text` 人類可讀（debug 流程圖節點 tooltip）

### 3.6 Capability Bits

不追求齊頭對稱。每個 agent 聲明自己的 capability：

| Capability | 含義 | cc | codex | opencode |
|---|---|---|---|---|
| `CanWait` | 支援 `waiting` 狀態 hook | ✅ | ❌ | ✅ |
| `CanError` | 支援 `error` 狀態 hook | ✅ | ❌ | ✅ |
| `CanClear` | 支援 `SessionEnd / clear` hook | ✅ | ❌ | ✅ |
| `CanPermissionRequest` | `PermissionRequest` hook | ✅ | ❌ | ✅ |
| `CanSubagent` | `SubagentStart/Stop` hook | ✅ | ❌（issue #16226）| ✅ |
| `HasOperator` | `Interrupt / Exit` 外部控制 | ✅ | ❌ | ❌ |
| `HasStatusline` | statusline wrapper | ✅ | ❌ | ❌ |

Core 使用 capability 決定：
- UI 是否顯示特定狀態（`CanWait=false` → 黃燈永不出現）
- Rescue 規則是否啟用（`CanError=false` → 強依賴 probe 補 error）
- Handoff / Operator / Statusline 等功能是否開放

---

## 4. 抽象層

### 4.1 AgentSpec 三層分離

```go
type AgentSpec interface {
    Descriptor() AgentDescriptor    // ID / DisplayName / Icon / DetectHints / Capabilities
    Provider() AgentProvider        // hook 側（已有 DeriveStatus）
    ProbePolicy() ProbePolicy       // probe 側（新增）
}

type AgentDescriptor struct {
    ID           string
    DisplayName  string
    Icon         string              // phosphor icon name
    DetectHints  []DetectHint        // pane content / cmdline 偵測 hint
    Capabilities CapabilitySet       // CanWait / CanError / ...
}

type CapabilitySet struct {
    CanWait              bool
    CanError             bool
    CanClear             bool
    CanPermissionRequest bool
    CanSubagent          bool
    HasOperator          bool
    HasStatusline        bool
}

type AgentProvider interface {
    // 既有：消化 hook event → NormalizedEvent
    Identify(ctx context.Context, h ProcessHint) (bool, error)
    NormalizeHook(raw HookPayload) (NormalizedEvent, error)
    DeriveStatus(prev Actor, ev NormalizedEvent) StateProposal
}
```

### 4.2 ProbePolicy

```go
type ProbePolicy interface {
    // 明確宣告本 agent 能觸發哪些 probe
    ProbeCatalog() []ProbeDefinition

    // 情境式：hook 發生時動態 arm 哪些 probe
    PlanProbesForHook(h HookKind) []ProbeBinding

    // 常駐式：module 啟動時永久註冊的 binding
    // （目前全 disabled，架構保留）
    ContinuousProbes() []ProbeBinding

    // probe 拿到 signal → 轉譯成 Observation
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
    - probe returns definitive signal → cancel + emit Observation
    - timeout 觸及 → cancel + emit SyntheticTimeout Observation
    - 對應 actor ended → cancel (no observation)
```

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

func NewDefaultRegistry() *Registry {
    r := &Registry{specs: map[string]AgentSpec{}}
    RegisterBuiltinAgents(r)
    return r
}
```

好處：
- 容易單元測試（可註冊假的 spec）
- 沒有隱藏的初始化順序依賴
- 外部擴充點明確（日後若要支援 plugin agent，可在此加 `RegisterExternal`）

### 4.6 Backward compatibility

**舊 `AgentProvider` 介面不破壞**：

- PR-3a 引入 `AgentSpec` 包裝層，`Spec.Provider()` 轉呼既有 provider
- 舊的 hook path 仍透過 provider 走（到 PR-3b 才從 hook collector 改寫走 observation bus）
- PR-4a/4b 逐個 agent 補 `ProbePolicy`，未補的 agent 使用 `NoopProbePolicy`

---

## 5. Pid Tree Role 判斷

### 5.1 Pid tree 驗證結論

**可靠性評級：🟡 黃燈**（需要補強才到綠）

- 「驗 pane-locality」**夠硬**（`PidAncestorIncludes` 可靠判斷 hook 是否屬於 pane）
- 「穩定分 primary/proxy」**還差兩塊**：
  1. Parent attribution 要用 **ancestor walk**（修 `cc → node shim → codex` 這類多跳錯判）
  2. **Retry + pending 視窗**（降低 3-10% hook 落 unknown 的比例）

### 5.2 嚴格策略（D1=C）

判不出就**不認**，顯示 `unknown`（UI 過渡態）而不是猜。

配合：
- **Ancestor walk** 修 shim-hop（下節）
- **Retry + pending 視窗** 降低 unknown 比例（5.4 節）

### 5.3 Ancestor Walk

**問題**：現況 `frame_ops.go:113-119` 只看直接 PPID：
```go
parent, err := m.frames.FindByPanePID(req.TmuxPaneID, info.PPID)
```
`cc → node shim → codex` 這種多跳 wrapper 會找不到 parent frame → proxy 被當 orphan。

**修正**：從 hook pid 往上 walk，找到最近一個**已 verified**的 actor 作為 parent：

```go
func (m *Module) findParentActor(pane string, hookPID int) (*Actor, error) {
    // 拿一次 process tree snapshot
    ancestors, err := liveness.WalkAncestors(hookPID)  // [self, parent, grandparent, ...]
    if err != nil {
        return nil, err
    }

    // 從最近的祖先往上找，跳過 shim（用 cmdline hint 辨識）
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
- `node` / `node22` / `node24`（多數 CLI 的 launcher）
- `bash -c` / `sh -c` / `zsh -c` wrapper
- `env` wrapper

由 `CC_HOOK_DEBUG=1` 環境變數可觀察 shim-hop trace，後續可擴充 shim 清單。

### 5.4 Retry + Pending Window

**問題**：hook 到達時若遇到：
- `pane_unresolvable`（tmux 查 pane 暫失敗）
- `pid_not_in_pane_tree`（process snapshot 漏 short-lived）
- `start_time_unavailable`（ps 系統呼叫瞬斷）

現況直接落 `unknown`，約 3-10% hook 受影響。

**修正**：

```go
const (
    RetryDelay1 = 100 * time.Millisecond
    RetryDelay2 = 250 * time.Millisecond
    RetryDelay3 = 500 * time.Millisecond
)

func (m *Module) verifyWithRetry(ev hookEvent) verifyResult {
    for i, delay := range []time.Duration{0, RetryDelay1, RetryDelay2, RetryDelay3} {
        if i > 0 { time.Sleep(delay) }

        res := m.verify.Run(ev)
        if res.Status == Verified {
            return res
        }
        if !res.IsRetryable() {
            return res  // 永久失敗（pane 真不在、pid 真死）就別 retry
        }
    }
    // 三次 retry 都沒成功 → 進 pending buffer
    return verifyResult{Status: Pending, Reason: "retry_exhausted"}
}
```

**Pending buffer**：
- 放 2s 超時視窗
- 期間內若收到同 actor 的後續 event 一併累積
- 2s 超時 → flush 一次確認 → 仍 pending 則降級為 `unknown` actor，Lifecycle 開啟但 Status = `unknown`
- Arbitrator 明確處理 `unknown`（UI 顯示灰燈）

### 5.5 Role 判斷表

| 情境 | pane pid 驗證 | 直接 PPID 命中 actor | ancestor walk 命中 actor | role |
|---|---|---|---|---|
| hook 來自 pane 內且是 session root process | ✅ | N/A | N/A | `primary` |
| hook 來自 pane 內、PPID 是另一 actor process | ✅ | ✅ | ✅ | `proxy`（parent = 那 actor） |
| hook 來自 pane 內、PPID 是 shim、祖父是 actor | ✅ | ❌ | ✅ | `proxy`（parent = 祖父 actor） |
| hook 來自 pane 內、無任何祖先是 actor | ✅ | ❌ | ❌ | `primary`（可能是新 agent 取代 pane） |
| hook 不在 pane tree | ❌ | N/A | N/A | `unknown`（drop 或 pending） |

**注意**：subagent 不由 pid tree 判，由 parent 的 `SubagentStart` hook detail 宣告。

### 5.6 Subagent 歸屬

當 parent 的 `SubagentStart` hook 觸發時，parent provider 產 Observation：

```go
Observation{
    SourceKind: SourceHook,
    Action:     "subagent.start",
    Proposal: StateProposal{
        ActorID:       "<subagent_id>",      // hook detail 的 agent_id
        SuggestStatus: "active",
    },
    Evidence: []EvidenceRef{
        {Key: "parent_actor_id", Value: parentActorID},
        {Key: "subagent_type",   Value: hookDetail["agent_type"]},
    },
}
```

Arbitrator 在 apply 時：
1. 讀 `parent_actor_id` 找到 parent actor（必須已 verified）
2. 新增 `Actor{Role: RoleSubagent, AgentType: hookDetail["agent_type"], PID: parent.PID}`
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

### 6.2 Capability 表達

不齊頭式對稱。每 agent 聲明 capability，缺席的 capability → core 降級：

| Capability | cc | codex | opencode |
|---|---|---|---|
| `CanWait` | ✅ | ❌ | ✅ |
| `CanError` | ✅ | ❌ | ✅ |
| `CanClear` | ✅ | ❌ | ✅ |
| `CanPermissionRequest` | ✅ | ❌ | ✅ |
| `CanSubagent` | ✅ | ❌ | ✅ |
| `HasOperator` | ✅ | ❌ | ❌ |
| `HasStatusline` | ✅ | ❌ | ❌ |

### 6.3 OpenCode 提升

- 實作 `ReadinessChecker`（平行於 cc 的 `Allow+Deny → waiting`）
- `ProbePolicy` 補齊 `common.motion` binding
- Subagent typed：plugin 已在 hook 帶 `agent_type`，只需 core 在 frame schema 保留即可（PR-4c）

### 6.4 Codex 提升

- **保持 capability=false**（不補 waiting/error/clear），UI 降級呈現
- `ProbePolicy` 補齊 `common.motion`（補 idle→running 邊界偵測）
- Readiness stub 加 capability bit 明示 `stub`（PR-0 已做）

### 6.5 三 agent self_detection 常駐 probe（disabled）

| Agent | Probe ID | 行為 | 預設 |
|---|---|---|---|
| cc | `cc.self_detection` | `PidAncestorIncludes` + identify 確認 pane 內有 cc process | disabled |
| codex | `codex.self_detection` | 同上 for codex | disabled |
| opencode | `opencode.self_detection` | 同上 for opencode | disabled |

未來若 `watchAlive` 形式的常駐監測確需啟用，啟動單一 probe 而非重寫架構。

---

## 7. Trace Viewer UI

（屬 SPA，Phase 6 實作）

### 7.1 設計目標

- Session 時間軸：主視覺是 **event 流程圖**（節點 = Observation / 轉接 = actor frame 變化）
- 每節點顯示 `source_kind / reason_code / status:outcome` 摘要
- 點擊節點展開 DAP-style inspector：原始 payload、state before/after、evidence refs

### 7.2 三層顯示

1. **Timeline bar**（水平）
   顯示 session 的 event 時間軸，顏色 = `source_kind`（hook 藍 / probe 綠 / sweep 橘 / synthetic 灰）

2. **Flow graph**（中央）
   actor 為 lane（swim-lane 佈局），Observation 為節點。連線表示 parent_span → child_span 關係。
   每節點小卡片：`name / source_kind / status:outcome / reason_code`。

3. **Inspector**（右側，lazy-load）
   展開一個節點時才查 detail API：`input_refs / output_refs / state_before_ref / state_after_ref / evidence_refs / attrs`。

### 7.3 過濾條件

- `source_kind` multi-select
- `phase ∈ {proposed, committed, rejected}`
- `outcome ∈ {matched, rejected, skipped, emitted}`
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

---

## 8. Migration Strategy

### 8.1 Phase 1 雙寫過渡

**PR-1b 引入**：hook/probe/sweep **同時直寫 frame**（維持舊行為）**並**送 Observation 到 Arbitrator（新 path passthrough）。

Arbitrator 在 passthrough 模式：
- 消費 Observation
- 不實際寫 frame
- 把「若切換後會寫什麼」記到 trace（`phase=proposed`）

**比對方式**：觀察 trace 中 `(old frame state, arbitrator proposal)` 是否一致。若出現發散（`divergence` counter），表示 Arbitrator 規則不對，需補上 clause 才能切換。

雙寫期間至少一個 alpha bump 週期（約 1 週），讓 log 有足夠樣本。

### 8.2 Phase 2 切換單一 writer

**PR-2b**：
1. Arbitrator 從 passthrough 切到 authoritative（開始實際寫 frame）
2. 移除 hook/probe/sweep 的 direct write path
3. 保留 feature flag `AGENT_ARB_MODE = authoritative | passthrough`；若 regression 嚴重可回 passthrough

### 8.3 Phase 3 抽象層重構

**PR-3a**：把 provider 包進 `AgentSpec`，`AgentSpec.Provider()` 轉呼既有 provider。舊代碼無感。

**PR-3b**：`ProbePolicy` + Scheduler 上線。舊 probe 路徑保留一段時間，新 probe binding 優先。

### 8.4 回退路徑

| 階段 | 回退策略 |
|---|---|
| PR-1a trace schema | 新欄位與舊欄位雙寫，SPA 讀 fallback；若 SPA 依新欄位 crash → revert schema |
| PR-1b Observation bus | Arbitrator passthrough 不寫 frame，關掉 bus 不影響 |
| PR-2b 切 writer | feature flag 回 passthrough；同步 revert commit |
| PR-3a AgentSpec | 保留 legacy provider 介面；切換點少，revert 易 |
| PR-3b ProbePolicy | 新 probe path 可關閉，legacy probe 仍註冊 |

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
| `observation_idempotent` | 同 `(source, action, evidence_hash)` 不重複入 frame | `arbitrator_test.go` |
| `monotone_lifecycle` | ended actor 的 late observation 被拒 | `arbitrator_test.go` |
| `probe_error_overrides_hook_waiting` | probe 的 error 可以 override hook 的 waiting | `arbitrator_test.go` |
| `retry_pending_unknown_flush` | 3 次 retry 全失敗 → unknown actor | `handler_test.go` |

### 9.3 測試優先級

- **PR-2a 必須附**：pid race 三件（proxy_first / shim_hop / retry_pending）
- **PR-2b 必須附**：arbitrator 全部 case
- **PR-4c 必須附**：typed subagent round-trip（hook 進 → frame 保留 type → API 回傳 type）

### 9.4 整合測試 scope

- Daemon e2e：`scripts/e2e/lights-flow.sh`（啟 tmux pane → 用假 hook client 模擬 cc 發 hook → 斷言 frame 狀態 + trace event）
- 不涵蓋 SPA（SPA 用 vitest unit test 覆蓋燈號顯示對映）

---

## 10. Phase / PR 拆分

原則：每 PR 單一關注點、中等尺寸（500-1500 lines）、合進 main 後仍可運行。

| Phase | PR | 範圍 | 尺寸 | 依賴 |
|---|---|---|---|---|
| **0 輕清理** | **PR-0** | close PR #486；刪 `feat/agent-watch-alive`；Codex readiness 加 capability bit（不補邏輯） | 極小 | — |
| **1 Schema + 雙寫過渡** | **PR-1a** | Trace schema 升級（加 `source_kind / action / reason_code / outcome / scenario_key / observed_generation` 等一級欄位，hook path 填值） | 中 | PR-0 |
| | **PR-1b** | Observation type + Arbitrator **passthrough**（hook/probe/sweep 雙寫直寫 frame **並**送 Observation 供比對） | 中大 | PR-1a |
| **2 Arbitrator 切換** | **PR-2a** | Frame 改 multi-actor + role via pid tree + **ancestor walk** + retry/pending 視窗 | 中大 | PR-1b |
| | **PR-2b** | 切換 Arbitrator 為唯一 writer；移除 hook/probe/sweep direct write | 大 | PR-2a |
| **3 抽象層重構** | **PR-3a** | `AgentSpec = Descriptor + Provider + ProbePolicy` 拆分 + `Capability` bits + `RegisterBuiltinAgents` 顯式註冊 | 中大 | PR-2b |
| | **PR-3b** | `ProbePolicy` + `ProbeBinding(OnDemand/Continuous)` + Scheduler + `common_probes`（motion + 彩虹字）+ self_detection probe（disabled） | 中大 | PR-3a |
| **4 三 agent 對齊** | **PR-4a** | Codex ProbePolicy 實作 + Capability bits 標註 | 中 | PR-3b |
| | **PR-4b** | OpenCode ProbePolicy 實作 + readiness 補齊 | 中 | PR-3b |
| | **PR-4c** | Subagent typed model（`[]SubagentRef{id, type}` 升級至 Frame / Projection；OpenCode typed 從 detail 提升） | 中 | PR-2a |
| **5 硬編拆除 + SPA 對齊** | **PR-5a** | Daemon 側拆 cc 硬編（statusline installer / `/api/agent/status` / stream orchestrator 透過 capability） | 中 | PR-3a |
| | **PR-5b** | SPA 側拆 cc 硬編（icon list / detect list / metadata）+ registry 化；燈號 UI 體質（`clear` 型別、顏色 SOT palette、SessionsSection 4 色對齊） | 中 | PR-5a |
| **6 Trace viewer UI** | **PR-6** | Trace monitor UI 重構：flow graph + DAP-style inspector + filter + time-range API | 大（純 SPA） | PR-1a |

### 10.1 可 parallel 的 PR

- **PR-4a 和 PR-4b**（不同 agent 實作，獨立 worktree）
- **PR-4c 和 PR-4a/4b**（subagent schema 升級 vs agent 實作）
- **PR-6**（SPA only）可從 PR-1a 合完就開始，與 Phase 2-5 並行

### 10.2 關鍵設計點

| 決定 | 理由 |
|---|---|
| Phase 1 雙寫不改行為 | 降風險；Arbitrator 先以 passthrough 跑一段時間，確認 Observation 正確再切 |
| Phase 2 兩 PR 而非一 PR | PR-2a 換 Frame schema 但舊行為照跑；PR-2b 才真切 writer。中間狀態可 run |
| 舊 `AgentProvider` backward-compat 一段時間 | PR-3a 後仍可跑，PR-4 逐步遷移 |
| PR-5a/5b 先 backend 再 SPA | 避免 SPA 依靠 registry 但 backend 還硬編 |
| Phase 6 放最後 | 新 schema 穩定後再重構 UI，避免白工 |

### 10.3 風險點

| PR | 風險 | 緩解 |
|---|---|---|
| PR-2b | 所有 frame 寫入路徑改動，regression 面積大 | Phase 1 雙寫留下 diff log 可比對；配合完整 regression test |
| PR-3a | 既有 hard-coded `cc` 可能有遺漏 | 配合 PR-5 grep 清單核對 |
| PR-4c | Schema 三層同步，migration 風險 | Alpha 階段可接受重置（memory: `feedback_no_alpha_migration`） |

### 10.4 時序指引

- PR-0 → alpha bump 一次（`alpha.198` 為起點）
- Phase 1（PR-1a + PR-1b）→ 至少一個 alpha bump 週期觀察 Arbitrator passthrough 比對 log
- Phase 2（PR-2a + PR-2b）→ PR-2b 上線後觀察一週（regression window）
- Phase 3 以後視團隊容量 / 使用者回饋節奏

---

## 11. 開放問題 / 追蹤 issue

| 主題 | 狀態 | 追蹤 |
|---|---|---|
| Codex hook 缺 `agent_id / agent_type` | 上游 OpenAI codex issue #16226 open；Purdex 靠 pid tree 繞過 | 追蹤 issue 補齊後可開啟 codex `CanSubagent` |
| Sweep → Observation 整合 | PR-1b 完成 | — |
| `common.rainbow_text` 偵測規則 | PR-3b 規劃時定；可能需要 per-agent 彩虹字 palette | 延後 spike |
| Trace snapshot 歷史保留策略 | alpha 階段不保留；GA 前再議 | — |
| Trace DB schema（SQLite）增量升級 | PR-1a 決定 schema 初版；alpha 階段可以 breaking drop | — |

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

**總結**：OTel span envelope + ECS event vocabulary + DAP 互動模型的混合

- **OTel**：span envelope（`trace_id / span_id / parent / kind / status / attributes / events / links`）；`status` 只表執行成功/失敗
- **ECS**：`event.action / event.reason`；`source_kind / action / reason_code / reason_text` 拆分
- **DAP**：到判斷點 → `stopped` event → client 按需拉 `scopes / variables`；UI 節點摘要先顯示 `reason / selected branch / key I/O`，細節 lazy-load

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

---

**End of Spec**
