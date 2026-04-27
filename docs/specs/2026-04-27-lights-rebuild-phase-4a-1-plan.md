# Lights Rebuild — Phase 4a-1 Plan v1.4 (PR-4a-1 Probe Primitive + cc + Helper + Dev Log)

**Status**: draft v1.4（Round 4 codex review fix — 1 P2 stale-callback finding 採納）

## v1.x 演進

| 版本 | 變更 |
|---|---|
| v1 | 初版起草（6 slices / 24 tests / ~305 LoC）|
| v1.1 | R1 codex review fix（`review-moh40grn-u7wxgv`，2 P2）：(1) API gap — `WatchTopLines` / `WatchFullScreen` 無法接收 `IdleStableTicks`；併為單一 `Watch(target, opts, cb)` + `WatchOptions{TopLines, IdleStableTicks}`；(2) repeated stable emission — `stableEmitted` flag 確保每次 changed→stable transition 只觸發一次 ScreenStable；新增 PR2b 測試覆蓋連續穩定無重發 |
| v1.2 | R2 codex review fix（1 P1 + 1 P2）：(1) **rename rewatch path** — `renameSessionLocked` (module.go:258) 也呼叫 `StartWatch(newName+":", ...)`；plan 必須一併走 orchestrator + 加 rename regression test；(2) **baseline 失敗 cleanup race** — 初始 capture 失敗時 `watchLoop` 直接 return 但 watcher map 已註冊；新增 baseline-fail-cleanup 邏輯與 PR4b 測試 |
| v1.3 | R3 codex review fix（1 P1 deadlock）：v1.2 §2.3.1 orchestrator API docstring 寫 `stopWatch` / `startWatch` 會 clear `activeWatchers`，但 `renameSessionLocked` 持 `m.mu` 時呼叫，會與內部要 acquire `m.mu` 的清理路徑互鎖（非可重入 mutex）。**改 API contract**：orchestrator 只碰 `prober` + `lastHookAt` + metrics，**不觸 `activeWatchers`**；caller（module.go wrapper）管理 `activeWatchers`。同時對齊 `interpretScreenEvent` 的 Error Guard 邏輯沿用 legacy 既有 lock 模式（讀 + 寫各自一次 m.mu.Lock/Unlock，不持鎖跨呼叫）|
| v1.4 | R4 codex review fix（1 P2 stale-callback guard）：legacy `onActivityDetected` (module.go:473) 開頭檢查 `activeWatchers[session]` 不存在則 return，這個 stale-callback guard v1.3 plan 漏搬。新 watch-loop-owned watcher fire callback 多次（不像 legacy fire 一次就退），**此 guard 比 legacy 更必要** — stopWatch / rename race 中 in-flight callback 會在 watcher 已停的情況下繼續更新 status / broadcast。`interpretScreenEvent` 開頭加 `currentAgent, active := m.activeWatchers[session]; if !active \|\| currentAgent != agentType { return }`；新增 OR6 regression test |

**前置**：
- `docs/specs/2026-04-23-lights-rebuild-spec.md` — 整體 Lights Rebuild 設計
- `docs/specs/2026-04-26-lights-rebuild-phase-4a-plan.md` v1.3 — Phase 4a 整體 plan（PR-4a-0 已 ship at alpha.233；PR-4a-1 / PR-4a-2 大綱於 §7.1 / §7.2；§7.3 Slice 6 design-impact stop/go gate）
- `docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md` §6 — Slice 6 design-impact summary：**All 3 triggers reliable → Go**（per plan v1.3 §7.3 evidence-based criteria）

**繼承**：
- spec §2.4.1 Architecture Guardrails（Probe layer = pure primitive，agent module = policy）
- kickoff_lights_rebuild Decision 1/2/4/6/7/8/13（pure primitive / 砍字元偵測 / Top-N hash / graceWindow + PDX_DEV_MODE log / `purdex_probe_*` expvar / `CapturePaneRange`+`CapturePaneTopLines` / watch-loop-owned ownership）

---

## 0. 來龍去脈

PR-4a-0（OpenCode 1.14.23 hooks completion）已 ship at alpha.233。audit 確認三家 trigger pattern 全 reliable，PR-4a-1 / PR-4a-2 切分按 plan v1.3 §7 大綱繼續。

PR-4a-1 把目前 `internal/agent/probe/activity.go` 的 watcher 從「probe 解讀 signal + 模組接收 enum」重構為「probe 發 ScreenChangeEvent 原始事件 + 模組解讀」，並把 cc module 的 watch lifecycle 抽成共用 orchestrator helper（PR-4a-2 codex / opencode 接同一份 helper）。同時補上 Phase 4 框架早就規劃但尚未落地的 graceWindow + PDX_DEV_MODE observation log + `purdex_probe_*` expvar。

**為什麼不做字元偵測**（spec §8.1 原方向）：cc / codex spinner 都帶 elapsed timer，整面 pane hash diff 永遠變動，字元偵測救的場景實務頻率近零。改用 **Top-N 行 hash 偵測**（自動過濾使用者打字 / spinner / 彩虹底部位置雜訊）— 見 Slice 1 §2.1.3。

**為什麼拆 PR-4a-1 / PR-4a-2**：probe primitive 與 module orchestrator 是平台 API，落地 cc 一家先驗證設計形狀；codex / opencode 接駁 + 清舊放下一個 PR，避免一次改三家發生 merge-time conflict 又卡 review。

---

## 1. Scope

### 1.1 In scope

**Slice 0**：tmux API 擴展
- `CapturePaneRange(target, start, endInclusive int) (string, error)` — 通用底層 primitive
- `CapturePaneTopLines(target string, n int) (string, error)` — Top-N 糖衣
- fake executor 對應實作 + `tmux.Executor` interface 擴增

