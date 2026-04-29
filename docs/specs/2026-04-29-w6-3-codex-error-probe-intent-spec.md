# W6-3 codex error ad-hoc ProbeIntent spec

> **Status**：draft（待 codex review）
> **Worktree**：`.claude/worktrees/lights-w6-3-codex-error` / branch `worktree-lights-w6-3-codex-error`
> **Base**：`origin/main` @ alpha.260（W3+W4 reverted ProbeProfile framework + dev log baseline）
> **依賴**：W1 audit `docs/specs/2026-04-28-hook-status-audit-spec.md` §6/§7 / lights-rebuild-spec `docs/specs/2026-04-23-lights-rebuild-spec.md` §8.2 / fix-spec `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3
> **後續**：W6-4 codex clear（沿用本 spec finalize 的 interface）/ W6-1/2/6 cc + codex TUI 觀察（新增 ScreenChange detector kind 時 generalize）

---

## 0. 來龍去脈

**直接動因**（W1 audit §6 W5-4 + §7 W6-3）：

codex CLI 0.124.0 不主動發 `StopFailure` hook（catalog FutureOnly = 5 entries 之一，cf. `internal/agent/codex/events.go:11-15`）。當 codex 進程 crash / kill / 異常退出時，user 看到 codex 不在了但 lights 不變紅 — `error` status 物理上不可達。

**lights-rebuild-spec §8.2 既定方向**：probe 出場條件**從寫死擴充為 agent 驅動**。每個 W6 缺口由對應 agent provider 透過 `ProbeIntentProvider` interface 宣告，detector 實作歸 `internal/agent/{cc,codex,opencode}/probe_intent_*.go`，daemon module 只負責 plumbing。

**W6-3 是 W6 系列的第一個 PR**（per audit §7.2 推薦順序）— 藉此 finalize ProbeIntent interface shape，後續 W6-4 / W6-1/2/6 沿用。

### 0.1 Pre-W6-3 baseline

W3 撤回後（alpha.260）的現況：

| 元素 | 現況 |
|---|---|
| `internal/agent/provider.go` | `AgentProvider` core + `HookInstaller` / `StatusSupporter` / 等 optional capabilities；**無** `ProbeIntentProvider` |
| `probeOrchestrator.startWatch(session, agentType, opts)` | 已 ship；caller 提供 `probe.WatchOptions`；目前**無 production caller**（W3 撤掉 always-on） |
| `manageActivityWatch(session, agentType, newStatus)` | 退化為 stop-only no-op；`agentType` / `newStatus` 參數保留給 W6 用 |
| `replayFromDB()` | 重建 `currentStatus` / `subagents` / frame projection；**不**重啟 watcher（issue #698） |
| Hook payload `sender_pid` | 已強制要求（`internal/module/agent/handler.go:145`：`req.SenderPID == 0` 視為 invalid） |
| `probe.IsPidAlive(pid)` / `probe.Prober.IsAliveFor(agentType, target)` | 已存在；polling-based |
| `probe.ScreenChangeWatcher` (`probe.Watch / WatchOptions / ScreenChangeEvent`) | 已 ship；**只**支援 tmux pane content 變化觀察 — **不**支援 process-exit 觀察 |
| Trace dev log（W4） | `[hook] / [derive] / [handler] / [broadcast]` 5 條 chain log；W6-3 將新增 `[probe-intent]` step kind |

### 0.2 重要 spec drift 修正（與 W1 audit §7 假設不符）

**audit §7 W6-3 寫**：

> codex provider 宣告 ProbeIntent；detector 觀察 codex 進程 exit code（非零）；signal `process_error_exit` → status=error。**約束**：…first PR 僅 process-exit + crash detection（**含 exit code**），TUI / stderr 偵測為延伸 PR

**Platform reality**：daemon 不是 codex 子程序（codex 由 user 在 tmux pane 手動啟動）。Unix 設計上：

- `wait4` / `waitpid` 只對 child PID 有效
- macOS `kqueue EVFILT_PROC + NOTE_EXIT|NOTE_EXITSTATUS` — `NOTE_EXIT` 對 non-child 仍能收事件，但 `NOTE_EXITSTATUS` 取得的 exit code **僅對 child 可信**
- `/proc/<pid>/stat` exit_state — Linux 才有，macOS daemon 環境不適用
- 用 `ps` poll exit status — 進程消失後 `ps` 無紀錄

**結論**：daemon 觀察 non-child codex 進程**只能取得 alive/dead binary signal**，無法區分 exit code 0 vs ≠ 0。

**因此 W6-3 first PR signal 從 `process_error_exit (含 exit code)` 修正為 `process_dead (binary)`**。`error` vs `clear` 的區分改用**伴隨觀察**（tmux pane 是否仍存在）：

| 伴隨觀察 | 語意推論 | Status |
|---|---|---|
| `process_dead + tmux_pane_alive` | codex 在 pane 內崩潰 / 被 kill / 異常退出，pane 回到 shell prompt | `error` |
| `process_dead + tmux_pane_gone` | user 主動關閉 pane / window，連帶 codex 退出 | `clear` |

> **W6-3 vs W6-4 拆分權衡**（spec drift signal #1，待 codex review 決議）
>
> 由於兩者共用 detector，「合併 first PR」也是合理選項。本 spec 預設**遵守 audit 拆分**：W6-3 first PR 只實作 `process_dead → error`（不觀察 pane existence）；W6-4 follow-up 加入 pane 觀察區分。
>
> Trade-off：W6-3 first PR scope 簡單但會誤標（user 主動退 codex 也標 error）。詳 §9.1。

### 0.3 與 fix-spec / W1 audit 對齊

| 設計要點 | 來源 | 落地處 |
|---|---|---|
| ProbeIntent 走 `ProbeIntentProvider` interface（signal → status mapping） | lights-rebuild-spec §8.2 / audit §7.1 | 本 spec §3 |
| 不偽裝為 hook event（probe 與 hook 是兩條獨立 channel） | audit §7.1 | 本 spec §3 / §5 |
| 不做跨 agent 中央 liveness watcher / 中央規則 | audit §7.1 / fix-spec §3 | 本 spec §3 / §5 |
| 不做 always-on probe / generic ProbeProfileProvider | audit §7.1 / fix-spec §3 | 本 spec §5（per-agent gating + 條件式 watch） |
| Detector 歸 `internal/agent/codex/probe_intent_*.go` | audit §7.1 | 本 spec §4 |
| daemon module 只負責 plumbing | audit §7.1 | 本 spec §5 |
| ProbeIntent interface lazy 設計（W6-3 finalize） | audit §7.1 | 本 spec §3.4 |
| 五大 bloat 徵兆 self-check | audit §7.1.3 / `feedback_skeleton_convergence` | 本 spec §2.2 |
| daemon-restart watcher recovery (issue #698) | audit §7.1.2 | 本 spec §6 |

---

## 1. 範圍與目標

### 1.1 在範圍

1. `ProbeIntentProvider` 新 optional interface 落 `internal/agent/provider.go`
2. `ProbeIntent` / `ProbeIntentKind` / `Signal` 結構 finalize（lazy；first PR 唯一 Kind = `ProcessDead`）
3. codex provider 宣告 `ProcessDead` ProbeIntent → `error`
4. codex `probe_intent_process_dead.go` detector：polling + `IsPidAlive(senderPID)` 雙檢查
5. 新檔 `internal/module/agent/probe_intent_dispatcher.go`：per-agent ProbeIntent gating + watcher lifecycle plumbing
6. `manageActivityWatch` 改造：接 ProbeIntent 啟停（per-agent，**不**做跨 agent 規則）
7. SenderPID 持久化 + daemon-restart 後 ProbeIntent watcher recovery（issue #698）
8. drift test：每 ProbeIntent 宣告 vs runtime 實際 dispatch 路徑對齊
9. dev log 補：`[probe-intent]` step kind（W4 trace pipeline 第 6 條 chain log）
10. mlab live verify：codex 進程被 kill → lights 變 error

### 1.2 不在範圍

- W6-4 codex clear（pane 觀察區分 error/clear；本 spec 預設拆 follow-up PR — 詳 §9.1）
- W6-1/2/6 cc + codex TUI 觀察（不同 detector kind，需新增 `ScreenChange` Kind 時再 generalize）
- W6-5 opencode busy/retry（首選 plugin 補 mapping，issue #661）
- Always-on probe 復辟（W3 已撤；本 spec 嚴禁）
- ScreenChange watcher 改造（W6-3 純 process_dead detector，與 ScreenChange watcher 平行 lifecycle）
- TraceStore schema 改動（dev log 走現有 trace pipeline 加 step kind 即可）
- Inspector UI（W7 範圍）

### 1.3 為何要做

- W5-4 燈號 bug：codex error 物理不可達是 user-visible 缺口（codex 異常退出時 lights 不變紅）
- 為 W6 系列鋪 interface — W6-3 first PR finalize 讓 W6-4 / W6-1/2/6 直接沿用
- 為 #698 daemon-restart 場景補 platform plumbing — 否則 W6 ProbeIntent 在 daemon 重啟後直到下個 hook 才重新掛，多數 W5/W6 ship 後 daemon-restart 體驗破功

---

## 2. 設計約束

### 2.1 必守

1. ✅ ProbeIntent 由 codex provider 透過 `ProbeIntentProvider` 宣告；detector 實作歸 `internal/agent/codex/`
2. ✅ daemon module 只負責 plumbing（dispatcher / watcher lifecycle / hook → status → broadcast）
3. ✅ ProbeIntent gating 為條件式（status ∈ {running, waiting} 且 senderPID 已知時 watch；否則 unwatch）
4. ✅ Interface lazy 設計 — W6-3 finalize 唯一 `ProcessDead` Kind；後續 PR 加新 Kind 時 extend struct
5. ✅ probe channel 與 hook channel 獨立 — probe 推論、hook 權威；`recordHookAt` graceWindow 機制保留
6. ✅ ErrorGuard 維持：`currentStatus == StatusError` 時 probe 不再覆寫
7. ✅ Stale-callback guard：watcher callback 收到事件時必須 re-check `activeWatchers[session] == agentType`，防 rename / agent-swap 後的 ghost broadcast

### 2.2 禁忌

1. ❌ probe 偽裝為 hook event（**不要**寫「fire 合成 StopFailure-equivalent」之類的描述）
2. ❌ 跨 agent 中央 liveness watcher / 中央規則（如 generic `any → process_dead → error`）
3. ❌ generic `ProbeProfileProvider` / always-on policy（fix-spec §3 已撤；本 spec 不重蹈）
4. ❌ 把 codex hook handler 既有 working code 改寫成 ProbeIntent 形式（refactor working code without functional reason）
5. ❌ parallel registry（除 codex provider 已有的 `ProbeIntents()` 之外不另起 registry）
6. ❌ 為「未來可能有」的 detector kind 預先抽象（lazy；只有 ProcessDead 一個 kind）

### 2.3 五大 bloat 自我檢查（per `feedback_skeleton_convergence`）

每個 PR commit 前 self-check：

| 徵兆 | W6-3 self-check |
|---|---|
| 把 working code 變 data | 不動 codex `events.go` / `status.go` / `hooks.go`；ProbeIntent 是新增 capability，不重寫既有 hook 路徑 |
| parallel registry | 不加新 registry；ProbeIntent 由 provider 宣告 → dispatcher 在 status 變更時讀取 |
| 統一抽象（generic framework） | `ProbeIntentKind` 只有 `ProcessDead` 一個 const；不預定義 `ScreenChange` / `LogTail` 等 |
| refactor working code | 不重寫 ScreenChangeWatcher / probeOrchestrator；新增獨立 `probe_intent_dispatcher.go` plumbing |
| config flag | 不加 `PDX_PROBE_INTENT_ENABLED` 之類的 flag；alpha 階段直接 ship |

任一冒出 → 停手 surface（per fix-spec §7）。

---

## 3. ProbeIntent 模型 finalize

### 3.1 `ProbeIntentProvider` interface（新 optional capability）

```go
// internal/agent/provider.go (附加；不動既有 capabilities)

