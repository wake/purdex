# J3 Probe Intent bidirectional graceWindow — Implementation Plan

> **Status**：v3（codex plan review 兩輪採納 — round 1 thread `019de428-def1-7533-80f4-cf3b959f7377`：P1 `probeIntentOnDropForSession` package-level closure factory 不是 Module field / P2 既有 `MetricProbeIntentSignalEmitted` 必 preserve；round 2 thread `019de42e-d05f-7781-84c9-3ab5d899826d`：P2 `captureDrops` helper intercept 不可行 → metric-only reason 斷言、P3 §3.4 `mapping` reason 與實際行為不符 → 移除；spec §3.1 / §3.4 / plan P1-T3 / P1-T4 case 1-5 + helper section 同步修；行為與 trim 範圍不變）
> **依賴 spec**：`docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-spec.md` v7.2（v7 trim + plan-review 兩輪 fix）
> **Worktree**：`.claude/worktrees/probe-intent-bidirectional-grace` / branch `worktree-probe-intent-bidirectional-grace`
> **Base**：`origin/main` @ alpha.280（codex broker P1 `13e91c64` + bump `5d40e2a2`）
> **拆分**：Phase 1（P1 dispatcher pre-hold + ctx + metrics + table-driven test；TDD subagent）→ Phase 2（P2 既有 path 重驗 + regression；TDD subagent）→ Phase 3（P3 mlab live verify + W6-3/W6-6 spec drift anchor；主 session）→ PR

---

## 0. Plan 總覽

### 0.1 範圍重申（per spec §1.1）

J3 雙向 graceWindow 擴展 — 在 `consumeSignals` 進入 `applyProbeGuards` 之前 hold 300ms，期間若同 session hook 進來 → drop pre-grace；無 hook → 進原 guard pipeline（含既有 post graceWindow 2s）。

**核心 in-scope**（spec §1.1 #1-#7）：
- #1：`probeIntentPreGraceWindow = 300ms` const
- #2：`consumeSignals(ctx, ...)` 啟用 ctx + pre-grace timer + select + lookup `lastHookAt`
- #3：3 新 expvar metrics（held / dropped-pre-grace / canceled）
- #4：`probeIntentOnDropForSession` reason 加 `"pre-grace"` / `"pre-grace-canceled"`
- #5：W6-3 / W6-6 spec §8 drift anchor 補「不可單一 Kind 特化 pre-grace timer」
- #6：W6-3 ProcessDead lifecycle test 全綠（含 `!appliedAny` post-loop rearm regression）
- #7：既有 metric 不改名（`MetricProbeGraceWindowSuppressed` 維持）

**Out-of-scope reaffirm**（spec §1.2）：
- 不動 `applyProbeGuards`（不改 signature / 不加 timer / 不加 test-only seam）
- 不動 legacy ScreenChange watcher path
- 不動 hook entry / `recordHookAt` 順序（per codex finding #3 防護）
- 不擴 ProbeIntent interface
- 不動 `probeGraceWindow=2s` post 值
- 不引入 hook → timer notify channel
- 不 adaptive / histogram tuning

### 0.2 估計

- 總 production code：~80-120 行（const + consumeSignals 改造 + 3 metrics）
- 總 test code：~250-350 行（P1-T4 表驅動 5 case + P2-T1~T6 既有 path 重驗 + regression）
- 預估 PR diff：~350-500 行
- 預估時間：4-6 小時 subagent 工作（trim 後 scope 縮減；無 trace log + 無 seam）+ 1-2 小時 mlab live verify

### 0.3 鎖序與不變式（per W6-3 §2.1 持守清單）

實作時必持守：
- 鎖序：`m.mu` only（無 cross-lock）；`probeOrch.graceMu` 只在 `lastHookAt` lookup 期間 hold（read-only）
- ProbeIntent state（`activeProbeIntents`）由 `m.mu` 保護
- detector goroutine 不持 `m.mu`
- ProbeIntent channel buffer = 1（W6-3 既有設計）
- generation token uint64 純遞增
- pre-grace decision 只 read `lastHookAt`，不 mutate state

### 0.4 Trim verification（per spec v7 fail-fast 處置）