**Slice 1**：Probe primitive 重構
- 新增 `ScreenChangeEvent` struct + `ScreenChangeCallback`
- 新增 `WatchOptions{TopLines int, IdleStableTicks int}`（R1 fix #1 — 統一收 caller tuning，避免雙 API 分歧 + IdleStableTicks 無入口）
- 新增 `Watch(target, opts, cb)` — 單一 entry：`opts.TopLines == 0` 走 full screen；`> 0` 走 top-N
- `watchLoop` 統一內部 poll 機制；watcher 自治（callback 不再 cancel/delete）；**`stableEmitted` flag**（R1 fix #2）— ScreenStable 每次 changed→stable transition 只觸發一次，避免 idle 重發
- **移除**：`activityLoop` 對 `ActivitySignal{Running, Idle, ShellPrompt}` 的解讀；`StartWatch` 簽名換成新 primitive；舊 callback 路徑由 module 層接管
- **保留**：`StopWatch` / `StopAllWatches` / `HasWatcher` 維持原語意

**Slice 2**：ShellPrompt utility 保留
- `looksLikeShellPrompt(content)` / `stripANSI(content)` 維持 probe package（公開為 `LooksLikeShellPrompt` 讓 module 呼用）；不改演算法、不加 test，純 visibility 提升

**Slice 3**：Module 共用 orchestrator helper
- 新增 `internal/module/agent/probe_orchestrator.go`（暫名）
- 抽出 `manageActivityWatch` + `onActivityDetected` 共用邏輯為：
  - `startProbeWatch(session, agentType)` — 啟動 watcher（依 agent profile 決定 TopN 行數）
  - `stopProbeWatch(session)` — 停止 watcher（含 activeWatchers map cleanup）
  - `interpretScreenEvent(session, agentType, event)` — 把 ScreenChangeEvent 解讀為 Status，套用 graceWindow + Error Guard + projection update + broadcast
- `agentpkg.Provider` 新增 optional interface `ProbeProfileProvider`（暫名）
  - `ProbeProfile()` → `{TopLines int, IdleStableTicks int}` 等 per-agent tuning
  - cc 在 Slice 4 實作；未實作的 agent 走 default profile（沿用現行 10 行 / 3 ticks）

**Slice 4**：cc 接新 primitive
- `internal/agent/cc/` 新增 `probe_profile.go`：cc 實作 `ProbeProfileProvider`，回傳 cc 專屬 TopLines / IdleStableTicks
- `internal/module/agent/module.go` 把現行 `manageActivityWatch` / `onActivityDetected` 改為 thin wrapper 呼叫 Slice 3 helper（codex / opencode 在 PR-4a-2 切換；本 PR 維持透過 default profile 走相同 helper，使三家同一 entry，行為向下相容）

**Slice 7**：graceWindow + PDX_DEV_MODE log + expvar
- `internal/module/agent/probe_orchestrator.go` 引入 `lastHookAt map[session]time.Time` + `probeGraceWindow = 2 * time.Second`（spec §2.4 cited 值）
- 新增 method `recordHookAt(session)` — handler 處理任何 hook event 時呼叫
- `interpretScreenEvent` 開頭檢查 graceWindow，命中則 suppress 並 log（gated by `PDX_DEV_MODE=1`）
- `internal/agent/metrics.go` 新增 4 個 expvar counter（命名 `purdex_probe_*`，**不 rename** 既有 `purdex_phase35_*`）：
  - `purdex_probe_watch_started_total`
  - `purdex_probe_watch_stopped_total`
  - `purdex_probe_screen_event_total`
  - `purdex_probe_grace_window_suppressed_total`

### 1.2 Out of scope（明列分流）

- **Slice 5（codex 接新 primitive）/ Slice 6（opencode 接新 primitive）/ Slice 8（清舊 onActivityDetected / shouldWatchActivity / ActivitySignal enum signal 解讀）** — 全部留 PR-4a-2
- **Phase 4b `ProbeIntentProvider`** — 整合 readiness 的 declarative intent 系統，留 Phase 4b
- **Phase 5 Dev Inspector SPA UI** — `/api/agent/monitor/*` 視覺化，留 Phase 5
- **Character-level detection（彩虹字 / spinner 偵測）** — kickoff Decision 2 已砍，Top-N hash 取代
- **User typing vs agent loading 細緻分辨** — kickoff Decision 3 已砍

---

## 2. 設計

### 2.0 Slice 0 — tmux API 擴展（~40 LoC + 4 tests）

#### 2.0.1 新增 `tmux.Executor` interface methods

```go
// CapturePaneRange returns lines [start, endInclusive] of the pane.
// Indexing follows tmux capture-pane -S/-E semantics:
//   - start/end are line indices; 0 = top of visible pane
//   - negative values reference history above the visible pane
// Returns error on tmux invocation failure or invalid range.
CapturePaneRange(target string, start, endInclusive int) (string, error)

// CapturePaneTopLines returns the top n lines (0..n-1) of the pane.
// Equivalent to CapturePaneRange(target, 0, n-1).
CapturePaneTopLines(target string, n int) (string, error)
```

#### 2.0.2 RealExecutor 實作

- `CapturePaneRange`：呼叫 `tmux capture-pane -p -t <target> -S <start> -E <endInclusive>`；空輸出視為合法（未必是錯）
- `CapturePaneTopLines`：呼叫 `CapturePaneRange(target, 0, n-1)`；`n <= 0` 回傳 ""（不報錯，方便 caller 用零值 disable）

**注意**：保留 `CapturePaneContent(target, lastN)` 不動 — 既有 callsite（probe / monitor / debug endpoints）仍使用，不在本 PR scope；PR-4a-2 Slice 8 清舊時再判斷是否 deprecated。

#### 2.0.3 Fake executor 對應

`internal/tmux/fake_executor.go`（如果不存在則在 test 檔內建）需要對應 stub：
- `CapturePaneRange` / `CapturePaneTopLines` 用同一份 `paneContent map[string]string` 作 source
- 加 `paneContentByRange map[string][]string` 讓 test 注入 per-range 模擬輸出