// ProbeIntentProvider declares probe-driven status transitions for an agent.
//
// Probe is a recovery channel that complements (not replaces) hooks: when an
// agent's hook catalog has a structural gap (e.g. codex StopFailure being
// FutureOnly = ✗ 0.124.0 不發), the agent's provider declares one or more
// ProbeIntents whose detectors infer the missing transition from runtime
// observation (process liveness, tmux pane content, etc).
//
// Implementing this interface is optional. Providers without ProbeIntents
// behave identically to pre-W6-3 (no probe-driven transitions).
//
// Daemon module reads ProbeIntents() on every status change for the session's
// agent and starts/stops detectors accordingly. Detectors live in the agent's
// own package (e.g. internal/agent/codex/probe_intent_*.go) — module only
// owns the dispatcher plumbing.
type ProbeIntentProvider interface {
    ProbeIntents() []ProbeIntent
}
```

### 3.2 `ProbeIntent` struct（lazy；W6-3 finalize）

```go
// ProbeIntent is one probe-driven transition declared by an agent provider.
//
// Lifecycle (driven by daemon dispatcher):
//   1. Status changes to a value listed in OnEntryStatus → dispatcher calls
//      Detector.Start(ctx) on the per-agent detector instance
//   2. Detector observes runtime state and emits Signal events on a channel
//   3. Dispatcher converts Signal → Status via OnSignal and broadcasts
//   4. Status changes to a value NOT in OnEntryStatus → dispatcher stops
//      detector (cancel ctx) and frees per-session state
//
// W6-3 first PR has one Kind: ProbeIntentKindProcessDead.
type ProbeIntent struct {
    // Kind classifies the detector. W6-3 finalize: only ProbeIntentKindProcessDead.
    // Subsequent W6 PRs MAY introduce additional Kind constants; existing Kind
    // semantics MUST remain stable.
    Kind ProbeIntentKind

    // OnEntryStatus is the set of currentStatus values that gate this intent
    // active. Detector starts on entry to any of these and stops on exit to any
    // status outside the set.
    //
    // Example (W6-3): {StatusRunning, StatusWaiting} — codex error inference
    // only makes sense while codex is supposed to be doing work.
    OnEntryStatus []Status

    // OnSignal maps a detector signal to the new Status. Empty Status returned
    // by OnSignal means "drop this signal" (detector observed transient state
    // that doesn't warrant a transition).
    //
    // dispatcher applies the same guards as ScreenChangeWatcher
    // (interpretScreenEvent in probe_orchestrator.go): graceWindow / ErrorGuard
    // / transition gate.
    OnSignal func(Signal) Status
}

