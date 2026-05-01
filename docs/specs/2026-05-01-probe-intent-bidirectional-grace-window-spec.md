# Probe Intent bidirectional graceWindow spec

> **Status**：v7.2（v6 後 codex consulting `task-mon19xir-vcknzb` 判定 R3-R5 累積為 review-driven scope creep；R13 已標 known limitation 與「要求 full reconstruction observability 當 ship gate」自相矛盾。Trim 範圍：移 (a) §1.1 #5 兩個 test seam（`preGracePostCheckHookFn` / `preGracePostStep2HookFn`） — case 5 用既有 `applyProbeGuards` unit test pattern、case 6 R13 boundary race 不為它新增 production seam，(b) §1.1 #6 dev-mode trace log 9 欄位 + 跨 log 對齊契約 — 改走 fail-fast：mlab A9 PASS 即 ship、A9 fail 才開 followup issue 加 trace 定位漏網類別，(c) §1.3 A13 acceptance 與 §6 三條 anchor（「想跳過 A13」/「想 lower A13」/「想引入 post-emit confirm rollback」）— 與 R13 known limitation 自相矛盾且把 review-driven scope creep 固化成 drift rule，(d) §1.2 `applyProbeGuards` test-only nil-check 例外段落 — 隨 seam 一起移除；保留：核心 pre-hold 300ms + 3 metrics + ctx cancel + drop reason + A1-A12 + A9 quantified gate（30 次 reject 閃 < 3）+ W6-3/W6-6 spec drift anchor。**v7→v7.2**：codex plan review 兩輪採納 — round 1 thread `019de428-def1-7533-80f4-cf3b959f7377` 抓 (P1) `probeIntentOnDropForSession` 是 package-level closure factory `(session, kind) func(reason)`，不是 Module field、(P2) 漏 preserve 既有 `MetricProbeIntentSignalEmitted` 計數；round 2 thread `019de42e-d05f-7781-84c9-3ab5d899826d` 抓 (P2) plan helper `captureDrops` 透過 helper callback intercept 不可行（package-level function 無 seam，與 v7 trim「不加 seam」衝突）→ 改 metric-only reason 斷言策略、(P3) §3.4 GoDoc 列 `mapping` reason 與實際 `applyProbeGuards` step 3 行為不符（`return false, ""` 不呼叫 OnDrop）→ 移除 `mapping` 行；§3.1 / §3.4 / plan P1-T3 / P1-T4 全部修；行為與 trim 範圍不變）。將既有 `probeGraceWindow`（post-direction only：hook 後 2s drop probe）擴成雙向 — ProbeIntent dispatcher `consumeSignals` 在進入 `applyProbeGuards` 之前先 hold `probeIntentPreGraceWindow`（300ms），期間若同 session hook 進來（`recordHookAt`）→ drop probe Signal；無 hook → 進原 guard pipeline。**Boundary race 仍存在**（hook 在 hold 過後到 + `recordHookAt` 與 step 2 read μs window race），acceptable as known limitation by R13；mlab live verify A9 quantified gate（30 次 reject 閃 running < 3）作為 production 漏網率測試，A9 fail 走 followup issue 加 per-event trace 定位。
>
> **動因**：W6-6 v5 spec round 5 standard codex review 抓到 reject path race — Phase B armed=true 後 user 按 [2] 拒絕，dialog 消失發 ScreenChanged 與 PdxStop hook race；極端場景 probe 先到 daemon → emit running → hook 後到覆蓋 idle → lights 短暫閃 `waiting → running → idle`。既有 post-direction graceWindow 只能壓 hook **之後** 的 probe，無法壓 hook **之前** 已 emit 的 probe。
>
> **設計來源**：codex job `task-momrowao-216mcq`（4m 18s, effort=high, model=spark）評估結果完整採納。
>
> **Worktree**：`.claude/worktrees/probe-intent-bidirectional-grace` / branch `worktree-probe-intent-bidirectional-grace`
> **Base**：`origin/main` @ alpha.280（codex broker P1 `13e91c64` + bump `5d40e2a2` 之後）
> **依賴**：
> - `internal/module/agent/probe_intent_dispatcher.go` — `consumeSignals` 5-case lifecycle（W6-3 P1-T4 引入）
> - `internal/module/agent/probe_orchestrator.go` — `applyProbeGuards` 4-step pipeline / `probeGraceWindow` 2s post-direction / `recordHookAt` 機制
> - `internal/module/agent/handler.go:380-413` — hook entry 與 `recordHookAt` 順序（codex finding #3 regression 防護）
> - `docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md` — ProbeIntent interface finalize
> - `docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md` — W6-6 v5 spec（reject race finding 來源）
> - `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3 — 不為單一 Kind 特化約束
> - `docs/specs/2026-04-30-daemon-hook-pipeline-lag-analysis.md` §2.5 — `pdx hook` CLI cold start 80-250ms 數據

---

## 0. 來龍去脈

### 0.1 直接動因（W6-6 round 5 finding）

W6-6 v5 spec 採 2-phase + 2-case truth table contract（Phase A `ScreenStable` arm → Phase B `ScreenChanged` emit-once）。Round 5 standard codex review 抓到 P2 finding：

> v5 contract 仍會在 stable dialog 的拒絕路徑把單純 dialog 變動當作 approval running，與文件自己的 A-mlab-2 驗收條件衝突。

具體場景：
1. user 看 codex permission dialog 思考 ≥1.5s → ScreenStable → armed=true（Phase B）
2. user 按 [2] 拒絕，codex 同時：
   - emit PdxStop hook（CLI 啟動 + HTTP 進 daemon → handler 設 status=idle）
   - dialog 消失 → 下個 prober 500ms tick 抓 ScreenChanged → detector emit Signal → mapper → status=running
3. **race window**：既有 post-direction graceWindow 只在 hook 進 daemon **之後** 啟動。極端場景下 probe 先到 daemon → status=running emit → hook 後到 → status=idle → lights 短暫閃 `waiting → running → idle`。

### 0.2 既有 graceWindow post-direction-only 機制

`internal/module/agent/probe_orchestrator.go`:

```go
const probeGraceWindow = 2 * time.Second  // line 27
```

`applyProbeGuards` step 2（line 287）：
```go
if hasHook && orchNowFn().Sub(last) < probeGraceWindow {
    agentpkg.MetricProbeGraceWindowSuppressed.Add(1)
    return false, ""  // drop probe Signal
}
```

`handler.go:401` hook entry 開 graceWindow：`m.probeOrch.recordHookAt(session)`。

**行為**：hook 後 2 秒內任何 probe Signal drop。**hook 之前** 已 emit 的 probe Signal 不會被回頭壓制 — 這是 reject race 失敗 path 的根因。

`probeGraceWindow=2s` 的由來：`docs/specs/2026-04-23-lights-rebuild-spec.md:71` 寫「graceWindow（例 2 秒）」— 範例值，**無嚴格量化依據**。

### 0.3 雙向擴展核心邏輯

ProbeIntent dispatcher `consumeSignals`（`probe_intent_dispatcher.go:515`）在 signal 進入 `applyProbeGuards` 之前先 hold `probeIntentPreGraceWindow=300ms` timer：

| timer 期間事件 | 行為 |
|---|---|
| ctx cancel | drop signal，記 `pre-grace-canceled` metric，不進 apply |
| 同 session hook 到（`lastHookAt[session]` updated）| drop signal，記 `pre-grace` reason metric，不進 apply |
| timer 過期，無 hook | 進原 `applyProbeGuards` 流程（含既有 post graceWindow 仍會 cover hook 已到的 case）|

**核心契約**：probe Signal 必須在 `applyProbeGuards` 前等候 N ms，給「同時 race 的 hook」一個進 daemon 的窗口。

### 0.4 為何放在 consumeSignals 不放在 applyProbeGuards

`applyProbeGuards`（`probe_orchestrator.go:265`）是 W3 撤回後保留的 shared guard pipeline（StaleCheck / graceWindow / Mapping / ErrorGuard / transition gate）。它同時服務：

1. **legacy ScreenChange watcher path**（`interpretScreenEvent` 經由 `applyProbeGuards`）— W3 撤回後雖無 production caller 但測試與 metric 仍依賴
2. **新 ProbeIntent dispatcher path**（`consumeSignals` 經由 `applyProbeGuards`）— W6-3 引入

在 `applyProbeGuards` 內加 timer / sleep 會：
- 改 legacy ScreenChange watcher callback timing 行為（不期望的 side effect）
- `applyProbeGuards` 目前無 ctx 參數（pure guard logic），加入 ctx-aware hold 改 signature
- 與 W3 撤回後保留 mechanical extraction（per W6-3 §5.4）的設計初衷衝突

放在 `consumeSignals`：
- 純 ProbeIntent 路徑，不影響 legacy ScreenChange
- 已有 ctx（detector lifecycle ctx）
- pre-hold 是 dispatcher-level 行為，與 detector emit / dispatcher 5-case lifecycle 對齊

### 0.5 N=300ms 選擇依據

**race 起點**不在 `recordHookAt` 內部（μs 級 mu lock + map write），而是「user 按鍵後 `pdx hook` CLI 還沒進 daemon」這段。

**timing 數據**（per `docs/specs/2026-04-30-daemon-hook-pipeline-lag-analysis.md` §2.5）：

| Metric | Value |
|---|---|
| `pdx hook` CLI cold start | **80-250ms** 額外 tax 在 daemon 之前 |
| HTTP request loopback | 1-10ms |
| handler decode + recordHookAt | μs 級 |
| post-fastpath broadcast pipeline (alpha.276) | ≤1s typical, p95=69ms |
| `watchPollInterval` | 500ms |
| `IdleStableTicks` default | 3 (= 1.5s) |

**N 候選評估**：

| N | 對 cold start (80-250ms) 餘裕 | approve case latency 影響 | 結論 |
|---|---|---|---|
| 100ms | < 1x（cold start 就吃光）| 不可察 | **太窄**，cold start tail 罕見但會破 |
| 200ms | ~1x | 邊緣可接受 | **偏緊**，無 safety margin |
| **300ms** | ~1.2x cold start tail | 300ms 仍小於 Phase A 1.5s + 500ms tick 主延遲 | **採納** |
| 500ms | 2x | 等於 prober tick，user 觀感邊界 | 過長 |

**動態 tuning 不採納**（codex 評估）：adaptive histogram 會讓 lifecycle 行為隨 runtime 分布漂移，違反「dispatcher 行為穩定可預期」原則。

**mlab live capture 為 PR 證據而非阻塞前置**：30-50 次 reject capture 量測 `signal_received → recordHookAt` p99；若 < 150ms 才考慮未來降到 200ms（issue 追，不在本 PR 內）。

### 0.6 與 fix-spec §3 的對齊（generic 不特化）

| fix-spec §3 約束 | J3 落地 |
|---|---|
| ❌ 不為單一 Kind 特化 | ✅ pre-grace 對 W6-3 ProcessDead / W6-6 ScreenChange / 未來 ProbeIntent Kind generic 適用 |
| ❌ 不偽裝為 hook event | ✅ pre-grace 是 dispatcher-level signal hold，hook 與 probe 仍兩條獨立 channel |
| ❌ 不破壞 hook authority | ✅ hook 仍是 status authoritative source；pre-grace 只是「probe 等 hook」單向妥協 |
| ✅ 沿用 5-case lifecycle / 4-step guard | ✅ 不動 `applyIntentLifecycle` / `applyProbeGuards`；pre-grace 在 consumeSignals 入口處掛勾 |

---

## 1. 範圍與目標

### 1.1 In-scope

1. **新增 const** `probeIntentPreGraceWindow = 300 * time.Millisecond` 於 `probe_intent_dispatcher.go`
2. **`consumeSignals` 改造**：
   - 啟用 ctx 參數（目前是 `_ context.Context`）
   - 收到 signal 後記 `signalAt = orchNowFn()`
   - 用 `time.NewTimer` + `select { case <-timer.C: case <-ctx.Done(): }` 實作 hold
   - timer 期滿後 read `probeOrch.lastHookAt[session]`：若 timestamp 在 `signalAt` 之後 → drop pre-grace；否則進 `applyProbeGuards`
   - ctx cancel during hold：drop pre-cancel metric
3. **新 metrics**（`internal/agent/metrics.go`）：
   - `purdex_probe_intent_pre_grace_held_total` — hold 開始計數
   - `purdex_probe_intent_dropped_pre_grace_total` — pre-grace drop 計數
   - `purdex_probe_intent_pre_grace_canceled_total` — ctx cancel during hold 計數
4. **`probeIntentOnDropForSession` reason 擴展**：加 `"pre-grace"` / `"pre-grace-canceled"` reason mapping
5. **W6-3/W6-6 spec drift anchor**：在兩個 spec 的 §8 spec drift signals 加條目「不可單一 Kind 特化 pre-grace timer」
6. **既有測試重驗**：W6-3 ProcessDead lifecycle test 全綠（含 `!appliedAny` post-loop rearm regression）；W6-6 v5 spec reject race 部分後續更新（非本 PR scope，J3 ship 後 W6-6 PR 處理）
7. **既有 metric 不改名**：`MetricProbeGraceWindowSuppressed` 維持 post graceWindow 計數，dashboard 不破

### 1.2 Out-of-scope（明列防 scope creep）

- ❌ **動 `applyProbeGuards`**（不改 signature、不加 timer/sleep、不改 4-step 順序、不改回傳格式、不加 test-only seam；保 mechanical extraction 設計）
- ❌ **動 legacy ScreenChange watcher path**（W3 撤回後雖無 production caller 但 test 仍依賴；測試行為不變）
- ❌ **動 hook entry / `recordHookAt`**（hook authority 不變；handler 順序保 codex finding #3 規範）
- ❌ **動 ProbeIntent interface 形狀**（W6-3 finalize 已 lock：Kind / Signal / OnEntryStatus / OnSignal）
- ❌ **動 `probeGraceWindow=2s` 既有值**（post-direction 行為不變；只新增 pre-direction）
- ❌ **per-session hook → timer notify channel**（codex 第一版不建議；等滿 N 看 timestamp 已足夠）
- ❌ **adaptive / histogram pre-grace tuning**（lifecycle 行為穩定性優先；本 PR 不引入 runtime tuning）
- ❌ **更新 W6-6 v5 spec reject race 段落**（本 PR ship 後再回 W6-6 worktree update spec → 派 round 6）
- ❌ **mlab live capture hook latency 校準降低 N=200ms**（PR 證據走 mlab 4 路徑驗 PASS 即可；降值動作排 followup issue）

### 1.3 Acceptance criteria

| 編號 | 條件 |
|---|---|
| A1 | probe Signal 進 dispatcher → hold 300ms → 期間無 hook → 進 `applyProbeGuards` → 既有 4-step guard 行為不變 |
| A2 | probe Signal 進 dispatcher → hold 300ms → 期間同 session hook 到 → drop pre-grace；status 不被 probe 翻；`MetricProbeIntentDroppedPreGrace` +1 |
| A3 | probe Signal 進 dispatcher → hold 300ms → ctx cancel during hold → drop pre-cancel；signal 不進 apply；`MetricProbeIntentPreGraceCanceled` +1 |
| A4 | probe Signal 進 dispatcher → hold 300ms 過 → hook 在 hold 過後到並且**在 `applyProbeGuards` step 2 read `lastHookAt` 之前 `recordHookAt` 完成** → step 2 既有 post graceWindow drop（與本 PR 前行為一致）。**注意**：hook 在 hold 過後到但 `recordHookAt` 與 step 2 read race 失敗的 boundary case 走 R13 處置（boundary race acceptable as known limitation）|
| A5 | W6-3 ProcessDead `!appliedAny` post-loop rearm 路徑：pre-grace drop 後 detector exit → consumeSignals 退迴 case → teardown + rearm + 新 generation detector arm 仍正確（regression test）|
| A6 | cross-provider switch during hold：reconcileSessionActive 取消 ProbeIntent entry → ctx cancel → drop pre-cancel；不殘留 active entry |
| A7 | replay path（daemon restart 後 `replayStatus` 觸發 ProbeIntent re-arm）：pre-grace 行為一致；ProcessDead detector 在 daemon restart 後死 process 多 +300ms 檢測延遲（仍 ≤2s W6-3 目標內）|
| A8 | metric 拆分清楚：`MetricProbeGraceWindowSuppressed`（既有 post）不變；新增三個 pre metric 命名與行為對齊 §1.1 #3 |
| A9 | mlab live verify §1（**quantified boundary race threshold**）：codex permission ask → user 按 [2] 拒絕 → 30 次重複 reject capture：lights `waiting → idle` 不誤觸 `running` ≥ 28/30（**閃 running 次數 < 3**）；若 ≥ 3 次閃 running 視為 design 不達標 surface（pre-grace + post graceWindow 雙層應 cover ≥93% 場景；boundary race 內漏網應極罕見）|
| A10 | mlab live verify §2：codex permission ask → user 按 [1] 批准 → lights `waiting → running` ≤500ms+300ms（observed latency 可接受）|
| A11 | mlab live verify §3：codex 進程被 SIGKILL → ProcessDead detector 抓到死 → status=error；observed latency 含 1Hz poll + 300ms pre-grace ≤2s |
| A12 | mlab live verify §4：daemon restart during waiting status → replayStatus → ProbeIntent re-arm；後續 hook / probe 行為與 J3 預期對齊 |

---

## 2. 設計約束

### 2.1 必須

- pre-grace hold 放在 `consumeSignals` 內，不放在 `applyProbeGuards`（per §0.4）
- `consumeSignals` signature 啟用 ctx 參數（既有 `_ context.Context` 改為 `ctx context.Context` 並使用）
- pre-grace timer 用 `time.NewTimer` + `select case <-timer.C: case <-ctx.Done():`；ctx 贏 → drop pre-cancel
- pre-grace decision 邏輯在 hold 結束後 lookup `probeOrch.lastHookAt[session]`（read-only，不 mutate state），與 `signalAt` 比較
- 用本地 const `probeIntentPreGraceWindow = 300 * time.Millisecond` 定義於 `probe_intent_dispatcher.go`，**不**從 `probe_orchestrator.go` import（語意分離：post grace 是 hook authority 後壓制；pre grace 是 dispatcher-level race 緩衝）
- 新 metric 命名前綴 `purdex_probe_intent_*`（與既有 `purdex_probe_intent_signal_emitted_total` 一致），不混用 `purdex_probe_*` 既有 ScreenChange watcher metric
- `probeIntentOnDropForSession` reason mapping 加 `"pre-grace"`；既有 reasons (`"stale-callback"` / `"grace"` / `"mapping"` / `"transition-gate"` / `"error-guard"`) 不動
- 改動範圍對 W6-3 ProcessDead 既有行為：emit latency +300ms（hold 期間 + 仍進 apply）；其餘 lifecycle 不變
- W6-3 dispatcher integration test 必須補：(a) pre-grace hook race drop / (b) pre-grace ctx cancel / (c) `!appliedAny` post-loop rearm 在 pre-grace drop 後仍 work

### 2.2 不可

- ❌ 不在 `applyProbeGuards` 內加 timer / sleep / ctx 參數（保 mechanical extraction，per W6-3 §5.4）
- ❌ 不為單一 Kind（如 ScreenChange）特化 pre-grace 行為（fix-spec §3）；W6-3 ProcessDead 與 W6-6 ScreenChange 通用 hold N ms
- ❌ 不引入 hook → timer notify channel 立刻 cancel hold（第一版簡化；等滿 300ms 看 timestamp 已正確；future enhancement 列 open question）
- ❌ 不引入 adaptive / histogram tuning（lifecycle 穩定性優先；N 是 const，未來調 const 走獨立 PR）
- ❌ 不影響 hook authority（hook 仍 set `currentStatus` directly via handler；pre-grace 只壓 probe Signal mapping）
- ❌ 不改 `probeGraceWindow=2s` 既有 post-direction 值（既有測試與 dashboard 仰賴）
- ❌ 不改 `MetricProbeGraceWindowSuppressed` 命名 / 語意（dashboard 不破）
- ❌ 不在 detector 端加 pre-grace 邏輯（detector 仍是 dumb emit；lifecycle 由 dispatcher 統一管）
- ❌ 不擴 ProbeIntent interface（Kind / Signal / OnEntryStatus / OnSignal 不動）
- ❌ 不動 hook entry handler.go:380-413 順序（保 codex finding #3 regression 防護：recordHookAt MUST run BEFORE currentStatus mutation）

### 2.3 既知 race / edge case

| ID | 場景 | 處置 |
|---|---|---|
| R1 | ctx cancel during hold 後舊 signal 又 apply | hold select 監聽 ctx.Done()；ctx 贏 → drop pre-cancel；不進 `applyProbeGuards`；`MetricProbeIntentPreGraceCanceled` +1 |
| R2 | hook during hold 但 active entry 已被 reconcile teardown（race window）| pre check 先看 lastHookAt > signalAt → drop pre-grace；既有 stale-callback guard 不會被觸發；reason `pre-grace` 與 `stale-callback` 拆分清楚 |
| R3 | hook 在 hold 期間 + active entry 仍存在 + same generation | 標準 case；drop pre-grace；status 由 hook authoritative set；`MetricProbeIntentDroppedPreGrace` +1 |
| R4 | hook 在 hold 過後到（hold 結束才 hook 進 daemon）+ `recordHookAt` 在 `applyProbeGuards` step 2 read `lastHookAt` 之前完成 | pre-grace 不 drop → 進 step 2 → 看到新 `lastHookAt` → post graceWindow drop（與 J3 前行為一致；standard case）|
| R5 | `!appliedAny` post-loop rearm 被 pre-grace drop 影響 | consumeSignals 既有「detector exited but no signal applied → teardown + rearm」邏輯（line 588-615）必須對 pre-grace drop case 一致 work；regression test 補 |
| R6 | replay path daemon restart：dead process 檢測延遲多 +300ms | W6-3 spec ≤2s 目標下仍可（1Hz poll + 300ms hold = ~1.3s）；replay test 預期值更新 |
| R7 | cross-provider switch during hold | reconcileSessionActive 取消 entry → ctx cancel → drop pre-cancel；既有 generation + stale check 仍 work；補 cross-provider test |
| R8 | 多 signal 排隊每個都 hold N | channel buffer 1（W6-3 設計），detector 多為 one-shot（emit 後 detector exit），實際排隊極少；如未來 multi-signal Kind 出現再評估（spec drift signal 留 anchor）|
| R9 | hook 在 hold 結束 0-1ms 內到（boundary race）| timestamp 比較：lastHookAt > signalAt → drop pre-grace。1ms 內仍能正確判定（mu lock + map write 是 monotonic clock）|
| R10 | daemon restart 期間 in-flight signal | consumer goroutine teardown 走 ctx cancel；in-flight signal 最多多活 N（300ms）；bounded |
| R11 | 同 session 多 hook 連續到（rapid hook 序列）| 第一個 hook 設 lastHookAt → 後續 probe Signal 進 hold → drop pre-grace（lastHookAt > signalAt）；多 hook 不影響 pre-grace 邏輯 |
| R12 | timestamp 解析度 boundary（同 nanosecond 內 hook + signal）| `orchNowFn` 用 `time.Now`（typically ns 解析）；race 條件下 hook 與 signal 同 ns 機率極低；若同 ns，定義 `lastHookAt > signalAt`（嚴格 greater）→ 同 ns 不 drop（probe 走 apply）；極端 race 不影響 acceptance |
| **R13** | **boundary race**（codex round 1 finding）：hook 在 hold 過後到（probe Signal 已進 `applyProbeGuards`）但 `recordHookAt` 完成的時間點 vs step 2 read `lastHookAt` 的時間點 race 失敗 — step 2 read 仍看舊值 → 不 drop → step 4 mutate currentStatus = running → hook 後到才 set idle → lights 短暫閃 `waiting → running → idle` | **acceptable as known limitation**；雙重保險（pre-grace 300ms + post graceWindow 2s）已 cover ≥93% 場景；漏網需「hook 進 daemon 在 hold 過後 + `recordHookAt` 與 step 2 read μs window race」兩個獨立罕見事件交集。**Fail-fast 處置**：mlab live verify A9 quantified threshold（30 次 reject 閃 < 3）作為 production 漏網率測試；A9 PASS 即代表 R13 確實 acceptable，本 PR ship；A9 fail（≥ 3 次閃）開 followup issue，加 per-event trace log + cross-log correlation 定位漏網類別（pre-grace miss / post graceWindow miss / `recordHookAt` 與 step 2 read μs race / 其他），透過 trace 數據真正解 R13 或調 N。**現階段不引入 trace 為 ship gate**（與 R13 known limitation 自相矛盾且為過早工程）|

---

## 3. 實作設計

### 3.1 `consumeSignals` 改造

**現狀**（`probe_intent_dispatcher.go:515` 大致結構）：

```go
func (d *probeIntentDispatcher) consumeSignals(_ context.Context, ...) {
    for signal := range signals {
        applied, appliedStatus := applyProbeGuards(d.parent, probeGuardArgs{...})
        // ... lifecycle 5-case + teardown + rearm
    }
}
```

**改造後**：

```go
func (d *probeIntentDispatcher) consumeSignals(ctx context.Context, ...) {
    for sig := range in {
        // 既有 signal-emitted metric 必須先 +1（每收到 detector signal）
        agentpkg.MetricProbeIntentSignalEmitted.Add(1)

        signalAt := orchNowFn()
        agentpkg.MetricProbeIntentPreGraceHeld.Add(1)

        // 既有 package-level helper：closure factory，吞 reason，內含 dev log + 4 既有 reason
        // counter（pre-grace 兩條新 reason 不在 switch 內 → 無 counter，避免與 caller 顯式
        // .Add(1) double-count；per §3.4 R4 修）
        onDrop := probeIntentOnDropForSession(session, intent.Kind)

        timer := time.NewTimer(probeIntentPreGraceWindow)
        select {
        case <-timer.C:
            // proceed to hook check
        case <-ctx.Done():
            timer.Stop()
            agentpkg.MetricProbeIntentPreGraceCanceled.Add(1)
            onDrop("pre-grace-canceled")
            continue
        }

        // pre-grace decision: did a hook arrive during hold?
        if d.parent.probeOrch != nil {
            o := d.parent.probeOrch
            o.graceMu.Lock()
            last, hasHook := o.lastHookAt[session]
            o.graceMu.Unlock()
            if hasHook && last.After(signalAt) {
                agentpkg.MetricProbeIntentDroppedPreGrace.Add(1)
                onDrop("pre-grace")
                continue
            }
        }

        applied, appliedStatus := applyProbeGuards(d.parent, probeGuardArgs{
            // ...
            OnDrop: onDrop,  // 沿用同一 closure；既有 callsite 不變
        })
        // ... 既有 lifecycle 5-case + teardown + rearm 完全不變
    }
}
```

**關鍵點**：

- ctx 參數啟用，hold select 監聽 ctx.Done
- **既有 `MetricProbeIntentSignalEmitted` 必 preserve**（每收到 signal +1）— pre-grace drop / cancel 路徑也要 `MetricProbeIntentSignalEmitted +1`，否則違反「既有 metric 不變」承諾並讓 dashboard 漏數
- **`probeIntentOnDropForSession` 是既有 package-level closure factory**（簽名 `(session, kind) func(reason)`），不是 Module field；pre-grace 兩條新 reason `"pre-grace"` / `"pre-grace-canceled"` 不在既有 switch case 內 → 不增 counter（caller 顯式 `.Add(1)` 統一管，避免 double-count，per §3.4 R4 修）；helper 內 dev log 仍會印出新 reason（log 不需修）
- pre-grace decision 在 timer 過期後 lookup `lastHookAt`；不在 hold 期間 polling（簡化第一版）
- pre-grace drop / pre-grace-canceled 兩條 path 各自獨立 metric `.Add(1)` + 同一 onDrop closure
- 進入 `applyProbeGuards` 後既有 4-step guard 不動（含既有 post graceWindow）；`OnDrop` 沿用同一 closure
- `!appliedAny` post-loop rearm 在 pre-grace drop case：drop continue 後 detector 仍可能 exit 觸發 outer teardown — 既有 line 588-615 邏輯仍適用，但要驗 regression

### 3.2 `probeIntentPreGraceWindow` const

```go
// internal/module/agent/probe_intent_dispatcher.go (top of file with other consts)

