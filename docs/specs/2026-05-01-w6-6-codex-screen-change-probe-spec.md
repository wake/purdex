# W6-6 codex permission-reply ScreenChange ProbeIntent spec

> **Status**：v4（codex round 3 review job `b4h63rkmk` 3 high findings 採納；retry-emit 撤回，回 emit-once 加 **detector-side grace gate**（drop dialog-render evidence in grace）；isCodexAlive 在 sync.Once.Do 內 emit 前重驗（修 F2 paneID identity drift）；改用 `Prober.FirstAliveAgentInTree(paneID)`（內部已用 ActivePanePID，paneID exact resolve；修 F3 PanePID vs ActivePanePID 不一致）；fast/silent approval 明列 known limitation — 由 PdxStop hook 自然 cover）
>
> **Meta-drift note**：v1 armed/ScreenStable → v2 emit-once+F1 re-arm → v3 retry-emit → v4 detector-grace+emit-once。每輪都在「dialog noise vs user-action」時間區隔上 churn；v4 視為物理約束下的收斂點（dialog 渲染 ~100ms vs user 反應 ≥1s vs grace 2s 的自然分界）。
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
- **不**用 `IdleStableTicks` / armed flag 機制（v1 撤回 — round 1 F1 quick-approval deadlock）
- **不**用 emit-once + dispatcher F1 re-arm（v2 撤回 — round 2 F1 fast/silent re-baseline 漏）
- **不**用 retry-emit pattern（v3 撤回 — round 3 F1 dialog evidence 跨 grace 誤觸）
- **v4 採 emit-once + detector-side grace gate**：detector 啟動記 `startedAt`；callback 收到 ScreenChanged 後先驗 `time.Since(startedAt) >= probeGraceWindow + 200ms`（grace 內 dialog render 期間全 drop），再驗 `isCodexAlive()`，然後 sync.Once.Do 包住 send；Once 內**第二次** isCodexAlive 重驗（防 set pending 後 paneID reuse race）。grace 過後第一個合格 ScreenChanged → emit-once → return。
- **`Prober.FirstAliveAgentInTree(paneID)` 取代 `IsAliveFor`**（v4 修 round 3 F3）：FirstAliveAgentInTree 已用 ActivePanePID，paneID `%N` exact-resolve；IsAliveFor 內部仍用 PanePID（first-pane only）對 multi-pane window 不精確 — pre-existing infra bug，開 follow-up issue 追一致性 fix（不在本 PR scope）。production: `func() bool { t, _, err := prober.FirstAliveAgentInTree(paneID); return err == nil && t == "codex" }`。

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
   - `StartScreenChangeDetector(ctx, prober, isCodexAlive, paneID, senderPID, out)`；`isCodexAlive func() bool` 注入為 closure（production: 走 FirstAliveAgentInTree；test: 注入 fake）
   - 內部用 `prober.Watch(paneID, WatchOptions{TopLines: 10}, cb)` + `now func() time.Time` test seam
   - **emit-once + detector-side grace gate**（v4）：
     1. 啟動記 `startedAt = now()`、`graceEndAt = startedAt + probeGraceWindow + 200ms`（200ms buffer 蓋 watch tick race）
     2. callback：if `now() < graceEndAt` → drop（dialog render 期間全 drop）
     3. callback：`isCodexAlive()` → false → drop
     4. `sync.Once.Do`：再次 `isCodexAlive()` 驗（防 race window）→ false → 不 emit；true → select case out<-sig: case <-ctx.Done(): close(emitted)
     5. main goroutine `select case <-emitted: case <-ctx.Done():` → `prober.StopWatch(paneID)` + return
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
| A1 | waiting 進入 → grace+200ms 過後第一個 ScreenChanged + isCodexAlive=true → emit-once → status=running → entry teardown |
| A2 | waiting 進入 → grace+200ms 過後仍無 screen change → 無 emit；status 維持 waiting；ProcessDead intent 仍 active |
| A3 | waiting → idle (PdxStop hook) → detector 因 OnEntryStatus 退出被 dispatcher cancel ctx → main goroutine `<-ctx.Done()` 退（emitted 路徑無觸發）→ StopWatch + return |
| A4 | waiting → 跨 provider 切換（agent_type 改）→ reconcileSessionActive 取消 ScreenChange entry |
| A5 | **A5/A14 dialog-period 防護**：grace 內任何 ScreenChanged → drop（不 set 任何 state、不 emit）；user 仍在看 dialog 不會誤觸 running（修 round 3 F1）|
| A6 | dispatcher cancel ctx 期間 main goroutine 在 select 內 → ctx 路徑贏 → StopWatch + return（不 send-on-closed-channel） |
| A7 | drift test 通過：startDetector switch case 數量 == supportedKinds 條目數 == ProbeIntents 宣告 Kind 集合 |
| A8 | mlab live verify §1：codex permission ask → user 按 1 批准（grace 過後）→ ≤500ms 內第一個 ScreenChanged → emit → lights running |
| A9 | mlab live verify §2：codex permission ask → user 按 2 拒絕 → PdxStop hook fires → lights idle（不誤觸 running） |
| A10 | mlab live verify §3：codex 在 waiting 時 user 主動關 pane → W6-4 ProcessDead PaneAlive=false → lights clear（ScreenChange detector 因 ctx cancel 退出） |
| A11 | **long-dialog 防護**：user 看 dialog 10s 才批准 → grace 內 dialog render drop / grace 過後 9s 期間 pane 穩定 → 不 emit；user 批准瞬間 → ScreenChanged → emit → status=running ✓ |
| A12 | **paneID identity 防護**：fake `isCodexAlive()=false`（模擬 tmux restart + paneID reuse / FirstAliveAgentInTree mismatch）→ grace 過後 ScreenChanged → drop（不 emit） |
| A13 | **emit-time identity re-check**：set sync.Once 在「callback A pass identity check 進 Once.Do」與「callback B 同時 fire」之間，模擬 paneID identity flip false → Once.Do 內第二次 isCodexAlive=false → 不 emit（修 round 3 F2）|
| A14 | **fast/silent approval — known limitation**：user 在 grace 內批准 + tool 立即完成 + 無後續變化 → grace 內 evidence drop + grace 過後無新變化 → 不 emit；PdxStop hook fires → status=idle；lights waiting → idle（跳過 running phase；可接受 secondary signal limitation）|

