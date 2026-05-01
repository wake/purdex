# W6-6 codex permission-reply ScreenChange ProbeIntent spec

> **Status**：v6（J3 ship at alpha.281 後 update — reject race 由 dispatcher pre-grace 接管，A9/A10 為本 PR ship gate）。v5 contract 主軸不變：**2-phase + 2-case truth table contract** — Phase A 等 `ScreenStable`（pane 停下來進入穩定）/ Phase B 監控 `ScreenChanged` → emit-once；Phase A 失敗一律歸 case 2 by contract → 由 PdxStop hook 自然 cover，detector 不再嘗試補位。實作面 — `armed atomic.Bool`（Phase A→B 閘門）+ `emitted atomic.Bool` + `sync.Mutex`（取代 v4 sync.Once；修 round 4 F1）；isCodexAlive 用 `prober.FirstAliveAgentInTree(paneID)`（已用 ActivePanePID exact resolve；修 round 3 F3）；emit 前 mutex 內 double-check isCodexAlive（修 round 3 F2 race window）。撤掉 grace gate / `now func()` seam — Phase A 由 ScreenStable 自然 demarcate。**v6 update**：round 5 抓到的 reject path race 由 J3 PR #797 dispatcher 雙向 `probeIntentPreGraceWindow=300ms` + `classifyAsHookRace` helper 接管（per W6-3 §9.14 + fix-spec §3 generic-Kind 不特化原則）；本 PR detector 仍是 dumb emit；§0.5 + R14 + §8 anchor + A9/A10 mlab gate 落地。
>
> **Spec evolution（progressive precision，per [feedback_meta_drift_progressive_precision]）**：v1 armed/ScreenStable → v2 emit-once + F1 re-arm → v3 retry-emit → v4 detector-grace + emit-once → **v5 2-phase contract** → **v6 (J3 ship at alpha.281) reject race 移交 dispatcher**。round 1-4 各打進不同邊界 race（quick-approval / fast-silent / long-dialog / fast-with-output），收斂方向是「同 area 修不同精度層次」非循環 drift；round 4 F2 已逼近物理約束（dialog 渲染 vs user-action 在 hash 層級無法區分），任何 detector-only 解法都會在另一邊界失效。v5 不再追求完美 detector 設計，把「無法判讀」明列為 by-contract limitation；reframe 後 detector 邏輯 ~25 行、無時間 const、無 grace gate、無 once-permanent-fired race。round 5 抓到 reject path race（armed=true 後 [2] 拒絕 → dialog 消失 ScreenChanged 與 PdxStop hook race） → user reframe 為**跨層 contract**：detector 仍是 dumb emit，dispatcher J3 雙向 graceWindow + `classifyAsHookRace` 處理 hook race（generic 對所有 ProbeIntent Kind 適用），與 W6-3 §9.14 anchor 對齊；boundary race acceptable as known limitation by R13/R14（fail-fast handling — A9 mlab gate 監控漏網率）。
>
> **4 round 9 finding 處置摘要**（詳見 §8 / §11）：
> - Round 1 F1（armed quick-approval deadlock，high）→ ✅ closed by v5 contract（quick-approval 屬 case 2 known limitation，非 bug）
> - Round 1 F2（senderPID 不夠，medium）→ ✅ fixed in v3+（用 paneID identity，v5 沿用）
> - Round 2 F1（emit-once + F1 re-arm fast/silent 漏，high）→ ✅ closed by v5 contract（fast/silent 屬 case 2 known limitation）
> - Round 2 F2（senderPID alive ≠ identity，medium）→ ✅ fixed in v3+（FirstAliveAgentInTree paneID exact）
> - Round 3 F1（retry-emit dialog evidence 跨 grace，high）→ ✅ closed by v5（不再有 retry-emit / grace 概念）
> - Round 3 F2（retry 不重驗 identity race，high）→ ✅ fixed in v5（mutex 內 double-check isCodexAlive）
> - Round 3 F3（PanePID vs ActivePanePID 不一致，high）→ ✅ fixed in v3+（用 FirstAliveAgentInTree；IsAliveFor 一致性 follow-up issue 不在本 PR scope）
> - Round 4 F1（sync.Once 永久熔斷，high）→ ✅ fixed in v5（atomic.Bool + Mutex 取代 sync.Once，transient identity false 不熔斷）
> - Round 4 F2（fast-with-output source-drop 漏，high）→ ✅ closed by v5 contract（fast-with-output 屬 case 2 known limitation）
> - Round 4 F3（§2.2 stale constraint，medium）→ ✅ fixed in v5（§2.2 重寫對齊 atomic.Bool/Mutex 結構）
> - Round 5 F1（reject path race：armed=true 後 [2] 拒絕 ScreenChanged 與 PdxStop hook race，high）→ ✅ closed by v6 cross-layer contract（J3 PR #797 dispatcher 雙向 `probeIntentPreGraceWindow=300ms` + `classifyAsHookRace` cover；boundary race acceptable as known limitation by R14；A9 mlab gate 監控漏網率）
>
> **Worktree**：`.claude/worktrees/lights-w6-6-codex-screen-change` / branch `worktree-lights-w6-6-codex-screen-change`
> **Base**：`origin/main` @ alpha.281（J3 PR #797 `56b3ba55` + bump #798 `5736f87e` 之後）
> **依賴**：
> - `docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md` — ProbeIntent interface finalize（Kind / Signal / OnEntryStatus / OnSignal）+ §9.14 generic-Kind 不特化 anchor
> - `docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-spec.md` — J3 dispatcher 雙向 graceWindow + `classifyAsHookRace` helper（PR #797 ship at alpha.281）— W6-6 reject race 處理層
> - `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3 — non-always-on / non-framework / per-agent ad-hoc 約束
> - `docs/specs/2026-04-28-hook-status-audit-spec.md` §6 W5-? + §7 W6-6 — 缺口定義
> - `internal/agent/probe/{probe.go, activity.go}` — `Prober.Watch` / `WatchOptions{TopLines, IdleStableTicks}` / `ScreenChangeEvent{ScreenChanged, ScreenStable}`
> - `internal/module/agent/probe_intent_dispatcher.go` — 5-case lifecycle / 4-step guards / supportedKinds drift gate / J3 pre-grace + `classifyAsHookRace`

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
| user 按 2 拒絕 / Esc 取消 → 回 idle | codex 發 `PdxStop` | ❌ hook authority 已 cover；**reject path race 由 J3 dispatcher pre-grace 接管**（見 §0.5）|
| codex pane 被 user 關閉 | tmux pane 消失 | ❌ W6-4 ProcessDead PaneAlive=false 已 cover |
| codex 進程崩潰 / SIGKILL | 進程死、pane 留 shell | ❌ W6-3 ProcessDead PaneAlive=true 已 cover |
| **user 按 1 批准 → codex 執行 tool** | **無 hook** | ✅ **本 PR 補位** |

**「螢幕任何變動 = running」之所以正確，是因為 waiting 唯一無 hook 的退場路徑就是 approval reply**。對話開始滾動 / dialog 消失 / 任何 line 1-10 文字變動，幾何上只能對應「批准已被消費，TUI 開始展示 tool 執行結果」。glyph / 字串 pattern 不可靠（codex 0.126+ 隨時改），但「pane 內容相對於 dialog stable baseline 的任何 hash 變動」是穩定的物理觀察。

### 0.4 為什麼選 `Prober.Watch` + `TopLines: 10` + 2-phase contract

**primitive 選擇**：

- `Prober.Watch(target, opts, cb)`：W3 撤回後保留的 dumb screen primitive。500ms tick / fnv32a hash diff / 連續 `IdleStableTicks` tick 同 hash 後 emit `ScreenStable`（counter reset）/ 後續 hash 變動 emit `ScreenChanged`。已 ship、有 race 測試、不再 always-on。
- `TopLines: 10`：codex 0.125.0 TUI 對話往上推送，conversation 從 line 1 起向下展開；行 11 是 `›` 輸入區、行 13 是 status line（cursor / token counter）。
  - **排除 line 11 input echo 噪音**（user 批准後若繼續打字不算 evidence）
  - **排除 line 13 status line tick 噪音**（cursor blink / status text 自更新）
  - **mlab live verify 2026-05-01 已證**（kickoff 記憶 §3）：codex 0.125.0 idle TUI 完全靜態（1s + 2s 兩次 capture diff 空），無 timer / 無動畫；對話展開後 line 1-10 一定變化
- `IdleStableTicks: 0`（=default 3 = 1.5s）：用既有預設；穩定門檻適中。

**v5 2-phase contract（核心）**：

| Phase | 觸發 | armed 狀態 | callback 行為 |
|---|---|---|---|
| **A** passive 觀察 | detector 啟動 → `prober.Watch` 開始 500ms tick | `false` | `ScreenStable` → `armed.Store(true)` 進入 Phase B；`ScreenChanged` → drop（armed=false 期間「Phase A 進行中或失敗」一律 drop）|
| **B** active 監控 | `armed=true` 後 | `true` | `ScreenChanged` + `isCodexAlive()` → mutex 內 double-check + emit-once → return |

**2 case truth table**：

| Case | Phase A 結果 | Detector 行為 | Status 結果 |
|---|---|---|---|
| **1** | 穩定 ✓（dialog 渲染完 hash 凝固 1.5s）→ 進入 Phase B | 持續監控 ScreenChanged；user 按 1 批准 → ScreenChanged → emit-once | waiting → running |
| **2** | 一直不穩定 ✗（user 反應太快、tool 立即完成沒 hash 凝固期、dialog 重繪不停等）| 永遠 armed=false → 所有 ScreenChanged drop → 等 ctx cancel | waiting 不動，交給 hook |

**「快速輸入 / fast-with-output」概念只在 Phase A 失敗時有意義**：user 在 IdleStableTicks（1.5s）內反應 → Phase A 永遠不穩定 → 歸 case 2 by contract。Phase A 一旦成功（凝固過 1.5s），後續變化（不論快慢）都會被 Phase B 抓到。

**為什麼不繼續 v1-v4 的 detector-only 路線**：

| 版本 | 設計 | 撤回原因（round finding）|
|---|---|---|
| v1 | armed/ScreenStable + dispatcher re-arm | round 1 F1：armed=false 期間 quick-approval 反應 → 永不 emit（無明確 contract framing）|
| v2 | emit-once + F1 re-arm | round 2 F1：fast/silent re-baseline 漏 emit |
| v3 | retry-emit + dispatcher grace | round 3 F1：dialog evidence 跨 grace carryover → long-dialog false positive |
| v4 | detector-side grace gate + emit-once + sync.Once | round 4 F2：fast-with-output source-drop 漏（user 在 grace 內反應 + tool 完成 + 螢幕穩定 → grace 過後無新變化 → 漏 emit）；F1：sync.Once 永久熔斷不適用 transient identity false retry |

每輪 detector-only 嘗試補位都引入新 race；round 4 F2 已逼近物理約束（dialog 渲染 vs user-action 在 hash 層級無法區分）。**v5 reframe**：放棄補位，把「無法判讀」明列為 by-contract limitation；用 hook 作 secondary-signal cover（PdxStop 自然把 status 帶 idle，lights waiting → idle 跳過 running phase；可接受 tradeoff vs long-dialog false positive 主動誤標）。

**identity 防護**（v5 沿用 v3+ 方案）：

- `prober.FirstAliveAgentInTree(paneID)` 內部用 `ActivePanePID(target)` 對 paneID `%N` exact resolve（liveness.go line 36-42 註解明說；本 PR worktree 已驗）
- production binding：`func() bool { t, _, err := prober.FirstAliveAgentInTree(paneID); return err == nil && t == "codex" }`
- emit 前 mutex 鎖內**第二次** isCodexAlive（防 first-check pass 與 emit 之間 paneID reuse race，修 round 3 F2）
- IsAliveFor 內部仍用 PanePID（first-pane only）對 multi-pane window 不精確 — pre-existing infra bug，開 follow-up issue 追一致性 fix（不在本 PR scope）

**emit-once 機制**（v5 新設計，取代 v4 sync.Once）：

- `armed atomic.Bool`：Phase A→B 閘門（monotonic — 一旦 set true 永不回 false）
- `emitted atomic.Bool` + `sync.Mutex`：emit-once。**不用 sync.Once**（v4 撤回，因 sync.Once.Do 永久熔斷 — transient `isCodexAlive=false` 不該永久熔斷，需 retry-able）
- 流程：callback 拿 mutex → 檢查 `emitted.Load()` → 第二次 `isCodexAlive()` 重驗 → `select case out<-sig: case <-ctx.Done():` → 若 out 贏 `emitted.Store(true)` + `close(emittedCh)`
- 後續 callback：若 emitted=true → 直接 return（idempotent）；若 emitted=false（如 transient identity false）→ 仍可 retry（修 round 4 F1）

### 0.5 Reject path race 與 J3 dispatcher pre-grace 的分工

**Round 5 standard codex review 抓到的 reject path race**：Phase B `armed=true` 後，user 按 [2] 拒絕，dialog 消失也會觸發一次 `ScreenChanged` callback。同時 codex 發出 `PdxStop` hook。極端時序下 ScreenChanged callback 先到 daemon → detector emit-once → status `waiting → running`，PdxStop hook 後到 → status `running → idle`，lights 短暫閃 `waiting → running → idle`。

**處理層次（J3 PR #797 已 ship at alpha.281）**：

| 層次 | 機制 | cover 路徑 |
|---|---|---|
| **Detector**（本 PR） | dumb emit；callback 觀察到 ScreenChanged + alive 就 emit Signal，不知道 hook 有沒有要進來 | approve happy path（無 hook 競賽） |
| **Dispatcher**（J3 ship） | `consumeSignals` 進 `applyProbeGuards` 前 hold `probeIntentPreGraceWindow=300ms`；期間若同 session hook 進來（`recordHookAt`）→ `classifyAsHookRace` 認定 hook race → drop probe；無 hook → 進原 guard pipeline | reject 典型路徑：dialog 消失 ScreenChanged 在 hook 前 300ms 內到 → drop |
| **Boundary race**（known limitation） | hook 在 300ms hold 過後到 + `recordHookAt` 與 `applyProbeGuards` step 2 read 之間 μs window race | R13-style acceptable as known limitation；A9 quantified gate（30 次 reject 閃 < 3）作為漏網率測試，A9 fail 才開 followup issue 加 per-event trace |

**為什麼不在 detector 端解（per W6-3 §9.14 + fix-spec §3）**：

- pre-grace timer 是 dispatcher 跨 Kind 通用機制（J3 spec §6 + W6-3 §9.14 anchor）：W6-3 ProcessDead 與 W6-6 ScreenChange 都會撞 hook race，dispatcher generic 處理一次到位
- 在 detector 內加 pre-emit confirm rollback 等於 v5 contract 之外再起一個時間判斷層 — 與 v5「detector 不需時間判斷、Phase A 由 ScreenStable 自然 demarcate」的 contract 牴觸
- detector 仍是 dumb emit；lifecycle / race protection / hook authority 由 dispatcher 統一管 — 與 W6-3 spec §9.14 anchor 一致

**A9/A10 acceptance（mlab live verify）**：J3 PR ship 時 A9（reject 閃 < 3/30）+ A10（approve latency ≤ 500ms+300ms）標 deferred to W6-6（J3 在 main 上唯一 active ProbeIntent Kind 是 W6-3 ProcessDead，沒有 ScreenChange Kind 可量 reject race）。**本 PR mlab live verify 必須跑完整 30 次 reject 量化 + approve latency**，作為 J3 + W6-6 ship gate。

### 0.6 與 fix-spec §3 的對齊

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
   - `StartScreenChangeDetector(ctx, prober, isCodexAlive, paneID, senderPID, out)`；`isCodexAlive func() bool` 注入為 closure（production: 走 FirstAliveAgentInTree；test: 注入 fake）。**不需要 `now func()` time seam**（v5 不再有時間判斷）。
   - 內部用 `prober.Watch(paneID, WatchOptions{TopLines: 10}, cb)`
   - **2-phase + 2-case truth table contract**（v5）：
     1. 內部狀態：`armed atomic.Bool` + `emitted atomic.Bool` + `sync.Mutex` + `emittedCh chan struct{}`
     2. callback `ScreenStable` → `armed.Store(true)`（Phase A → Phase B；idempotent）
     3. callback `ScreenChanged`：if `!armed.Load()` drop / if `!isCodexAlive()` drop / `mu.Lock()` / if `emitted.Load()` return / if `!isCodexAlive()` second check return / `select case out<-sig: case <-ctx.Done():` → out 贏 `emitted.Store(true)` + `close(emittedCh)`；ctx 贏 → 不 emit
     4. main goroutine `select case <-emittedCh: case <-ctx.Done():` → `prober.StopWatch(paneID)` + return
     5. 其他 ScreenChangeKind 直接 ignore
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

**Truth table — Phase A / Phase B 行為矩陣**：

| 編號 | Phase A 結果 | Phase B 觸發 | isCodexAlive | 期望行為 |
|---|---|---|---|---|
| **A-Case1-Happy** | 穩定 ✓（armed=true）| ScreenChanged | true（first + second check 皆 true）| emit-once → status=running → entry teardown |
| **A-Case1-IdentityFalse** | 穩定 ✓（armed=true）| ScreenChanged | first false | drop（不入 mutex、不 set emitted）；後續 callback 仍可 retry |
| **A-Case1-IdentityRace** | 穩定 ✓（armed=true）| ScreenChanged | first true / mutex 內 second false | mutex 內 double-check 阻 emit；emitted 不 store；後續 callback 若 isCodexAlive 恢復 true 仍可 retry |
| **A-Case1-MultipleChanges** | 穩定 ✓（armed=true）| 多次 ScreenChanged + isCodexAlive=true | true | sync.Mutex + emitted=true 保證只 emit 一次；後續 callback 看 emitted=true 直接 return |
| **A-Case2-NoStable** | 不穩定 ✗（hash 持續變動，永無 ScreenStable）| ScreenChanged 多次 | (any) | armed=false → 全 drop → 等 ctx cancel；不 emit |
| **A-Case2-QuickApproval** | 不穩定 ✗（user 在 1.5s IdleStableTicks 內反應）| ScreenChanged | true | armed=false → drop；PdxStop hook 後續 cover → status=idle（lights waiting → idle，跳過 running phase；by-contract 非 bug）|
| **A-Case2-FastWithOutput** | 不穩定 ✗（user 反應 + tool 立即完成 + 短暫穩定不及 1.5s）| ScreenChanged | true | armed=false → drop；PdxStop hook 後續 cover → status=idle；by-contract case 2 |

**輔助條件（Lifecycle / Drift / Race / mlab）**：

| 編號 | 條件 |
|---|---|
| A-Lifecycle1 | waiting → idle (PdxStop hook) → dispatcher cancel ctx → main goroutine 從 `<-ctx.Done()` 返回 → StopWatch + return |
| A-Lifecycle2 | waiting → 跨 provider 切換（agent_type 改）→ reconcileSessionActive 取消 ScreenChange entry |
| A-Lifecycle3 | waiting → close pane → W6-4 ProcessDead PaneAlive=false → status=clear → ScreenChange entry 因 OnEntryStatus 退出被 cancel；ScreenChange detector ctx cancel 退出 |
| A-Lifecycle4 | armed=true 後 ctx cancel → main goroutine 從 `<-ctx.Done()` 返回 → StopWatch + return；callback 若 race fire 也走 mutex 內 ctx.Done 路徑（不 send-on-closed） |
| A-Drift | drift test 通過：startDetector switch case 數量 == supportedKinds 條目數 == ProbeIntents 宣告 Kind 集合 |
| A-NoSendOnClosed | dispatcher cancel ctx 期間 callback 在 mutex 內 select → ctx 路徑贏 → 不 send-on-closed-channel |
| A-mlab-1 | mlab live verify §1：codex permission ask → user 等 dialog 渲染完 ≥1.5s → 按 1 批准 → ≤500ms 內 ScreenChanged → emit → lights running ✓（case 1 happy path）|
| A-mlab-2 | mlab live verify §2：codex permission ask → user 按 2 拒絕 → PdxStop hook fires → lights idle（不誤觸 running；典型路徑由 J3 dispatcher pre-grace cover） |
| A-mlab-3 | mlab live verify §3：codex 在 waiting 時 user 主動關 pane → W6-4 ProcessDead PaneAlive=false → lights clear |
| A-mlab-4 | mlab live verify §4（**case 2 known limitation 觀察**）：codex permission ask → user 立即按 1（dialog 渲染未完）→ Phase A 失敗 → lights waiting；後續 PdxStop → lights idle（跳過 running phase；by contract 不算 bug，記入 PR body §test plan 預期）|
| **A9** (J3 ship gate, deferred to W6-6) | mlab live verify §5：30 次 reject 路徑量化測試 — codex permission ask → 等 dialog 渲染完 ≥1.5s → 按 [2] 拒絕；統計 lights 閃 `waiting → running → idle` 的次數 < 3/30 為 PASS（dispatcher pre-grace cover 漏網率）；A9 fail 開 followup issue 加 per-event trace 定位漏網類別 |
| **A10** (J3 ship gate, deferred to W6-6) | mlab live verify §6：30 次 approve 路徑 latency 量化測試 — codex permission ask → 等 dialog 渲染完 ≥1.5s → 按 [1] 批准；統計每次 ScreenChanged 抵達 daemon 到 SPA 收到 status=running broadcast 的 latency；30 次 P95 ≤ 500ms（probe tick）+ 300ms（pre-grace hold）= 800ms 為 PASS |

**Known limitations（by contract，非 bug）**：

- **quick-approval**：user 在 1.5s IdleStableTicks 內反應 → Phase A 沒進入 Phase B → case 2
- **fast-with-output**：user 反應 + tool 在短時間內完成 + 螢幕沒有穩定凝固期 → Phase A 失敗 → case 2
- 兩者均由 PdxStop hook 自然 cover：lights waiting → idle，跳過 running phase；secondary signal 可接受 tradeoff（vs alternative：long-dialog false positive 主動誤標）

---

## 2. 設計約束

### 2.1 必須

- 新 detector 命名 `internal/agent/codex/probe_intent_screen_change.go`（W6-3 spec §0.1 + audit §7.1 約束 detector 歸 agent package）
- detector 公開函式 `StartScreenChangeDetector(ctx, prober screenWatcher, isCodexAlive func() bool, paneID string, senderPID int, out chan<- agent.Signal)`，與 `StartProcessDeadDetector` 對稱（`isCodexAlive` 注入 closure 便測試 — production: `func() bool { t, _, err := prober.FirstAliveAgentInTree(paneID); return err == nil && t == "codex" }`）；**不需要 `now func()` time seam**（v5 不再有時間判斷）
- `screenWatcher` interface 是本 detector 包私有的 minimal contract，只暴露 `Watch(target, opts, cb)` + `StopWatch(target)` 兩個方法，**不直接 import `*probe.Prober`**——便於測試注入 fake，與 W6-3 `tmuxPaneLister` interface 同 pattern
- detector 採 **2-phase + 2-case truth table contract**（v5）：
  - **Phase A（armed=false）**：等 `ScreenStable` event → `armed.Store(true)` → 進入 Phase B
  - **Phase B（armed=true）**：每次 `ScreenChanged` callback → 若 `!armed.Load()` drop / 若 `!isCodexAlive()` drop / mutex 鎖內檢查 `emitted.Load()` + 第二次 `isCodexAlive()` 重驗 → `select case out<-sig: case <-ctx.Done():` 配 `emitted.Store(true)` + `close(emittedCh)`
  - **main goroutine**：`select case <-emittedCh: case <-ctx.Done():` → `StopWatch(paneID)` + return
- 用 `armed atomic.Bool` 作 Phase A→B 閘門（不用 mutex 因為 ScreenStable 是 monotonic：一旦 set true 永不回 false；多次 ScreenStable 重複 Store 是 idempotent）
- 用 `emitted atomic.Bool` + `sync.Mutex` 作 emit-once（**不用 sync.Once**，因 sync.Once.Do 是 fire-and-forget 永久熔斷；transient `isCodexAlive=false` 不該永久熔斷，需 retry-able；修 round 4 F1）
- emit 前在 mutex 鎖內**第二次** `isCodexAlive()` 重驗（防 first-check pass 與 emit 之間 paneID reuse race，修 round 3 F2）
- callback 對非 `ScreenStable` / `ScreenChanged` Kind 直接 ignore
- main goroutine `<-ctx.Done()` arm 必須在每個 case 後 `StopWatch` + return（避免 watcher leak）
- `Provider.ProbeIntents()` 回傳 slice 順序：`ProcessDead` 在前、`ScreenChange` 在後（穩定順序便於測試 fixture 對齊）

### 2.2 不可

- ❌ 不直接 import `internal/agent/probe.Prober`（用 minimal interface 注入；同 W6-3 模式）
- ❌ 不在 detector 內讀 `m.currentStatus` 或 `m.activeProbeIntents`（dispatcher 已負責 lifecycle）
- ❌ 不複用 `ProbeIntentKindProcessDead` 常數（語意不同，drift test 會抓）
- ❌ 不在 `OnSignal` 內 emit log / metric（dispatcher consumeSignals 已 emit `[probe-intent] signal …`）
- ❌ 不採用 `sync.Once`（v4 撤回；round 4 F1 — Once.Do 永久熔斷不適用 transient identity false retry-able 場合）
- ❌ 不採用 detector-side grace gate / `now func()` time seam / `screenChangeGraceBuffer` 常數（v4 撤回；round 4 F2 — fast-with-output source-drop 漏；v5 由 ScreenStable 自然 demarcate Phase A，不需時間判斷）
- ❌ 不採用 retry-emit pattern（v3 撤回；round 3 F1 dialog evidence 跨 grace carryover）
- ❌ 不採用 emit-once-and-return + dispatcher F1 re-arm（v2 撤回；round 2 F1 fast/silent re-baseline 漏）
- ❌ 不依賴 detector 自己解 quick-approval / fast-with-output / long-dialog 等邊界（物理約束下無解；v5 contract 明列為 case 2 known limitation）
- ❌ 不抓 codex specific glyph / 字串 pattern（agent 改 TUI 即 break；fix-spec §3 + audit §7.1）
- ❌ 不引入 sustained-change counter（mlab live verify 已證 idle TUI 完全靜態 + scroll 不影響 capture-pane；counter 是預先優化雜訊）

### 2.3 既知 race / edge case

| ID | 場景 | 處置 |
|---|---|---|
| R1 | ctx cancel 後 prober callback 仍 fire 一兩次（500ms tick race）| callback 內 mutex 鎖內 `select case out<-sig: case <-ctx.Done():` 防 send-on-closed-channel；若 ctx 贏，emitted 不 store，emittedCh 不 close |
| R2 | ScreenStable 多次 fire（Watch 內部 counter reset 後若 hash 又連續 IdleStableTicks 同會再 emit）| `armed.Store(true)` idempotent；多次 Store 為 true 等價無副作用 |
| R3 | callback A 拿 mutex emit 後 callback B 進入 mutex | callback B 看 `emitted.Load() == true` → 直接 return（無 double emit）|
| R4 | callback 拿 mutex 內 emit 與 ctx cancel 競賽 | mutex 內 `select case out<-sig: case <-ctx.Done():` — ctx 贏不 emit、out 贏 emit；emitted 只在 out 贏時 store |
| R5 | first isCodexAlive=true、mutex 內 second isCodexAlive=false（paneID reuse race）| mutex 內 second check 阻 emit；emitted 不 store；release mutex 後下次 ScreenChanged + isCodexAlive 恢復 true 仍可 retry（修 round 4 F1 sync.Once 永久熔斷）|
| R6 | W6-3 ProcessDead intent 與 W6-6 ScreenChange intent 同時 active | 不同 Kind，dispatcher per-(session, kind) 分槽；reconcile 只看 declaredKinds，不衝突 |
| R7 | `prober.Watch(paneID, ...)` 與 W3-revert 後 production caller 為 0 的事實 | W6-6 是 W3 撤回後 `Prober.Watch` 的**第一個 production caller**；測試用 fake prober，production 用 module.prober |
| R8 | tmux server restart + paneID reuse 場景 | callback 內每次 ScreenChanged 都驗 `isCodexAlive()`；mutex 內第二次驗（修 round 3 F2）；FirstAliveAgentInTree 用 ActivePanePID 對 paneID exact resolve（修 round 3 F3 PanePID 不一致）|
| R9 | `FirstAliveAgentInTree` transient query failure（tmux 短暫無回應）| 回 `("", 0, err)`；callback 視為 false drop；下次 ScreenChanged 重試（mutex 內 emitted 仍 false，可 retry — 修 round 4 F1 sync.Once 永久熔斷不適用 retry-able 場合）|
| R10 | **case 2 known limitations**（quick-approval / fast-with-output）| Phase A 不穩定 → armed=false → 所有 ScreenChanged drop；PdxStop hook 自然把 status 帶 idle；lights waiting → idle 跳過 running phase；by-contract，非 bug |
| R11 | `Prober.IsAliveFor` 內部 PanePID 不一致（round 3 F3 pre-existing infra bug）| W6-6 不用 IsAliveFor，改 FirstAliveAgentInTree 規避；開 follow-up issue 追 IsAliveFor 一致性 fix（不在本 PR scope）|
| R12 | armed=true 後又 fire ScreenStable（screen 又穩定一次）| `armed.Store(true)` idempotent；後續 ScreenChanged 仍走 Phase B 流程不影響 |
| R13 | dialog 渲染期間連發 ScreenChanged | armed=false → 全 drop；dialog 渲染穩定（連續 1.5s 同 hash）後 ScreenStable → armed=true → 後續 user-action ScreenChanged 才會被 Phase B 接收（v5 用 ScreenStable 取代 v4 grace gate；天然處理 long-dialog 與 dialog-noise）|
| R14 | **Reject path race**：armed=true 後 user 按 [2] 拒絕 → dialog 消失發 ScreenChanged + PdxStop hook 同時往 daemon 走，極端時序 ScreenChanged 先到 → emit → status `waiting → running`，PdxStop 後到 → `running → idle` → lights 短暫閃 | **不在 detector 端處理**（per W6-3 §9.14 anchor + fix-spec §3）；J3 dispatcher 雙向 graceWindow（PR #797 ship at alpha.281）`consumeSignals` 進 `applyProbeGuards` 前 hold 300ms `probeIntentPreGraceWindow`；同 session hook 進來 → `classifyAsHookRace` → drop probe；其餘邊界（hook 在 hold 過後到 + step 2 read μs window）為 R13-style known limitation；**A9 quantified gate 30 次 reject 閃 < 3 為 ship 漏網率測試**（A9 fail 開 followup issue 加 per-event trace） |

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

### 4.3 Detector：`StartScreenChangeDetector`（v5，2-phase + 2-case truth table contract）

```go
// internal/agent/codex/probe_intent_screen_change.go
package codex