// probeIntentPreGraceWindow is the pre-applyProbeGuards hold duration. While
// active, a probe Signal waits to see whether a hook for the same session
// arrives at the daemon. If a hook arrives during the window, the probe Signal
// is dropped (hook authority pre-emption). If no hook arrives, the Signal
// proceeds into applyProbeGuards (which still has its own post-direction 2s
// graceWindow as a backstop).
//
// Sized to cover the `pdx hook` CLI cold start (80-250ms typical, per
// daemon-hook-pipeline-lag-analysis §2.5) plus a small safety margin. 300ms
// is small enough that approve-case ScreenChange Signal latency is bounded by
// the 1.5s Phase A IdleStableTicks gate + 500ms watchPollInterval, not this
// window. Future tuning (e.g. lower to 200ms after mlab live capture shows
// hook p99 < 150ms) goes through a separate PR.
const probeIntentPreGraceWindow = 300 * time.Millisecond
```

### 3.3 metrics 新增

`internal/agent/metrics.go`:

```go
// MetricProbeIntentPreGraceHeld counts probe Signal entries into the
// dispatcher's pre-applyProbeGuards hold window. Held +1 per Signal, before
// the hold timer fires.
var MetricProbeIntentPreGraceHeld = expvar.NewInt("purdex_probe_intent_pre_grace_held_total")