R13 boundary race acceptable as known limitation；不為 R13 引入 production seam / dev-mode trace log。Test 涵蓋 P1-T4 5 case 走 deterministic logical assertion（`lastHookAt` timestamp 設定）；R13 case 6 不 unit-gate，由 mlab A9 quantified threshold（30 次 reject 閃 < 3）把關 production 漏網率。

---

## 1. Phase 1：Dispatcher pre-hold + ctx + metrics

### P1-T1 — 三 expvar metrics in `internal/agent/metrics.go`

**目標**：新增 `MetricProbeIntentPreGraceHeld` / `MetricProbeIntentDroppedPreGrace` / `MetricProbeIntentPreGraceCanceled` 三 expvar Int counter，命名前綴 `purdex_probe_intent_*`（per spec §3.3）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/agent/metrics.go` | append 3 個 `expvar.NewInt` + 對應 GoDoc |

**GoDoc 內容**（per spec §3.3）：
- `MetricProbeIntentPreGraceHeld` — counts probe Signal entries into the dispatcher's pre-applyProbeGuards hold window
- `MetricProbeIntentDroppedPreGrace` — counts probe Signals dropped because a hook arrived during the hold window
- `MetricProbeIntentPreGraceCanceled` — counts probe Signals dropped because ctx canceled during hold

**Test**：
- 不需獨立 metric test（單純 expvar 宣告；P1-T4 / P2-T6 透過 dispatcher 行為間接驗 increment）

**Acceptance**：
- `go build ./...` 全綠
- 既有 `MetricProbeGraceWindowSuppressed` / `MetricProbeIntentSignalEmitted` / `MetricProbeIntentDroppedGrace` 不改名不改語意

**估計**：~30 行 production / 0 行 test（間接驗證）

**依賴**：none（最先做）。

---

### P1-T2 — `probeIntentPreGraceWindow` const + reason 註解

**目標**：在 `internal/module/agent/probe_intent_dispatcher.go` 加 `probeIntentPreGraceWindow = 300 * time.Millisecond` const + GoDoc，並擴展 reason mapping 註解（兩條新 reason `pre-grace` / `pre-grace-canceled`）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher.go` | 加 const（top of file with other consts）+ GoDoc（per spec §3.2 完整文字）+ reason mapping 註解擴展（per spec §3.4 7 條 reason 列表） |

**GoDoc 重點**（per spec §3.2）：
- 大小 sized to cover `pdx hook` CLI cold start (80-250ms) + safety margin
- Approve-case latency 由 1.5s Phase A IdleStableTicks + 500ms watchPollInterval 主導，非此窗口
- Future tuning 走獨立 PR

**Test**：
- 不需獨立 const test（P1-T4 透過行為間接驗 300ms hold 行為）

**Acceptance**：
- `go build ./...` 全綠
- 既有 ScreenChange watcher / W6-3 dispatcher 行為不變

**估計**：~25 行 production（const + GoDoc + reason 註解）/ 0 行 test

**依賴**：P1-T1（metrics 名字會在 GoDoc / reason 註解引用）

---

### P1-T3 — `consumeSignals(ctx, ...)` ctx 啟用 + pre-grace timer + hook lookup