import (
    "context"
    "sync"
    "sync/atomic"

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

// StartScreenChangeDetector watches a codex pane and emits a single Signal
// once the screen contract is satisfied. The detector is a 2-phase passive
// observer with a 2-case truth table:
//
//   Phase A (passive observation, armed=false): wait for ScreenStable.
//   ScreenChanged events in this phase are dropped (dialog render bursts,
//   user typing into the prompt before the pane settles, etc.).
//
//   Phase B (active monitoring, armed=true): once ScreenStable arms the
//   detector, the next ScreenChanged with isCodexAlive=true emits the
//   Signal exactly once. Subsequent ScreenChanged events are no-ops
//   because emitted=true.
//
//   Case 1: Phase A succeeds — dialog renders, hash settles for
//   IdleStableTicks consecutive ticks (default 3 = 1.5s). User approval
//   triggers ScreenChanged → emit → status: waiting → running.
//
//   Case 2: Phase A never succeeds — user reacts inside IdleStableTicks,
//   tool completes silently before the pane settles, dialog redraws
//   indefinitely, etc. armed stays false; all ScreenChanged drop;
//   detector waits for ctx cancel. Lights stay waiting until the PdxStop
//   hook transitions to idle (skipping the running phase by contract).
//
// "Quick approval" and "fast with output" are case-2 by contract, not bugs.
// The PdxStop hook is the secondary-signal cover. This is the accepted
// tradeoff against introducing long-dialog false positives (see spec §0.4
// v1-v4 retrospective).
//
// Why atomic.Bool + sync.Mutex instead of sync.Once (v4 retired — round 4 F1):
//   sync.Once.Do permanently fires after its first invocation regardless
//   of whether the inner emit succeeded. A transient isCodexAlive=false
//   (tmux query failure, brief pane reuse) inside Once.Do would
//   permanently disable the detector. v5 uses atomic.Bool for emitted plus
//   a sync.Mutex so a failed emit attempt leaves emitted=false and the
//   next ScreenChanged can retry.
//
// Why two isCodexAlive checks (v3 round 3 F2, carried into v5):
//   The first check (outside the mutex) short-circuits dialog noise from
//   a pane that has already been reused by another process before the
//   detector has a chance to acquire the mutex. The second check (inside
//   the mutex, before emit) closes the race between the first check and
//   the actual send.
//
// Why FirstAliveAgentInTree (production wiring, v3 round 3 F3, carried into v5):
//   prober.IsAliveFor internally uses tmux PanePID(target), which returns
//   the first pane of the target's window — wrong for paneID %N in
//   multi-pane windows. prober.FirstAliveAgentInTree uses ActivePanePID,
//   which honors pane id targets exactly. The IsAliveFor inconsistency is
//   pre-existing infrastructure tracked in a follow-up issue (out of
//   scope for W6-6 PR). Production binds:
//
//     isCodexAlive := func() bool {
//         t, _, err := prober.FirstAliveAgentInTree(paneID)
//         return err == nil && t == "codex"
//     }
func StartScreenChangeDetector(
    ctx context.Context,
    prober screenWatcher,
    isCodexAlive func() bool,
    paneID string,
    senderPID int,
    out chan<- agent.Signal,
) {
    var (
        armed     atomic.Bool
        emitted   atomic.Bool
        mu        sync.Mutex
        emittedCh = make(chan struct{})
    )
    sig := agent.Signal{
        Kind:      agent.ProbeIntentKindScreenChange,
        PaneAlive: true,
        PaneID:    paneID,
        SenderPID: senderPID,
    }
    cb := func(ev probe.ScreenChangeEvent) {
        switch ev.Kind {
        case probe.ScreenStable:
            // Phase A → Phase B. Idempotent across multiple ScreenStable
            // (e.g. the watcher's stable counter resets after fire and
            // can fire again if the hash stays put for another window).
            armed.Store(true)
        case probe.ScreenChanged:
            if !armed.Load() {
                // Phase A still in progress (or failed) — drop. Case 2
                // path stays here permanently.
                return
            }
            if !isCodexAlive() {
                // Pane no longer hosts a codex process (reused after
                // tmux server restart, etc.) — drop. emitted stays
                // false so a later ScreenChanged with a recovered
                // identity can still emit.
                return
            }
            mu.Lock()
            defer mu.Unlock()
            if emitted.Load() {
                // Idempotent: another callback already emitted.
                return
            }
            // Re-verify identity inside the critical section to close
            // the race between the outer isCodexAlive check and the
            // emit. If the second check fails, emitted stays false and
            // the next ScreenChanged can retry — this is the v5 fix
            // for round 4 F1 (sync.Once permanent fuse).
            if !isCodexAlive() {
                return
            }
            select {
            case out <- sig:
                emitted.Store(true)
                close(emittedCh)
            case <-ctx.Done():
                // ctx wins: emitted stays false, emittedCh stays open.
                // The main goroutine's <-ctx.Done() arm handles
                // teardown. No send-on-closed-channel risk.
            }
        }
    }
    prober.Watch(paneID, probe.WatchOptions{TopLines: screenChangeTopLines}, cb)
    select {
    case <-emittedCh:
    case <-ctx.Done():
    }
    prober.StopWatch(paneID)
}
```

**v5 2-phase + emit-once 機制細節**：

- `armed atomic.Bool`：Phase A→B 閘門。`ScreenStable` callback `armed.Store(true)` 進入 Phase B。Monotonic — 多次 Store 為 true 等價。Watch 內部 stable counter 在 fire 後 reset，可能再次 fire；`armed` idempotent 處理。
- `emitted atomic.Bool` + `sync.Mutex` + `emittedCh chan struct{}`：emit-once。callback 進入 mutex 後檢查 `emitted.Load()` + 第二次 `isCodexAlive()`，pass 後 `select case out<-sig` 配 `<-ctx.Done()`；out 贏 → `emitted.Store(true)` + `close(emittedCh)`；ctx 贏 → emitted 不 store，emittedCh 不 close。
- callback `ScreenStable`：set armed → return。`ScreenChanged`：4-step gate（armed → first isCodexAlive → mutex emitted check → mutex second isCodexAlive）→ emit。其他 Kind 直接 ignore。
- main goroutine `<-emittedCh | <-ctx.Done()`：任一觸發 → StopWatch + return。
- detector 不 close out channel（dispatcher startDetector wrap goroutine 在 detector return 後 close）。F1 re-arm 路徑：Phase A 失敗（case 2）→ 永無 emit → dispatcher 因 status 變化（PdxStop → idle）cancel ctx → wrap close(out) → consumeSignals appliedAny=false → F1 由 dispatcher 處理（與 W6-3 ProcessDead 對 OnEntryStatus 退出時的處理一致）。
- **不需要 mirror `probeGraceWindow` const**（v4 撤回）。v5 由 ScreenStable 自然 demarcate Phase A，無時間判斷邏輯，detector 包不再依賴 dispatcher private 常數。

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
| P1-T2 | `internal/agent/codex/probe_intent_screen_change.go` | 新檔：`screenWatcher` interface + `screenChangeTopLines` const + `StartScreenChangeDetector(ctx, prober, isCodexAlive, paneID, senderPID, out)`（v5 — 不需 `now func()` time seam、不需 grace gate / mirror const）；內部用 `armed atomic.Bool` + `emitted atomic.Bool` + `sync.Mutex` + `emittedCh chan struct{}` |
| P1-T3 | `internal/agent/codex/probe_intent_screen_change_test.go` | 表驅動 tests（覆蓋 v5 truth table）：(1) **A-Case1-Happy**：ScreenStable → ScreenChanged + alive=true → emit / (2) **Case2-NoStable**：armed=false 期間 ScreenChanged 多次 → 全 drop（無 emit、emittedCh 未 close）/ (3) **A-Case1-IdentityFalse**：Phase B + alive=false → drop（emitted 仍 false，可 retry）/ (4) **A-Case1-IdentityRace**：first check true、mutex 內 second check false → 不 emit、emitted 仍 false / (5) **A-Case1-MultipleChanges**：Phase B 多次 ScreenChanged + alive=true → 只 emit 一次（mutex 內 emitted check）/ (6) **ScreenStable idempotent**：多次 ScreenStable → armed 持續 true，無副作用 / (7) **retry after transient false**：alive=false drop → 後續 alive 恢復 true → ScreenChanged → 成功 emit（修 round 4 F1 — sync.Once 永久熔斷不會發生）/ (8) ctx cancel before ScreenStable → main goroutine `<-ctx.Done()` → StopWatch + return / (9) ctx cancel during emit select → ctx.Done 贏 → emitted 不 store、emittedCh 不 close（不 send-on-closed）/ (10) other ScreenChangeKind ignored |
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
| P3-T1 | `internal/module/agent/probe_intent_dispatcher_integration_test.go` | 端到端 lifecycle（覆蓋 v5 truth table 主路徑）：(a) **A-Case1-Happy**：waiting hook → arm intent → fake prober ScreenStable → ScreenChanged + alive=true → emit → status=running + teardown / (b) **long-dialog 自然 cover**：waiting → 注入連發 ScreenChanged 模擬 dialog 渲染 → drop（armed=false）→ 注入 ScreenStable → 9 秒虛擬時間後 ScreenChanged → emit → running ✓（不再依賴 grace gate）/ (c) **A-Case2-FastWithOutput**：waiting → 注入 ScreenChanged 多次（無 ScreenStable）+ 後續 idle hook → no emit + status=idle（PdxStop cover；by-contract case 2）/ (d) waiting → idle hook → teardown 不 emit / (e) cross-provider switch → reconcile teardown / (f) **A-Case1-IdentityFalse**：fake isCodexAlive=false → drop；emitted 仍 false / (g) **A-Case1-IdentityRace**：注入 isCodexAlive 第一次 true、mutex 內 second false → 不 emit、emitted 仍 false / (h) **retry after transient false**：alive 從 false 恢復 true → 後續 ScreenChanged → 成功 emit（驗 v5 修 round 4 F1）|
| P3-T2 | mlab live verify | §1 approval reply（case 1 happy path）/ §2 reject reply / §3 close pane during waiting / §4 quick-approval（case 2 known limitation 觀察 — 預期 lights waiting → idle 跳過 running phase；by-contract 不算 bug，記入 PR body §test plan）/ **§5 A9 30 次 reject 量化（閃 running < 3 為 PASS；J3 ship 時 deferred 的 ship gate）** / **§6 A10 30 次 approve latency 量化（P95 ≤ 800ms）**；建 dev log + screenshot 證據；spec → plan 階段 placeholder，PR body §test plan checklist 條列；A9 fail 開 followup issue 加 per-event trace 定位漏網類別 |

---

## 7. 驗收條件（重述 §1.3 Acceptance）

- `A-Case1-*` + `A-Case2-*` + `A-Lifecycle*` + `A-Drift` + `A-NoSendOnClosed` unit + integration test 全綠
- `A-mlab-1` / `A-mlab-2` / `A-mlab-3` mlab live verify 全 PASS；`A-mlab-4`（case 2 known limitation 觀察）by-contract 預期 lights waiting → idle 跳過 running phase（非 bug，PR body §test plan 預先標註）
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
| 想刪掉 `armed atomic.Bool` / ScreenStable arming（直接收第一個 ScreenChanged 就 emit） | 等同 v1 設計，已被 round 1 F1 quick-approval race 抓到；ScreenStable arming 是 v5 contract 核心 — Phase A 是設計而非 bug |
| 想恢復 emit-once-and-return + dispatcher F1 re-arm（無 ScreenStable arm 機制）| v2 設計，已被 round 2 F1 fast/silent re-baseline 漏 emit 抓到；v5 走 Phase A→B contract，case 2 path 由 hook 自然 cover |
| 想恢復 isPidAlive 單獨 check（不查 paneID identity） | v2 設計，已被 round 2 F2 paneID reuse 漏抓到；v3+ 改 FirstAliveAgentInTree paneID exact resolve |
| 想恢復 retry-emit pattern（pending atomic + main goroutine ticker）| v3 設計，已被 round 3 F1 dialog-render evidence 跨 grace carryover 抓到；v5 不需要 retry 機制（Phase A by design）|
| 想恢復 detector-side grace gate / `now func()` time seam / `screenChangeGraceBuffer` const / mirror `probeGraceWindow` | v4 設計，已被 round 4 F2 fast-with-output source-drop 漏抓到；v5 由 ScreenStable 自然 demarcate Phase A，detector 不再需要時間判斷 |
| 想恢復 `sync.Once` 包 emit | v4 設計，已被 round 4 F1 永久熔斷不適用 transient identity false retry-able 場合抓到；v5 改 atomic.Bool + Mutex |
| 想用 `IsAliveFor("codex", paneID)` 取代 FirstAliveAgentInTree | round 3 F3：IsAliveFor 內部 PanePID 對 multi-pane window 不精確；v3+ 改用 FirstAliveAgentInTree（內部 ActivePanePID exact resolve）|
| 想 detector 自己解 quick-approval / fast-with-output / long-dialog 邊界 | 物理約束（dialog 渲染 vs user-action 在 hash 層級無法區分）；v1-v4 各自嘗試都引入新 race；v5 reframe 為 by-contract case 2 known limitation，PdxStop hook 作 secondary cover |
| 想擴 `StartScreenChangeDetector` signature 加 `now func() time.Time` 或 grace 相關參數 | v5 不需時間判斷；signature `(ctx, prober, isCodexAlive, paneID, senderPID, out)` 即可；不需要 dispatcher 私有常數 mirror |
| 想 generalize 為 「per-agent ScreenChangeProfile」（cc / opencode 也用） | fix-spec §3 撤回 framework；W6-1b cc 已降級不做、W6-5 opencode 走 plugin |
| 想 detector 內部直接讀 `m.activeProbeIntents` / `m.currentStatus` | dispatcher 已負責 lifecycle；detector 只發 Signal |
| 想抓 codex 特定 glyph / 字串 pattern（spinner / "Approved." / etc）| audit §7.1：agent 改 TUI 即 break；fix-spec 撤回 |
| 想擴 `startDetector` signature 加 session target | paneID 已是合法 capture-pane target（spec §4.4） |
| 想 ScreenChange 觸發後 emit 多次 Signal | v5 emit-once 設計：emitted atomic.Bool + sync.Mutex + emittedCh；dispatcher F1 re-arm 路徑由 case 2 path（永無 emit）走 status-driven cancel，與 W6-3 ProcessDead OnEntryStatus 退出處理一致 |
| 想為 ScreenChange Kind 特化 pre-grace timer（如只對 ScreenChange hold 300ms / ProcessDead 不 hold） | **與 W6-3 §9.14 anchor 對齊** — J3 PR #797（spec `2026-05-01-probe-intent-bidirectional-grace-window-spec.md`）已確立 pre-grace timer 在 dispatcher `consumeSignals` 內 generic 對所有 ProbeIntent Kind 適用（per fix-spec §3 不為單一 Kind 特化約束）；W6-3 ProcessDead 也撞 hook race，dispatcher generic 處理一次到位；本 PR 不在 detector 端加 pre-hold；hook race protection 由 J3 dispatcher 接管，detector 仍是 dumb emit |
| 想在 W6-6 detector 內加 reject path race 處理（pre-emit confirm rollback / post-emit retract / probe-side hold）| §0.5 + R14 已明訂：reject race 由 J3 dispatcher pre-grace + `classifyAsHookRace` cover；detector 不知道 hook 有沒有要進來、不應做時間判斷（與 v5 contract「detector 不需時間判斷」牴觸）；boundary race acceptable as known limitation by R13/R14 fail-fast handling（A9 mlab gate 監控漏網率）|
| 想把 J3 spec `probeIntentPreGraceWindow=300ms` 改名 / 改值 / 加新 const for ScreenChange | J3 已 ship at alpha.281 為 dispatcher 跨 Kind 通用 const；W6-6 不動 J3 contract（per W6-3 §9.14）；若改值需走獨立 J3 後續 spec，不在本 PR scope |

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

- W6-3 spec：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md`（interface finalize / dispatcher / drift gate / 11 finding 收斂 / §9.14 generic-Kind 不特化 anchor）
- W6-3 plan：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-plan.md`（14 task / 5 輪 review）
- J3 bidirectional graceWindow spec：`docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-spec.md`（v7.5；dispatcher 雙向 `probeIntentPreGraceWindow=300ms` + `classifyAsHookRace` helper；PR #797 ship at alpha.281）
- J3 plan：`docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-plan.md`
- Fix-spec：`docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3（framework 撤回約束）
- W1 audit：`docs/specs/2026-04-28-hook-status-audit-spec.md` §6 / §7 / §7.1（缺口工作池 + 設計約束）
- Lights rebuild spec：`docs/specs/2026-04-23-lights-rebuild-spec.md` §8.2（ProbeIntent 起源）
- Probe primitives：`internal/agent/probe/probe.go` / `internal/agent/probe/activity.go`
- W6-3 detector：`internal/agent/codex/probe_intent_process_dead.go`
- Dispatcher：`internal/module/agent/probe_intent_dispatcher.go`

---

## 11. Open questions（已隨 round 1-4 收斂 + v5 reframe + v6 J3 cross-layer）

1. **`screenWatcher` interface 暴露面** ✅ — 只暴露 Watch / StopWatch 足夠；整合測試驗 status 翻轉而非 prober 內部狀態。
2. **`OnEntryStatus = {Waiting}`** ✅ — 不含 Running；running 已是目標，ScreenChange running→running 是 noop 但會 spam log。
3. **F1 re-arm 互動** ✅ — v5 contract：Phase A 失敗（case 2）→ 永無 emit → applied=false → dispatcher 因 status 變化（PdxStop → idle）cancel ctx → wrap close(out) → consumeSignals appliedAny=false → F1 由 dispatcher 處理（與 W6-3 ProcessDead OnEntryStatus 退出時的處理一致）。
4. **`prober` 在 Init 後才 ready** ✅ — closure 用 `mod.prober` lazy-resolve；Module.New 順序與 tmux 同模式（W6-3 已驗）。
5. **`FirstAliveAgentInTree` 已用 ActivePanePID** ✅ — 本 PR worktree 已驗 `internal/agent/probe/liveness.go` line 64 `panePIDRaw, err := p.tmux.ActivePanePID(target)`，line 36-42 註解明說對 paneID `%N` exact resolve（PR #638 codex review round 1 P2 fix 已落地）。
6. **detector 不依賴 `probeGraceWindow` mirror const** ✅ — v5 移除 grace gate，detector 不需要時間判斷，沒有 cross-package mirror 漂移問題。

### 11.1 v5 後 + J3 ship 後仍 open（給 round 6 review）

- **case 2 known limitation 觀察**：mlab live verify §4 預期會抓到 quick-approval / fast-with-output 不 emit running 的情境；by-contract 不算 bug，PR body §test plan 預先標註觀察行為（waiting → idle 跳過 running phase 是 contract 正確）。
- **Reject path race**（R14）已 J3 ship at alpha.281 cover：J3 dispatcher 雙向 `probeIntentPreGraceWindow=300ms` + `classifyAsHookRace` helper 已落地（spec `docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-spec.md` v7.5 + PR #797）。本 PR mlab live verify 必須跑完整 30 次 reject 量化（A9 < 3）+ approve latency（A10 P95 ≤ 800ms），作為 J3 + W6-6 ship gate；A9 fail 才開 followup issue 加 per-event trace（per J3 R13 fail-fast handling）。
- **若後續 codex permission flow 改成 hook 通知 approval**：本 detector 可整個拿掉（spec drift signal — W6-6 是 hook 缺口的補位，hook 補上後 detector 變多餘）。
- **IsAliveFor 一致性 follow-up issue**：本 PR 不修；開 GH issue 描述 PanePID vs ActivePanePID 對 paneID target 行為差異，建議 IsAliveFor 改 ActivePanePID 並加 multi-pane test。Issue 標 W6-6 spec round 3 F3 derive。
- **integration test fake prober 能力**：P3-T1 需 fake prober 支援同時 fire ScreenStable + ScreenChanged 序列。實作面確認既有 W6-3 wire test fake 是否可擴或需重寫；若重寫，需在 plan 階段標 task 規模。

