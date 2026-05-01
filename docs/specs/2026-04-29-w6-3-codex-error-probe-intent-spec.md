# W6-3 codex error ad-hoc ProbeIntent spec

> **Status**：v6 final（codex round 1-5 共 12 P1+P2 採納；user 同意 (D) — round 5 P1 微修 + 不再派 round 6 直接進 plan）
> **Worktree**：`.claude/worktrees/lights-w6-3-codex-error` / branch `worktree-lights-w6-3-codex-error`
> **Base**：`origin/main` @ alpha.261（W3+W4 reverted ProbeProfile framework + monitor top processes）
> **依賴**：W1 audit `docs/specs/2026-04-28-hook-status-audit-spec.md` §6/§7 / lights-rebuild-spec `docs/specs/2026-04-23-lights-rebuild-spec.md` §8.2 / fix-spec `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3
> **後續**：W6-1/2/6 cc + codex TUI 觀察（新增 ScreenChange detector kind 時 generalize）— **W6-4 已併入本 PR**（詳 §0.2）

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

### 0.2 重要 spec drift 修正（與 W1 audit §7 假設不符；codex review b36pap7jc 已確認）

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

### 0.2.1 W6-3 + W6-4 合併 first PR（per b36pap7jc ATK-3 / DEF-1）

**原 spec drift 決議**：採 §9.2 選項 B — W6-3（error）與 W6-4（clear）合併 first PR。

**理由**：

1. **detector 共用** — process_dead detector 已能同時觀察 pane existence；拆兩 PR 等於造兩個幾乎相同 detector
2. **interface 穩定性** — W6-3 first PR 既要 finalize ProbeIntent interface，必須包含 W6-4 共用 minimum context（PaneAlive / PaneID / SenderPID），否則第二 PR 立刻改 signature 是 framework drift（fix-spec §3 要避免的徵兆）
3. **避免 user-visible false alarm** — 拆分意味 W6-3 ship 後 user 主動 close pane（codex running/waiting）會被誤標 error，且因 ErrorGuard 一直 pin 到下次 hook；這不是「小折扣」，是 user-visible 缺陷

**W6-3 + W6-4 合併後 first PR 實際範圍**：

- ProbeIntent interface finalize（含 PaneAlive / PaneID / SenderPID 在 Signal 內，給未來 Kind 同樣的最小 context）
- codex provider 宣告唯一 ProbeIntent，OnSignal 區分：pane alive → `error` / pane gone → `clear`
- detector 觀察 pane id 是否還在 tmux pane list（不是 session target — 細節 §4.2）
- module dispatcher + persist + #698 daemon-restart recovery 一次到位

### 0.3 與 fix-spec / W1 audit / b36pap7jc 對齊

| 設計要點 | 來源 | 落地處 |
|---|---|---|
| ProbeIntent 走 `ProbeIntentProvider` interface（signal → status mapping） | lights-rebuild-spec §8.2 / audit §7.1 | 本 spec §3 |
| 不偽裝為 hook event（probe 與 hook 是兩條獨立 channel） | audit §7.1 | 本 spec §3 / §5 |
| 不做跨 agent 中央 liveness watcher / 中央規則 | audit §7.1 / fix-spec §3 | 本 spec §3 / §5 |
| 不做 always-on probe / generic ProbeProfileProvider | audit §7.1 / fix-spec §3 | 本 spec §5（per-agent gating + 條件式 watch） |
| Detector 歸 `internal/agent/codex/probe_intent_*.go` | audit §7.1 | 本 spec §4 |
| daemon module 只負責 plumbing | audit §7.1 | 本 spec §5 |
| ProbeIntent interface lazy 設計（W6-3 finalize 含 W6-4 共用 minimum context） | audit §7.1 / b36pap7jc DEF-1 | 本 spec §3.2 |
| 五大 bloat 徵兆 self-check | audit §7.1.3 / `feedback_skeleton_convergence` | 本 spec §2.3 |
| daemon-restart watcher recovery (issue #698) | audit §7.1.2 | 本 spec §6 |
| **stale-callback guard 用 active-set strategy 注入** | b36pap7jc ATK-1 | 本 spec §5.2 |
| **detector 用 pane id target，非 session target** | b36pap7jc ATK-2 / PR #638 教訓 | 本 spec §4.2 |
| **W6-3 + W6-4 合併 first PR**（避免 ship 已知 false alarm） | b36pap7jc ATK-3 | 本 spec §0.2.1 / §1.1 |
| **reuse `agent_frames.pid + pane_id`，不新增 schema 欄位** | b36pap7jc FH-1 + self-check | 本 spec §4.3 / §6.2 |
| **單一 m.mu 統管 active-set（含 activeProbeIntents）** | by2z79ouc ATK-1 | 本 spec §5.1 / §5.2 |
| **startDetector compare-and-arm（m.mu 內 re-read currentStatus）** | by2z79ouc ATK-2 | 本 spec §5.4 / §6.4 |
| **OnSignal mapping callback in applyProbeGuards（guard 後才 invoke）** | by2z79ouc DEF-1 | 本 spec §3.2 / §5.2 / §5.4 |
| **detector `isPidAliveFn` package var injection** | by2z79ouc FH-1 | 本 spec §4.2 |
| **單一 lifecycle helper + active target mismatch 第五 case** | round 3 P1 | 本 spec §5.4 |
| **applyStatus 入口 reconcileSessionActive（cross-provider cleanup）** | round 4 P1 | 本 spec §5.4 |
| **consumeSignals applied 後 re-run applyStatus（probe-applied teardown）** | round 5 P1 | 本 spec §5.4 |

---

## 1. 範圍與目標

### 1.1 在範圍（W6-3 + W6-4 合併）

1. `ProbeIntentProvider` 新 optional interface 落 `internal/agent/provider.go`
2. `ProbeIntent` / `ProbeIntentKind` / `Signal` 結構 finalize；Signal 含 W6 共用最小 context（`Kind` / `PaneAlive bool` / `PaneID string` / `SenderPID int`）— 讓未來 Kind 沿用同一 context shape
3. codex provider 宣告唯一 `ProcessDead` ProbeIntent；OnSignal 依 `PaneAlive` 區分 `error` / `clear`
4. codex `probe_intent_process_dead.go` detector：polling + `IsPidAlive(senderPID)` + 「pane id 還在 tmux pane list」雙檢查
5. 新檔 `internal/module/agent/probe_intent_dispatcher.go`：per-agent ProbeIntent gating + per-(session, kind) watcher lifecycle + active-set 自管（不共用 `activeWatchers`）
6. `applyProbeGuards` 抽 free function — guard / re-check active-set 透過注入 strategy（ProbeIntent 路徑用 `activeProbeIntents`，ScreenChange 路徑沿用 `activeWatchers`）
7. `manageActivityWatch` 改造：接 ProbeIntent 啟停（per-agent，**不**做跨 agent 規則）
8. Replay recovery：reuse 既有 `agent_frames.pid + pane_id`（無需新欄位）；`Module.Start` 後重建 ProbeIntent watcher
9. drift test：每 ProbeIntent 宣告 vs runtime 實際 dispatch 路徑對齊
10. dev log 補：`[probe-intent]` step kind（W4 trace pipeline 第 6 條 chain log）
11. mlab live verify：codex 進程被 kill（pane alive） → lights 變 `error`；codex pane 被 close → lights 變 `clear`

### 1.2 不在範圍

- W6-1/2/6 cc + codex TUI 觀察（不同 detector kind，需新增 `ScreenChange` Kind 時再 generalize）
- W6-5 opencode busy/retry（首選 plugin 補 mapping，issue #661）
- Always-on probe 復辟（W3 已撤；本 spec 嚴禁）
- ScreenChange watcher 機制改造（W6-3 純 process_dead detector，與 ScreenChange watcher 平行 lifecycle；只共用 guard helpers）
- TraceStore schema 改動（dev log 走現有 trace pipeline 加 step kind 即可）
- Inspector UI（W7 範圍）

### 1.3 為何要做

- W5-4 + W5-5 燈號 bug：codex `error` / `clear` 物理不可達是 user-visible 缺口
- 為 W6 系列鋪 interface — W6-3 first PR finalize 讓 W6-1/2/6 直接沿用
- 為 #698 daemon-restart 場景補 platform plumbing — 否則 W6 ProbeIntent 在 daemon 重啟後直到下個 hook 才重新掛，多數 W5/W6 ship 後 daemon-restart 體驗破功

---

## 2. 設計約束

### 2.1 必守

1. ✅ ProbeIntent 由 codex provider 透過 `ProbeIntentProvider` 宣告；detector 實作歸 `internal/agent/codex/`
2. ✅ daemon module 只負責 plumbing（dispatcher / watcher lifecycle / hook → status → broadcast）
3. ✅ ProbeIntent gating 為條件式（status ∈ {running, waiting} 且 senderPID + paneID 已知時 watch；否則 unwatch）
4. ✅ Interface lazy 設計 — W6-3 first PR finalize 唯一 `ProcessDead` Kind；後續 PR 加新 Kind 時 extend struct（Signal 既有 fields 必保持向後相容）
5. ✅ probe channel 與 hook channel 獨立 — probe 推論、hook 權威；`recordHookAt` graceWindow 機制保留
6. ✅ ErrorGuard 維持：`currentStatus == StatusError` 時 probe 不再覆寫
7. ✅ Stale-callback guard（per-route）：ProbeIntent 路徑必須 re-check `activeProbeIntents[session][kind] && agentType == 期望 && generation == 期望` 三條件；ScreenChange 路徑維持 re-check `activeWatchers[session] == agentType`；**兩條 active-set 共享 m.mu**（per by2z79ouc ATK-1）— `applyProbeGuards` 透過 strategy 注入正確的 active-set checker，但 checker 與 module mutation 在同一 mutex 內，no cross-lock
8. ✅ Detector goroutine 不持有 `m.mu`；channel emit 後 dispatcher 內 consume → 走 `applyProbeGuards`；Signal channel buffer = 1（detector emit 一次後退出）；**鎖序：m.mu only**（per by2z79ouc ATK-1：activeProbeIntents 移進 m.mu 保護，dispatcher 不另持 mutex）
9. ✅ ProbeIntent state 必須含 pane id；detector 觀察 pane existence 用 pane id 為 tmux target（不是 session target）— 防 multi-pane window 取錯 pane PID（per PR #638 教訓 / b36pap7jc ATK-2）
10. ✅ ProbeIntent contract（per by2z79ouc DEF-1）：dispatcher 在 **guard 通過後**才 invoke OnSignal（mapping callback）；OnSignal 不會看到被 stale guard / graceWindow / ErrorGuard drop 的 signal — provider 即使有 side effect 也安全
11. ✅ ProbeIntent compare-and-arm（per by2z79ouc ATK-2）：lifecycle 在 m.mu 內 re-read `currentStatus` + 比對 `OnEntryStatus`，只在 live status 仍 match 才 record active；防 replay 期間 hook race 後 arm stale watcher
12. ✅ Detector pidAlive 注入（per by2z79ouc FH-1）：codex 包用 `isPidAliveFn` package var（沿 module 包既有 `orchNowFn` / `isPidAliveFn` pattern）；test 覆寫後 cleanup
13. ✅ ProbeIntent active target re-arm（per round 3 P1）：lifecycle helper 在 `wasActive && shouldActive` 時 m.mu 內比對 `(agentType, paneID, senderPID)` 是否與 active entry 一致；不一致 → cancel 舊 detector + 計算 new generation + 寫入新 entry；防同 status 內 codex 換 pid/pane（restart / multi-pane 切換）後舊 detector 蓋掉新狀態
14. ✅ Cross-provider reconcile（per round 4 P1）：`applyStatus` 入口先 `reconcileSessionActive`，cancel/delete 任何 `(agentType, kind)` 不再屬於新 provider 宣告集合的 active entry；防 session 切到無 ProbeIntent 的 provider（cc / opencode）後舊 codex detector 仍 emit 蓋掉新狀態
15. ✅ Probe-applied teardown（per round 5 P1）：`consumeSignals` 在 `applyProbeGuards` 回傳 `applied=true` 後 re-run `applyStatus(session, agentType, newStatus)` — 讓 lifecycle helper 看到 newStatus 不在 OnEntryStatus（error / clear）走 case 3 cancel+delete；防 active entry 與 currentStatus 不一致（detector goroutine 已退出但 activeProbeIntents 仍含舊 entry → map leak / 後續 lifecycle 誤以為已 armed）

### 2.2 禁忌

1. ❌ probe 偽裝為 hook event（**不要**寫「fire 合成 StopFailure-equivalent」之類的描述）
2. ❌ 跨 agent 中央 liveness watcher / 中央規則（如 generic `any → process_dead → error`）
3. ❌ generic `ProbeProfileProvider` / always-on policy（fix-spec §3 已撤；本 spec 不重蹈）
4. ❌ 把 codex hook handler 既有 working code 改寫成 ProbeIntent 形式（refactor working code without functional reason）
5. ❌ parallel registry（除 codex provider 已有的 `ProbeIntents()` 之外不另起 registry）
6. ❌ 為「未來可能有」的 detector kind 預先抽象（lazy；只有 ProcessDead 一個 kind）

### 2.3 五大 bloat 自我檢查（per `feedback_skeleton_convergence`）

每個 PR commit 前 self-check：

| 徵兆 | W6-3+W6-4 self-check |
|---|---|
| 把 working code 變 data | 不動 codex `events.go` / `status.go` / `hooks.go`；ProbeIntent 是新增 capability，不重寫既有 hook 路徑；不新增 schema 欄位（reuse `agent_frames.pid + pane_id`） |
| parallel registry | 不加新 registry；ProbeIntent 由 provider 宣告 → dispatcher 在 status 變更時讀取；`activeProbeIntents` 是 lifecycle 狀態（不是 declaration registry） |
| 統一抽象（generic framework） | `ProbeIntentKind` 只有 `ProcessDead` 一個 const；不預定義 `ScreenChange` / `LogTail` 等；Signal 預留 `PaneAlive` / `PaneID` / `SenderPID` 是 W6-3+W6-4 的當下需求，**非**為未來 Kind 預留 |
| refactor working code | 不重寫 ScreenChangeWatcher / probeOrchestrator；`applyProbeGuards` 抽 free function 是 ScreenChange 路徑既有邏輯的 mechanical extraction（行為零變動），不是 refactor without functional reason — 用 `applyProbeGuards` 後 ProbeIntent 路徑能 reuse 同一 guard 是 functional 必要 |
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

### 3.2 `ProbeIntent` struct（W6-3+W6-4 finalize；含未來 Kind 共用 minimum context）

```go
// ProbeIntent is one probe-driven transition declared by an agent provider.
//
// Lifecycle (driven by daemon dispatcher):
//   1. Status changes to a value listed in OnEntryStatus → dispatcher resolves
//      pane id + senderPID from the active frame, then dispatches to the
//      per-Kind detector goroutine
//   2. Detector observes runtime state (per Kind) and emits Signal events
//   3. Dispatcher applies guards (see §5.3) then OnSignal(sig) → Status
//   4. Status changes to a value NOT in OnEntryStatus → dispatcher stops
//      detector (cancel ctx) and frees per-(session, kind) state
//
// W6-3 first PR finalize: one Kind = ProbeIntentKindProcessDead. Future Kind
// additions (W6-1/2/6 ScreenChange) MUST keep the existing Signal fields
// backward compatible (add fields, don't repurpose).
type ProbeIntent struct {
    // Kind classifies the detector. W6-3 finalize: only ProbeIntentKindProcessDead.
    // Subsequent W6 PRs MAY introduce additional Kind constants; existing Kind
    // semantics and Signal field semantics MUST remain stable.
    Kind ProbeIntentKind

    // OnEntryStatus is the set of currentStatus values that gate this intent
    // active. Detector starts on entry to any of these and stops on exit to any
    // status outside the set.
    //
    // Example (W6-3+W6-4): {StatusRunning, StatusWaiting} — codex process_dead
    // inference only makes sense while codex is supposed to be doing work.
    OnEntryStatus []Status

    // OnSignal maps a detector signal to the new Status. Empty Status returned
    // by OnSignal means "drop this signal" (detector observed transient state
    // that doesn't warrant a transition).
    //
    // Contract (per by2z79ouc DEF-1): dispatcher invokes OnSignal as a
    // mapping callback INSIDE applyProbeGuards, AFTER the stale-callback
    // guard + graceWindow guard pass. OnSignal therefore never sees signals
    // that should have been dropped by hook authority. ErrorGuard +
    // transition gate run AFTER OnSignal returns (they need newStatus to
    // compare against currentStatus). Provider may safely log / count in
    // OnSignal because pre-guard-survived signals are guaranteed.
    OnSignal func(Signal) Status
}

