# Agent Identity / Liveness 收斂實作 Plan

> 日期：2026-04-20（v2 — 依 spec v2 更新）
> 狀態：Ready for Codex Execution
> Spec：`docs/specs/2026-04-20-agent-identity-and-liveness-convergence-design.md`
> 執行者：Codex（透過 `/codex:rescue` 或 codex-companion 委派）
> 關聯：issue #487、PR #484（保留）、PR #486（not-ship，被新實作取代）

---

## 執行規範

### 基本規則

每個 Phase 都走完整流程，**一次只做一個 Phase**：

1. 從 `origin/main` 最新版開新 worktree / feature branch（不重用 `feat/agent-watch-alive`）
2. TDD：先寫 failing tests，再實作到 tests pass
3. 每個 task 獨立 commit
4. 完成 Phase 後開 feature PR
5. 依 CLAUDE.md 的 PR Review 兩輪制（Codex 主審 + CC 收尾彙整）跑完
6. 問題彙整表格後優先修高關聯/高信心/低複雜項目；不修的開 issue 追蹤
7. Merge 後 bump VERSION + CHANGELOG.md 開 PR，merge bump PR
8. 進入下個 Phase 前驗證本 Phase 所有驗收點

### 技術規範（遵守 CLAUDE.md）

- 絕對不能直推 main
- Go：`gofmt ./...`、`make test`、`make lint`
- SPA：`cd spa && pnpm run lint && npx vitest run`
- 每個 PR merge 後都要 bump VERSION（同步 `package.json` + `spa/package.json`）

### Codex 執行格式（每個 Phase 的啟動 prompt）

```
依據 docs/specs/2026-04-20-agent-identity-and-liveness-convergence-design.md 與
docs/superpowers/plans/2026-04-20-agent-identity-and-liveness-convergence.md 的 Phase N。

從最新 origin/main 開新 worktree / feature branch。
固定流程：TDD → implement → PR → 兩輪 review → merge → bump PR → merge。
一次只做這一個 Phase，完成後停下讓我確認再進下一個。
```

---

## Phase 0：基線凍結 & 回歸清單

### 目標

建立工作基線，明確 `#484` 保留、`#486` 不出貨、`feat/agent-watch-alive` 參考用。建立手動回歸清單。

### 工作

1. 在 issue #487 加 comment 標記：
   - `#484` = keep
   - `#486` / `feat/agent-watch-alive` = reference-only，不直接沿用
2. 建立 `docs/testing/agent-identity-regression-checklist.md`（新檔），包含 spec §9 的 14 個手動驗證情境 + checkbox
3. 無 code 改動

### 驗收

- [ ] issue #487 有 comment 註記狀態
- [ ] regression checklist 建檔
- [ ] 走小型 docs PR；不直接 push / merge 到 main

### 預估工作量

0.5 天

---

## Phase 1：Hook Payload Schema v2 + pdx hook PID 解析

### 目標

擴充 hook payload 帶 provenance 欄位，讓 daemon 後續能驗證 claim。**核心難點：pdx hook 正確解析出 agent 本身的 PID（不是 pdx 自己的 PID），否則 Q3 驗證會永遠過，B2 原 bug 沒修**。

### 範圍

- `pdx hook` CLI 實作祖先鏈向上 walk 找 agent PID
- 新 payload 欄位：`tmux_pane_id`、`sender_pid`、`sender_start_time`、`sender_uncertain`
- Daemon ingest 解析新欄位但**暫不使用**（Phase 3 才啟用驗證）
- 舊 payload **直接拒絕**（alpha 階段無相容）

### 檔案改動清單

| 檔案 | 動作 | 說明 |
|------|------|------|
| `cmd/pdx/hook.go` | 修改 | 填充 provenance；實作 resolveAgentPid() |
| `cmd/pdx/hook_pid_resolver.go` | 新增 | 祖先鏈 walk + shim 跳過邏輯 |
| `cmd/pdx/hook_pid_resolver_test.go` | 新增測試 | |
| `cmd/pdx/hook_test.go` | 新增測試 | pdx hook 端到端 |
| `internal/module/agent/handler.go` | 修改 | `EventRequest` struct 加新欄位；缺欄位回 400 |
| `internal/module/agent/handler_test.go` | 新增測試 | |

### pdx hook PID 解析規則

