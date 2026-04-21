# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 專案概述

**Purdex** — tmux session 的遠端管理工具，含 Go daemon + React SPA + Electron shell。支援 Terminal、Stream（Claude Code `-p` 串流）、JSONL 三種模式。（原名 tmux-box，2026-04 更名）

- Repo: `git@github.com:wake/purdex.git`
- 主分支: `main`（v0 備份在 `v0` 分支）
- 版本: `VERSION` 檔案為 SOT，bump 時須同步 `package.json` + `spa/package.json`

## 開發環境

- **Package manager**: pnpm（不是 npm）
- **Daemon**: `100.64.0.2:7860`（Go binary `bin/pdx`）
- **SPA**: `100.64.0.2:5174`（`spa/`）
- **測試**: `cd spa && npx vitest run`
- **Lint**: `cd spa && pnpm run lint`
- **Build**: `cd spa && pnpm run build`

## 技術棧

- **Daemon**: Go / net/http / gorilla/websocket / creack/pty / modernc.org/sqlite
- **SPA**: React 19 / Vite 8 / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons / xterm.js 6
- **Electron**: electron-vite / electron-builder / contextBridge IPC
- **Icon 圖示**: 統一使用 Phosphor Icons

## 打包與更新

- **Electron 打包**：`pnpm run electron:build` → `dist/mac/`（x64）+ `dist/mac-arm64/`（ARM）
- **SPA 更新**：`.app` 啟動時偵測 Mini dev server，可達則 `loadURL`（HMR 即時），不可達則 fallback 到 bundled renderer
- **Electron 更新**：daemon `/api/dev/update/check` + `/api/dev/update/download`，Settings → Development 頁面操作（需 `PDX_DEV_UPDATE=1`）
- **跨機開發**：Mini（100.64.0.2）編譯，Air 執行 `.app`，SPA 改動即時生效，Electron 改動透過 dev update 機制

### Dev Update 注意事項

- **check 與 download 的來源不同**：`/check` 用 `git log` 取源碼最新 commit hash，`/download` 打包 `out/` 目錄的建置產出
- **改動後必須重新打包**：push 新 commit 後須在 Mini 跑 `pnpm run electron:build`，否則 `out/` 裡的 baked-in hash 是舊的，Air 端會無限顯示 "Update available"
- **SPA 走 HMR 不受影響**：dev server 跑著時 SPA 改動即時生效，但 Electron main/preload 改動仍需打包 + dev update

## 完整開發流程

**絕對不能直推 main**，即使 hotfix 也必須走 PR + review
**TDD：先寫測試再實作**
**每個 task 獨立 commit**

1. 依照需求 / 請求提出建議方案
2. 依據討論完成方案撰寫 spec，必須按照合適 review 大小切分 phase
3. 委派 codex 審閱 spec
4. 依據定稿的 spec 撰寫 plan
5. 委派 codex 審閱 plan
6. 依據 plan 使用自己的 subagent 進行開發
7. PR & 委派 codex 兩輪深度 review
8. 確認完成後進行 PR mrege，必須更新 `VERSION` + `CHANGELOG.md` 並 commit push

### PR Review 兩輪制 (委派 Codex 進行)

**第一輪：標準 code review（跨模型差異化檢查）**

**第二輪：3 個 parallel**
- 攻擊方：找 bug / 安全漏洞 / race / 邊界條件
- 防守方：驗證設計合理性 / 架構一致性 / API 邊界
- 檔案體質：過大檔案 / SRP 違反 / 職責不清

輪詢 `/codex:status` → `/codex:result <job-id>` 讀回 4 份輸出。

Focus text 越具體越好（指定檔案 / 具體風險點 / 設計疑問）。全域 CLAUDE.md 載明 Skill 設計意圖與 companion script 啟動路徑。

### Review 問題彙整

兩輪跑完後，提交所有問題項目的彙整表格，每個項目必須包含：

| 欄位 | 說明 |
|------|------|
| 嚴重性信心評分 | 對該問題確實是 bug / 設計缺陷的信心程度 |
| 關聯度 | 與當前開發階段的相關程度 |
| 複雜度 | 修復所需的工作量 |

優先處理原則（聯集，非交集）：
- **高關聯**：與當前 Phase 直接相關的問題
- **高信心**：確定是真正問題而非誤報的項目
- **低複雜**：修復成本低、可快速解決的項目

只有低關聯 + 中高複雜可以延後，其他統一優先處理。需要討論的項目先討論完再修。當下不修的問題建立 `gh issue` 追蹤。

### Issue 管理

**Labels — 兩個維度**

| 維度 | Labels | 規則 |
|------|--------|------|
| Type（必選一，互斥） | `bug` `feature` `refactor` `perf` `test` `chore` | 每個 issue 恰好一個 |
| Scope（選填，可多選） | `daemon` `spa` `electron` | 跨元件的 issue 標多個 |

**Milestones — 管時程**

- 活躍開發的 phase 建 milestone（如 `Phase 5b`），完成後 close
- 其餘放 `Backlog`，開工時再移入對應 milestone
- 不回溯建已完成 phase 的 milestone
