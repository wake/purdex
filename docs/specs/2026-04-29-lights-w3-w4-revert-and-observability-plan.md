# Lights Rebuild — W3 + W4 Plan (PR-4 Implementation)

- **Date**: 2026-04-29
- **Companion spec**: `2026-04-29-lights-w3-w4-revert-and-observability-spec.md`
- **Worktree**: `lights-w3-w4`（branch `worktree-lights-w3-w4`）
- **Base**: `main` @ `bff6cfad` (alpha.256)
- **Status**: Draft（待 codex review）

---

## 0. 來龍去脈

W3 撤回 + W4 observability 同 PR-4 三 phase 實作 plan。

**Phase 大表**：

| Phase | 範圍 | 預期 commit 數 | 預期 LoC | 依賴 |
|-------|------|---------------|----------|------|
| P1 | W3 撤回（pure removal + signature 改 + caller 改） | 5-7 | ~250 | — |
| P2 | W4 dev log 跨層覆蓋（[hook]/[derive]/[handler]/[broadcast]） | 5-7 | ~200 | P1 |
| P3 | TraceStore step gap audit doc + 必要補測試 | 1-3 | ~50 | P2 |
| Bump | alpha.257（VERSION + package.json + spa/package.json + CHANGELOG） | 1 | ~10 | PR squash merge |

---

## 1. Conventions

### 1.1 Commit format

`<type>(<scope>): <subject>` — 對齊既有 commit history（W2 P1/P2/P3 全用此格式）：

- `refactor(agent): W3 P1-Tx <task subject>`
- `feat(observability): W4 P2-Tx <task subject>`
- `docs(specs): <task subject>`
- 末尾不加 Co-Authored-By（per existing W2 pattern）

### 1.2 Branch

- 所有 commit 進 `worktree-lights-w3-w4`
- 每 task 獨立 commit
- ship 時 PR squash merge（單一 squash commit 進 main）
- worktree 清理走 main repo `gh pr merge --squash --delete-branch`，避免在 worktree branch 內刪自己

### 1.3 TDD flow（每個 task）

```
1. 改測試（撤 / 反向斷言 / 新增 failing test）
2. go test ./<package> → 預期 fail
3. 改 production code → 預期 pass
4. go test ./... → 全綠
5. go vet ./... + golangci-lint（如裝） → 0 warning
6. commit
```

W3 撤回部分 task 是純 deletion（無新邏輯），TDD 退化成「**compile-driven removal**」：
1. 改測試（撤 OR1/OR2 等）+ 改 production 同 commit（因 type 撤掉 compile-fail）
2. 跑 → 全綠

### 1.4 路徑前綴

**所有 Edit/Write 必須帶 worktree 絕對路徑前綴**：
`/Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-w3-w4/...`

per `feedback_worktree_absolute_path` — 過去有 leak 到主 repo 的事故。

### 1.5 Subagent 委派

per `feedback_subagent_tdd_priority` — 每個 task（寫測試 + 實作）派 subagent 執行 TDD；主 session 只做 plan / 整合 / review。

---

## 2. Pre-flight checks（每 phase 開工前跑一次）

```bash
# 必跑（順序：fetch → status → test 基準）
git fetch origin
git status -s                  # 預期 clean
git log --oneline -3           # 確認 HEAD 與 plan 對齊
go test ./...                  # 既有 baseline 全綠
go vet ./...                   # baseline 0 warning
cd spa && pnpm install --frozen-lockfile  # SPA dep
cd spa && pnpm run lint        # baseline clean
cd spa && pnpm run build       # baseline clean
cd spa && npx vitest run       # 預期 4 既有 fail（TabBar + hosts contributor，alpha.247 自帶）
```

**注意**：spa 4 baseline fail 不是本 PR 引入；W3+W4 只動 daemon 端 Go code，預期 SPA test pass count 不變。

---

## 3. Phase 1 — W3 撤回

### 3.1 Phase 1 task list

**Final commit 編號 = P1-T1 ~ P1-T4**（per round-2 R2-06 統一）。子任務（compile-driven 同 commit 合併進 final commit 的部分）標 a/b/c：