// ProbeIntentKind is the detector category. W6-3 finalize introduces one Kind;
// future PRs MAY add more. Use a string type so test fixtures can declare
// expected kinds without import cycles.
type ProbeIntentKind string

const (
    // ProbeIntentKindProcessDead — detector polls senderPID + observes pane
    // existence; emits one Signal with PaneAlive set when the agent process
    // is no longer in the pane PID tree (W6-3+W6-4 combined: PaneAlive=true
    // → caller maps to error; false → caller maps to clear).
    ProbeIntentKindProcessDead ProbeIntentKind = "process_dead"
)

// Signal is the runtime observation emitted by a detector. W6-3 finalize
// includes the minimum context W6-3+W6-4 both need; future Kind additions
// MAY add fields but MUST keep existing field semantics stable.
//
// Field rationale (per b36pap7jc DEF-1 — preventing immediate signature churn
// on the next W6 PR):
//   - Kind: required for OnSignal type discrimination
//   - PaneAlive: W6-3+W6-4 binary distinction; future ScreenChange Kinds may
//     leave it unconditionally true (they observe pane content, not pane life)
//   - PaneID: explicit pane id — detector resolves & captures this when the
//     intent arms; OnSignal receives the same value (used by trace
//     observability and for downstream future-Kind detectors that act on
//     pane-scoped state)
//   - SenderPID: the codex sender pid resolved from frame state; carried on
//     Signal so OnSignal handlers can log without re-querying
type Signal struct {
    Kind       ProbeIntentKind
    PaneAlive  bool
    PaneID     string
    SenderPID  int
}
```

**Lazy 設計理由**（per audit §7.1 / `feedback_skeleton_convergence` / b36pap7jc DEF-1）：

- `ProbeIntent` 只兩個 owner-supplied 欄位（`OnEntryStatus` / `OnSignal`）+ 一個 `Kind` discriminator
- `Signal` 4 欄位都是 W6-3+W6-4 當下需求（不是預留為未來 Kind）；future Kind 加新 fields 但既有 fields 不改語意
- `ProbeIntentKind` 只有一個 const — 後續 PR extend 時加 const 即可
- 不抽 `Detector` interface — 每 Kind 一個 detector goroutine，dispatcher 用 switch on Kind 啟停（switch 範圍小，比 interface 抽象更直白）

### 3.3 First PR 範圍：W6-3 + W6-4 共用 `ProcessDead`

合併後的 codex 宣告：

- `ProbeIntentKindProcessDead`
- detector：codex 實作 `probe_intent_process_dead.go`
- 觀察雙條件：
  - `probe.IsPidAlive(senderPID)` — 進程級
  - pane id 是否仍在 tmux pane list（detector 內部用 `tmux list-panes -F '#{pane_id}'` 檢查 paneID 是否還在當前 session 列表中）
- gating：`OnEntryStatus = {StatusRunning, StatusWaiting}`
- mapping：
  ```go
  OnSignal = func(sig Signal) Status {
      if sig.Kind != ProbeIntentKindProcessDead { return "" }
      if sig.PaneAlive { return StatusError }   // W6-3
      return StatusClear                         // W6-4
  }
  ```

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

// onProcessDead maps a ProcessDead signal to the recovery status, splitting
// W6-3 (PaneAlive=true → Error) and W6-4 (PaneAlive=false → Clear) on the
// pane existence observation that detector captured.
func onProcessDead(sig agent.Signal) agent.Status {
    if sig.Kind != agent.ProbeIntentKindProcessDead {
        return ""  // dispatcher should not invoke OnSignal for mismatched Kind
    }
    if sig.PaneAlive {
        return agent.StatusError
    }
    return agent.StatusClear
}
```