**目標**：核心改造 — `consumeSignals` 啟用 ctx 參數、加 pre-grace timer + select 監聽 ctx、timer 過期後 lookup `lastHookAt` 與 `signalAt` 比較、drop pre-grace / drop pre-cancel 兩 path 各自 metric +1 + reason mapping。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher.go` | `consumeSignals` signature 從 `_ context.Context` 改 `ctx context.Context`；in-loop pre-hold（per spec §3.1 完整 code 草稿）；hook check after timer；drop pre-grace metric + onDrop reason；drop pre-cancel metric + onDrop reason；進入既有 `applyProbeGuards` 4-step pipeline 不變 |

**核心程式碼**（spec §3.1 v7.1 完整草稿，搬入即可；plan-review P1/P2 finding 修正後）：

```go
func (d *probeIntentDispatcher) consumeSignals(ctx context.Context, ...) {
    for sig := range in {
        // 既有 signal-emitted metric 必須先 +1（每收到 detector signal）— preserve dashboard / test / obs
        agentpkg.MetricProbeIntentSignalEmitted.Add(1)

        signalAt := orchNowFn()
        agentpkg.MetricProbeIntentPreGraceHeld.Add(1)

        // 既有 package-level closure factory（probe_intent_dispatcher.go:42）
        // 簽名：probeIntentOnDropForSession(session, kind agentpkg.ProbeIntentKind) func(reason string)
        // 注意：不是 Module field！pre-grace 兩條新 reason 不在 helper switch 內 → 無 counter
        // （caller 顯式 .Add(1) 避免 double-count，per spec §3.4 R4）；helper dev log 仍會印
        onDrop := probeIntentOnDropForSession(session, intent.Kind)

        timer := time.NewTimer(probeIntentPreGraceWindow)
        select {
        case <-timer.C:
            // proceed
        case <-ctx.Done():
            timer.Stop()
            agentpkg.MetricProbeIntentPreGraceCanceled.Add(1)
            onDrop("pre-grace-canceled")
            continue
        }

        // pre-grace decision
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
        // ... 既有 lifecycle 5-case + teardown + rearm 完全不動
    }
}
```

**關鍵實作點**：
- **`MetricProbeIntentSignalEmitted` preserve**（pre-grace drop / cancel 路徑也要 +1）— plan-review P2 finding 修
- **`probeIntentOnDropForSession` 是 package-level closure factory**（不是 Module field）— plan-review P1 finding 修；簽名 `(session, kind) func(reason)`
- pre-grace 兩條新 reason `"pre-grace"` / `"pre-grace-canceled"` 不擴 helper switch case → 無 helper-side counter；caller `.Add(1)` 統一管 metric（per spec §3.4 R4）；helper dev log 仍會印新 reason（switch fall-through 後 log 不受影響）
- `signalAt` 用 `orchNowFn()`（既有 inject hook，test 端可 stub）
- timer.Stop() 在 ctx.Done branch 顯式呼叫避免 goroutine leak
- `last.After(signalAt)` 嚴格 greater（per spec §2.3 R12 / Q5；同 ns 不 drop）
- pre-grace lookup 期間 hold `graceMu` read-only（per spec §0.4 → §2.1 必守清單）
- `applyProbeGuards` 既有 callsite 的 `OnDrop: probeIntentOnDropForSession(session, intent.Kind)` 改為 reuse 同一 `onDrop` closure（micro-tidiness；行為等價）
- 既有 `applyIntentLifecycle` / `applyProbeGuards` 後續 5-case + teardown + rearm 邏輯**完全不動**

**Test**：本 task 不寫 test（split 給 P1-T4）；只跑現有 dispatcher test 確認沒被本改造打破（subagent 必跑 `go test -run 'TestConsumeSignals|TestApply' ./internal/module/agent`）。

**Acceptance**：
- `go build ./...` 全綠
- `go vet ./...` clean
- 既有 dispatcher / probe_orchestrator / drift test 全綠（zero regression assertion）
- ctx parameter 啟用後既有 caller 不需要修改（caller 已 pass ctx，只是 dispatcher 之前 ignore）

**估計**：~40 行 production / 0 行 test（新 test 在 P1-T4）

**依賴**：P1-T1（metric 名稱）+ P1-T2（const + reason 註解）

---

### P1-T4 — 表驅動 pre-grace tests in `probe_intent_dispatcher_test.go`

**目標**：5 個表驅動 pre-grace 行為 test，覆蓋 hold pass / hook drop / ctx cancel / boundary。case 5 沿用既有 `applyProbeGuards` unit test pattern；case 6 R13 boundary race 不 gating（per spec §4.1 P1-T4 trim 後）。

**Test cases**（per spec §4.1 P1-T4，trim 後）：

> **共用斷言**（每個 case 必驗）：`MetricProbeIntentSignalEmitted +1`（既有 metric preservation per plan-review P2 finding）+ `MetricProbeIntentPreGraceHeld +1`（per case 進 hold）。下表只列 case-specific 額外 metric / drop reason。

| Case | 場景 | 預期行為 | Case-specific metric / reason |
|---|---|---|---|
| 1 | hold 期間無 hook（無 `lastHookAt` 紀錄）→ timer expire → 進 `applyProbeGuards` → mapping fixture 回穩定值 → applied | applied=true；status 翻轉到 mapping 回值 | `MetricProbeIntentApplied +1`；無 drop reason |
| 2 | hold 期間同 session hook 到（透過 caller 在 inject signal 後 `recordHookAt` 設 `lastHookAt > signalAt`，但 timer 仍未 expire）→ drop pre-grace | applied=false（不進 apply）；status 不被 probe 翻 | `MetricProbeIntentDroppedPreGrace +1`；`onDrop reason="pre-grace"` |
| 3 | hold 期間 ctx cancel → drop pre-cancel | applied=false（不進 apply）；timer.Stop 呼叫 | `MetricProbeIntentPreGraceCanceled +1`；`onDrop reason="pre-grace-canceled"` |
| 4 | hook 在 hold 期間到（早於 timer expire；caller 在 timer-fire 前 inject `lastHookAt > signalAt`）→ drop pre-grace | 同 case 2，但驗 timing 順序：hook 早於 timer expire | 同 case 2 |
| 5 | **pre-grace pass + post graceWindow catch**：caller 在 inject signal 前設 `lastHookAt = signalAt - 1ms`（hook 略早於 signal），即 hook 已 record 但尚未過 2s grace window；pre-grace check 看 `last.After(signalAt)` = false → 通過 → 進 `applyProbeGuards`；step 2 看 `now - last = 301ms < 2s` → drop | applied=false（既有 step 2 graceWindow 觸發） | `MetricProbeIntentSignalEmitted +1`（既有 metric preserved）；`MetricProbeIntentPreGraceHeld +1`；無 pre-grace drop；**`MetricProbeGraceWindowSuppressed +1`** + **`MetricProbeIntentDroppedGrace +1`**（既有 post graceWindow 雙計數，per `applyProbeGuards` step 2）；`onDrop reason="grace"`（既有 helper switch 命中） |

**Case 6（R13 boundary race）不在本 task gating** — 為 known limitation by spec R13；mlab A9 quantified threshold 把關 production 漏網率（P3-T1）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_test.go` | 新增 `TestConsumeSignals_PreGrace_Table` 表驅動 5 case；helper 注入 `orchNowFn` stub + `lastHookAt` 設定；ctx cancel 用 `context.WithCancel` |