| Task | 名稱 | 內容 | 依賴 | 預期 LoC |
|------|------|------|------|---------|
| **P1-T1** | 撤 cc ProbeProfile impl + test 整檔 | 刪 `internal/agent/cc/probe_profile.go` (19 行) + `_test.go` (26 行) | — | -45 |
| **P1-T2** | 撤 ProbeProfileProvider/ProbeProfile + startWatch signature 改 + orchestrator_test 改 (含 P1-T2a) | provider.go (-21) / probe_orchestrator.go startWatch 改吃 opts (-15/+10) / 撤 defaultProbeProfile (-10) / orchestrator_test 撤 OR1+OR2 + 改 FX4 (-55/+30) | P1-T1 | -91/+40 |
| └─ P1-T2a | (compile-driven sub of T2) probe_orchestrator_test.go 撤 L757-810 OR1/OR2 + 改 FX4 (L507) reshape | 同 commit 整合進 P1-T2 | — | (含於 P1-T2) |
| **P1-T3** | 改 manageActivityWatch + renameSessionLocked stop-only + integration_test + handler_test (含 P1-T3a/P1-T3b) | module.go: 撤 shouldWatchActivity / 改 manageActivityWatch / 改 renameSessionLocked / 重寫 doc comment / integration_test 撤 OR1 + reshape CC4 + 改 rename / handler_test 三處反向 | P1-T2 | -100/+120 |
| └─ P1-T3a | (compile-driven sub of T3) probe_orchestrator_integration_test.go 撤 OR1 (L40-101) + reshape CC4 (L209+) + 改 rename (L260-300, L325-410) | 同 commit 整合進 P1-T3 | — | (含於 P1-T3) |
| └─ P1-T3b | (compile-driven sub of T3) handler_test.go 三處 activeWatchers["work"] (L1157/L1188/L1694) 斷言反向 | 同 commit 整合進 P1-T3 | — | (含於 P1-T3) |
| **P1-T4** | 新增 module_test.go positive coverage（per round-2 R2-06 改編號自原 P1-T7） | 3 新 test：TestManageActivityWatch_DefaultNoOp / StopsExistingWatcher / TestRenameSessionLocked_StopOnly | P1-T3 | +85 |

### 3.2 Phase 1 commit 順序與 compile dependency

```
P1-T1: 純 deletion（cc/probe_profile.go 撤）
        → go build daemon ❌ provider.go 還宣告 ProbeProfileProvider/ProbeProfile，但無 cc impl 不會 break
        → go test ./internal/agent/cc/... ✅（無剩餘 ProbeProfile 相關 test）
        → 但 go test ./internal/module/agent/... 仍綠（既有 OR1/OR2 用 fake provider 不依 cc impl）
        commit ✅

P1-T2 (含 P1-T2a)：撤 provider.go ProbeProfileProvider/ProbeProfile + 撤 defaultProbeProfile + startWatch 改吃 opts
        → 同 commit 同步改 probe_orchestrator.go startWatch caller path
        → 同 commit 改 orchestrator_test (P1-T2a) 撤 OR1/OR2 改 FX4 — 因 type 撤後既有 OR1/OR2 compile 失敗，必須整合
        → 單一 commit `P1-T2`：撤 provider 三型別 + 改 orchestrator + 改 orchestrator_test

P1-T3 (含 P1-T3a + P1-T3b)：manageActivityWatch + renameSessionLocked + shouldWatchActivity 撤
        → 同 commit 改 integration_test (P1-T3a) 撤 OR1 + reshape CC4 + 改 rename
        → 同 commit 改 handler_test (P1-T3b) 三處 activeWatchers 斷言反向
        → 單一 commit `P1-T3`：caller 改 + integration_test 改 + handler_test 改

P1-T4: 新增 module_test.go positive coverage（TestManageActivityWatch_DefaultNoOp 等 3 test）
        → 獨立 commit
```

**最終 Phase 1 commit chain**（4 個 commit，per round-2 R2-06）：

| Commit | 含 task | 主題 |
|--------|---------|------|
| 1 | P1-T1 | 撤 cc/probe_profile.go + test 整檔 |
| 2 | P1-T2 (含 P1-T2a) | 撤 ProbeProfileProvider/ProbeProfile + startWatch 改吃 opts + orchestrator_test 撤 OR1/OR2 改 FX4 |
| 3 | P1-T3 (含 P1-T3a + P1-T3b) | 改 manageActivityWatch/renameSessionLocked stop-only + integration_test 撤 OR1 + reshape CC4 + 改 rename + handler_test 三處反向 |
| 4 | P1-T4 | 新增 module_test.go 三個 positive coverage test |

### 3.3 Phase 1 各 task 詳情

#### P1-T1 撤 cc ProbeProfile impl + test 整檔

**檔案**：
- 撤 `internal/agent/cc/probe_profile.go`（整檔 19 行）
- 撤 `internal/agent/cc/probe_profile_test.go`（整檔 26 行）

