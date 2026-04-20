# Probe Chain 探測架構設計規格

> 日期：2026-04-13
> 狀態：Draft

## 前置文件

本文件建立在 [agent-module-design (2026-04-10)](2026-04-10-agent-module-design.md) 已完成實作的基礎上。以下部分覆蓋該 spec 的原始假設：

- **「Stream module 邏輯不動，只改 import path」** → 本 spec 的 Step 2 會改寫 Stream orchestrator 內部的 `Detect()` 呼叫，以 `IsAliveFor` + `CheckReadiness` 組合取代
- **「cc.CCDetector interface / DetectorKey 繼續存在」** → 本 spec 的 Step 2 會刪除這兩者，以 `agent.prober` registry key 取代

## 問題

燈號系統收到 `asking` 事件（`permission_prompt` / `elicitation_dialog`）後進入黃燈，但 Claude Code 的 `AskUserQuestion` 工具不觸發後續 hook 事件，導致黃燈卡住無法解除。

### 業界現狀

| 專案 | 做法 | 評價 |
|------|------|------|
| ClaudePulse | 30 秒無事件降級 idle | 不精確 |
| Copilot/Cursor | 控制完整 UI，回覆動作即信號 | 不適用外部工具 |
| Claude Code hooks | `PreToolUse` 可攔截 `AskUserQuestion`，`UserPromptSubmit` 偵測回覆 | 最精確但依賴 hook 覆蓋率 |

### 根本原因

現有架構缺乏「主動觀察」能力。燈號完全被動依賴 hook 事件推送，hook 覆蓋不到的場景就卡住。

## 現有偵測需求盤點

分析 codebase 後，識別出四類偵測需求：