**Helper 設計要點**：
- `withFakeNow(t *testing.T, baseAt time.Time)` 注入 `orchNowFn` 回 baseAt（讓 signalAt deterministic）
- `setHookAt(t, orch, session, at)` 透過 `recordHookAt`（既有 method）設定 `lastHookAt`；不直接 manipulate `lastHookAt` map
- `expectMetricDelta(t, metric *expvar.Int, before, want int64)` 比較 expvar.Int.Value() 變化（既有 dispatcher / orchestrator test 既有 pattern）

**Reason 斷言策略**：**只透過 metric 斷言 reason，不 intercept `probeIntentOnDropForSession` callback**（callback 是 package-level function，無 production seam 可替換；intercept 會違反 v7 trim「不加 seam」承諾）。每條新 reason 對應唯一 metric：
- `pre-grace` ↔ `MetricProbeIntentDroppedPreGrace +1`（exclusive）
- `pre-grace-canceled` ↔ `MetricProbeIntentPreGraceCanceled +1`（exclusive）
- `grace` ↔ `MetricProbeGraceWindowSuppressed +1` + `MetricProbeIntentDroppedGrace +1`（雙計數，case 5）

dev-mode log 是 observability 給人讀，不是 test 契約。

**Acceptance**：
- 5 case 全 PASS
- `go test ./internal/module/agent -run 'TestConsumeSignals_PreGrace' -count=1` 全綠
- `go test -race ./internal/module/agent -run 'TestConsumeSignals_PreGrace' -count=1` 全綠
- 各 case metric increment 對齊上表

**估計**：~150 行 test / 0 行 production

**依賴**：P1-T3（consumeSignals 改造完成）

---

## 2. Phase 2：既有 path 重驗 + regression

### P2-T1 — W6-3 ProcessDead lifecycle 5-case + `!appliedAny` rearm regression