**TDD step**：
```bash
git rm internal/agent/cc/probe_profile.go internal/agent/cc/probe_profile_test.go
go build ./...                           # 預期 ✅（無 caller 強相依 cc 的 ProbeProfile()）
go test ./internal/agent/cc/...          # 預期 ✅
git commit -m "refactor(agent): W3 P1-T1 remove cc ProbeProfile impl + test"
```

**驗收**：
- 兩檔不存在於 working tree
- go build / go test 全綠

---

#### P1-T2 撤 ProbeProfileProvider/ProbeProfile + startWatch 改 + orchestrator_test 改

**檔案**：
1. `internal/agent/provider.go` — 撤 L275-295（ProbeProfileProvider interface + ProbeProfile struct）
2. `internal/module/agent/probe_orchestrator.go`：
   - 撤 L21-28（defaultProbeProfile + R9 fix comment）
   - L137-174 startWatch 改：刪 L144-151 type-assert + provider.(ProbeProfileProvider)，新 signature 接 `opts probe.WatchOptions`，內部直接用 opts
   - 重寫 startWatch doc comment（撤 R8 fix profile-resolution 描述）
3. `internal/module/agent/probe_orchestrator_test.go`：
   - 撤 OR1 (`TestOrchestrator_StartWatchUsesAgentProfile`，L757-783)
   - 撤 OR2 (`TestOrchestrator_DefaultProfileWhenAgentMissing`，L785-810)
   - 改 FX4（具體 line 以實際檔案為準）：
     ```go
     // 改前（spec 推測）：
     // pp := fakeProvider{...} ; m.registry.Register("cc", pp)
     // ok := m.probeOrch.startWatch("sess", "cc")  // signature: (session, agentType)
     // assert ok == false
     //
     // 改後：
     opts := probe.WatchOptions{TopLines: 5, BottomLines: 5}
     ok := m.probeOrch.startWatch("sess", "cc", opts)
     assert.False(t, ok)
     // dev log capture: assert "[probe] startWatch invalid opts" present
     ```

**TDD step（compile-driven，必須三 file 同 commit；中間步驟 build/test 都 fail，git bisect 看的是最終 commit）**：
```bash
# 三檔同步改（順序不重要，但都要在 commit 之前完成）：
#   - probe_orchestrator_test.go: 撤 OR1/OR2 + 改 FX4 吃新 signature (session, agentType, opts)
#   - provider.go: 撤 ProbeProfileProvider interface + ProbeProfile struct
#   - probe_orchestrator.go: 撤 defaultProbeProfile + type-assert + startWatch signature 改吃 opts
# 中間任一步單獨改都會 compile fail：
#   - 只改 _test.go: FX4 用三參數 signature 但 production 仍二參數 → fail
#   - 只改 provider.go: orchestrator.go type-assert 找不到 type → fail
#   - 只改 orchestrator.go: provider.go 還宣告 type 但 unused → vet warn
# 全部改完才驗證：
go build ./...                                     # compile pass
go test ./internal/module/agent/... -run Probe   # FX4 pass / OR1 OR2 不存在
go vet ./...                                       # 0 warning
git commit -am "refactor(agent): W3 P1-T2 drop ProbeProfileProvider abstraction; startWatch takes WatchOptions"
```

**驗收**：
- `provider.go` grep `ProbeProfile` 0 hit
- `probe_orchestrator.go` grep `ProbeProfileProvider\|defaultProbeProfile` 0 hit
- `startWatch` signature 為 `(session, agentType string, opts probe.WatchOptions) bool`
- `go test ./internal/module/agent/...` 全綠
- 既有保護機制（graceWindow / Error guard / stale-callback / transition gate）測試（OR3/OR4/OR5/FX1-FX3/FX5）全綠

---

#### P1-T3 改 manageActivityWatch + renameSessionLocked stop-only + integration_test + handler_test

**檔案**：
1. `internal/module/agent/module.go`：
   - 撤 `shouldWatchActivity`（L525-532）
   - 改 `manageActivityWatch`（L490-523）：撤 `if shouldWatchActivity(...) { startWatch }` 區塊，留 stop-only path
   - 重寫 `manageActivityWatch` doc comment：說明 W3 後 default no-op，W6 caller 直接 startWatch
   - 改 `renameSessionLocked`（L267-303）：撤 L289-294 startWatch + rollback 區塊；保留 stopWatch (L288) + delete activeWatchers (L280) + migrateLastHookAt (L301)；新 doc comment 說明 W3 後 stop-only
