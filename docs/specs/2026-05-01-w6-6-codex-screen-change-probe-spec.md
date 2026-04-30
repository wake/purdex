# W6-6 codex permission-reply ScreenChange ProbeIntent spec

> **Status**：v3（codex round 2 review job `bjv3831k4` 2 high findings 採納；F1 fast/silent approval 改 retry-emit pattern（pending flag + 每 retry interval 重發直到 ctx cancel）；F2 paneID identity 改用 `Prober.IsAliveFor("codex", paneID)` — 驗 paneID 對應 pane_pid 子樹有 codex 進程，而非單純 senderPID alive）
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
- **不**用 `IdleStableTicks` / armed flag 機制（v1 設計）。codex round 1 F1：armed=ScreenStable 在 quick-approval 下卡 waiting。
- **不**用 emit-once-and-return + dispatcher F1 re-arm 機制（v2 設計）。codex round 2 F1：fast/silent approval 場景（grace 期間所有 screen 變化發生 + 後續無變化）下，re-arm 後新 detector baseline 已是 post-change 螢幕，無下一個 diff → 仍卡 waiting。
- **v3 改用 retry-emit pattern**：detector 用 `pending atomic.Bool` 記錄「至少一次 ScreenChanged 已被觀察」；main goroutine 每 retry interval（= watchPollInterval = 500ms）若 pending=true 則重發 Signal；dispatcher grace drop 期間反覆 retry，grace 過期後第一個 retry emit 通過 → status=running → ctx cancel → detector 退。fast/silent 場景下 pending 一旦 set 就持續 retry，不依賴 grace 後是否還有 ScreenChanged。
- **`IsAliveFor("codex", paneID)` 取代 isPidAlive**（v3 修 codex round 2 F2 paneID identity）：emit 前驗 paneID 對應 pane_pid 子樹仍有 codex 進程；tmux server restart + paneID reuse 給 unrelated pane 場景下 pane_pid 不再是 codex 子樹 → 驗失敗 → drop。比 senderPID alive 嚴格。

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
   - `StartScreenChangeDetector(ctx, prober, isCodexAlive, paneID, senderPID, out)`；`isCodexAlive func() bool` 注入為 closure（production: `func() bool { return prober.IsAliveFor("codex", paneID) }`；test: 注入 fake）
   - 內部用 `prober.Watch(paneID, WatchOptions{TopLines: 10}, cb)`
   - **retry-emit** pattern（v3 — 修 codex round 2 F1）：callback 收 ScreenChanged + isCodexAlive=true → 設 `pending atomic.Bool=true`（不直接 emit）；main goroutine 每 `retryInterval`（= watchPollInterval = 500ms）若 pending=true 則用 select-with-ctx 嘗試 send Signal；dispatcher grace drop 期間反覆 retry，grace 過期後第一個 emit 通過 → status=running → dispatcher cancel ctx → detector 退
   - **emit 前 paneID identity gate**（v3 — 修 codex round 2 F2）：`isCodexAlive()` 在 callback 內每次 ScreenChanged 都驗（不只 senderPID alive）；false → drop（pane 不再屬於 codex 進程，可能是 tmux restart + paneID reuse 場景）
   - Signal payload 不變：`{Kind: ScreenChange, PaneAlive: true, PaneID, SenderPID}`
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
| A1 | waiting 進入 → 第一個 ScreenChanged + isCodexAlive=true → pending=true → main goroutine retry 直到 grace 過期第一個 emit 通過 → status=running、entry teardown |
| A2 | waiting 進入 → 2s 內無 screen change → pending 仍 false → 無 emit；status 維持 waiting；ProcessDead intent 仍 active |
| A3 | waiting → idle (PdxStop hook) → detector 因 OnEntryStatus 退出被 dispatcher cancel ctx → main goroutine select hits ctx.Done → StopWatch + return（pending=true 也不 emit，因 ctx 已 cancel） |
| A4 | waiting → 跨 provider 切換（agent_type 改）→ reconcileSessionActive 取消 ScreenChange entry |
| A5 | dialog 渲染期間多次 ScreenChanged 連發 → callback 反覆 set pending=true（idempotent）；main goroutine 反覆 retry-emit → grace drop 直到過期 → 通過 |
| A6 | dispatcher cancel ctx 期間 main goroutine 在 emit select 內 → ctx.Done 路徑贏 → StopWatch + return（不 send-on-closed-channel） |
| A7 | drift test 通過：startDetector switch case 數量 == supportedKinds 條目數 == ProbeIntents 宣告 Kind 集合 |
| A8 | mlab live verify §1：codex permission ask → user 按 1 批准 → ≤2.5s lights running（含 grace=2s + 一個 retry interval=0.5s）|
| A9 | mlab live verify §2：codex permission ask → user 按 2 拒絕 → PdxStop hook fires → lights idle（不誤觸 running） |
| A10 | mlab live verify §3：codex 在 waiting 時 user 主動關 pane → W6-4 ProcessDead PaneAlive=false → lights clear（ScreenChange detector 因 ctx cancel 退出） |
| A11 | **fast/silent approval**：grace 期間所有 screen 變化發生 + 後續無新變化 → pending=true 一旦 set 就持續 retry → grace 過期後 retry 通過 → status=running。Latency ≤ 2.5s。 |
| A12 | **paneID identity 防護**：fake prober 注入 `IsAliveFor("codex", paneID)=false`（模擬 tmux restart + paneID reuse 給 unrelated pane）→ ScreenChanged 進 callback → drop → pending 維持 false → 無 emit |
| A13 | **isCodexAlive recovers**：`IsAliveFor` 第一個 callback 回 false（transient tmux 查詢失敗）→ drop；下一個 callback 回 true → pending=true → retry-emit → 通過。詳見 §2.3 R7 |