#### 2.0.4 測試（4 tests）

| Test | 重點 |
|------|------|
| TT1 `TestCapturePaneRange_RealExecutor_PassesArgs` | 用 `tmux` script-mode mock 驗證 `-S` `-E` 參數透傳正確 |
| TT2 `TestCapturePaneTopLines_DelegatesToRange` | 表 test：n=1/3/10 對應 range (0,0)/(0,2)/(0,9) |
| TT3 `TestCapturePaneTopLines_ZeroOrNegative_ReturnsEmpty` | n=0/n=-1 回 ""，不調 tmux |
| TT4 `TestFakeExecutor_RangeMethods_Roundtrip` | 注入 5-line content，每種 range 取出對應 slice |

---

### 2.1 Slice 1 — Probe primitive 重構（~80 LoC + 6 tests）

#### 2.1.1 新增 `ScreenChangeEvent`

```go
// ScreenChangeEvent is the raw output of a screen watcher.
// The probe layer emits these without interpreting them as agent status —
// callers (agent module / orchestrator) own the policy of mapping events
// to status transitions.
type ScreenChangeEvent struct {
    Kind        ScreenChangeKind
    Target      string    // tmux target e.g. "session-name:"
    Content     string    // captured content at the moment of the event
    OccurredAt  time.Time // probe-layer wall clock
}

type ScreenChangeKind string

const (
    ScreenChanged ScreenChangeKind = "changed" // hash differs from last sample
    ScreenStable  ScreenChangeKind = "stable"  // hash matched for N consecutive ticks
)

type ScreenChangeCallback func(ScreenChangeEvent)
```

#### 2.1.2 新增 `WatchOptions` + 單一 `Watch` API（R1 fix #1）

```go
// WatchOptions tunes the watch loop behavior. Zero values fall back to
// safe defaults (TopLines = 0 → full screen; IdleStableTicks = 0 → 3).
type WatchOptions struct {
    // TopLines limits captured content to the top N lines via
    // tmux.CapturePaneTopLines. 0 means full screen (CapturePaneContent).
    TopLines int

    // IdleStableTicks is the number of consecutive identical-hash ticks
    // before ScreenStable is emitted. 0 means default 3.
    IdleStableTicks int
}

// Watch starts a screen change watcher on the target.
// Emits ScreenChanged on hash diff; emits ScreenStable exactly once per
// changed→stable transition (see watchLoop §2.1.3). The watcher persists
// until StopWatch is called — callbacks do NOT terminate the watcher
// (kickoff Decision 13: watch-loop-owned).
func (p *Prober) Watch(target string, opts WatchOptions, cb ScreenChangeCallback) {...}
```

**設計理由**（R1 fix #1）：
- 單一 entry 比雙 API 簡潔；`TopLines == 0` 是天然 full-screen sentinel
- `WatchOptions` 結構體擴展性好，未來加 poll interval / hash algorithm 等不破壞 API
- IdleStableTicks 經由 opts 結構流入 watchLoop，OR1 test 可以對 cc profile `{Lines:5, IdleStableTicks:2}` 驗 stable threshold

**caller 端**：orchestrator 拿 ProbeProfile 後組成 `WatchOptions{TopLines: profile.TopLines, IdleStableTicks: profile.IdleStableTicks}` 傳入 `Watch`（§2.3.3 詳述）。

#### 2.1.3 `watchLoop` 內部 poll 機制（含 R1 fix #2）

- 啟動：`captureFn` (closure 依 `opts.TopLines` 決定呼叫 `CapturePaneTopLines(target, opts.TopLines)` 或 `CapturePaneContent(target, fullScreenLineCount)`) → ticker 500ms → ctx loop
- 邏輯：
  ```
  idleStableTicks = opts.IdleStableTicks; if idleStableTicks == 0 { idleStableTicks = 3 }
  baseline, ok = capture()
  if !ok {
      // R2 fix #2: baseline-fail cleanup — watcher map 已在 Watch 內註冊
      // 此 goroutine 即將退出，必須清自己的 entry，否則 HasWatcher 會說謊
      p.watcherMu.Lock()
      if entry, ok := p.watchers[target]; ok && entry.id == id {
          delete(p.watchers, target)
      }
      p.watcherMu.Unlock()
      return
  }
  stableCount = 0
  stableEmitted = false  // R1 fix #2: 每次 changed→stable transition 只觸發一次
  loop:
    select ctx.Done -> return
    select tick:
      current, ok = capture()
      if !ok: continue (skip tick, don't fire false event)
      if hash(current) != hash(baseline):
        cb(ScreenChanged{Content: current})
        baseline = current
        stableCount = 0
        stableEmitted = false  // change resets, future stable-emit allowed
        continue
      // hash unchanged
      stableCount++
      if !stableEmitted && stableCount >= idleStableTicks:
        cb(ScreenStable{Content: current})
        stableEmitted = true
        // do NOT reset stableCount; do NOT re-emit ScreenStable until next change
  ```
- **關鍵差異 vs legacy `activityLoop`**：watcher 不退出；可發多次 ScreenChanged + ScreenStable transitions；callback 不擁有 watcher 生命週期；同一 stable run 不會重發 ScreenStable
- **R1 fix #2 rationale**：legacy `activityLoop` 自動退出後不存在 repeated emission 問題；新 watch-loop-owned 設計若不加 `stableEmitted` flag，idle pane 會每 N ticks 重發 ScreenStable → orchestrator 對應重發 StatusIdle broadcast → metrics counter 無限累積 + log 噴 + WS 客戶端收 idle 風暴。`stableEmitted` 確保「stable run 視為一個 transition，到下次 change 才再 arm」
- **R2 fix #2 rationale**：legacy `activityLoop` 用 `defer` 統一在 goroutine 退出時清 watcher map entry，所以 baseline 失敗的 early-return 也涵蓋；新 ownership 文字寫成「cleanup only happens on ctx cancel」會漏掉 baseline-failure path → watcher map 殘留死 entry → `HasWatcher(target) == true` 但實際上 goroutine 已退 → 後續 `Watch(target, ...)` 會把同一個 entry 的 cancel 替換掉但既有 goroutine 已死 → 行為不可預測。修法：在 baseline 失敗 early-return 前同步清自己的 entry（only-if-still-mine 用 `id` 比對，避免清到後續 watch 的）