2. `internal/module/agent/probe_orchestrator_integration_test.go`：
   - 撤 OR1 integration（L40-101，整個 `TestOrchestrator_ManageActivityWatchEvictsOnStatusOff` 假設 always-on）
   - **Reshape CC4 `TestCC_E2E_ScreenChangedToRunning`（L209+）**：撤 L218 `m.manageActivityWatch("work", "cc", agentpkg.StatusWaiting)`；改成 `m.activeWatchers["work"] = "cc"` 手動 seed + 直接呼叫 `m.probeOrch.startWatch("work", "cc", probe.WatchOptions{TopLines: 12})` 模擬 W6 caller；保留 ScreenChanged → Running broadcast 主測試骨架（L228+ Subscribe / L232+ callback / WS broadcast 全部不動）
   - 改 rename test（L260-300 + L325-410 兩段）：
     - 改前：rename 後 `m.activeWatchers["newname"]` 期 active
     - 改後：rename 後 `m.activeWatchers["newname"]` 不存在 + stopWatch +1 + migrateLastHookAt 仍轉移 graceWindow
   - 保留 OR3/OR4/OR5/FX1-FX3/FX5 全套 graceWindow / Error guard / stale-callback / transition gate / interruptBeforeFinalLockFn 測試
3. `internal/module/agent/handler_test.go`：
   - 三處 `_, watching := m.activeWatchers["work"]` (L1157/L1188/L1694) — 將原本「after hook handle X status, expect watching == true」改為「expect watching == false」（W3 後 manageActivityWatch 不 start watcher）
   - 同步檢視 surrounding context 確認測試名稱與意圖（如 `TestHandle_*ActivatesWatcher` 改名 `*StopsWatcher` 或撤該測；以實際檔案為準）

**TDD step**：
```bash
# 1. 先改 integration_test + handler_test （測試先反向）— compile 仍 ok 但 fail
go test ./internal/module/agent/... -run "Test.*Activity|Test.*Rename|TestHandle_.*"  # 預期 fail
# 2. 改 module.go（撤 shouldWatchActivity + 改 manageActivityWatch + 改 renameSessionLocked）
go test ./internal/module/agent/... -run "Test.*Activity|Test.*Rename|TestHandle_.*"  # 預期 pass
go test ./...                                                                            # 全綠
go vet ./...
git commit -am "refactor(agent): W3 P1-T3 manageActivityWatch default no-op; rename stop-only"
```

**驗收**：
- `module.go` grep `shouldWatchActivity` 0 hit
- `manageActivityWatch` 不再呼叫 `startWatch`
- `renameSessionLocked` 不再呼叫 `startWatch`，仍呼叫 `stopWatch` + `migrateLastHookAt`
- integration_test rename test：rename 後 newname 不在 activeWatchers
- handler_test 三處：valid hook 後 activeWatchers 不存在
- `go test ./...` 全綠

---

#### P1-T4 新增 module_test.go positive coverage

**檔案**：`internal/module/agent/module_test.go`（新增 3 test，可能放在既有檔案或新檔 — 以既有 module_test.go 是否已存在判斷）

**新測試**：
1. `TestManageActivityWatch_DefaultNoOp`：
   - 五個 status (waiting/running/idle/error/clear) 各跑一次 manageActivityWatch
   - fake prober 觀察 Watch 呼叫 0 次
   - StopWatch 視 input state 而定（無 active watcher 則不呼叫；spec 確認此細節）

2. `TestManageActivityWatch_StopsExistingWatcher`：
   - 手動設 `m.activeWatchers["sess"] = "cc"`
   - 呼叫 manageActivityWatch("sess", "cc", StatusIdle)
   - 驗 stopWatch +1（fake prober StopWatch 計數）
   - 驗 m.activeWatchers["sess"] 已 delete

3. `TestRenameSessionLocked_StopOnly`：
   - 手動設 `m.activeWatchers["oldname"] = "cc"` + `recordHookAt("oldname")` 記 graceWindow
   - 呼叫 renameSessionLocked("oldname", "newname")
   - 驗：
     - oldname 從 activeWatchers 消失
     - newname 不在 activeWatchers
     - stopWatch +1
     - migrateLastHookAt 已轉：用 orchestrator graceMu 直接讀 lastHookAt["newname"] 應有 timestamp

**TDD step**：
```bash
# 1. 先寫 3 test 檔（測試先；此時可能無對應 helper 但 test 應該會 fail since 行為已被 P1-T3 改）
go test ./internal/module/agent/... -run "TestManageActivityWatch_|TestRenameSessionLocked_"  # 預期 pass（行為已對）
go test ./...                                                                                    # 全綠
go vet ./...
git commit -am "test(agent): W3 P1-T4 positive coverage for default-no-op + rename stop-only"
```

**驗收**：
- 3 個新 test 全 pass
- 既有 test 未受影響