// ProbeIntentKind is the detector category. W6-3 finalize introduces one Kind;
// future PRs MAY add more. Use a string type so test fixtures can declare
// expected kinds without import cycles.
type ProbeIntentKind string

const (
    // ProbeIntentKindProcessDead — detector polls senderPID via probe.IsPidAlive
    // and fires when the agent process is no longer in the tmux pane PID tree.
    ProbeIntentKindProcessDead ProbeIntentKind = "process_dead"
)

// Signal is the runtime observation emitted by a detector. The struct is
// intentionally minimal in W6-3 — future Kind additions extend by adding
// optional fields (existing fields MUST remain backward compatible).
type Signal struct {
    Kind ProbeIntentKind

    // Reserved: future Kind variants populate richer payload here. W6-3
    // ProcessDead carries no extra payload — the signal itself is the
    // observation.
}
```

**Lazy 設計理由**（per audit §7.1 / `feedback_skeleton_convergence`）：

- `ProbeIntent` 只設計 W6-3 + W6-4 都會用的 `OnEntryStatus` / `OnSignal` 兩欄位
- `Signal` 預留結構但只有 `Kind` 欄位 — 不預先填 `Payload struct { ... }`
- `ProbeIntentKind` 只有一個 const — 後續 PR extend 時加 const 即可
- 不抽 `Detector` interface — 每 Kind 一個 detector goroutine，dispatcher 用 switch on Kind 啟停（switch 範圍小，比 interface 抽象更直白）

### 3.3 First PR 範圍：唯一 Kind = `ProcessDead`

W6-3 only：

- `ProbeIntentKindProcessDead`
- detector：codex 實作 `probe_intent_process_dead.go`
- 觀察：tmux pane PID tree 是否仍含 codex 進程（透過 `probe.Prober.IsAliveFor("codex", target)`，已有方法）
- gating：`OnEntryStatus = {StatusRunning, StatusWaiting}`
- mapping：`OnSignal = func(Signal) { return StatusError }`

W6-4 follow-up（不在 W6-3 PR 範圍）：

- 同 `ProcessDead` Kind，OnSignal 改為依 pane existence 區分 error/clear
- 需要時 `Signal.Payload` 增 `PaneAlive bool`（**或** dispatcher 直接傳 pane 觀察結果為第二參數；W6-4 設計時決定）

---

## 4. codex provider 實作

### 4.1 ProbeIntent 宣告（`internal/agent/codex/provider.go` 附加）

```go
func (p *Provider) ProbeIntents() []agent.ProbeIntent {
    return []agent.ProbeIntent{
        {
            Kind:          agent.ProbeIntentKindProcessDead,
            OnEntryStatus: []agent.Status{agent.StatusRunning, agent.StatusWaiting},
            OnSignal:      onProcessDead,
        },
    }
}