**目標**：跑既有 W6-3 dispatcher lifecycle 5-case test，確認加入 pre-grace +300ms hold 後行為仍正確；特別驗 `!appliedAny` post-loop rearm 在 pre-grace drop case 之後仍能 trigger teardown + rearm。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_test.go` | 既有 `TestConsumeSignals_*` lifecycle 5-case test 預期值更新（含 +300ms latency 修正）；補 `TestConsumeSignals_PreGraceDrop_RearmsAfterTeardown` regression test 驗 pre-grace drop 後 detector exit → consumeSignals 退迴 case → teardown + rearm + 新 generation detector arm 仍正確 |

**Regression test 設計**：
- arm ProbeIntent
- inject signal + 同 session 設 `lastHookAt > signalAt`（pre-grace drop 觸發）
- assert：drop pre-grace + detector ctx 仍存活 → 下次 signal 仍能正常進入 timer hold（generation 不變）
- detector exit → outer for-loop 走 `!appliedAny` post-loop teardown branch → 新 generation detector arm

**Acceptance**：
- 既有 5-case test 全 PASS（+300ms latency 預期修正）
- `TestConsumeSignals_PreGraceDrop_RearmsAfterTeardown` PASS
- `go test ./internal/module/agent -run 'TestConsumeSignals' -count=1 -race` 全綠

**估計**：~80 行 test 修正 + ~50 行新 regression test

**依賴**：P1-T4（pre-grace test infrastructure 已建立）

---

### P2-T2 — ProcessDead wire test 重驗

**目標**：`probe_intent_dispatcher_codex_wire_test.go` 既有 ProcessDead wire test 跑 +300ms hold timing 修正後仍正確 wire 通 fake tmux pane lister → ProcessDead detector → dispatcher → applyProbeGuards → status mutation。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_codex_wire_test.go` | wire test 預期 latency +300ms 修正；不需新 test，只 timing 校正 |

**Acceptance**：
- `go test ./internal/module/agent -run 'TestProbeIntent.*Wire' -count=1` 全綠

**估計**：~20 行 test 修正

**依賴**：P2-T1

---

### P2-T3 — Cross-provider switch during hold test

**目標**：新增 cross-provider switch 場景：active ProbeIntent 進 hold 中，`reconcileSessionActive` 取消該 entry → ctx cancel → drop pre-cancel；不殘留 active entry。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_test.go` | 新增 `TestReconcileSessionActive_DuringPreGraceHold_CancelsAndCleans` |

**Test 設計**：
- arm ProbeIntent on session S（codex provider）
- inject signal → hold 開始
- 在 hold 中（timer 未 expire）呼叫 `reconcileSessionActive(S, newProvider="cc")` → 取消舊 entry ctx
- assert：`MetricProbeIntentPreGraceCanceled +1` / drop reason `"pre-grace-canceled"` / `activeProbeIntents[S]` 不殘留舊 entry / 新 cc entry 正確 register

**Acceptance**：
- test PASS
- `go test ./internal/module/agent -run 'TestReconcileSessionActive_DuringPreGraceHold' -count=1 -race` 全綠

**估計**：~70 行 test

**依賴**：P1-T4

---

### P2-T4 — Replay path test（daemon restart 後 ProbeIntent re-arm）

**目標**：驗 daemon restart 後 `replayStatus` 觸發 ProbeIntent re-arm 場景，pre-grace 行為與 fresh arm 一致；ProcessDead detector +300ms 檢測延遲仍 ≤2s W6-3 目標內。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_test.go` | 新增 `TestReplayStatus_TriggersProbeIntent_PreGraceConsistent` |

**Test 設計**：
- 模擬 daemon restart：先 setup 已存在 codex pane + session waiting
- 觸發 `replayStatus(session)` → ProbeIntent re-arm
- inject signal → 驗 pre-grace 300ms hold 行為與 fresh arm 一致
- 量測 process-dead 檢測 latency：1Hz poll + 300ms hold = ~1.3s ≤ 2s（spec §2.3 R6）

**Acceptance**：
- test PASS（latency assertion 用 timeout 或 fake clock）
- `go test ./internal/module/agent -run 'TestReplayStatus_TriggersProbeIntent' -count=1 -race` 全綠

**估計**：~60 行 test

**依賴**：P2-T1

---

### P2-T5 — `applyProbeGuards` 既有 OR/FX test zero regression

