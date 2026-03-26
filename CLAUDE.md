# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 專案概述

**tmux-box** (v1.0.0-alpha.20) — tmux session 的遠端管理工具，含 Go daemon + React SPA + Electron shell。支援 Terminal、Stream（Claude Code `-p` 串流）、JSONL 三種模式。

- Repo: `git@github.com:wake/tmux-box.git`
- 主分支: `main`（v0 備份在 `v0` 分支）
- 版本: `VERSION` 檔案為 SOT，每個 PR bump

## 開發環境

- **Package manager**: pnpm（不是 npm）
- **Daemon**: `100.64.0.2:7860`（Go binary `bin/tbox`）
- **SPA**: `100.64.0.2:5174`（`spa/`）
- **測試**: `cd spa && npx vitest run`
- **Lint**: `cd spa && pnpm run lint`
- **Build**: `cd spa && pnpm run build`

## 技術棧

- **Daemon**: Go / net/http / gorilla/websocket / creack/pty / modernc.org/sqlite
- **SPA**: React 19 / Vite 8 / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons / xterm.js 6

## 開發流程

- **絕對不能直推 main**，即使 hotfix 也必須走 PR + review
- TDD：先寫測試再實作
- 每個 task 獨立 commit
- 圖示統一使用 Phosphor Icons
- 每個 PR merge 時 bump `VERSION`，同步更新 `CHANGELOG.md`

### PR Review 兩輪制

**第一輪：`code-review:code-review` skill**
- 標準化 code review（CLAUDE.md 合規、bug scan、git history、PR comments、code comments）

**第二輪：3 個 parallel agent 正反方審查**
- 攻擊方：找 bug / 安全漏洞 / 邊界情況
- 防守方：驗證設計合理性 / 架構一致性
- 檔案大小審查：偵測過大檔案、職責不清

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

符合以上任一條件即優先處理。需要討論的項目先討論完再修。當下不修的問題建立 `gh issue` 追蹤。