### 4.2 Detector 機制（`internal/agent/codex/probe_intent_process_dead.go`，新檔）

**Polling-based detection**（不用 platform-specific kqueue / pidfd）：

detector goroutine 接收 dispatcher 提供的 `(senderPID, paneID)` snapshot，每 `processDeadPollInterval` 醒來檢查兩個獨立 invariant：

1. **進程級 alive**：`probe.IsPidAlive(senderPID)` — 直接 `syscall.Kill(pid, 0)`，cross-platform、cheap
2. **pane 級 alive**：透過 `tmuxPaneLister.HasPane(paneID)` 檢查 paneID 是否仍出現在當前 tmux pane list（**不**用 session target 走 `IsAliveFor`，避免 multi-pane window 誤判 — per b36pap7jc ATK-2）

**Signal emission 邏輯**：

| `IsPidAlive(senderPID)` | `HasPane(paneID)` | 動作 |
|---|---|---|
| true | true | 全活，繼續 poll |
| true | false | 進程在但 pane 不見（罕見：tmux pane killed but process re-parented to init）→ 視為 `pane_alive=false` 路徑 emit |
| false | true | 進程死、pane 還在 → emit `Signal{PaneAlive: true}`（W6-3 error） |
| false | false | 進程死、pane 也不見 → emit `Signal{PaneAlive: false}`（W6-4 clear） |

進程死即 emit 一次後 detector 結束（dispatcher 已停 watcher，不再 poll）。

**Polling 頻率**：`1 * time.Second`。理由：

- codex crash 復原需求對 latency 不敏感（user 看到 codex 不在了，等 1s 變色可接受）
- 1Hz × `tmux list-panes` + `syscall.Kill(pid, 0)` 成本可忽略
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

// processDeadPollInterval is the polling cadence for pidAlive + HasPane.
// Exported as a package var so tests can override — production code never
// mutates it.
var processDeadPollInterval = 1 * time.Second

// isPidAliveFn is the injectable pid-liveness probe. Production points at
// probe.IsPidAlive (which is a syscall.Kill(pid, 0) wrapper). Tests override
// to control the alive/dead state per call (per by2z79ouc FH-1 — the
// 4 pidAlive×paneAlive combinations require deterministic injection rather
// than spawning real processes).
//
// Same pattern as internal/module/agent/probe_orchestrator.go's
// `isPidAliveFn` / `orchNowFn` / `recordHookAtHook` etc.
var isPidAliveFn = probe.IsPidAlive

// StartProcessDeadDetector is the codex-side detector for
// ProbeIntentKindProcessDead. It polls senderPID + paneID; on first dead-
// confirmed tick it emits one Signal carrying PaneAlive observation and
// returns. Cancel ctx to stop early (e.g. on session rename / status exit
// OnEntryStatus).
//
// paneLister is the minimal contract the detector needs (HasPane(paneID)).
// Tests inject a recording fake. Production wires *tmux.Executor.
//
// Caller is expected to consume the emitted Signal via dispatcher's
// applyProbeGuards (graceWindow / stale-callback / ErrorGuard / transition
// gate / OnSignal mapping all live there).
func StartProcessDeadDetector(
    ctx context.Context,
    paneLister tmuxPaneLister,
    paneID string,
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
            pidAlive := isPidAliveFn(senderPID)
            paneAlive := paneLister.HasPane(paneID)
            if pidAlive && paneAlive {
                continue  // both alive, keep polling
            }
            if pidAlive && !paneAlive {
                // process re-parented but pane gone; treat as pane-gone path
                pidAlive = false
            }
            select {
            case out <- agent.Signal{
                Kind:      agent.ProbeIntentKindProcessDead,
                PaneAlive: paneAlive,
                PaneID:    paneID,
                SenderPID: senderPID,
            }:
            case <-ctx.Done():
            }
            return
        }
    }
}