---

## 2. 設計約束

### 2.1 必須

- 新 detector 命名 `internal/agent/codex/probe_intent_screen_change.go`（W6-3 spec §0.1 + audit §7.1 約束 detector 歸 agent package）
- detector 公開函式 `StartScreenChangeDetector(ctx, prober screenWatcher, isCodexAlive func() bool, now func() time.Time, paneID string, senderPID int, out chan<- agent.Signal)`，與 `StartProcessDeadDetector` 對稱（`isCodexAlive` + `now` 注入 closure 便測試 — production: `func() bool { t, _, err := prober.FirstAliveAgentInTree(paneID); return err == nil && t == "codex" }`、`time.Now`）
- `screenWatcher` interface 是本 detector 包私有的 minimal contract，只暴露 `Watch(target, opts, cb)` + `StopWatch(target)` 兩個方法，**不直接 import `*probe.Prober`**——便於測試注入 fake，與 W6-3 `tmuxPaneLister` interface 同 pattern
- detector **採 emit-once + detector-side grace gate**（v4）：callback 收 ScreenChanged → 若 `now() - startedAt < probeGraceWindow + 200ms` drop / 若 `!isCodexAlive()` drop / `sync.Once.Do` 內**重驗 isCodexAlive** → select case out<-sig 配 ctx.Done → close(emitted) → main goroutine `<-emitted | <-ctx.Done()` → StopWatch + return
- callback 對 ScreenStable / 其他 Kind 直接 ignore
- main goroutine 兩 select arm 都必須 watch `<-ctx.Done()` → StopWatch + return（避免 watcher leak）
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
| R1 | ctx cancel 後 prober callback 仍 fire 一兩次（500ms tick race）| callback 內 sync.Once.Do 包住 send；Once 內 select with ctx.Done 防 send-on-closed |
| R2 | dialog 渲染期間連發 ScreenChanged | callback 內 grace gate 全 drop（now < graceEndAt）；無任何 state 改變；user 仍在看 dialog 不會誤觸 |
| R3 | sync.Once 與 ctx cancel 競賽 | Once.Do 內 select case out<-sig: case <-ctx.Done(): — ctx 贏 → 不 emit；out 贏 → close(emitted)；main goroutine `select <-emitted | <-ctx.Done()` 任一觸發 StopWatch + return |
| R4 | sync.Once 包住 send 後 callback 又 fire | Once 已 fire → 後續 Do 是 no-op；多餘 callback 不副作用 |
| R5 | W6-3 ProcessDead intent 與 W6-6 ScreenChange intent 同時 active | 不同 Kind，dispatcher per-(session, kind) 分槽；reconcile 只看 declaredKinds，不衝突 |
| R6 | `prober.Watch(paneID, ...)` 與 W3-revert 後 production caller 為 0 的事實 | W6-6 是 W3 撤回後 `Prober.Watch` 的**第一個 production caller**；測試用 fake prober 即可，production 用 module.prober |
| R7 | tmux server restart + paneID reuse 場景（round 2 F2 + round 3 F2）| callback 內每次 ScreenChanged 都驗 `isCodexAlive()`；sync.Once.Do 內**第二次**驗（防 set pending 後 race）；FirstAliveAgentInTree 用 ActivePanePID 對 paneID exact resolve（解 round 3 F3 PanePID 不一致）|
| R8 | dialog noise grace gate buffer=200ms 為何 | dialog 渲染通常 <100ms（一次 atomic redraw），但 watch 500ms tick 邊界 + race 緩衝 200ms safer；總 gate = 2.2s；user 反應 typical ≥1s 加 grace 共 ~3s 才 emit |
| R9 | `FirstAliveAgentInTree` transient query failure（tmux 短暫無回應）| 回 (("", 0, err))；callback 視為 false drop；下個 ScreenChanged 重試 |
| R10 | **fast/silent approval（known limitation）** | grace 內所有 evidence drop + grace 過後無新變化 → 不 emit；PdxStop hook 自然把 status 帶 idle；lights waiting → idle 跳過 running phase；user 觀感 secondary signal 可接受 |
| R11 | `Prober.IsAliveFor` 內部 PanePID 不一致（round 3 F3 pre-existing infra bug）| W6-6 不用 IsAliveFor，改 FirstAliveAgentInTree 規避；開 follow-up issue 追 IsAliveFor 一致性 fix（不在本 PR scope）|

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