### 3.4 Phase 1 mlab live verify（在主 repo 內，不在 worktree 跑）

per spec §4.2 §1-§7 PASS criteria：

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-w3-w4
go build -o /tmp/pdx ./cmd/pdx                                  # ✅ 編譯通過
# 在 mlab：
killall pdx 2>/dev/null
PDX_DEV_MODE=1 /tmp/pdx serve > /tmp/pdx-w3-p1.log 2>&1 &
sleep 2
# 跑 cc UserPromptSubmit 一次
/tmp/pdx hook --agent cc UserPromptSubmit ...                   # broadcast normalized event
/tmp/pdx hook --agent cc Stop ...                               # broadcast status=idle
# 驗證
grep '\[probe\] startWatch\|\[probe\] status reason=screen-\|\[probe\] graceWindow suppress' /tmp/pdx-w3-p1.log
# 預期 0 hit
curl -s http://100.64.0.2:7860/debug/vars | jq '.purdex_probe_watch_started_total, .purdex_probe_screen_event_total, .purdex_probe_grace_window_suppressed_total'
# 預期三個都是 0
# 跑 codex / opencode 同樣 hook 各一次驗 broadcast 正常
```

**Phase 1 PASS 條件**（per spec §6 部分）：
- ✅ R1-R12 撤回清單全部完成（grep 0 hit）
- ✅ Go test 22 packages 全綠
- ✅ Go vet 0 warning
- ✅ mlab live verify §1-§7 PASS

---

## 4. Phase 2 — W4 dev log 跨層覆蓋

### 4.1 Phase 2 task list

| Task | 名稱 | 內容 | 依賴 | 預期 LoC |
|------|------|------|------|---------|
| P2-T1 | 在 handler.go HandleEvent / handleHook 入口補 `[hook]` trigger log | hook trigger 1 條 / hook | — | +20 |
| P2-T2 | DeriveStatus 出口補 `[derive]` verify_passed/skipped log | 三家（cc/codex/opencode）DeriveStatus return 之後在 handler 統一一條（避免每家重複） | P2-T1 | +25 |
| P2-T3 | 補 `[handler] frame_apply` log | handler.go:286 trace.Frame 後 1 條 | P2-T1 | +15 |
| P2-T4 | 補 `[handler] projection_built` log | handler.go:298 trace.Projection 後 1 條 | P2-T1 | +15 |
| P2-T5 | 補 `[broadcast]` log | **handler.go:324** trace.Emit (SubagentStart updated_frame) + **handler.go:388** trace.Emit (main valid path) 兩處各 1 條，含 `has_clients=bool` (用 `EventsBroadcaster.HasSubscribers()`) + `decision/reason` (從 `emitHookToSession` 回傳) | P2-T1 | +30 |
| P2-T6 | 補 `[handler] invalid_skip` log | **handler.go:230-248** catalog miss `if !result.Valid` 區塊 + **handler.go:310-318** SubagentStart/Stop frame_missing/subagent_id_missing 早 return | P2-T1 | +20 |

### 4.2 Phase 2 commit 順序

每 task 獨立 commit（純加 log line + isDevMode gate；不互相依賴 compile）：

```
1 | P2-T1 [hook] trigger
2 | P2-T2 [derive] verify_passed/skipped
3 | P2-T3 [handler] frame_apply
4 | P2-T4 [handler] projection_built
5 | P2-T5 [broadcast]
6 | P2-T6 [handler] invalid_skip
```

P2-T1 是先決條件（chain_id 取得來源；後續 5 個 task 都依賴它取 chain_id 帶在 log 內），可獨立 commit。

### 4.3 Phase 2 共通 conventions

**dev log helper**（per spec §2.5 MUST 約束 + F7 verifiable 約束）：
- 不抽 helper（避免 W2 P2 cleanup 那種「helper 重複 cost 但測試覆蓋足夠」討論）
- 每行統一格式 `if isDevMode() { log.Printf("[xxx] kind key1=v1 key2=v2 ...", ...) }`
- 整個 `log.Printf` 與其參數 fmt 必須在 `isDevMode()` gate 內 — codex round-1 F7 已要求
- chain_id 從 hook trigger 路徑下傳：handler.HandleEvent 取得 chain_id 後在 ctx 或 local var 帶到後續 trace.Frame/Projection/Emit 對應的 dev log

### 4.4 Phase 2 各 task TDD pattern

每 task 共用 TDD 模式：

```bash
# 1. 先寫 test：handler_test.go 新增 TestHandler_DevModeLog_<Step>
#    用 t.Setenv("PDX_DEV_MODE", "1") + capture log
#    跑一個對應路徑的 hook event
#    grep log.String() 對 "[xxx] kind ..." 期待 substring
go test ./internal/module/agent/... -run "TestHandler_DevModeLog_<Step>"  # 預期 fail
# 2. 補 production log line
go test ./internal/module/agent/... -run "TestHandler_DevModeLog_<Step>"  # 預期 pass
# 3. 跑完整 suite + 跑 production-mode 對照
go test ./...
go vet ./...
git commit -am "feat(observability): W4 P2-Tx [<tag>] <kind> dev log"
```

**Production-mode 對照測試**（每 task 都附）：
- `t.Setenv("PDX_DEV_MODE", "0")` + 同 hook → 驗 log 不含新標籤
- 在 P2-T1 第一個 task 順手新增 `TestHandler_NoDevModeLog_Production` 整體 negative test，後續 task 不重複

### 4.5 Phase 2 mlab live verify

per spec §4.2 §8-§12 PASS criteria：

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-w3-w4
go build -o /tmp/pdx ./cmd/pdx
# 在 mlab
killall pdx 2>/dev/null
PDX_DEV_MODE=1 /tmp/pdx serve > /tmp/pdx-w3-p2.log 2>&1 &
sleep 2
# 跑 cc UserPromptSubmit
/tmp/pdx hook --agent cc UserPromptSubmit ...
# 觀察 5 條連貫 log
tail -50 /tmp/pdx-w3-p2.log | grep -E '\[hook\]|\[derive\]|\[handler\]|\[broadcast\]'
# 預期：
# [hook]      trigger session=X agent=cc purdex_name=PdxUserPromptSubmit chain_id=...
# [derive]    verify_passed agent=cc purdex_name=PdxUserPromptSubmit status=running reason=
# [handler]   frame_apply session=X frame_id=... lifecycle=PdxUserPromptSubmit decision=updated_frame
# [handler]   projection_built session=X top_status=running tabs=... codes=...
# [broadcast] session=X has_clients=true decision=delivered reason=... raw_event_name=PdxUserPromptSubmit chain_id=...

# 跑 invalid catalog miss
/tmp/pdx hook --agent cc BogusEvent ...
tail -20 /tmp/pdx-w3-p2.log | grep -E '\[hook\]|\[derive\]|\[handler\]'
# 預期：
# [hook]      trigger session=X agent=cc purdex_name=BogusEvent chain_id=...
# [derive]    skipped agent=cc reason=event_not_in_catalog
# [handler]   invalid_skip reason=event_not_in_catalog
# 無 frame/projection/broadcast log

# 跑 SubagentStart updated_frame 路徑（hook 帶完整 frame_id + subagent_id）
/tmp/pdx hook --agent cc SubagentStart ...
# 預期：trigger/verify/frame/projection/broadcast 5 step 全有（per handler.go:298 projection 在 subagent branch 之前 + handler.go:324 updated_frame 走 emitHookToSession + trace.Emit）

# 跑 SubagentStart frame_missing 路徑（合成 missing frame_id 的 request）
/tmp/pdx hook --agent cc SubagentStart ... # missing frame
# 預期：trigger/verify/frame 有；**projection 跑過 + emit skip**（per handler.go:310-318 frameMeta.Decision != "updated_frame" 早 return）；無 broadcast log

# 切 PDX_DEV_MODE=0 重啟
killall pdx
/tmp/pdx serve > /tmp/pdx-prod.log 2>&1 &
/tmp/pdx hook --agent cc UserPromptSubmit ...
grep -E '\[hook\]|\[derive\]|\[handler\]|\[broadcast\]' /tmp/pdx-prod.log
# 預期 0 hit
grep '\[agent\]' /tmp/pdx-prod.log
# 預期 error path 仍可能有（既有 9 條保留）
```