// tmuxPaneLister is the minimal contract this detector requires for pane
// existence checks. Implementation: tmux.Executor.HasPane(paneID) — wraps
// `tmux list-panes -a -F '#{pane_id}'` and scans for the target id.
//
// Per b36pap7jc ATK-2: do NOT delegate to probe.Prober.IsAliveFor with a
// session target — PanePID(session) on multi-pane window resolves to first
// pane, which is the wrong pane for non-first siblings.
type tmuxPaneLister interface {
    HasPane(paneID string) bool
}
```

### 4.3 SenderPID + PaneID 來源（**不**新增 schema 欄位）

**Hook 路徑 hydration**（per b36pap7jc FH-1 修正）：

`EventRequest`（`internal/module/agent/handler.go:91, 145`）已強制要求 `SenderPID != 0`。daemon 處理每筆 hook 時 frame_ops 已將 `(pane_id, agent_type, pid, ppid, process_start_time, status, ...)` 寫入 `agent_frames` 表，**`agent_frames.pid` 即是 codex sender 的進程 PID**（daemon 觀察 tmux pane PID tree + identifier 找到的 codex process — 在 codex 場景下與 hook payload `sender_pid` 一致；frame.pid 是 daemon 已驗證版本的 senderPID）。

**Replay 路徑 hydration**：

`agent_frames.pane_id` + `agent_frames.pid` 已是 frame schema 既有欄位，**不需新增 column**：

- ProbeIntent dispatcher 啟動 detector 時，從 frame projection 取 top frame：`(paneID, pid) = projection.TopFrame.PaneID, projection.TopFrame.PID`
- daemon-restart 後 `replayFromDB` 已將 `agent_frames` 重建到 `m.framesByPane`；`Module.Start` 後續呼叫 `dispatcher.replayStatus()` 對每個 session 取 top frame → 啟 detector

**為何 reuse 而非新增 `last_sender_pid`**（per `feedback_skeleton_convergence` self-check）：

| 比較 | reuse `agent_frames.pid + pane_id` | 新增 `last_sender_pid` 欄位 |
|---|---|---|
| schema 改動 | 0（既有） | +1 column |
| frame.pid vs sender_pid 語意一致性 | frame.pid 是 daemon identify 驗證後的 codex pid（更穩） | sender_pid 是 hook 自報（未驗證） |
| 「把 working code 變 data」徵兆 | 不觸發 | 觸發（既有概念已涵蓋） |
| multi-frame session（罕見） | 取 top frame，與 lights status 同來源 | 需要決定哪個 sender_pid 是有效的 |

採 reuse 方案，spec drift signal #5 已決議。

### 4.4 Detector lifecycle（與 dispatcher 互動）

| 觸發 | 動作 |
|---|---|
| status enter `running` 或 `waiting`，frame projection top frame `(paneID, pid)` 已知 | dispatcher 啟 detector goroutine 帶 `(paneID, pid)` snapshot |
| status exit OnEntryStatus（變 idle / error / clear） | dispatcher cancel ctx，detector 收到 `<-ctx.Done()` 退出 |
| session rename | `renameSessionLocked` 呼叫 dispatcher.applyStatus(newName, agentType, currentStatus) — detector 在 oldName 已被先 stop（`activeProbeIntents[oldName]` 清掉），newName 重新評估 + 啟動（pane id 不變，frame state migrate） |
| daemon stop | `Module.Stop` 呼叫 dispatcher.stopAll |
| top frame `(paneID, pid)` 缺失（罕見：projection rebuild 失敗） | dispatcher skip 啟 detector；下個 hook 重建 projection 後 applyStatus 再次嘗試 |

---

## 5. Module plumbing

### 5.1 新檔 `internal/module/agent/probe_intent_dispatcher.go`

職責：

- 提供 ProbeIntent gating logic（status 變更時讀 provider.ProbeIntents() 啟停對應 detector）
- detector 透過 channel emit Signal → dispatcher 套 `applyProbeGuards`（含 OnSignal mapping）

**Lock ownership**（per by2z79ouc ATK-1 — v2 設計修正）：

- `activeProbeIntents map[session]map[Kind]activeIntent` **由 m.mu 保護**（與 `activeWatchers` 同一 mutex），**不**另起 dispatcher.mu
- 統一鎖序：`m.mu only`，無 cross-lock；`applyProbeGuards` 在 m.mu 內 re-check active-set 是 lock-free read（lock 已持）
- `activeIntent` 結構：`cancel func()` / `agentType string` / `paneID string` / `senderPID int` / `generation uint64`

**為何不 reuse `probeOrchestrator`**（per b36pap7jc DEF — 重新挑戰過）：

- `probeOrchestrator` 簽名與 `probe.ScreenChangeWatcher` 緊耦合（`startWatch(session, agentType, opts probe.WatchOptions)` / `makeCallback` 走 `probe.ScreenChangeCallback` / `interpretScreenEvent` 直接套 `m.activeWatchers`）
- ProcessDead detector 與 ScreenChange 是兩種獨立 mechanism（前者 `(senderPID, paneID) → bool×bool`；後者 `tmux pane content hash → ScreenChanged|ScreenStable`）— 強行整進 `probeOrchestrator` 會引入「unify abstraction」bloat（§2.3）
- 共用部分（graceWindow / ErrorGuard / transition gate / **strategy-injectable** stale-callback guard）抽為 free function `applyProbeGuards(m, args)`，在兩個 dispatcher 間共用 — 行為零變動，是 mechanical extraction（不違 §2.3 refactor without functional reason）

### 5.2 `applyProbeGuards` 重設計（per by2z79ouc ATK-1 / DEF-1）

抽 free function — **單一 m.mu 保護**所有 active-set；OnSignal 在 guard 通過後才被 invoke：

```go
// applyProbeGuards 套用 4 層 guard（含 mapping）並 broadcast。
// 回傳 applied=false 代表此 signal 被 guard drop / 視為 no-op。
//
// 流程（m.mu 在 step 1+5 內各持一次）：
//   1. m.mu Lock — Stale-callback guard：透過 StaleCheck closure（caller 提供）
//      檢查 active-set 是否仍含目標 entry 且 agentType + generation 都匹配
//      ProbeIntent 路徑：staleCheck 讀 m.activeProbeIntents[session][kind] +
//                        agentType + generation
//      ScreenChange 路徑：staleCheck 讀 m.activeWatchers[session] + agentType
//      不匹配 → m.mu Unlock + return false
//      匹配 → m.mu Unlock（不持 lock 進下一步，因為 graceWindow / mapping 不需要）
//
//   2. graceWindow check（probeGraceMu 獨立 mutex — 既有 lock）
//      hook recent (≤ probeGraceWindow) → return false
//
//   3. Mapping callback (per by2z79ouc DEF-1)：
//      caller 已 captured Signal + Mapping function 進 args
//      newStatus := args.Mapping(args.Signal)
//      newStatus == "" → return false
//      （ProbeIntent 路徑：args.Mapping = intent.OnSignal；
//        ScreenChange 路徑：args.Mapping = kind→status fixed function）
//
//   4. m.mu Lock — final critical section + 再次原子 staleCheck
//      （close race window between step 1 unlock and this lock — codex
//       finding #4 regression）
//      staleCheck false → m.mu Unlock + return false
//      ErrorGuard：m.currentStatus[session] == StatusError → m.mu Unlock + return false
//      Transition gate：m.currentStatus[session] == newStatus → m.mu Unlock + return false
//      mutate m.currentStatus[session] = newStatus
//      m.mu Unlock
//
//   5. broadcast（lock-free）：setProjectionTopStatus + buildProjectionNormalized + broadcastToSession
//
type probeGuardArgs struct {
    Session    string
    AgentType  string
    Reason     string                          // dev log："probe-intent:process_dead" / "probe:activity"
    Signal     Signal                          // raw signal，未 mapping
    Mapping    func(Signal) Status             // OnSignal (ProbeIntent) / kind→status (ScreenChange)
    StaleCheck func(*Module) bool              // m.mu 內 lock-free read（caller 已持 m.mu 進入）
}

