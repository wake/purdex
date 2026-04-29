# Lights Rebuild — W3 Framework 撤回 + W4 Observability 補完 (Dev Spec)

- **Date**: 2026-04-29
- **Worktree**: `lights-w3-w4`（branch `worktree-lights-w3-w4`）
- **Work item**: W3 + W4（per `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §2 + §4 PR-4）
- **Base**: `main` @ `bff6cfad` (alpha.256；W2 全系列 + cleanup-followup ship 後)
- **依賴**: W1 audit ✅ alpha.243（PR #692 + bump #700）/ W2 全 ship ✅ alpha.256
- **Type**: 撤回 PR-4a-1 ship 的 generic framework + 補 dev mode log 跨層覆蓋
- **產出 Phase**: P1 (W3 撤回) → P2 (W4 dev log) → P3 (W4 trace gap audit)；視 P3 規模可併入 P2

---

## 0. 來龍去脈

### 0.1 為什麼 W3 + W4 同 PR

`fix-spec` §4 PR-4 明定 W3 撤回與 W4 observability 為**同一 PR**：

> PR-4 [W3 + W4] Framework 撤回 + Observability 補完
>   - 撤 §3 列的 framework code
>   - manageActivityWatch policy 改 per-agent gating（三家先全 disable probe，等 W6 為各 agent 加 ProbeIntent 才啟動）
>   - 補 dev log 覆蓋整條 hook → DeriveStatus → handler → projection → broadcast 路徑
>   - 補 TraceStore step 在每層轉換點寫一筆（per spec §2.3 SOT 設計）
>   - 依賴 PR-2 audit 結果決定 dev log 哪些路徑 priority 最高
>   - 依賴 PR-3 完成（撤回時 catalog 已是 PurdexName 形態）

W3 撤回後因 always-on 補位消失，部分 W5 燈號 bug 會裸露（audit §0.1 baseline framing），W4 同期補完 observability 是觀察期所需 — 沒有 dev log 跨層覆蓋無法精準診斷之後 W5/W6 要修哪些缺口。

### 0.2 W3 撤回會造成「觀察期 regression」（trade-off explicit）

audit §0.1 已說明：當前 main `manageActivityWatch` always-on 對 §6 部分 W5 條目（W5-1 cc permission→running、W5-3 cc compact→idle）提供**不精準的補位**，使用者體感不到「lights 卡死」。W3 撤回後這些補位裸露：

| W5 ID | agent | 撤回後使用者可見影響 | W6 對應修補（非本 PR） |
|-------|-------|----------------------|--------------------------|
| W5-1 | cc | permission 批准後 running 燈不亮（直到下個 hook） | W6-1 |
| W5-2 | cc | waiting dual fire 仍可能；不在 always-on cover 範圍 | 觀察 |
| W5-3 | cc | compact 退場可能卡 idle 不更新 | W6-2 |
| W5-4 | codex | error 不亮（FutureOnly hook 未發） | W6-3 (P1, first ProbeIntent) |
| W5-5 | codex | clear 不消失（FutureOnly hook 未發） | W6-4 |
| W5-6 | opencode | running 中介 plugin filter 可補；不依 always-on | plugin 補 mapping |
| W5-7 | opencode | error/idle 交織 RPC 性質 | W5-7 仍走 RPC reconcile，本 PR 不影響 |
| W5-8 | opencode | running plugin 補 mapping | plugin 補 mapping |

**結論**：本 PR 是觀察期切換點，明知 W5-1/W5-3/W5-4/W5-5 會回到「純 hook coverage」缺口狀態。W6-3 (codex error) 是 W6 推薦第一個動工的 ProbeIntent，定 interface 後逐步補位。

### 0.3 解掉的 issue

- **#719** PR-4a-1 always-on probe framework residue — 撤掉 always-on policy 後此 issue 直接 obsolete，併同 PR merge 時關閉
- **#698** daemon restart 後 activeWatchers 不恢復 — W3 撤掉 always-on 後 watcher 數量歸零，此 issue 暫時不再 user-visible；W6 重新引入 caller 時再評估是否要 restart-recovery 機制（per audit §7.1.2 platform prerequisite）

### 0.4 不在 scope

- ❌ W5 燈號 bug 修復（依賴 W3 ship 後再啟動 PR-5+）
- ❌ W6 ad-hoc ProbeIntent（同上；本 PR **不**前置定義 ProbeIntent interface — lazy 設計，等 W6-3 第一個 PR 才 finalize）
- ❌ W7 Dev Inspector UI（依賴 W4 dev log 補完後）
- ❌ Bun.spawn / opencode plugin 改動（W2 已處理）
- ❌ TraceStore schema 變更（既有 1057 行 schema 已可覆蓋 5-step pipeline）
- ❌ SPA 端任何改動（dev log 純 daemon 端）
- ❌ issue #717 opencode liveness（W6 scope）

---

## 1. 撤回清單（W3 範圍）

### 1.1 完整移除清單

**從 main 撤回**（PR-4a-1 ship 的 generic framework 部分）：

| # | 項目 | 檔案 / 位置 | 撤回後行為 |
|---|------|-------------|------------|
| R1 | `ProbeProfileProvider` interface | `internal/agent/provider.go:275-280` | 移除；orchestrator 不再 type-assert |
| R2 | `ProbeProfile` struct | `internal/agent/provider.go:282-295` | 移除；改由 `probe.WatchOptions` 直接承擔 caller 提供 |
| R3 | cc `ProbeProfile()` impl | `internal/agent/cc/probe_profile.go` (19 行) | 整檔刪 |
| R4 | cc `ProbeProfile()` test | `internal/agent/cc/probe_profile_test.go` (26 行) | 整檔刪 |
| R5 | `defaultProbeProfile` 變數 | `internal/module/agent/probe_orchestrator.go:21-28` | 移除（probe 不 always-on，無 default） |
| R6 | type-assert profile lookup 區塊 | `internal/module/agent/probe_orchestrator.go:144-151`（`startWatch` 內 `provider.(agentpkg.ProbeProfileProvider)` 與其包圍 if） | 移除；`startWatch` signature 改吃 `probe.WatchOptions` |
| R7 | always-on policy `shouldWatchActivity` | `internal/module/agent/module.go:525-532` | 移除 |
| R8 | always-on caller `manageActivityWatch` 內 `if shouldWatchActivity(...)` 分支 | `internal/module/agent/module.go:509-522` | 改成 default no-op；註解明示「W6 ProbeIntent 第一個 caller 進來時再啟動 watcher」 |
| R9 | OR1 `TestOrchestrator_StartWatchUsesAgentProfile` | `internal/module/agent/probe_orchestrator_test.go:757-783` | 撤；改測 startWatch 直接吃 opts |
| R10 | OR2 `TestOrchestrator_DefaultProfileWhenAgentMissing` | `internal/module/agent/probe_orchestrator_test.go:785-810` | 撤（無 default，即不需 fallback test） |
| R11 | FX4 mutually-exclusive validation test | `internal/module/agent/probe_orchestrator_test.go:507`（具體名稱以實際檔案為準） | 改成測「caller 傳入 invalid opts → startWatch 回 false 並 dev-log 抱怨」；保留校驗邏輯 |
| R12 | `manageActivityWatch` 整個函式 doc comment 中關於 ProbeProfile / 預設 fallback 的描述 | `internal/module/agent/module.go:490-499` | 重寫；說明本 PR 後預設 no-op |

### 1.2 保留清單（不變動）

- `internal/agent/probe/{watch,activity,liveness,readiness,shell_prompt}.go` — shared utility primitives，W6 caller 仍會用
- `internal/tmux/executor.go` 的 `CapturePaneRange` / `CapturePaneTopLines` — 同上
- orchestrator 的 graceWindow / Error guard / stale-callback / transition gate / `recordHookAt` / `interpretScreenEvent` 完整流程（共 6-7 個 codex finding 攢出來的保護機制，全保留）
- 4 個 expvar counter：`MetricProbeWatchStarted` / `MetricProbeWatchStopped` / `MetricProbeScreenEvent` / `MetricProbeGraceWindowSuppressed`（撤回後本 PR 期不增長，counter 本身保留）
- `isDevMode()` helper + 既有 5 條 `[probe]` dev log（後 W4 統一風格，但不刪除）
- `recordHookAtHook` / `interruptBeforeFinalLockFn` / `orchNowFn` 三個 test seam

### 1.3 撤回後 startWatch 簽名（W3 完成形態）

**Before**:
```go
func (o *probeOrchestrator) startWatch(session, agentType string) bool {
    // 內部 lookup ProbeProfileProvider，撈出 ProbeProfile，做 mutually-exclusive 校驗，opts := probe.WatchOptions{...}
}
```

**After**:
```go
// startWatch wires a probe watcher with caller-provided options. Caller (e.g. W6 ProbeIntent
// hook in agent module) decides watcher parameters per detection need; orchestrator no longer
// owns a default profile. Returns false when prober is nil or opts violate probe.Watch contract
// (TopLines + BottomLines mutually exclusive).
func (o *probeOrchestrator) startWatch(session, agentType string, opts probe.WatchOptions) bool {
    pw := o.prober()
    if pw == nil {
        return false
    }
    if opts.TopLines > 0 && opts.BottomLines > 0 {
        if isDevMode() {
            log.Printf("[probe] startWatch invalid opts session=%s agent=%s TopLines=%d BottomLines=%d — mutually exclusive; not registering",
                session, agentType, opts.TopLines, opts.BottomLines)
        }
        return false
    }
    pw.Watch(session+":", opts, o.makeCallback(session, agentType))
    agentpkg.MetricProbeWatchStarted.Add(1)
    return true
}
```

`manageActivityWatch` 撤掉 `shouldWatchActivity` 之後變：

```go
func (m *Module) manageActivityWatch(session, agentType string, newStatus agentpkg.Status) {
    // W3: probe is no longer always-on. This function only stops a stale watcher when the
    // session's status changes; W6 ad-hoc ProbeIntent (introduced per per-agent need) will
    // be the entry point that calls m.probeOrch.startWatch directly with WatchOptions.
    m.mu.Lock()
    _, wasWatching := m.activeWatchers[session]
    delete(m.activeWatchers, session)
    m.mu.Unlock()
    if wasWatching {
        m.probeOrch.stopWatch(session)
    }
    _ = newStatus // reserved for W6 hook; intentionally unused after W3 revert
}
```

> **設計選項討論**：另一個極端是把 `manageActivityWatch` / `activeWatchers` map 全部撤掉，由 W6 caller 完全自管。但這會在 W6 第一個 ProbeIntent PR 內重新生出活動 watcher 的 lifecycle 機制（rename / stop on session-end / restart-recovery），同時把 stale-callback guard 機制再講一遍。**保留 stop-on-status-change 入口**（即上述 simplified 版本）讓 W6 可以漸進加 start 邏輯，避免大重構。

### 1.4 撤回後 manageActivityWatch caller 路徑

`manageActivityWatch` 的 caller 在 `handler.go:361` 仍會呼叫（per issue #719 source pointer），但**只執行 stop 半邊**：

```go
// handler.go:361 (unchanged)
if req.TmuxSession != "" && m.prober != nil && result.Valid {
    m.manageActivityWatch(req.TmuxSession, watchAgentType, watchStatus)
}
```

效果：
- 任何狀態變化都會 stop 既有 watcher（如有）
- **不**啟動新 watcher（W3 撤回的核心改變）
- 4 個 expvar counter 中 `MetricProbeWatchStopped` 在 ship 初期可能會 +N（清掉 user 升級前殘留的 watcher）；之後就不再增長
- 「W3 撤回後」期 W6 第一個 ProbeIntent 進來前，`MetricProbeWatchStarted` / `MetricProbeScreenEvent` / `MetricProbeGraceWindowSuppressed` 三 counter 全為 0

---

## 2. W4 Observability 補完範圍

### 2.1 現況差距分析（critical baseline finding）

跑 `grep -n "isDevMode\|log.Printf" internal/module/agent/handler.go internal/module/agent/frame_ops.go` 得到 9 條 `log.Printf`：

| 位置 | 內容 | 性質 |
|------|------|------|
| `handler.go:238` `log.Printf("[agent] clear legacy event on invalid result: %v", err)` | 失敗清理 | error log（無 isDevMode gate） |
| `handler.go:280` `log.Printf("[agent] frame event: %v", err)` | applyFrameEvent 失敗 | error log |
| `handler.go:291` `log.Printf("[agent] session projection: %v", err)` | projection 計算失敗 | error log |
| `handler.go:302` `log.Printf("[agent] clear legacy event: %v", err)` | clear 失敗 | error log |
| `handler.go:445`, `handler.go:919` | list/history 失敗 | error log |
| `frame_ops.go:289` `[agent] rebuild_from_process_tree_failed` | sweep 失敗 | error log |
| `frame_ops.go:1035`, `frame_ops.go:1209` | reconcile partial state | error log |

**全部都是 error path**，沒有任何一條對應「正常路徑的 dev-mode trace」。即 PDX_DEV_MODE=1 跑 daemon 時，正常 hook 流程在 daemon 端 console 完全看不到 chain 跑到哪一層。

只有 `[probe]` (5 條) 和 `[agent][trace]` (`internal/module/agent/trace.go:35,52`) 有 dev-mode trace；後者只記 TraceStore save/drop chain，不記中間步驟。

### 2.2 W4 補完目標

讓 PDX_DEV_MODE=1 跑 daemon 時，**單一 hook 的完整 chain 路徑**可在 console log 觀察到 5 步驟：

```
[hook]      trigger session=X agent=Y purdex_name=Z chain_id=...
[derive]    verify_passed agent=Y purdex_name=Z status=running reason=
[handler]   frame_apply session=X frame_id=... lifecycle=Pdx... decision=updated_frame
[handler]   projection_built session=X top_status=running tabs=... codes=...
[handler]   broadcasted session=X clients=N reason=... raw_event_name=Z
```

對應 5 個 trace step：trigger / verify / frame / projection / emit（per `internal/module/agent/trace.go`）。每步在 dev mode 下都對應一條 `[xxx]` log line。

### 2.3 補完路徑（依 W1 audit Quick Paths priority）

優先序依 audit §0.2 + §6 / §7 工作池 — 哪些 status path 是 W5/W6 後續最會用 trace 診斷的：

**P1 – 必補（W6-3/W6-4 直接會用）**
- `[hook]` trigger 入口（handler.go HandleEvent / handleHook 開頭）— 每 hook event 一條
- `[derive]` DeriveStatus 出口（每家 `cc/codex/opencode/status.go DeriveStatus` return 後）— invalid path 也記
- `[handler]` invalid path 早 return（handler.go:155-158 catalog miss / 236-242 subagent skip 等）
- `[broadcast]` 廣播決策（handler.go:324 trace.Emit 後）

**P2 – 高值補完（W5-7 opencode reconcile 路徑、W6-1/W6-2 cc spinner 路徑）**
- `[handler]` frame_apply 結果（handler.go:286 trace.Frame 後）
- `[handler]` projection_built 結果（handler.go:298 trace.Projection 後）
- frame_ops.go 各 lifecycle dispatch 入口（detail-only / SessionEnd / SubagentStart-Stop / SessionStart hot path 4 處）

**P3 – 補完（次要）**
- `m.broadcastToSession` 內 client count + 過濾條件
- `replayFromDB` / `sendSnapshot` 路徑（snapshot 重送時 dev log 標記 source=replay）
- `path_hint_extractor` PreToolUse / PostToolUse 命中與否

### 2.4 dev log 標籤約定

**新標籤**（W4 引入）：
- `[hook]` — handler 入口 trigger，1 hook = 1 條
- `[derive]` — DeriveStatus 出口 verify_passed/skipped + reason
- `[handler]` — handler.go 內處理流程（frame_apply / projection_built / broadcasted / invalid_skip）
- `[broadcast]` — 實際 WS 廣播決策（含 client count / suppress reason）

**沿用標籤**：
- `[probe]` — orchestrator（W3 撤回後本 PR 期間幾乎不會印）
- `[agent]` — error path（既有 9 條保留；dev mode 與否都印）
- `[agent][trace]` — TraceStore save/drop（既有保留）

### 2.5 設計約束

**MUST**:
- 所有新增 dev log line 必須 `if isDevMode() { ... }` gate，避免 production log 噴發
- 每行格式統一 `[xxx] kind key=value key=value`，便於 grep / `tail -f` 解讀
- chain_id (= TraceChainID) 帶在前段欄位（[hook]/[derive]/[handler]/[broadcast]）
- 不得改 production log 行為（既有 `[agent]` error 9 條原樣保留）

**MUST NOT**:
- 不新增任何 expvar counter（per fix-spec §2「不擴不撤」）
- 不重寫 TraceStore schema
- 不改 trace.go 的 5-step kind 分類（既有 trigger/verify/frame/projection/emit 不動）
- 不引入新 log library / structured log；維持 `log.Printf` 純 stdlib

### 2.6 TraceStore step 補完（gap audit）

既有 `internal/module/agent/trace.go` 5 steps：
- `trace.go:103+` `BeginHookTrace` → `trigger`
- `trace.go:150+` `Verify` → `verify`
- `handler.go:286` `trace.Frame` → `frame`
- `handler.go:298` `trace.Projection` → `projection`
- `handler.go:324` `trace.Emit` → `emit`

需 audit 確認是否每條 hook 路徑都會走過全部 5 步。**Phase 3 預期工作**：
- 寫 audit script / 跑 mlab 看哪些 hook 類型缺 step（subagent detail-only / invalid catalog miss / SessionEnd / replay-from-DB）
- 對缺 step 的路徑判斷：(a) 缺是合理的（如 subagent detail-only 不 broadcast 故無 emit step）— 用 doc 註解；(b) 缺是 bug（補 step）

預期此 phase **不會大量改 code** — TraceStore 已涵蓋大部分；focus 在 audit doc + 少量補測試。如果 P3 工作量很小（< 50 行 diff），併入 P2 同 PR commit 即可。

---

## 3. Phase 拆分

按照 CLAUDE.md「合適 review 大小」+ feedback `phase_skip_threshold` 不順手做下一 phase：

### 3.1 Phase 1 — W3 撤回（純 removal）

**Scope**：§1.1 R1-R12 全部撤回 + §1.3/§1.4 signature 與 caller path 改造

**獨立可 ship 的合理性**：
- 撤回後 daemon 仍 boot，三家 hook 仍 broadcast 狀態（依 audit baseline）
- W6 ad-hoc ProbeIntent 還沒進來，4 個 probe counter 全 0（這是 expected）
- mlab live verify：跑 cc 一個 prompt，broadcast 仍正常；issue #719 daemon log 不再有 always-on `[probe]` 訊息

**估算大小**：~250 lines（其中 ~120 是刪除測試 OR1/OR2 + cc/probe_profile_test.go；新測試 ~50；signature 改 ~30；caller 改 ~50）

**TDD flow**：
1. 先改測試（撤 OR1/OR2/probe_profile_test，FX4 改吃 opts，新增 `TestManageActivityWatch_DefaultNoOp`）
2. 改 production code 讓測試過
3. `go test ./...` + `go vet ./...` 全綠
4. mlab build/install/verify

### 3.2 Phase 2 — W4 dev log 跨層補完

**Scope**：§2.3 P1 + P2 dev log lines + 標籤約定 §2.4

**獨立可 ship 的合理性**：
- 純加 log line + isDevMode gate；零 production 行為變化
- 可獨立 verify：PDX_DEV_MODE=1 跑 daemon，丟一個 cc UserPromptSubmit hook，console 5 條 log line 連貫

**估算大小**：~200 lines（其中 ~80 是新 log line；~50 是測試 — 用 capture log 驗 dev mode + 非 dev mode；~70 是註解 / format helper）

**TDD flow**：
1. 寫 test 用 `t.Setenv("PDX_DEV_MODE", "1")` + capture log，驗每層觸發後對應 log line 出現
2. 在 handler/frame_ops 補對應 log line
3. 全綠 + mlab live verify（PDX_DEV_MODE=1 看 console）

### 3.3 Phase 3 — W4 TraceStore step gap audit

**Scope**：§2.6 audit doc + 必要補測試

**併入 Phase 2 的條件**：實際發現的 gap < 50 lines diff（high probability — 既有 trace 已大致覆蓋）

**獨立 phase 的條件**：發現有需要補新 trace step（如 replay-from-DB 路徑沒進 trace）— 需要動 trace.go schema 級

**判斷時機**：Phase 2 結束時，跑一輪 hook coverage smoke + 看 TraceStore 出來的 chain 有沒有缺 step。

### 3.4 Phase 順序與依賴

```
P1 (W3 撤回)         ← 可獨立 ship
  ↓