**Phase 2 PASS 條件**：
- ✅ §2.3 P1+P2 dev log 行全部就位
- ✅ Phase 1 全部驗收條件仍滿足（regression-free）
- ✅ mlab live verify §8-§12 PASS（5 條 chain log 連貫 + chain_id 對得上）

---

## 5. Phase 3 — W4 TraceStore step gap audit

### 5.1 Phase 3 task list

| Task | 名稱 | 內容 | 依賴 | 預期 LoC |
|------|------|------|------|---------|
| P3-T1 | 跑 audit script 取 hook 路徑 × trace step 矩陣 | mlab 實機跑 8 條 hook 路徑，撈 TraceStore record，產 coverage matrix | P2 全部 ship | doc only |
| P3-T2 | 寫 audit 結果 doc 進 spec 或新 followup doc | 確認每條 hook chain 5 step 覆蓋 / 列「合理缺步」case + reason | P3-T1 | +30~50 |
| P3-T3 | 視 audit 結果決定是否開 follow-up issue | 如發現需新 trace step → 開 issue（不阻擋 PR-4） | P3-T2 | issue only |

### 5.2 Audit 路徑清單（per spec §3.3 PR-4 必含 scope）

8 條 hook 路徑要驗：

| # | 路徑 | 預期 5-step coverage |
|---|------|----------------------|
| 1 | cc valid main（UserPromptSubmit/Stop） | trigger/verify/frame/projection/emit 全有 |
| 2 | cc invalid catalog miss（BogusEvent） | trigger/verify(skipped) 有；frame/projection/emit 不有（合理） |
| 3a | cc subagent SubagentStart/Stop **updated_frame** | trigger/verify/frame/projection/emit 5 step 全有（projection 在 subagent branch 之前；updated_frame 走 emitHookToSession） |
| 3b | cc subagent SubagentStart/Stop **frame_missing/subagent_id_missing 早 return** | trigger/verify/frame/projection 有；emit 不有（per handler.go:310-318 早 return；audit §3.2 對應） |
| 4 | cc SessionEnd（status=clear） | trigger/verify/frame/projection/emit 全有 |
| 5 | replay-from-DB（snapshot/cold reconnect） | trigger 是否有？需驗 |
| 6 | codex valid main | 同 cc valid main |
| 7 | opencode plugin event（session.idle 等） | trigger 起點是 daemon HTTP POST 不是 CLI hook，但仍應走 5 step |
| 8 | opencode legacy fallback path（W2 仍保留的 isLegacyHookForUnmigrated 路徑） | 5 step 全有 |