func applyProbeGuards(m *Module, args probeGuardArgs) (applied bool) { ... }
```

ProbeIntent 路徑的 staleCheck 範例（**caller 在 m.mu 內呼叫 — lock-free read**）：

```go
staleCheck := func(m *Module) bool {
    intents, ok := m.activeProbeIntents[args.Session]
    if !ok { return false }
    cur, ok := intents[currentIntentKind]
    if !ok || cur.agentType != args.AgentType || cur.generation != currentGeneration {
        return false
    }
    return true
}
```

`generation` token：每次 `startDetector` 對 (session, kind) 啟動時遞增；detector emit signal 時帶當下 generation；guard re-check 確保 detector 沒被中途 stop+restart 過（防 ghost broadcast，per audit §7.1 必守 #7 / by2z79ouc DEF）。

**Generation token 必要性**（回應 by2z79ouc Defense round 2 questioning）：理論上 ctx cancel 後 detector goroutine 已退出，新 detector 是新 goroutine，channel 也不同 — 但 emission 在 channel buffer 內可能還沒被 consume；start→stop→start' 期間 stop 走的 cancel 不保證**已 consume 的 signal** 不會繼續走完 `applyProbeGuards`（goroutine schedule 問題）。Generation 是兩次 active set 檢查的雙保險，相對 over-engineering 風險低（uint64 增量；無 owner reuse）。

### 5.3 `manageActivityWatch` 改造

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

    // 2. 新：dispatch ProbeIntent lifecycle（per-agent gating）
    if m.probeIntentDisp != nil {
        m.probeIntentDisp.applyStatus(session, agentType, newStatus)
    }
}
```

### 5.4 ProbeIntent lifecycle（單一 helper 在 m.mu 內統管 4 case；per round 3 P1 + by2z79ouc ATK-2 + DEF-1）

`applyStatus` 對每個宣告 intent 走 `applyIntentLifecycle`。Lifecycle decision 全在單一 `m.mu` critical section 內完成：read 當前 active state + currentStatus + top frame；計算需要做的 cancel / start work；unlock 後執行（避免在持 lock 時跑 detector goroutine 啟動工作）。

四個 case：

| `wasActive` | `shouldActive` | live target == active target | 動作 |
|---|---|---|---|
| ✗ | ✗ | — | noop |
| ✗ | ✓ | — | record active + start detector |
| ✓ | ✗ | — | cancel old + delete active |
| ✓ | ✓ | ✓ | noop（已 armed correctly）|
| ✓ | ✓ | ✗ | cancel old + record new active + start new detector（**round 3 P1 修法**：active target mismatch 重啟）|

```go
func (d *probeIntentDispatcher) applyStatus(session, agentType string, newStatus agent.Status) {
    provider, ok := d.parent.registry.GetByType(agentType)
    if !ok {
        // 未知 agent → reconcile 清掉所有 active（含舊 codex entry 等）
        d.reconcileSessionActive(session, agentType, nil)
        return
    }
    intents := probeIntentsOf(provider)  // type assert ProbeIntentProvider；non-implementer 回 nil

    // round 4 P1：reconcile session 既有 active entries
    // 凡 (cur.agentType != newAgentType) 或 cur.kind 不在新 provider 宣告集合 → cancel + delete
    declaredKinds := make(map[agent.ProbeIntentKind]struct{}, len(intents))
    for _, intent := range intents {
        declaredKinds[intent.Kind] = struct{}{}
    }
    d.reconcileSessionActive(session, agentType, declaredKinds)

    for _, intent := range intents {
        d.applyIntentLifecycle(session, agentType, newStatus, intent)
    }
}

// reconcileSessionActive cancel/delete active entries that don't apply to the
// (newAgentType, declaredKinds) pair. Called from applyStatus before the
// lifecycle loop. declaredKinds=nil → drop everything (unknown agent / no
// ProbeIntents declared).
//
// Per round 4 P1: without this, switching session top agent from codex (with
// ProcessDead intent) to cc/opencode (no ProbeIntents) would leave codex
// active entry stranded. The stranded detector continues polling and its
// emission still passes makeProbeIntentStaleCheck (active entry's agentType
// + generation match) → broadcasts wrong status for the new agent.
func (d *probeIntentDispatcher) reconcileSessionActive(
    session string,
    newAgentType string,
    declaredKinds map[agent.ProbeIntentKind]struct{}, // nil = drop all
) {
    var toCancel []context.CancelFunc

    d.parent.mu.Lock()
    perSession := d.parent.activeProbeIntents[session]
    if perSession != nil {
        for kind, cur := range perSession {
            stale := declaredKinds == nil ||
                cur.agentType != newAgentType ||
                func() bool { _, ok := declaredKinds[kind]; return !ok }()
            if stale {
                toCancel = append(toCancel, cur.cancel)
                delete(perSession, kind)
            }
        }
        if len(perSession) == 0 {
            delete(d.parent.activeProbeIntents, session)
        }
    }
    d.parent.mu.Unlock()

    for _, cancel := range toCancel {
        cancel()
    }
}

type lifecyclePlan struct {
    cancelOld   context.CancelFunc  // 非 nil → 須 cancel 舊 detector
    startCtx    context.Context     // 非 nil → 須啟新 detector goroutines
    paneID      string
    senderPID   int
    generation  uint64
}

func (d *probeIntentDispatcher) applyIntentLifecycle(session, agentType string, newStatus agent.Status, intent agent.ProbeIntent) {
    shouldActive := slices.Contains(intent.OnEntryStatus, newStatus)

    // 進 m.mu，計算 plan
    d.parent.mu.Lock()
    var plan lifecyclePlan
    perSession := d.parent.activeProbeIntents[session]
    cur, wasActive := perSession[intent.Kind]

    switch {
    case !shouldActive && !wasActive:
        // case 1: noop
    case !shouldActive && wasActive:
        // case 3: stop only
        plan.cancelOld = cur.cancel
        delete(perSession, intent.Kind)
        if len(perSession) == 0 {
            delete(d.parent.activeProbeIntents, session)
        }
    case shouldActive:
        // case 2 / 4 / 5：lookup live currentStatus + top frame，配合 active target 比較
        curStatus, hasStatus := d.parent.currentStatus[session]
        if !hasStatus || !slices.Contains(intent.OnEntryStatus, curStatus) {
            // by2z79ouc ATK-2：snapshot 與 lifecycle 之間 status 已變 → 不 arm
            // （若舊 entry 還在，視 wasActive 順手清掉）
            if wasActive {
                plan.cancelOld = cur.cancel
                delete(perSession, intent.Kind)
                if len(perSession) == 0 {
                    delete(d.parent.activeProbeIntents, session)
                }
            }
            break
        }
        paneID, senderPID, hasFrame := d.parent.lookupTopFrameForSessionLocked(session)
        if !hasFrame || paneID == "" || senderPID == 0 {
            // top frame 缺失（罕見）：若 wasActive 但 target 已不可確認 → tear down 防舊 detector 對失效目標誤觸
            if wasActive {
                plan.cancelOld = cur.cancel
                delete(perSession, intent.Kind)
                if len(perSession) == 0 {
                    delete(d.parent.activeProbeIntents, session)
                }
            }
            break
        }
        targetMatches := wasActive && cur.agentType == agentType && cur.paneID == paneID && cur.senderPID == senderPID
        if wasActive && targetMatches {
            // case 4: already armed correctly → noop
            break
        }
        // case 2 (!wasActive) 或 case 5 (target mismatch)：(re)arm
        if wasActive {
            plan.cancelOld = cur.cancel
        }
        generation := d.parent.nextProbeIntentGeneration()
        ctx, cancel := context.WithCancel(d.parentCtx)
        if perSession == nil {
            perSession = make(map[agent.ProbeIntentKind]activeIntent)
            d.parent.activeProbeIntents[session] = perSession
        }
        perSession[intent.Kind] = activeIntent{
            agentType:  agentType,
            paneID:     paneID,
            senderPID:  senderPID,
            cancel:     cancel,
            generation: generation,
        }
        plan.startCtx = ctx
        plan.paneID = paneID
        plan.senderPID = senderPID
        plan.generation = generation
    }
    d.parent.mu.Unlock()

    // 在 m.mu 外執行 work（cancel / start 都不需 lock）
    if plan.cancelOld != nil {
        plan.cancelOld()
    }
    if plan.startCtx != nil {
        out := make(chan agent.Signal, 1)
        go func() {
            switch intent.Kind {
            case agent.ProbeIntentKindProcessDead:
                codex.StartProcessDeadDetector(plan.startCtx, d.parent.tmux, plan.paneID, plan.senderPID, out)
            // future Kind: 加 case
            }
            close(out)
        }()
        go d.consumeSignals(plan.startCtx, session, agentType, intent, plan.generation, out)
    }
}

func (d *probeIntentDispatcher) consumeSignals(
    ctx context.Context,
    session, agentType string,
    intent agent.ProbeIntent,
    generation uint64,
    in <-chan agent.Signal,
) {
    for sig := range in {
        // OnSignal 由 applyProbeGuards 在 guard 通過後 invoke（per by2z79ouc DEF-1）
        applied := applyProbeGuards(d.parent, probeGuardArgs{
            Session:    session,
            AgentType:  agentType,
            Reason:     "probe-intent:" + string(intent.Kind),
            Signal:     sig,
            Mapping:    intent.OnSignal,
            StaleCheck: makeProbeIntentStaleCheck(session, intent.Kind, agentType, generation),
        })
        if !applied { continue }

        // round 5 P1：probe 把 status 改了 (error/clear) — re-run lifecycle 觸發
        // case 3 (active && !shouldActive)：cancel + delete active entry。
        // 否則 detector goroutine 已退出但 activeProbeIntents 仍含舊 entry：
        //   - map leak（每次 probe-applied 都累積一筆死 entry）
        //   - 後續 lifecycle 看到 wasActive=true 走 case 4/5 而非 case 2 重 arm
        d.parent.mu.Lock()
        appliedStatus := d.parent.currentStatus[session]
        d.parent.mu.Unlock()
        d.applyStatus(session, agentType, appliedStatus)
    }
}

func makeProbeIntentStaleCheck(session string, kind agent.ProbeIntentKind, agentType string, generation uint64) func(*Module) bool {
    return func(m *Module) bool {
        intents, ok := m.activeProbeIntents[session]
        if !ok { return false }
        cur, ok := intents[kind]
        return ok && cur.agentType == agentType && cur.generation == generation
    }
}
```