P2 (W4 dev log)      ← 依賴 P1（撤回後 [probe] 行為才穩定）；同 PR
  ↓
P3 (TraceStore audit)← 視結果併入 P2 或拆 PR
  ↓
[PR-4 squash merge]  ← 三 phase 全 ship 後一起 squash
[bump PR alpha.257]  ← merge 後立 bump PR
```

**單 PR 還是多 PR 子 PR**：W3 + W4 走**同一個 PR 多 commit**（每 phase 一個 commit，獨立可 revert），squash merge 進 main。理由：
- fix-spec §4 PR-4 明定 W3 + W4 同 PR
- 規模合理（~500 lines diff），不需要拆
- 觀察期 regression 跟 dev log 補完同 ship 有 narrative 一致性

---

## 4. Test Plan

### 4.1 單元測試（Go）

**Phase 1**：
- 撤 `internal/agent/cc/probe_profile_test.go` 全檔
- `internal/module/agent/probe_orchestrator_test.go`：
  - 撤 OR1 (`TestOrchestrator_StartWatchUsesAgentProfile`)
  - 撤 OR2 (`TestOrchestrator_DefaultProfileWhenAgentMissing`)
  - 改 FX4 (`TestStartWatch_InvalidOptionsRollback` 或新名稱)：caller 直接傳入 `probe.WatchOptions{TopLines: 5, BottomLines: 5}` → 期 false + dev log 印 `[probe] startWatch invalid opts`
- `internal/module/agent/module_test.go` 新增：
  - `TestManageActivityWatch_DefaultNoOp` — 任何 status 改變都只 stop 既有 watcher，不 startWatch（用 fake prober 觀察 Watch 呼叫 0 次）
  - `TestManageActivityWatch_StopsExistingWatcher` — 先 manually 設 activeWatchers，呼叫後驗 stopWatch +1
- 既有 `Test*Probe*` 測試走完一輪 — 確認 graceWindow / Error guard / stale-callback / transition gate 沒被破壞

**Phase 2**：
- `internal/module/agent/handler_test.go` 新增：
  - `TestHandler_DevModeLog_HookTrigger` — capture log + `t.Setenv("PDX_DEV_MODE", "1")`，丟一個 valid hook，驗 `[hook]` line 出現含 chain_id/agent/purdex_name
  - `TestHandler_DevModeLog_DeriveVerifyPassed` — 同上驗 `[derive] verify_passed`
  - `TestHandler_DevModeLog_FrameApply` — 驗 `[handler] frame_apply`
  - `TestHandler_DevModeLog_ProjectionBuilt` — 驗 `[handler] projection_built`
  - `TestHandler_DevModeLog_Broadcasted` — 驗 `[broadcast]`
  - `TestHandler_NoDevModeLog_Production` — `t.Setenv("PDX_DEV_MODE", "0")`，相同 hook，驗無新增 log line（既有 `[agent]` error 不在這條 hook path）
  - `TestHandler_DevModeLog_InvalidCatalogMiss` — invalid hook + dev mode，驗 `[handler] invalid_skip` + reason

**Phase 3（如需）**：
- `internal/module/agent/trace_test.go` 補：
  - 跑完整 hook chain，assert TraceStore record 5 step kinds 全有

### 4.2 mlab live verify

**Phase 1 verify**（撤回不破壞既有功能）：

| § | 步驟 | 期望 |
|---|------|------|
| §1 | build daemon: `go build -o /tmp/pdx ./cmd/pdx` | 編譯通過 |
| §2 | stop 舊 daemon, start `/tmp/pdx serve` env `PDX_DEV_MODE=1` | listen 100.64.0.2:7860 |
| §3 | 跑 cc UserPromptSubmit | broadcast normalized event；status=running |
| §4 | 跑 cc Stop | broadcast；status=idle |
| §5 | grep daemon log `\[probe\]` | 0 hit（撤回後無 always-on） |
| §6 | curl `/debug/vars` | `purdex_probe_watch_started_total=0` 不再增長 |
| §7 | 跑 codex / opencode 同樣 hook 一遍 | broadcast 正常；無 `[probe]` log |

**Phase 2 verify**（dev log 跨層覆蓋）：

| § | 步驟 | 期望 |
|---|------|------|
| §8 | tail daemon log，跑 cc UserPromptSubmit | 看到 5 條連貫 log：`[hook] trigger ...` → `[derive] verify_passed ...` → `[handler] frame_apply ...` → `[handler] projection_built ...` → `[broadcast] ...` |
| §9 | 同步 chain_id 對得上 | 5 條 log 同 chain_id |
| §10 | 跑 invalid catalog miss（發 `purdex_name=BogusEvent` 給 cc） | `[hook] trigger` → `[derive] skipped reason=event_not_in_catalog` → `[handler] invalid_skip`；無 frame/projection/broadcast log |
| §11 | 跑 SubagentStart | trigger/derive/frame 都有；projection skip（detail-only 不 broadcast）；用 doc 解釋 |
| §12 | 切 PDX_DEV_MODE=0 重啟 | tail 無新標籤 log；既有 `[agent]` error path 不變 |

### 4.3 已知盲區（live verify 不覆蓋）

- W5/W6 缺口本來就在，本 PR 不修。dev log 應該能更精準看到「哪個 status 路徑沒走過」這個 baseline observation
- 撤回對 user 升級無影響（純 daemon 端，不需 user reinstall hooks）

---

## 5. 風險與 Mitigation

| Risk | 嚴重 | 信心 | Mitigation |
|------|------|------|------------|
| R1 撤回後 cc/codex/opencode 出現 lights 卡死 user 投訴 | medium | high | spec §0.2 explicit；CHANGELOG entry 標 known regression；W6-3 第一個 ad-hoc ProbeIntent 推薦立即啟動 |
| R2 W4 dev log 量太大，PDX_DEV_MODE=1 跑 console 噴 | low | medium | 每 hook 5 條 + 每秒最多 ~10 hook = ~50 line/sec；可接受；用 isDevMode gate 確保 production 0 影響 |
| R3 撤回 OR1/OR2 後 startWatch 邏輯回退測試覆蓋率不足 | medium | medium | 改 FX4 + 新增 default-no-op test 補回；codex round 1 review 抓漏 |
| R4 W4 dev log 加在 hot path 衝擊性能 | low | low | `isDevMode()` 是 env-var read，~10ns；production 不印；hot path 平均加 2 funccall 量級可忽略 |
| R5 issue #719 always-on residue 沒在 mlab log 完整消失（殘留 watcher 沒清） | medium | medium | Phase 1 verify §5 grep `[probe]` 必須 0 hit；非 0 視為 P1 finding |
| R6 並發 session 寫 main repo 期間 worktree 被污染 | low | high | feedback `concurrent_session_safety` + `worktree_absolute_path`；Edit 全用絕對路徑 |
| R7 codex round-1 不讀 spec, 建議違反 W3 「不前置 ProbeIntent interface」原則 | medium | high | feedback `codex_pr_review_spec_alignment`；round-2 防守視角必派 |
| R8 W4 規模膨脹（補太多 log line / 加新 trace step） | medium | medium | spec §2.3 嚴格 P1/P2/P3 分級；本 PR 限 P1+P2；P3 視 audit 結果 |

---

## 6. 結束條件（W3 + W4 完成）

- ✅ §1 撤回清單 R1-R12 全部完成
- ✅ §2.3 P1 + P2 dev log 行全部就位（`[hook]` / `[derive]` / `[handler]` / `[broadcast]`）
- ✅ §2.6 TraceStore audit 完成（gap doc 或補測試）
- ✅ Go test `go test ./...` 全綠（22 packages）
- ✅ SPA `pnpm lint` / `pnpm build` clean（baseline 4 既有 vitest fail 不變）
- ✅ mlab live verify §1-§12 全 PASS
- ✅ codex round 1 標準 + round 2 三平行 adversarial 收斂無 critical / P1
- ✅ issue #719 PR description 標 `Closes #719`，merge 時自動關閉
- ✅ CHANGELOG entry 標 known regression（W5-1/W5-3/W5-4/W5-5 lights 觀察期）
- ✅ Memory `kickoff_lights_rebuild.md` 更新觸發詞為「W5 燈號 bug」+「W6 per-agent ProbeIntent」

