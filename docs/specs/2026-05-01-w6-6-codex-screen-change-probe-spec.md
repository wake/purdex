# W6-6 codex permission-reply ScreenChange ProbeIntent spec

> **Status**：v2（codex round 1 review job `bl7niz2q4` 2 findings 採納；F1 quick-approval race 改 emit-once-and-return + grace-window-natural-suppression；F2 paneID reuse 改 emit-time senderPID liveness gate）
> **Worktree**：`.claude/worktrees/lights-w6-6-codex-screen-change` / branch `worktree-lights-w6-6-codex-screen-change`
> **Base**：`origin/main` @ alpha.279（W6-1a `c02299b7` 之後）
> **依賴**：
> - `docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md` — ProbeIntent interface finalize（Kind / Signal / OnEntryStatus / OnSignal）
> - `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3 — non-always-on / non-framework / per-agent ad-hoc 約束
> - `docs/specs/2026-04-28-hook-status-audit-spec.md` §6 W5-? + §7 W6-6 — 缺口定義
> - `internal/agent/probe/{probe.go, activity.go}` — `Prober.Watch` / `WatchOptions{TopLines}` / `ScreenChangeEvent`
> - `internal/module/agent/probe_intent_dispatcher.go` — 5-case lifecycle / 4-step guards / supportedKinds drift gate

---

## 0. 來龍去脈

### 0.1 直接動因（缺口）

codex CLI 0.125.0 在 user 發出 prompt 後若觸發 `requires_approval`（permission gate；e.g. tool 寫盤、shell exec），TUI 會渲染一個 modal dialog 等 user 按 1/2 同意。這條路徑會發 `PdxPermissionRequest` hook（status 切 `waiting`），但 **user 按下批准後 codex 不再發任何 hook**：

| Hook | 是否發 | 對應 status |
|---|---|---|
| `PdxPermissionRequest` (waiting → ...) | ✅ 發 | `waiting` |
| 批准後執行 tool | ❌ 不發任何 hook | 應為 `running` |
| 完成後 | ✅ `PdxStop` | `idle` |
| 失敗後 | ❌ `PdxStopFailure` 0.125.0 仍 FutureOnly | 應為 `error`（W6-3 已由 ProcessDead probe 承接） |

User 看到 codex 在 waiting 中按了批准、TUI 動起來了，但 lights 仍卡 waiting（黃 ⏳）直到下次 `PdxStop` 才落 `idle`。

### 0.2 與 W6-3 的對稱性與本 PR 的角色

W6-3 finalize 了 `ProbeIntentProvider` interface（一 Kind = `ProcessDead`）並把 W6-4（codex clear）合併進同一 PR。W6-6 是 **第二個 ProbeIntent Kind**：`ScreenChange`。本 PR 的工作：

1. 新 `ProbeIntentKindScreenChange` 常數（不重用 ProcessDead）
2. 新 detector `internal/agent/codex/probe_intent_screen_change.go`
3. codex `Provider.ProbeIntents()` 多回一筆 ScreenChange intent
4. module dispatcher switch + supportedKinds map 同步擴一格（drift test 自動 enforce 雙邊對齊）

**不擴 ProbeIntent interface 的形狀**。W6-3 spec §3 已明訂「未來 Kind MAY add Signal 欄位但 MUST keep existing 語意」— 本 PR 重用既有 `Signal{Kind, PaneAlive, PaneID, SenderPID}`，**無新欄位**。

### 0.3 為什麼 ScreenChange = running 是正確語意

waiting 狀態下，codex 退出 waiting 的所有路徑：

| 退出路徑 | 觸發 | 是否需要 probe |
|---|---|---|
| user 按 2 拒絕 / Esc 取消 → 回 idle | codex 發 `PdxStop` | ❌ hook authority 已 cover |
| codex pane 被 user 關閉 | tmux pane 消失 | ❌ W6-4 ProcessDead PaneAlive=false 已 cover |
| codex 進程崩潰 / SIGKILL | 進程死、pane 留 shell | ❌ W6-3 ProcessDead PaneAlive=true 已 cover |
| **user 按 1 批准 → codex 執行 tool** | **無 hook** | ✅ **本 PR 補位** |

**「螢幕任何變動 = running」之所以正確，是因為 waiting 唯一無 hook 的退場路徑就是 approval reply**。對話開始滾動 / dialog 消失 / 任何 line 1-10 文字變動，幾何上只能對應「批准已被消費，TUI 開始展示 tool 執行結果」。glyph / 字串 pattern 不可靠（codex 0.126+ 隨時改），但「pane 內容相對於 dialog stable baseline 的任何 hash 變動」是穩定的物理觀察。

### 0.4 為什麼選 `Prober.Watch` + `TopLines: 10`

- `Prober.Watch(target, opts, cb)`：W3 撤回後保留的 dumb screen primitive，500ms tick / fnv32a hash diff / orchestrator-owned dedup。已 ship、有 race 測試、不再 always-on。
- `TopLines: 10`：codex 0.125.0 TUI 對話往上推送，conversation 從 line 1 起向下展開；行 11 是 `›` 輸入區、行 13 是 status line（cursor / token counter）。
  - **排除 line 11 input echo 噪音**（user 在批准後若繼續打字會觸發 false running，雖然 user 已按批准、本來就應該是 running，但純粹噪音不算 evidence）
  - **排除 line 13 status line tick 噪音**（cursor blink / status text 自更新）
  - **mlab live verify 2026-05-01 已證**（kickoff 記憶 §3）：codex 0.125.0 idle TUI 完全靜態（1s + 2s 兩次 capture diff 空），無 timer / 無動畫；對話展開後 line 1-10 一定變化
- **不**用 `IdleStableTicks` / armed flag 機制（v1 設計）。codex round 1 F1 finding：armed=ScreenStable 在 quick-approval 場景下會卡 waiting（user 在 dialog 渲染完之前批准 → streaming 期 ScreenChanged 全被 armed=false drop → streaming 穩定才 armed=true 但已無下一個變化）。**v2 改用 `probeGraceWindow=2s` 自然吸收 dialog 渲染噪音**：detector emit-once-and-return；emit 在 grace 內被 drop → consumeSignals F1 re-arm cycle → 直到 grace 過期第一個 ScreenChanged 通過 → status=running。dialog 渲染通常 <100ms，user 反應通常 ≥1s，grace=2s 安全 cover dialog render；user 批准後 status=running latency ≤ `grace_remaining + watch_poll(500ms)` ≤ 2.5s。

### 0.5 與 fix-spec §3 的對齊

| fix-spec §3 約束 | W6-6 落地 |
|---|---|
| ❌ 不做 always-on probe | ✅ OnEntryStatus = {Waiting} 才 arm；其他 status 進來自動 teardown |
| ❌ 不做 generic ProbeProfileProvider | ✅ 走 `ProbeIntentProvider` interface，detector 在 codex package 內 |
| ❌ 不偽裝為 hook event | ✅ Signal 走 detector → channel → `applyProbeGuards` 與 hook 兩條獨立 channel |
| ❌ 不做跨 agent 中央規則 | ✅ 只 codex provider 宣告；cc / opencode 不動 |
| ✅ 沿用 W6-3 dispatcher plumbing | ✅ 5-case lifecycle + 4-step guards + supportedKinds drift gate 都不改 |
| ✅ ProbeIntent interface lazy 設計 | ✅ 新 Kind 常數 + 新 detector，interface 形狀不動 |

---

## 1. 範圍與目標

### 1.1 In-scope

1. **新 `ProbeIntentKindScreenChange`** 常數於 `internal/agent/provider.go`
2. **新 detector** `internal/agent/codex/probe_intent_screen_change.go`：
   - `StartScreenChangeDetector(ctx, prober, isPidAlive, paneID, senderPID, out)` — `isPidAlive` 注入為 minimal interface 便測試
   - 內部用 `prober.Watch(paneID, WatchOptions{TopLines: 10}, cb)`
   - **emit-once-and-return** pattern（與 ProcessDead 對稱）：第一個 ScreenChanged 收到時 callback emit Signal → close(emitted) → main goroutine `<-emitted` 解阻塞 → `prober.StopWatch(paneID)` + return → close(out) → consumeSignals 看 channel close 走 F1 re-arm decision
   - **emit 前 senderPID liveness gate**：callback 在 send 前驗 `isPidAlive(senderPID)`；false → drop（codex 已死，讓 W6-3 ProcessDead 處理 clear/error；防 paneID reuse 後 false-positive，spec 11 review F2）
   - Signal payload：`{Kind: ScreenChange, PaneAlive: true, PaneID, SenderPID}`
   - send 用 `select case out <- sig: case <-ctx.Done():` 防 ctx-cancel 後 send-on-closed-channel
3. **`codex.Provider.ProbeIntents()`** 第二筆 entry：
   - `Kind: ProbeIntentKindScreenChange`
   - `OnEntryStatus: []Status{StatusWaiting}`
   - `OnSignal: onScreenChange`（Kind != ScreenChange → return ""；其他 → return StatusRunning）
4. **`module.go startDetector` switch** 加 `case ProbeIntentKindScreenChange`，路由 `codex.StartScreenChangeDetector(ctx, mod.prober, paneID, senderPID, out)`
5. **`module.go supportedKinds` map** 加 `ProbeIntentKindScreenChange: {}`
6. **drift test** 既有的 `TestStartDetectorSwitchMatchesSupportedKinds`（W6-3 P2-T5 引入）自動 enforce 雙邊同步——本 PR 只需擴 fixture
7. **integration test**：`internal/module/agent/probe_intent_dispatcher_codex_wire_test.go` 加 ScreenChange wire path（fake prober 注入 ScreenChangeEvent → 驗 status 從 waiting → running 並 teardown）

### 1.2 Out-of-scope（明列防 scope creep）

- ❌ **抓 glyph / spinner 樣本**（codex 改 TUI 即 break；fix-spec §3 + audit §7.1）
- ❌ **sustained-change counter / consecutive≥N 抗噪**（mlab live verify 已證 tmux capture-pane 不受 copy mode scroll 影響、idle TUI 完全靜態，counter 無價值）
- ❌ **跨 agent 通用 `ProbeProfileProvider` / always-on policy**（fix-spec §3 明令撤回）
- ❌ **W6-1b cc question ask probe**（probe 必須 always-on + full-pane 通體掃描，違反 fix-spec §3；upstream Anthropic 標 not planned）
- ❌ **W6-5 opencode running 中介**（default 不做；要做走 plugin emit）
- ❌ **擴 startDetector signature**（用 paneID 作 prober.Watch target，tmux `%N` 是合法 capture-pane target，無需 session-name）
- ❌ **動 W6-3 既有 ProcessDead detector / interface / Signal struct**

### 1.3 Acceptance criteria

| 編號 | 條件 |
|---|---|
| A1 | waiting 進入 → grace 過後第一個 ScreenChanged → status 切 running、ScreenChange entry teardown |
| A2 | waiting 進入 → 2s 內無 screen change → grace 過後無 emit；status 維持 waiting；ProcessDead intent 仍 active |
| A3 | waiting → idle (PdxStop hook) → detector 因 OnEntryStatus 退出被 dispatcher cancel + StopWatch + 不 emit |
| A4 | waiting → 跨 provider 切換（agent_type 改，e.g. user 在同 session 換成 cc）→ reconcileSessionActive 取消 ScreenChange entry |
| A5 | dialog 渲染期間 ScreenChanged 進來 → emit Signal → grace drop → consumeSignals F1 re-arm（cycle 持續直到 grace 過期；不卡 waiting）|
| A6 | dispatcher cancel ctx 後 callback 仍可能 fire → select-with-ctx 防 leak（不 panic、不 send-on-closed-channel）|
| A7 | drift test 通過：startDetector switch case 數量 == supportedKinds 條目數 == ProbeIntents 宣告 Kind 集合 |
| A8 | mlab live verify §1：codex permission ask → user 按 1 批准 → ≤2.5s lights running（含 grace=2s + 一個 watch tick=0.5s）|
| A9 | mlab live verify §2：codex permission ask → user 按 2 拒絕 → PdxStop hook fires → lights idle（不誤觸 running） |
| A10 | mlab live verify §3：codex 在 waiting 時 user 主動關 pane → W6-4 ProcessDead PaneAlive=false → lights clear（ScreenChange detector 因 ctx cancel 退出） |
| A11 | **quick approval（hook 後 0.5s 即按批准）**：detector 在 dialog-render 時 emit → grace drop → re-arm；grace 過後新 ScreenChanged → emit 通過 → status=running。Latency ≤ 2.5s。|
| A12 | **paneID reuse 防護**：detector 在 emit 前 senderPID 已死 → drop（不 emit false running）；test 用 fake `isPidAlive` 注入 dead state |

---

## 2. 設計約束

### 2.1 必須

- 新 detector 命名 `internal/agent/codex/probe_intent_screen_change.go`（W6-3 spec §0.1 + audit §7.1 約束 detector 歸 agent package）
- detector 公開函式 `StartScreenChangeDetector(ctx, prober screenWatcher, isPidAlive func(int) bool, paneID string, senderPID int, out chan<- agent.Signal)`，與 `StartProcessDeadDetector` 對稱（`isPidAlive` 注入便測試）
- `screenWatcher` interface 是本 detector 包私有的 minimal contract，只暴露 `Watch(target, opts, cb)` + `StopWatch(target)` 兩個方法，**不直接 import `*probe.Prober`**——便於測試注入 fake，與 W6-3 `tmuxPaneLister` interface 同 pattern
- detector **必須 emit-once-and-return**：第一個成功的 emit 後立即解阻塞 main goroutine、`prober.StopWatch(paneID)`、return；channel close → consumeSignals F1 re-arm 路徑可用（v2 修 codex F1）
- callback 內 send Signal 必須 `select case out <- sig: case <-ctx.Done(): return`，防 ctx cancel 後 send-on-closed-channel
- callback 在 send 前必須 `if !isPidAlive(senderPID) { return }`（v2 修 codex F2 paneID reuse race）
- main goroutine `<-ctx.Done()` OR `<-emitted` 後必須 call `prober.StopWatch(paneID)` 才 return（避免 watcher 留在 prober.watchers map leak）
- `Provider.ProbeIntents()` 回傳 slice 順序：`ProcessDead` 在前、`ScreenChange` 在後（穩定順序便於測試 fixture 對齊）

### 2.2 不可

- ❌ 不直接 import `internal/agent/probe.Prober`（用 minimal interface 注入；同 W6-3 模式）
- ❌ 不在 detector 內讀 `m.currentStatus` 或 `m.activeProbeIntents`（dispatcher 已負責 lifecycle）
- ❌ 不複用 `ProbeIntentKindProcessDead` 常數（語意不同，drift test 會抓）
- ❌ 不在 `OnSignal` 內 emit log / metric（dispatcher consumeSignals 已 emit `[probe-intent] signal …`）
- ❌ 不引入 `armed` flag / ScreenStable 消費機制（v2 撤回；codex F1 找到的死鎖場景）
- ❌ 不依賴 detector 自己抗 dialog-render noise（grace window=2s 自然吸收；detector 一發 emit-once 走 dispatcher F1 re-arm cycle 即可）

### 2.3 既知 race / edge case

| ID | 場景 | 處置 |
|---|---|---|
| R1 | ctx cancel 後 prober callback 仍 fire 一兩次（500ms tick race）| out channel `select case out<-: case <-ctx.Done(): return`；callback 同時讀 `<-emitted` 防止已 emit 過再送 |
| R2 | dialog 渲染期間連發 ScreenChanged | 第一個 emit → grace drop → consumeSignals applied=false → channel close → F1 re-arm；新 detector 重複，直到 grace 過期（典型 2s）第一個 emit 通過 |
| R3 | callback 在 emit 與 close(emitted) 之間 race（兩 callback goroutine 同時 fire）| `sync.Once` or `atomic.Bool` 把 emit-and-close 包成 atomic；spec 內示意以 `sync.Once` |
| R4 | dispatcher cancel ctx 與 callback emit 競賽 | `select case out<-: case <-ctx.Done():` — ctx cancel 贏 → drop；`out<-` 贏 → 後續 `<-emitted` 路徑解阻塞 |
| R5 | W6-3 ProcessDead intent 與 W6-6 ScreenChange intent 同時 active | 不同 Kind，dispatcher per-(session, kind) 分槽；reconcile 只看 declaredKinds，不衝突 |
| R6 | `prober.Watch(paneID, ...)` 與 W3-revert 後 production caller 為 0 的事實 | W6-6 是 W3 撤回後 `Prober.Watch` 的**第一個 production caller**；測試用 fake prober 即可，production 用 module.prober |
| R7 | tmux server restart 導致 paneID 重用（codex F2）| callback emit 前驗 `isPidAlive(senderPID)`；server restart 期間原 codex 進程已死 → drop。production 接 `probe.IsPidAlive`（syscall.Kill(pid,0)）|
| R8 | F1 re-arm 反覆執行造成 expvar 噪音 | 驗收條件 A11 要求 grace 過後 ≤2.5s 解；re-arm cycle 在 grace 期間每 tick 一次 = 最多 4 次（grace=2s, tick=500ms）；MetricProbeIntentStarted 多+4 可接受。devlog 開時可看到 cycle pattern。|

### 2.4 與 W6-3 spec drift signal 預警表的對照

| W6-3 spec §9 signal | W6-6 對應 |
|---|---|
| 「Signal 欄位重用 vs 新增」 | ✅ 不新增；PaneAlive 對 ScreenChange 設 true（detector 觀察的就是 pane 還在的狀態） |
| 「未來 Kind 改 OnEntryStatus 語意」 | ✅ 不改；只 declaredKinds 多一格 |
| 「跨 Kind 觀察拼裝」 | ✅ 不做；W6-6 entry 與 W6-3 entry 完全獨立 lifecycle |

---

## 3. ProbeIntent 模型擴展

`internal/agent/provider.go` 新增 1 行常數，**不改 struct**：

```go
const (
    ProbeIntentKindProcessDead   ProbeIntentKind = "process_dead"
    ProbeIntentKindScreenChange  ProbeIntentKind = "screen_change" // W6-6: codex permission-reply via top-10-line content change
)
```

`Signal` struct **不改**。對 ScreenChange Signal：
- `Kind = ProbeIntentKindScreenChange`
- `PaneAlive = true`（detector 觀察的是「pane 還在且內容變」；pane 不在會 capture-pane error 自動進 watchLoop tmux-error skip path 不 fire callback）
- `PaneID`, `SenderPID`：detector 入參直接 carry

---

## 4. codex provider 實作

### 4.1 `Provider.ProbeIntents()` 變化

```go
func (p *Provider) ProbeIntents() []agent.ProbeIntent {
    return []agent.ProbeIntent{
        {
            Kind:          agent.ProbeIntentKindProcessDead,
            OnEntryStatus: []agent.Status{agent.StatusRunning, agent.StatusWaiting},
            OnSignal:      onProcessDead,
        },
        {
            Kind:          agent.ProbeIntentKindScreenChange,
            OnEntryStatus: []agent.Status{agent.StatusWaiting},
            OnSignal:      onScreenChange,
        },
    }
}
```

`OnEntryStatus = {StatusWaiting}` 的理由：

- running 已是目標狀態，不需要 probe
- error / clear / idle 不會由 ScreenChange 推論（hook 已 authority）
- waiting 是唯一進入點

不重疊 ProcessDead 的 `{Running, Waiting}`：兩 intent 在 waiting 同時 active；ScreenChange 一觸發切 running → 退出 OnEntryStatus → ScreenChange entry teardown；ProcessDead entry 因 newStatus=running 仍在其 OnEntryStatus 內、繼續監控 pane/process 死活——這是 dispatcher 5-case lifecycle 的自然行為，無特例。

### 4.2 `onScreenChange` mapper

```go
// onScreenChange maps a ScreenChange signal to StatusRunning. Defends against
// dispatcher misuse: a Signal carrying a non-ScreenChange Kind returns ""
// (drop the signal) rather than mapping to Running. Mirrors onProcessDead
// pattern from W6-3.
func onScreenChange(sig agent.Signal) agent.Status {
    if sig.Kind != agent.ProbeIntentKindScreenChange {
        return ""
    }
    return agent.StatusRunning
}
```

不檢 `PaneAlive`（語意上 ScreenChange Kind 必然 PaneAlive=true，detector contract 保證；額外 check 是 dead code 噪音）。

### 4.3 Detector：`StartScreenChangeDetector`（v2，emit-once + senderPID gate）

```go
// internal/agent/codex/probe_intent_screen_change.go
package codex