**`Module.Stop` 的 cleanup**：直接遍歷 `activeProbeIntents` cancel 全部 entry + clear map。同一鎖（m.mu）保證 cleanup 期間沒有新 detector 啟動。

**Generation 遞增**：`nextProbeIntentGeneration()` 是 m.mu 內遞增（因 caller 已持 m.mu），純 uint64++；無 reuse 風險（uint64 overflow 需 ~5×10¹⁹ 次 cycle，daemon 永不會到）。

**為何 helper 不暴露 `startDetector` / `stopDetector` 為公開 method**：兩者必須與 active-set mutation + lookup 在同一 m.mu critical section 完成（lifecycle 是原子操作）；拆出單獨的 public method 會誘導 caller 跳過 lifecycle 比對 → 重蹈 round 3 P1 教訓。helper 只透過 `applyStatus`（hook + replay 兩 caller 共用）入口。

### 5.5 Polling 頻率 / grace window

- ProcessDead polling：1Hz（§4.2 已述）
- graceWindow：沿用既有 `probeGraceWindow = 2s`（hook 後 2s 內 probe signal 全 drop）
- `applyProbeGuards` free function 抽 `probe_orchestrator.go interpretScreenEvent` 的 step 1+2+4+5+6（stale guard / graceWindow / ErrorGuard / transition gate / broadcast），讓 ProcessDead 與 ScreenChange 兩條 dispatcher 共用 — guard 都用 strategy 注入 active-set checker（§5.2）

---

## 6. Daemon-restart watcher recovery (issue #698)

### 6.1 問題

`Module.Start()` (`internal/module/agent/module.go:223`) 呼叫 `replayFromDB` 重建 `currentStatus` / `subagents` / frame projection，但**不**呼叫 `manageActivityWatch` 重啟任何 watcher。所有 W6 ProbeIntent 在 daemon 重啟後**直到下個 hook 才會重新掛**。

對 W6-3 的影響：user 在 daemon 重啟前 codex 是 running 狀態，daemon 重啟後若 codex crash，**沒有 ProbeIntent watcher**會發現，user 看不到 lights 變紅。

### 6.2 hydrate 來源：reuse `agent_frames.pid + pane_id`（**不**新增欄位）

per §4.3 / b36pap7jc FH-1 修正 — 不採 spec v1 的「frames 加 last_sender_pid」設計。

`agent_frames` 表既有結構（`internal/store/frames.go:34-48`）：

```sql
CREATE TABLE IF NOT EXISTS agent_frames (
    frame_id            TEXT PRIMARY KEY,
    pane_id             TEXT NOT NULL,
    agent_type          TEXT NOT NULL,
    pid                 INTEGER NOT NULL,
    ppid                INTEGER NOT NULL,
    process_start_time  TEXT NOT NULL,
    parent_frame_id     TEXT,
    subagents_json      TEXT NOT NULL DEFAULT '[]',
    status              TEXT NOT NULL,
    started_at          INTEGER NOT NULL,
    last_seen_at        INTEGER NOT NULL,
    verified            INTEGER NOT NULL DEFAULT 1,
    ...
)
```

ProbeIntent dispatcher 啟動 detector 時走：

```go
// lookupTopFrameForSession 從現有 projection 取 top frame 的 pane_id + pid
func (m *Module) lookupTopFrameForSession(session string) (paneID string, pid int, ok bool) {
    projection, err := m.projectionForSession(session)
    if err != nil || projection == nil || projection.TopFrame == nil {
        return "", 0, false
    }
    return projection.TopFrame.PaneID, projection.TopFrame.PID, true
}
```

frame projection 已是 hook 路徑 + replayFromDB 共用的 single source（`internal/module/agent/projection.go`） — replay 後 projection 已 hydrate 完成。

### 6.3 Replay 後重新啟動 dispatcher

`Module.Start()` 流程加一步：

