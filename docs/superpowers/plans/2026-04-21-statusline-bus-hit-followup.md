# Statusline Pipeline Self-Test - Bus Hit Follow-up

> 日期：2026-04-21
> 狀態：In Progress
> 基線：`origin/main` at `9e57f17f`
> 承接計畫：`docs/superpowers/plans/2026-04-19-statusline-pipeline-test.md`
> 相關 spec：
> - `docs/superpowers/specs/2026-04-18-cc-statusline-integration-design.md`
> - `docs/superpowers/specs/2026-04-18-statusline-and-daemon-rebuild-design.md`

## 目的

主功能與 stage-4 grace fallback 已經在 `main`。本 follow-up 處理 self-test race 的最小可驗證修正：

- 在 self-test 開始時先送 `init` nonce，讓 SPA 先訂閱 `statuslineTestBus`
- 加入 ready ack，讓 daemon 在 client 宣告 ready 之後才 spawn proxy
- 保留真實 `/api/agent/status -> agent.status broadcast` 路徑，不把 broadcast 移到 self-test handler 自己重建

這是 `2026-04-19-statusline-pipeline-test.md` 的 post-plan 修正；不改 spec contract，只修正 plan 落地後暴露的 race。

## 非目標

- 不重做 `stage45-timeout` 的 SPA grace-window；該修正已在 `main`
- 不擴大到 `StatuslineTestPanel` UI、i18n、installer、daemon rebuild 題目
- 不處理其他 statusline / installer / daemon rebuild 題目

## 範圍

允許變更以下檔案：

- `internal/module/agent/handler.go`
- `internal/module/agent/handler_test.go`
- `internal/module/agent/module.go`
- `internal/module/agent/statusline_selftest.go`
- `internal/module/agent/statusline_selftest_test.go`
- `spa/src/hooks/useStatuslineTest.ts`
- `spa/src/hooks/useStatuslineTest.test.ts`

## 預期行為

### 目前 main 的風險

- daemon 無法得知 SPA 是否已拿到 nonce 並完成 bus subscriber 註冊
- 單靠 SSE / WS 兩條獨立連線的寫出順序，無法建立 client-ready barrier
- 結果是 stage 4 可能常落到 early-hit / grace fallback，而非真實 bus callback

### Follow-up 後

1. `handleStatuslineTest` 先送 `init` event，讓 SPA 取得 nonce
2. SPA 用 nonce 先訂閱 `statuslineTestBus`，再呼叫 ready ack endpoint
3. daemon 優先等待 ready ack，再 spawn proxy；若 SPA 尚未升級，逾時後回退舊行為
4. test nonce 仍走真實 `handleAgentStatus -> agent.status broadcast` 路徑
5. `agent.status.cleared` 仍由 self-test handler 在結尾送出，清掉 synthetic nonce state
6. 新舊版相容：
   - 新 frontend + 舊 daemon：保留 stage-1 nonce fallback
   - 新 daemon + 舊 frontend：ready wait 逾時後回退舊行為，不在 spawn 前直接 fail
   - 新 frontend + 新 daemon：走 init/ready 優先路徑，減少 bus subscriber race

## 驗證

- `go test ./internal/module/agent -count=1`
- `pnpm --prefix spa exec vitest run src/hooks/useStatuslineTest.test.ts src/lib/agent-ws-dispatch.test.ts`
- `pnpm --prefix spa exec eslint src/hooks/useStatuslineTest.ts src/hooks/useStatuslineTest.test.ts`
- 至少保留 / 補強以下行為測試：
  - observer register / signal / deregister
  - self-test ready ack 會 gate proxy spawn
  - test nonce 只有在 observer 存在時才走 self-test special-case；否則仍走 production path
  - self-test endpoint 正常輸出 stage 1-3 + done
  - self-test endpoint 在 client ready 後能走到真實 `agent.status` broadcast 與 cleanup
  - proxy spawn failure 路徑

## 交付方式

- 一個 focused commit
- 後續走 PR 與兩輪 review
- merge 後再做 bump PR（不在本 worktree 內先改 `VERSION` / `CHANGELOG.md`）
