# OpenCode Hook Integration Implementation Plan

**Goal:** 為 Purdex 新增 `opencode` 等位 agent hooks，走 host-global plugin 安裝，並接入現有 agent provider / hook status / settings UI，同時對齊 Claude Code 的 subagent lifecycle 行為。

**Spec:** `docs/superpowers/specs/2026-04-21-opencode-hook-integration-design.md`

**本階段刻意不做：** 不處理 `session.status=busy/retry` → running 映射，僅保留為觀察項。

---

## File Map

### New Files

| Path | Responsibility |
|------|---------------|
| `internal/agent/opencode/provider.go` | OpenCode provider assembly / identify |
| `internal/agent/opencode/status.go` | OpenCode status derivation |
| `internal/agent/opencode/hooks.go` | Host-global plugin installer / checker / remover |
| `internal/agent/opencode/plugin_template.go` | Render managed OpenCode plugin source and own all OpenCode-specific event normalization |
| `internal/agent/opencode/status_test.go` | Status derivation tests |
| `internal/agent/opencode/hooks_test.go` | Plugin install/check/remove tests |
| `internal/agent/opencode/plugin_template_test.go` | Plugin mapping / suppress / subagent pairing tests, including negative pairing cases |

### Modified Files

| Path | Change |
|------|--------|
| `internal/module/agent/module.go` | Register OpenCode provider |
| `internal/module/agent/handler.go` | Agent detect 增加 `opencode`; adjust `handleEvent` error guard for OpenCode |
| `internal/module/agent/frame_ops.go` | Ensure `SessionStart` clears persisted subagent membership before projection sync |
| `internal/module/agent/frame_ops_test.go` | Add OpenCode projection cleanup and subagent membership tests |
| `internal/module/agent/handler_test.go` | Add OpenCode error guard tests |
| `cmd/pdx/setup.go` | `--agent opencode` local setup |
| `cmd/pdx/setup_test.go` | OpenCode install/remove cases |
| `spa/src/lib/hook-modules.ts` | Add OpenCode hooks module |
| `spa/src/lib/agent-metadata.ts` | Add OpenCode display name |
| `spa/src/locales/en.json` | Add OpenCode hook labels |
| `spa/src/locales/zh-TW.json` | Add OpenCode hook labels |

---

## Task 1: Backend Provider + TDD

- [ ] 新增 `internal/agent/opencode/status_test.go`
- [ ] 新增 `internal/agent/opencode/hooks_test.go`
- [ ] 新增 `internal/agent/opencode/plugin_template_test.go`
- [ ] 實作 `provider.go`
- [ ] 實作 `status.go`
- [ ] 實作 `hooks.go`
- [ ] 實作 `plugin_template.go`
- [ ] 在 plugin template 中實作 `task` tool → `SubagentStart` / `SubagentStop` mapping
- [ ] 在 plugin template 中實作 `session.error` 後 `session.idle` suppress
- [ ] 在 `plugin_template_test.go` 覆蓋負向 pairing：unknown callID、stop-before-start、duplicate start、missing `agent_id`
- [ ] 確認 install/check/remove 與 unmanaged guard 行為都被測到

## Task 2: Agent Module / CLI Wiring

- [ ] 在 `internal/module/agent/module.go` 註冊 `opencode` provider
- [ ] 在 `internal/module/agent/module.go` 註冊 `m.prober.RegisterIdentifier(opencodeProvider.Type(), opencodeProvider.Identify)`
- [ ] 視需要決定本階段是否略過 `RegisterReadiness`，不要把它當成必做項目
- [ ] 在 `internal/module/agent/handler.go` 的 `handleEvent` error guard 明確調整 OpenCode whitelist：`SessionEnd` 可通過、`Stop` 不會清掉 error
- [ ] 在 `internal/module/agent/frame_ops.go` 或等位路徑確保 `SessionStart` 會清除 persisted `Subagents`，不只清 in-memory state
- [ ] 在 `internal/module/agent/handler.go` 的 `/api/agents/detect` 增加 `opencode --version`
- [ ] 在 `cmd/pdx/setup.go` 補上 `opencode` 分支與錯誤訊息
- [ ] 在 `cmd/pdx/setup_test.go` 補 install/remove case

## Task 2.1: Module-Level Behavior Tests

- [ ] 補 `internal/module/agent` 測試，覆蓋 `opencode` 的 `SubagentStart` / `SubagentStop` projection path
- [ ] 補 `internal/module/agent` 測試，覆蓋 `opencode` 的 `SessionStart` 會清殘留 subagent，且不會被 projection 寫回
- [ ] 補 `internal/module/agent` 測試，覆蓋 `opencode` 的 `SessionEnd` 在 error 狀態下仍可 cleanup
- [ ] 補 `internal/module/agent` 測試，覆蓋 `opencode` 的 `Stop` 不可作為 error clear 事件

## Task 3: SPA Hook 管理 UI

- [ ] 在 `spa/src/lib/hook-modules.ts` 新增 `opencode` card
- [ ] 在 `spa/src/lib/agent-metadata.ts` 新增 `OpenCode`
- [ ] 同步更新 `en.json` / `zh-TW.json`
- [ ] 確認 hooks settings page 會列出第三個 agent hook module

## Task 4: Verification

- [ ] 跑 `go test ./internal/agent/opencode ./cmd/pdx ./internal/module/agent -count=1`
- [ ] 跑 `pnpm --prefix spa run build`
- [ ] 檢查 worktree diff，確認沒有碰到產物或無關檔案