### 4.3 Detector：`StartScreenChangeDetector`（v4，emit-once + detector-side grace gate）

```go
// internal/agent/codex/probe_intent_screen_change.go
package codex

import (
    "context"
    "sync"
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

// screenChangeGraceBuffer pads the dispatcher's probeGraceWindow (2s) before
// the detector starts honouring ScreenChanged. The buffer covers watch tick
// boundary races (Watch's first capture happens on the first 500ms tick) and
// dialog redraw under terminal resize / wrapped text. Tests override via the
// `now` injection seam to fast-forward virtual time.
const screenChangeGraceBuffer = 200 * time.Millisecond

// StartScreenChangeDetector watches the codex pane for top-10-line content
// change and emits a single Signal once the dispatcher's probeGraceWindow
// (plus a small buffer) has elapsed AND a qualifying ScreenChanged is observed
// while the pane still hosts a codex process. Cancel ctx to stop the watcher
// and exit early.
//
// Why detector-side grace gate (v4 — codex round 3 F1 finding):
//   v3 retry-emit re-sent the signal after dispatcher grace expiry, but
//   pending=true was set by dialog-render ScreenChanged events that landed
//   inside grace. Once grace expired, retry mapped that pre-grace evidence
//   to running even when the user had not approved yet (long-dialog case).
//   v4 drops every ScreenChanged inside (probeGraceWindow + buffer) at the
//   detector level so dialog render evidence cannot propagate forward.
//
// Why double isCodexAlive check (v4 — codex round 3 F2):
//   v3 only checked isCodexAlive once when setting pending. Between the
//   initial pass and the actual emit, the pane could be reused by an
//   unrelated process. v4 verifies twice: first in the callback to short-
//   circuit dialog noise from a reused pane, second inside sync.Once.Do
//   immediately before the emit so any race between the two checks is
//   caught.
//
// Why FirstAliveAgentInTree replaces IsAliveFor (v4 — codex round 3 F3):
//   prober.IsAliveFor uses tmux PanePID(target), which list-panes returns
//   the FIRST listed pane of the target's window — wrong for paneID %N in
//   multi-pane windows. FirstAliveAgentInTree already uses ActivePanePID
//   for paneID exact resolution. Production binds:
//     isCodexAlive := func() bool {
//         t, _, err := prober.FirstAliveAgentInTree(paneID)
//         return err == nil && t == "codex"
//     }
//   The IsAliveFor inconsistency is pre-existing infrastructure and is
//   tracked in a follow-up issue (out of scope for W6-6 PR).
//
// fast/silent approval is a known limitation (spec §2.3 R10): user approves
// inside grace and tool runs silently → no post-grace ScreenChanged → no
// emit → PdxStop hook lands the status on idle, lights skip the running
// phase. Acceptable secondary-signal tradeoff vs the alternative (long-
// dialog false positive).
func StartScreenChangeDetector(
    ctx context.Context,
    prober screenWatcher,
    isCodexAlive func() bool,
    now func() time.Time,
    paneID string,
    senderPID int,
    out chan<- agent.Signal,
) {
    startedAt := now()
    graceEndAt := startedAt.Add(probeGraceWindowForScreenChange + screenChangeGraceBuffer)

    emitted := make(chan struct{})
    var once sync.Once
    sig := agent.Signal{
        Kind:      agent.ProbeIntentKindScreenChange,
        PaneAlive: true,
        PaneID:    paneID,
        SenderPID: senderPID,
    }
    cb := func(ev probe.ScreenChangeEvent) {
        if ev.Kind != probe.ScreenChanged {
            return // ignore ScreenStable / other Kinds
        }
        if now().Before(graceEndAt) {
            return // dialog-render era; drop unconditionally
        }
        if !isCodexAlive() {
            return // pane no longer hosts codex (reused / restarted)
        }
        once.Do(func() {
            // Re-verify identity inside the critical section to close the
            // race between the outer isCodexAlive check and emit.
            if !isCodexAlive() {
                return
            }
            select {
            case out <- sig:
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

> `probeGraceWindowForScreenChange` 是 detector 內部 const，等於 `probeGraceWindow`（2s）的 mirror（avoid cross-package private constant import；W6-3 ProcessDead 也類似 mirror `processDeadPollInterval`）。codex package 不 import `probe_orchestrator.go` 私有常數，使用本地 const 並由測試 + spec §2.3 R8 文字註明常數同步。

**emit-once + detector grace gate 機制細節**：

- `startedAt = now()`：detector 啟動時間，由注入的 `now` seam 控制（test 用虛擬時鐘）
- `graceEndAt = startedAt + probeGraceWindow + 200ms buffer`：總 dialog noise drop 區間
- callback：先驗 grace（時間 gate）→ 再驗 isCodexAlive（identity gate）→ Once.Do 內**重驗** isCodexAlive（race close）→ select-with-ctx send → close(emitted)
- main goroutine `<-emitted | <-ctx.Done()`：任一觸發 → StopWatch + return
- ScreenStable / 其他 Kind 直接 ignore（不 set 任何 state）
- detector 不 close out channel（dispatcher startDetector wrap goroutine 在 detector return 後 close）；F1 re-arm 路徑：emit 落在 grace 過後 → applied=true → ctx cancel → wrap close(out) → consumeSignals appliedAny=true → F1 不 fire

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
        // FirstAliveAgentInTree internally uses tmux ActivePanePID(target) so
        // it resolves paneID %N exactly (vs IsAliveFor which uses PanePID =
        // first-pane-only — pre-existing inconsistency tracked in follow-up).
        isCodexAlive := func() bool {
            t, _, err := mod.prober.FirstAliveAgentInTree(paneID)
            return err == nil && t == "codex"
        }
        codex.StartScreenChangeDetector(ctx, mod.prober, isCodexAlive, time.Now, paneID, senderPID, out)
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
| P1-T2 | `internal/agent/codex/probe_intent_screen_change.go` | 新檔：`screenWatcher` interface + `screenChangeTopLines` + `screenChangeGraceBuffer` const + 內部 `probeGraceWindowForScreenChange` mirror + `StartScreenChangeDetector(ctx, prober, isCodexAlive, now, paneID, senderPID, out)` |
| P1-T3 | `internal/agent/codex/probe_intent_screen_change_test.go` | 表驅動 tests：(1) grace 過後 ScreenChanged + isCodexAlive=true → emit / (2) ScreenStable 忽略 / (3) **dialog era drop**：grace 內 ScreenChanged → drop（無 emit、emitted 未 close）/ (4) **isCodexAlive false**：grace 過後 ScreenChanged + isCodexAlive=false → drop / (5) **identity flip race**：first check pass、Once.Do 內 second check fail → 不 emit / (6) **multiple post-grace ScreenChanged**：sync.Once 保證只 emit 一次 / (7) ctx cancel before grace end → main goroutine StopWatch + return / (8) ctx cancel during emit select → ctx.Done 路徑贏（不 send-on-closed） |
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
| P3-T1 | `internal/module/agent/probe_intent_dispatcher_integration_test.go` | 端到端 lifecycle：(a) waiting hook → arm → 注入 fake `now` 跨過 grace → ScreenChanged → emit → status=running + teardown / (b) **A11 long-dialog**：grace 內 ScreenChanged drop / grace 過後 9s 無變化 → user 批准 → ScreenChanged → emit → running / (c) **A14 fast/silent**：grace 內 ScreenChanged + 後續 idle hook → no emit + status=idle（PdxStop cover）/ (d) waiting → idle hook → teardown 不 emit / (e) cross-provider switch → reconcile teardown / (f) **A12 identity 防護**：fake isCodexAlive=false → drop / (g) **A13 identity flip race**：注入 isCodexAlive 第一次 true、第二次 false → Once.Do 內 second check 阻 emit |
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
| 想恢復 retry-emit pattern（pending atomic + main goroutine ticker）| v3 採用、codex round 3 F1 已抓到 dialog-render evidence 跨 grace 誤觸 long-dialog；v4 改 detector-side grace gate |
| 想用 `IsAliveFor("codex", paneID)` | round 3 F3：IsAliveFor 內部 PanePID 對 multi-pane window 不精確；v4 改用 FirstAliveAgentInTree |
| 想 detector 自己解 fast/silent approval | 物理約束（dialog vs user-action 在 hash 層級無法區分）+ round 3 已驗每嘗試都引入新 race；接受 PdxStop 自然 cover 作 known limitation |
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
3. **F1 re-arm 互動（v4 dormant 不依賴）** ✅ — v4 emit-once + detector grace gate；emit 落在 grace 過後 → applied=true → ctx cancel；F1 不 fire。
4. **`prober` 在 Init 後才 ready** ✅ — closure 用 `mod.prober` lazy-resolve；Module.New 順序與 tmux 同模式（W6-3 已驗）。

### 11.1 v4 後仍 open（給 round 4 review）

- **`probeGraceWindowForScreenChange` mirror 與 `probeGraceWindow` 漂移**：codex package 不能 import internal/module/agent 私有常數，detector 內部用 mirror const = 2s。若 module 端改 `probeGraceWindow` 而 detector 端忘了同步 → silent drift。處置：drift unit test（同 W6-3 spec 對 `processDeadPollInterval` 的處理 — 直接 cross-package import const 比對；codex package 用 reflect 或 explicit equality test）。
- **fast/silent approval 跳過 running phase**：v4 接受作 known limitation。考慮 spec drift signal — 若後續 codex permission flow 改成 hook 通知 approval，本 detector 可整個拿掉。
- **`FirstAliveAgentInTree` 已用 ActivePanePID**：spec 假設 — 需在 P1-T2 實作前 read liveness.go line 41-100 確認；如該函式 fallback 仍走 PanePID，需擴 spec scope 改 ActivePanePID 路徑。
- **IsAliveFor 一致性 follow-up issue**：本 PR 不修；開 GH issue 描述 PanePID vs ActivePanePID 對 paneID target 行為差異，建議 IsAliveFor 改 ActivePanePID 並加 multi-pane test。Issue 標 W6-6 spec round 3 F3 derive。