```go
// hook_pid_resolver.go
var knownShims = map[string]bool{
    "sh": true, "bash": true, "zsh": true, "dash": true, "fish": true,
    "npx": true, "yarn": true, "pnpm": true, "env": true,
}

// resolveAgentPid starts from os.Getppid() and walks up the process tree,
// skipping known shim layers, until finding the agent process.
func resolveAgentPid(startPpid int) (pid int, uncertain bool) {
    current := startPpid
    for current > 1 {
        info, err := readProcessInfo(current)
        if err != nil {
            return current, true
        }
        if !knownShims[filepath.Base(info.ExePath)] {
            return current, false
        }
        current = info.PPID
    }
    return 1, true  // reached init, return uncertain
}
```

### TDD 任務

1. **Test 1**：`TestResolveAgentPid_DirectParent`
   - pdx 直接由 agent 啟動 → ppid 就是 agent PID
2. **Test 2**：`TestResolveAgentPid_SkipsShell`
   - pdx 由 `sh -c "pdx hook ..."` 啟動 → 跳過 sh，回 agent PID
3. **Test 3**：`TestResolveAgentPid_SkipsNpx`
   - pdx 由 `npx pdx hook` 啟動 → 跳過 npx
4. **Test 4**：`TestResolveAgentPid_SkipsMultipleShims`
   - pdx 由 `bash -c "npx pdx hook"` → 跳過兩層
5. **Test 5**：`TestResolveAgentPid_ReachesInit`
   - PPID=1 fallback，回報 uncertain=true
6. **Test 6**：`TestHookCLI_PopulatesProvenance`
   - Fake tmux env + fake parent process，驗證 pdx hook JSON 輸出含正確欄位
7. **Test 7**：`TestHandleEvent_RejectsLegacyPayload`
   - 舊 schema → 400 + 不寫 DB
8. **Test 8**：`TestHandleEvent_AcceptsV2Payload_StillUsesLegacyFlow`
   - 新 schema → ingest 通過（Phase 3 前仍走舊 store 邏輯）

### Implementation 要點

- `resolveAgentPid` 測試必須用 fake process tree（mock `readProcessInfo`），不依賴真實 `os.Getppid()`
- `sender_start_time` 用 `ps -p <agent_pid> -o lstart=` 格式化輸出
- payload 加 `sender_uncertain: true` 時，daemon 下一個 phase 會將其視為 unverifiable 直接拒絕

### 驗收 (DoD)

- [ ] 所有新增 test 通過
- [ ] `TestResolveAgentPid_SkipsShell` + `TestResolveAgentPid_SkipsNpx` 過（確認 shim 跳過正確）
- [ ] `go test ./... -count=1` 全綠
- [ ] `go vet ./...` 無 warning
- [ ] Codex `/codex:review` 無 high severity
- [ ] PR 兩輪 review 完成
- [ ] Merge + bump

### 預估工作量

1.5 天（比 v1 多 0.5 天，因 PID 解析邏輯新加）

---

## Phase 2：ProcessInfo + Provider Identify

### 目標

新增 system-level process query 能力，各 provider 實作 `Identify(ProcessInfo) bool`。取代 `RegisterProcessNames` + `matcher.commands` 的字串比對機制。

### 範圍

- 新增 `ProcessInfo` 結構（含 PID/PPID/ExePath/Argv/StartTime）
- macOS/Linux 雙平台實作（避開 macOS `ps -o comm=` 的 16 字元截斷）
- Symlink 處理（EvalSymlinks）
- `AgentProvider` interface 加 `Identify`
- cc / codex provider 實作 Identify
- **不動**既有 `IsAliveFor` 呼叫點（Phase 5 才接新流）

### 檔案改動清單

| 檔案 | 動作 | 說明 |
|------|------|------|
| `internal/agent/process_info.go` | 新增 | ProcessInfo struct + Read(pid) |
| `internal/agent/process_info_darwin.go` | 新增 | macOS 實作 |
| `internal/agent/process_info_linux.go` | 新增 | Linux 實作 |
| `internal/agent/process_info_darwin_test.go` | 新增測試 | |
| `internal/agent/process_info_linux_test.go` | 新增測試 | |
| `internal/agent/provider.go` | 修改 | `AgentProvider` interface 加 `Identify(ProcessInfo) bool` |
| `internal/agent/cc/provider.go` | 修改 | 實作 Identify |
| `internal/agent/codex/provider.go` | 修改 | 實作 Identify |
| `internal/agent/cc/provider_test.go` | 新增測試 | Identify 各情境 |
| `internal/agent/codex/provider_test.go` | 新增測試 | Identify 各情境 |

### 平台實作

| 平台 | ExePath | Argv | StartTime |
|------|---------|------|-----------|
| macOS | `ps -p PID -o comm=`（再做 path normalize / symlink resolve） | `ps -p PID -o args=` 全部 | `ps -p PID -o lstart=` |
| Linux | `readlink /proc/PID/exe` + `filepath.EvalSymlinks` | `/proc/PID/cmdline`（null-sep）| `ps -p PID -o lstart=` 或 `/proc/PID/stat` 第 22 欄 |