// onProcessDead maps a ProcessDead signal to the recovery status. W6-3 first
// PR returns Error unconditionally; W6-4 will refine to (Error | Clear) based
// on tmux pane existence observation.
func onProcessDead(sig agent.Signal) agent.Status {
    if sig.Kind != agent.ProbeIntentKindProcessDead {
        return ""  // dispatcher should not invoke OnSignal for mismatched Kind
    }
    return agent.StatusError
}
```

### 4.2 Detector 機制（`internal/agent/codex/probe_intent_process_dead.go`，新檔）

**Polling-based detection**（不用 platform-specific kqueue / pidfd）：

- daemon 持有 codex senderPID（per-session）
- detector goroutine 每 `processDeadPollInterval` 醒來檢查：
  1. `probe.IsPidAlive(senderPID)` — false 即視為 dead（基本快檢）
  2. （補強）`prober.IsAliveFor("codex", target)` — 整個 tmux pane PID tree 都不含 codex 進程才確認 dead（防 senderPID 是 short-lived 子進程的誤判）
- dead 確認後 emit signal 一次，detector 結束（dispatcher 已停 watcher，不再 poll）

**Polling 頻率**：建議 `1 * time.Second`。理由：

- codex crash 復原需求對 latency 不敏感（user 看到 codex 不在了，等 1s 變紅可接受）
- 1Hz × pane PID tree 查詢成本（`ps -Ao pid=,ppid=` + descendant cache 250ms TTL）：每秒 1 個 ps 子進程，可忽略
- 比 ScreenChangeWatcher 預設 200ms tick 慢 5×，符合「probe 是 recovery」精神（不搶 hook）

**Detector signature**（與 dispatcher 約定）：

```go
// probe_intent_process_dead.go
package codex

import (
    "context"
    "time"

    "github.com/wake/purdex/internal/agent"
    "github.com/wake/purdex/internal/agent/probe"
)

// processDeadPollInterval is the polling cadence for IsPidAlive + IsAliveFor.
// Exported as a package var so tests can override (see *_test.go) — production
// code never mutates it.
var processDeadPollInterval = 1 * time.Second

// ProcessDeadDetector is the codex-side detector for ProbeIntentKindProcessDead.
// It polls the agent module's prober + senderPID; on first dead-confirmed tick
// it emits one Signal and returns. Cancel ctx to stop early (e.g. on session
// rename / status exit OnEntryStatus).
//
// prober is *probe.Prober (production) or a test fake satisfying probeIntentProber.
// target is the tmux target with ":" suffix (e.g. "mySession:") matching the
// ScreenChangeWatcher convention.
//
// Caller is expected to dispatch the emitted Signal to OnSignal and apply the
// usual probe guards (graceWindow / ErrorGuard / transition gate).
func StartProcessDeadDetector(
    ctx context.Context,
    prober probeIntentProber,
    target string,
    senderPID int,
    out chan<- agent.Signal,
) {
    ticker := time.NewTicker(processDeadPollInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if probe.IsPidAlive(senderPID) {
                continue
            }
            // senderPID is gone; double-check the tmux pane tree to avoid
            // false-positive on senderPID being a short-lived child (e.g.
            // codex helper script) that exited but parent codex is alive.
            if prober.IsAliveFor("codex", target) {
                continue
            }
            select {
            case out <- agent.Signal{Kind: agent.ProbeIntentKindProcessDead}:
            case <-ctx.Done():
            }
            return
        }
    }
}

