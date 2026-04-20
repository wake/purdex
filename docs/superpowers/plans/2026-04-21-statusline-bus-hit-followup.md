# Statusline Pipeline Self-Test - Bus Hit Follow-up

> 日期：2026-04-21
> 狀態：In Progress
> 基線：`origin/main` at `9e57f17f`
> 承接計畫：`docs/superpowers/plans/2026-04-19-statusline-pipeline-test.md`
> 相關 spec：
> - `docs/superpowers/specs/2026-04-18-cc-statusline-integration-design.md`
> - `docs/superpowers/specs/2026-04-18-statusline-and-daemon-rebuild-design.md`

## 目的

主功能與 stage-4 grace fallback 已經在 `main`。本 follow-up 只處理 daemon-side sequencing refinement：

- 讓 statusline self-test 的 `agent.status` WS broadcast 明確發生在 SSE stage 2 之後
- 讓 SPA 更常走到真實的 `statuslineTestBus` subscriber 路徑，而不是仰賴 early-hit / grace fallback

這是 `2026-04-19-statusline-pipeline-test.md` 的 post-plan 修正；不改 spec contract，只修正 plan 落地後暴露的 race。

## 非目標

- 不重做 `stage45-timeout` 的 SPA grace-window；該修正已在 `main`
- 不擴大到 `StatuslineTestPanel` UI 或 i18n 變更
- 不處理其他 statusline / installer / daemon rebuild 題目

## 範圍

只允許變更以下 5 個檔案：

- `internal/module/agent/handler.go`
- `internal/module/agent/handler_test.go`
- `internal/module/agent/module.go`
- `internal/module/agent/statusline_selftest.go`
- `internal/module/agent/statusline_selftest_test.go`

## 預期行為

### 目前 main 的風險

- `handleAgentStatus` 在 test nonce 路徑中直接 broadcast `agent.status`
- SSE stage 2 與 WS event 抵達 SPA 的時序沒有被 self-test handler 額外約束
- 結果是 stage 4 可能常落到 early-hit / grace fallback，而非真實 bus callback

### Follow-up 後

1. `handleAgentStatus` 在 test nonce 路徑只把 raw status payload 傳給 test observer
2. `handleStatuslineTest` 在 SSE stage 2 成功後，自己執行 `agent.status` WS broadcast
3. SSE stage 3 代表「daemon 已完成 WS broadcast」
4. `agent.status.cleared` 仍由 self-test handler 在結尾送出，清掉 synthetic nonce state

## 驗證

- `go test ./internal/module/agent -count=1`
- 至少保留 / 補強以下行為測試：
  - observer register / signal / deregister
  - self-test endpoint 正常輸出 stage 1-3 + done
  - `handleAgentStatus` test nonce 路徑只 signal observer，不自行 broadcast
  - self-test endpoint 會在 stage 2 之後 broadcast `agent.status` 與 `agent.status.cleared`
  - proxy spawn failure 路徑

## 交付方式

- 一個 focused commit
- 後續走 PR 與兩輪 review
- merge 後再做 bump PR（不在本 worktree 內先改 `VERSION` / `CHANGELOG.md`）