#### 2.1.4 Watcher ownership 變更

- **Legacy**：`activityLoop` defer 內 `delete(p.watchers, target)`；callback fire 一次後 goroutine 自動退出 + 清自己
- **New**：`watchLoop` defer 只在 ctx cancel 時 cleanup；callback **不能** 觸發退出；module 層必須顯式 `StopWatch(target)` 終止
- 這個設計避免 module 在 callback 內擔心 race（watcher 還沒退就被新 `StartWatch` 覆蓋）+ 對齊 spec §2.4.1 「probe 不知道 status，不該決定該不該停」

#### 2.1.5 移除的東西

- `ActivitySignal` enum + `ActivityCallback` type — 由 ScreenChangeCallback / ScreenChangeEvent 取代；舊 callsite 由 module 層轉接（Slice 3）
- `StartWatch(target, ActivityCallback)` 公開 API — 替換為 `Watch(target, opts, cb)`
- `activityLoop` 內部 method — 改成 `watchLoop`
- `hashCapture` 內部 method — 改成支援 captureFn closure 的版本

#### 2.1.6 測試（7 tests — R1 +1 PR2b）

| Test | 重點 |
|------|------|
| PR1 `TestWatch_FiresChangedOnDiff` | 注入兩次 capture（同 → 異），驗證單次 ScreenChanged callback、Content 為新內容 |
| PR2 `TestWatch_FiresStableAfterNIdenticalSamples` | 連續 4 次同 capture，驗證單次 ScreenStable callback（baseline + 3 stable ticks）|
| PR2b `TestWatch_StableEmittedOnceUntilNextChange`（**R1 fix #2 regression**）| 注入 6 次同 capture（baseline + 5 stable）— 驗證 ScreenStable 只 fire 1 次；接著注入 diff → ScreenChanged fire；再注入 4 同 → ScreenStable 再 fire 1 次（changed→stable 切換才 re-arm）|
| PR3 `TestWatch_DoesNotExitOnCallback` | 一次 ScreenChanged callback 後再注入 diff，驗證再發 ScreenChanged（loop 持續）|
| PR4 `TestWatch_StopWatch_CancelsLoop` | StopWatch 後 capture mock 不再被 call、HasWatcher false |
| PR4b `TestWatch_BaselineFailure_CleansMapEntry`（**R2 fix #2 regression**）| capture mock 對 first call 回 err；驗證 `Watch` 啟動後 200ms 內 `HasWatcher(target) == false`（goroutine 自己清 entry）|
| PR5 `TestWatch_TopLinesIgnoresBottomChanges` | 注入 only-bottom-line-changed scenario，opts.TopLines=3 不報 changed；opts.TopLines=0（full screen）報 changed |
| PR6 `TestWatch_TmuxErrorTickSkipped` | capture err 在 tick 中發生 → 不 fire ScreenChanged，下一 tick 復原 → 報 ScreenChanged |

---

### 2.2 Slice 2 — ShellPrompt utility 保留（0 LoC + 0 tests）

`probe.LooksLikeShellPrompt(content string) bool` 與 `probe.stripANSI` 改 export（首字大寫），無邏輯變動。Module 層 Slice 3 引用此 utility 把 ScreenStable 細分為 ShellPrompt 場景。

純 visibility 改動，沿用既有 `shell_prompt_test.go`，本 PR 不加 test。

---

### 2.3 Slice 3 — Module orchestrator helper（~60 LoC + 5 tests）

#### 2.3.1 新增 `internal/module/agent/probe_orchestrator.go`

```go
// probeOrchestrator owns the lifecycle of probe watchers per session.
// It interprets ScreenChangeEvent into Status transitions, applies the
// graceWindow + Error Guard, and broadcasts updates.
type probeOrchestrator struct {
    parent *Module  // back-reference for projection / broadcast / activeWatchers
    
    graceMu     sync.Mutex
    lastHookAt  map[string]time.Time  // session → last hook timestamp
}

func newProbeOrchestrator(m *Module) *probeOrchestrator {...}

// startWatch starts a probe watcher for the session, using agent's profile.
// Replaces inline call to m.prober.StartWatch in manageActivityWatch.
// NOTE (R3 fix): orchestrator does NOT touch m.activeWatchers — see
// stopWatch comment. Callers (module.go wrappers) own the activeWatchers
// transitions; orchestrator just resolves profile + invokes prober.Watch.
func (o *probeOrchestrator) startWatch(session, agentType string) {...}

// stopWatch stops the probe watcher for the session.
// NOTE (R3 fix): orchestrator does NOT touch m.activeWatchers — that
// map is owned by Module.manageActivityWatch and renameSessionLocked
// callers, which already serialize updates under m.mu. Keeping
// orchestrator lock-free with respect to m.mu lets renameSessionLocked
// call stopWatch/startWatch while holding m.mu without deadlocking.
func (o *probeOrchestrator) stopWatch(session string) {...}

// recordHookAt records that a hook event was just processed for this session.
// Subsequent screen events within graceWindow are suppressed (hook authority).
func (o *probeOrchestrator) recordHookAt(session string) {...}

// interpretScreenEvent maps a ScreenChangeEvent to a status update.
// Applies graceWindow, Error Guard, projection update, broadcast.
func (o *probeOrchestrator) interpretScreenEvent(session, agentType string, ev probe.ScreenChangeEvent) {...}
```

#### 2.3.2 ProbeProfileProvider optional interface