// probeIntentProber is the minimal contract this detector requires from
// internal/agent/probe.Prober. Tests inject a recording fake; production wires
// the real *probe.Prober.
type probeIntentProber interface {
    IsAliveFor(agentType, target string) bool
}
```

### 4.3 SenderPID 來源

**hook payload SenderPID** 已在 `EventRequest`（`internal/module/agent/handler.go:91`）+ 強制 validation（line 145：`req.SenderPID == 0` → invalid 400）。daemon 處理每筆 hook event 時：

1. 在 handler.go 主路徑 store senderPID 進 module 新欄位 `senderPIDs map[string]int` (session → pid)
2. **同時**寫入 frame projection（DB 持久化，給 #698 daemon-restart recovery 用）
3. 後續 codex hook 抵達時 update senderPID（user 重新啟 codex 會給新 PID）

**選項 A（推薦）**：在 `frames` table 加欄位 `last_sender_pid INTEGER`
**選項 B**：另起 in-memory map + 不持久化（daemon-restart 後 senderPID 缺失，等下個 hook 重 hydrate；不滿足 #698）

預設選 **A** — 詳 §6 daemon-restart recovery。

### 4.4 Detector lifecycle（與 dispatcher 互動）

| 觸發 | 動作 |
|---|---|
| status enter `running` 或 `waiting`，且 senderPID 已知 | dispatcher 啟 detector goroutine（context cancel 在手） |
| status exit OnEntryStatus（變 idle / error / clear） | dispatcher cancel ctx，detector 收到 `<-ctx.Done()` 退出 |
| session rename | renameSessionLocked 呼叫 dispatcher.stopFor(oldName) + startFor(newName)（若新 status 仍在 OnEntryStatus） |
| daemon stop | Module.Stop 呼叫 dispatcher.stopAll |
| senderPID 缺失（罕見：hook 缺欄位被 handler reject 在前；replay 後 DB 無 last_sender_pid） | dispatcher skip 啟 detector；下個 hook 帶來 PID 後重新觸發 |

---

## 5. Module plumbing

### 5.1 新檔 `internal/module/agent/probe_intent_dispatcher.go`

職責：

- 持有 per-session detector goroutine 集合（`map[session]map[ProbeIntentKind]context.CancelFunc`）
- 在 status 變更時讀取 provider.ProbeIntents()，啟停對應 detector
- detector 透過 channel emit Signal → dispatcher 走 OnSignal → 套 guards（graceWindow / ErrorGuard / transition gate） → broadcast
- 不直接 mutate `m.activeWatchers`（保留給 ScreenChangeWatcher）；新增 `m.activeProbeIntents map[session]map[Kind]ctxCancel`

**為何不 reuse `probeOrchestrator`**：

- `probeOrchestrator` 與 `ScreenChangeWatcher` 緊耦合（`startWatch(session, agentType, opts probe.WatchOptions)` 簽名、`makeCallback` 走 `probe.ScreenChangeCallback` 等）
- ProcessDead detector 與 ScreenChange 是兩種獨立 mechanism — 強行整進 `probeOrchestrator` 會引入「unify abstraction」bloat（§2.3）
- 共用部分（graceWindow / ErrorGuard / transition gate）抽為 free function `applyProbeGuards(m, session, agentType, status) (Status, bool)`，在兩個 dispatcher 間共用

### 5.2 `manageActivityWatch` 改造

退化為 stop-only no-op 的版本（W3 撤後）改為：

```go
func (m *Module) manageActivityWatch(session, agentType string, newStatus agentpkg.Status) {
    // 1. 既有：停 ScreenChangeWatcher（ScreenChange 在 W6-3 仍無 caller，no-op 保留）
    m.mu.Lock()
    _, wasWatching := m.activeWatchers[session]
    delete(m.activeWatchers, session)
    m.mu.Unlock()
    if wasWatching {
        m.probeOrch.stopWatch(session)
    }

    // 2. 新：dispatch ProbeIntent 啟停（per-agent gating）
    if m.probeIntentDisp != nil {
        m.probeIntentDisp.applyStatus(session, agentType, newStatus)
    }
}
```

`applyStatus` 內部：

```go
func (d *probeIntentDispatcher) applyStatus(session, agentType string, newStatus agent.Status) {
    provider, ok := d.parent.registry.GetByType(agentType)
    if !ok { return }
    intents := probeIntentsOf(provider)  // 走 type assert ProbeIntentProvider
    for _, intent := range intents {
        wasActive := d.isActive(session, intent.Kind)
        shouldActive := slices.Contains(intent.OnEntryStatus, newStatus)
        switch {
        case !wasActive && shouldActive:
            d.startDetector(session, agentType, intent)
        case wasActive && !shouldActive:
            d.stopDetector(session, intent.Kind)
        }
    }
}
```

### 5.3 watcher lifecycle / Signal 處理

```go
func (d *probeIntentDispatcher) startDetector(session, agentType string, intent agent.ProbeIntent) {
    senderPID, ok := d.parent.lookupSenderPID(session)
    if !ok || senderPID == 0 {
        return  // 等下個 hook 帶 PID，applyStatus 會再次嘗試
    }
    ctx, cancel := context.WithCancel(d.parentCtx)
    out := make(chan agent.Signal, 1)
    d.recordActive(session, intent.Kind, cancel)

    go func() {
        switch intent.Kind {
        case agent.ProbeIntentKindProcessDead:
            codex.StartProcessDeadDetector(ctx, d.parent.prober, session+":", senderPID, out)
        // future Kind: 加 case
        }
        close(out)
    }()
    go d.consumeSignals(ctx, session, agentType, intent, out)
}