---

## 2. 設計約束

### 2.1 必須

- 新 detector 命名 `internal/agent/codex/probe_intent_screen_change.go`（W6-3 spec §0.1 + audit §7.1 約束 detector 歸 agent package）
- detector 公開函式 `StartScreenChangeDetector(ctx, prober screenWatcher, isCodexAlive func() bool, paneID string, senderPID int, out chan<- agent.Signal)`，與 `StartProcessDeadDetector` 對稱（`isCodexAlive` 注入 closure 便測試 — production: `func() bool { return prober.IsAliveFor("codex", paneID) }`）
- `screenWatcher` interface 是本 detector 包私有的 minimal contract，只暴露 `Watch(target, opts, cb)` + `StopWatch(target)` 兩個方法，**不直接 import `*probe.Prober`**——便於測試注入 fake，與 W6-3 `tmuxPaneLister` interface 同 pattern
- detector **採 retry-emit pattern**（v3）：callback 設 `pending atomic.Bool=true`；main goroutine 每 retryInterval（= 500ms = watchPollInterval）若 pending=true 用 select-with-ctx send Signal；ctx cancel 即 StopWatch + return
- callback 在 set pending 前必須 `if !isCodexAlive() { return }`（v3 修 codex round 2 F2 paneID identity）
- main goroutine 在 send select 與 ticker select 都必須 watch `<-ctx.Done()` → StopWatch + return（避免 watcher leak）
- `Provider.ProbeIntents()` 回傳 slice 順序：`ProcessDead` 在前、`ScreenChange` 在後（穩定順序便於測試 fixture 對齊）

### 2.2 不可

- ❌ 不直接 import `internal/agent/probe.Prober`（用 minimal interface 注入；同 W6-3 模式）
- ❌ 不在 detector 內讀 `m.currentStatus` 或 `m.activeProbeIntents`（dispatcher 已負責 lifecycle）
- ❌ 不複用 `ProbeIntentKindProcessDead` 常數（語意不同，drift test 會抓）
- ❌ 不在 `OnSignal` 內 emit log / metric（dispatcher consumeSignals 已 emit `[probe-intent] signal …`）
- ❌ 不引入 `armed` flag / ScreenStable 消費機制（v2 撤回；codex round 1 F1 死鎖）
- ❌ 不採 emit-once-and-return + dispatcher F1 re-arm（v2 撤回；codex round 2 F1 fast/silent approval re-baseline 漏）
- ❌ 不依賴 detector 自己抗 dialog-render noise；改靠 dispatcher graceWindow drop + main goroutine retry 自然 cover
- ❌ 不在 detector 內讀 `m.currentStatus` 或 `m.activeProbeIntents`（dispatcher 已負責 lifecycle）