**備註**：macOS 目前實作以 `comm=` 取得 executable、以 `args=` 還原 argv；重點是不再信任 `argv[0]` 當成 Identify 依據。

### TDD 任務

1. **Test 1**：`TestProcessInfo_ReadsCurrentProcess`
   - 取 `os.Getpid()` 的 ProcessInfo，驗證 ExePath 和 Argv 非空
2. **Test 2**：`TestProcessInfo_ResolvesSymlinks` ⭐
   - 建 tmp symlink 指向真實 binary，spawn 該 symlink，驗證 ExePath 是真實路徑
3. **Test 3**：`TestCcProvider_Identify_NativeBinary`
   - `ProcessInfo{ExePath: "/usr/local/bin/claude"}` → true
4. **Test 4**：`TestCcProvider_Identify_WithArgv0VersionString` ⭐ spec §8 驗收點 3
   - 模擬 CC 把 argv[0] 改成 `"2.1.114"`：`ProcessInfo{ExePath: "/Users/x/.local/bin/claude", Argv: ["2.1.114", ...]}` → true
   - 注意 Identify 看 ExePath basename，不看 argv[0]
5. **Test 5**：`TestCcProvider_Identify_NodeWrapper` ⭐ spec §8 驗收點 4
   - `ProcessInfo{ExePath: "/usr/bin/node", Argv: ["node", "/lib/@anthropic-ai/claude-code/index.js"]}` → true
6. **Test 6**：`TestCcProvider_Identify_Negative`
   - 純 node 程序 + 不相關 argv → false
   - Codex 程序 → false
7. **Test 7**：`TestCodexProvider_Identify_*`（對稱覆蓋）
8. **Test 8**：`TestProcessInfo_MissingProcess`
   - 取不存在的 PID → 錯誤不 panic

### Implementation 要點

- macOS 先試 cgo `proc_pidpath`（最乾淨），若 build 選項不允許 cgo 則 fallback 到 `ps -p PID -o args=` 取第一個 token
- Linux `readlink /proc/PID/exe` + `filepath.EvalSymlinks` 處理多層 symlink
- `isJSRuntime(bname)` helper 判斷 `node` / `bun` / `deno`
- Provider Identify 內部允許硬編碼自身 binary basename 和 module path pattern，但禁止出現別 agent 的名字

### 驗收 (DoD)

- [ ] 所有新增 test 通過
- [ ] **`TestCcProvider_Identify_WithArgv0VersionString` 必過**（解 spec §8 驗收點 3）
- [ ] **`TestProcessInfo_ResolvesSymlinks` 必過**（解 symlink wrapper）
- [ ] 在 macOS 和 Linux 雙平台 CI 都過（若有）；至少手動在 macOS 上跑一次 integration smoke
- [ ] `go test ./... -count=1` 全綠
- [ ] Codex review 無 high severity
- [ ] PR 兩輪 review + merge + bump

### 預估工作量

2 天（比 v1 多 0.5 天，含 cgo proc_pidpath + symlink 處理）

---

## Phase 3：Verified Hook Ingestion

### 目標

Daemon 收 hook 時跑完整 verify 流：PID 活著 + start_time 一致 + PID 在 claimed pane tree 內（**向上 walk** 祖先鏈）+ provider Identify 一致。驗證失敗的事件拒收。

### 範圍

- `handleEvent` 加同步 verify 步驟
- 拒絕事件 log reason + metric counter（by reason）
- Q3 使用**向上 walk**（從 sender_pid 走 PPID chain 找 pane_pid）
- 既有 store 不動（仍是單列）；Phase 4 才改 store

### 檔案改動清單

| 檔案 | 動作 | 說明 |
|------|------|------|
| `internal/module/agent/verify.go` | 新增 | verify flow 實作 |
| `internal/module/agent/verify_test.go` | 新增測試 | |
| `internal/module/agent/handler.go` | 修改 | ingest 流程插入 verify |
| `internal/module/agent/handler_test.go` | 新增測試 | end-to-end ingest 情境 |
| `internal/agent/probe/liveness.go` | 新增 | `IsPidAlive(pid) bool`（`kill -0`）|
| `internal/agent/probe/liveness.go` | 新增 | `PidAncestorIncludes(pid int, ancestor int) bool`（向上 walk）|
| `internal/agent/probe/liveness.go` | 新增 | `ProcessStartTime(pid) string` |

### Verify 流（7 步，同步執行）