func (d *probeIntentDispatcher) consumeSignals(ctx context.Context, session, agentType string, intent agent.ProbeIntent, in <-chan agent.Signal) {
    for sig := range in {
        newStatus := intent.OnSignal(sig)
        if newStatus == "" { continue }
        // applyProbeGuards: graceWindow / ErrorGuard / transition gate / stale-callback re-check
        // 與 probeOrchestrator.interpretScreenEvent 同邏輯；抽 free function 共用
        if d.parent.applyProbeGuards(session, agentType, newStatus, "probe-intent:"+string(intent.Kind)) {
            // applyProbeGuards 內部已 setProjectionTopStatus + broadcast
        }
    }
}
```

### 5.4 Polling 頻率 / grace window

- ProcessDead polling：1Hz（4.2 已述）
- graceWindow：沿用既有 `probeGraceWindow = 2s`（hook 後 2s 內 probe signal 全 drop）
- `applyProbeGuards` free function 抽 `probe_orchestrator.go interpretScreenEvent` 的 step 1+2+4+5+6（stale guard / graceWindow / ErrorGuard / transition gate / broadcast），讓 ProcessDead 與 ScreenChange 兩條 dispatcher 共用

---

## 6. Daemon-restart watcher recovery (issue #698)

### 6.1 問題

`Module.Start()` (`internal/module/agent/module.go:223`) 呼叫 `replayFromDB` 重建 `currentStatus` / `subagents` / frame projection，但**不**呼叫 `manageActivityWatch` 重啟任何 watcher。所有 W6 ProbeIntent 在 daemon 重啟後**直到下個 hook 才會重新掛**。

對 W6-3 的影響：user 在 daemon 重啟前 codex 是 running 狀態，daemon 重啟後若 codex crash，**沒有 ProbeIntent watcher**會發現，user 看不到 lights 變紅。

### 6.2 SenderPID 持久化

`frames` table 加欄位 `last_sender_pid INTEGER`：

- 每次 hook handler 處理 EventRequest 時 update：
  ```sql
  UPDATE frames SET last_sender_pid = ?, ... WHERE tmux_session = ?
  ```
- replayFromDB 時 read 出來填回 `m.senderPIDs[session]`

DB schema：alpha 階段直接 alter（per `feedback_no_alpha_migration` — 不用 migration script，直接改 `internal/store/frame.go` schema）。

### 6.3 Replay 後重新啟動 dispatcher

`Module.Start()` 流程加一步：

```go
func (m *Module) Start(_ context.Context) error {
    if err := m.sweepOnce(); err != nil { ... }
    m.replayFromDB()         // 重建 currentStatus + senderPIDs + frame projection
    m.startSweep()
    // 新：根據 replay 後的 currentStatus 重新評估 ProbeIntent gating + 啟動 detector
    m.probeIntentDisp.replayStatus()
    ...
}