### 2.3 既知 race / edge case

| ID | 場景 | 處置 |
|---|---|---|
| R1 | ctx cancel 後 prober callback 仍 fire 一兩次（500ms tick race）| callback 設 pending atomic 不直接 send；main goroutine 在 ticker + send select 都 watch ctx.Done，cancel 後 return |
| R2 | dialog 渲染期間連發 ScreenChanged | callback 反覆 set pending=true（atomic.Bool 冪等）；main goroutine 不論 retry 幾次 grace drop，grace 過期後第一個 retry 通過 |
| R3 | retry interval 與 watchPollInterval 同（500ms），啟動瞬間 main ticker 與 watch ticker phase shift | 不影響 — pending 只要 set，下次 main ticker 就 retry；phase 不重要 |
| R4 | dispatcher cancel ctx 與 main goroutine retry-send 競賽 | `select case out<-: case <-ctx.Done():` — ctx 贏 → 不 send + return；out 贏 → 下一輪 ticker 看 ctx.Done 退 |
| R5 | W6-3 ProcessDead intent 與 W6-6 ScreenChange intent 同時 active | 不同 Kind，dispatcher per-(session, kind) 分槽；reconcile 只看 declaredKinds，不衝突 |
| R6 | `prober.Watch(paneID, ...)` 與 W3-revert 後 production caller 為 0 的事實 | W6-6 是 W3 撤回後 `Prober.Watch` 的**第一個 production caller**；測試用 fake prober 即可，production 用 module.prober |
| R7 | tmux server restart + paneID reuse + 原 codex pid 仍 alive 三重巧合（codex round 2 F2）| callback 內每次 ScreenChanged 都驗 `isCodexAlive()`（內部用 `Prober.IsAliveFor("codex", paneID)`：查 paneID 對應 pane_pid 子樹有無 codex 進程）；reused pane_pid 不在 codex 子樹 → drop |
| R8 | retry-emit 反覆執行造成 dispatcher expvar 噪音 | grace 期間每 retryInterval(500ms) 一次 = 最多 4 次（grace=2s）。`MetricProbeIntentSignalEmitted` 多+4 +`MetricProbeGraceWindowSuppressed` 多+4 可接受；devlog 看得到 cycle pattern；**無 F1 re-arm cycle**（detector 不 close channel） |
| R9 | `IsAliveFor` 內部 transient query failure（tmux 短暫無回應）| 該次 callback drop 不 set pending；下一個 callback 重試。watch tick 500ms cadence 下 transient 通常 1-2 tick 內恢復 |
| R10 | main goroutine retry-send 阻塞 channel buffer=1 | dispatcher consumeSignals 在 grace drop 後 continue loop → 立刻 read next emit → 解 buffer 阻塞；最壞情況 retry-send block 一個 dispatcher cycle = ms 級 |

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

### 4.3 Detector：`StartScreenChangeDetector`（v3，retry-emit + paneID identity gate）