**audit 工具**：寫小 Go test 跑 in-memory daemon + 三家 fixture，撈 TraceStore record 比對；無需改 production code。

### 5.3 Phase 3 commit

通常一個 commit 即可：
- `docs(specs): W4 P3 trace step coverage audit`

如有發現 gap 需補測試（如 replay-from-DB 沒進 trigger）：
- 第二 commit `test(observability): W4 P3 add replay path trace step coverage` (~30 LoC)

如有 schema 級需求（如新 trace kind）：
- 不在本 PR；開 follow-up issue 於 GitHub

### 5.4 Phase 3 PASS 條件

- ✅ audit doc 涵蓋 8 條路徑 × 5 step 矩陣
- ✅ 「合理缺步」case 有 doc 解釋進 trace.go package comment
- ✅ 如有 schema follow-up issue，issue # 已在 PR description 引用

---

## 6. Risk & Mitigation matrix

| ID | Risk | 嚴重 | 信心 | Mitigation |
|----|------|------|------|------------|
| MR1 | 撤掉 OR1/OR2 後 startWatch invalid-opts 校驗測試覆蓋率不足 | medium | high | P1-T2 改 FX4 + P1-T4 新 module_test + codex round 1 review 抓漏 |
| MR2 | renameSessionLocked stop-only 改造後 graceWindow 不再正確轉移 | medium | medium | P1-T4 TestRenameSessionLocked_StopOnly 顯式驗 migrateLastHookAt 仍轉；OR4/OR5 既有 graceWindow 測試走過一輪 |
| MR3 | W4 dev log 加在 hot path 衝擊性能（per F7） | low | low | spec §5 R4 + spec §2.5 MUST 約束 + **diff-scoped 機械驗證**：`git diff origin/main...HEAD` 中新增的 `[hook]` / `[derive]` / `[handler]` / `[broadcast]` log block 必須整段在 `if isDevMode()` gate 內；既有 `[agent]` / `[agent][trace]` / `[probe]` 不在驗證範圍 |
| MR4 | issue #719 always-on residue 沒在 mlab log 完整消失 | medium | medium | Phase 1 verify §5 三類 [probe] 訊息必 0 hit；非 0 視為 P1 finding |
| MR5 | 並發 session 寫主 repo 期間 worktree 被污染 | low | high | conventions §1.4 + feedback `concurrent_session_safety` |
| MR6 | codex round-1 不讀 spec, 建議違反 spec 設計（如建議重新引入 ProbeIntent interface） | medium | high | feedback `codex_pr_review_spec_alignment`；PR review round-2 防守視角必派 |
| MR7 | Phase 2 dev log 量太大、PDX_DEV_MODE=1 跑 console 噴 | low | medium | spec §5 R2；每 hook 5 條 + 每秒最多 ~10 hook = ~50 line/sec；可接受 |
| MR8 | Phase 3 audit 發現需要動 TraceStore schema | medium | low | spec §3.3 已 scope-out；開 follow-up issue 不阻擋 PR-4 |
| MR9 | spa baseline 4 既有 vitest fail count 改變 | low | medium | pre-flight 檢查；W3+W4 不動 SPA，count 應不變 |
| MR10 | rename test 改造後 old/new naming 假設失準（如 newname 仍應 active 但被改沒） | medium | medium | 先讀完整 rename test context（L260-410）再改；P1-T3 commit 前 single-test 跑驗 |