| 類型 | 問題 | 現在誰做 | 問題點 |
|------|------|---------|--------|
| **A. Session 存在** | tmux session 在不在 | `tmux.HasSession()` | 正常，不需改動 |
| **B. Process 存活** | pane 裡跑的還是 agent 嗎 | CC: `Detector.Detect()` 全套；Codex: raw `exec.Command` | CC 過度依賴畫面解析；Codex 不走 `tmux.Executor` (issue #256) |
| **C. 狀態辨識** | agent 是 idle/running/waiting | CC: `detectCCSubState()`；Codex: 無 | CC 專屬，與 B 綁死在同一個 `Detect()` 呼叫 |
| **D. 畫面變化** | 畫面有沒有動 | 不存在 | **本次要解決的核心問題** |

### 現有 Detector 呼叫鏈

```
cc.Detector.Detect(session)
├── PaneCurrentCommand()          ← B 層：前景指令
├── PaneChildCommands()           ← B 層：子行程
├── CapturePaneContent(last 5)    ← B+C 混合：畫面解析
│   ├── looksLikeCC()             ← B 層：確認是 CC
│   └── detectCCSubState()        ← C 層：辨識 idle/running/waiting
```

被呼叫的場景（無持續 polling）：

| 場景 | 呼叫 | 實際需要 |
|------|------|---------|
| `checkAliveAll`（WS reconnect） | `IsAlive()` → `Detect()` 全套 | 只需 B |
| `handleAlive` API | `IsAlive()` → `Detect()` 全套 | 只需 B |
| `provider.Claim()`（無 hook fallback） | `Detect()` 全套 | 需 B+C |
| `operator.Interrupt()` | poll `Detect()` 等 idle | 需 C |
| `operator.Exit()` | poll `Detect()` 等 normal | 需 B（!alive） |
| Stream handoff（4 處） | `ccDetect.Detect()` | 需 B+C |

**核心問題：B 和 C 綁死。** `IsAlive` 只需要知道「agent 還在嗎」，卻要跑完整的畫面解析。

## 設計：Probe Chain

參考 Kubernetes 的 startup → liveness → readiness 三層 probe 模式，每一層只在前一層通過時才跑：

```
Liveness  →  Activity  →  Readiness
 (便宜)       (中等)        (昂貴)
```

### 目標

1. 解決黃燈卡住問題（Activity 層偵測畫面變化）
2. 拆解 Detector 的 B/C 混合問題（Liveness 與 Readiness 分離）
3. 跨 agent 通用（Liveness + Activity 不認識 agent 類型）
4. 統一 CC/Codex 的 process 檢查（消除 Codex raw exec.Command）

### 非目標

- 取代 hook 事件作為燈號主要來源（hook 仍是第一優先）
- 持續 polling 所有 session 的狀態（只在特定條件下啟動）
- Stream mode 的 probe 支援

### 目錄結構

```
internal/agent/probe/
├── probe.go          # Prober 主結構 + 共用 tmux 依賴
├── liveness.go       # Layer 1: process 存活檢查
├── activity.go       # Layer 2: 畫面變化偵測（有生命週期）
├── readiness.go      # Layer 3: 狀態辨識 interface
└── probe_test.go
```

### Layer 1: Liveness — 「還活著嗎」

**職責：** 判斷 pane 裡跑的是否為已知 agent process。

**輸入：** tmux target + 已註冊的 process name list

**輸出：** `alive bool`

```go
// ProcessMatcher 由各 provider 註冊
type ProcessMatcher struct {
    commands map[string]bool  // e.g. {"claude": true, "cld": true}
}

func (p *Prober) IsAliveFor(agentType, target string) bool
```

**實作：**

```
1. PaneCurrentCommand(target)
   → 是已知 agent command？→ alive
   → 是 shell (zsh/bash/...)？→ dead
2. PaneChildCommands(target)
   → 子行程有已知 agent command？→ alive
3. CapturePaneContent(target, 5) + looksLikeAgent()  ← content fallback
   → 畫面有 agent 特徵？→ alive
4. → dead
```

**Content fallback（第 3 步）的必要性：**

CC 可能被 wrapper script（`npx`）或 node vm 啟動，此時 `pane_current_command` 是 `node`，子行程列表裡也沒有 `claude`，但畫面確實顯示 CC 的 prompt。若不做 content fallback，Liveness 會誤判 dead，`checkAliveAll` 廣播假的 `StatusClear` 清除正在使用中的 session。

Content fallback 由各 provider 透過 `ContentMatcher` 可選實作：

```go
// ContentMatcher 是 Liveness 的可選 fallback（provider 不實作則跳過）
type ContentMatcher interface {
    LooksLikeAgent(content string) bool
}
```

CC 實作：搬自現有 `looksLikeCC()`（偵測 `❯`、`Opus`/`Sonnet`/`Haiku`、`Allow`+`Deny`）。
Codex 實作：可暫不實作（Codex binary 名稱固定，不會被 wrapper 包裝）。

**取代範圍：**
- `cc.Provider.IsAlive()` — 不再呼叫 `Detect()` 全套
- `codex.checkPaneProcess()` — 不再用 raw `exec.Command`
- `checkAliveAll()` + `handleAlive` API 的底層實作

**跨 agent 通用：** 各 provider 在 Init 時註冊自己的 process name list。

```go
prober.RegisterProcessNames("cc", []string{"claude", "cld"})
prober.RegisterProcessNames("codex", []string{"codex"})
```

**動態更新：** CC process name list 來自 `config.Detect.CCCommands`，可被使用者在 Settings 動態修改。Prober 提供 `UpdateProcessNames` 方法，由 agent module 的 `OnConfigChange` 回呼觸發：

```go
// Prober API
func (p *Prober) UpdateProcessNames(agentType string, names []string)

// agent module Init() 中
c.OnConfigChange(func() {
    c.CfgMu.RLock()
    cmds := c.Cfg.Detect.CCCommands
    c.CfgMu.RUnlock()
    prober.UpdateProcessNames("cc", cmds)
})
```

### Layer 2: Activity — 「畫面有沒有動」

**職責：** 持續監控指定 pane 的畫面變化，偵測到變化時通知 agent module。

**輸入：** tmux target + callback function

**輸出：** 透過 callback 通知（不用 channel，避免 goroutine 洩漏風險）

**生命週期：** 有啟動/停止，不是永久跑。

```go
// ActivityCallback 在偵測到畫面變化時被呼叫（在 watcher goroutine 內）
type ActivityCallback func(target string)

// StartWatch 啟動畫面變化監控。若該 target 已有 active watcher，先停止舊的再啟動新的。
func (p *Prober) StartWatch(target string, cb ActivityCallback)

// StopWatch 停止指定 target 的畫面變化監控。冪等，無 active watcher 時為 no-op。
func (p *Prober) StopWatch(target string)

// StopAllWatches 停止所有 active watchers。用於 daemon shutdown。
func (p *Prober) StopAllWatches()
```

**設計決策 — 為什麼用 callback 而非 channel：**

Channel 有兩個陷阱：(1) 消費者不讀時 goroutine 卡在 unbuffered send 永久阻塞；(2) `StartWatch` 回傳 CancelFunc + `StopWatch` 雙重取消路徑，同一 target 重複啟動會洩漏。Callback 模式下 `StartWatch` 和 `StopWatch` 是唯一的控制路徑，Prober 內部管理所有 goroutine 生命週期，不暴露 cancel 語意給外部。

**實作：**

```go
func (p *Prober) StartWatch(target string, cb ActivityCallback) {
    p.mu.Lock()
    // 若已有 active watcher，先停止
    if cancel, ok := p.watchers[target]; ok {
        cancel()
    }
    ctx, cancel := context.WithCancel(context.Background())
    p.watchers[target] = cancel
    p.mu.Unlock()

    go func() {
        defer func() {
            p.mu.Lock()
            // 只清自己（避免清掉後來啟動的 watcher）
            if c, ok := p.watchers[target]; ok && c == cancel {
                delete(p.watchers, target)
            }
            p.mu.Unlock()
        }()

        baseline := hashCapture(p.tmux, target)
        ticker := time.NewTicker(500 * time.Millisecond)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                current := hashCapture(p.tmux, target)
                if current != baseline {
                    cb(target)
                    return  // 發射一次後結束，由消費者決定是否重新啟動
                }
            }
        }
    }()
}
```

**設計細節：**

| 參數 | 值 | 理由 |
|------|-----|------|
| poll interval | 500ms | 與現有 Detector poll 一致，平衡精度與成本 |
| capture lines | 10 | 比 Detector 的 5 行多一點，提高偵測可靠度 |
| hash 演算法 | fnv32a | 標準庫內建，比 SHA256 便宜，只需偵測變化 |
| 觸發後行為 | 發射一次後 goroutine 自行結束 | 避免重複觸發，消費者決定後續 |

**生命週期管理：**

| 情境 | 處理 |
|------|------|
| 同一 target 重複 `StartWatch` | 先 cancel 舊 watcher，再啟動新的 |
| `StopWatch` 呼叫時無 active watcher | no-op（冪等） |
| Daemon shutdown | agent module 的 `Stop()` 呼叫 `prober.StopAllWatches()` |
| Session 被刪除 | `checkAliveAll` 清理時或 session delete handler 呼叫 `StopWatch` |
| Watcher goroutine 偵測到變化 | goroutine 自行結束，清理 `watchers` map |
| Daemon restart 後 DB replay 出 `waiting` 狀態 | 不重新啟動 watcher — replay 只重建靜態 status，Activity 只在即時事件觸發 |

### Layer 3: Readiness — 「什麼狀態」

**職責：** 判斷 agent 的細部狀態（idle / running / waiting）。

**輸入：** tmux target

**輸出：** `agent.Status`

```go
// ReadinessChecker 由各 provider 實作
type ReadinessChecker interface {
    CheckReadiness(target string) ReadinessResult
}

type ReadinessResult struct {
    Status agent.Status  // 統一使用 agent.Status 型別，非裸 string
    Raw    string        // 原始畫面內容（debug 用，可選）
}
```

**CC 實作（搬自現有 `detectCCSubState`）：**

```
CapturePaneContent(target, 5)
→ Contains "Allow" + "Deny" → agent.StatusWaiting
→ 最後幾行有 ❯ → agent.StatusIdle
→ 其他 → agent.StatusRunning
```

**Codex 實作（新增）：**

```
CapturePaneContent(target, 5)
→ Codex 特有的 prompt 特徵 → agent.StatusIdle
→ 其他 → agent.StatusRunning
```

**取代範圍：**
- `cc.detectCCSubState()` — 搬入 CC 的 ReadinessChecker 實作
- Stream handoff 的 `ccDetect.Detect()` — 改為 `IsAliveFor` + `CheckReadiness` 組合

### Prober 主結構

```go
package probe

type Prober struct {
    tmux       tmux.Executor
    
    matcherMu  sync.RWMutex
    matchers   map[string]*ProcessMatcher   // agentType → matcher
    content    map[string]ContentMatcher     // agentType → optional content fallback
    readiness  map[string]ReadinessChecker   // agentType → checker
    
    watcherMu  sync.Mutex
    watchers   map[string]context.CancelFunc // target → active watcher cancel
}

func New(tmux tmux.Executor) *Prober

// Layer 1
func (p *Prober) RegisterProcessNames(agentType string, names []string)
func (p *Prober) UpdateProcessNames(agentType string, names []string)
func (p *Prober) RegisterContentMatcher(agentType string, m ContentMatcher)
func (p *Prober) IsAliveFor(agentType, target string) bool

// Layer 2
func (p *Prober) StartWatch(target string, cb ActivityCallback)
func (p *Prober) StopWatch(target string)
func (p *Prober) StopAllWatches()

// Layer 3
func (p *Prober) RegisterReadiness(agentType string, checker ReadinessChecker)
func (p *Prober) CheckReadiness(agentType, target string) (ReadinessResult, bool)
```

**注意：不提供無 agentType 的 `IsAlive(target)` 方法。** 所有存活檢查都必須指定 agent type，避免跨 agent process name 誤匹配（例如 pane 跑著 codex 但 DB 記錄的是 cc session）。

## 整合：Agent Module

### 初始化

```go
// module/agent/module.go Init()

prober := probe.New(c.Tmux)

// CC
prober.RegisterProcessNames("cc", c.Cfg.Detect.CCCommands)
prober.RegisterContentMatcher("cc", cc.NewContentMatcher())
prober.RegisterReadiness("cc", cc.NewReadinessChecker(c.Tmux))

// Codex
prober.RegisterProcessNames("codex", []string{"codex"})
prober.RegisterReadiness("codex", codex.NewReadinessChecker(c.Tmux))

// 動態更新 CC process names
c.OnConfigChange(func() {
    c.CfgMu.RLock()
    cmds := c.Cfg.Detect.CCCommands
    c.CfgMu.RUnlock()
    prober.UpdateProcessNames("cc", cmds)
})

// 註冊到 registry 供其他 module 使用
c.Registry.Register("agent.prober", prober)
```

### 併發模型：Mutex + Active Flag

Agent module 的 `handleEvent` 和 Activity callback 是兩個併發 state writer。採用現有的 `sync.Mutex mu` 序列化存取，以 `activeWatchers` map 作為 flag 區分優先級：

```go
// Module 擴充
type Module struct {
    // ...existing fields...
    prober         *probe.Prober
    activeWatchers map[string]string  // tmuxSession → agentType
}
```

**Hook 事件路徑（優先）：**

```go
func (m *Module) handleEvent(session, agentType string, event HookEvent) {
    // 1. 停止該 session 的 Activity watcher（如有）
    m.mu.Lock()
    if _, watching := m.activeWatchers[session]; watching {
        delete(m.activeWatchers, session)
        m.mu.Unlock()
        m.prober.StopWatch(session + ":")
    } else {
        m.mu.Unlock()
    }

    // 2. 正常 deriveStatus + 廣播
    result := provider.DeriveStatus(event.EventName, event.RawEvent)
    // ...

    // 3. 若新狀態是 waiting，啟動 Activity watch
    if result.Status == agent.StatusWaiting {
        m.mu.Lock()
        m.activeWatchers[session] = agentType
        m.mu.Unlock()
        m.prober.StartWatch(session+":", m.onActivityDetected(session, agentType))
    }
}
```

**Activity callback 路徑：**

```go
func (m *Module) onActivityDetected(session, agentType string) probe.ActivityCallback {
    return func(target string) {
        m.mu.Lock()
        // 檢查 watcher 是否仍 active（可能已被 hook 事件停止）
        if _, active := m.activeWatchers[session]; !active {
            m.mu.Unlock()
            return  // hook 事件已優先處理，丟棄
        }
        delete(m.activeWatchers, session)
        m.mu.Unlock()

        // Liveness 閘控
        if !m.prober.IsAliveFor(agentType, target) {
            return
        }

        // Readiness 辨識
        result, ok := m.prober.CheckReadiness(agentType, target)
        if !ok {
            return
        }

        // 狀態有變化才廣播（仍是 waiting 就不動）
        if result.Status != agent.StatusWaiting {
            m.broadcastStatus(session, result.Status)
        }
    }
}
```

**race 不會發生的原因：** `activeWatchers[session]` 在 `mu.Lock()` 保護下讀寫。hook 路徑先 delete → Activity callback 檢查時已不存在 → 丟棄。反之亦然。先到的贏。

### Module Stop 清理

```go
func (m *Module) Stop(_ context.Context) error {
    m.prober.StopAllWatches()
    m.mu.Lock()
    m.activeWatchers = make(map[string]string)
    m.mu.Unlock()
    return nil
}
```

### checkAliveAll 改造

```go
// 現在
provider.IsAlive(tmuxTarget)  // CC: Detect() 全套，不區分 agent type

// 改後
prober.IsAliveFor(ev.AgentType, tmuxTarget)  // 指定 agent type，只跑 Liveness 層
```

### Stream Module 改造

```go
// 現在
m.ccDetect.Detect(target)  // 回傳 cc.Status

// 改後（runHandoff — 判斷 CC 是否在跑）
if !m.prober.IsAliveFor("cc", target) {
    broadcast("failed:no CC running")
    return
}
result, _ := m.prober.CheckReadiness("cc", target)
if result.Status != agent.StatusIdle {
    // CC 在忙，需要先 interrupt
}
```

```go
// runHandoffToTerm — 「等殼」邏輯
// 現在
m.ccDetect.Detect(target) == agentcc.StatusNormal

// 改後：StatusNormal 的語意 = 「不在 CC 裡了」= !IsAliveFor
for time.Now().Before(shellDeadline) {
    if !m.prober.IsAliveFor("cc", target) {
        break  // CC 已退出，回到 shell
    }
    time.Sleep(500 * time.Millisecond)
}
if m.prober.IsAliveFor("cc", target) {
    broadcast("failed:shell did not recover")
    return
}
```

Stream module 從 registry 取 `"agent.prober"` 而非 `"cc.detector"`。

### Operator 改造

Operator（`cc/operator.go`）的 `Interrupt()` 和 `Exit()` 內部直接呼叫 `p.detector.Detect()`。改造後 `cc.Provider` 需持有 `Prober` 參考：

```go
// cc/provider.go
type Provider struct {
    prober   *probe.Prober   // 取代 detector
    tmuxExec tmux.Executor
    cfg      *config.Config
    cfgMu    *sync.RWMutex
}
```

```go
// operator.Interrupt() — poll 等 idle
// 現在
p.detector.Detect(tmuxTarget) == StatusCCIdle

// 改後
result, _ := p.prober.CheckReadiness("cc", tmuxTarget)
result.Status == agent.StatusIdle
```

```go
// operator.Exit() — poll 等 CC 退出
// 現在
p.detector.Detect(tmuxTarget) == StatusNormal

// 改後：「不在 CC 裡了」= !IsAliveFor
!p.prober.IsAliveFor("cc", tmuxTarget)
```

### Claim 路徑遷移

`cc.Provider.Claim()` 在無 hook 時 fallback 呼叫 `Detect()` 全套。遷移方式：

```go
// 現在
func (p *Provider) Claim(ctx agent.ClaimContext) bool {
    if ctx.HookEvent != nil {
        return ctx.HookEvent.AgentType == "cc"
    }
    status := p.detector.Detect(ctx.TmuxTarget)
    return status != StatusNormal && status != StatusNotInCC
}

// 改後
func (p *Provider) Claim(ctx agent.ClaimContext) bool {
    if ctx.HookEvent != nil {
        return ctx.HookEvent.AgentType == "cc"
    }
    return p.prober.IsAliveFor("cc", ctx.TmuxTarget)
}
```

`IsAliveFor` 包含 content fallback（`looksLikeCC`），所以 wrapper script 啟動 CC 的情境也能正確 claim。

## 現有 Detector 處置

| 現有元件 | 處置 |
|---------|------|
| `cc.Detector` struct | 拆解後刪除 |
| `cc.Detect()` | Layer 1 (process check + content fallback) → `probe.IsAliveFor`；Layer 3 (sub-state) → CC ReadinessChecker |
| `cc.detectCCSubState()` | 搬入 CC ReadinessChecker |
| `cc.looksLikeCC()` | 搬入 CC ContentMatcher（`probe.ContentMatcher` interface 實作） |
| `cc.Status` 常數 (StatusNormal 等) | Liveness 用 `bool`；Readiness 用 `agent.Status`。Step 1 不動，**Step 2 才可刪除** |
| `cc.CCDetector` interface | 刪除，改用 `probe.ReadinessChecker` |
| `cc.DetectorKey` registry key | 刪除，改用 `"agent.prober"` |
| `cc.StatusCCUnread` | 目前未被任何消費者使用，直接刪除 |
| `codex.checkPaneProcess()` | 刪除，改用 `probe.IsAliveFor` |
| `codex.isCodexProcess()` | 邏輯移入 `prober.RegisterProcessNames` |

## Readiness Status 值

各 provider 的 `ReadinessResult.Status` 統一使用 `agent.Status` 型別常數：

| ReadinessResult.Status | 對應常數 | 說明 |
|----------------------|---------|------|
| `agent.StatusIdle` | `"idle"` | agent 在 prompt，等待使用者輸入 |
| `agent.StatusRunning` | `"running"` | agent 正在處理 |
| `agent.StatusWaiting` | `"waiting"` | agent 等待使用者操作（permission 等） |

不再使用 CC 專屬的 `StatusNormal` / `StatusNotInCC` / `StatusCCIdle` 等——這些語意由 Liveness（`alive`/`dead`）+ Readiness（`idle`/`running`/`waiting`）組合表達。

消費者直接用 `agent.Status` 常數比較，不用字串字面值：

```go
// ✓ 正確
result.Status == agent.StatusIdle

// ✗ 錯誤 — 不要用字串字面值
result.Status == "idle"
```

## 遷移策略

分三步，每步可獨立 PR：

### Step 1: 建立 probe package + Liveness 層

- 建立 `internal/agent/probe/`
- 實作 `Prober` + `IsAliveFor` + `RegisterProcessNames` + `UpdateProcessNames`
- 實作 `ContentMatcher` interface + CC `looksLikeCC` 搬入
- CC/Codex provider 改為透過 prober 做 IsAlive
- `checkAliveAll` / `handleAlive` 改用 `prober.IsAliveFor`
- `OnConfigChange` 改為呼叫 `prober.UpdateProcessNames`
- 此步不動 Readiness，Stream module 暫時同時依賴 prober 和舊 Detector
- **`cc.Status` 常數在此步不刪除**（Stream module 仍依賴）

### Step 2: 拆出 Readiness 層 + 刪除舊 Detector

- 實作 `ReadinessChecker` interface + `ReadinessResult`（使用 `agent.Status` 型別）
- CC: 把 `detectCCSubState` 搬入 `cc.ReadinessChecker`
- Codex: 新增基本 `ReadinessChecker`
- `cc.Provider` 改持有 `Prober` 參考，取代 `Detector`
- Operator `Interrupt()` 改用 `prober.CheckReadiness`（比較 `agent.StatusIdle`）
- Operator `Exit()` 改用 `!prober.IsAliveFor`
- Claim 路徑改用 `prober.IsAliveFor`
- Stream module 改用 `prober.IsAliveFor` + `prober.CheckReadiness`
  - `runHandoff`：`IsAliveFor` 判斷 CC 在跑 + `CheckReadiness` 判斷是否 idle
  - `runHandoffToTerm`：`!IsAliveFor` 表示「CC 已退出，回到 shell」
- 刪除 `cc.Detector` / `cc.CCDetector` interface / `cc.DetectorKey` / `cc.Status` 常數 / `cc.StatusCCUnread`
- **Review 保護：** `orchestrator.go` 的 `runHandoff` 和 `runHandoffToTerm` 分開 review，逐一確認語意對齊

### Step 3: 新增 Activity 層（解決黃燈問題）

- 實作 `StartWatch` / `StopWatch` / `StopAllWatches`（callback 模式）
- Agent module 擴充 `activeWatchers` map + mutex 序列化
- `handleEvent` 在 `waiting` 狀態時啟動 Activity watch
- `handleEvent` 收到任何 hook 事件時先 `StopWatch`（事件穿透優先）
- Activity callback 檢查 `activeWatchers` flag 後走 Liveness → Readiness → 廣播
- Agent module `Stop()` 呼叫 `StopAllWatches()` 清理所有 goroutine
- Session 刪除時呼叫 `StopWatch` 清理
- Daemon restart replay **不** 重啟 watcher（只重建靜態 status）
- 端到端測試：asking → 畫面變化 → 綠燈；asking → hook 事件穿透 → 正確狀態