func (d *probeIntentDispatcher) replayStatus() {
    snapshot := d.parent.snapshotStatuses()  // session → (agentType, status)
    for session, entry := range snapshot {
        d.applyStatus(session, entry.agentType, entry.status)
    }
}
```

### 6.4 Race / 一致性

- `applyStatus` 已是 dispatcher 公開介面（hook 路徑也走它）— 重用即可，不需另起新方法
- replay 順序：先 `replayFromDB` 把 senderPIDs / currentStatus 都 hydrate 完，再 `replayStatus()`，避免 detector 啟動時 lookupSenderPID miss
- 若 replay 後某 session 的 senderPID 真的缺失（DB 欄位 NULL，老資料），`startDetector` skip — 等下個 hook 補

---

## 7. Phase 拆分

### Phase 1 — Interface finalize + module dispatcher + #698 recovery

**目標**：建立 `ProbeIntentProvider` interface + module dispatcher plumbing + DB schema 升級 + replay recovery；codex provider **暫**用 stub detector（永不 emit signal，純測 dispatcher lifecycle）。

**Tasks**（每 task 獨立 commit；TDD）：

| ID | 內容 |
|---|---|
| P1-T1 | `ProbeIntent` / `ProbeIntentKind` / `Signal` / `ProbeIntentProvider` 落 `internal/agent/provider.go` + 單元測試 |
| P1-T2 | `frames` 表加 `last_sender_pid INTEGER`；schema migration（alpha：直接改）+ store layer test |
| P1-T3 | handler.go 路徑 store senderPID 進 `m.senderPIDs` + DB；replayFromDB 補 hydrate；test |
| P1-T4 | 新檔 `internal/module/agent/probe_intent_dispatcher.go`：`applyStatus` / `startDetector` / `stopDetector` / `replayStatus` / `consumeSignals`；用 stub detector 測 lifecycle |
| P1-T5 | `applyProbeGuards` free function 抽出（`probe_orchestrator.go` 的 stale guard / graceWindow / ErrorGuard / transition gate / broadcast），ScreenChange + ProbeIntent 兩 dispatcher 共用；regression test 確保 ScreenChange 行為不變 |
| P1-T6 | `manageActivityWatch` 接 `probeIntentDisp.applyStatus`；rename / Stop 路徑也接 |
| P1-T7 | `Module.Start` 加 `probeIntentDisp.replayStatus`；daemon-restart recovery integration test |

**驗收**：

- `go test ./...` 全綠
- stub detector 啟停流程在 lifecycle test 中可觀察（counter 計數）
- daemon-restart recovery test：mock DB 內 senderPID + status=running → Start 後 dispatcher.activeProbeIntents 含對應 entry
- ScreenChange 行為 zero regression（既有 probe_orchestrator_test.go / probe_orchestrator_integration_test.go 全綠）

### Phase 2 — codex detector + drift test + mlab live verify

**Tasks**：

| ID | 內容 |
|---|---|
| P2-T1 | `internal/agent/codex/probe_intent_process_dead.go` 新檔 + 單元測試（fake prober + IsPidAlive 控制 + emit signal 1 次後退出 + ctx cancel 提早退出） |
| P2-T2 | codex `Provider.ProbeIntents()` 宣告 + provider_test.go regression（既有不變、新增 ProbeIntent 宣告 assertion） |
| P2-T3 | dispatcher 路由 `ProbeIntentKindProcessDead` 到 `codex.StartProcessDeadDetector`（生產 wiring） |
| P2-T4 | drift test：iterate `registry` 找出所有 `ProbeIntentProvider`；逐一驗證 `ProbeIntents()` 宣告的 Kind 都有 dispatcher case；OnSignal 不為 nil；OnEntryStatus 非空 |
| P2-T5 | trace dev log 補：`[probe-intent]` step kind（chain log 第 6 條），W4 trace pipeline package comment 同步 |
| P2-T6 | integration test：mock codex 進程 alive→dead；驗 status running → error broadcast |
| P2-T7 | mlab live verify：手動測試 codex crash → lights 變 error；codex `/exit` （idle exit）→ ProbeIntent 在 idle 已停 → 無誤觸 |

**驗收**：

- `go test ./internal/...` 全綠
- drift test 失敗時清楚指出哪個 provider / kind 缺實作
- mlab live：codex SIGKILL → ≤2s lights 變 error；codex `/exit` 正常退場 → lights 維持 idle，無 error 誤觸
- 兩輪 codex review：standard + 三平行（攻擊 / 防守 / 體質）
- 收斂後 squash merge

### 為何拆兩 phase

- Phase 1 純 plumbing + interface — 改動範圍包到 schema / replay，risk 集中在共用 `applyProbeGuards` 抽取（regression 風險）
- Phase 2 detector + provider wiring — 改動局限 codex 包 + dispatcher 路由 case
- 拆兩 phase 兩輪 codex review 可分別聚焦：Phase 1 review plumbing safety；Phase 2 review detector correctness

> **PR 拆分權衡**（spec drift signal #2，待 codex review 決議）
>
> 也可合併 single PR — Phase 1 / 2 共 14 task，估 ~800 行 diff，仍在 medium PR review 容量內。Trade-off 詳 §9.2。

---

## 8. 驗收條件

### 8.1 功能性

1. ✅ codex 進程被 SIGKILL（running / waiting 狀態下）→ ≤2s 內 lights 變 `error`
2. ✅ codex `/exit` 正常退場 → 走 idle hook（PdxStop） → ProbeIntent 在 idle 不啟動 → 無 error 誤觸
3. ✅ codex idle 期間（已 PdxStop）crash → ProbeIntent 不啟動（gating 排除 idle） → status 維持 idle（**接受**：W6-4 follow-up 處理 idle→clear pane gone 場景）
4. ✅ daemon 重啟前 codex running，重啟後 codex crash → ≤2s 內 lights 變 error（issue #698 修復）
5. ✅ session rename 期間 ProbeIntent watcher 跟著 oldName 停、newName 啟（若新 status 仍 running/waiting）

### 8.2 不回歸

1. ✅ ScreenChangeWatcher 行為零變動（既有 probe_orchestrator_test.go / probe_orchestrator_integration_test.go 全綠）
2. ✅ codex 既有 hook handling 路徑零變動（events.go / status.go / hooks.go 無功能改動）
3. ✅ recordHookAt graceWindow / ErrorGuard / transition gate 在 ProbeIntent 路徑也生效
4. ✅ codex provider 既有 capabilities 都不受影響（HookInstaller / StatusSupporter / etc）

### 8.3 結構

1. ✅ `ProbeIntentProvider` 是 optional interface — 未實作的 provider 行為不變（cc / opencode 不受影響）
2. ✅ `ProbeIntentKind` 只有 `ProcessDead` 一個 const（lazy）
3. ✅ Detector 實作位於 `internal/agent/codex/probe_intent_*.go`；module 只負責 dispatcher
4. ✅ drift test 可偵測 provider 宣告 vs dispatcher 實作不一致

### 8.4 觀察

1. ✅ dev log（`PDX_DEV_MODE=1`）：`[probe-intent] start session=X agent=codex kind=process_dead pid=Y` / `[probe-intent] signal session=X kind=process_dead newStatus=error` / `[probe-intent] stop session=X kind=process_dead reason=Y` 三類 log 完整
2. ✅ TraceStore chain 第 6 條 step：`[probe-intent]`，與既有 `[hook] / [derive] / [handler] / [broadcast] / [verify_passed]` 對齊
3. ✅ expvar metric：`probe_intent_started` / `probe_intent_signal_emitted` / `probe_intent_dropped_grace` / etc

---

## 9. Spec Drift Signals（待 codex review 決議）

### 9.1 audit §7 假設修正：「process exit code」不可行

**Drift**：audit §7 W6-3 假設 daemon 能取得 codex exit code 並用 0 vs ≠0 區分 error/clear，但 daemon 不是 codex parent，Unix 設計限制不允許。

**建議**：以 §0.2 的 binary `process_dead` signal 取代 audit 描述，並在後續更新 audit doc（W6-3 ship 後同步修訂 §6/§7 對應條目）。

**待決議**：codex review 確認此 platform reality 修正是否合理；若 reviewer 發現替代方案（如 codex 是否有 stderr reporting 機制可低成本接），surface 出來。

### 9.2 W6-3 vs W6-4 拆/合決議

**選項 A（本 spec 預設，遵守 audit §7.2）**：W6-3 first PR 範圍只含 `process_dead → error`；W6-4 follow-up PR 加 pane existence 觀察區分 error/clear。

- 優點：first PR scope 最小，interface finalize 風險低
- 缺點：alpha 階段 user 主動 close pane（codex 在 running/waiting）會誤標 error；W6-4 ship 前 user-visible 體驗折扣

**選項 B（合併 first PR）**：W6-3 + W6-4 同 PR，detector 一次到位。

- 優點：detector 共用、區分機制是 binary signal，拆開的邊際成本不低；user 體驗從第一個 W6 PR 就完整
- 缺點：first PR scope 略大；interface 需多一個 `Signal.Payload` 欄位（或 dispatcher 多傳一參數）

**待決議**：codex review 給意見；user 拍板。

### 9.3 PR 拆 / 合 phase 決議

詳 §7「為何拆兩 phase」末尾 trade-off。預設拆兩 phase；若 codex review 認為合 single PR 更乾淨，採之。

### 9.4 detector mechanism — polling vs event-driven

**本 spec 預設 polling**（1Hz）。

**alternative：macOS kqueue `EVFILT_PROC + NOTE_EXIT`**（不需 NOTE_EXITSTATUS）— 對 non-child PID 也能收 exit 事件（雖然取不到 exit code，但 W6-3 不需要）。

- 優點：event-driven，零 polling 成本；latency 接近即時
- 缺點：darwin only（linux 需 fallback `pidfd_open` + `poll`，但 mlab 環境 linux daemon 不在當前 scope）；implementation 複雜度高（cgo / syscall.Kevent）

**本 spec 不採 kqueue**：1Hz polling 成本可忽略，complexity-vs-benefit 不划算。codex review 若發現 kqueue 已有現成 lib 包好可直接 import，再評估。

### 9.5 senderPID 持久化決議

§4.3 / §6.2 預設選項 A（`frames.last_sender_pid` 欄位）。Alternative：選項 B（不持久化，daemon-restart 後等下個 hook 補 — 但破 #698 修復目標）。

**建議**：採 A。codex review 確認 schema 改動是否符合 alpha 階段 `feedback_no_alpha_migration`（不寫 migration script，直接 alter）。

---

## 10. 文獻與相關檔案

### 設計來源

- `docs/specs/2026-04-23-lights-rebuild-spec.md` §8.2 — `ProbeIntentProvider` 既定方向
- `docs/specs/2026-04-28-hook-status-audit-spec.md` §6 / §7 / §7.1 / §7.2 — W5/W6 工作池 + 設計約束 + 推薦順序
- `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3 — Framework 撤回範圍

