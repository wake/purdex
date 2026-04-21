# 2026-04-21 Worktree Handoff

目的：把剩餘 worktree 的後續處理拆成 3 個獨立 session。

## 目前剩餘 worktree

- `main`
- `agent-hook-trace-monitor`
- `agent-identity-p0`
- `probe-recursive-cache`
- `statusline-test-bus-hit`
- `statusline-test-stage45-timeout`

其中 `agent-hook-trace-monitor` 是唯一明顯仍在活躍開發中的 worktree；本 handoff 不處理它。

## Session 1: 關閉 `probe-recursive-cache`

目標：清掉已完成但殘留的 bump worktree 與 branch。

判斷依據：

- worktree 路徑：`.claude/worktrees/probe-recursive-cache`
- 實際 branch：`worktree-bump-alpha188`
- `git rev-list --left-right --count origin/main...HEAD` = `96 1`
- 相對 `origin/main` 的差異只剩：`CHANGELOG.md`、`VERSION`、`package.json`、`spa/package.json`
- `CHANGELOG.md` 已明確記錄 `Fix(agent): probe wrapped descendants with bounded cache (#484)`，代表 feature 本體已進主線，這條只剩版本 bump 尾巴。

建議動作：

1. 再確認 worktree clean。
2. 移除 worktree `probe-recursive-cache`。
3. 刪除本地 branch `worktree-bump-alpha188`。
4. remote tracking 已是 `[gone]`，通常不需做額外 remote 清理。

## Session 2: 關閉 `agent-identity-p0`

目標：清掉 docs-only worktree；必要時保留文件內容作為後續實作參考。

判斷依據：

- worktree 路徑：`.claude/worktrees/agent-identity-p0`
- branch：`worktree-agent-identity-p0`
- `git rev-list --left-right --count origin/main...HEAD` = `95 2`
- 相對 `origin/main` 的差異只有 3 份文件：
  - `docs/specs/2026-04-20-agent-identity-and-liveness-convergence-design.md`
  - `docs/superpowers/plans/2026-04-20-agent-identity-and-liveness-convergence.md`
  - `docs/testing/agent-identity-regression-checklist.md`
- 這條沒有程式碼變更，是規劃 branch，不適合直接拿來實作，因為已落後 `main` 很多。

建議動作：

1. 先決定文件要不要保留到主線或其他地方。
2. 若暫不實作，移除 `agent-identity-p0` worktree。
3. branch `worktree-agent-identity-p0` 可視需要保留或刪除：
   - 想保留規劃：留 branch
   - 已另有備份：刪 branch

備註：若未來真要做這條，應從最新 `main` 重新開 fresh worktree，把這 3 份文件帶過去，而不是直接在這條舊 branch 上開工。

## Session 3: 整理 `statusline-test-bus-hit` 與 `statusline-test-stage45-timeout`

目標：判斷兩條 statusline follow-up 是否應合併為一條新的 fresh branch，並提出整併方式。

### A. `statusline-test-bus-hit`

- worktree 路徑：`.claude/worktrees/statusline-test-bus-hit`
- branch：`worktree-statusline-test-bus-hit`
- `git rev-list --left-right --count origin/main...HEAD` = `57 1`
- 相對 `origin/main` 的差異檔案：
  - `internal/module/agent/handler.go`
  - `internal/module/agent/handler_test.go`
  - `internal/module/agent/module.go`
  - `internal/module/agent/statusline_selftest.go`
  - `internal/module/agent/statusline_selftest_test.go`
- 核心修正：把 self-test 的 `agent.status` WS broadcast 排到 SSE stage 2 之後，降低 client subscriber race。

### B. `statusline-test-stage45-timeout`

- worktree 路徑：`.claude/worktrees/statusline-test-stage45-timeout`
- branch：`worktree-statusline-test-stage45-timeout`
- `git rev-list --left-right --count origin/main...HEAD` = `95 2`
- 相對 `origin/main` 的差異檔案：
  - `spa/src/hooks/useStatuslineTest.ts`
  - `spa/src/hooks/useStatuslineTest.test.ts`
- 核心修正：加入 stage 4 grace window (`STAGE4_GRACE_MS = 2000`)，避免 SSE 已 done 但 WS 晚到時，stage 4/5 永遠 spinner。

### 整體判讀

- 兩條不是重複修正，而是同一問題面的 daemon-side + SPA-side 補強。
- `bus-hit` 偏 race ordering 修正。
- `stage45-timeout` 偏 client safety net。
- 兩條都建議不要在舊 base 上直接續做；更合理的方式是從最新 `main` 開一條 fresh branch，把這兩組差異重新 cherry-pick / 重做 / re-verify。

### 建議輸出

Session 3 最好產出：

1. 是否合併：`yes/no`
2. 若 `yes`：新 branch 建議名稱
3. 整併順序：先 daemon 後 SPA，或反之
4. 在最新 `main` 上預期要重跑的 targeted tests
5. 是否保留原兩個 worktree，或整併後一起清掉

## 參考檔案

- `docs/superpowers/plans/2026-04-20-agent-hook-trace-monitor.md`
- `docs/superpowers/specs/2026-04-20-agent-trace-monitor-design.md`
- `.claude/worktrees/agent-identity-p0/docs/specs/2026-04-20-agent-identity-and-liveness-convergence-design.md`
- `.claude/worktrees/agent-identity-p0/docs/superpowers/plans/2026-04-20-agent-identity-and-liveness-convergence.md`
- `.claude/worktrees/statusline-test-bus-hit/internal/module/agent/statusline_selftest.go`
- `.claude/worktrees/statusline-test-bus-hit/internal/module/agent/statusline_selftest_test.go`
- `.claude/worktrees/statusline-test-stage45-timeout/spa/src/hooks/useStatuslineTest.ts`
- `.claude/worktrees/statusline-test-stage45-timeout/spa/src/hooks/useStatuslineTest.test.ts`