```
1. Schema 完整 → 否則 400 "schema_invalid"
2. kill -0 sender_pid → 否則 202 "pid_dead"
3. ProcessStartTime 一致 → 否則 202 "pid_reused"
4. tmux display pane_pid 成功 → 否則 202 "pane_unresolvable"
5. PidAncestorIncludes(sender_pid, pane_pid) → 否則 202 "pid_not_in_pane_tree"
6. provider.Identify(ProcessInfo) → 否則 202 "identify_mismatch"
7. 通過 → ingest 繼續（Phase 3 仍寫舊 agent_events；Phase 4 換 frames）
```

**向上 walk**（Q3 實作）：

```go
func PidAncestorIncludes(pid int, ancestor int) bool {
    current := pid
    for current > 1 {
        if current == ancestor { return true }
        info, err := readProcessInfo(current)
        if err != nil { return false }
        current = info.PPID
    }
    return false
}
```

### TDD 任務

1. **Test 1**：`TestVerify_AcceptsPaneNativeHook`
   - Fake pane pid=100，agent pid=200 with PPID chain → 100
   - sender_pid=200, pane_id 指向 pid=100 的 pane
   - 通過
2. **Test 2**：`TestVerify_RejectsDetachedRuntime` ⭐ 本 Phase 核心
   - Fake pane pid=100 有 CC 在跑
   - sender_pid=999，PPID=1（detached），不在 pane tree
   - Verify 拒絕 reason=`pid_not_in_pane_tree`
3. **Test 3**：`TestVerify_AcceptsNestedAgent` ⭐ 支援 CC→Codex
   - pane pid=100，CC pid=200（PPID=100），Codex pid=300（PPID=200）
   - sender_pid=300（Codex）走 up：300 → 200 → 100，命中 pane_pid
   - 通過
4. **Test 4**：`TestVerify_RejectsDeadPid`
   - sender_pid 是已死 PID → reason=`pid_dead`
5. **Test 5**：`TestVerify_RejectsPidReuse` ⭐
   - payload.sender_start_time = "A"
   - 實際 ProcessStartTime(pid) = "B"（PID 被重用）
   - 拒絕 reason=`pid_reused`
6. **Test 6**：`TestVerify_RejectsIdentifyMismatch`
   - payload 說 agent_type=cc，但 sender_pid 的 ExePath basename 是 codex
   - 拒絕 reason=`identify_mismatch`
7. **Test 7**：`TestVerify_RejectsPaneUnresolvable`
   - tmux display pane_pid 失敗（模擬 tmux restart）
   - 拒絕 reason=`pane_unresolvable`
8. **Test 8**：`TestVerify_RejectsUncertainSender`
   - payload 有 `"sender_uncertain": true`
   - 直接拒絕 reason=`sender_uncertain`
9. **Test 9**：`TestHandleEvent_RejectedHookDoesNotOverwriteSession`
   - 既有 row 是 cc
   - 送進冒名 codex hook（拒絕）
   - 驗證 DB row 仍是 cc，未被覆蓋

### Implementation 要點

- `IsPidAlive` 用 `syscall.Kill(pid, 0)`（Unix）；err == nil 活著
- `ProcessStartTime` 用 `ps -p PID -o lstart=` 字串比對即可（秒級精度）
- verify 失敗回 202 Accepted + `{"status":"rejected","reason":"..."}`，不是 500
- log 輸出 `[agent][verify] rejected pid=X reason=Y pane=Z`
- 若 verify 本身出 panic/error，回 202 rejected + reason，並記 warning log（fail-closed，避免 detached runtime 混入）

### 驗收 (DoD)

- [ ] **`TestVerify_RejectsDetachedRuntime` 過 — issue #487 核心 bug 修掉**
- [ ] **`TestVerify_AcceptsNestedAgent` 過 — CC→Codex 合法場景不被誤拒**
- [ ] **`TestVerify_RejectsPidReuse` 過 — PID 重用防護有效**
- [ ] 其他 verify tests 全過
- [ ] 手動驗證：spec §9 情境 2（detached Codex 不覆蓋 CC）通過
- [ ] Codex review 無 high severity
- [ ] PR 兩輪 review + merge + bump

### 預估工作量

2.5 天

---

## Phase 4：Frame Store + Session Projection

### 目標

取代「一個 tmux_session 一列」的 DB schema，改為 pane-scoped frame 表。Daemon 層計算 session projection，SPA 接口保持不變。

### 範圍

- 新 `agent_frames` table
- Frame 持久化到 SQLite（與現有 agent_events 同 DB）
- Daemon replay：restart 後讀回 frames + 用 ProcessStartTime 驗證 PID 未被重用
- Hook 事件對 Frame 的操作映射（見 spec §5.8）
- Orphan 政策：parent 死 → child 的 ParentFrameID 設 NULL，child 不清