---

## 7. 對齊已決議的設計關鍵點

per `kickoff_lights_rebuild.md` §「對齊已決議的設計關鍵點」+ fix-spec §7：

| 原則 | 本 spec 對應 |
|------|--------------|
| 1. Probe 不是 always-on | §1 R7/R8 撤回 always-on policy |
| 2. 缺口 per-agent specific | §0.2 trade-off 說明 W6 各 agent 缺口分開處理 |
| 3. 補位方式 ad-hoc 在 agent module 內 | §1.3 simplified `manageActivityWatch` 留 hook 給 W6；不前置 ProbeIntent interface |
| 4. 抽象在 input/output 邊界（catalog naming） | W2 已 ship；本 PR 不動 |
| 5. 觀察優先（runtime observability vs ship-time sampling） | §2 W4 dev log 跨層覆蓋 = runtime observability 補完 |
| 6. Phase 4a-2 工作全部撤回 | §1 R1-R6 |
| 7. 保留 PR-4a-1 ship 的 shared utilities | §1.2 保留清單 |
| 8. 接受 alpha 階段破壞性升級 | 本 PR 純 daemon 改動，user 升 alpha.257 無感（不需 reinstall） |

---

## 8. 文獻

- **Fix-spec**: `docs/specs/2026-04-28-lights-rebuild-fix-spec.md`（235 行；§3 撤回清單 / §4 PR 拆分 / §7 設計原則）
- **W1 audit**: `docs/specs/2026-04-28-hook-status-audit-spec.md`（577 行；§0.1 baseline framing / §6 W5 工作池 / §7 W6 工作池）
- **原 spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md`（§8.1 / §8.2 / §9）
- **W2 spec/plan**:
  - `docs/specs/2026-04-28-catalog-naming-separation-spec.md`
  - `docs/specs/2026-04-28-catalog-naming-separation-plan.md`
- **PR-4a-1 plan（被本 PR 撤回 framework 部分）**:
  - `docs/specs/2026-04-27-lights-rebuild-phase-4a-1-plan.md`
- **既有 PR**: #670 (PR-4a-1 ship at alpha.234)
- **相關 issue**: #719（撤回後 close）/ #698（撤回後暫時 obsolete，W6 重評）/ #717（W6 scope，本 PR 不影響）

---

## 9. 後續產出

本 spec ship 後，按 fix-spec §10 順序產出對應 plan：

- ✅ 本 spec — `docs/specs/2026-04-29-lights-w3-w4-revert-and-observability-spec.md`
- 接著 — `docs/specs/2026-04-29-lights-w3-w4-revert-and-observability-plan.md`（per phase task 拆分 + TDD 順序 + 依賴矩陣）
- 後續 W5/W6 — 待 W3+W4 ship 後分別啟動

---

## 10. 結束 disclaimer

本 spec 是 fix-spec PR-4 工作項；ship 後 W5（燈號 bug 修復）+ W6（per-agent ad-hoc ProbeIntent）將在後續分別啟動 PR；W7 Dev Inspector UI 等 W4-W6 stabilize 後啟動。