```go
// internal/agent/codex/probe_intent_screen_change.go
package codex

import (
    "context"
    "sync/atomic"
    "time"

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

// screenChangeRetryInterval is the cadence at which the main goroutine
// re-attempts to emit Signal while pending=true. Set to watchPollInterval
// (500ms) so retry roughly tracks the watch loop's own observation cadence
// — no point retrying faster than the watcher can refresh its baseline.
// Tests override via SetScreenChangeRetryIntervalForTest.
var screenChangeRetryInterval atomic.Int64 // nanos

func init() {
    screenChangeRetryInterval.Store(int64(500 * time.Millisecond))
}

func currentScreenChangeRetryInterval() time.Duration {
    return time.Duration(screenChangeRetryInterval.Load())
}

// StartScreenChangeDetector watches the codex pane for top-10-line content
// change and persistently re-emits a Signal while at least one ScreenChanged
// has been observed and the codex process is still attached to the pane.
// Cancel ctx to stop the watcher and exit.
//
// Why retry-emit (v3 — codex round 2 F1 finding):
//   v2 emit-once-and-return relied on consumeSignals' F1 re-arm to retry
//   after grace drop. F1 re-arm baselines a fresh watcher, so the
//   "fast/silent approval" path (all visible diffs land within the 2s
//   grace, then no further ScreenChanged) loses the only observed signal.
//   v3 keeps the detector alive across grace; the main goroutine periodically
//   re-attempts the send, so even if the dispatcher drops the first emit,
//   the next retry after grace expiry passes through.
//
// Why isCodexAlive paneID identity gate (v3 — codex round 2 F2):
//   senderPID liveness is insufficient: a tmux server restart can reuse
//   pane id %N while the original codex process happens to still be alive.
//   isCodexAlive (production: prober.IsAliveFor("codex", paneID)) verifies
//   the pane_pid descendant tree of paneID currently contains a codex
//   process, so a reused %N attached to an unrelated shell drops.
func StartScreenChangeDetector(
    ctx context.Context,
    prober screenWatcher,
    isCodexAlive func() bool,
    paneID string,
    senderPID int,
    out chan<- agent.Signal,
) {
    var pending atomic.Bool
    sig := agent.Signal{
        Kind:      agent.ProbeIntentKindScreenChange,
        PaneAlive: true,
        PaneID:    paneID,
        SenderPID: senderPID,
    }
    cb := func(ev probe.ScreenChangeEvent) {
        if ev.Kind != probe.ScreenChanged {
            return
        }
        // F2 gate: pane_pid subtree still has a codex process? otherwise
        // we may be observing an unrelated pane that reused %N.
        if !isCodexAlive() {
            return
        }
        pending.Store(true)
    }
    prober.Watch(paneID, probe.WatchOptions{TopLines: screenChangeTopLines}, cb)

    ticker := time.NewTicker(currentScreenChangeRetryInterval())
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            prober.StopWatch(paneID)
            return
        case <-ticker.C:
            if !pending.Load() {
                continue
            }
            select {
            case out <- sig:
                // Dispatcher either applies (cancelling ctx via teardown)
                // or grace-drops; on the next ticker we retry.
            case <-ctx.Done():
                prober.StopWatch(paneID)
                return
            }
        }
    }
}
```

**retry-emit 機制細節**：