### 檔案改動清單

| 檔案 | 動作 | 說明 |
|------|------|------|
| `internal/store/migrations/NNN_frames.sql` | 新增 | 新 table schema |
| `internal/store/frames.go` | 新增 | CRUD + orphan policy |
| `internal/store/frames_test.go` | 新增測試 | |
| `internal/module/agent/projection.go` | 新增 | frames → SessionProjection |
| `internal/module/agent/projection_test.go` | 新增測試 | |
| `internal/module/agent/handler.go` | 修改 | verify 通過後呼叫 frame operation（見下 mapping）|
| `internal/module/agent/module.go` | 修改 | replay 改讀 frames；含 ProcessStartTime 驗證 |

### Schema

```sql
CREATE TABLE agent_frames (
    frame_id            TEXT PRIMARY KEY,
    pane_id             TEXT NOT NULL,
    agent_type          TEXT NOT NULL,
    pid                 INTEGER NOT NULL,
    ppid                INTEGER NOT NULL,
    process_start_time  TEXT NOT NULL,                -- lstart 字串，PID reuse 保險
    parent_frame_id     TEXT,
    subagents_json      TEXT NOT NULL DEFAULT '[]',   -- JSON array of subagent ids
    status              TEXT NOT NULL,
    started_at          INTEGER NOT NULL,              -- daemon-side epoch
    last_seen_at        INTEGER NOT NULL,
    verified            INTEGER NOT NULL DEFAULT 1,
    FOREIGN KEY (parent_frame_id) REFERENCES agent_frames(frame_id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX idx_frames_pane_pid_start ON agent_frames(pane_id, pid, process_start_time);
CREATE INDEX idx_frames_pane ON agent_frames(pane_id);
CREATE INDEX idx_frames_agent_type ON agent_frames(agent_type);
```

### Hook → Frame Operation 對應

| Hook | Operation |
|------|-----------|
| `SessionStart` | Upsert frame（建立，若存在則更新 LastSeenAt）|
| `UserPromptSubmit` / `PreToolUse` / `PostToolUse` | Update frame.Status=`running`、LastSeenAt |
| `Notification`（CC） | Update frame.Status=`waiting` |
| `Stop`（per turn） | Update frame.Status=`idle`；**不 pop frame** |
| `SessionEnd`（CC） | Pop frame（ON DELETE SET NULL 會自動 orphan child）|
| `SubagentStart` / `SubagentStop` | Update frame.Subagents（不建新 frame）|

### TDD 任務

1. **Test 1**：`TestFramesStore_UpsertAndRead`
2. **Test 2**：`TestFramesStore_NestedFrames_ParentFrameLink`
3. **Test 3**：`TestFramesStore_OrphanPolicy` ⭐
   - parent frame 刪除 → child 的 ParentFrameID 自動設 NULL
   - child frame 本身仍存在
4. **Test 4**：`TestFramesStore_UniqueOnPidAndStartTime`
   - 同 pane_id + pid 但不同 start_time → 視為不同 frame（PID reuse 情境）
5. **Test 5**：`TestProjection_TopFrameWins`
   - 多個 frame，TopFrame = 最近 StartedAt 或 stack top
6. **Test 6**：`TestProjection_CcAndCodexCoexist` ⭐
   - 同 pane 兩個 frame（CC + Codex）不互相覆蓋
7. **Test 7**：`TestHandleEvent_SessionStartUpsertsFrame`
8. **Test 8**：`TestHandleEvent_StopDoesNotPopFrame`
   - Stop 只改 status 為 idle，不刪 frame
9. **Test 9**：`TestHandleEvent_SessionEndPopsFrame`
10. **Test 10**：`TestHandleEvent_SubagentDoesNotCreateFrame`
    - SubagentStart 只更新 Subagents 欄位，不建新 frame
11. **Test 11**：`TestReplay_SkipsFramesWithStaleStartTime` ⭐
    - DB 有 frame pid=100, start_time="A"
    - 重啟時 ProcessStartTime(100) = "B"（PID 被重用給別的程序）
    - Replay 丟棄此 frame
12. **Test 12**：`TestReplay_RestoresLiveFrames`
    - 用 `os.Getpid()` 當 frame.PID，replay 後保留

### Implementation 要點

- `parent_frame_id` 計算：新 frame 的 PPID 若為某既有 frame 的 PID → 該 frame 為 parent
- `subagents_json` 儲存 `Frame.Subagents`；daemon restart 後 replay 時一併恢復
- TopFrame 算法：`ORDER BY started_at DESC LIMIT 1`（同 pane_id）
- Projection 在 WS broadcast 前重新計算
- SPA 接口保持向後相容：WS 廣播 `agent_type` 欄位填 TopFrame.AgentType