```go
// ProbeProfileProvider lets agent providers tune probe watch parameters.
// Default profile is used when an agent doesn't implement this interface.
type ProbeProfileProvider interface {
    ProbeProfile() ProbeProfile
}

type ProbeProfile struct {
    TopLines        int  // 0 = use full screen
    IdleStableTicks int  // 0 = default 3
}
```

放 `internal/agent/provider.go`。沿用 `StatusSupporter` / `HookInstaller` 的 optional interface 模式。

#### 2.3.3 default profile（R1 fix #1：opts 結構流入 prober）

定義於 orchestrator package：

```go
var defaultProbeProfile = ProbeProfile{TopLines: 10, IdleStableTicks: 3}
```

orchestrator `startWatch` 邏輯：
```go
profile := defaultProbeProfile
if pp, ok := agentProvider.(agentpkg.ProbeProfileProvider); ok {
    profile = pp.ProbeProfile()
}
opts := probe.WatchOptions{
    TopLines:        profile.TopLines,        // 0 = full screen
    IdleStableTicks: profile.IdleStableTicks, // 0 = default 3 (handled by watchLoop)
}
o.parent.prober.Watch(target, opts, o.makeCallback(session, agentType))
```

#### 2.3.4 stale-callback guard（R4 fix） + graceWindow 解讀

`interpretScreenEvent` 開頭兩個 guard，順序：

**Guard 1 — stale callback 檢查（R4 fix）**：

watch-loop-owned watcher 不再 fire-once-then-exit；callback 多次觸發。stopWatch / rename 都會與 in-flight callback 競態：
- `stopWatch(session)` 後 callback 仍可能跑出來 — `activeWatchers[session]` 已 delete
- `renameSessionLocked` 把 oldName 從 activeWatchers 搬到 newName — oldName 的 callback closure 仍 reference 舊 session 字串

不檢查就 broadcast，會在 watcher 已停 / 已 rename 後對舊 session 廣播狀態（ghost broadcast）。

```go
o.parent.mu.Lock()
currentAgent, active := o.parent.activeWatchers[session]
o.parent.mu.Unlock()
if !active || currentAgent != agentType {
    // Stale callback (post-stop or post-rename); skip.
    // Note: we intentionally do NOT delete from activeWatchers here,
    //       unlike legacy onActivityDetected which fired once + exited.
    return
}
```

**Guard 2 — graceWindow 解讀**：

```go
o.graceMu.Lock()
last, hasHook := o.lastHookAt[session]
o.graceMu.Unlock()
if hasHook && time.Since(last) < probeGraceWindow {
    metrics.MetricProbeGraceWindowSuppressed.Add(1)
    if isDevMode() {
        log.Printf("[probe] graceWindow suppress session=%s agent=%s kind=%s", session, agentType, ev.Kind)
    }
    return
}
```

#### 2.3.5 ScreenStable → Status 解讀

```go
switch ev.Kind {
case probe.ScreenChanged:
    status = StatusRunning
case probe.ScreenStable:
    if probe.LooksLikeShellPrompt(ev.Content) {
        // 保留 legacy 行為：dead PID + shell prompt → sweep
        if projection != nil && projection.TopFrame != nil && !isPidAliveFn(projection.TopFrame.PID) {
            o.parent.sweepOnce()
            return
        }
        status = StatusIdle  // shell prompt 仍 idle，由 sweep 決定 frame 命運
    } else {
        status = StatusIdle
    }
}
```

接著套用 Error Guard + projection update + broadcast（與既有 `onActivityDetected` 同邏輯，整段搬入 helper）。

#### 2.3.6 測試（6 tests — R4 +1 OR6）

| Test | 重點 |
|------|------|
| OR1 `TestOrchestrator_StartWatchUsesAgentProfile` | mock cc agent 回 `{TopLines: 5, IdleStableTicks: 2}`；驗證 prober 收到 lines=5 / stable=2 |
| OR2 `TestOrchestrator_DefaultProfileWhenAgentMissing` | mock agent 不實作 ProbeProfileProvider；驗證 fallback 到 `{TopLines: 10, IdleStableTicks: 3}` |
| OR3 `TestOrchestrator_GraceWindowSuppressesEventWithinWindow` | recordHookAt → 1s 後注入 ScreenChanged；驗證無狀態變化、metrics counter +1 |
| OR4 `TestOrchestrator_GraceWindowExpiresAfterWindow` | recordHookAt → 3s 後注入 ScreenChanged；驗證 StatusRunning 廣播 |
| OR5 `TestOrchestrator_ErrorGuardBlocksProbeOverwrite` | currentStatus=StatusError；ScreenStable 注入；驗證 StatusError 維持、無 broadcast |
| OR6 `TestOrchestrator_StaleCallbackGuard`（**R4 fix regression**）| 兩 sub-case：(a) stopWatch 後注入 ScreenChanged → 無 broadcast、無 metrics；(b) renameSession 前 watch oldName，rename 後注入 oldName 的 ScreenChanged callback → 無 broadcast、無 status 更新（驗證 currentAgent != agentType 比對）|

---

### 2.4 Slice 4 — cc 接新 primitive（~40 LoC + 5 tests）

#### 2.4.1 cc Provider 實作 ProbeProfileProvider

`internal/agent/cc/provider.go`（或新檔 `probe_profile.go`）：

```go
func (p *Provider) ProbeProfile() agentpkg.ProbeProfile {
    return agentpkg.ProbeProfile{
        TopLines:        12,  // cc 螢幕頂部 cluster: ●● header + 任務描述
        IdleStableTicks: 3,
    }
}
```

具體 TopLines 值由實際觀察 cc UI layout 決定；本 plan 暫定 12，allowed to adjust during implementation 並文件化決定。

#### 2.4.2 module.go 改為走 orchestrator（含 R2 fix #1 — 兩條 callsite 一起遷）

`module.go` 有 **兩處** 呼叫 legacy `m.prober.StartWatch`，新 plan 都必須遷到 orchestrator：