---

## 7. Definition of Done per phase

### 7.1 Phase 1 DoD

- ✅ 4 個 commit（P1-T1/T2/T3/T4）all push
- ✅ `git diff origin/main..HEAD --stat` net negative LoC（撤回應為 -100~150 lines）
- ✅ Pre-flight checks 全部仍 pass（go test / go vet / spa baseline）
- ✅ mlab live verify §1-§7 PASS（記錄於 PR description）
- ✅ §1.1 R1-R13 撤回清單 grep 0 hit
- ✅ 既有保護機制（graceWindow / Error guard / stale-callback / transition gate）OR3/OR4/OR5/FX1-FX3/FX5 全綠

### 7.2 Phase 2 DoD

- ✅ 6 個 commit（P2-T1 ~ P2-T6）all push
- ✅ Phase 1 DoD 全部仍滿足
- ✅ mlab live verify §8-§12 PASS（5 條 chain log 連貫 + chain_id 對得上 + production-mode 對照無新標籤）
- ✅ codex review 對 `git diff origin/main...HEAD` 中**新增**的 `[hook]` / `[derive]` / `[handler]` normal-path / `[broadcast]` log block 機械驗證：每段都在 `if isDevMode()` gate 內 + 參數 fmt 也在 gate 內（per spec §2.5 MUST 約束 + §5 R4）；既有 production logs 不在驗證範圍

### 7.3 Phase 3 DoD

- ✅ 1-3 個 commit（audit doc + 必要補測試）
- ✅ Phase 2 DoD 全部仍滿足
- ✅ audit doc 涵蓋 8 條 hook 路徑 × 5 step 矩陣
- ✅ 如需 schema 變更，follow-up issue 已開且 PR description 引用

### 7.4 PR-4 整體 DoD

- ✅ 三 phase 全部 commit chain push
- ✅ PR description 完整（test plan checklist + Phase 1/2 mlab evidence + Phase 3 audit summary + closes #719 + 已知 W5/W6 觀察期 regression caveat）
- ✅ codex round-1 標準 review + round-2 三平行 adversarial 收斂無 critical / P1
- ✅ squash merge 進 main
- ✅ bump PR alpha.257 開 + merge
- ✅ memory `kickoff_lights_rebuild.md` 更新觸發詞改為「W5 燈號 bug」+「W6-3 first ad-hoc ProbeIntent」
- ✅ worktree `lights-w3-w4` 清理（留待 user 手動 — 同 W2 經驗）

---

## 8. Hand-off & memory updates

PR-4 ship 後：

| 觸發 | 內容 |
|------|------|
| 觸發詞「啟動 W5 燈號 bug」 | per audit §6 8 條 W5 條目，視 W6 對應補位 ship 進度逐條啟動 PR |
| 觸發詞「啟動 W6 / W6-3 codex error first ProbeIntent」 | 進入 W6 phase；finalize ProbeIntent interface；audit §7.2 推薦順序 |
| 觸發詞「啟動 W7 Dev Inspector UI」 | 等 W6 大致 stabilize 後 |

memory updates：
- `kickoff_lights_rebuild.md` 標 PR-4 ship 完成 + 更新 phase roadmap
- `project_progress.md` 對應 alpha.257
- 解掉 `kickoff_lights_phase_4a_1_impl.md` 殘留 framework 描述（如有）

---

## 9. References

- **Spec**: `docs/specs/2026-04-29-lights-w3-w4-revert-and-observability-spec.md`
- **Fix-spec**: `docs/specs/2026-04-28-lights-rebuild-fix-spec.md`
- **W1 audit**: `docs/specs/2026-04-28-hook-status-audit-spec.md`
- **W2 spec/plan**: `docs/specs/2026-04-28-catalog-naming-separation-{spec,plan}.md`
- **既有 PR-4a-1 plan**: `docs/specs/2026-04-27-lights-rebuild-phase-4a-1-plan.md`（撤回對象 framework 部分）
- **issue**: #719 (close on merge) / #698 (W6 reassess) / #717 (W6 scope)