### 驗收 (DoD)

- [ ] **`TestProjection_CcAndCodexCoexist` 過 — 多 agent 共存**
- [ ] **`TestFramesStore_OrphanPolicy` 過 — 主從生命週期**
- [ ] **`TestReplay_SkipsFramesWithStaleStartTime` 過 — PID reuse 防護**
- [ ] SPA 端無需改動仍可正常渲染
- [ ] 手動：spec §9 情境 4（CC 內呼叫 Codex subprocess，跨 quiet period / sweep 仍不誤清）正確
- [ ] 手動：spec §9 情境 13（tmux rename / tmux 短暫失聯）正確
- [ ] 手動：spec §9 情境 11（daemon restart 3 個活 session）正確
- [ ] Codex review 無 high severity
- [ ] PR 兩輪 review + merge + bump

### 預估工作量

3 天（比 v1 多 0.5 天，因 PID reuse / orphan 政策 / hook mapping 補完）

---

## Phase 5：安全版 Periodic Sweep（per-frame）

### 目標

在 frame 模型基礎上重做 `watchAlive`，取代 PR #486。Sweep 以 **per-frame** 為單位用 `kill -0` + start_time 驗證，不再 per-pane，不再用字串比對。

### 範圍

- Frame-based per-frame periodic sweep
- Proper lifecycle（繼承 ctx + WaitGroup 等 goroutine 退出）
- 舊 `checkAliveAll` 刪除

### 檔案改動清單

| 檔案 | 動作 | 說明 |
|------|------|------|
| `internal/module/agent/sweep.go` | 新增 | per-frame sweep |
| `internal/module/agent/sweep_test.go` | 新增測試 | |
| `internal/module/agent/module.go` | 修改 | Start/Stop 改用新 sweep，刪舊 `checkAliveAll` |
| `internal/module/agent/handler_test.go` | 修改 | 刪舊 `TestCheckAliveAll_*` |

### Sweep 邏輯

```go
every 2s:
  frames := store.ListVerifiedFrames()
  for _, f := range frames {
      if !IsPidAlive(f.PID) {
          clearFrame(f)  // 記得 recompute projection + broadcast
          continue
      }
      if ProcessStartTime(f.PID) != f.ProcessStartTime {
          clearFrame(f)  // PID reuse 偵測
      }
  }
```

### TDD 任務

1. **Test 1**：`TestSweep_ClearsDeadFramesByPid`
   - frame pid=99999（不存在）→ sweep 清除、broadcast
2. **Test 2**：`TestSweep_PreservesLiveFrames`
   - frame pid=`os.Getpid()` → 保留
3. **Test 3**：`TestSweep_DetectsPidReuse` ⭐
   - frame 存入 start_time="A"
   - Mock ProcessStartTime 回 "B"
   - Sweep 清除
4. **Test 4**：`TestSweep_StopWaitsForInFlight` ⭐ 解 PR #486 review finding
   - 啟動 sweep，在 sweep 執行中 Stop
   - Stop 回傳後無後續 operation
5. **Test 5**：`TestSweep_ContextCancellationPropagates`
6. **Test 6**：`TestSweep_DoesNotMassDeleteOnTmuxOutage` ⭐
   - Sweep 只看 frame.PID，不依賴 tmux ListSessions
   - tmux 失聯不影響 frame sweep
7. **Test 7**：`TestSweep_ClearingFramePreservesSiblings`
   - 同 pane 兩個 frame（CC + Codex），清 Codex 不動 CC

### Implementation 要點

- Ticker 預設 2s
- Stop 用 `sync.WaitGroup` 等 goroutine exit
- `checkAlive` 接收 Module lifecycle ctx
- Sweep 不需要 tmux（它看 PID 不看 tmux session）→ 解 PR #486 tmux outage 大屠殺

### 驗收 (DoD)

- [ ] **`TestSweep_StopWaitsForInFlight` 過**（解 PR #486 finding 4）
- [ ] **`TestSweep_DoesNotMassDeleteOnTmuxOutage` 過**（解 PR #486 finding 3）
- [ ] **`TestSweep_DetectsPidReuse` 過**
- [ ] 手動：kill -9 殺掉 agent 後 icon 在 ≤2s 回到 terminal（spec §9 情境 10）
- [ ] 手動：spec §9 情境 13（tmux rename / tmux 短暫失聯）正確
- [ ] Codex review 無 high severity
- [ ] PR 兩輪 review + merge + bump
- [ ] 同 PR 或後續 follow-up：close PR #486 + comment 指向新實作