import (
    "context"
    "sync"

    "github.com/wake/purdex/internal/agent"
    "github.com/wake/purdex/internal/agent/probe"
)

// screenWatcher is the minimal contract this detector requires from the prober.
// Production wires *probe.Prober; tests inject a fake to drive callbacks.
type screenWatcher interface {
    Watch(target string, opts probe.WatchOptions, cb probe.ScreenChangeCallback)
    StopWatch(target string)
}

// screenChangeTopLines is the capture region. Codex 0.125.0 TUI puts
// conversation at lines 1-10, input at line 11, status at line 13. Capturing
// only the top 10 lines excludes input echo + status line tick noise so the
// detector observes only conversation flow (mlab live verify 2026-05-01).
const screenChangeTopLines = 10

// StartScreenChangeDetector watches the codex pane for top-10-line content
// change and emits a single Signal on the first ScreenChanged event whose
// senderPID is still alive. Behaviour mirrors StartProcessDeadDetector:
// emit-once-and-return so consumeSignals can run F1 re-arm if the dispatcher
// drops the signal (e.g. probeGraceWindow). Cancel ctx to stop early.
//
// Why no armed/ScreenStable consumer (v2 — codex round 1 F1 finding):
// In quick-approval flows, the user can dismiss the permission dialog before
// the watcher sees a ScreenStable, leaving armed=false through the entire
// post-approval streaming sequence. v2 instead lets dispatcher's existing
// 2-second probeGraceWindow absorb dialog-render noise: every ScreenChanged
// fires Signal once, returns; if the dispatcher drops it within grace,
// consumeSignals re-arms a fresh detector — until grace expires the next
// ScreenChanged passes and flips status to running.
//
// Why senderPID liveness gate (v2 — codex round 1 F2):
// tmux server restart can reuse pane id %N for an unrelated pane. The
// detector validates the original codex sender PID is still alive before
// emitting; pid-dead means the codex process has gone, so dispatch should
// flow through W6-3 ProcessDead instead.
func StartScreenChangeDetector(
    ctx context.Context,
    prober screenWatcher,
    isPidAlive func(int) bool,
    paneID string,
    senderPID int,
    out chan<- agent.Signal,
) {
    emitted := make(chan struct{})
    var once sync.Once
    cb := func(ev probe.ScreenChangeEvent) {
        if ev.Kind != probe.ScreenChanged {
            return
        }
        // F2 gate: codex pid still alive? otherwise let ProcessDead handle.
        if !isPidAlive(senderPID) {
            return
        }
        once.Do(func() {
            select {
            case out <- agent.Signal{
                Kind:      agent.ProbeIntentKindScreenChange,
                PaneAlive: true,
                PaneID:    paneID,
                SenderPID: senderPID,
            }:
                close(emitted)
            case <-ctx.Done():
            }
        })
    }
    prober.Watch(paneID, probe.WatchOptions{TopLines: screenChangeTopLines}, cb)
    select {
    case <-emitted:
    case <-ctx.Done():
    }
    prober.StopWatch(paneID)
}
```

**emit-once 機制細節**：

- `sync.Once` 保證 emit + close(emitted) 是 atomic — 多個 callback goroutine 同時 fire 時只一個進 critical section
- main goroutine 等 `<-emitted` 或 `<-ctx.Done()`，任一觸發就 StopWatch + return
- `out` channel buffer=1（dispatcher 提供）；callback select 同時等 `<-ctx.Done()`，ctx cancel 場景下不會 send-on-closed
- emit-once-and-return 使 consumeSignals 在 grace drop 時看到 `range in` 終止 + `appliedAny=false` → F1 re-arm 路徑生效
- ScreenStable event 直接忽略（不 set armed flag、不 emit；只用 ScreenChanged）

### 4.4 為何用 `paneID` 作 prober.Watch target

tmux pane id `%N` 是 capture-pane 的合法 target（`tmux capture-pane -t %N -p` 工作正常）。優點：

1. **不需擴 `startDetector` signature**（dispatcher 已經把 paneID 傳下來；session target name 沒在參數列裡）
2. **多 pane 同 session 不衝突**（prober.watchers map key=target，paneID 唯一）
3. **paneID 死後自動 teardown**：tmux pane 消失 → capture-pane 回 error → watchLoop `if !ok { continue }` 跳 tick；W6-4 ProcessDead PaneAlive=false 會在另一個 intent 切 status，dispatcher 5-case 連帶 teardown ScreenChange entry

---

## 5. Module plumbing

### 5.1 `module.go` switch + supportedKinds

```go
m.probeIntentDisp.startDetector = func(
    ctx context.Context, mod *Module, kind agentpkg.ProbeIntentKind,
    paneID string, senderPID int, out chan<- agentpkg.Signal,
) {
    switch kind {
    case agentpkg.ProbeIntentKindProcessDead:
        codex.StartProcessDeadDetector(ctx, mod.tmux, paneID, senderPID, out)
    case agentpkg.ProbeIntentKindScreenChange:
        codex.StartScreenChangeDetector(ctx, mod.prober, probe.IsPidAlive, paneID, senderPID, out)
    default:
        defaultStartProbeIntentDetector(ctx, mod, kind, paneID, senderPID, out)
    }
}