// MetricProbeIntentDroppedPreGrace counts probe Signals dropped because a
// hook for the same session arrived during the pre-applyProbeGuards hold
// window. Hook authority pre-emption.
var MetricProbeIntentDroppedPreGrace = expvar.NewInt("purdex_probe_intent_dropped_pre_grace_total")

// MetricProbeIntentPreGraceCanceled counts probe Signals dropped because the
// detector ctx was canceled during the pre-applyProbeGuards hold window
// (e.g. cross-provider switch, daemon shutdown).
var MetricProbeIntentPreGraceCanceled = expvar.NewInt("purdex_probe_intent_pre_grace_canceled_total")
```

**既有 metric 不改名**：
- `MetricProbeIntentSignalEmitted` — 維持「consumeSignals 已觀察到 signal」語意（held 同步 +1）
- `MetricProbeIntentDroppedGrace` — 維持 post graceWindow drop 計數
- `MetricProbeGraceWindowSuppressed` — 維持 legacy/global post counter（dashboard 仰賴）

### 3.4 `probeIntentOnDropForSession` reason 擴展（log 註解，不擴 switch）

既有 helper 是 package-level closure factory（`probe_intent_dispatcher.go:42`）：

```go
func probeIntentOnDropForSession(session string, kind agentpkg.ProbeIntentKind) func(string) {
    return func(reason string) {
        switch reason {
        case "stale-callback": agentpkg.MetricProbeIntentDroppedStale.Add(1)
        case "grace":          agentpkg.MetricProbeIntentDroppedGrace.Add(1)
        case "error-guard":    agentpkg.MetricProbeIntentDroppedErrorGuard.Add(1)
        case "transition-gate":agentpkg.MetricProbeIntentDroppedTransitionGate.Add(1)
        }
        if isDevMode() {
            log.Printf("[probe-intent] drop session=%s kind=%s reason=%s", session, kind, reason)
        }
    }
}
```

**新增兩條 reason 不擴 switch case**（**callback 本身不計 expvar metric — metric 計數責任在 caller (`consumeSignals`) 內 explicit `.Add(1)` 統一管，避免 double-counting**；callback 僅供 dev-mode log 觀察 drop 行為，switch 不命中即不加 counter）：
- `"pre-grace"` — pre-applyProbeGuards hold 期間 hook 到 → drop（caller `MetricProbeIntentDroppedPreGrace.Add(1)`，callback 落 dev log）
- `"pre-grace-canceled"` — pre-applyProbeGuards hold 期間 ctx cancel → drop（caller `MetricProbeIntentPreGraceCanceled.Add(1)`，callback 落 dev log）

**reason mapping 註解擴展**（GoDoc 在 `probe_intent_dispatcher.go` top；本 PR 唯一改 helper 周邊的點是註解）：

```go
// reason values are stable strings emitted by applyProbeGuards + consumeSignals:
//
//   stale-callback        — applyProbeGuards step 1 stale check failed (counter: helper)
//   grace                 — applyProbeGuards step 2 post graceWindow active (counter: helper)
//   transition-gate       — applyProbeGuards step 4 transition gate rejected (counter: helper)
//   error-guard           — applyProbeGuards step 4 error guard rejected (counter: helper)
//   pre-grace             — consumeSignals pre-applyProbeGuards hold: hook arrived (J3; counter: caller)
//   pre-grace-canceled    — consumeSignals pre-applyProbeGuards hold: ctx canceled (J3; counter: caller)
//
// Note: applyProbeGuards step 3 "Mapping returned empty" path returns false
// without invoking OnDrop（probe_orchestrator.go:302-308）— there is no
// "mapping" reason emitted; this is an existing behavior, out of J3 scope.
```

---

## 4. Phase 拆分（給 plan 用）

### 4.1 P1 — dispatcher pre-hold + ctx + metrics

| Task | 檔案 | 內容 |
|---|---|---|
| P1-T1 | `internal/agent/metrics.go` | 加 `MetricProbeIntentPreGraceHeld` / `MetricProbeIntentDroppedPreGrace` / `MetricProbeIntentPreGraceCanceled` 三 expvar Int |
| P1-T2 | `internal/module/agent/probe_intent_dispatcher.go` | 加 `probeIntentPreGraceWindow = 300ms` const + GoDoc；reason 註解擴展 `pre-grace` / `pre-grace-canceled` |
| P1-T3 | `internal/module/agent/probe_intent_dispatcher.go` | `consumeSignals(ctx, ...)` ctx 參數啟用；pre-grace timer + select 監聽 ctx；hook check after timer；pre-grace drop / pre-grace-canceled 兩 path metric +1 + onDrop reason mapping |
| P1-T4 | `internal/module/agent/probe_intent_dispatcher_test.go` | 表驅動 tests：(1) hold 過後無 hook → 進 apply ✓ / (2) hold 期間 hook → drop pre-grace + metric +1 + reason / (3) hold 期間 ctx cancel → drop pre-cancel + metric +1 + reason / (4) **hook 在 hold 期間到（早於 hold 結束）→ drop pre-grace**（透過 caller 設 `lastHookAt` 時序模擬）/ (5) **hook 在 hold 過後到、進入 `applyProbeGuards` 後在 step 2 read `lastHookAt` 之前 `recordHookAt` 完成 → 走 post graceWindow drop**（沿用既有 `applyProbeGuards` unit test pattern：直接設 `lastHookAt` 於 signalAt 之後，呼叫 `applyProbeGuards` 驗 step 2 graceWindow drop；不引入 production seam，不精準模擬微時序）。**Case 6（R13 boundary race：hook 在 step 2 read 之後 `recordHookAt`）不 gating** — 屬 known limitation by R13，靠 mlab A9 quantified threshold 把關；本 PR 不為它新增 unit test 或 production seam |

### 4.2 P2 — test coverage（既有 path 重驗 + regression）

| Task | 檔案 | 內容 |
|---|---|---|
| P2-T1 | `internal/module/agent/probe_intent_dispatcher_test.go` | W6-3 ProcessDead 既有 lifecycle 5-case test 全跑：含 +300ms latency 預期更新；含 `!appliedAny` post-loop rearm 在 pre-grace drop case 仍正確 |
| P2-T2 | `internal/module/agent/probe_intent_dispatcher_codex_wire_test.go` | ProcessDead wire test 重驗：fake tmux pane lister 注入 + hold +300ms timing 修正 |
| P2-T3 | `internal/module/agent/probe_intent_dispatcher_test.go` | 加 cross-provider switch during hold test：reconcileSessionActive cancel ctx → pre-grace-canceled metric |
| P2-T4 | `internal/module/agent/probe_intent_dispatcher_test.go` | 加 replay path test：daemon restart → replayStatus → re-arm → pre-grace 行為與 fresh arm 一致 |
| P2-T5 | `internal/module/agent/probe_orchestrator_apply_guards_test.go` | 既有 OR3/OR4/OR5/FX1-FX5 全綠（applyProbeGuards 行為不變）|
| P2-T6 | observability tests | `purdex_probe_intent_pre_grace_*` 三 metric increment 觀察；既有 `MetricProbeGraceWindowSuppressed` 不變 |

### 4.3 P3 — mlab live verify + W6-3/W6-6 spec anchor

| Task | 檔案 | 內容 |
|---|---|---|
| P3-T1 | mlab live verify | §1 reject 30 次 capture：lights `waiting → idle` ≥ 28/30（**閃 running < 3 = 90% PASS rate**，per A9 quantified threshold）；若 ≥ 3 次閃 surface evaluate（per R13 fail-fast handling — 開 followup issue 加 per-event trace 定位漏網類別）/ §2 approve：lights `waiting → running` ≤1s observed / §3 SIGKILL：ProcessDead detector ≤2s / §4 daemon replay 一致；驗證手段：grep `pre-grace` / `pre-grace-canceled` drop reason 計數 + expvar 三 pre-grace metric increment 觀察 |
| P3-T2 | `docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md` §8 | 加 spec drift signal anchor：「不可在單一 Kind 特化 pre-grace timer；不可 detector 端自行加 pre-hold」 |
| P3-T3 | `docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md` §8 | 同 P3-T2 anchor；W6-6 reject race 段落 update 排在 W6-6 後續 PR（J3 ship 後）|
| P3-T4 | `docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-spec.md` | 本 spec 文件含 mlab 結果填回 §1.3 A9-A12 |

---

## 5. 驗收條件（重述 §1.3 + 工程約束）

- A1-A8 unit + integration test 全綠
- A9-A12 mlab live verify 全 PASS（§1 reject 30 次 ≥ 28/30 per A9 quantified threshold / §2 approve / §3 SIGKILL / §4 replay）
- `go test ./internal/module/agent -count=1` 全綠
- `go test -race ./internal/module/agent ./internal/agent/codex -count=1` 全綠
- `go test ./...` 全 packages 綠
- vet / lint clean
- W6-3 既有 11 finding 收斂行為不變（regression suite）
- W6-6 v5 spec round 5 reject race finding 在 J3 ship 後可由 W6-6 後續 PR update spec 解決（不在本 PR scope）

---

## 6. Spec Drift Signals（給 codex review 與 plan 留 anchor）

下列任一徵兆出現，停修 + surface：

| Signal | 為什麼是 drift |
|---|---|
| 想把 pre-grace timer 放進 `applyProbeGuards` | shared guard pipeline 同時服務 legacy ScreenChange watcher path；改 callback timing 是不期望的 side effect；W6-3 §5.4 mechanical extraction 要求保留 |
| 想為單一 Kind 特化 pre-grace（如只對 ScreenChange，ProcessDead 不 hold）| fix-spec §3 不為單一 Kind 特化；W6-3 ProcessDead 也有同類 race；generic pre-hold 對所有 Kind 一致 |
| 想引入 hook → timer notify channel 立刻 cancel hold | 第一版過度工程；等滿 N 看 timestamp 已正確；hook 本身獨立 broadcast，延後 drop hidden signal 不影響 user 觀感 |
| 想 adaptive / histogram 動態 tuning N | lifecycle 行為穩定可預期優先；adaptive 讓 dispatcher 行為隨 runtime 分布漂移 |
| 想改 `probeGraceWindow=2s` post-direction 值 | 既有測試與 dashboard 仰賴；本 PR scope 只新增 pre，不動 post |
| 想改 `MetricProbeGraceWindowSuppressed` 命名 | dashboard 仰賴；新增 pre metric 用獨立命名前綴 |
| 想在 detector 端加 pre-grace 邏輯 | detector 仍是 dumb emit；lifecycle 由 dispatcher 統一管；detector 端加 pre-hold 違反 single-source-of-truth |
| 想擴 ProbeIntent interface 加 pre-grace 相關欄位 | W6-3 finalize 已 lock；pre-grace 是 dispatcher-level 行為，不通過 interface |
| 想改 hook entry handler.go:380-413 順序 | codex finding #3 regression 防護：recordHookAt MUST run BEFORE currentStatus mutation；本 PR 不動 |
| 想跳過 P2-T1 ProcessDead `!appliedAny` rearm regression test | high risk corner case（codex 評估）；pre-grace drop 後 detector exit + rearm 路徑必須驗 |
| 想 claim「J3 完全消除 reject race」 | spec round 1 codex finding 已抓到 — boundary race 仍存在（hook 在 hold 過後到 + `recordHookAt` 與 step 2 read μs window race）；J3 雙重保險 cover ≥93% 場景，剩 < 7% 走 R13 acceptable as known limitation；**spec wording 必須誠實**（A4 / R13 / A9 quantified threshold 三處錨定）|
| 想在本 PR 加回 per-event trace log / cross-log correlation / 雙 test seam | v7 trim 已 fail-fast 處置：mlab A9 PASS = R13 確實 acceptable（不需 trace），A9 fail = 才開 followup issue 加 trace 定位漏網類別；同 PR ship trace 與 R13 known limitation 自相矛盾且為過早工程（codex consulting `task-mon19xir-vcknzb` 判定 R3-R5 review-driven scope creep）|

---

## 7. 不在 scope（明列防 scope creep）

- ❌ 動 W6-6 v5 spec reject race 段落（J3 ship 後 W6-6 後續 PR 處理）
- ❌ 動 W6-3 ProcessDead detector / cc / opencode 各家 detector 實作
- ❌ 動 hook entry handler.go / `recordHookAt` / lights-rebuild fix-spec §3 約束
- ❌ 動 SPA 端（status 切換 lights race 是 daemon 端內部事；SPA 已認 hook authority）
- ❌ 加新 ProbeIntent Kind（W6-6 ScreenChange 在另一 PR 落地）
- ❌ mlab capture 數據後降低 N=200ms（排 followup issue；本 PR 定 300ms）
- ❌ adaptive tuning / observability dashboard 改造

---

## 8. 文獻

- W6-3 spec：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md`（interface finalize / dispatcher / 11 finding 收斂）
- W6-3 plan：`docs/specs/2026-04-29-w6-3-codex-error-probe-intent-plan.md`（5-case lifecycle / 4-step guards）
- W6-6 v5 spec：`docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md`（reject race finding 來源）
- Fix-spec：`docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §3（不為單一 Kind 特化約束）
- Daemon hook pipeline lag analysis：`docs/specs/2026-04-30-daemon-hook-pipeline-lag-analysis.md` §2.5（`pdx hook` CLI cold start 80-250ms 數據）
- Daemon perf fastpath（alpha.276 ship）：kickoff `kickoff_daemon_perf_fastpath.md`（hook entry → broadcast pipeline ≤1s p95=69ms）
- ProbeIntent dispatcher：`internal/module/agent/probe_intent_dispatcher.go:515` `consumeSignals`
- applyProbeGuards：`internal/module/agent/probe_orchestrator.go:265`（4-step pipeline）
- recordHookAt：`internal/module/agent/probe_orchestrator.go:173` / `handler.go:401` hook entry
- expvar metrics：`internal/agent/metrics.go`
- Codex evaluation job：`task-momrowao-216mcq`（4m 18s, effort=high, model=spark；評估 J3 雙向擴展設計 + N 秒數）

---

## 9. Open questions

> Round 1 codex review (job `review-momyypsf-bd2hlw`) 已採納：A4 boundary race claim 過度樂觀 → §1.3 A4 改寫精確、§1.3 加 A9 quantified threshold (30 次 reject 閃 < 3)、§2.3 加 R13 boundary race acceptable as known limitation。Round 2-5 standard review 採納修到 v6；codex consulting `task-mon19xir-vcknzb` 判定 R3-R5 review-driven scope creep → v7 trim 改 fail-fast：A9 PASS = R13 確實 acceptable / A9 fail = followup issue 加 per-event trace 定位漏網類別。下列為 spec v7 後仍 open 的 questions（給後續 codex round 6 review）。

1. **N=300ms vs mlab 校準**：本 PR 直接定 300ms const ship；mlab capture 為 PR 證據。後續 30-50 次 reject capture 若 `signal_received → recordHookAt` p99 < 150ms，是否考慮獨立 PR 降 N=200ms？建議走 followup issue + 累積足夠 data points 後再決定，避免 const 頻繁變動破壞 lifecycle 預期。
2. **hook → timer notify channel 第一版不做**：等滿 300ms 看 timestamp vs hook 立刻 cancel hold，user 觀感差別在 worst case probe 多 holding 0-300ms（hidden signal 永不 emit）。第一版簡化 OK；future enhancement 評估點 — 若觀測到大量 pre-grace drop hold 滿 timer 才釋放，notify channel 可降 hold tail latency。
3. **pre-grace metric 是否要 per-Kind 拆**：目前 generic 計數；future enhancement 可加 label（W6-3 ProcessDead vs W6-6 ScreenChange vs 未來 Kind）。第一版簡化 OK。
4. **`!appliedAny` post-loop rearm 對 pre-grace drop 是否需要特殊處理**：codex evaluation 標 high risk；第一版假設既有 line 588-615 邏輯對 pre-grace drop case 一致 work（`appliedAny=false` 走 teardown + rearm），P2-T1 regression test 驗證。若 test 抓到不一致行為，spec 補設計擴展。
5. **timestamp boundary case `lastHookAt == signalAt`**：用 `last.After(signalAt)`（嚴格 greater）→ 同 ns 不 drop（probe 走 apply）。極端 race 機率忽略不計；若 codex review 提出 boundary 問題再評估改 `>=`。
6. **R13 boundary race threshold（A9 mlab quantified）**：30 次 reject 閃 running 次數 ≥ 3 視為 design 不達標的依據是「pre-grace 300ms cover hook cold start 80-250ms typical p95」推算 ~93%（27/30）下界。實際 hook cold start 分布若 p99 顯著落在 250-400ms 區段，30/30 0 閃可能不可行；threshold 是否該調整？建議 mlab capture 同時量 hook cold start tail 分布，threshold 隨數據動態，但**本 PR 仍以 < 3 為 ship gate**，不達標 surface 評估 N 加大或補 followup spec。

---

## 10. PR scope 估算

| 項目 | 範圍 |
|---|---|
| 改 production files | 2 (`probe_intent_dispatcher.go` + `metrics.go`) |
| 改 spec docs | 3 (本 spec + W6-3 spec §8 anchor + W6-6 v5 spec §8 anchor) |
| 新增 unit test | 5 pre-grace P1-T4 cases（hold pass / hook drop / ctx cancel / hook 早於 timer / hook 晚於 timer 走 post graceWindow；後者沿用既有 `applyProbeGuards` unit test pattern）|
| 新增 regression test | 4 ProcessDead `!appliedAny` rearm / cross-provider switch / replay path / metric observability |
| 既有 test 重驗 | OR3/OR4/OR5/FX1-FX5 / W6-3 全 lifecycle test |
| mlab live verify | 4 路徑（reject 30 次 / approve / SIGKILL / daemon replay）|
| codex review | 兩輪（standard + adversarial 三平行）|
| effort 估 | 實作 + 單測 0.5 天；mlab capture 0.5 天；review 兩輪；總 1-2 天（trim 後 scope 縮減；無 trace log + 無 seam）|
| bump target | alpha.281（J3 ship 後 W6-6 v5 spec update + 後續 PR ship 排 alpha.282 起）|