### 預估工作量

1.5 天

---

## Phase 6：Activity 三規則 + `-e` flag

### 目標

Activity watcher 實作三規則（動 → running、靜 → idle、shell prompt **AND** kill -0 失敗 → 通知 sweep 清除）。`capture-pane` 加 `-e` flag。

### 關鍵修正

**Shell prompt 規則是兩條件 AND**：單純畫面像 shell prompt 不足以 pop frame（避免 markdown code fence / CC 輸出 `$` 等誤觸發）；必須同時 `kill -0` PID 失敗。

### 範圍

- Activity watcher 收斂成三規則 state machine
- Shell prompt pattern detection
- `capture-pane` 加 `-e` flag（含 ANSI escape）
- `ContentMatcher` interface + `looksLikeCC` 刪除（身份層已用 Phase 2 Identify）
- ReadinessChecker 保留不動

### 檔案改動清單

| 檔案 | 動作 | 說明 |
|------|------|------|
| `internal/agent/probe/activity.go` | 修改 | 三規則 state machine |
| `internal/agent/probe/activity_test.go` | 修改 | 對應測試 |
| `internal/tmux/executor.go` | 修改 | `CapturePaneContent` 加 `-e` flag |
| `internal/agent/probe/shell_prompt.go` | 新增 | `looksLikeShellPrompt(string) bool` |
| `internal/agent/probe/shell_prompt_test.go` | 新增測試 | |
| `internal/agent/probe/probe.go` | 修改 | 刪 `RegisterContentMatcher` / `ContentMatcher` |
| `internal/agent/cc/content_matcher.go` | 刪除 | |
| `internal/agent/cc/content_matcher_test.go` | 刪除 | |
| `internal/module/agent/module.go` | 修改 | 刪 `RegisterContentMatcher("cc", ...)` |

### TDD 任務

1. **Test 1**：`TestActivity_MotionYieldsRunning`
2. **Test 2**：`TestActivity_StableYieldsIdle`
   - 連續 3 次 hash 相同判 idle
3. **Test 3**：`TestActivity_ShellPromptAlone_DoesNotPop` ⭐ 關鍵
   - 畫面像 shell prompt，但 `kill -0` 通過
   - Frame 不被清
4. **Test 4**：`TestActivity_ShellPromptAndDeadPid_TriggersSweep` ⭐
   - 畫面像 shell prompt **AND** `kill -0` 失敗
   - Activity watcher 通知 sweep（或標記 frame 為可清）
5. **Test 5**：`TestActivity_ColorOnlySpinnerDetected` ⭐
   - hash 只有 ANSI escape 變化（彩虹單字元）
   - 驗證 `-e` 有效
6. **Test 6**：`TestShellPrompt_Patterns`
   - 常見 shell prompt 變體（zsh / bash / 含 cwd / 含 git status 等）
7. **Test 7**：`TestShellPrompt_NotMarkdownCodeBlock`
   - Markdown `$` 結尾行不應被判 shell prompt（實際上會被判但靠 kill -0 AND 條件兜住；本 test 確認單純 pattern 檢查會命中，提醒需靠兩條件 AND）
8. **Test 8**：`TestActivity_UserAnswersNotificationResumesRunning`（CC Gap 1 補強）
9. **Test 9**：`TestActivity_CodexCtrlCReturnsToShell`（Codex Gap 4 補強）
   - 模擬 Codex 退出（PID 死 + 畫面變 shell prompt）
   - Activity 偵測到，sweep 下一輪清掉

### Implementation 要點

- 取樣間隔 500ms
- idle threshold：連續 3 次 hash 相同
- shell prompt pattern 通用規則（`$` / `#` / `%` / `>`）
- Activity watcher **不直接 pop frame**；它觸發 status 轉換（running / idle）或給 sweep 一個 hint（frame 可能要清了）
- 實際 pop 仍由 sweep 用 `kill -0` 最終確認

### 驗收 (DoD)

- [ ] **`TestActivity_ShellPromptAlone_DoesNotPop` 過**（避免 markdown 誤觸發）
- [ ] **`TestActivity_ColorOnlySpinnerDetected` 過**（`-e` flag 有效）
- [ ] 手動：spec §9 情境 8（Ctrl+C 回 shell）通過
- [ ] 手動：spec §9 情境 9（CC Notification 回應升 running）通過
- [ ] Codex review 無 high severity
- [ ] PR 兩輪 review + merge + bump

### 預估工作量

1.5 天

---

## Phase 7：Legacy 路徑清理

### 目標

刪除舊的 command-name 身份路徑。收斂最終設計。

### 範圍