m.probeIntentDisp.supportedKinds = map[agentpkg.ProbeIntentKind]struct{}{
    agentpkg.ProbeIntentKindProcessDead:  {},
    agentpkg.ProbeIntentKindScreenChange: {},
}
```

### 5.2 `m.prober` 是否已可用

`m.prober *probe.Prober` 在 Module struct 中，由 `Init` 階段 `agent` module 從 `core.Registry` 取得，本 PR 不動該 wiring。檢查 `Init()` 確認 prober field 在 startDetector closure 被 invoke 時已 set（與 `mod.tmux` 同樣 lazy-resolve；W6-3 closure comment 已說明）。

### 5.3 不變的 dispatcher path

- `applyIntentLifecycle` 5-case：不動
- `applyProbeGuards` 4-step（StaleCheck / graceWindow / Mapping / ErrorGuard / transition gate）：不動
- `consumeSignals` teardown + F1 fix re-arm：不動
- `reconcileSessionActive`：不動（只看 declaredKinds，多一個 Kind 自動處理）
- `replayStatus`（issue #698 daemon-restart recovery）：不動（snapshot status → applyStatus → 兩 Kind 各自 lifecycle）

### 5.4 expvar / dev log

既有 `[probe-intent] start … kind=screen_change`、`[probe-intent] signal … kind=screen_change applied=true`、`[probe-intent] stop … kind=screen_change` 三條 log line 自動 cover（既有 logger 把 kind 印出）。expvar 計數器（`MetricProbeIntentStarted` 等）也自動加。**本 PR 不新增 metric**。

---

## 6. Phase 拆分（給 plan 用）

### 6.1 P1 — interface + provider + detector（單元層）

| Task | 檔案 | 內容 |
|---|---|---|
| P1-T1 | `internal/agent/provider.go` | 加 `ProbeIntentKindScreenChange` const + GoDoc |
| P1-T2 | `internal/agent/codex/probe_intent_screen_change.go` | 新檔：`screenWatcher` interface + `StartScreenChangeDetector(ctx, prober, isPidAlive, paneID, senderPID, out)` |
| P1-T3 | `internal/agent/codex/probe_intent_screen_change_test.go` | 表驅動 tests：(1) ScreenChanged + pid alive → emit Signal once / (2) multiple ScreenChanged → 只 emit 一次（sync.Once）/ (3) ScreenStable 忽略（不 emit）/ (4) pid dead → drop（不 emit；F2 gate）/ (5) ctx cancel before emit → main goroutine StopWatch + return / (6) ctx cancel after emit → return（emitted 已 close）/ (7) emit 後 callback 仍 fire → sync.Once 保證 no-op |
| P1-T4 | `internal/agent/codex/provider.go` | `ProbeIntents()` 加第二筆 + 新 `onScreenChange` mapper |
| P1-T5 | `internal/agent/codex/provider_test.go` | 擴 fixture：ProbeIntents 長度 2、第二筆 Kind/OnEntryStatus/OnSignal 對齊；onScreenChange 三 case（match→Running / 非 ScreenChange Kind→"" / nil → ""） |

### 6.2 P2 — module wiring + drift gate

| Task | 檔案 | 內容 |
|---|---|---|
| P2-T1 | `internal/module/agent/module.go` | startDetector switch 加 case + supportedKinds map 加 entry |
| P2-T2 | `internal/module/agent/probe_intent_dispatcher_drift_test.go` | 擴 drift fixture：switch ↔ supportedKinds ↔ codex ProbeIntents() 三邊 Kind 集合相等 |
| P2-T3 | `internal/module/agent/probe_intent_dispatcher_codex_wire_test.go` | 加 ScreenChange wire test：fake prober 注入 stable→change → 驗 status waiting→running + active intent teardown |

### 6.3 P3 — integration + race + mlab

| Task | 檔案 | 內容 |
|---|---|---|
| P3-T1 | `internal/module/agent/probe_intent_dispatcher_integration_test.go` | 端到端 lifecycle：(a) waiting hook → arm → fake event → grace 過後 status=running + teardown / (b) **A11 quick-approval cycle**：waiting hook → fake ScreenChanged 在 grace 內 → grace drop → re-arm → 再 ScreenChanged → grace 已過 → status=running / (c) waiting → idle hook → teardown 不 emit / (d) cross-provider switch → reconcile teardown / (e) **A12 paneID reuse 防護**：注入 isPidAlive=false → ScreenChanged → no Signal |
| P3-T2 | mlab live verify | §1 approval reply / §2 reject reply / §3 close pane during waiting；建 dev log + screenshot 證據；spec → plan 階段 placeholder，PR body §test plan checklist 條列 |

---

## 7. 驗收條件（重述 §1.3 Acceptance）

- A1-A7 unit + integration test 全綠
- A8-A10 mlab live verify 全 PASS
- `go test ./...` 24 packages 全綠
- `go test -race ./internal/agent/codex/... ./internal/module/agent/...` 全綠
- drift test 抓得到刻意撤掉 supportedKinds entry 或刻意註解 switch case
- vet / lint clean

---

## 8. Spec Drift Signals（給 codex review 與 plan 留 anchor）

下列任一徵兆出現，停修 + surface：

| Signal | 為什麼是 drift |
|---|---|
| 想擴 `ProbeIntent` struct 多欄位 | W6-3 finalize 已涵蓋；ScreenChange 用既有欄位足夠 |
| 想擴 `Signal` struct 多欄位 | 同上；PaneAlive=true 對 ScreenChange 已是合理常量 |
| 想引入 sustained-change counter（連續 N tick 才 emit） | mlab live verify 已證 idle TUI 完全靜態 + scroll 不影響 capture-pane；counter 是預先優化雜訊 |
| 想恢復 `armed` flag / ScreenStable 消費機制 | v1 採用、codex round 1 F1 已抓到 quick-approval race；v2 撤回 |
| 想 generalize 為 「per-agent ScreenChangeProfile」（cc / opencode 也用） | fix-spec §3 撤回 framework；W6-1b cc 已降級不做、W6-5 opencode 走 plugin |
| 想 detector 內部直接讀 `m.activeProbeIntents` / `m.currentStatus` | dispatcher 已負責 lifecycle；detector 只發 Signal |
| 想抓 codex 特定 glyph / 字串 pattern（spinner / "Approved." / etc）| audit §7.1：agent 改 TUI 即 break；fix-spec 撤回 |
| 想擴 `startDetector` signature 加 session target | paneID 已是合法 capture-pane target（spec §4.4） |
| 想 ScreenChange 觸發後 emit 多次 Signal | v2 emit-once-and-return 設計：sync.Once + emitted channel + main goroutine return；dispatcher F1 re-arm 自然處理 grace 內的反覆嘗試 |
| 想跳過 grace window 對 ScreenChange 特化 | grace 是 dialog-render 噪音的天然吸收；繞過 grace 會引入 dialog-render false positive，比 quick-approval +2.5s latency 嚴重 |

---

## 9. 不在 scope（明列防 scope creep）

- ❌ W6-1b cc question ask probe（probe 必須 always-on + full-pane，違反 fix-spec §3；upstream Anthropic not planned）
- ❌ W6-5 opencode running 中介（default 不做；要做走 plugin emit）
- ❌ W7 Dev Inspector UI（W4 5-step chain log 已涵蓋 trace 需求；DROPPED 2026-04-30）
- ❌ 動 W6-3 既有 ProcessDead detector / Provider.ProbeIntents() 第一筆 entry / Signal struct
- ❌ 加新 metric（既有 expvar 自動 cover screen_change kind）
- ❌ 動 SPA 端（status 切換是 daemon 端內部事；SPA 已認 running）

---

## 10. 文獻

- W6-3 spec：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md`（interface finalize / dispatcher / drift gate / 11 finding 收斂）
- W6-3 plan：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-plan.md`（14 task / 5 輪 review）
- Fix-spec：`docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3（framework 撤回約束）
- W1 audit：`docs/specs/2026-04-28-hook-status-audit-spec.md` §6 / §7 / §7.1（缺口工作池 + 設計約束）
- Lights rebuild spec：`docs/specs/2026-04-23-lights-rebuild-spec.md` §8.2（ProbeIntent 起源）
- Probe primitives：`internal/agent/probe/probe.go` / `internal/agent/probe/activity.go`
- W6-3 detector：`internal/agent/codex/probe_intent_process_dead.go`
- Dispatcher：`internal/module/agent/probe_intent_dispatcher.go`