```go
func (m *Module) Start(_ context.Context) error {
    if err := m.sweepOnce(); err != nil { ... }
    m.replayFromDB()         // 重建 currentStatus + agent_frames + frame projection
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

### 6.4 Race / 一致性（per b36pap7jc + by2z79ouc ATK-2）

- `applyStatus` 已是 dispatcher 公開介面（hook 路徑也走它）— 重用即可
- replay 順序：先 `replayFromDB` 把 currentStatus + projection 全 hydrate 完，再 `replayStatus()` — 避免 detector 啟動時 `lookupTopFrameForSession` miss
- **Replay vs hook race（by2z79ouc ATK-2 + round 3 P1 修法）**：`replayStatus` 內部呼叫 `applyStatus(session, agentType, snapshotStatus)` 走標準路徑；`applyIntentLifecycle` 在 m.mu 內 re-read `currentStatus` + `lookupTopFrameForSessionLocked` + 比對 active target — 若 snapshot 取到 `running` 但 hook 已先把 status 改成 `idle`（or 改了 top frame target），lifecycle 在 m.mu 內看到 live state 為基準直接 skip / restart。**Race 完全封閉**
- **Active target mismatch（round 3 P1）**：同 status 內 codex 換 pid/pane（restart / multi-pane 切換）— `applyIntentLifecycle` 第五 case：cancel 舊 detector + 寫新 entry（含新 generation） + 啟新 detector；舊 detector 在新 entry 寫入後仍可能 emit 舊 generation 的 stale signal，被 `makeProbeIntentStaleCheck` drop（generation mismatch）
- **Cross-provider lifecycle gap（round 4 P1）**：session 切到無 ProbeIntent 的 provider（cc / opencode）時，`applyStatus` 入口 `reconcileSessionActive` 清掉舊 codex active entry → 舊 detector emission 因 active entry 已不存在被 `makeProbeIntentStaleCheck` drop
- **Stale frame race**：若 daemon 在 codex 已死的情況下重啟，replay 取到的 top frame.pid 已是 dead → detector 第一次 poll 立即 emit signal → 套 guards → 因 currentStatus 經 replay 仍是 running，transition gate 通過 → broadcast `error`。**這是預期行為**：user-facing 結果是「daemon restart 後正確發現 codex 不見了，立刻變色」，符合 #698 修復目標
- **Idle session pane gone（accept 限制）**：若 codex 走完 PdxStop hook（status=idle）後 daemon restart，replay snapshot status=idle → ProbeIntent 不在 OnEntryStatus 不啟 detector → pane 後續被 close 不會自動轉 clear。此屬 §8.1 #4 的 accept 限制（idle 期間 pane 退場由 sweep / SessionEnd hook 處理，不在 W6-3+W6-4 scope）
- ErrorGuard 與 grace window：replay 路徑**沒有**剛收的 hook，故 graceWindow 不啟動；ErrorGuard 視 currentStatus 而定（若已是 error，guard 阻擋無妨）
- 若 replay 後某 session 的 top frame 缺失（projection rebuild 失敗 / 老資料），`startDetector` skip — 等下個 hook 補

---

## 7. Phase 拆分

### Phase 1 — Interface finalize + module dispatcher + replay recovery

**目標**：建立 `ProbeIntentProvider` interface + module dispatcher plumbing + replay recovery；codex provider **暫**用 stub detector（永不 emit signal，純測 dispatcher lifecycle）。**不**動 schema（reuse `agent_frames.pid + pane_id`）。

**Tasks**（每 task 獨立 commit；TDD）：

| ID | 內容 |
|---|---|
| P1-T1 | `ProbeIntent` / `ProbeIntentKind` / `Signal` / `ProbeIntentProvider` 落 `internal/agent/provider.go` + 單元測試（schema 對齊 §3.2；Signal 4 fields） |
| P1-T2 | `applyProbeGuards` free function 抽出（mechanical extraction `probe_orchestrator.go interpretScreenEvent` step 1+2+4+5+6） + 新 `staleCheck strategy` 注入點；regression test 確保 ScreenChange 行為零變動（既有 probe_orchestrator_test.go / probe_orchestrator_integration_test.go 全綠） |
| P1-T3 | `Module.lookupTopFrameForSessionLocked` helper（讀 projection top frame `(paneID, pid)`）+ test — **plan v2 swap 後為 dispatcher core 的前置依賴** |
| P1-T4 | 新檔 `internal/module/agent/probe_intent_dispatcher.go`：`applyStatus` / `reconcileSessionActive` / `applyIntentLifecycle` / `consumeSignals` + activeProbeIntents 結構（在 m.mu 內保護） + generation token；用 stub detector 測 lifecycle 5 case（含 active target mismatch 觸發 cancel-and-rearm） + cross-provider reconcile（codex → cc 切換清掉舊 entry） + stale-callback re-check + generation 不匹配 drop + **probe-applied teardown**（detector emit 把 status 改成 error → activeProbeIntents 該 entry 被清） |
| P1-T5 | `manageActivityWatch` 接 `probeIntentDisp.applyStatus`；rename / Stop 路徑也接；test 覆蓋 rename 期間 (oldName stop, newName 重評估) |
| P1-T6 | `Module.Start` 加 `probeIntentDisp.replayStatus`；daemon-restart recovery integration test（fixture：projection top frame 含 dead pid + status=running → Start 後 detector 啟動立即 emit → broadcast error） |

**驗收**：

- `go test ./...` 全綠
- stub detector 啟停流程在 lifecycle test 中可觀察（counter 計數 + generation 隔離）
- daemon-restart recovery test：fixture mock projection top frame + status → Start 後 dispatcher.activeProbeIntents 含對應 entry
- ScreenChange 行為 zero regression
- ProbeIntent 路徑 stale-callback guard 用 `activeProbeIntents` re-check（**不**用 `activeWatchers`） — assertion 由 test 強制（per b36pap7jc ATK-1）

### Phase 2 — codex detector + drift test + mlab live verify

**Tasks**：

| ID | 內容 |
|---|---|
| P2-T1 | `tmux.Executor.HasPane(paneID string) bool` 方法新增 + 單元測試（wraps `tmux list-panes -a -F '#{pane_id}'`）— 排序前置 detector，因 detector 編譯需要此方法（per by2z79ouc Defense） |
| P2-T2 | `internal/agent/codex/probe_intent_process_dead.go` 新檔 + 單元測試（fake `tmuxPaneLister` + 覆寫 `isPidAliveFn` package var 控制 4 種 pidAlive×paneAlive 組合 + emit Signal 1 次後退出 + ctx cancel 提早退出 + multi-pane fixture 驗 paneID 路徑取對 pane）；test cleanup 還原 `isPidAliveFn` |
| P2-T3 | codex `Provider.ProbeIntents()` 宣告 + provider_test.go regression（OnSignal 區分 PaneAlive=true→error / =false→clear） |
| P2-T4 | dispatcher 路由 `ProbeIntentKindProcessDead` 到 `codex.StartProcessDeadDetector`（生產 wiring，傳 `(tmux, paneID, senderPID)` 三參數） |
| P2-T5 | drift test：iterate `registry` 找出所有 `ProbeIntentProvider`；逐一驗證 `ProbeIntents()` 宣告的 Kind 都有 dispatcher case + OnSignal 不為 nil + OnEntryStatus 非空 + Signal context fields semantics 不被改變（PaneAlive / PaneID / SenderPID 必填） |
| P2-T6 | trace dev log 補：`[probe-intent]` step kind（chain log 第 6 條），W4 trace pipeline package comment 同步 |
| P2-T7 | integration test：mock codex 進程 alive→dead，pane 兩種變體（alive→error 路徑、gone→clear 路徑） |
| P2-T8 | mlab live verify：(a) codex SIGKILL（pane alive） → lights 變 error；(b) codex pane 直接 `tmux kill-pane`（pane gone）→ lights 變 clear；(c) codex `/exit` （走 PdxStop hook → idle）→ ProbeIntent 在 idle 已停 → 無誤觸；(d) daemon restart 後 codex 從 running 變 dead → ≤2s 變色 |

**驗收**：

- `go test ./internal/...` 全綠
- drift test 失敗時清楚指出哪個 provider / kind 缺實作
- mlab live 4 個場景都通過
- 兩輪 codex review：standard + 三平行（攻擊 / 防守 / 體質）
- 收斂後 squash merge

### 為何拆兩 phase

- Phase 1 純 plumbing + interface — 改動範圍核心是 `applyProbeGuards` mechanical extraction + dispatcher state machine（regression 風險）
- Phase 2 detector + provider wiring — 改動局限 codex 包 + tmux helper + dispatcher 路由 case
- 拆兩 phase 兩輪 codex review 可分別聚焦：Phase 1 review plumbing safety + ScreenChange regression；Phase 2 review detector correctness + multi-pane edge case

---

## 8. 驗收條件

### 8.1 功能性

1. ✅ codex 進程被 SIGKILL（running / waiting 狀態下，pane 仍存在）→ ≤2s 內 lights 變 `error`
2. ✅ codex pane 被 `tmux kill-pane`（running / waiting 狀態下）→ ≤2s 內 lights 變 `clear`（W6-4 場景）
3. ✅ codex `/exit` 正常退場 → 走 idle hook（PdxStop） → ProbeIntent 在 idle 不啟動 → 無誤觸
4. ✅ codex idle 期間 pane 被 close → ProbeIntent 不啟動（gating 排除 idle） → status 維持 idle（accept：純 idle 期間 pane 退場仍由 sweep / SessionEnd hook 處理，不在 W6-3+W6-4 scope）
5. ✅ daemon 重啟前 codex running，重啟後 codex 已死 → ≤2s 內 lights 變 `error` 或 `clear`（依 pane existence）（issue #698 修復）
6. ✅ session rename 期間 ProbeIntent watcher 跟著 oldName 停、newName 啟（若新 status 仍 running/waiting）
7. ✅ multi-pane window：detector 對 hook 來源 pane 作判斷，不被同 window 其他 pane（含其他 codex 實例）影響
8. ✅ active target mismatch（round 3 P1 防護）：codex pane 內 `/exit` + 立即重啟新實例（同 session、status 短暫從 idle 經 SessionStart→UserPromptSubmit 變回 running），detector 必須 cancel 舊 (paneID, oldPID) entry 並 arm 新 (paneID, newPID) entry — 舊 detector 對 oldPID 死亡的 emission 不應蓋掉新 codex 的 running status
9. ✅ status-stable 換 frame：罕見場景下若 hook 在不變 status 的情況下推送新 top frame（例 frame_ops 重 detect），lifecycle 仍正確 re-arm（不沿用舊 detector）
10. ✅ provider 切換（round 4 P1 防護）：session 從 codex 切到 cc / opencode（後者無 ProbeIntents）— 舊 codex active entry 必須被 `reconcileSessionActive` 清掉；舊 detector 後續 emission 因 active entry 不存在被 stale guard drop

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

## 9. Spec Drift Signals（已隨 codex review b36pap7jc 決議）

### 9.1 ✅ 採納 — audit §7 「process exit code」假設修正

**Drift**：audit §7 W6-3 假設 daemon 能取得 codex exit code，但 daemon 非 codex parent → Unix 限制不允許。

**決議**：採 §0.2 的 binary `process_dead` signal + tmux pane existence 區分 error/clear。W6-3 ship 後同步修訂 audit doc §6/§7 對應條目。

### 9.2 ✅ 採選項 B — 合併 W6-3 + W6-4 first PR

**決議**：採選項 B（per b36pap7jc ATK-3）。理由詳 §0.2.1：detector 共用 + interface 穩定性 + 避免 user-visible false alarm。

### 9.3 ✅ 採拆 Phase — Phase 1 (interface + plumbing) + Phase 2 (detector + verify)

**決議**：拆兩 phase（詳 §7「為何拆兩 phase」）— 兩輪 codex review 焦點分離（Phase 1 plumbing/regression / Phase 2 detector/edge case）。

### 9.4 ✅ 採 polling — 1Hz `IsPidAlive + tmux list-panes`

**決議**：採 polling 1Hz。1Hz × `syscall.Kill(pid, 0)` + `tmux list-panes` cost 可忽略；不採 kqueue（macOS only / cgo 複雜度 / lib 風險）。codex review 若認可此決議即定案。

### 9.5 ✅ 採 reuse `agent_frames.pid + pane_id` — **不**新增 schema 欄位

**決議**：per b36pap7jc FH-1 + 後續 self-check（既有 frame.pid / pane_id 已涵蓋語意，新增 last_sender_pid 欄位是「把 working code 變 data」徵兆）。修法詳 §4.3 / §6.2。

### 9.6 ✅ 採單一 m.mu 統管 active-set — 解 by2z79ouc ATK-1

**決議**：`activeProbeIntents` 移到 `m.mu` 保護（與 `activeWatchers` 同 mutex），不另起 dispatcher.mu。鎖序簡化為 m.mu only，`applyProbeGuards` 內 staleCheck closure 為 lock-free read（caller 已持 m.mu）。修法詳 §5.1 / §5.2。

### 9.7 ✅ 採 compare-and-arm — 解 by2z79ouc ATK-2

**決議**：`startDetector` 在 m.mu 內 re-read `currentStatus` + 比對 `OnEntryStatus`，只在 live status 仍 match 才 record active；防 replay snapshot 與 hook 之間 race。修法詳 §5.4 / §6.4。

### 9.8 ✅ 採 mapping callback in applyProbeGuards — 解 by2z79ouc DEF-1

**決議**：`applyProbeGuards` 接受 `Mapping func(Signal) Status` callback，在 stale guard + graceWindow guard 通過後才 invoke；ProbeIntent 路徑 args.Mapping = intent.OnSignal；ScreenChange 路徑 args.Mapping = kind→status。Provider OnSignal 不會被 drop signal 打到。修法詳 §3.2 / §5.2 / §5.4。

### 9.9 ✅ 採 `isPidAliveFn` package var injection — 解 by2z79ouc FH-1

**決議**：codex 包加 `isPidAliveFn = probe.IsPidAlive` package var；test 覆寫 + cleanup（同既有 `orchNowFn` / `recordHookAtHook` / `interruptBeforeFinalLockFn` pattern）。修法詳 §4.2。

### 9.10 ✅ 採 unified lifecycle helper + active target mismatch 第五 case — 解 round 3 P1

**決議**：`applyIntentLifecycle` 在單一 m.mu critical section 內處理 5 case（含 active 但 target changed 的 cancel-and-rearm）；不暴露 `startDetector` / `stopDetector` 為公開 method（避免 caller 跳過 lifecycle 比對）。修法詳 §5.4。

### 9.11 ✅ 採 `reconcileSessionActive` 入口清理 — 解 round 4 P1

**決議**：`applyStatus` 入口先 reconcile session 既有 active entries，cancel/delete 任何 `(agentType, kind)` 不再屬於新 provider 宣告集合的 entry；防 cross-provider 切換的 stranded detector。修法詳 §5.4 reconcileSessionActive helper。

### 9.12 ✅ 採 probe-applied teardown re-run applyStatus — 解 round 5 P1

**決議**：`consumeSignals` 在 `applied=true` 後 re-run `applyStatus(session, agentType, newStatus)` — 讓 newStatus（error / clear）走 lifecycle case 3 cancel+delete active entry。修法詳 §5.4 consumeSignals。

### 9.13 Spec convergence 與 stopping criterion（user 同意 (D)）

5 輪 codex review finding 趨勢：5 → 4 → 1 → 1 → 1。每輪挖到不同類別 lifecycle corner case，convergence 明顯但非到 0。User 2026-04-29 決議採 (D)：修 v6 採納 round 5 P1 + **不再派 round 6**，直接進 plan。理由：

- 修法 scope 5 行內，不涉及架構
- spec lifecycle 已大致窮舉（hook → applyStatus → reconcile → applyIntentLifecycle → consumeSignals → re-apply）
- plan 階段 codex review 仍會驗證實作正確性（test plan 涵蓋全部 5 case + 4 reconcile + probe-applied teardown）
- 5 輪是 spec 階段合理上限（同 W2 spec 經驗）

**Known issue 追蹤**：若 round 5 之後仍有 lifecycle corner case（unlikely），於 plan / 實作 / PR review 階段以 follow-up issue 追蹤。

### 9.14 ✅ 採 J3 generic pre-grace timer at dispatcher level — anchor 跨 Kind 不特化

**Drift signal**：未來若有人想為單一 ProbeIntent Kind（如只對 ScreenChange，ProcessDead 不 hold）特化 pre-grace timer，或在 detector 端自行加 pre-hold 邏輯。

**決議**：J3 PR（spec `2026-05-01-probe-intent-bidirectional-grace-window-spec.md`）已確立 pre-grace timer 在 dispatcher `consumeSignals` 內 generic 對所有 ProbeIntent Kind 適用（per fix-spec §3 不為單一 Kind 特化約束 + W6-3 ProcessDead 也有同類 race）；**不可在單一 Kind 特化 pre-grace timer；不可 detector 端自行加 pre-hold**（detector 仍是 dumb emit；lifecycle 由 dispatcher 統一管）。

---

**所有 spec drift signal 已收斂。本 spec v6 為 final draft；直接進 plan 階段。J3 PR（2026-05-01）後新增 §9.14 anchor 防跨 Kind 特化，不影響 v6 final draft 內容。**

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
  - `internal/module/agent/probe_intent_dispatcher_drift_test.go`（registry sweep + Kind exhaustiveness）
- **修改**：
  - `internal/agent/provider.go`（新增 `ProbeIntentProvider` interface + `ProbeIntent` / `ProbeIntentKind` / `Signal` types）
  - `internal/agent/codex/provider.go`（實作 `ProbeIntents()` + `onProcessDead` mapper）
  - `internal/module/agent/module.go`（`Start` 加 replayStatus / `manageActivityWatch` 接 dispatcher / `Stop` 加 dispatcher.stopAll / `lookupTopFrameForSession` helper）
  - `internal/module/agent/probe_orchestrator.go`（**mechanical** 抽 `applyProbeGuards` free function + active-set strategy 注入點；行為零變動）
  - `internal/module/agent/trace.go`（package comment 加 `[probe-intent]` step kind）
  - `internal/tmux/executor.go`（新增 `HasPane(paneID string) bool` 方法）
- **不動**：
  - `internal/store/frames.go` schema（reuse 既有 `pid + pane_id`，不加新欄位）
  - `internal/agent/codex/{events,status,hooks}.go`（既有 hook 路徑零變動）
  - 其他 agent provider（cc / opencode 不受影響 — `ProbeIntentProvider` 是 optional）

### Issue / 文獻 references

- Issue #698 — daemon restart watcher recovery（W6-3 一併處理）
- Issue #719 — always-on probe residue（W3 已撤；非本 spec 範圍）
- Memory `feedback_skeleton_convergence` / `feedback_phase_skip_threshold` / `feedback_codex_pr_review_spec_alignment` / `feedback_no_alpha_migration` / `feedback_codex_review_termination`
- Codex spec review b36pap7jc — 5 finding 全採納修訂