1. **`manageActivityWatch`**（module.go:454）— hook event 觸發 status 變化時的 start/stop
2. **`renameSessionLocked`**（module.go:276）— session rename 後重啟 watcher（callback closure 鎖了 oldName，必須 stop 舊 + start 新）

把 `manageActivityWatch` / `onActivityDetected` 改成 thin wrapper：

```go
func (m *Module) manageActivityWatch(session, agentType string, newStatus agentpkg.Status) {
    m.mu.Lock()
    _, wasWatching := m.activeWatchers[session]
    delete(m.activeWatchers, session)
    m.mu.Unlock()
    if wasWatching {
        m.probeOrch.stopWatch(session)
    }
    if shouldWatchActivity(newStatus) {
        m.mu.Lock()
        m.activeWatchers[session] = agentType
        m.mu.Unlock()
        m.probeOrch.startWatch(session, agentType)
    }
}
```

把 `renameSessionLocked` 中的 watcher 段落改成（保留既有 m.mu held + activeWatchers transfer 邏輯）：

```go
// In renameSessionLocked, runs with m.mu held (caller contract):
if agentType, ok := m.activeWatchers[oldName]; ok {
    // activeWatchers transfer (m.mu-protected map mutation)
    delete(m.activeWatchers, oldName)
    m.activeWatchers[newName] = agentType
    // orchestrator calls (R3 fix: lock-free wrt m.mu, safe under hold)
    m.probeOrch.stopWatch(oldName)
    m.probeOrch.startWatch(newName, agentType)
}
```

注意（R3 fix）：
- `activeWatchers` map 的 transfer 由 `renameSessionLocked` 自己做（持 `m.mu` 是其 contract），orchestrator API 不觸 `activeWatchers`，故此處不會 deadlock
- orchestrator `startWatch` / `stopWatch` 內部對 `prober` 的呼叫使用 `prober` 自己的 `watcherMu`（與 `m.mu` 不同 mutex），可重入安全
- orchestrator 內部會處理 prober nil-check（保留 legacy `if m.prober != nil` 行為），rename callsite 不必重複檢查

舊 `onActivityDetected` 整個刪掉（邏輯已搬到 orchestrator.interpretScreenEvent；本 PR 範圍內 cc 走新 path，codex / opencode 也透過 default profile 走同一份 helper — 但 codex / opencode 的 TopN 微調留 PR-4a-2，本 PR 三家共用 default profile = 行為向下相容）。

#### 2.4.3 hook handler 呼叫 recordHookAt

`internal/module/agent/handler.go` 的 hook entry path（具體位置實作時定）：每個有效 hook 處理完前 call `m.probeOrch.recordHookAt(session)`，讓接下來 graceWindow 啟動。

#### 2.4.4 測試（5 tests）

| Test | 重點 |
|------|------|
| CC1 `TestCCProvider_ProbeProfile` | cc.Provider.ProbeProfile() 回 {12, 3}（characterization；implementer 改值需 review）|
| CC2 `TestModule_ManageActivityWatch_RoutesThroughOrchestrator` | mock orch；waiting → start watch called；off → stop watch called |
| CC3 `TestModule_HookHandler_CallsRecordHookAt` | 注入 cc hook event；驗證 orchestrator.lastHookAt[session] 記錄到 |
| CC4 `TestCC_E2E_ScreenChangedToRunning` | cc waiting → orchestrator.startWatch → 注入 ScreenChanged → status 廣播 Running |
| CC5 `TestCC_E2E_ScreenStableToIdle` | cc running → orchestrator.startWatch → 注入 ScreenStable（non-shell-prompt）→ status 廣播 Idle |
| CC6 `TestCC_RenameSession_RestartsWatchViaOrchestrator`（**R2 fix #1 + R3 deadlock-freedom regression**）| cc running on `oldname:` → `RenameSession(oldname, newname)` → 驗證 (a) orchestrator.stopWatch(oldname) + startWatch(newname) 各 call 一次；(b) activeWatchers map key 從 oldname 遷到 newname；(c) 整個操作在 t.Run 內 100ms timeout 完成（deadlock 會 hang，此測試 CI run with `-race -timeout=30s` 也是 fail-loud）|

---

### 2.5 Slice 7 — graceWindow + dev log + expvar（~30 LoC + 4 tests）

實作整合在 Slice 3（同一個 orchestrator）— Slice 7 LoC 含 `internal/agent/metrics.go` 4 個新 expvar + `isDevMode()` helper（package internal/module/agent or internal/agent）+ orchestrator 內 log gating。

#### 2.5.1 expvar 命名（kickoff Decision 7）

```go
// internal/agent/metrics.go (append, do NOT rename existing purdex_phase35_*)
var (
    MetricProbeWatchStarted        = expvar.NewInt("purdex_probe_watch_started_total")
    MetricProbeWatchStopped        = expvar.NewInt("purdex_probe_watch_stopped_total")
    MetricProbeScreenEvent         = expvar.NewInt("purdex_probe_screen_event_total")
    MetricProbeGraceWindowSuppressed = expvar.NewInt("purdex_probe_grace_window_suppressed_total")
)
```

#### 2.5.2 PDX_DEV_MODE log

`internal/module/agent/probe_orchestrator.go` 加 `func isDevMode() bool { return os.Getenv("PDX_DEV_MODE") == "1" }`（或搬到 shared util；可借既有 dev module 的 helper 但避免循環依賴）。

log 點（gated）：
- `recordHookAt` — `[probe] recordHookAt session=%s` （讓觀察者知道 graceWindow 起算）
- `interpretScreenEvent` graceWindow suppress — 已列 §2.3.4
- `interpretScreenEvent` 觸發 status 變化 — `[probe] status session=%s agent=%s status=%s reason=screen-%s`

#### 2.5.3 測試（4 tests）