---

## 11. Open questions（已隨 codex round 1 收斂）

1. **`screenWatcher` interface 暴露面** ✅ — 只暴露 Watch / StopWatch 足夠；整合測試驗 status 翻轉而非 prober 內部狀態。
2. **`OnEntryStatus = {Waiting}`** ✅ — 不含 Running；running 已是目標，ScreenChange running→running 是 noop 但會 spam log。
3. **F1 re-arm 互動（v2 改變角色）** ✅ — F1 re-arm 在 v2 設計裡是 **核心機制**：detector emit-once-and-return → consumeSignals 看 channel close → 若 appliedAny=false（grace drop）→ re-run applyStatus → 新 detector arm。grace 過後第一個 emit 通過。re-arm 在 ScreenChange 路徑不再是 "harmless edge case"，而是必要的 dialog-noise-suppression 機制（codex F1 finding 收斂後的設計）。
4. **`prober` 在 Init 後才 ready** ✅ — closure 用 `mod.prober` lazy-resolve；Module.New 順序與 tmux 同模式（W6-3 已驗）。`probe.IsPidAlive` 是 package-level function 直接 ref（無 lazy 問題）。

### 11.1 v2 後仍 open（給 round 2 review）

- **F1 re-arm 上限**：grace 期間理論上反覆 emit→drop→re-arm；每 watch_poll(500ms) 一次 = 最多 4 cycle / 2s grace。expvar `MetricProbeIntentStarted` / `MetricProbeIntentSignalEmitted` 多+4 在 dev mode 是 noise pattern；production 不影響功能。是否需要 cap？傾向不需要（CycleCount=4 上限固定，grace 過後自然停止）。
- **A11 acceptance test 可重現性**：integration test 注入 `orchNowFn` 推進虛擬時間使 grace 過期；W6-3 既有 OR4 test 已用此 pattern。直接複用。
- **F2 paneID reuse 完整 coverage**：原 codex F2 提的「tmux server restart」是極端 case；本 spec 修法靠 isPidAlive 攔截（emit 前一刻原 codex pid 已死的話 drop）。但若 tmux 重啟期間 pid 仍 alive（codex 不依賴 tmux server）+ paneID 真的 reuse，理論仍可能 false positive。實務上 codex 通常在自己的 tmux pane 裡跑、tmux server 死意味著 pane 也死、pid 跟著被 SIGHUP — pid alive 同時 paneID reuse 的概率近零。spec 接受此 known limitation 作 production rare-event。