- 刪除 `RegisterProcessNames` + `UpdateProcessNames`
- 刪除 probe Liveness Layer 1a（`pane_current_command` 比對）
- 刪除 `matcher.commands` map
- `config.Detect.CCCommands` 不再參與 probe liveness；僅保留給 CC provider Identify 補充 command basename
- 更新 `docs/superpowers/specs/2026-04-13-probe-chain-design.md` 「已被收斂」標記
- 更新 README / AGENTS.md / CLAUDE.md 相關描述

### 檔案改動清單

| 檔案 | 動作 | 說明 |
|------|------|------|
| `internal/agent/probe/probe.go` | 修改 | 刪 `RegisterProcessNames` / `matcher.commands` |
| `internal/agent/probe/liveness.go` | 修改 | 收斂為 Identify + Q1/Q2/Q3 |
| `internal/agent/probe/liveness_test.go` | 修改 | |
| `internal/module/agent/module.go` | 修改 | 刪 `RegisterProcessNames("cc", ...)` 呼叫 |
| `internal/config/config.go` | 修改 | `CCCommands` deprecated 或刪除 |
| `docs/superpowers/specs/2026-04-13-probe-chain-design.md` | 修改 | 加「已被 2026-04-20 spec 取代部分邏輯」section |
| `CLAUDE.md` | 修改 | 若有相關描述則更新 |

### TDD 任務

1. **Test 1**：`grep` 驗證 legacy 符號已清（build-time）
2. **Test 2**：完整 end-to-end 跑 issue #487 重現情境，確認 icon 穩定
3. 既有測試全過（regression 最重要）

### 驗收 (DoD)

- [ ] `grep -r "RegisterProcessNames\|pane_current_command\|LooksLikeCC\|looksLikeCC" internal/` 回空
- [ ] 手動：spec §9 所有 14 個情境通過
- [ ] spec §8 驗收標準所有 8 項通過
- [ ] Codex review 無 high severity
- [ ] PR 兩輪 review + merge + bump
- [ ] 更新 issue #487：close + 指向最終 PR

### 預估工作量

1 天

---

## 總計

| Phase | 工作量 | 累計 |
|:---:|:---:|:---:|
| P0 | 0.5 | 0.5 |
| P1 | 1.5 | 2.0 |
| P2 | 2.0 | 4.0 |
| P3 | 2.5 | 6.5 |
| P4 | 3.0 | 9.5 |
| P5 | 1.5 | 11.0 |
| P6 | 1.5 | 12.5 |
| P7 | 1.0 | 13.5 |

約 **13-14 工作天**（不含 review 等待時間）。實際抓 17-20 工作天。

---

## 風險追蹤

| 風險 | Phase | 緩解 |
|------|:---:|------|
| pdx hook 的 shim list 不完整（新 shim 導致 agent PID 解析錯誤）| P1 | 測試覆蓋常見 shim；新增 shim 時加 log；unknown 第一層不 skip 以保守 |
| Verify 過嚴誤拒合法 wrapper | P3 | log reason 詳細；透過使用者回報調整 shim list（P1）或 Identify（P2），不放寬主 verify |
| macOS `proc_pidpath` 不可用或不值得引入 cgo | P2 | 改用 `ps -p PID -o comm=` 取 executable，再配 `args=` 組 argv；避免把被改寫的 `argv[0]` 當成 ExePath |
| Frame schema migration 複雜 | P4 | alpha 階段直接新表 + 舊表退役；不做雙軌 |
| Codex ApplyPatch 期間狀態不變 | P6 | 上游 issue #16732；本輪以「畫面不動 → idle」兜底，不再追求完美 |
| SPA 需不需要改 | 全程 | 設計目標：不改。projection 在 daemon 層完成 |
| PID reuse 極短窗口 | P3/P4 | `lstart` 秒級精度已夠，極短窗口接受殘留風險 |

---

## 交付清單（每 Phase 都要產出）

- [ ] PR description 明確指向本 plan 的 Phase N
- [ ] PR description 列出本 Phase 所有驗收點 + 勾選狀態
- [ ] CHANGELOG.md 加條目
- [ ] Codex review 報告摘要貼 PR comment
- [ ] 手動驗證情境（spec §9 對應項目）執行結果

## 完成後

所有 Phase 完成後：

1. Close issue #487 + 指向合併 PR 清單
2. Close PR #486 + comment 指向 Phase 5 新實作
3. 歸檔 branch `feat/agent-watch-alive`（可刪）
4. 在 `project_progress.md` / memory index 標記本 spec 已完成
5. 把 `docs/testing/agent-identity-regression-checklist.md` 納入例行 PR review checklist