- `pending atomic.Bool`：callback 觀察到 ScreenChanged + isCodexAlive=true 後設 true。set 是 idempotent — 多個 callback fires 都 OK
- main goroutine ticker 每 retryInterval（500ms）檢查 pending。pending=false → continue（沒有 user-action 信號）
- pending=true → select-with-ctx send Signal；dispatcher grace drop → 下個 ticker 再 send；grace 過後 → applied=true → cancel ctx → main goroutine 在下次 select 看 ctx.Done → StopWatch + return
- ScreenStable event 直接忽略（不 set pending、不 emit）
- channel buffer=1（dispatcher 提供）：retry 期間 dispatcher consumeSignals 反覆 read + grace drop，buffer 不會卡（每次 send 立刻被 read）
- detector 不 close channel；ctx cancel 後 startDetector 的 wrap goroutine 才 close — F1 re-arm path 不被觸發（appliedAny=true 已退）

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
        // closure binds paneID into IsAliveFor target so the detector signature
        // stays {ctx, prober, isCodexAlive(), paneID, senderPID, out}.
        isCodexAlive := func() bool { return mod.prober.IsAliveFor("codex", paneID) }
        codex.StartScreenChangeDetector(ctx, mod.prober, isCodexAlive, paneID, senderPID, out)
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
| P1-T2 | `internal/agent/codex/probe_intent_screen_change.go` | 新檔：`screenWatcher` interface + `screenChangeRetryInterval` atomic + `StartScreenChangeDetector(ctx, prober, isCodexAlive, paneID, senderPID, out)` |
| P1-T3 | `internal/agent/codex/probe_intent_screen_change_test.go` | 表驅動 tests：(1) ScreenChanged + isCodexAlive=true → 下個 ticker emit Signal / (2) ScreenStable 忽略 / (3) isCodexAlive=false → drop（pending 不 set）/ (4) multiple ScreenChanged → pending idempotent / (5) ctx cancel before pending → ticker 路徑 ctx.Done 退 / (6) ctx cancel during retry-send → select-with-ctx ctx 路徑贏 / (7) retry across grace (用 fake retry interval + 控制 dispatcher channel consume race) — emit 多次直到 applied / (8) drift： `screenChangeRetryInterval` 是測試 seam 不影響 production 1Hz 級別 |
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
| P3-T1 | `internal/module/agent/probe_intent_dispatcher_integration_test.go` | 端到端 lifecycle：(a) waiting hook → arm → fake event → grace 過後 retry emit 通過 → status=running + teardown / (b) **A11 fast/silent approval**：waiting hook → 一個 ScreenChanged 在 grace 內 → 之後再無 event → main goroutine 持續 retry → grace 過後 retry 通過 → status=running / (c) waiting → idle hook → teardown 不 emit / (d) cross-provider switch → reconcile teardown / (e) **A12 paneID identity 防護**：fake prober 注入 IsAliveFor=false → ScreenChanged 進 callback → drop / (f) **A13 isCodexAlive transient recovery**：第一個 callback IsAliveFor=false drop，第二個 true → pending → retry-emit → 通過 |
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
| 想恢復 emit-once-and-return + dispatcher F1 re-arm | v2 採用、codex round 2 F1 已抓到 fast/silent approval re-baseline 漏；v3 改 retry-emit |
| 想恢復 isPidAlive 單獨 check（不查 paneID identity） | v2 採用、codex round 2 F2 已抓到 paneID reuse 漏；v3 改 isCodexAlive=IsAliveFor("codex", paneID) |
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
3. **F1 re-arm 互動（v3 不再依賴）** ✅ — v2 emit-once 依賴 F1 re-arm；v3 retry-emit pattern detector 自己跨 grace 持續 retry，不再走 F1 re-arm 路徑。F1 在 ScreenChange 路徑變回 dormant edge case（appliedAny=true 退；只有 detector 啟動後 0 callback 收到的奇案會走 F1 — but ScreenChange OnEntryStatus={Waiting} 退場時 F1 fix 重 arm 也只會走 case 1 noop）。
4. **`prober` 在 Init 後才 ready** ✅ — closure 用 `mod.prober` lazy-resolve；Module.New 順序與 tmux 同模式（W6-3 已驗）。

### 11.1 v3 後仍 open（給 round 3 review）

- **retry-emit 期間 channel buffer 阻塞**：dispatcher consumeSignals 在 grace drop applied=false 後 continue loop → 立即 read next emit；channel buffer=1 不卡。但若 dispatcher 因為其他原因 slow（鎖競爭）→ retry-send block 一個 dispatcher cycle。production 不應該；但 race-mode test 可能 flaky。需要設計 test fixture 控制 channel consume 節奏。
- **expvar 噪音 cap**：每 retry interval(500ms) 一次 emit + grace drop = `MetricProbeIntentSignalEmitted` 與 `MetricProbeGraceWindowSuppressed` 在 grace 期間多+4。devlog 看 cycle pattern。production OK；W6-3 spec 已類似 noise 接受。
- **A11 / A12 / A13 test 可重現性**：integration test 注入 `orchNowFn` 推進虛擬時間使 grace 過期；W6-3 既有 OR4 test pattern 直接複用。`screenChangeRetryInterval` atomic test seam 模仿 W6-3 `processDeadPollInterval`。
- **F2 paneID identity 是否完整**：v3 用 `Prober.IsAliveFor("codex", paneID)` 內部查 pane_pid 子樹有 codex 進程。tmux server restart + paneID reuse + 原 codex 仍 alive 場景下，新 pane_pid 的子樹不再有 codex（codex 物理上不在那個 pane）→ IsAliveFor=false → drop。剩下唯一可能 false-positive 的場景：**reused pane 內**有別的 codex 進程（user 自己另開了一個 codex）— 此情況 ScreenChange running 反映的是另一個 codex 的活動，但 dispatcher 將其 attribute 給原 session（waiting → running）。極端罕見但理論存在。可接受作 known limitation。