### 改動觸及

- **新檔**：
  - `internal/agent/codex/probe_intent_process_dead.go` + test
  - `internal/module/agent/probe_intent_dispatcher.go` + test
  - `internal/module/agent/probe_intent_dispatcher_integration_test.go`
- **修改**：
  - `internal/agent/provider.go`（新增 `ProbeIntentProvider` interface + types）
  - `internal/agent/codex/provider.go`（實作 `ProbeIntents()`）
  - `internal/module/agent/module.go`（`Start` 加 replayStatus / `manageActivityWatch` 接 dispatcher / `Stop` 加 dispatcher.stopAll）
  - `internal/module/agent/handler.go`（store senderPID）
  - `internal/module/agent/probe_orchestrator.go`（抽 `applyProbeGuards` free function）
  - `internal/module/agent/frame_ops.go` / `internal/module/agent/sweep.go`（如有 senderPID 路徑相關）
  - `internal/module/agent/trace.go`（package comment 加 `[probe-intent]` step kind）
  - `internal/store/frame.go`（schema：`last_sender_pid INTEGER`）
- **drift test**：
  - `internal/module/agent/probe_intent_dispatcher_drift_test.go`（registry sweep + Kind exhaustiveness）

### Issue / 文獻 references

- Issue #698 — daemon restart watcher recovery（W6-3 一併處理）
- Issue #719 — always-on probe residue（W3 已撤；非本 spec 範圍）
- Memory `feedback_skeleton_convergence` / `feedback_phase_skip_threshold` / `feedback_codex_pr_review_spec_alignment`