| Test | 重點 |
|------|------|
| OB1 `TestMetrics_WatchStartedIncrements` | startWatch → counter +1 |
| OB2 `TestMetrics_ScreenEventIncrements` | ScreenChanged + ScreenStable 各注入一次 → counter +2 |
| OB3 `TestMetrics_GraceWindowSuppressedIncrements` | graceWindow hit → counter +1 |
| OB4 `TestDevMode_LogsGatedByEnv` | t.Setenv PDX_DEV_MODE=1 → log buffer 含 `[probe]` line；unset → 無 |

---

## 3. 測試矩陣

| Slice | Test ID 區段 | 數量 | 涵蓋 |
|-------|--------------|------|------|
| 0 | TT1-TT4 | 4 | tmux range API + fake executor parity |
| 1 | PR1-PR2b + PR3-PR4b + PR5-PR6 | 8 | watcher 自治 / Top-N vs full screen / 多次 fire / err tick skip / stable-emit-once（R1 fix #2）/ baseline-fail map cleanup（R2 fix #2）|
| 2 | (沿用既有) | 0 | shell prompt utility（純 visibility）|
| 3 | OR1-OR6 | 6 | profile / graceWindow / Error Guard / **stale-callback guard（R4 fix）**|
| 4 | CC1-CC6 | 6 | cc profile + module wiring + E2E + rename rewatch via orchestrator（R2 fix #1）|
| 7 | OB1-OB4 | 4 | expvar + PDX_DEV_MODE log |

**總計**：28 tests（plan v1.3 §7.1 estimate 24 → +1 PR2b R1 / +1 PR4b R2 / +1 CC6 R2 / +1 OR6 R4 regression tests）。

---

## 4. Commit 順序（TDD）

每個 commit 都先寫測試（red）→ 實作（green）→ 測試 pass。Commit 邊界對齊 Slice 邊界。

### Commit 1 — `feat(tmux): add CapturePaneRange and CapturePaneTopLines`

Slice 0 全部。包含：
- TT1-TT4 全 written first（red）
- `tmux.Executor` interface 擴增 + `RealExecutor` 實作 + fake stub
- `go test ./internal/tmux ./...` 全綠

### Commit 2 — `refactor(probe): extract pure ScreenChangeEvent primitive`

Slice 1 + Slice 2。包含：
- PR1-PR2b + PR3-PR6 全 written first（含 R1 fix #2 regression PR2b）
- 新增 `ScreenChangeEvent` / `ScreenChangeCallback` / `WatchOptions` / `Watch` / `watchLoop`（含 `stableEmitted` flag）
- 移除 `ActivitySignal` / `ActivityCallback` / `activityLoop` / `StartWatch (legacy)` / `hashCapture`（或改名）
- `LooksLikeShellPrompt` / `StripANSI` export
- **Caller 端**：`module.go` 暫時 build error 容忍（下個 commit 修）— 用 `// TODO Slice 3` 註記；或這個 commit 同時把 module.go 改為 stub 呼叫（建議後者，避免 broken intermediate state）

**注意**：本 commit 結束時 `go build ./...` 必須通；test 必須全綠。若無法兩者並立，stub 接 module.go 為 no-op + 標 TODO，commit 訊息中明列。

### Commit 3 — `feat(agent): add ProbeProfileProvider optional interface`

Slice 3 一半（介面 + helper skeleton）：
- OR1-OR2 written first（profile resolution tests）
- `agentpkg.ProbeProfileProvider` + `ProbeProfile` 加入 `internal/agent/provider.go`
- `internal/module/agent/probe_orchestrator.go` skeleton（newProbeOrchestrator + startWatch + stopWatch；interpretScreenEvent 暫不接 graceWindow）
- 接管 module.go 的 `manageActivityWatch` 路徑

### Commit 4 — `feat(probe): graceWindow + screen event interpretation`

Slice 3 後半 + Slice 7 主體：
- OR3-OR5 + OB1-OB4 written first
- orchestrator.interpretScreenEvent 完整邏輯（graceWindow + ScreenStable shellPrompt 分支 + Error Guard + projection + broadcast）
- `internal/agent/metrics.go` 4 個新 expvar
- `isDevMode()` helper + `[probe]` log 點
- `recordHookAt` integration into module.go hook path

### Commit 5 — `feat(agent/cc): adopt new probe primitive via profile`

Slice 4：
- CC1-CC5 written first
- `internal/agent/cc/probe_profile.go`（cc 實作 ProbeProfileProvider）
- module.go 確認 cc path 走 orchestrator + recordHookAt 在 cc hook handler call 到

### Final Verification

- `go test ./... -count=1` 全綠
- `pnpm --prefix spa run lint` / `build`（雖然本 PR 無 SPA 改動，跑一次防回歸）
- expvar 手動驗：`curl http://localhost:7860/debug/vars | jq '. | with_entries(select(.key | startswith("purdex_probe_")))'` 看到 4 個 counter

---

## 5. 不做（boundary）

本 PR 不修改：

- `internal/agent/codex/**`（PR-4a-2 Slice 5）
- `internal/agent/opencode/**`（PR-4a-2 Slice 6）
- `internal/agent/cc/hooks.go` / `hooks_test.go`（不在 probe 範疇）
- `spa/**`（無 SPA 改動）
- `internal/module/dev/**`（PDX_DEV_MODE 已存在；本 PR 只 reuse env var，不改 module）
- `internal/agent/probe/liveness.go` / `readiness.go` / `liveness_test.go` / `readiness_test.go`（本 PR 不動 liveness / readiness 層）
- `internal/agent/probe/shell_prompt.go` 演算法（純 export visibility）

**Allowed 路徑**（boundary script 用）：