**目標**：跑既有 `probe_orchestrator_apply_guards_test.go` OR3 / OR4 / OR5 / FX1-FX5 test 全綠，確認 `applyProbeGuards` 行為**完全不變**（trim 後本 PR 不改 applyProbeGuards 任何行；包含不加 test seam）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_orchestrator_apply_guards_test.go` | 不需修改；只跑既有 test 確認 zero regression |

**Acceptance**：
- `go test ./internal/module/agent -run 'TestApplyProbeGuards' -count=1 -race` 全綠

**估計**：0 行（純 regression run）

**依賴**：P1-T3 結束後即可跑

---

### P2-T6 — Observability tests（3 pre-grace metric increment 觀察）

**目標**：補 metric increment 直接 assertion（P1-T4 已間接驗，本 task 確保 dashboard / monitoring 端 expvar key 正確 expose）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `internal/module/agent/probe_intent_dispatcher_test.go` | 新增 `TestProbeIntentMetrics_PreGraceExpvarExposed`：透過 `expvar.Get("purdex_probe_intent_pre_grace_held_total")` 等 3 個 key 確認可從 expvar registry 取得且 type 是 `*expvar.Int` |

**Test 設計**：
- 取 `expvar.Get(key)` for 3 個新 metric key
- 斷 not nil + type assertion `*expvar.Int`
- 既有 `MetricProbeGraceWindowSuppressed` 同樣 assertion（dashboard 不破）

**Acceptance**：
- test PASS
- `go test ./internal/module/agent -run 'TestProbeIntentMetrics_PreGraceExpvarExposed' -count=1` 全綠

**估計**：~30 行 test

**依賴**：P1-T1

---

## 3. Phase 3：mlab live verify + W6-3/W6-6 spec drift anchor

### P3-T1 — mlab live verify §1-§4

**目標**：在 mlab 機（100.64.0.2）跑 4 路徑 live verify，per spec §4.3 P3-T1 + §1.3 A9-A12。

**4 路徑**：

| § | 場景 | Acceptance |
|---|---|---|
| §1 | codex permission ask → user 按 [2] 拒絕 → 重複 30 次 | lights `waiting → idle` ≥ 28/30（**閃 running < 3** per A9 quantified threshold）；若 ≥ 3 surface evaluate（per R13 fail-fast handling — 開 followup issue 加 per-event trace） |
| §2 | codex permission ask → user 按 [1] 批准 | lights `waiting → running` ≤500ms+300ms observed |
| §3 | codex 進程被 SIGKILL（pane 仍存在）→ ProcessDead 抓死 | status=error；observed latency ≤2s（含 1Hz poll + 300ms pre-grace） |
| §4 | daemon restart during waiting status → replayStatus → ProbeIntent re-arm | 後續 hook / probe 行為與 J3 預期對齊 |

**驗證手段**：
- daemon 跑帶 `PDX_DEV_MODE=1`（既有 dev log 用）
- grep `pre-grace` / `pre-grace-canceled` drop reason 計數
- expvar curl `/debug/vars` 取 3 pre-grace metric increment 觀察
- §1 reject 30 次同樣計算閃 running 次數

**Acceptance**（per spec §1.3 A9-A12）：
- §1 PASS（≥ 28/30）
- §2 PASS
- §3 PASS
- §4 PASS

**A9 fail handling**（spec v7 fail-fast，per §2.3 R13）：若 §1 < 28/30，**不直接 ship**，先開 followup issue 加 per-event trace log + cross-log correlation 定位漏網類別（pre-grace miss / post graceWindow miss / `recordHookAt` 與 step 2 read μs race / 其他），透過 trace 數據真正解 R13 或調 N。

**估計**：1-2 小時手動 mlab capture + 0.5 小時數據彙整入 PR body

**依賴**：Phase 1+2 全部 commit；本 task 在 PR review round 之前完成（PR body §test plan 引用結果）

---

### P3-T2 — W6-3 spec §8 drift anchor

**目標**：在 W6-3 spec `docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md` §8 spec drift signals 加新 row（per spec §1.1 #5）。

**Anchor 條目**：

```
| 想為單一 ProbeIntent Kind 特化 pre-grace timer（如只對 ScreenChange，ProcessDead 不 hold）| fix-spec §3 不為單一 Kind 特化；J3 pre-grace 對所有 Kind generic 適用；detector 端不應自行加 pre-hold |
```

**改動**：

| 檔案 | 改動 |
|---|---|
| `docs/specs/2026-04-29-w6-3-codex-error-probe-intent-spec.md` | §8 spec drift signals table append 1 row |

**Acceptance**：spec doc commit；conform W6-3 spec §8 既有格式

**估計**：~5 行 doc

**依賴**：none（可平行）

---

### P3-T3 — W6-6 spec §8 drift anchor + reject race 段落留 J3 ship 後處理

**目標**：在 W6-6 v5 spec `docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md` §8 加同一 anchor；reject race 段落留待 J3 ship 後 W6-6 後續 PR update（不在本 PR scope）。

**改動**：

| 檔案 | 改動 |
|---|---|
| `docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md` | §8 spec drift signals table append 同 P3-T2 row |

**Acceptance**：spec doc commit；conform W6-6 spec §8 既有格式

**估計**：~5 行 doc

**依賴**：none（可平行 P3-T2）

---

### P3-T4 — 本 spec mlab 結果填回

**目標**：mlab live verify 完成後，在本 spec §1.3 A9-A12 acceptance 表上方加結果摘要 block 或 footnote。

**改動**：

| 檔案 | 改動 |
|---|---|
| `docs/specs/2026-05-01-probe-intent-bidirectional-grace-window-spec.md` | header status block 或 §1.3 末加「mlab live verify 結果：A9 N/30 閃 / A10 latency Xms / A11 latency Xms / A12 PASS」block |

**Acceptance**：mlab 數據明寫入 spec doc

**估計**：~10 行 doc

**依賴**：P3-T1 完成

---

## 4. PR 流程

PR 建立流程（per CLAUDE.md「完整開發流程」第 7-9 步）：

1. Phase 1+2 完成（subagent TDD：每個 task 獨立 commit；P1-T1 → P1-T2 → P1-T3 → P1-T4 → P2-T1-T6 順序）
2. Phase 3 P3-T1 mlab live verify（在主 session 跑）→ P3-T2/T3/T4 spec drift anchor + 結果填回
3. `gh pr create` — title `[J3] ProbeIntent dispatcher bidirectional graceWindow`；body 含 §summary / §test plan（mlab 4 路徑結果）/ §spec / §plan
4. **Round 1 standard codex review**：`/codex:review --base origin/main --background`
5. 收斂 round 1 finding（per `feedback_codex_pr_review_spec_alignment` — 對照 spec 採納）
6. **Round 2 三平行 adversarial codex review**（per CLAUDE.md PR Review 兩輪制）：
   - 攻擊方：lifecycle race / pre-grace 與 post graceWindow 邊界 / generation token bypass / ctx cancel goroutine leak
   - 防守方：spec alignment（v7 trim 是否被 review 提議倒退）/ in-scope ↔ acceptance / fix-spec §3 generic Kind 約束
   - 檔案體質：probe_intent_dispatcher.go 過長？SRP 違反？test file 切分合理？
7. 收斂 round 2 finding 直到 0 critical/P1（per `feedback_codex_review_termination`）；medium 屬 known issue 化追蹤入 issue
8. squash merge → main
9. 起 bump PR alpha.281（`bump-alpha-281` 獨立 worktree；VERSION + package.json + spa/package.json + CHANGELOG.md 同步；CHANGELOG 加 J3 entry）

---

## 5. Test plan summary

### Unit test（隔離測試）
- `TestConsumeSignals_PreGrace_Table`（P1-T4）：5 cases
- `TestConsumeSignals_PreGraceDrop_RearmsAfterTeardown`（P2-T1）：1 case
- `TestReconcileSessionActive_DuringPreGraceHold_CancelsAndCleans`（P2-T3）：1 case
- `TestReplayStatus_TriggersProbeIntent_PreGraceConsistent`（P2-T4）：1 case
- `TestProbeIntentMetrics_PreGraceExpvarExposed`（P2-T6）：1 case

**新增 test 小計**：9 cases

### Regression（既有 test zero regression）
- W6-3 ProcessDead lifecycle 5-case：latency +300ms 預期修正
- W6-3 wire test：latency +300ms 預期修正
- `applyProbeGuards` OR3/OR4/OR5/FX1-FX5：完全不變

### Live verify
- mlab §1-§4：4 必驗場景（reject 30 次 / approve / SIGKILL / replay）

### Test 總計
- ~9 新 unit test + ~6+ 既有 test 修正 + 4 mlab 場景

---

## 6. Known issues / risks

### 已知接受限制（non-blocking for ship per spec R13）

1. **R13 boundary race（hook 在 hold 過後到 + `recordHookAt` 與 step 2 read μs race）**：spec §2.3 標 acceptable as known limitation；mlab A9 quantified threshold（30 次 reject 閃 < 3）作為 production 漏網率 ship gate；A9 fail 才開 followup issue 加 per-event trace 定位漏網類別（per spec v7 fail-fast 處置）

### 風險與對策

| 風險 | 機率 | 影響 | 對策 |
|---|---|---|---|
| `consumeSignals` ctx 啟用後既有 caller 仍 pass `_` 變成 not-used 編譯失敗 | low | low | P1-T3 subagent build verify；若有 caller 改 `_, ` 為 `ctx, ` 即可 |
| pre-grace 與 post graceWindow 兩層保險時序 race（test 端難重現 deterministic）| medium | medium | P1-T4 透過 `orchNowFn` stub + caller-controlled `lastHookAt` 設定保 deterministic；不依賴 scheduler |
| W6-3 ProcessDead 既有 latency 測試預期值依硬編碼 hardcode timeout 而非 hold 時長 | medium | low | P2-T1 / P2-T2 timing 預期值 +300ms 修正時 sweep 全 lifecycle test 看有沒有漏改 |
| codex round 2 adversarial review 提議倒退 v7 trim（如建議加回 trace log）| medium | low | spec §6 已加 anchor「想加回 trace log / cross-log correlation / 雙 test seam」必須 surface；codex consulting verdict 為 trim 依據 |
| mlab A9 < 28/30（即 boundary race 漏網率 > 7%）| low | high (block ship) | per spec v7 fail-fast — 不 ship；開 followup issue 加 per-event trace log + cross-log correlation；可能 N=300ms 不夠，調至 400ms 或補 post-emit confirm rollback |
| ctx cancel during hold 後 timer.Stop 沒被呼叫導致 goroutine leak | low | low (限本 detector lifecycle) | P1-T3 implementation 必呼叫 `timer.Stop()` 在 ctx.Done branch；P2-T3 test 驗 cancel 後不殘留 active entry |

### Rollback 計畫

若 ship 後發現 regression：
1. revert merge commit
2. 起 hotfix bump
3. issue 追蹤 root cause
4. 重新 plan 修法 → 重新 PR

---

## 7. Bump PR

Phase 2 PR squash merge 後獨立 bump PR：
- VERSION: alpha.280 → alpha.281
- spa/package.json + package.json 同步
- CHANGELOG.md 加：
  ```
  ### lights rebuild
  - **J3**: ProbeIntent dispatcher bidirectional graceWindow
    - dispatcher `consumeSignals` 在 `applyProbeGuards` 之前 hold 300ms，期間 hook 到 → drop pre-grace
    - 雙向保險（pre-hold 300ms + post graceWindow 2s）cover ≥93% reject race；boundary race 走 R13 known limitation
    - 三新 expvar metric (`purdex_probe_intent_pre_grace_*`) 觀察 pre-grace 行為
  ```
- bump PR 用獨立 worktree：`bump-alpha-281`
- W6-6 後續 PR ship 排 alpha.282 起（J3 ship 後 update v5 spec reject race 段）

---

## 8. 完成檢核總表

| Phase | 檢核 | 狀態 |
|---|---|---|
| Phase 1 P1-T1~T4 | 4 task 全 commit + test 全綠 + race 全綠 | ⏳ |
| Phase 2 P2-T1~T6 | 6 task 全 commit + 既有 test zero regression + new regression test 全綠 | ⏳ |
| Phase 3 P3-T1 | mlab live §1-§4 全 PASS（含 §1 reject 30 次 ≥ 28/30 per A9 quantified threshold） | ⏳ |
| Phase 3 P3-T2/T3/T4 | W6-3 / W6-6 spec §8 drift anchor commit + 本 spec mlab 結果填回 commit | ⏳ |
| PR + codex review 兩輪 | 0 critical/P1 + known issue 追蹤化 | ⏳ |
| Squash merge → main | branch 刪除 + worktree 清理 | ⏳ |
| Bump PR alpha.281 | merge | ⏳ |