```
docs/specs/2026-04-27-lights-rebuild-phase-4a-1-plan.md
internal/tmux/executor.go
internal/tmux/executor_test.go
internal/tmux/fake_executor.go (or equivalent test stub file)
internal/agent/probe/activity.go
internal/agent/probe/activity_test.go
internal/agent/probe/probe.go
internal/agent/probe/probe_test.go (if exists)
internal/agent/probe/shell_prompt.go (export only)
internal/agent/provider.go
internal/agent/cc/probe_profile.go (new)
internal/agent/cc/provider.go (if test seam needed)
internal/agent/metrics.go
internal/module/agent/probe_orchestrator.go (new)
internal/module/agent/probe_orchestrator_test.go (new)
internal/module/agent/module.go (manageActivityWatch / onActivityDetected refactor)
internal/module/agent/module_test.go
internal/module/agent/handler.go (recordHookAt wiring)
internal/module/agent/handler_test.go
scripts/check-pr-4a-1-boundary.sh (new)
```

`scripts/check-pr-4a-1-boundary.sh` 沿用 PR-4a-0 三-dot diff 模板，only 換 ALLOWED 清單。

---

## 6. Ship gate

| Gate | 條件 |
|------|------|
| G1 unit | `go test ./... -count=1` 全綠 |
| G2 boundary | `scripts/check-pr-4a-1-boundary.sh origin/main` 退出碼 0 |
| G3 expvar | 啟動 daemon 跑一次任意 cc session，`/debug/vars` 看到 4 個 `purdex_probe_*` counter（手動驗 — 文件化於 PR description）|
| G4 dev log | `PDX_DEV_MODE=1` 啟動跑一次 graceWindow hit，stderr 看到 `[probe]` log；unset 時無 log（手動驗 — 文件化於 PR description）|
| G5 default profile parity | 不實作 ProbeProfileProvider 的 agent（codex / opencode）行為與 PR-4a-1 前等價（透過 `TestOrchestrator_DefaultProfileWhenAgentMissing` + cc / codex / opencode 三家既有 module integration tests 通過驗證）|
| G6 watcher 不洩漏 | `TestModule_StopWatch_ClearsActiveWatchers` 驗證 manageActivityWatch(off) 後 HasWatcher false（regression — 既有測試應已涵蓋；本 PR 確認無回歸）|

---

## 7. Risk

| Risk | Mitigation |
|------|------------|
| `manageActivityWatch` 重構引入 race（StopWatch 與 callback fire 競態）| watch-loop-owned 設計（kickoff Decision 13）+ orchestrator stopWatch 顯式 cancel；callback 在 ctx done 後不再 fire；OR3 + 既有 module concurrent test 覆蓋 |
| graceWindow 太短 / 太長 — hook 與 probe 互相覆蓋或漏報 | 沿用 spec §2.4 cited 2s；orchestrator API 內部寫死，PR-4a-1 後依 PDX_DEV_MODE log 觀察決定是否做成 const-overridable；本 PR 不暴露為 config |
| cc TopLines = 12 不適配（cc UI layout 變動）| Slice 4 文件 expects implementer 跑一次 cc 互動測試確認 captured Top-N 含 ●● header；不適配時 plan 草稿允許 implementer 修改值並在 commit message 紀錄理由 |
| 既有 `purdex_phase35_*` 與新 `purdex_probe_*` 並存看似冗餘 | kickoff Decision 7 已決議「新增不 rename」；保留歷史連續性；docs/specs 中文件化兩組 metric 用途差異即可 |
| `LooksLikeShellPrompt` export 後外部誤用 | 寫 godoc 註明「intended for module/agent layer; do not use from non-probe-aware code」；無 enforcement，依 review |
| `manageActivityWatch` 改動觸動三家 module integration tests | 沿用 default profile 確保 codex / opencode 行為向下相容；本 PR 不切換它們的 agent profile，留 PR-4a-2 |
| 主 repo 並發 session（feedback_concurrent_session_safety）| 進 worktree 前 `git status -s` 必 clean；`git fetch origin main && git reset --hard origin/main`；輸出貼入 PR description 留痕（沿用 PR-4a-0 H6.5 慣例）|
| Codex sandbox 無網路（feedback_codex_sandbox_no_install）| 主 Claude 必須手動跑 go test + lint + build；codex review 不取代驗證 |

---

## 8. LOC 預估

| Slice | 估 LoC | 估 tests |
|-------|--------|----------|
| 0 tmux API | ~40 | 4 |
| 1 probe primitive | ~90 | 8（R1 +PR2b / R2 +PR4b）|
| 2 shell prompt export | ~5 | 0 |
| 3 module orchestrator | ~85 | 6（R4 +OR6）|
| 4 cc adoption | ~50 | 6（R2 +CC6 rename）|
| 7 graceWindow + dev log + expvar | ~30 | 4 |
| (extra) boundary script | ~30 | 0 |

**總計**：~330 LoC + 28 tests（plan v1.3 §7.1 estimate 24 → R1 +1 / R2 +2 / R4 +1，總 +4 regression tests + R2 baseline-fail cleanup + rename callsite migration + R4 stale-callback guard ~20 LoC，仍在 PR 合理 size 內）。

**屬中型 PR**。`go test` 預期 elapsed < 30s 內。

---

## 9. 結束條件（PR-4a-1）

**Ship**：

- §6 ship gate G1-G6 全通過
- PR merged
- 對應 main bump PR ship（VERSION 進到 alpha.234 或 235，視 bump 排程）
- `kickoff_lights_rebuild.md` 更新觸發詞為 `啟動 PR-4a-2`
- `project_progress.md` 補 alpha.X PR-4a-1 milestone

**手動驗收**：
- 啟動 daemon `PDX_DEV_MODE=1 ./bin/pdx`，跑一次 cc session 互動，stderr 觀察 `[probe]` log 至少出現 startWatch / screenChanged / screenStable 三類 line
- `/debug/vars` 包含 4 個 `purdex_probe_*` counter

---

## 10. 文獻

- `docs/specs/2026-04-23-lights-rebuild-spec.md`
- `docs/specs/2026-04-26-lights-rebuild-phase-4a-plan.md` v1.3 §7
- `docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md` §6（Slice 6 Go verdict）
- kickoff_lights_rebuild Decision 1/2/4/6/7/8/13
- PR-4a-0（[#664](https://github.com/wake/purdex/pull/664)）為設計典範對照
